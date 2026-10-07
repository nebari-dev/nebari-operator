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
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

const (
	jobNamePrefix       = "userdel"
	placeholderUserID   = "18a5a8cc-f27f-44d7-b3d7-8366b2fca605"
	placeholderUsername = "delete-me"
)

// placeholderMarker returns a dummy UserDeletion for dry-run validation
func placeholderMarker() *lifecyclev1alpha1.UserDeletion {
	return &lifecyclev1alpha1.UserDeletion{
		ObjectMeta: metav1.ObjectMeta{Name: placeholderUserID},
		Spec: lifecyclev1alpha1.UserDeletionSpec{
			UserID:    placeholderUserID,
			Username:  placeholderUsername,
			DeletedAt: metav1.NewTime(time.Now()),
		},
	}
}

// jobName returns the deterministic name of the Job for one marker and hook.
// A replayed create collides on it and fails with AlreadyExists. The name is
// a readable prefix, the first segment of the user id, and a short hash of
// everything that identifies the Job, so it stays well under the 63 character
// limit regardless of hook name length.
func jobName(marker *lifecyclev1alpha1.UserDeletion, hook *lifecyclev1alpha1.UserCleanupHook) string {
	sum := sha256.Sum256([]byte(marker.Name + "/" + hook.Namespace + "/" + hook.Name + "/" + string(hook.Spec.Stage)))
	short := hex.EncodeToString(sum[:])[:8]

	userSegment := marker.Spec.UserID
	if len(userSegment) > 8 {
		userSegment = userSegment[:8]
	}
	return jobNamePrefix + "-" + userSegment + "-" + short
}

// buildJob renders the Job that runs hook's cleanup for marker. It is pure:
// the same inputs always produce the same Job, and neither input is modified.
// Callers decide the rest: the UserCleanupHook reconciler swaps the name for
// GenerateName before a dry-run, the UserDeletion reconciler sets the owner
// reference before a real create.
func buildJob(hook *lifecyclev1alpha1.UserCleanupHook, marker *lifecyclev1alpha1.UserDeletion) *batchv1.Job {
	// Make sure to use a copy to avoid modifying the original object
	tmpl := hook.Spec.Template.DeepCopy()
	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      tmpl.Metadata.Labels,
			Annotations: tmpl.Metadata.Annotations,
		},
		Spec: tmpl.Spec,
	}

	// Fill default RestartPolicy if not set
	if podTemplate.Spec.RestartPolicy == "" {
		podTemplate.Spec.RestartPolicy = corev1.RestartPolicyNever
	}

	envVars := []corev1.EnvVar{
		{Name: lifecyclev1alpha1.EnvVarUserID, Value: marker.Spec.UserID},
		{Name: lifecyclev1alpha1.EnvVarUsername, Value: marker.Spec.Username},
		{Name: lifecyclev1alpha1.EnvVarDeletedAt, Value: marker.Spec.DeletedAt.UTC().Format(time.RFC3339)},
		{Name: lifecyclev1alpha1.EnvVarStage, Value: string(hook.Spec.Stage)},
		{Name: lifecyclev1alpha1.EnvVarDryRun, Value: strconv.FormatBool(hook.Spec.DryRun)},
		{Name: lifecyclev1alpha1.EnvVarUserDeletion, Value: marker.Name},
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
			Name:      jobName(marker, hook),
			Namespace: hook.Namespace,
			Labels: map[string]string{
				lifecyclev1alpha1.LabelUserID: marker.Spec.UserID,
				lifecyclev1alpha1.LabelHook:   hook.Name,
				lifecyclev1alpha1.LabelStage:  string(hook.Spec.Stage),
			},
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
