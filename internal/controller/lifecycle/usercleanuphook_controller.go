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
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

// UserCleanupHookReconciler reconciles a UserCleanupHook object
type UserCleanupHookReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

const (
	placeHolderJobName  = "user-deletion"
	placeHolderUserID   = "18a5a8cc-f27f-44d7-b3d7-8366b2fca605"
	placeHolderUsername = "delete-me"
)

// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=create
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=lifecycle.nebari.dev,resources=usercleanuphooks,verbs=get;list;watch
// +kubebuilder:rbac:groups=lifecycle.nebari.dev,resources=usercleanuphooks/status,verbs=get;update;patch

// Reconcile validates a UserCleanupHook by rendering its pod template into a Job,
// the same way the UserDeletion controller will, and submitting it to the API server
// as a dry-run create. The outcome is recorded in the Accepted condition. Validation
// runs once per spec generation. It never creates real Jobs.
func (r *UserCleanupHookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the hook. Not found means it was deleted since the event was queued,
	// so there is nothing to do.
	var hook lifecyclev1alpha1.UserCleanupHook
	if err := r.Get(ctx, req.NamespacedName, &hook); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Return if there have been no changes
	if hook.Generation == hook.Status.ObservedGeneration {
		return ctrl.Result{}, nil
	}

	job := buildJob(&hook)
	err := r.Create(ctx, job, client.DryRunAll)

	var result ctrl.Result
	cond := metav1.Condition{
		Type:               lifecyclev1alpha1.ConditionTypeAccepted,
		ObservedGeneration: hook.Generation,
	}

	switch {
	// Job validation was successful and the UserCleanupHook Accepted status is true
	case err == nil:
		cond.Status = metav1.ConditionTrue
		cond.Reason = lifecyclev1alpha1.ReasonTemplateValid
		cond.Message = "rendered Job passed API server validation"
		r.Recorder.Event(&hook, corev1.EventTypeNormal, lifecyclev1alpha1.ReasonTemplateValid, cond.Message)
		log.Info("template accepted", "stage", hook.Spec.Stage)
	// Job validation failed and the UserCleanupHook Accepted status is false
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		cond.Status = metav1.ConditionFalse
		cond.Reason = lifecyclev1alpha1.ReasonTemplateInvalid
		cond.Message = err.Error()
		r.Recorder.Event(&hook, corev1.EventTypeWarning, lifecyclev1alpha1.ReasonTemplateInvalid, cond.Message)
		log.Info("template rejected by API server", "reason", err.Error())
	// Submitting the job failed for another reason and the UserCleanupHook Accepted status is unknown
	default:
		cond.Status = metav1.ConditionUnknown
		cond.Reason = lifecyclev1alpha1.ReasonValidationUnavailable
		cond.Message = err.Error()
		result = ctrl.Result{RequeueAfter: time.Minute}
		r.Recorder.Event(&hook, corev1.EventTypeWarning, lifecyclev1alpha1.ReasonValidationUnavailable, cond.Message)
		log.Error(err, "dry-run request failed, retrying in a minute")
	}

	meta.SetStatusCondition(&hook.Status.Conditions, cond)
	hook.Status.ObservedGeneration = hook.Generation
	if err := r.Status().Update(ctx, &hook); err != nil {
		log.Error(err, "failed to update UserCleanupHook status")
		return ctrl.Result{}, err
	}

	// Only the Unknown branch sets a RequeueAfter; the other two return an
	// empty result and wait for the next spec change.
	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UserCleanupHookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&lifecyclev1alpha1.UserCleanupHook{}).
		Named("lifecycle-usercleanuphook").
		Complete(r)
}

func buildJob(hook *lifecyclev1alpha1.UserCleanupHook) *batchv1.Job {
	// Make sure to use a copy to avoid modifying the original object
	podTemplate := *hook.Spec.Template.DeepCopy()

	// Fill default RestartPolicy if not set
	if podTemplate.Spec.RestartPolicy == "" {
		podTemplate.Spec.RestartPolicy = corev1.RestartPolicyNever
	}

	envVars := []corev1.EnvVar{
		{Name: lifecyclev1alpha1.EnvVarUserID, Value: placeHolderUserID},
		{Name: lifecyclev1alpha1.EnvVarUsername, Value: placeHolderUsername},
		{Name: lifecyclev1alpha1.EnvVarDryRun, Value: strconv.FormatBool(hook.Spec.DryRun)},
	}
	containers := podTemplate.Spec.Containers
	for i := range containers {
		containers[i].Env = append(containers[i].Env, envVars...)
	}
	initContainers := podTemplate.Spec.InitContainers
	for i := range initContainers {
		initContainers[i].Env = append(initContainers[i].Env, envVars...)
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      placeHolderJobName,
			Namespace: hook.Namespace,
		},
		Spec: batchv1.JobSpec{
			ActiveDeadlineSeconds:   hook.Spec.ActiveDeadlineSeconds,
			BackoffLimit:            hook.Spec.BackoffLimit,
			TTLSecondsAfterFinished: hook.Spec.TTLSecondsAfterFinished,
			Completions:             ptr.To(int32(1)),
			Parallelism:             ptr.To(int32(1)),
			Template:                podTemplate,
		},
	}
}
