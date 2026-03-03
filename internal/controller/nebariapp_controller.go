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

package controller

import (
	"context"
	"fmt"
	"time"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"

	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/routing"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/tls"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

// NebariAppReconciler reconciles a NebariApp object
type NebariAppReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	CoreReconciler    *core.CoreReconciler
	TLSReconciler     *tls.TLSReconciler
	RoutingReconciler *routing.RoutingReconciler
	AuthReconciler    *auth.AuthReconciler
}

// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.envoyproxy.io,resources=securitypolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NebariAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	logger.Info("Reconciling NebariApp", "name", req.Name, "namespace", req.Namespace)

	// Fetch the NebariApp instance
	nebariApp := &appsv1.NebariApp{}
	if err := r.Get(ctx, req.NamespacedName, nebariApp); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			logger.Info("NebariApp resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get NebariApp")
		return ctrl.Result{}, err
	}

	// Handle finalizer
	if nebariApp.DeletionTimestamp.IsZero() {
		// Object is not being deleted, ensure finalizer is present
		if !controllerutil.ContainsFinalizer(nebariApp, constants.NebariAppFinalizer) {
			controllerutil.AddFinalizer(nebariApp, constants.NebariAppFinalizer)
			if err := r.Update(ctx, nebariApp); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// Object is being deleted
		if controllerutil.ContainsFinalizer(nebariApp, constants.NebariAppFinalizer) {
			// Run cleanup logic
			if err := r.cleanup(ctx, nebariApp); err != nil {
				logger.Error(err, "Failed to cleanup resources")
				return ctrl.Result{}, err
			}

			// Remove finalizer
			controllerutil.RemoveFinalizer(nebariApp, constants.NebariAppFinalizer)
			if err := r.Update(ctx, nebariApp); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Initialize status if needed
	if nebariApp.Status.ObservedGeneration == 0 {
		nebariApp.Status.ObservedGeneration = nebariApp.Generation
		nebariApp.Status.Hostname = nebariApp.Spec.Hostname
	}

	// Set initial reconciling status only for new resources (no existing Ready condition).
	// Avoid setting Unknown on every reconcile, which would toggle True->Unknown->True
	// and update lastTransitionTime even when nothing changed.
	if conditions.GetCondition(nebariApp, appsv1.ConditionTypeReady) == nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionUnknown,
			appsv1.ReasonReconciling, "Reconciliation in progress")
	}

	// Validate namespace opt-in and NebariApp spec
	if err := r.CoreReconciler.ValidateSpec(ctx, nebariApp); err != nil {
		logger.Error(err, "Core validation failed")
		if err := r.Status().Update(ctx, nebariApp); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue after a longer delay for validation failures
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Core validation completed successfully (logged by CoreReconciler)

	// Reconcile TLS certificates and Gateway listener.
	// When TLSReconciler is nil (TLS_CLUSTER_ISSUER_NAME not set), no per-app Certificate
	// or Gateway listener is created. The routing reconciler will fall back to the static
	// "https" listener on the Gateway, which assumes a pre-existing shared HTTPS listener
	// with a wildcard certificate is already configured.
	var tlsListenerName string
	if r.TLSReconciler != nil {
		tlsResult, err := r.TLSReconciler.ReconcileTLS(ctx, nebariApp)
		if err != nil {
			logger.Error(err, "TLS reconciliation failed")
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
				appsv1.ReasonFailed, fmt.Sprintf("TLS reconciliation failed: %v", err))
			if err := r.Status().Update(ctx, nebariApp); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}
		if tlsResult != nil {
			tlsListenerName = tlsResult.ListenerName
			if !tlsResult.CertReady {
				logger.Info("TLS Certificate not ready yet, will requeue")
				// Save status so TLSReady=False is visible, then requeue.
				// The Certificate watch will also trigger re-reconciliation
				// when the cert becomes ready.
				nebariApp.Status.ObservedGeneration = nebariApp.Generation
				if err := r.Status().Update(ctx, nebariApp); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
			}
		}
		logger.Info("TLS reconciled successfully", "nebariapp", nebariApp.Name, "listenerName", tlsListenerName)
	}

	// Reconcile routing (HTTPRoute creation/update) if routing is configured
	if nebariApp.Spec.Routing != nil {
		if err := r.RoutingReconciler.ReconcileRouting(ctx, nebariApp, tlsListenerName); err != nil {
			logger.Error(err, "Routing reconciliation failed")
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
				appsv1.ReasonFailed, fmt.Sprintf("Routing reconciliation failed: %v", err))
			if err := r.Status().Update(ctx, nebariApp); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}
		logger.Info("Routing reconciled successfully", "nebariapp", nebariApp.Name)
	} else {
		// Routing not configured - set condition to indicate routing is not enabled
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionFalse,
			"RoutingNotConfigured", "Routing configuration not provided in spec")
		logger.Info("Routing not configured, skipping HTTPRoute reconciliation", "nebariapp", nebariApp.Name)
	}

	// Reconcile public route (unauthenticated paths) if auth has publicPaths
	if nebariApp.Spec.Auth != nil && nebariApp.Spec.Auth.Enabled && len(nebariApp.Spec.Auth.PublicPaths) > 0 {
		if err := r.RoutingReconciler.ReconcilePublicRoute(ctx, nebariApp, tlsListenerName); err != nil {
			logger.Error(err, "Public route reconciliation failed")
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
				appsv1.ReasonFailed, fmt.Sprintf("Public route reconciliation failed: %v", err))
			if err := r.Status().Update(ctx, nebariApp); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}
		logger.Info("Public route reconciled successfully", "nebariapp", nebariApp.Name)
	}

	// Reconcile authentication (SecurityPolicy creation/update) if auth is configured
	if err := r.AuthReconciler.ReconcileAuth(ctx, nebariApp); err != nil {
		logger.Error(err, "Auth reconciliation failed")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
			appsv1.ReasonFailed, fmt.Sprintf("Auth reconciliation failed: %v", err))
		if err := r.Status().Update(ctx, nebariApp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	logger.Info("Auth reconciled successfully", "nebariapp", nebariApp.Name)

	// Validation succeeded, set Ready condition to True
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionTrue,
		appsv1.ReasonReconcileSuccess, "NebariApp reconciled successfully")

	// Update observed generation
	nebariApp.Status.ObservedGeneration = nebariApp.Generation

	// Update status
	if err := r.Status().Update(ctx, nebariApp); err != nil {
		logger.Error(err, "Failed to update NebariApp status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled NebariApp")
	// Requeue after 1 minute for now (until full implementation)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// cleanup removes resources created by this NebariApp
func (r *NebariAppReconciler) cleanup(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := logf.FromContext(ctx)
	logger.Info("Cleaning up resources for NebariApp", "name", nebariApp.Name, "namespace", nebariApp.Namespace)

	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, "Cleanup", "Starting resource cleanup")

	// Cleanup in reverse pipeline order: Auth -> Routing -> TLS.
	// Auth depends on routing (SecurityPolicy references HTTPRoute), and
	// routing depends on TLS (HTTPRoute references the per-app listener).

	// Cleanup authentication resources (delete OIDC client if provisioned)
	if r.AuthReconciler != nil {
		if err := r.AuthReconciler.CleanupAuth(ctx, nebariApp); err != nil {
			logger.Error(err, "Failed to cleanup auth resources")
			return err
		}
	}

	// Delete HTTPRoute explicitly (also has ownerReferences for GC)
	if r.RoutingReconciler != nil {
		if err := r.RoutingReconciler.CleanupHTTPRoute(ctx, nebariApp); err != nil {
			logger.Error(err, "Failed to delete HTTPRoute")
			return err
		}
		// Also clean up the public HTTPRoute if it exists
		if err := r.RoutingReconciler.CleanupPublicHTTPRoute(ctx, nebariApp); err != nil {
			logger.Error(err, "Failed to delete public HTTPRoute")
			return err
		}
	}

	// Cleanup TLS resources (Certificate + Gateway listener)
	if r.TLSReconciler != nil {
		if err := r.TLSReconciler.CleanupTLS(ctx, nebariApp); err != nil {
			logger.Error(err, "Failed to cleanup TLS resources")
			return err
		}
	}

	// Additional cleanup handled automatically:

	logger.Info("Cleanup completed")
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NebariAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.NebariApp{}).
		Named("nebariapp")

	// Watch cert-manager Certificates so that Certificate readiness transitions
	// trigger NebariApp reconciliation without waiting for the periodic requeue.
	// Certificates are matched to NebariApps via the nebari.dev/nebariapp-name
	// and nebari.dev/nebariapp-namespace labels.
	if r.TLSReconciler != nil {
		builder = builder.Watches(
			&certmanagerv1.Certificate{},
			handler.EnqueueRequestsFromMapFunc(r.certificateToNebariApp),
		)
	}

	return builder.Complete(r)
}

// certificateToNebariApp maps a cert-manager Certificate to the NebariApp that owns it
// using the labels set by the TLS reconciler.
func (r *NebariAppReconciler) certificateToNebariApp(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetLabels()["nebari.dev/nebariapp-name"]
	namespace := obj.GetLabels()["nebari.dev/nebariapp-namespace"]
	if name == "" || namespace == "" {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: name, Namespace: namespace}},
	}
}
