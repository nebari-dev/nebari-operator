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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
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
// serving via the legacy shared-Gateway listener (see reconcileTLSAttachment /
// shouldCutOver).
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

// shouldCutOver decides whether this NebariApp's HTTPS traffic should be served
// by its per-app ListenerSet (ADR-0011 Option 2) rather than the legacy
// shared-Gateway listener. It is reason-aware, keying off the ListenerSet status
// the way Envoy Gateway actually reports it (validated on EG v1.8.2):
//
//   - Programmed=True (set-level): the ListenerSet is live. Cut over.
//   - The app's own listener reports Conflicted=True/HostnameConflict while its
//     refs still resolve: the ListenerSet is blocked only by our own legacy
//     listener holding the same (port, hostname). Cutting over removes that legacy
//     listener so the ListenerSet can leave the conflict and program. A ListenerSet
//     that merely claims a hostname already detaches that hostname's routes from
//     the shared Gateway, so holding the legacy listener does not keep serving in
//     the meantime, it only deadlocks the cutover.
//   - Anything else (no status yet, Accepted=False/NotAllowed, unresolved refs):
//     do NOT cut over, keep the legacy listener serving. NotAllowed in particular
//     means the Gateway refuses the attachment (e.g. spec.allowedListeners unset),
//     so the ListenerSet can never serve and removing the legacy listener would
//     strand the app.
//
// A missing ListenerSet returns (false, nil): treat as not-yet-created.
func (r *TLSReconciler) shouldCutOver(ctx context.Context, nebariApp *appsv1.NebariApp) (bool, error) {
	ls := &gatewayv1.ListenerSet{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      naming.ListenerSetName(nebariApp),
		Namespace: nebariApp.Namespace,
	}, ls); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get ListenerSet for cutover decision: %w", err)
	}

	// Fully programmed: the ListenerSet is serving, cut over unconditionally.
	if meta.IsStatusConditionTrue(ls.Status.Conditions, string(gatewayv1.ListenerSetConditionProgrammed)) {
		return true, nil
	}

	// Not programmed yet: cut over only on our own hostname conflict with refs
	// resolving (see docstring) — removing our legacy listener frees the tuple so
	// the ListenerSet can program. Every other state (NotAllowed, unresolved refs)
	// stays on legacy.
	//
	// NOTE: a HostnameConflict is indistinguishable by condition type/status/reason
	// from a conflict with a *peer* app's ListenerSet on the same hostname (only the
	// condition message names the culprit, validated on EG v1.8.2). Cutting over in
	// that peer case would strand this app, and unlike the legacy path it no longer
	// surfaces a conflict condition. Restoring a surfaced signal and discriminating
	// the peer case is left to the conflict-handling rework (#168), not parsed here.
	listenerName := gatewayv1.SectionName(naming.ListenerName(nebariApp))
	for _, l := range ls.Status.Listeners {
		if l.Name != listenerName {
			continue
		}
		conflict := meta.FindStatusCondition(l.Conditions, string(gatewayv1.ListenerConditionConflicted))
		hostnameConflict := conflict != nil && conflict.Status == metav1.ConditionTrue &&
			conflict.Reason == string(gatewayv1.ListenerReasonHostnameConflict)
		refsResolved := !meta.IsStatusConditionFalse(l.Conditions, string(gatewayv1.ListenerConditionResolvedRefs))
		return hostnameConflict && refsResolved, nil
	}

	return false, nil
}

// reconcileTLSAttachment reconciles the per-app ListenerSet and decides which
// listener serves this app's HTTPS traffic, returning whether it has cut over to
// the ListenerSet (ADR-0011 Option 2).
//
// The ListenerSet is always (re)created. Then, reason-aware (see shouldCutOver):
//   - If the app should cut over, the legacy shared-Gateway listener is removed so
//     the ListenerSet owns the (port, hostname) tuple, and routes are pointed at
//     the ListenerSet. removeGatewayListener is idempotent.
//   - Otherwise the legacy shared-Gateway listener is kept in place and serves,
//     and routes stay on it. This covers the brief pre-status window right after
//     creation and the genuinely-unsupported cluster (Gateway refusing the
//     attachment). Cutover is per-NebariApp and driven by status, with no
//     user-facing strategy flag.
//
// secretName is the TLS secret name; it is resolved in the app namespace for the
// ListenerSet and in the Gateway namespace for the legacy shared listener.
func (r *TLSReconciler) reconcileTLSAttachment(ctx context.Context, nebariApp *appsv1.NebariApp, secretName string) (bool, error) {
	logger := log.FromContext(ctx)

	if err := r.reconcileListenerSet(ctx, nebariApp, secretName); err != nil {
		return false, err
	}

	cutover, err := r.shouldCutOver(ctx, nebariApp)
	if err != nil {
		return false, err
	}

	if cutover {
		// removeGatewayListener is idempotent (no-op once removed / never created).
		if err := r.removeGatewayListener(ctx, nebariApp); err != nil {
			return false, err
		}
		logger.V(1).Info("Serving via per-app ListenerSet; legacy Gateway listener retired",
			"listenerSet", naming.ListenerSetName(nebariApp))
		return true, nil
	}

	// Keep the legacy shared-Gateway listener serving until the ListenerSet is
	// usable.
	if err := r.reconcileGatewayListener(ctx, nebariApp, secretName); err != nil {
		return false, err
	}
	return false, nil
}

// removeListenerSet deletes this NebariApp's per-app ListenerSet if present, so an
// app that moves off the ListenerSet path (e.g. switching to a user-provided TLS
// secret) does not leave a ListenerSet still claiming the hostname and detaching
// the route from the shared Gateway. Idempotent: a missing ListenerSet is a no-op.
func (r *TLSReconciler) removeListenerSet(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	ls := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.ListenerSetName(nebariApp),
			Namespace: nebariApp.Namespace,
		},
	}
	err := r.Client.Delete(ctx, ls)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete ListenerSet: %w", err)
	}
	log.FromContext(ctx).V(1).Info("Removed per-app ListenerSet",
		"listenerSet", ls.Name, "namespace", nebariApp.Namespace)
	return nil
}
