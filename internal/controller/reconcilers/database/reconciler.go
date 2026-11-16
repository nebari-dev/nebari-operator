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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	// through external action: the CNPG CRDs being installed, a name that is
	// too long, or a Cluster that belongs to someone else.
	settledRequeue = 5 * time.Minute

	// Labels set on every Cluster the operator provisions. The app labels
	// match the ones the TLS reconciler puts on Certificates.
	labelAppName      = "nebari.dev/nebariapp-name"
	labelAppNamespace = "nebari.dev/nebariapp-namespace"
	labelDatabaseName = "nebari.dev/database-name"

	defaultInstances = 1
	defaultSize      = "1Gi"
)

// errForeignCluster marks a Cluster that already exists under the name an
// entry needs but is not controlled by this NebariApp. The operator never
// adopts or overwrites such a Cluster.
var errForeignCluster = errors.New("a Cluster with this name already exists and is not managed by this NebariApp")

// DatabaseReconciler provisions managed PostgreSQL databases for NebariApps
// through CloudNativePG.
type DatabaseReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// entryState is where one spec.databases entry stands after a reconcile pass.
type entryState int

const (
	// entryReady: the Cluster is ready and its credentials are published.
	entryReady entryState = iota
	// entryProvisioning: waiting on CloudNativePG; poll again soon.
	entryProvisioning
	// entryBlocked: cannot proceed until someone changes something (name too
	// long, or a foreign Cluster holds the name); re-check slowly.
	entryBlocked
	// entryFailed: an error worth retrying with backoff.
	entryFailed
)

// entryOutcome is the result of reconciling one spec.databases entry.
type entryOutcome struct {
	name   string
	state  entryState
	detail string
}

// ReconcileDatabase handles spec.databases for a NebariApp. Every entry is
// reconciled independently, so one database that cannot be provisioned does
// not hold the others back.
//
// A non-nil result means the caller should persist status and return it
// (polling while a database provisions, or backing off on a blocked one). A
// nil result with nil error means the database subsystem is settled: every
// requested database is ready, or none is requested.
//
// Once ready, a credentials Secret is refreshed only when this function runs
// again; keeping it in sync with CNPG password rotation therefore relies on
// the caller reconciling periodically or watching the CNPG-generated source
// Secret.
func (r *DatabaseReconciler) ReconcileDatabase(ctx context.Context, nebariApp *appsv1.NebariApp) (*ctrl.Result, error) {
	requested := nebariApp.Spec.Databases

	// Without a request or any status evidence of an earlier database there
	// is nothing to do. Returning here also avoids lazily starting a
	// cluster-wide CNPG informer for the many apps that never use a database.
	if len(requested) == 0 && len(nebariApp.Status.Databases) == 0 &&
		conditions.GetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady) == nil {
		return nil, nil
	}

	owned, err := r.listOwnedClusters(ctx, nebariApp)
	if err != nil {
		if isCNPGMissing(err) {
			return r.handleCNPGMissing(nebariApp), nil
		}
		err = fmt.Errorf("failed to list database clusters: %w", err)
		r.recordFailure(nebariApp, err.Error())
		return nil, err
	}

	previous := make(map[string]appsv1.DatabaseStatus, len(nebariApp.Status.Databases))
	for _, st := range nebariApp.Status.Databases {
		previous[st.Name] = st
	}

	statuses := make([]appsv1.DatabaseStatus, 0, len(requested))
	outcomes := make([]entryOutcome, 0, len(requested))
	wanted := make(map[string]struct{}, len(requested))
	var newlyReady []string
	for i := range requested {
		db := &requested[i]
		wanted[naming.DatabaseClusterName(nebariApp, db.Name)] = struct{}{}

		outcome, err := r.reconcileEntry(ctx, nebariApp, db, previous[db.Name].SecretRef != nil)
		if err != nil && isCNPGMissing(err) {
			return r.handleCNPGMissing(nebariApp), nil
		}
		outcomes = append(outcomes, outcome)

		st := appsv1.DatabaseStatus{Name: db.Name}
		switch outcome.state {
		case entryReady:
			st.SecretRef = &appsv1.ResourceReference{
				Name:      naming.DatabaseSecretName(nebariApp, db.Name),
				Namespace: nebariApp.Namespace,
			}
			if previous[db.Name].SecretRef == nil {
				newlyReady = append(newlyReady, db.Name)
			}
		case entryProvisioning, entryFailed:
			// Credentials published on an earlier pass are still valid.
			st.SecretRef = previous[db.Name].SecretRef
		case entryBlocked:
			// The name is not ours to use, so neither is any Secret under it.
		}
		statuses = append(statuses, st)
	}

	// Clusters this app owns that no entry asks for any more are retained:
	// reported, but never deleted and no longer managed.
	var newlyRetained []string
	retained := retainedClusters(owned, wanted)
	for _, cluster := range retained {
		name := databaseNameOf(nebariApp, cluster)
		prev := previous[name]
		if !prev.Retained {
			newlyRetained = append(newlyRetained, name)
		}
		statuses = append(statuses, appsv1.DatabaseStatus{Name: name, SecretRef: prev.SecretRef, Retained: true})
	}

	if err := r.reconcileSecretRBAC(ctx, nebariApp, publishedSecretNames(statuses)); err != nil {
		// Status is left as it was so the transitions above are reported on
		// the pass that completes.
		r.recordFailure(nebariApp, err.Error())
		return nil, err
	}

	nebariApp.Status.Databases = statuses
	for _, name := range newlyReady {
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonDatabaseProvisioned,
			fmt.Sprintf("Managed database %q is ready (Cluster %s); credentials in Secret %s",
				name, naming.DatabaseClusterName(nebariApp, name), naming.DatabaseSecretName(nebariApp, name)))
	}
	for _, name := range newlyRetained {
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonDatabaseRetained,
			fmt.Sprintf("Database %q was removed from spec.databases but Cluster %s still exists; a database is never deleted by removing its entry. Add the entry back to resume management, or delete the Cluster to remove the database and its data",
				name, naming.DatabaseClusterName(nebariApp, name)))
	}

	return r.summarize(nebariApp, outcomes, retained)
}

// summarize folds the per-entry outcomes into the DatabaseReady condition and
// the result the caller should act on.
func (r *DatabaseReconciler) summarize(nebariApp *appsv1.NebariApp, outcomes []entryOutcome, retained []*cnpgv1.Cluster) (*ctrl.Result, error) {
	pending := make([]string, 0, len(outcomes))
	worst := entryReady
	for _, o := range outcomes {
		if o.state == entryReady {
			continue
		}
		pending = append(pending, fmt.Sprintf("%s: %s", o.name, o.detail))
		if o.state > worst {
			worst = o.state
		}
	}
	message := strings.Join(pending, "; ")

	switch worst {
	case entryFailed:
		r.recordFailure(nebariApp, message)
		return nil, errors.New(message)
	case entryBlocked:
		r.recordFailure(nebariApp, message)
		// Entries still provisioning alongside a blocked one keep the fast poll.
		for _, o := range outcomes {
			if o.state == entryProvisioning {
				return &ctrl.Result{RequeueAfter: provisioningRequeue}, nil
			}
		}
		return &ctrl.Result{RequeueAfter: settledRequeue}, nil
	case entryProvisioning:
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
			appsv1.ReasonDatabaseProvisioning, "Waiting for databases: "+message)
		return &ctrl.Result{RequeueAfter: provisioningRequeue}, nil
	}

	retainedNote := ""
	if len(retained) > 0 {
		names := make([]string, 0, len(retained))
		for _, cluster := range retained {
			names = append(names, cluster.Name)
		}
		retainedNote = "retained and no longer managed: Cluster " + strings.Join(names, ", ")
	}

	switch {
	case len(outcomes) > 0:
		message = fmt.Sprintf("%d of %d databases ready and credentials available", len(outcomes), len(outcomes))
		if retainedNote != "" {
			message += "; " + retainedNote
		}
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionTrue,
			appsv1.ReasonAvailable, message)
	case len(retained) > 0:
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
			appsv1.ReasonDatabaseRetained, "No database is requested; "+retainedNote)
	default:
		// Nothing requested and nothing left behind: drop the stale condition.
		clearDatabaseStatus(nebariApp)
	}
	return nil, nil
}

// reconcileEntry drives one spec.databases entry as far as it can go on this
// pass. published says whether this database's credentials were published on
// an earlier pass. The returned error is non-nil only for entryFailed
// outcomes and is there so the caller can tell a missing CNPG installation
// apart.
func (r *DatabaseReconciler) reconcileEntry(ctx context.Context, nebariApp *appsv1.NebariApp, db *appsv1.DatabaseConfig, published bool) (entryOutcome, error) {
	outcome := entryOutcome{name: db.Name}

	// CNPG's admission webhook caps Cluster names at 50 characters; fail
	// with a friendly preflight message instead of a webhook rejection
	// buried in reconcile logs.
	if err := naming.ValidateDatabaseClusterName(nebariApp, db.Name); err != nil {
		outcome.state, outcome.detail = entryBlocked, err.Error()
		return outcome, nil
	}

	cluster, err := r.reconcileCluster(ctx, nebariApp, db)
	if err != nil {
		if errors.Is(err, errForeignCluster) {
			outcome.state = entryBlocked
			outcome.detail = fmt.Sprintf("Cluster %s already exists and is not managed by this NebariApp; choose a different database name",
				naming.DatabaseClusterName(nebariApp, db.Name))
			return outcome, nil
		}
		outcome.state, outcome.detail = entryFailed, err.Error()
		return outcome, err
	}

	if !apimeta.IsStatusConditionTrue(cluster.Status.Conditions, string(cnpgv1.ConditionClusterReady)) {
		// A database that was ready before usually keeps serving while its
		// Cluster is busy (adding a replica, a rolling update, a failover),
		// and that can last a long time. Keep the credentials it already
		// published current, so a password rotated meanwhile still reaches
		// the app. Credentials are never published for the first time here.
		if published {
			if _, err := r.reconcileCredentialsSecret(ctx, nebariApp, db.Name, cluster); err != nil {
				outcome.state, outcome.detail = entryFailed, err.Error()
				return outcome, err
			}
		}
		outcome.state, outcome.detail = entryProvisioning, "cluster is not ready yet"
		if cluster.Status.Phase != "" {
			outcome.detail = fmt.Sprintf("%s (phase: %s)", outcome.detail, cluster.Status.Phase)
		}
		return outcome, nil
	}

	requeue, err := r.reconcileCredentialsSecret(ctx, nebariApp, db.Name, cluster)
	if err != nil {
		outcome.state, outcome.detail = entryFailed, err.Error()
		return outcome, err
	}
	if requeue {
		outcome.state, outcome.detail = entryProvisioning, "waiting for CloudNativePG to publish connection credentials"
		return outcome, nil
	}

	outcome.state = entryReady
	return outcome, nil
}

// reconcileCluster creates or updates the CNPG Cluster for one entry. It
// returns errForeignCluster, without touching the object, when a Cluster of
// that name exists but is not controlled by this NebariApp.
func (r *DatabaseReconciler) reconcileCluster(ctx context.Context, nebariApp *appsv1.NebariApp, db *appsv1.DatabaseConfig) (*cnpgv1.Cluster, error) {
	logger := log.FromContext(ctx)

	// The API server applies these defaults; they are repeated here for
	// objects that did not pass through it.
	instances := int(db.Instances)
	if instances < 1 {
		instances = defaultInstances
	}
	size := db.Size
	if size == "" {
		size = defaultSize
	}

	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.DatabaseClusterName(nebariApp, db.Name),
			Namespace: nebariApp.Namespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cluster, func() error {
		// Two NebariApps in one namespace can derive the same Cluster name
		// ("foo" + "bar-baz" and "foo-bar" + "baz"), and a user may have
		// created one by hand. Neither is ours to adopt or resize.
		if cluster.ResourceVersion != "" && !metav1.IsControlledBy(cluster, nebariApp) {
			return errForeignCluster
		}
		if cluster.Labels == nil {
			cluster.Labels = map[string]string{}
		}
		cluster.Labels[labelAppName] = nebariApp.Name
		cluster.Labels[labelAppNamespace] = nebariApp.Namespace
		cluster.Labels[labelDatabaseName] = db.Name
		cluster.Spec.Instances = instances
		cluster.Spec.StorageConfiguration.Size = size
		return controllerReference(nebariApp, cluster, r.Scheme)
	})
	if err != nil {
		if errors.Is(err, errForeignCluster) || isCNPGMissing(err) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to reconcile CNPG Cluster %s: %w", cluster.Name, err)
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("Reconciled CNPG Cluster", "cluster", cluster.Name, "database", db.Name, "operation", op)
	}
	return cluster, nil
}

// listOwnedClusters returns the CNPG Clusters in the app's namespace that this
// NebariApp controls, whether or not spec.databases still asks for them.
func (r *DatabaseReconciler) listOwnedClusters(ctx context.Context, nebariApp *appsv1.NebariApp) ([]*cnpgv1.Cluster, error) {
	list := &cnpgv1.ClusterList{}
	if err := r.Client.List(ctx, list,
		client.InNamespace(nebariApp.Namespace),
		client.MatchingLabels{labelAppName: nebariApp.Name},
	); err != nil {
		return nil, err
	}

	owned := make([]*cnpgv1.Cluster, 0, len(list.Items))
	for i := range list.Items {
		if metav1.IsControlledBy(&list.Items[i], nebariApp) {
			owned = append(owned, &list.Items[i])
		}
	}
	return owned, nil
}

// retainedClusters returns the owned Clusters that no entry asks for, sorted
// by name so status and messages are stable across passes.
func retainedClusters(owned []*cnpgv1.Cluster, wanted map[string]struct{}) []*cnpgv1.Cluster {
	var retained []*cnpgv1.Cluster
	for _, cluster := range owned {
		if _, ok := wanted[cluster.Name]; !ok {
			retained = append(retained, cluster)
		}
	}
	sort.Slice(retained, func(i, j int) bool { return retained[i].Name < retained[j].Name })
	return retained
}

// databaseNameOf recovers the spec.databases name a Cluster was created for.
func databaseNameOf(nebariApp *appsv1.NebariApp, cluster *cnpgv1.Cluster) string {
	if name := cluster.Labels[labelDatabaseName]; name != "" {
		return name
	}
	// The label was removed by hand; fall back to the naming pattern.
	return strings.TrimSuffix(strings.TrimPrefix(cluster.Name, nebariApp.Name+"-"), "-db")
}

// publishedSecretNames returns the credentials Secrets the app may read: one
// per database whose credentials have been published, retained ones included
// so removing an entry does not also cut a running app off from its data.
func publishedSecretNames(statuses []appsv1.DatabaseStatus) []string {
	var names []string
	for _, st := range statuses {
		if st.SecretRef != nil {
			names = append(names, st.SecretRef.Name)
		}
	}
	return names
}

// handleCNPGMissing reports that the CloudNativePG CRDs are absent. With no
// database requested there is nothing to report, so stale status is cleared.
func (r *DatabaseReconciler) handleCNPGMissing(nebariApp *appsv1.NebariApp) *ctrl.Result {
	if len(nebariApp.Spec.Databases) == 0 {
		clearDatabaseStatus(nebariApp)
		return nil
	}
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
		appsv1.ReasonCNPGNotInstalled,
		"CloudNativePG is not installed on this cluster; install the CloudNativePG operator (in Nebari, NIC installs it on every GitOps bootstrap)")
	return &ctrl.Result{RequeueAfter: settledRequeue}
}

// clearDatabaseStatus removes every trace of the database subsystem from status.
func clearDatabaseStatus(nebariApp *appsv1.NebariApp) {
	apimeta.RemoveStatusCondition(&nebariApp.Status.Conditions, appsv1.ConditionTypeDatabaseReady)
	nebariApp.Status.Databases = nil
}

// isCNPGMissing reports whether err means the CloudNativePG CRDs (or their Go
// types) are not available.
func isCNPGMissing(err error) bool {
	return apimeta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err)
}

// recordFailure sets DatabaseReady=False/Failed and emits a warning event,
// but only on transition into this exact failure, to avoid spamming an
// identical warning on every retry.
func (r *DatabaseReconciler) recordFailure(nebariApp *appsv1.NebariApp, message string) {
	if cond := conditions.GetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady); cond == nil ||
		cond.Reason != appsv1.ReasonFailed || cond.Message != message {
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonDatabaseProvisionFailed, message)
	}
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
		appsv1.ReasonFailed, message)
}

// controllerReference is a seam shared with tests so fixtures carry the same
// owner reference the reconciler sets.
func controllerReference(owner, object metav1.Object, scheme *runtime.Scheme) error {
	return controllerutil.SetControllerReference(owner, object, scheme)
}
