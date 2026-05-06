/*
Copyright 2026, OpenTeams.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tls

import (
	"context"
	"fmt"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TLSReconciler reconciles TLS resources (cert-manager Certificates and Gateway listeners)
// for NebariApp resources.
type TLSReconciler struct {
	Client            client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	ClusterIssuerName string
}

// TLSResult contains the outcome of a TLS reconciliation.
type TLSResult struct {
	// ListenerName is the name of the per-app HTTPS listener on the Gateway.
	ListenerName string

	// SecretName is the TLS secret the per-app listener references.
	// On the cert-manager path it is the cert-manager-issued secret;
	// on the user-provided-secret path it is the secret named in routing.tls.secretName.
	SecretName string

	// CertReady indicates whether the listener's TLS secret is ready for use.
	// On the cert-manager path it reflects the Certificate's Ready=True condition;
	// on the user-provided-secret path it reflects whether the named secret exists
	// and is of type kubernetes.io/tls.
	CertReady bool
}

// isTLSEnabled returns true if TLS is enabled for the NebariApp.
// TLS defaults to enabled unless explicitly set to false.
// When routing is nil (externally managed routing), TLS is considered disabled
// since the operator won't create HTTPRoutes that would use the certificate.
func isTLSEnabled(nebariApp *appsv1.NebariApp) bool {
	if nebariApp.Spec.Routing == nil {
		return false
	}
	if nebariApp.Spec.Routing.TLS == nil {
		return true
	}
	if nebariApp.Spec.Routing.TLS.Enabled == nil {
		return true
	}
	return *nebariApp.Spec.Routing.TLS.Enabled
}

// ReconcileTLS handles TLS configuration for a NebariApp.
// When routing.tls.secretName is set, the user-provided secret path is taken:
// any owned cert-manager Certificate is cleaned up, the Gateway listener is
// pointed at the named secret, and TLSReady reflects the secret's validity.
// When secretName is empty, the cert-manager path is taken as before.
func (r *TLSReconciler) ReconcileTLS(ctx context.Context, nebariApp *appsv1.NebariApp) (*TLSResult, error) {
	logger := log.FromContext(ctx)

	if !isTLSEnabled(nebariApp) {
		logger.Info("TLS not enabled, skipping TLS reconciliation")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"TLSDisabled", "TLS is not enabled for this app")
		return nil, nil
	}

	userSecret := ""
	if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil {
		userSecret = nebariApp.Spec.Routing.TLS.SecretName
	}

	if userSecret != "" {
		return r.reconcileUserProvidedTLS(ctx, nebariApp, userSecret)
	}

	if r.ClusterIssuerName == "" {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"ClusterIssuerNotConfigured", "No ClusterIssuer configured for TLS certificate management")
		return nil, fmt.Errorf("ClusterIssuerName is not configured; set TLS_CLUSTER_ISSUER_NAME environment variable")
	}

	logger.Info("Reconciling TLS",
		"hostname", nebariApp.Spec.Hostname,
		"clusterIssuer", r.ClusterIssuerName,
		"gateway", naming.GatewayName(nebariApp))

	if err := r.reconcileCertificate(ctx, nebariApp); err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"CertificateFailed", fmt.Sprintf("Failed to reconcile Certificate: %v", err))
		return nil, err
	}

	if err := r.reconcileGatewayListener(ctx, nebariApp, naming.CertificateSecretName(nebariApp)); err != nil {
		if containsListenerConflict(err) {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				appsv1.ReasonGatewayListenerConflict,
				fmt.Sprintf("Gateway listener conflict: Multiple NebariApps cannot share hostname %s with per-app TLS. "+
					"Set routing.tls.enabled=false to use shared wildcard listener, or use unique hostnames.",
					nebariApp.Spec.Hostname))
		} else {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				"GatewayListenerFailed", fmt.Sprintf("Failed to reconcile Gateway listener: %v", err))
		}
		return nil, err
	}

	certReady, err := r.isCertificateReady(ctx, nebariApp)
	if err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"CertificateCheckFailed", fmt.Sprintf("Failed to check Certificate readiness: %v", err))
		return nil, err
	}

	if certReady {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionTrue,
			"TLSConfigured", "TLS certificate is ready and Gateway listener is configured")
	} else {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			appsv1.ReasonCertificateNotReady, "Waiting for cert-manager Certificate to become ready")
	}

	return &TLSResult{
		ListenerName: naming.ListenerName(nebariApp),
		SecretName:   naming.CertificateSecretName(nebariApp),
		CertReady:    certReady,
	}, nil
}

// reconcileUserProvidedTLS handles the path where routing.tls.secretName references
// a pre-existing Kubernetes TLS secret in the Gateway namespace.
func (r *TLSReconciler) reconcileUserProvidedTLS(ctx context.Context, nebariApp *appsv1.NebariApp, secretName string) (*TLSResult, error) {
	logger := log.FromContext(ctx)
	logger.Info("Using user-provided TLS secret",
		"secret", secretName,
		"namespace", constants.GatewayNamespace,
		"hostname", nebariApp.Spec.Hostname)

	if err := r.cleanupOwnedCertificate(ctx, nebariApp); err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"CertificateCleanupFailed", fmt.Sprintf("Failed to clean up owned Certificate during migration: %v", err))
		return nil, err
	}

	if err := r.reconcileGatewayListener(ctx, nebariApp, secretName); err != nil {
		if containsListenerConflict(err) {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				appsv1.ReasonGatewayListenerConflict,
				fmt.Sprintf("Gateway listener conflict: Multiple NebariApps cannot share hostname %s with per-app TLS. "+
					"Set routing.tls.enabled=false to use shared wildcard listener, or use unique hostnames.",
					nebariApp.Spec.Hostname))
		} else {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				"GatewayListenerFailed", fmt.Sprintf("Failed to reconcile Gateway listener: %v", err))
		}
		return nil, err
	}

	status, reason, msg := r.checkUserProvidedSecret(ctx, secretName)
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, status, reason, msg)

	switch reason {
	case appsv1.ReasonUserProvidedSecretReady:
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonUserProvidedSecretInUse, msg)
	case appsv1.ReasonUserProvidedSecretNotFound:
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonUserProvidedSecretNotFound, msg)
	case appsv1.ReasonUserProvidedSecretInvalidType:
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonUserProvidedSecretInvalid, msg)
	case appsv1.ReasonUserProvidedSecretCheckFailed:
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonUserProvidedSecretCheckFailed, msg)
	}

	return &TLSResult{
		ListenerName: naming.ListenerName(nebariApp),
		SecretName:   secretName,
		CertReady:    status == metav1.ConditionTrue,
	}, nil
}

// CleanupTLS removes TLS resources for a NebariApp.
// It removes the per-app listener from the Gateway and deletes the Certificate.
// Both operations are attempted even if one fails, to minimize orphaned resources.
func (r *TLSReconciler) CleanupTLS(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)
	var errs []error

	// Step 1: Remove the per-app listener from the Gateway
	if err := r.removeGatewayListener(ctx, nebariApp); err != nil {
		logger.Error(err, "Failed to remove Gateway listener during cleanup")
		errs = append(errs, err)
	}

	// Step 2: Delete the Certificate from the Gateway namespace
	if err := r.deleteCertificate(ctx, nebariApp); err != nil {
		logger.Error(err, "Failed to delete Certificate during cleanup")
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("TLS cleanup encountered %d error(s): %v", len(errs), errs)
	}
	return nil
}

// reconcileCertificate creates or updates a cert-manager Certificate for the NebariApp.
func (r *TLSReconciler) reconcileCertificate(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	certName := naming.CertificateName(nebariApp)
	secretName := naming.CertificateSecretName(nebariApp)

	cert := &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      certName,
			Namespace: constants.GatewayNamespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cert, func() error {
		// Set labels (cannot use SetControllerReference since Certificate is cross-namespace)
		if cert.Labels == nil {
			cert.Labels = make(map[string]string)
		}
		cert.Labels["app.kubernetes.io/managed-by"] = "nebari-operator"
		cert.Labels["nebari.dev/nebariapp-name"] = nebariApp.Name
		cert.Labels["nebari.dev/nebariapp-namespace"] = nebariApp.Namespace

		cert.Spec = certmanagerv1.CertificateSpec{
			SecretName: secretName,
			DNSNames:   []string{nebariApp.Spec.Hostname},
			IssuerRef: cmmeta.ObjectReference{
				Name: r.ClusterIssuerName,
				Kind: "ClusterIssuer",
			},
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create or update Certificate: %w", err)
	}

	logger.Info("Certificate reconciled", "name", certName, "namespace", constants.GatewayNamespace, "operation", op)

	switch op {
	case controllerutil.OperationResultCreated:
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateCreated,
			fmt.Sprintf("Created cert-manager Certificate %s/%s", constants.GatewayNamespace, certName))
	case controllerutil.OperationResultUpdated:
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateUpdated,
			fmt.Sprintf("Updated cert-manager Certificate %s/%s", constants.GatewayNamespace, certName))
	}

	return nil
}

// reconcileGatewayListener adds or updates a per-app HTTPS listener on the shared Gateway.
// It uses a Get→upsert-in-slice→Update pattern so that concurrent reconcilers operating
// on the same Gateway each own exactly one named listener without rewriting the whole spec.
func (r *TLSReconciler) reconcileGatewayListener(ctx context.Context, nebariApp *appsv1.NebariApp, secretName string) error {
	logger := log.FromContext(ctx)

	gatewayName := naming.GatewayName(nebariApp)
	listenerName := naming.ListenerName(nebariApp)
	hostname := gatewayv1.Hostname(nebariApp.Spec.Hostname)
	tlsMode := gatewayv1.TLSModeTerminate
	fromSelector := gatewayv1.NamespacesFromSelector
	secretNS := gatewayv1.Namespace(constants.GatewayNamespace)

	listener := gatewayv1.Listener{
		Name:     gatewayv1.SectionName(listenerName),
		Hostname: &hostname,
		Port:     443,
		Protocol: gatewayv1.HTTPSProtocolType,
		TLS: &gatewayv1.ListenerTLSConfig{
			Mode: &tlsMode,
			CertificateRefs: []gatewayv1.SecretObjectReference{
				{
					Name:      gatewayv1.ObjectName(secretName),
					Namespace: &secretNS,
				},
			},
		},
		AllowedRoutes: &gatewayv1.AllowedRoutes{
			Namespaces: &gatewayv1.RouteNamespaces{
				From: &fromSelector,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						// kubernetes.io/metadata.name is automatically set by Kubernetes (v1.21+)
						"kubernetes.io/metadata.name": nebariApp.Namespace,
					},
				},
			},
		},
	}

	// Get the existing Gateway. We refuse to create a bare Gateway here —
	// the Gateway must already exist and be managed by the infrastructure layer.
	gateway := &gatewayv1.Gateway{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      gatewayName,
		Namespace: constants.GatewayNamespace,
	}, gateway); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("gateway %s not found in namespace %s", gatewayName, constants.GatewayNamespace)
		}
		return fmt.Errorf("failed to get Gateway: %w", err)
	}

	// Upsert the listener: replace in-place if a listener with the same name
	// already exists, otherwise append. This preserves all other listeners.
	updated := false
	for i, l := range gateway.Spec.Listeners {
		if l.Name == gatewayv1.SectionName(listenerName) {
			gateway.Spec.Listeners[i] = listener
			updated = true
			break
		}
	}
	if !updated {
		gateway.Spec.Listeners = append(gateway.Spec.Listeners, listener)
	}

	if err := r.Client.Update(ctx, gateway); err != nil {
		// Detect Gateway listener conflicts (duplicate port+protocol+hostname)
		if apierrors.IsInvalid(err) && containsListenerConflict(err) {
			msg := fmt.Sprintf("Gateway listener conflict detected. Multiple NebariApps cannot use the same hostname (%s) with per-app TLS listeners. "+
				"Solutions: 1) Set routing.tls.enabled=false on all apps sharing this hostname to use the shared wildcard HTTPS listener, "+
				"or 2) Use different hostnames for each app. See https://gateway-api.sigs.k8s.io for Gateway API constraints.",
				nebariApp.Spec.Hostname)
			r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonGatewayListenerConflict, msg)
			return fmt.Errorf("%s: %w", msg, err)
		}
		return fmt.Errorf("failed to update Gateway listener: %w", err)
	}

	logger.Info("Applied Gateway listener", "listener", listenerName, "gateway", gatewayName)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonGatewayListenerAdded,
		fmt.Sprintf("Applied HTTPS listener %s to Gateway %s", listenerName, gatewayName))

	return nil
}

// containsListenerConflict checks if the error message indicates a Gateway listener conflict.
// Gateway API validates that port + protocol + hostname combinations are unique.
func containsListenerConflict(err error) bool {
	if err == nil {
		return false
	}
	errMsg := err.Error()
	// Check for the specific validation error from Gateway API
	return contains(errMsg, "Combination of port, protocol and hostname must be unique") ||
		contains(errMsg, "listener") && contains(errMsg, "unique")
}

// contains is a case-insensitive substring check.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if matchesAt(s, substr, i) {
			return true
		}
	}
	return false
}

func matchesAt(s, substr string, offset int) bool {
	for i := 0; i < len(substr); i++ {
		c1 := s[offset+i]
		c2 := substr[i]
		if c1 != c2 && toLower(c1) != toLower(c2) {
			return false
		}
	}
	return true
}

func toLower(c byte) byte {
	if 'A' <= c && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// isCertificateReady checks whether the cert-manager Certificate has a Ready=True condition.
// Returns (ready, error) so that transient API failures are distinguished from "cert not ready".
func (r *TLSReconciler) isCertificateReady(ctx context.Context, nebariApp *appsv1.NebariApp) (bool, error) {
	certName := naming.CertificateName(nebariApp)
	cert := &certmanagerv1.Certificate{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      certName,
		Namespace: constants.GatewayNamespace,
	}, cert); err != nil {
		return false, fmt.Errorf("failed to get Certificate for readiness check: %w", err)
	}

	for _, c := range cert.Status.Conditions {
		if c.Type == certmanagerv1.CertificateConditionReady && c.Status == cmmeta.ConditionTrue {
			return true, nil
		}
	}
	return false, nil
}

// removeGatewayListener removes the per-app listener from the Gateway.
func (r *TLSReconciler) removeGatewayListener(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	gatewayName := naming.GatewayName(nebariApp)
	listenerName := naming.ListenerName(nebariApp)

	// Get the Gateway
	gateway := &gatewayv1.Gateway{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      gatewayName,
		Namespace: constants.GatewayNamespace,
	}, gateway); err != nil {
		if apierrors.IsNotFound(err) {
			// Gateway already gone, nothing to do
			return nil
		}
		return fmt.Errorf("failed to get Gateway for cleanup: %w", err)
	}

	// Filter out the per-app listener
	filtered := make([]gatewayv1.Listener, 0, len(gateway.Spec.Listeners))
	listenerRemoved := false
	for _, l := range gateway.Spec.Listeners {
		if string(l.Name) == listenerName {
			listenerRemoved = true
			continue
		}
		filtered = append(filtered, l)
	}

	if !listenerRemoved {
		// Listener was already removed
		return nil
	}

	gateway.Spec.Listeners = filtered
	if err := r.Client.Update(ctx, gateway); err != nil {
		// Conflict errors are expected when multiple NebariApps clean up the same Gateway concurrently.
		if apierrors.IsConflict(err) {
			logger.V(1).Info("Gateway update conflict during cleanup, will retry", "gateway", gatewayName)
			return fmt.Errorf("gateway update conflict during cleanup (will retry): %w", err)
		}
		return fmt.Errorf("failed to update Gateway to remove listener: %w", err)
	}

	logger.Info("Removed Gateway listener", "listener", listenerName, "gateway", gatewayName)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonGatewayListenerRemoved,
		fmt.Sprintf("Removed HTTPS listener %s from Gateway %s", listenerName, gatewayName))

	return nil
}

// cleanupOwnedCertificate deletes the cert-manager Certificate for this NebariApp
// if it exists and is labeled as owned by this NebariApp. This is used when a
// NebariApp switches from cert-manager to a user-provided TLS secret via
// routing.tls.secretName. The function is idempotent: missing Certificates and
// Certificates with mismatched ownership labels are left alone.
func (r *TLSReconciler) cleanupOwnedCertificate(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)
	certName := naming.CertificateName(nebariApp)

	cert := &certmanagerv1.Certificate{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      certName,
		Namespace: constants.GatewayNamespace,
	}, cert); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get Certificate for cleanup check: %w", err)
	}

	if cert.Labels["nebari.dev/nebariapp-name"] != nebariApp.Name ||
		cert.Labels["nebari.dev/nebariapp-namespace"] != nebariApp.Namespace {
		logger.V(1).Info("Certificate exists with mismatched ownership labels, leaving it alone",
			"name", certName, "namespace", constants.GatewayNamespace)
		return nil
	}

	if err := client.IgnoreNotFound(r.Client.Delete(ctx, cert)); err != nil {
		return fmt.Errorf("failed to delete owned Certificate during migration: %w", err)
	}

	logger.Info("Deleted owned Certificate during migration to user-provided secret",
		"name", certName, "namespace", constants.GatewayNamespace)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateDeleted,
		fmt.Sprintf("Deleted cert-manager Certificate %s/%s after switch to user-provided secret", constants.GatewayNamespace, certName))
	return nil
}

// checkUserProvidedSecret inspects a user-supplied TLS secret in the Gateway
// namespace and returns a condition (status, reason, message) tuple describing
// its readiness. The check is best-effort: a missing or malformed secret yields
// ConditionFalse but does not error, so the caller can still proceed to attach
// the listener.
func (r *TLSReconciler) checkUserProvidedSecret(ctx context.Context, secretName string) (metav1.ConditionStatus, string, string) {
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: constants.GatewayNamespace,
	}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return metav1.ConditionFalse,
				appsv1.ReasonUserProvidedSecretNotFound,
				fmt.Sprintf("TLS secret %s/%s not found; create it and the listener will pick it up",
					constants.GatewayNamespace, secretName)
		}
		return metav1.ConditionFalse,
			appsv1.ReasonUserProvidedSecretCheckFailed,
			fmt.Sprintf("failed to check TLS secret %s/%s: %v", constants.GatewayNamespace, secretName, err)
	}

	if secret.Type != corev1.SecretTypeTLS {
		return metav1.ConditionFalse,
			appsv1.ReasonUserProvidedSecretInvalidType,
			fmt.Sprintf("TLS secret %s/%s is type %s, expected kubernetes.io/tls",
				constants.GatewayNamespace, secretName, secret.Type)
	}

	return metav1.ConditionTrue,
		appsv1.ReasonUserProvidedSecretReady,
		fmt.Sprintf("using pre-provisioned TLS secret %s/%s", constants.GatewayNamespace, secretName)
}

// deleteCertificate removes the cert-manager Certificate from the Gateway namespace.
func (r *TLSReconciler) deleteCertificate(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	certName := naming.CertificateName(nebariApp)
	cert := &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      certName,
			Namespace: constants.GatewayNamespace,
		},
	}

	if err := client.IgnoreNotFound(r.Client.Delete(ctx, cert)); err != nil {
		return fmt.Errorf("failed to delete Certificate: %w", err)
	}

	logger.Info("Deleted Certificate", "name", certName, "namespace", constants.GatewayNamespace)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateDeleted,
		fmt.Sprintf("Deleted cert-manager Certificate %s/%s", constants.GatewayNamespace, certName))

	return nil
}
