/*
Copyright 2025.

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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/conditions"
)

const (
	// Wildcard TLS secret configuration
	WildcardTLSSecretName      = "nic-wildcard-tls"
	WildcardTLSSecretNamespace = "envoy-gateway-system"

	// Event reasons
	EventReasonTLSSecretNotFound = "TLSSecretNotFound"
	EventReasonTLSConfigured     = "TLSConfigured"
	EventReasonTLSDisabled       = "TLSDisabled"
)

// TLSReconciler handles TLS certificate management for NicApp resources
type TLSReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// ReconcileTLS validates TLS configuration for a NicApp
func (r *TLSReconciler) ReconcileTLS(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	// Check if TLS is enabled
	if nicApp.Spec.TLS == nil || !nicApp.Spec.TLS.Enabled {
		logger.Info("TLS is disabled for NicApp", "nicapp", nicApp.Name)
		r.Recorder.Event(nicApp, corev1.EventTypeNormal, EventReasonTLSDisabled,
			"TLS is disabled for this application")
		conditions.SetCondition(nicApp, appsv1.ConditionTypeTLSReady, metav1.ConditionTrue,
			EventReasonTLSDisabled, "TLS is not enabled")
		return nil
	}

	// Determine TLS mode (wildcard is default)
	tlsMode := "wildcard"
	if nicApp.Spec.TLS.Mode != "" {
		tlsMode = nicApp.Spec.TLS.Mode
	}

	logger.Info("Reconciling TLS", "mode", tlsMode, "hostname", nicApp.Spec.Hostname)

	switch tlsMode {
	case "wildcard":
		return r.reconcileWildcardMode(ctx, nicApp)
	case "perHost":
		// TODO: Implement per-host certificate mode with cert-manager Certificate resources
		logger.Info("Per-host TLS mode not yet implemented, falling back to wildcard")
		return r.reconcileWildcardMode(ctx, nicApp)
	default:
		err := fmt.Errorf("unsupported TLS mode: %s", tlsMode)
		logger.Error(err, "Invalid TLS mode")
		conditions.SetCondition(nicApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"InvalidMode", err.Error())
		return err
	}
}

// reconcileWildcardMode validates that the wildcard TLS secret exists
func (r *TLSReconciler) reconcileWildcardMode(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	// Verify wildcard secret exists
	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Name:      WildcardTLSSecretName,
		Namespace: WildcardTLSSecretNamespace,
	}

	if err := r.Client.Get(ctx, secretKey, secret); err != nil {
		if errors.IsNotFound(err) {
			errMsg := fmt.Sprintf("Wildcard TLS secret %s not found in namespace %s",
				WildcardTLSSecretName, WildcardTLSSecretNamespace)
			logger.Error(err, errMsg)
			r.Recorder.Event(nicApp, corev1.EventTypeWarning, EventReasonTLSSecretNotFound, errMsg)
			conditions.SetCondition(nicApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				EventReasonTLSSecretNotFound, errMsg)
			return fmt.Errorf("%s", errMsg)
		}
		logger.Error(err, "Failed to get wildcard TLS secret")
		conditions.SetCondition(nicApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"SecretAccessError", fmt.Sprintf("Failed to access TLS secret: %v", err))
		return err
	}

	// Validate secret has required keys
	if err := r.validateTLSSecret(secret); err != nil {
		logger.Error(err, "Wildcard TLS secret validation failed")
		r.Recorder.Event(nicApp, corev1.EventTypeWarning, "InvalidTLSSecret", err.Error())
		conditions.SetCondition(nicApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"InvalidSecret", err.Error())
		return err
	}

	logger.Info("Wildcard TLS secret validated successfully")
	r.Recorder.Event(nicApp, corev1.EventTypeNormal, EventReasonTLSConfigured,
		fmt.Sprintf("Using wildcard TLS certificate from secret %s", WildcardTLSSecretName))

	conditions.SetCondition(nicApp, appsv1.ConditionTypeTLSReady, metav1.ConditionTrue,
		EventReasonTLSConfigured, "TLS configured with wildcard certificate")

	return nil
}

// validateTLSSecret checks if the secret has the required TLS keys
func (r *TLSReconciler) validateTLSSecret(secret *corev1.Secret) error {
	if secret.Type != corev1.SecretTypeTLS {
		return fmt.Errorf("secret type must be kubernetes.io/tls, got %s", secret.Type)
	}

	if _, ok := secret.Data[corev1.TLSCertKey]; !ok {
		return fmt.Errorf("secret missing required key: %s", corev1.TLSCertKey)
	}

	if _, ok := secret.Data[corev1.TLSPrivateKeyKey]; !ok {
		return fmt.Errorf("secret missing required key: %s", corev1.TLSPrivateKeyKey)
	}

	return nil
}

// GetTLSSecretReference returns the TLS secret name and namespace for a NicApp
func (r *TLSReconciler) GetTLSSecretReference(nicApp *appsv1.NicApp) (name, namespace string) {
	// Currently only wildcard mode is implemented
	// Future enhancement: support per-host certificate mode
	if nicApp.Spec.TLS != nil && nicApp.Spec.TLS.Mode == "perHost" {
		// TODO: Return per-host certificate secret name
		// For now, fall back to wildcard
	}

	return WildcardTLSSecretName, WildcardTLSSecretNamespace
}
