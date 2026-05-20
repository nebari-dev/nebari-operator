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

package routing

import (
	"context"
	"fmt"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

// streamingRequestTimeout disables Envoy's default 15s HTTP request timeout
// so SSE / long-poll / gRPC streams can hold connections open indefinitely.
const streamingRequestTimeout = "0s"

// streamingIdleTimeout caps idle connections at 5 minutes. Matches the
// downstream PR that motivated the design (openteams-ai/nebari.openteams.ai#12)
// and common practice for SSE backends.
const streamingIdleTimeout = "300s"

// StreamingReconciler manages the Envoy Gateway BackendTrafficPolicy that
// the operator emits when a NebariApp opts into long-lived connection support
// via routing.streaming: true.
type StreamingReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// Reconcile creates or removes the BackendTrafficPolicy for a NebariApp based on
// spec.routing.streaming. The policy targets every operator-owned HTTPRoute for
// the app (main plus public when present). Failures are surfaced on the
// StreamingReady condition but do not block the overall reconcile.
func (r *StreamingReconciler) Reconcile(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	if !streamingEnabled(nebariApp) {
		// Streaming disabled or routing absent — clean up any policy we previously owned.
		if err := r.deleteIfOwned(ctx, nebariApp); err != nil {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeStreamingReady, metav1.ConditionFalse,
				"CleanupFailed", fmt.Sprintf("Failed to delete BackendTrafficPolicy: %v", err))
			return err
		}
		// Don't surface a StreamingReady condition at all when the user hasn't asked for streaming —
		// it would be noise on every NebariApp.
		conditions.RemoveCondition(nebariApp, appsv1.ConditionTypeStreamingReady)
		return nil
	}

	policy := &egv1alpha1.BackendTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.BackendTrafficPolicyName(nebariApp),
			Namespace: nebariApp.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		// Refuse to take ownership of a foreign resource. ResourceVersion is
		// empty for newly initialized objects and non-empty when CreateOrUpdate
		// has fetched an existing one, so this branch only fires for an
		// existing policy that we don't already own.
		if policy.ResourceVersion != "" && !controllerOwnedBy(policy, nebariApp) {
			return errForeignPolicy
		}
		if err := controllerutil.SetControllerReference(nebariApp, policy, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}
		policy.Spec = r.buildSpec(nebariApp)
		return nil
	})

	if err != nil {
		switch {
		case err == errForeignPolicy:
			logger.Info("Refusing to take ownership of foreign BackendTrafficPolicy", "name", policy.Name)
			r.Recorder.Event(nebariApp, corev1.EventTypeWarning,
				appsv1.EventReasonBackendTrafficPolicyForeign,
				fmt.Sprintf("BackendTrafficPolicy %s exists but is not owned by this NebariApp", policy.Name))
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeStreamingReady, metav1.ConditionFalse,
				"ForeignPolicyExists",
				fmt.Sprintf("BackendTrafficPolicy %s exists in namespace %s but is not owned by this NebariApp", policy.Name, nebariApp.Namespace))
			return nil
		case meta.IsNoMatchError(err):
			logger.Info("Envoy Gateway BackendTrafficPolicy CRD not installed in the cluster")
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeStreamingReady, metav1.ConditionFalse,
				"CRDMissing",
				"Envoy Gateway BackendTrafficPolicy CRD is not installed in this cluster")
			return nil
		default:
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeStreamingReady, metav1.ConditionFalse,
				"ReconcileFailed", fmt.Sprintf("Failed to reconcile BackendTrafficPolicy: %v", err))
			return fmt.Errorf("failed to reconcile BackendTrafficPolicy: %w", err)
		}
	}

	switch op {
	case controllerutil.OperationResultCreated:
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal,
			appsv1.EventReasonBackendTrafficPolicyCreated,
			fmt.Sprintf("Created BackendTrafficPolicy %s", policy.Name))
	case controllerutil.OperationResultUpdated:
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal,
			appsv1.EventReasonBackendTrafficPolicyUpdated,
			fmt.Sprintf("Updated BackendTrafficPolicy %s", policy.Name))
	}

	logger.Info("BackendTrafficPolicy reconciled", "name", policy.Name, "operation", op)
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeStreamingReady, metav1.ConditionTrue,
		"PolicyReconciled", "Streaming BackendTrafficPolicy is in sync")
	return nil
}

// errForeignPolicy is sentinel-returned from the CreateOrUpdate mutation when
// a BackendTrafficPolicy with our chosen name already exists in the namespace
// but is not owner-referenced to this NebariApp.
var errForeignPolicy = fmt.Errorf("foreign BackendTrafficPolicy exists")

// streamingEnabled returns true when the NebariApp has opted into streaming
// timeouts. False when routing is absent or routing.streaming is unset/false.
func streamingEnabled(nebariApp *appsv1.NebariApp) bool {
	return nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.Streaming
}

// buildSpec constructs the BackendTrafficPolicy spec for a streaming-enabled
// NebariApp. The policy targets every operator-owned HTTPRoute for the app
// and applies fixed canned timeouts.
func (r *StreamingReconciler) buildSpec(nebariApp *appsv1.NebariApp) egv1alpha1.BackendTrafficPolicySpec {
	requestTimeout := gwapiv1.Duration(streamingRequestTimeout)
	idleTimeout := gwapiv1.Duration(streamingIdleTimeout)

	return egv1alpha1.BackendTrafficPolicySpec{
		PolicyTargetReferences: egv1alpha1.PolicyTargetReferences{
			TargetRefs: r.targetRefs(nebariApp),
		},
		ClusterSettings: egv1alpha1.ClusterSettings{
			Timeout: &egv1alpha1.Timeout{
				HTTP: &egv1alpha1.HTTPTimeout{
					RequestTimeout:        &requestTimeout,
					ConnectionIdleTimeout: &idleTimeout,
				},
			},
		},
	}
}

// targetRefs enumerates the HTTPRoutes the operator owns for this NebariApp.
// The main HTTPRoute is always present. The public HTTPRoute is included when
// the app declares publicRoutes (the routing reconciler creates a separate
// HTTPRoute for those so the SecurityPolicy doesn't apply).
func (r *StreamingReconciler) targetRefs(nebariApp *appsv1.NebariApp) []gwapiv1.LocalPolicyTargetReferenceWithSectionName {
	refs := []gwapiv1.LocalPolicyTargetReferenceWithSectionName{
		httpRouteRef(naming.HTTPRouteName(nebariApp)),
	}
	if nebariApp.Spec.Routing != nil && len(nebariApp.Spec.Routing.PublicRoutes) > 0 {
		refs = append(refs, httpRouteRef(naming.PublicHTTPRouteName(nebariApp)))
	}
	return refs
}

// httpRouteRef builds a LocalPolicyTargetReferenceWithSectionName pointing at
// an HTTPRoute by name.
func httpRouteRef(name string) gwapiv1.LocalPolicyTargetReferenceWithSectionName {
	return gwapiv1.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{
			Group: gwapiv1.Group("gateway.networking.k8s.io"),
			Kind:  gwapiv1.Kind("HTTPRoute"),
			Name:  gwapiv1.ObjectName(name),
		},
	}
}

// deleteIfOwned removes the BackendTrafficPolicy for this NebariApp if it
// exists and we own it. Idempotent — no error if it doesn't exist, refuses
// to delete if a foreign resource shares our chosen name.
func (r *StreamingReconciler) deleteIfOwned(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	policy := &egv1alpha1.BackendTrafficPolicy{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      naming.BackendTrafficPolicyName(nebariApp),
		Namespace: nebariApp.Namespace,
	}, policy)

	if apierrors.IsNotFound(err) {
		return nil
	}
	if meta.IsNoMatchError(err) {
		// CRD missing — nothing to clean up.
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get BackendTrafficPolicy: %w", err)
	}

	if !controllerOwnedBy(policy, nebariApp) {
		logger.Info("Not deleting foreign BackendTrafficPolicy", "name", policy.Name)
		return nil
	}

	if err := r.Client.Delete(ctx, policy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete BackendTrafficPolicy: %w", err)
	}

	r.Recorder.Event(nebariApp, corev1.EventTypeNormal,
		appsv1.EventReasonBackendTrafficPolicyDeleted,
		fmt.Sprintf("Deleted BackendTrafficPolicy %s (streaming disabled)", policy.Name))
	return nil
}

// controllerOwnedBy returns true when obj has a controller owner reference
// pointing at nebariApp.
func controllerOwnedBy(obj client.Object, nebariApp *appsv1.NebariApp) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.Controller != nil && *ref.Controller &&
			ref.UID == nebariApp.UID &&
			ref.Kind == "NebariApp" {
			return true
		}
	}
	return false
}
