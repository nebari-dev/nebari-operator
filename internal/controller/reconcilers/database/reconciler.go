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

package database

import (
	"context"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

const (
	// provisioningRequeue is how often to poll a CNPG Cluster that is not
	// ready yet. There is deliberately no startup Watches on CNPG types (the
	// CRDs are optional infrastructure and a watch on a missing CRD would
	// fail manager startup); the cached client may still start an informer
	// lazily once the CRD exists, which is fine.
	provisioningRequeue = 15 * time.Second

	// settledRequeue is how often to re-check states that only change
	// through external action: the CNPG CRDs being installed, or an invalid
	// app name being renamed (which also triggers reconciliation on its own).
	settledRequeue = 5 * time.Minute
)

// DatabaseReconciler provisions managed PostgreSQL databases for NebariApps
// through CloudNativePG.
type DatabaseReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// ReconcileDatabase handles spec.database for a NebariApp. A non-nil result
// means the caller should persist status and return it (polling while the
// database provisions, or backing off on settled non-ready states). A nil
// result with nil error means the database subsystem is settled and healthy
// (ready, disabled, or not configured).
// Once ready, the normalized credentials Secret is refreshed only when
// this function runs again; keeping it in sync with CNPG password
// rotation therefore relies on the caller reconciling periodically or
// watching the CNPG-generated source Secret.
func (r *DatabaseReconciler) ReconcileDatabase(ctx context.Context, nebariApp *appsv1.NebariApp) (*ctrl.Result, error) {
	db := nebariApp.Spec.Database
	if db == nil || !db.Enabled {
		return nil, r.handleDisabled(ctx, nebariApp)
	}

	// CNPG's admission webhook caps Cluster names at 50 characters; fail
	// with a friendly preflight message instead of a webhook rejection
	// buried in reconcile logs.
	if err := naming.ValidateDatabaseClusterName(nebariApp); err != nil {
		// Emit the event only on transition into this exact failure to avoid
		// spamming an identical warning on every slow requeue.
		if cond := conditions.GetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady); cond == nil ||
			cond.Reason != appsv1.ReasonFailed || cond.Message != err.Error() {
			r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonDatabaseProvisionFailed, err.Error())
		}
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
			appsv1.ReasonFailed, err.Error())
		return &ctrl.Result{RequeueAfter: settledRequeue}, nil
	}

	cluster, result, err := r.reconcileCluster(ctx, nebariApp)
	if result != nil || err != nil {
		return result, err
	}

	if !apimeta.IsStatusConditionTrue(cluster.Status.Conditions, string(cnpgv1.ConditionClusterReady)) {
		msg := "Waiting for the database cluster to become ready"
		if cluster.Status.Phase != "" {
			msg = fmt.Sprintf("%s (phase: %s)", msg, cluster.Status.Phase)
		}
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
			appsv1.ReasonDatabaseProvisioning, msg)
		return &ctrl.Result{RequeueAfter: provisioningRequeue}, nil
	}

	if requeue, err := r.reconcileCredentialsSecret(ctx, nebariApp, cluster); requeue || err != nil {
		if err != nil {
			r.recordFailure(nebariApp, err)
			return nil, err
		}
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
			appsv1.ReasonDatabaseProvisioning, "Waiting for CloudNativePG to publish connection credentials")
		return &ctrl.Result{RequeueAfter: provisioningRequeue}, nil
	}

	if err := r.reconcileSecretRBAC(ctx, nebariApp); err != nil {
		r.recordFailure(nebariApp, err)
		return nil, err
	}

	nebariApp.Status.DatabaseSecretRef = &appsv1.ResourceReference{
		Name:      naming.DatabaseSecretName(nebariApp),
		Namespace: nebariApp.Namespace,
	}

	// Emit the provisioned event only on the transition to ready.
	if !conditions.IsConditionTrue(nebariApp, appsv1.ConditionTypeDatabaseReady) {
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonDatabaseProvisioned,
			fmt.Sprintf("Managed database %s is ready; credentials in Secret %s",
				naming.DatabaseClusterName(nebariApp), naming.DatabaseSecretName(nebariApp)))
	}
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionTrue,
		appsv1.ReasonAvailable, "Database is ready and credentials are available")
	return nil, nil
}

// reconcileCluster creates or updates the CNPG Cluster. A non-nil result is
// returned when CNPG is not installed (degrade to condition + slow requeue).
func (r *DatabaseReconciler) reconcileCluster(ctx context.Context, nebariApp *appsv1.NebariApp) (*cnpgv1.Cluster, *ctrl.Result, error) {
	logger := log.FromContext(ctx)
	db := nebariApp.Spec.Database

	instances := int(db.Instances)
	if instances < 1 {
		instances = 1
	}
	size := db.Size
	if size == "" {
		size = "1Gi"
	}

	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.DatabaseClusterName(nebariApp),
			Namespace: nebariApp.Namespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cluster, func() error {
		if cluster.Labels == nil {
			cluster.Labels = map[string]string{}
		}
		cluster.Labels["nebari.dev/nebariapp-name"] = nebariApp.Name
		cluster.Labels["nebari.dev/nebariapp-namespace"] = nebariApp.Namespace
		cluster.Spec.Instances = instances
		cluster.Spec.StorageConfiguration.Size = size
		return controllerReference(nebariApp, cluster, r.Scheme)
	})
	if err != nil {
		if apimeta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
				appsv1.ReasonCNPGNotInstalled,
				"CloudNativePG is not installed on this cluster; in Nebari, set the top-level database.enabled toggle in the NIC config to install it")
			return nil, &ctrl.Result{RequeueAfter: settledRequeue}, nil
		}
		r.recordFailure(nebariApp, err)
		return nil, nil, fmt.Errorf("failed to reconcile CNPG Cluster: %w", err)
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("Reconciled CNPG Cluster", "cluster", cluster.Name, "operation", op)
	}
	return cluster, nil, nil
}

// handleDisabled covers spec.database nil or enabled: false. A previously
// provisioned Cluster is deliberately never deleted by the toggle: the
// operator only stops managing it and surfaces that through the condition
// and an event. Deleting the NebariApp still cascades via owner references.
func (r *DatabaseReconciler) handleDisabled(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	// Without any status evidence of a previous database there is nothing to
	// orphan-check; skipping the Get also avoids lazily starting a
	// cluster-wide CNPG informer for the many apps that never use a database.
	if nebariApp.Status.DatabaseSecretRef == nil && conditions.GetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady) == nil {
		return nil
	}

	cluster := &cnpgv1.Cluster{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      naming.DatabaseClusterName(nebariApp),
		Namespace: nebariApp.Namespace,
	}, cluster)
	if err != nil {
		// Not found, CRD missing, or CNPG types not registered: nothing was
		// ever provisioned (or CNPG is gone entirely). Clear any stale
		// condition and finish.
		if apierrors.IsNotFound(err) || apimeta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
			apimeta.RemoveStatusCondition(&nebariApp.Status.Conditions, appsv1.ConditionTypeDatabaseReady)
			nebariApp.Status.DatabaseSecretRef = nil
			return nil
		}
		return fmt.Errorf("failed to check for existing database cluster: %w", err)
	}

	// A same-named Cluster we do not own is none of our business; clear any
	// stale condition left from a failed attempt against it.
	if !metav1.IsControlledBy(cluster, nebariApp) {
		apimeta.RemoveStatusCondition(&nebariApp.Status.Conditions, appsv1.ConditionTypeDatabaseReady)
		nebariApp.Status.DatabaseSecretRef = nil
		return nil
	}

	if cond := conditions.GetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady); cond == nil || cond.Reason != appsv1.ReasonDatabaseDisabled {
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonDatabaseOrphaned,
			fmt.Sprintf("database.enabled is false but Cluster %s still exists; it is never deleted by the toggle. Delete the Cluster resource to remove the database and its data", cluster.Name))
	}
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
		appsv1.ReasonDatabaseDisabled,
		fmt.Sprintf("Database management is disabled; Cluster %s is retained and no longer managed", cluster.Name))
	return nil
}

func (r *DatabaseReconciler) recordFailure(nebariApp *appsv1.NebariApp, err error) {
	// Emit the event only on transition into this exact failure to avoid
	// spamming an identical warning on every retry.
	if cond := conditions.GetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady); cond == nil ||
		cond.Reason != appsv1.ReasonFailed || cond.Message != err.Error() {
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonDatabaseProvisionFailed, err.Error())
	}
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
		appsv1.ReasonFailed, err.Error())
}

// controllerReference is a seam shared with tests so fixtures carry the same
// owner reference the reconciler sets.
func controllerReference(owner, object metav1.Object, scheme *runtime.Scheme) error {
	return controllerutil.SetControllerReference(owner, object, scheme)
}
