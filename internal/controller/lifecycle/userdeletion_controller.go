/*
Copyright (c) 2026, OpenTeams

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

package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

const (
	// createRetryInterval is how soon to requeue an event after a transient create failure
	createRetryInterval = 30 * time.Second

	// finalizerRetryInterval is how soon to check again whether running Jobs have
	// finished on a marker that is being deleted
	finalizerRetryInterval = 30 * time.Second

	// jobLostGrace is how long a Running entry's Job may be missing from the
	// cache before it is considered gone rather than not yet seen
	jobLostGrace = time.Minute

	// conflictRetryInterval is how soon to retry after a write conflict, long
	// enough for the cache to receive the version that caused it
	conflictRetryInterval = time.Second
)

// UserDeletionReconciler reconciles a UserDeletion object
type UserDeletionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// GracePeriod is how long after the Keycloak deletion the delete stage runs.
	GracePeriod time.Duration
	// JobCreateRetryWindow is how long a Job create that keeps failing
	// transiently is retried, counted from its first failure, before the
	// entry is marked Failed.
	JobCreateRetryWindow time.Duration
	// MarkerRetention is how long a completed marker is kept as a tombstone,
	// counted from completedAt, before the reconciler deletes it.
	MarkerRetention time.Duration
}

// +kubebuilder:rbac:groups=lifecycle.nebari.dev,resources=userdeletions,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=lifecycle.nebari.dev,resources=userdeletions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=lifecycle.nebari.dev,resources=userdeletions/finalizers,verbs=update
// +kubebuilder:rbac:groups=lifecycle.nebari.dev,resources=usercleanuphooks,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile registers user cleanup hooks and submit their corresponding jobs when
// it's time to run them.
func (r *UserDeletionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the marker. Not found means it was deleted since the event was
	// queued, and owner references take the Jobs with it, so nothing to do.
	var marker lifecyclev1alpha1.UserDeletion
	if err := r.Get(ctx, req.NamespacedName, &marker); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// A marker being deleted waits for its running Jobs, then lets go.
	if !marker.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, &marker)
	}

	// The finalizer is what makes the wait above possible. Adding it is a
	// write to the main resource; the Update refreshes the in-memory marker so
	// the status write at the end still applies.
	if controllerutil.AddFinalizer(&marker, lifecyclev1alpha1.UserDeletionFinalizer) {
		if err := r.Update(ctx, &marker); err != nil {
			return retryOnConflict(err)
		}
	}

	now := time.Now()

	// A completed marker is a tombstone: it exists so a replayed Keycloak event
	// collides on the name instead of running cleanup again. It never discovers
	// hooks or creates Jobs again. Once the retention has passed it goes away.
	if marker.Status.Phase == lifecyclev1alpha1.UserDeletionCompleted {
		if marker.Status.CompletedAt != nil && !now.Before(marker.Status.CompletedAt.Add(r.MarkerRetention)) {
			log.Info("tombstone expired, deleting marker", "completedAt", marker.Status.CompletedAt.Time)
			return ctrl.Result{}, client.IgnoreNotFound(r.Delete(ctx, &marker))
		}
		return nextRequeue(&marker, false, r.MarkerRetention, now), nil
	}

	// Work on a copy of the status so the write at the end can compare against
	// what was fetched and skip the update when nothing changed.
	original := marker.DeepCopy()

	// dueAt is computed only once, when a UserDeletion marker is created
	if marker.Status.DueAt == nil {
		due := metav1.NewTime(marker.Spec.DeletedAt.Add(r.GracePeriod))
		marker.Status.DueAt = &due
		log.Info("scheduled delayed cleanup", "dueAt", due.Time)
	}

	if marker.Status.Phase == "" {
		marker.Status.Phase = lifecyclev1alpha1.UserDeletionPending
	}

	identifiers := metav1.Condition{
		Type:               lifecyclev1alpha1.ConditionTypeIdentifiersComplete,
		Status:             metav1.ConditionTrue,
		Reason:             lifecyclev1alpha1.ReasonUsernameKnown,
		Message:            "user id and username are known",
		ObservedGeneration: marker.Generation,
	}
	if marker.Spec.Username == "" {
		identifiers.Status = metav1.ConditionFalse
		identifiers.Reason = lifecyclev1alpha1.ReasonUsernameMissing
		identifiers.Message = "Keycloak admin event carried no username; hooks keyed on username may do nothing"
	}
	meta.SetStatusCondition(&marker.Status.Conditions, identifiers)

	// Reconcile the marker's hook entries against the hooks that exist in the cluster
	hooks, err := r.listAcceptedHooks(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	discoverHooks(&marker, hooks)

	// Record the outcome of every Job that has finished since the last pass.
	// This runs before creating new Jobs because Job reads go through the
	// cache and a Job created in this pass is not in it yet. It is observed
	// on the next pass, which its own create event triggers.
	if err := r.observeJobs(ctx, &marker); err != nil {
		return ctrl.Result{}, err
	}

	// Create Jobs for the entries whose stage is due. A disable entry is due as
	// soon as it exists and a delete entry only once the grace period has elapsed
	blocked, err := r.createDueJobs(ctx, &marker, hooks)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Compute the phase and the HooksSucceeded condition from the hook entries
	summarize(&marker, now)
	if marker.Status.Phase == lifecyclev1alpha1.UserDeletionCompleted && original.Status.Phase != lifecyclev1alpha1.UserDeletionCompleted {
		log.Info("cleanup completed", "hooks", len(marker.Status.Hooks))
		r.Recorder.Event(&marker, corev1.EventTypeNormal, "Completed", "All cleanup hooks reached a terminal state")
	}

	marker.Status.ObservedGeneration = marker.Generation

	// Only update the resource if status has changed
	if !equality.Semantic.DeepEqual(original.Status, marker.Status) {
		if err := r.Status().Update(ctx, &marker); err != nil {
			return retryOnConflict(err)
		}
	}

	return nextRequeue(&marker, blocked, r.MarkerRetention, now), nil
}

// finalize handles a marker that is being deleted. Running Jobs are left to
// finish so a cleanup script is not killed halfway. Once none are running the
// finalizer is removed and garbage collection takes the Jobs with the marker.
func (r *UserDeletionReconciler) finalize(ctx context.Context, marker *lifecyclev1alpha1.UserDeletion) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(marker, lifecyclev1alpha1.UserDeletionFinalizer) {
		return ctrl.Result{}, nil
	}

	original := marker.DeepCopy()
	if err := r.observeJobs(ctx, marker); err != nil {
		return ctrl.Result{}, err
	}
	if !equality.Semantic.DeepEqual(original.Status, marker.Status) {
		if err := r.Status().Update(ctx, marker); err != nil {
			return retryOnConflict(err)
		}
	}

	for _, entry := range marker.Status.Hooks {
		if entry.State == lifecyclev1alpha1.HookRunning {
			logf.FromContext(ctx).Info("waiting for running Job before releasing marker", "job", entry.Job, "namespace", entry.Namespace)
			return ctrl.Result{RequeueAfter: finalizerRetryInterval}, nil
		}
	}

	controllerutil.RemoveFinalizer(marker, lifecyclev1alpha1.UserDeletionFinalizer)
	if err := r.Update(ctx, marker); err != nil {
		return retryOnConflict(client.IgnoreNotFound(err))
	}
	return ctrl.Result{}, nil
}

// retryOnConflict turns a write conflict into a short requeue. A conflict means
// the marker was read from the cache before a newer version arrived, which is
// routine for a cache-backed client and not a failure: the next pass reads the
// current version and recomputes. Any other error is returned as is.
func retryOnConflict(err error) (ctrl.Result, error) {
	if apierrors.IsConflict(err) {
		return ctrl.Result{RequeueAfter: conflictRetryInterval}, nil
	}
	return ctrl.Result{}, err
}

// summarize derives the phase and the HooksSucceeded condition from the hook
// entries. The marker completes only when every entry is terminal and dueAt
// has passed, so a hook installed during the grace period still gets its turn
// even when no hook existed at first. A marker with no hooks completes at
// dueAt with reason NoHooks.
func summarize(marker *lifecyclev1alpha1.UserDeletion, now time.Time) {
	var pending, running, failed, skipped int
	for _, entry := range marker.Status.Hooks {
		switch entry.State {
		case lifecyclev1alpha1.HookPending:
			pending++
		case lifecyclev1alpha1.HookRunning:
			running++
		case lifecyclev1alpha1.HookFailed:
			failed++
		case lifecyclev1alpha1.HookSkipped:
			skipped++
		}
	}
	total := len(marker.Status.Hooks)
	allTerminal := pending == 0 && running == 0
	dueReached := marker.Status.DueAt != nil && !now.Before(marker.Status.DueAt.Time)

	switch {
	case allTerminal && dueReached:
		marker.Status.Phase = lifecyclev1alpha1.UserDeletionCompleted
		if marker.Status.CompletedAt == nil {
			completed := metav1.NewTime(now)
			marker.Status.CompletedAt = &completed
		}
	case total > pending:
		marker.Status.Phase = lifecyclev1alpha1.UserDeletionInProgress
	default:
		marker.Status.Phase = lifecyclev1alpha1.UserDeletionPending
	}

	cond := metav1.Condition{
		Type:               lifecyclev1alpha1.ConditionTypeHooksSucceeded,
		ObservedGeneration: marker.Generation,
	}
	switch {
	case !allTerminal || !dueReached:
		cond.Status = metav1.ConditionFalse
		cond.Reason = lifecyclev1alpha1.ReasonHooksPending
		cond.Message = fmt.Sprintf("%d of %d hooks still pending or running", pending+running, total)
	case failed > 0:
		cond.Status = metav1.ConditionFalse
		cond.Reason = lifecyclev1alpha1.ReasonHookFailed
		cond.Message = fmt.Sprintf("%d of %d hooks failed", failed, total)
	case skipped > 0:
		cond.Status = metav1.ConditionFalse
		cond.Reason = lifecyclev1alpha1.ReasonHookSkipped
		cond.Message = fmt.Sprintf("%d of %d hooks were skipped", skipped, total)
	case total == 0:
		cond.Status = metav1.ConditionTrue
		cond.Reason = lifecyclev1alpha1.ReasonNoHooks
		cond.Message = "no UserCleanupHook was registered during the grace period"
	default:
		cond.Status = metav1.ConditionTrue
		cond.Reason = lifecyclev1alpha1.ReasonAllSucceeded
		cond.Message = fmt.Sprintf("all %d hooks succeeded", total)
	}
	meta.SetStatusCondition(&marker.Status.Conditions, cond)
}

// nextRequeue decides when the reconciler should wake up on its own. Hook and
// Job events arrive through the watches; this only covers the moments nothing
// in the cluster changes: a create retry, the grace period elapsing, and the
// tombstone expiring. An empty result means wait for the next event.
func nextRequeue(marker *lifecyclev1alpha1.UserDeletion, blocked bool, retention time.Duration, now time.Time) ctrl.Result {
	switch {
	case blocked:
		// A Job create failed transiently, so try again after the interval.
		return ctrl.Result{RequeueAfter: createRetryInterval}
	case marker.Status.Phase == lifecyclev1alpha1.UserDeletionCompleted && marker.Status.CompletedAt != nil:
		// A tombstone waits for its retention to pass, then deletes itself.
		return ctrl.Result{RequeueAfter: marker.Status.CompletedAt.Add(retention).Sub(now)}
	case marker.Status.DueAt != nil && marker.Status.DueAt.After(now):
		// Come back when the delete stage is due so its Jobs are created.
		return ctrl.Result{RequeueAfter: marker.Status.DueAt.Sub(now)}
	default:
		// Past dueAt every due Job exists and its completion arrives through
		// the Owns watch. Nothing to wait for.
		return ctrl.Result{}
	}
}

// listAcceptedHooks returns every UserCleanupHook whose template passed
// validation. A hook that is not Accepted is ignored rather than recorded, so
// a pack fixing its template gets picked up on the next pass as if it had just
// been installed.
func (r *UserDeletionReconciler) listAcceptedHooks(ctx context.Context) ([]lifecyclev1alpha1.UserCleanupHook, error) {
	var list lifecyclev1alpha1.UserCleanupHookList
	if err := r.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("failed to list UserCleanupHooks: %w", err)
	}

	accepted := make([]lifecyclev1alpha1.UserCleanupHook, 0, len(list.Items))
	for _, hook := range list.Items {
		if meta.IsStatusConditionTrue(hook.Status.Conditions, lifecyclev1alpha1.ConditionTypeAccepted) {
			accepted = append(accepted, hook)
		}
	}
	return accepted, nil
}

// discoverHooks makes the marker's hook entries match the hooks that exist.
// A hook without an entry gets one as Pending. An entry whose hook is gone and
// has not reached a terminal state is Skipped, so the marker can still
// complete. Entries whose hook still exists are left to the Job logic.
func discoverHooks(marker *lifecyclev1alpha1.UserDeletion, hooks []lifecyclev1alpha1.UserCleanupHook) {
	present := make(map[types.NamespacedName]bool, len(hooks))
	for _, hook := range hooks {
		present[types.NamespacedName{Namespace: hook.Namespace, Name: hook.Name}] = true
	}

	recorded := make(map[types.NamespacedName]bool, len(marker.Status.Hooks))
	for i := range marker.Status.Hooks {
		entry := &marker.Status.Hooks[i]
		key := types.NamespacedName{Namespace: entry.Namespace, Name: entry.Name}
		recorded[key] = true

		if !present[key] && !entry.State.IsTerminal() {
			now := metav1.Now()
			entry.State = lifecyclev1alpha1.HookSkipped
			entry.Reason = lifecyclev1alpha1.HookReasonHookRemoved
			entry.Message = "UserCleanupHook was removed before its stage ran"
			entry.FinishedAt = &now
		}
	}

	for _, hook := range hooks {
		key := types.NamespacedName{Namespace: hook.Namespace, Name: hook.Name}
		if recorded[key] {
			continue
		}
		marker.Status.Hooks = append(marker.Status.Hooks, lifecyclev1alpha1.HookStatus{
			Name:      hook.Name,
			Namespace: hook.Namespace,
			Stage:     hook.Spec.Stage,
			State:     lifecyclev1alpha1.HookPending,
		})
	}
}

// stageDue reports whether a hook stage may run now for this marker
func stageDue(marker *lifecyclev1alpha1.UserDeletion, stage lifecyclev1alpha1.CleanupStage, now time.Time) bool {
	switch stage {
	case lifecyclev1alpha1.CleanupStageDisable:
		return true
	case lifecyclev1alpha1.CleanupStageDelete:
		return marker.Status.DueAt != nil && !now.Before(marker.Status.DueAt.Time)
	default:
		return false
	}
}

// permanentCreateError reports whether a Job create error will fail the same
// way on every retry. Permanent errors include failed validations, malformed
// requests, and RBAC issues. The one exception is quota, which returns a 403
// clears as other Jobs finish, so it is treated as transient.
func permanentCreateError(err error) bool {
	if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		return true
	}
	return apierrors.IsForbidden(err) && !strings.Contains(err.Error(), "exceeded quota")
}

// createDueJobs creates a Job for every Pending entry whose stage is due and
// records it on the entry as Running. The Job name is deterministic to ensure
// idempotency. A create that finds the Job already adopts it instead of failing.
// That means a crash between the create and the status write is handled safely.
//
// A failed create never returns an error, so the status write still records the
// Jobs that were created. A permanent rejection makes the entry Failed at once.
// A transient failure leaves it Pending with the error on the entry and is
// retried for JobCreateRetryWindow from its first failure, then becomes Failed too.
// The returned bool is true when at least one entry is waiting on a retry.
func (r *UserDeletionReconciler) createDueJobs(ctx context.Context, marker *lifecyclev1alpha1.UserDeletion, hooks []lifecyclev1alpha1.UserCleanupHook) (bool, error) {
	log := logf.FromContext(ctx)
	now := time.Now()
	blocked := false

	byKey := make(map[types.NamespacedName]*lifecyclev1alpha1.UserCleanupHook, len(hooks))
	for i := range hooks {
		byKey[types.NamespacedName{Namespace: hooks[i].Namespace, Name: hooks[i].Name}] = &hooks[i]
	}

	for i := range marker.Status.Hooks {
		entry := &marker.Status.Hooks[i]
		if entry.State != lifecyclev1alpha1.HookPending || !stageDue(marker, entry.Stage, now) {
			continue
		}

		// Give up on an entry whose create has kept failing for the whole retry
		// window, measured from the first failure. The last error stays in the
		// message.
		if entry.FirstFailedAt != nil && now.After(entry.FirstFailedAt.Add(r.JobCreateRetryWindow)) {
			failEntry(entry, now, lifecyclev1alpha1.HookReasonJobCreateTimedOut, entry.Message)
			r.Recorder.Eventf(marker, corev1.EventTypeWarning, lifecyclev1alpha1.HookReasonJobCreateTimedOut,
				"Gave up creating Job for hook %s/%s after %s: %s", entry.Namespace, entry.Name, r.JobCreateRetryWindow, entry.Message)
			continue
		}

		hook, ok := byKey[types.NamespacedName{Namespace: entry.Namespace, Name: entry.Name}]
		// discoverHooks already skipped entries whose hook is gone, so a miss
		// here means the hook disappeared between the list and now
		if !ok {
			continue
		}

		job := buildJob(hook, marker)
		// The controller reference is what routes this Job's events back to the
		// marker through Owns, and what garbage-collects the Job with the marker.
		if err := controllerutil.SetControllerReference(marker, job, r.Scheme); err != nil {
			return blocked, fmt.Errorf("failed to set owner on Job %s/%s: %w", job.Namespace, job.Name, err)
		}

		err := r.Create(ctx, job)
		switch {
		case err == nil:
			log.Info("created cleanup Job", "job", job.Name, "namespace", job.Namespace, "hook", entry.Name, "stage", entry.Stage)
			r.Recorder.Eventf(marker, corev1.EventTypeNormal, "JobCreated", "Created Job %s/%s for hook %s stage %s", job.Namespace, job.Name, entry.Name, entry.Stage)
		case apierrors.IsAlreadyExists(err):
			log.Info("adopting existing cleanup Job", "job", job.Name, "namespace", job.Namespace)
		case permanentCreateError(err):
			log.Info("cleanup Job rejected by API server", "job", job.Name, "namespace", job.Namespace, "error", err.Error())
			failEntry(entry, now, lifecyclev1alpha1.HookReasonJobCreateRejected, err.Error())
			r.Recorder.Eventf(marker, corev1.EventTypeWarning, lifecyclev1alpha1.HookReasonJobCreateRejected,
				"Job %s/%s for hook %s rejected: %s", job.Namespace, job.Name, entry.Name, err.Error())
			continue
		default:
			log.Error(err, "failed to create cleanup Job, will retry", "job", job.Name, "namespace", job.Namespace)
			entry.Reason = lifecyclev1alpha1.HookReasonJobCreateFailed
			entry.Message = err.Error()
			if entry.FirstFailedAt == nil {
				first := metav1.NewTime(now)
				entry.FirstFailedAt = &first
			}
			r.Recorder.Eventf(marker, corev1.EventTypeWarning, lifecyclev1alpha1.HookReasonJobCreateFailed,
				"Failed to create Job %s/%s for hook %s: %s", job.Namespace, job.Name, entry.Name, err.Error())
			blocked = true
			continue
		}

		started := metav1.NewTime(now)
		entry.State = lifecyclev1alpha1.HookRunning
		entry.Job = job.Name
		entry.StartedAt = &started
		entry.Reason = ""
		entry.Message = ""
		entry.FirstFailedAt = nil
	}

	return blocked, nil
}

// failEntry moves an entry to Failed with the given reason and message
func failEntry(entry *lifecyclev1alpha1.HookStatus, now time.Time, reason, message string) {
	finished := metav1.NewTime(now)
	entry.State = lifecyclev1alpha1.HookFailed
	entry.Reason = reason
	entry.Message = message
	entry.FinishedAt = &finished
}

// observeJobs copies the outcome of each Running entry's Job onto the entry.
// The request does not say which Job changed, so every Running entry is looked
// up. A Job with a Complete condition makes the entry Succeeded, a Failed
// condition makes it Failed with the Job controller's reason, and a Job that is
// gone makes it Failed with JobLost, since there is no way to know whether the
// script ran. Jobs without a terminal condition are left alone.
func (r *UserDeletionReconciler) observeJobs(ctx context.Context, marker *lifecyclev1alpha1.UserDeletion) error {
	log := logf.FromContext(ctx)

	for i := range marker.Status.Hooks {
		entry := &marker.Status.Hooks[i]
		if entry.State != lifecyclev1alpha1.HookRunning {
			continue
		}

		var job batchv1.Job
		key := client.ObjectKey{Namespace: entry.Namespace, Name: entry.Job}
		if err := r.Get(ctx, key, &job); err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get Job %s: %w", key, err)
			}
			// A Job created moments ago may not be in the cache yet. Only call
			// it lost once it has been missing for longer than any cache lag.
			if entry.StartedAt != nil && time.Since(entry.StartedAt.Time) < jobLostGrace {
				continue
			}
			failEntry(entry, time.Now(), lifecyclev1alpha1.HookReasonJobLost,
				"Job disappeared before the controller saw it finish; the cleanup may or may not have run")
			r.Recorder.Eventf(marker, corev1.EventTypeWarning, lifecyclev1alpha1.HookReasonJobLost,
				"Job %s for hook %s/%s disappeared before finishing", key, entry.Namespace, entry.Name)
			continue
		}

		// The Job controller records when it actually started the Job, which
		// can be later than when the create was issued.
		if job.Status.StartTime != nil {
			entry.StartedAt = job.Status.StartTime
		}

		switch {
		case jobCondition(&job, batchv1.JobComplete) != nil:
			cond := jobCondition(&job, batchv1.JobComplete)
			entry.State = lifecyclev1alpha1.HookSucceeded
			entry.FinishedAt = finishedAt(&job, cond)
			entry.Reason = ""
			entry.Message = ""
			log.Info("cleanup Job succeeded", "job", key.String(), "hook", entry.Name, "stage", entry.Stage)
		case jobCondition(&job, batchv1.JobFailed) != nil:
			cond := jobCondition(&job, batchv1.JobFailed)
			entry.State = lifecyclev1alpha1.HookFailed
			entry.FinishedAt = finishedAt(&job, cond)
			entry.Reason = cond.Reason
			entry.Message = cond.Message
			log.Info("cleanup Job failed", "job", key.String(), "hook", entry.Name, "stage", entry.Stage, "reason", cond.Reason)
			r.Recorder.Eventf(marker, corev1.EventTypeWarning, "JobFailed",
				"Job %s for hook %s/%s failed: %s: %s", key, entry.Namespace, entry.Name, cond.Reason, cond.Message)
		}
	}

	return nil
}

// jobCondition returns the Job's condition of the given type if it is True.
func jobCondition(job *batchv1.Job, t batchv1.JobConditionType) *batchv1.JobCondition {
	for i := range job.Status.Conditions {
		c := &job.Status.Conditions[i]
		if c.Type == t && c.Status == corev1.ConditionTrue {
			return c
		}
	}
	return nil
}

// finishedAt picks the most reliable finish time: the Job's own completion
// time, then the condition's transition time, then now.
func finishedAt(job *batchv1.Job, cond *batchv1.JobCondition) *metav1.Time {
	if job.Status.CompletionTime != nil {
		return job.Status.CompletionTime
	}
	if !cond.LastTransitionTime.IsZero() {
		return &cond.LastTransitionTime
	}
	now := metav1.Now()
	return &now
}

// SetupWithManager registers the three sources that drive a marker: the
// marker itself, the Jobs it owns, and every UserCleanupHook.
func (r *UserDeletionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lifecyclev1alpha1.UserDeletion{}).
		// A Job carries a controller reference to its marker, so a Job finishing
		// enqueues the marker. Nothing else tells the reconciler a stage is done.
		Owns(&batchv1.Job{}).
		// A hook installed or changed mid-flight must be picked up by every
		// deletion still in progress, so hook events fan out to all open markers.
		Watches(
			&lifecyclev1alpha1.UserCleanupHook{},
			handler.EnqueueRequestsFromMapFunc(r.hookToOpenMarkers),
		).
		Named("lifecycle-userdeletion").
		Complete(r)
}

// hookToOpenMarkers maps any UserCleanupHook event to every UserDeletion that
// has not completed. Completed markers are tombstones and never run new Jobs,
// so they are left alone. Which hook changed does not matter: the reconcile
// pass lists hooks itself.
func (r *UserDeletionReconciler) hookToOpenMarkers(ctx context.Context, _ client.Object) []reconcile.Request {
	var markers lifecyclev1alpha1.UserDeletionList
	if err := r.List(ctx, &markers); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list UserDeletions for hook event")
		return nil
	}

	requests := make([]reconcile.Request, 0, len(markers.Items))
	for i := range markers.Items {
		if markers.Items[i].Status.Phase == lifecyclev1alpha1.UserDeletionCompleted {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: markers.Items[i].Name},
		})
	}
	return requests
}
