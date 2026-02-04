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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
)

// NebariAppReconciler reconciles a NebariApp object
type NebariAppReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	CoreReconciler *core.CoreReconciler
}

// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=reconcilers.nebari.dev,resources=nebariapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

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

// SetupWithManager sets up the controller with the Manager.
func (r *NebariAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize the event recorder
	r.Recorder = mgr.GetEventRecorderFor("nebariapp-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.NebariApp{}).
		Named("nebariapp").
		Complete(r)
}
