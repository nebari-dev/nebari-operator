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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

var _ = Describe("UserDeletion Controller", func() {
	const gracePeriod = 30 * 24 * time.Hour

	var reconciler *UserDeletionReconciler

	// deletedAt is fixed and in the past so dueAt is predictable and a
	// requeue duration can be compared against it.
	deletedAt := time.Now().Add(-time.Hour).Truncate(time.Second).UTC()

	// createMarker stores a marker named by userID and registers its deletion.
	createMarker := func(userID, username string) *lifecyclev1alpha1.UserDeletion {
		marker := &lifecyclev1alpha1.UserDeletion{
			ObjectMeta: metav1.ObjectMeta{Name: userID},
			Spec: lifecyclev1alpha1.UserDeletionSpec{
				UserID:    userID,
				Username:  username,
				DeletedAt: metav1.NewTime(deletedAt),
			},
		}
		Expect(k8sClient.Create(ctx, marker)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, marker))).To(Succeed())
		})
		return marker
	}

	getMarker := func(userID string) *lifecyclev1alpha1.UserDeletion {
		marker := &lifecyclev1alpha1.UserDeletion{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: userID}, marker)).To(Succeed())
		return marker
	}

	reconcileMarker := func(userID string) reconcile.Result {
		result, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: userID},
		})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	BeforeEach(func() {
		reconciler = &UserDeletionReconciler{
			Client:               k8sClient,
			Scheme:               scheme.Scheme,
			Recorder:             record.NewFakeRecorder(10),
			GracePeriod:          gracePeriod,
			JobCreateRetryWindow: 7 * 24 * time.Hour,
			MarkerRetention:      90 * 24 * time.Hour,
		}
	})

	It("schedules the delayed stage and marks the identifiers complete", func() {
		createMarker("ud-scheduled", "jane")

		result := reconcileMarker("ud-scheduled")

		marker := getMarker("ud-scheduled")
		Expect(marker.Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionPending))
		Expect(marker.Status.DueAt).NotTo(BeNil())
		Expect(marker.Status.DueAt.Time).To(BeTemporally("==", deletedAt.Add(gracePeriod)))
		Expect(marker.Status.ObservedGeneration).To(Equal(marker.Generation))

		cond := meta.FindStatusCondition(marker.Status.Conditions, lifecyclev1alpha1.ConditionTypeIdentifiersComplete)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonUsernameKnown))

		// The requeue must land on dueAt. Measured from now rather than from a
		// fixed duration, since deletedAt was computed when the suite started
		// and envtest startup time would otherwise eat into the tolerance.
		Expect(result.RequeueAfter).To(BeNumerically("~", time.Until(marker.Status.DueAt.Time), 10*time.Second))
	})

	It("flags a marker whose username is unknown", func() {
		createMarker("ud-no-username", "")

		reconcileMarker("ud-no-username")

		cond := meta.FindStatusCondition(getMarker("ud-no-username").Status.Conditions, lifecyclev1alpha1.ConditionTypeIdentifiersComplete)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonUsernameMissing))
	})

	It("does not write status when nothing changed", func() {
		createMarker("ud-idempotent", "jane")

		reconcileMarker("ud-idempotent")
		first := getMarker("ud-idempotent")

		reconcileMarker("ud-idempotent")
		second := getMarker("ud-idempotent")

		Expect(second.ResourceVersion).To(Equal(first.ResourceVersion))
		Expect(second.Status.DueAt.Time).To(BeTemporally("==", first.Status.DueAt.Time))
	})

	It("keeps dueAt frozen when the grace period changes", func() {
		createMarker("ud-frozen", "jane")
		reconcileMarker("ud-frozen")
		first := getMarker("ud-frozen")

		reconciler.GracePeriod = 7 * 24 * time.Hour
		reconcileMarker("ud-frozen")

		Expect(getMarker("ud-frozen").Status.DueAt.Time).To(BeTemporally("==", first.Status.DueAt.Time))
	})

	// acceptedHook stores a hook and marks it Accepted by hand, standing in
	// for the UserCleanupHook reconciler, and registers its deletion.
	acceptedHook := func(name string, stage lifecyclev1alpha1.CleanupStage) *lifecyclev1alpha1.UserCleanupHook {
		hook := newHook(name)
		hook.Spec.Stage = stage
		Expect(k8sClient.Create(ctx, hook)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, hook))).To(Succeed())
		})
		meta.SetStatusCondition(&hook.Status.Conditions, metav1.Condition{
			Type:   lifecyclev1alpha1.ConditionTypeAccepted,
			Status: metav1.ConditionTrue,
			Reason: lifecyclev1alpha1.ReasonTemplateValid,
		})
		Expect(k8sClient.Status().Update(ctx, hook)).To(Succeed())
		return hook
	}

	findEntry := func(marker *lifecyclev1alpha1.UserDeletion, name string) *lifecyclev1alpha1.HookStatus {
		for i := range marker.Status.Hooks {
			if marker.Status.Hooks[i].Name == name && marker.Status.Hooks[i].Namespace == testNamespace {
				return &marker.Status.Hooks[i]
			}
		}
		return nil
	}

	It("records an entry for each accepted hook", func() {
		acceptedHook("ud-hook-disable", lifecyclev1alpha1.CleanupStageDisable)
		acceptedHook("ud-hook-delete", lifecyclev1alpha1.CleanupStageDelete)
		createMarker("ud-discover", "jane")

		reconcileMarker("ud-discover")

		marker := getMarker("ud-discover")
		Expect(marker.Status.Hooks).To(HaveLen(2))

		// The disable stage is due at once, so its entry has already moved on
		// to Running in the same pass. The delete stage waits for dueAt.
		disable := findEntry(marker, "ud-hook-disable")
		Expect(disable).NotTo(BeNil())
		Expect(disable.Stage).To(Equal(lifecyclev1alpha1.CleanupStageDisable))
		Expect(disable.State).To(Equal(lifecyclev1alpha1.HookRunning))
		DeferCleanup(func() {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: disable.Job, Namespace: testNamespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job))).To(Succeed())
		})

		del := findEntry(marker, "ud-hook-delete")
		Expect(del).NotTo(BeNil())
		Expect(del.Stage).To(Equal(lifecyclev1alpha1.CleanupStageDelete))
		Expect(del.State).To(Equal(lifecyclev1alpha1.HookPending))
	})

	It("ignores hooks that are not accepted", func() {
		rejected := newHook("ud-hook-rejected")
		Expect(k8sClient.Create(ctx, rejected)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, rejected))).To(Succeed())
		})
		createMarker("ud-ignore-rejected", "jane")

		reconcileMarker("ud-ignore-rejected")

		Expect(findEntry(getMarker("ud-ignore-rejected"), "ud-hook-rejected")).To(BeNil())
	})

	It("picks up a hook installed after the marker", func() {
		createMarker("ud-late-hook", "jane")
		reconcileMarker("ud-late-hook")
		Expect(getMarker("ud-late-hook").Status.Hooks).To(BeEmpty())

		acceptedHook("ud-hook-late", lifecyclev1alpha1.CleanupStageDelete)
		reconcileMarker("ud-late-hook")

		Expect(findEntry(getMarker("ud-late-hook"), "ud-hook-late")).NotTo(BeNil())
	})

	It("skips a pending entry whose hook was removed", func() {
		hook := acceptedHook("ud-hook-removed", lifecyclev1alpha1.CleanupStageDelete)
		createMarker("ud-removed", "jane")
		reconcileMarker("ud-removed")
		Expect(findEntry(getMarker("ud-removed"), "ud-hook-removed").State).To(Equal(lifecyclev1alpha1.HookPending))

		Expect(k8sClient.Delete(ctx, hook)).To(Succeed())
		reconcileMarker("ud-removed")

		entry := findEntry(getMarker("ud-removed"), "ud-hook-removed")
		Expect(entry).NotTo(BeNil(), "the entry must survive as a record")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookSkipped))
		Expect(entry.Reason).To(Equal(lifecyclev1alpha1.HookReasonHookRemoved))
		Expect(entry.FinishedAt).NotTo(BeNil())
	})

	It("leaves a terminal entry alone when its hook is removed", func() {
		hook := acceptedHook("ud-hook-done", lifecyclev1alpha1.CleanupStageDisable)
		createMarker("ud-terminal", "jane")
		reconcileMarker("ud-terminal")

		// The disable Job was created on that pass. Pretend it finished, since
		// nothing runs Jobs in envtest, and clean it up afterwards.
		marker := getMarker("ud-terminal")
		DeferCleanup(func() {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: findEntry(marker, "ud-hook-done").Job, Namespace: testNamespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job))).To(Succeed())
		})
		findEntry(marker, "ud-hook-done").State = lifecyclev1alpha1.HookSucceeded
		Expect(k8sClient.Status().Update(ctx, marker)).To(Succeed())

		Expect(k8sClient.Delete(ctx, hook)).To(Succeed())
		reconcileMarker("ud-terminal")

		Expect(findEntry(getMarker("ud-terminal"), "ud-hook-done").State).To(Equal(lifecyclev1alpha1.HookSucceeded))
	})

	getJob := func(name string) *batchv1.Job {
		job := &batchv1.Job{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, job)).To(Succeed())
		return job
	}

	It("creates the disable Job immediately and records it", func() {
		acceptedHook("ud-job-disable", lifecyclev1alpha1.CleanupStageDisable)
		marker := createMarker("ud-job-now", "jane")

		reconcileMarker("ud-job-now")

		entry := findEntry(getMarker("ud-job-now"), "ud-job-disable")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookRunning))
		Expect(entry.Job).NotTo(BeEmpty())
		Expect(entry.StartedAt).NotTo(BeNil())

		job := getJob(entry.Job)
		Expect(job.Labels[lifecyclev1alpha1.LabelUserID]).To(Equal(marker.Spec.UserID))
		Expect(job.Labels[lifecyclev1alpha1.LabelStage]).To(Equal("disable"))
		Expect(metav1.IsControlledBy(job, getMarker("ud-job-now"))).To(BeTrue(), "Job must be owned by the marker")
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job))).To(Succeed()) })
	})

	It("holds the delete Job until the grace period has elapsed", func() {
		acceptedHook("ud-job-delete", lifecyclev1alpha1.CleanupStageDelete)
		createMarker("ud-job-later", "jane")

		reconcileMarker("ud-job-later")

		entry := findEntry(getMarker("ud-job-later"), "ud-job-delete")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookPending))
		Expect(entry.Job).To(BeEmpty())
	})

	It("creates the delete Job once dueAt has passed", func() {
		acceptedHook("ud-job-due", lifecyclev1alpha1.CleanupStageDelete)
		createMarker("ud-job-past-due", "jane")

		// A zero grace period makes dueAt equal deletedAt, an hour ago.
		reconciler.GracePeriod = 0
		reconcileMarker("ud-job-past-due")

		entry := findEntry(getMarker("ud-job-past-due"), "ud-job-due")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookRunning))
		job := getJob(entry.Job)
		Expect(job.Labels[lifecyclev1alpha1.LabelStage]).To(Equal("delete"))
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job))).To(Succeed()) })
	})

	It("adopts an existing Job instead of creating a second one", func() {
		acceptedHook("ud-job-adopt", lifecyclev1alpha1.CleanupStageDisable)
		createMarker("ud-job-adopted", "jane")

		reconcileMarker("ud-job-adopted")
		first := getMarker("ud-job-adopted")
		jobName := findEntry(first, "ud-job-adopt").Job
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: testNamespace}}))).To(Succeed())
		})

		// Simulate a lost status write: reset the entry to Pending and reconcile.
		findEntry(first, "ud-job-adopt").State = lifecyclev1alpha1.HookPending
		findEntry(first, "ud-job-adopt").Job = ""
		Expect(k8sClient.Status().Update(ctx, first)).To(Succeed())

		reconcileMarker("ud-job-adopted")

		entry := findEntry(getMarker("ud-job-adopted"), "ud-job-adopt")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookRunning))
		Expect(entry.Job).To(Equal(jobName))

		var jobs batchv1.JobList
		Expect(k8sClient.List(ctx, &jobs, client.InNamespace(testNamespace), client.MatchingLabels{lifecyclev1alpha1.LabelUserID: "ud-job-adopted"})).To(Succeed())
		Expect(jobs.Items).To(HaveLen(1))
	})

	It("ignores an accepted hook in a namespace that is not managed", func() {
		// Accepted by hand, as if the namespace lost its label after validation.
		hook := newHook("ud-hook-unmanaged")
		hook.Namespace = unmanagedNamespace
		hook.Spec.Stage = lifecyclev1alpha1.CleanupStageDisable
		Expect(k8sClient.Create(ctx, hook)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, hook))).To(Succeed()) })
		meta.SetStatusCondition(&hook.Status.Conditions, metav1.Condition{
			Type: lifecyclev1alpha1.ConditionTypeAccepted, Status: metav1.ConditionTrue, Reason: lifecyclev1alpha1.ReasonTemplateValid,
		})
		Expect(k8sClient.Status().Update(ctx, hook)).To(Succeed())
		createMarker("ud-unmanaged", "jane")

		reconcileMarker("ud-unmanaged")

		for _, entry := range getMarker("ud-unmanaged").Status.Hooks {
			Expect(entry.Namespace).NotTo(Equal(unmanagedNamespace))
		}
		var jobs batchv1.JobList
		Expect(k8sClient.List(ctx, &jobs, client.InNamespace(unmanagedNamespace))).To(Succeed())
		Expect(jobs.Items).To(BeEmpty())
	})

	It("marks an entry Failed when the API server rejects the Job", func() {
		// A dangling volume mount passes the CRD schema but fails Job
		// validation, so the create is rejected with Invalid.
		hook := newHook("ud-hook-rejected-job")
		hook.Spec.Stage = lifecyclev1alpha1.CleanupStageDisable
		withDanglingVolumeMount(hook)
		Expect(k8sClient.Create(ctx, hook)).To(Succeed())
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, hook))).To(Succeed()) })
		meta.SetStatusCondition(&hook.Status.Conditions, metav1.Condition{
			Type: lifecyclev1alpha1.ConditionTypeAccepted, Status: metav1.ConditionTrue, Reason: lifecyclev1alpha1.ReasonTemplateValid,
		})
		Expect(k8sClient.Status().Update(ctx, hook)).To(Succeed())
		createMarker("ud-rejected-job", "jane")

		result := reconcileMarker("ud-rejected-job")

		entry := findEntry(getMarker("ud-rejected-job"), "ud-hook-rejected-job")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookFailed))
		Expect(entry.Reason).To(Equal(lifecyclev1alpha1.HookReasonJobCreateRejected))
		Expect(entry.Message).To(ContainSubstring("scripts"))
		Expect(entry.FinishedAt).NotTo(BeNil())
		Expect(entry.Job).To(BeEmpty())
		// Permanent failures do not ask for a quick retry.
		Expect(result.RequeueAfter).To(BeNumerically(">", time.Hour))
	})

	It("gives up on a transiently failing entry after the retry window", func() {
		acceptedHook("ud-hook-timeout", lifecyclev1alpha1.CleanupStageDelete)
		createMarker("ud-timeout", "jane")
		reconciler.GracePeriod = 0
		reconciler.JobCreateRetryWindow = 0

		// The first pass creates the Job. Then pretend an earlier pass had
		// recorded a transient failure instead, by resetting the entry.
		reconcileMarker("ud-timeout")
		marker := getMarker("ud-timeout")
		entry := findEntry(marker, "ud-hook-timeout")
		createdJob := entry.Job
		DeferCleanup(func() {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: createdJob, Namespace: testNamespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job))).To(Succeed())
		})
		firstFailed := metav1.NewTime(time.Now().Add(-time.Hour))
		entry.State = lifecyclev1alpha1.HookPending
		entry.Job = ""
		entry.StartedAt = nil
		entry.Reason = lifecyclev1alpha1.HookReasonJobCreateFailed
		entry.Message = "exceeded quota"
		entry.FirstFailedAt = &firstFailed
		Expect(k8sClient.Status().Update(ctx, marker)).To(Succeed())

		// With a one-hour-old first failure and a window shorter than that,
		// the entry is given up on before any create is attempted.
		reconciler.JobCreateRetryWindow = 30 * time.Minute
		reconcileMarker("ud-timeout")

		entry = findEntry(getMarker("ud-timeout"), "ud-hook-timeout")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookFailed))
		Expect(entry.Reason).To(Equal(lifecyclev1alpha1.HookReasonJobCreateTimedOut))
		Expect(entry.Message).To(Equal("exceeded quota"), "the last error is kept")
	})

	It("keeps retrying a transiently failing entry inside the window", func() {
		acceptedHook("ud-hook-retrying", lifecyclev1alpha1.CleanupStageDisable)
		createMarker("ud-retrying", "jane")

		// Seed a recent transient failure. The create will now succeed, which
		// proves a Pending entry with FirstFailedAt still gets its attempt and
		// that success clears the failure bookkeeping.
		reconcileMarker("ud-retrying")
		marker := getMarker("ud-retrying")
		entry := findEntry(marker, "ud-hook-retrying")
		createdJob := entry.Job
		DeferCleanup(func() {
			job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: createdJob, Namespace: testNamespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job))).To(Succeed())
		})
		recent := metav1.NewTime(time.Now().Add(-time.Minute))
		entry.State = lifecyclev1alpha1.HookPending
		entry.Job = ""
		entry.StartedAt = nil
		entry.Reason = lifecyclev1alpha1.HookReasonJobCreateFailed
		entry.Message = "exceeded quota"
		entry.FirstFailedAt = &recent
		Expect(k8sClient.Status().Update(ctx, marker)).To(Succeed())

		reconcileMarker("ud-retrying")

		entry = findEntry(getMarker("ud-retrying"), "ud-hook-retrying")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookRunning), "the existing Job is adopted")
		Expect(entry.Reason).To(BeEmpty())
		Expect(entry.Message).To(BeEmpty())
		Expect(entry.FirstFailedAt).To(BeNil())
	})

	// runningEntry creates a disable hook and marker, reconciles once so the
	// Job exists, and returns the entry and the Job. Nothing runs Jobs in
	// envtest, so specs set the Job's terminal condition by hand.
	runningEntry := func(hookName, userID string) (*lifecyclev1alpha1.HookStatus, *batchv1.Job) {
		acceptedHook(hookName, lifecyclev1alpha1.CleanupStageDisable)
		createMarker(userID, "jane")
		reconcileMarker(userID)
		entry := findEntry(getMarker(userID), hookName)
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookRunning))
		job := getJob(entry.Job)
		DeferCleanup(func() { Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, job))).To(Succeed()) })
		return entry, job
	}

	// setJobCondition writes a terminal condition the way the Job controller
	// would. Since Kubernetes 1.31 the API server requires the interim
	// SuccessCriteriaMet or FailureTarget condition before the terminal one.
	setJobCondition := func(job *batchv1.Job, t batchv1.JobConditionType, reason, message string) {
		now := metav1.Now()
		job.Status.StartTime = &now
		interim := batchv1.JobSuccessCriteriaMet
		if t == batchv1.JobFailed {
			interim = batchv1.JobFailureTarget
		}
		job.Status.Conditions = append(job.Status.Conditions,
			batchv1.JobCondition{Type: interim, Status: corev1.ConditionTrue, Reason: reason, Message: message, LastTransitionTime: now},
			batchv1.JobCondition{Type: t, Status: corev1.ConditionTrue, Reason: reason, Message: message, LastTransitionTime: now},
		)
		if t == batchv1.JobComplete {
			job.Status.CompletionTime = &now
		}
		Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())
	}

	It("records a completed Job as Succeeded", func() {
		_, job := runningEntry("ud-hook-complete", "ud-complete")

		setJobCondition(job, batchv1.JobComplete, "", "")
		reconcileMarker("ud-complete")

		entry := findEntry(getMarker("ud-complete"), "ud-hook-complete")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookSucceeded))
		Expect(entry.FinishedAt).NotTo(BeNil())
		Expect(entry.StartedAt.Time).To(BeTemporally("==", job.Status.StartTime.Time), "startedAt comes from the Job")
		Expect(entry.Reason).To(BeEmpty())
	})

	It("records a failed Job with the Job controller's reason", func() {
		_, job := runningEntry("ud-hook-failed", "ud-failed")

		setJobCondition(job, batchv1.JobFailed, "BackoffLimitExceeded", "Job has reached the specified backoff limit")
		reconcileMarker("ud-failed")

		entry := findEntry(getMarker("ud-failed"), "ud-hook-failed")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookFailed))
		Expect(entry.Reason).To(Equal("BackoffLimitExceeded"))
		Expect(entry.Message).To(ContainSubstring("backoff limit"))
		Expect(entry.FinishedAt).NotTo(BeNil())
	})

	// backdateStart moves an entry's startedAt into the past so a missing Job
	// is treated as lost rather than as not yet visible in the cache.
	backdateStart := func(userID, hookName string, by time.Duration) {
		marker := getMarker(userID)
		started := metav1.NewTime(time.Now().Add(-by))
		findEntry(marker, hookName).StartedAt = &started
		Expect(k8sClient.Status().Update(ctx, marker)).To(Succeed())
	}

	It("marks a Job that disappeared as lost", func() {
		_, job := runningEntry("ud-hook-lost", "ud-lost")

		// A bare delete of a batch/v1 Job defaults to orphan propagation, which
		// leaves a finalizer for the garbage collector that envtest does not
		// run. Background propagation removes the object immediately.
		Expect(k8sClient.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		backdateStart("ud-lost", "ud-hook-lost", 2*jobLostGrace)
		reconcileMarker("ud-lost")

		entry := findEntry(getMarker("ud-lost"), "ud-hook-lost")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookFailed))
		Expect(entry.Reason).To(Equal(lifecyclev1alpha1.HookReasonJobLost))
		Expect(entry.FinishedAt).NotTo(BeNil())
	})

	It("does not call a Job lost while it may still be arriving in the cache", func() {
		_, job := runningEntry("ud-hook-lagging", "ud-lagging")

		Expect(k8sClient.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground))).To(Succeed())
		reconcileMarker("ud-lagging")

		entry := findEntry(getMarker("ud-lagging"), "ud-hook-lagging")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookRunning), "startedAt is seconds old, so a miss is cache lag")
	})

	It("keeps observing a running Job after its hook is removed", func() {
		_, job := runningEntry("ud-hook-gone-mid-run", "ud-gone-mid-run")

		hook := &lifecyclev1alpha1.UserCleanupHook{ObjectMeta: metav1.ObjectMeta{Name: "ud-hook-gone-mid-run", Namespace: testNamespace}}
		Expect(k8sClient.Delete(ctx, hook)).To(Succeed())
		reconcileMarker("ud-gone-mid-run")
		Expect(findEntry(getMarker("ud-gone-mid-run"), "ud-hook-gone-mid-run").State).To(Equal(lifecyclev1alpha1.HookRunning), "a running entry is not skipped")

		setJobCondition(job, batchv1.JobComplete, "", "")
		reconcileMarker("ud-gone-mid-run")

		Expect(findEntry(getMarker("ud-gone-mid-run"), "ud-hook-gone-mid-run").State).To(Equal(lifecyclev1alpha1.HookSucceeded))
	})

	It("leaves a Job without a terminal condition as Running", func() {
		_, _ = runningEntry("ud-hook-running", "ud-still-running")

		reconcileMarker("ud-still-running")

		entry := findEntry(getMarker("ud-still-running"), "ud-hook-running")
		Expect(entry.State).To(Equal(lifecyclev1alpha1.HookRunning))
		Expect(entry.FinishedAt).To(BeNil())
	})

	hooksSucceeded := func(userID string) *metav1.Condition {
		cond := meta.FindStatusCondition(getMarker(userID).Status.Conditions, lifecyclev1alpha1.ConditionTypeHooksSucceeded)
		Expect(cond).NotTo(BeNil())
		return cond
	}

	It("adds the finalizer on the first pass", func() {
		createMarker("ud-finalizer", "jane")
		reconcileMarker("ud-finalizer")
		Expect(getMarker("ud-finalizer").Finalizers).To(ContainElement(lifecyclev1alpha1.UserDeletionFinalizer))
	})

	It("completes with NoHooks once dueAt has passed and nothing is registered", func() {
		createMarker("ud-no-hooks", "jane")
		reconciler.GracePeriod = 0

		result := reconcileMarker("ud-no-hooks")

		marker := getMarker("ud-no-hooks")
		Expect(marker.Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionCompleted))
		Expect(marker.Status.CompletedAt).NotTo(BeNil())
		cond := hooksSucceeded("ud-no-hooks")
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonNoHooks))
		Expect(result.RequeueAfter).To(BeNumerically("~", reconciler.MarkerRetention, 10*time.Second), "tombstone requeue")
	})

	It("stays InProgress while a Job is running", func() {
		runningEntry("ud-hook-progress", "ud-in-progress")
		reconciler.GracePeriod = 0
		reconcileMarker("ud-in-progress")

		Expect(getMarker("ud-in-progress").Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionInProgress))
		Expect(hooksSucceeded("ud-in-progress").Reason).To(Equal(lifecyclev1alpha1.ReasonHooksPending))
	})

	It("does not complete before dueAt even when every entry is terminal", func() {
		_, job := runningEntry("ud-hook-early", "ud-early")
		setJobCondition(job, batchv1.JobComplete, "", "")

		reconcileMarker("ud-early")

		marker := getMarker("ud-early")
		Expect(findEntry(marker, "ud-hook-early").State).To(Equal(lifecyclev1alpha1.HookSucceeded))
		Expect(marker.Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionInProgress), "a hook may still register during the grace period")
		Expect(marker.Status.CompletedAt).To(BeNil())
	})

	It("completes with AllSucceeded after the Jobs finish", func() {
		// dueAt is frozen on the first pass, so the grace must be zero before it.
		reconciler.GracePeriod = 0
		_, job := runningEntry("ud-hook-all-ok", "ud-all-ok")
		setJobCondition(job, batchv1.JobComplete, "", "")

		reconcileMarker("ud-all-ok")

		Expect(getMarker("ud-all-ok").Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionCompleted))
		cond := hooksSucceeded("ud-all-ok")
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonAllSucceeded))
	})

	It("completes with HookFailed when a Job failed", func() {
		reconciler.GracePeriod = 0
		_, job := runningEntry("ud-hook-one-failed", "ud-one-failed")
		setJobCondition(job, batchv1.JobFailed, "DeadlineExceeded", "Job was active longer than specified deadline")

		reconcileMarker("ud-one-failed")

		Expect(getMarker("ud-one-failed").Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionCompleted))
		cond := hooksSucceeded("ud-one-failed")
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonHookFailed))
	})

	It("ignores hooks installed after completion", func() {
		createMarker("ud-tombstone", "jane")
		reconciler.GracePeriod = 0
		reconcileMarker("ud-tombstone")
		Expect(getMarker("ud-tombstone").Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionCompleted))

		acceptedHook("ud-hook-too-late", lifecyclev1alpha1.CleanupStageDisable)
		reconcileMarker("ud-tombstone")

		Expect(getMarker("ud-tombstone").Status.Hooks).To(BeEmpty())
	})

	It("deletes the tombstone once its retention has passed", func() {
		createMarker("ud-expired", "jane")
		reconciler.GracePeriod = 0
		reconciler.MarkerRetention = 0
		reconcileMarker("ud-expired")
		Expect(getMarker("ud-expired").Status.Phase).To(Equal(lifecyclev1alpha1.UserDeletionCompleted))

		// This pass issues the delete. Because of the finalizer that only sets
		// the deletion timestamp; the update event brings the reconciler back
		// once more, and that pass releases the finalizer.
		reconcileMarker("ud-expired")
		Expect(getMarker("ud-expired").DeletionTimestamp).NotTo(BeNil())
		reconcileMarker("ud-expired")

		err := k8sClient.Get(ctx, types.NamespacedName{Name: "ud-expired"}, &lifecyclev1alpha1.UserDeletion{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "marker should be gone, got: %v", err)
	})

	It("holds a deleted marker until its running Job finishes", func() {
		_, job := runningEntry("ud-hook-held", "ud-held")
		marker := getMarker("ud-held")
		Expect(k8sClient.Delete(ctx, marker)).To(Succeed())

		// The finalizer keeps the object while the Job runs.
		result := reconcileMarker("ud-held")
		held := getMarker("ud-held")
		Expect(held.DeletionTimestamp).NotTo(BeNil())
		Expect(result.RequeueAfter).To(Equal(finalizerRetryInterval))

		setJobCondition(job, batchv1.JobComplete, "", "")
		reconcileMarker("ud-held")

		err := k8sClient.Get(ctx, types.NamespacedName{Name: "ud-held"}, &lifecyclev1alpha1.UserDeletion{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "finalizer should have been released, got: %v", err)
	})

	It("fans a hook event out to open markers only", func() {
		createMarker("ud-open", "jane")
		completed := createMarker("ud-completed", "jane")

		// Mark one completed by hand; the controller cannot get there yet.
		completed.Status.Phase = lifecyclev1alpha1.UserDeletionCompleted
		Expect(k8sClient.Status().Update(ctx, completed)).To(Succeed())

		requests := reconciler.hookToOpenMarkers(ctx, &lifecyclev1alpha1.UserCleanupHook{})

		names := make([]string, 0, len(requests))
		for _, req := range requests {
			names = append(names, req.Name)
		}
		Expect(names).To(ContainElement("ud-open"))
		Expect(names).NotTo(ContainElement("ud-completed"))
	})
})
