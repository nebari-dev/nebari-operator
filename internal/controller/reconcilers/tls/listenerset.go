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

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Gateway API standard-channel ListenerSet condition types (gateway.networking.k8s.io/v1).
const (
	listenerSetConditionAccepted   = "Accepted"
	listenerSetConditionProgrammed = "Programmed"
)

// reconcileListenerSet creates or updates the per-app ListenerSet (ADR-0011
// Option 2). The ListenerSet lives in the NebariApp's own namespace, attaches to
// the shared Gateway via spec.parentRef, and carries a single HTTPS Terminate
// listener whose certificate secret is co-located in that same namespace (so no
// ReferenceGrant is needed). It is owner-referenced to the NebariApp, so it is
// garbage-collected with the app.
//
// This is always reconciled, even on an Envoy Gateway that does not yet support
// ListenerSet: there it simply never reaches Programmed=True and the caller keeps
// serving via the legacy shared-Gateway listener (see reconcileTLS phase logic).
func (r *TLSReconciler) reconcileListenerSet(ctx context.Context, nebariApp *appsv1.NebariApp, secretName string) error {
	logger := log.FromContext(ctx)

	parentGatewayName := naming.GatewayName(nebariApp)
	listenerName := naming.ListenerName(nebariApp)
	hostname := gatewayv1.Hostname(nebariApp.Spec.Hostname)
	tlsMode := gatewayv1.TLSModeTerminate
	fromSame := gatewayv1.NamespacesFromSame
	parentGroup := gatewayv1.Group(gatewayv1.GroupName)
	parentKind := gatewayv1.Kind("Gateway")
	parentNS := gatewayv1.Namespace(constants.GatewayNamespace)

	ls := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.ListenerSetName(nebariApp),
			Namespace: nebariApp.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, ls, func() error {
		if ls.Labels == nil {
			ls.Labels = make(map[string]string)
		}
		ls.Labels["app.kubernetes.io/managed-by"] = "nebari-operator"
		ls.Labels["nebari.dev/nebariapp-name"] = nebariApp.Name

		ls.Spec.ParentRef = gatewayv1.ParentGatewayReference{
			Group:     &parentGroup,
			Kind:      &parentKind,
			Name:      gatewayv1.ObjectName(parentGatewayName),
			Namespace: &parentNS,
		}
		ls.Spec.Listeners = []gatewayv1.ListenerEntry{
			{
				Name:     gatewayv1.SectionName(listenerName),
				Hostname: &hostname,
				Port:     443,
				Protocol: gatewayv1.HTTPSProtocolType,
				TLS: &gatewayv1.ListenerTLSConfig{
					Mode: &tlsMode,
					// No namespace on the ref: the secret is co-located in the
					// ListenerSet's own namespace, so it resolves without a
					// ReferenceGrant.
					CertificateRefs: []gatewayv1.SecretObjectReference{
						{Name: gatewayv1.ObjectName(secretName)},
					},
				},
				AllowedRoutes: &gatewayv1.AllowedRoutes{
					Namespaces: &gatewayv1.RouteNamespaces{
						From: &fromSame,
					},
				},
			},
		}

		// Same namespace as the NebariApp, so a real owner reference works and GC
		// removes the ListenerSet with the app.
		return controllerutil.SetControllerReference(nebariApp, ls, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to create or update ListenerSet: %w", err)
	}

	logger.Info("ListenerSet reconciled",
		"listenerSet", ls.Name, "namespace", nebariApp.Namespace,
		"parentGateway", parentGatewayName, "operation", op)
	if op == controllerutil.OperationResultCreated {
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonGatewayListenerAdded,
			fmt.Sprintf("Created ListenerSet %s/%s attached to Gateway %s/%s",
				nebariApp.Namespace, ls.Name, constants.GatewayNamespace, parentGatewayName))
	}
	return nil
}

// isListenerSetProgrammed reports whether this NebariApp's ListenerSet has been
// accepted and programmed by the gateway controller. The staged migration only
// cuts routes over to the ListenerSet (and tears down the legacy shared-Gateway
// listener) once both conditions are True. On an Envoy Gateway that does not
// reconcile ListenerSet (e.g. pre-v1.8), the conditions never flip and this
// returns false, so the operator keeps serving via the legacy path.
//
// A missing ListenerSet returns (false, nil): not yet created, treat as not
// programmed rather than an error.
func (r *TLSReconciler) isListenerSetProgrammed(ctx context.Context, nebariApp *appsv1.NebariApp) (bool, error) {
	ls := &gatewayv1.ListenerSet{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      naming.ListenerSetName(nebariApp),
		Namespace: nebariApp.Namespace,
	}, ls); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get ListenerSet for status check: %w", err)
	}

	accepted, programmed := false, false
	for _, c := range ls.Status.Conditions {
		switch c.Type {
		case listenerSetConditionAccepted:
			accepted = c.Status == metav1.ConditionTrue
		case listenerSetConditionProgrammed:
			programmed = c.Status == metav1.ConditionTrue
		}
	}
	return accepted && programmed, nil
}

// reconcileTLSAttachment reconciles the per-app ListenerSet and decides which
// listener actually serves this app's HTTPS traffic, returning whether the app
// has cut over to the ListenerSet.
//
// Staged, status-gated migration (ADR-0011 Option 2):
//   - The ListenerSet is always (re)created.
//   - Until it reports Accepted+Programmed, the legacy per-app listener on the
//     shared Gateway is kept in place, so TLS keeps working on an Envoy Gateway
//     that does not yet reconcile ListenerSet (pre-v1.8) or has not programmed it
//     yet.
//   - Once Programmed, the shared-Gateway listener is removed and traffic is
//     served by the ListenerSet. Cutover is per-NebariApp and driven by status,
//     with no user-facing strategy flag.
//
// secretName is the TLS secret name; it is resolved in the app namespace for the
// ListenerSet and in the Gateway namespace for the legacy shared listener.
func (r *TLSReconciler) reconcileTLSAttachment(ctx context.Context, nebariApp *appsv1.NebariApp, secretName string) (bool, error) {
	logger := log.FromContext(ctx)

	if err := r.reconcileListenerSet(ctx, nebariApp, secretName); err != nil {
		return false, err
	}

	programmed, err := r.isListenerSetProgrammed(ctx, nebariApp)
	if err != nil {
		return false, err
	}

	if programmed {
		// Cut over: the ListenerSet is serving, so retire the legacy shared-Gateway
		// listener. removeGatewayListener is idempotent (no-op once removed).
		if err := r.removeGatewayListener(ctx, nebariApp); err != nil {
			return false, err
		}
		logger.V(1).Info("Serving via per-app ListenerSet; legacy Gateway listener retired",
			"listenerSet", naming.ListenerSetName(nebariApp))
		return true, nil
	}

	// Not yet programmed: keep the legacy shared-Gateway listener serving.
	if err := r.reconcileGatewayListener(ctx, nebariApp, secretName); err != nil {
		return false, err
	}
	return false, nil
}
