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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/routing"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

// NebariAppReconciler reconciles a NebariApp object
type NebariAppReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	CoreReconciler    *core.CoreReconciler
	RoutingReconciler *routing.RoutingReconciler
}

// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
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

	// Initialize core reconciler
	if r.CoreReconciler == nil {
		r.CoreReconciler = &core.CoreReconciler{
			Client:   r.Client,
			Scheme:   r.Scheme,
			Recorder: r.Recorder,
		}
	}

	// Initialize routing reconciler if needed
	if r.RoutingReconciler == nil {
		r.RoutingReconciler = &routing.RoutingReconciler{
			Client:   r.Client,
			Scheme:   r.Scheme,
			Recorder: r.Recorder,
		}
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

	// Set initial reconciling status
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionUnknown,
		appsv1.ReasonReconciling, "Reconciliation in progress")

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

	// Reconcile routing (HTTPRoute creation/update) if routing is configured
	if nebariApp.Spec.Routing != nil {
		if err := r.RoutingReconciler.ReconcileRouting(ctx, nebariApp); err != nil {
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
	// Delete HTTPRoute explicitly (also has ownerReferences for GC)
	if r.RoutingReconciler != nil {
		if err := r.RoutingReconciler.CleanupHTTPRoute(ctx, nebariApp); err != nil {
			logger.Error(err, "Failed to delete HTTPRoute")
			return err
		}
	}

	// Additional cleanup handled automatically:

	logger.Info("Cleanup completed")
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NebariAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize the event recorder
	r.Recorder = mgr.GetEventRecorderFor("nebariapp-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.NebariApp{}).
		Named("nebariapp").
		Complete(r)
}
