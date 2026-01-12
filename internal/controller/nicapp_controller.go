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

package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/auth"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/keycloak"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/routing"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/tls"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/constants"
)

// NicAppReconciler reconciles a NicApp object
type NicAppReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	CoreReconciler    *core.CoreReconciler
	RoutingReconciler *routing.RoutingReconciler
	TLSReconciler     *tls.TLSReconciler
	AuthReconciler    *auth.Reconciler

	// Keycloak configuration
	KeycloakURL       string // Internal URL for admin API
	KeycloakIssuerURL string // External URL for OIDC (browser-accessible)
	KeycloakRealm     string
	KeycloakAdmin     string
	KeycloakPass      string
}

// +kubebuilder:rbac:groups=apps.nic.nebari.dev,resources=nicapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps.nic.nebari.dev,resources=nicapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps.nic.nebari.dev,resources=nicapps/finalizers,verbs=update
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
//
// The reconciliation logic:
// 1. Validate that the namespace is opted-in to NIC management
// 2. Ensure finalizer is present for cleanup
// 3. Handle deletion (cleanup generated resources)
// 4. Validate the NicApp spec (service exists, etc.)
// 5. Reconcile routing (HTTPRoute generation)
// 6. Reconcile TLS (certificate management)
// 7. Reconcile auth (SecurityPolicy + Keycloak client)
// 8. Update status conditions
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *NicAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling NicApp", "name", req.Name, "namespace", req.Namespace)

	// Fetch the NicApp instance
	nicApp := &appsv1.NicApp{}
	if err := r.Get(ctx, req.NamespacedName, nicApp); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("NicApp resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get NicApp")
		return ctrl.Result{}, err
	}

	// Initialize core reconciler if needed
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

	// Initialize TLS reconciler if needed
	if r.TLSReconciler == nil {
		r.TLSReconciler = &tls.TLSReconciler{
			Client:   r.Client,
			Scheme:   r.Scheme,
			Recorder: r.Recorder,
		}
	}

	// Initialize auth reconciler if needed
	if r.AuthReconciler == nil {
		// Initialize Keycloak provisioner with configuration from controller
		kcProvisioner := &keycloak.ClientProvisioner{
			Client:        r.Client,
			KeycloakURL:   r.KeycloakURL,
			KeycloakRealm: r.KeycloakRealm,
			AdminUsername: r.KeycloakAdmin,
			AdminPassword: r.KeycloakPass,
		}

		r.AuthReconciler = &auth.Reconciler{
			Client:              r.Client,
			KeycloakProvisioner: kcProvisioner,
			KeycloakIssuerURL:   r.KeycloakIssuerURL,
			KeycloakRealm:       r.KeycloakRealm,
		}
	}

	// Handle finalizer
	if nicApp.ObjectMeta.DeletionTimestamp.IsZero() {
		// Object is not being deleted, ensure finalizer is present
		if !controllerutil.ContainsFinalizer(nicApp, constants.NicAppFinalizer) {
			controllerutil.AddFinalizer(nicApp, constants.NicAppFinalizer)
			if err := r.Update(ctx, nicApp); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// Object is being deleted
		if controllerutil.ContainsFinalizer(nicApp, constants.NicAppFinalizer) {
			// Run cleanup logic
			if err := r.cleanup(ctx, nicApp); err != nil {
				logger.Error(err, "Failed to cleanup resources")
				return ctrl.Result{}, err
			}

			// Remove finalizer
			controllerutil.RemoveFinalizer(nicApp, constants.NicAppFinalizer)
			if err := r.Update(ctx, nicApp); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Initialize status if needed
	if nicApp.Status.ObservedGeneration == 0 {
		nicApp.Status.ObservedGeneration = nicApp.Generation
		nicApp.Status.Hostname = nicApp.Spec.Hostname
	}

	// Set initial reconciling status
	conditions.SetCondition(nicApp, appsv1.ConditionTypeReady, metav1.ConditionUnknown,
		appsv1.ReasonReconciling, "Reconciliation in progress")

	// Validate namespace opt-in and NicApp spec
	if err := r.CoreReconciler.ValidateNicApp(ctx, nicApp); err != nil {
		logger.Error(err, "Core validation failed")
		if err := r.Status().Update(ctx, nicApp); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue after a longer delay for validation failures
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	// Reconcile routing (HTTPRoute generation)
	if err := r.RoutingReconciler.ReconcileRouting(ctx, nicApp); err != nil {
		logger.Error(err, "Routing reconciliation failed")
		if err := r.Status().Update(ctx, nicApp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Reconcile TLS (certificate validation)
	if err := r.TLSReconciler.ReconcileTLS(ctx, nicApp); err != nil {
		logger.Error(err, "TLS reconciliation failed")
		if err := r.Status().Update(ctx, nicApp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Reconcile authentication (SecurityPolicy + Keycloak client provisioning)
	if err := r.AuthReconciler.ReconcileAuth(ctx, nicApp); err != nil {
		logger.Error(err, "Auth reconciliation failed")
		if err := r.Status().Update(ctx, nicApp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// Update observed generation
	nicApp.Status.ObservedGeneration = nicApp.Generation

	// Update status
	if err := r.Status().Update(ctx, nicApp); err != nil {
		logger.Error(err, "Failed to update NicApp status")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully reconciled NicApp")
	// Requeue after 1 minute for now (until full implementation)
	return ctrl.Result{RequeueAfter: time.Minute}, nil
}

// cleanup removes resources created by this NicApp
func (r *NicAppReconciler) cleanup(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)
	logger.Info("Cleaning up resources for NicApp", "name", nicApp.Name, "namespace", nicApp.Namespace)

	r.Recorder.Event(nicApp, corev1.EventTypeNormal, "Cleanup", "Starting resource cleanup")

	// Delete HTTPRoute explicitly (also has ownerReferences for GC)
	if r.RoutingReconciler != nil {
		if err := r.RoutingReconciler.CleanupHTTPRoute(ctx, nicApp); err != nil {
			logger.Error(err, "Failed to delete HTTPRoute")
			return err
		}
	}

	// Additional cleanup handled automatically:
	// - SecurityPolicy (via ownerReferences)
	// - Keycloak OIDC client (via auth reconciler)
	// - Client secret (via ownerReferences)
	// Future: Certificate deletion for per-host mode

	logger.Info("Cleanup completed")
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *NicAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize the event recorder
	r.Recorder = mgr.GetEventRecorderFor("nicapp-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1.NicApp{}).
		Named("nicapp").
		Complete(r)
}
