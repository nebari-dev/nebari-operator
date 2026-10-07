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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

func testHook(name, namespace string, stage lifecyclev1alpha1.CleanupStage) *lifecyclev1alpha1.UserCleanupHook {
	return &lifecyclev1alpha1.UserCleanupHook{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: lifecyclev1alpha1.UserCleanupHookSpec{
			Stage:  stage,
			DryRun: true,
			Template: lifecyclev1alpha1.PodTemplate{
				Metadata: lifecyclev1alpha1.PodTemplateMetadata{
					Labels:      map[string]string{"hub.jupyter.org/network-access-hub": "true"},
					Annotations: map[string]string{"note": "kept"},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{{Name: "init", Image: "busybox"}},
					Containers: []corev1.Container{
						{Name: "a", Image: "img", Env: []corev1.EnvVar{{Name: "EXISTING", Value: "keep"}}},
						{Name: "b", Image: "img"},
					},
				},
			},
		},
	}
}

// The hook's pod labels and annotations must reach the Job's pod template,
// a NetworkPolicy may depend on them.
func TestBuildJobCarriesTemplateMetadata(t *testing.T) {
	hook := testHook("hub", "ns", lifecyclev1alpha1.CleanupStageDisable)
	job := buildJob(hook, testMarker("u1"))

	if got := job.Spec.Template.Labels["hub.jupyter.org/network-access-hub"]; got != "true" {
		t.Errorf("pod label: got %q, want %q", got, "true")
	}
	if got := job.Spec.Template.Annotations["note"]; got != "kept" {
		t.Errorf("pod annotation: got %q, want %q", got, "kept")
	}
}

func testMarker(userID string) *lifecyclev1alpha1.UserDeletion {
	return &lifecyclev1alpha1.UserDeletion{
		ObjectMeta: metav1.ObjectMeta{Name: userID},
		Spec: lifecyclev1alpha1.UserDeletionSpec{
			UserID:    userID,
			Username:  "jane",
			DeletedAt: metav1.NewTime(time.Date(2026, 9, 29, 8, 10, 47, 0, time.UTC)),
		},
	}
}

func envValue(env []corev1.EnvVar, name string) (string, bool) {
	for _, e := range env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

func TestBuildJob(t *testing.T) {
	hook := testHook("jupyterhub", "data-science", lifecyclev1alpha1.CleanupStageDelete)
	marker := testMarker("91fb5e10-5017-48ad-8568-0e9210a306cb")

	job := buildJob(hook, marker)

	if job.Namespace != "data-science" {
		t.Errorf("namespace: got %q", job.Namespace)
	}
	if job.Labels[lifecyclev1alpha1.LabelUserID] != marker.Spec.UserID ||
		job.Labels[lifecyclev1alpha1.LabelHook] != "jupyterhub" ||
		job.Labels[lifecyclev1alpha1.LabelStage] != "delete" {
		t.Errorf("labels: got %v", job.Labels)
	}
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy: got %q, want default Never", job.Spec.Template.Spec.RestartPolicy)
	}
	if *job.Spec.Completions != 1 || *job.Spec.Parallelism != 1 {
		t.Errorf("completions/parallelism must be 1")
	}

	want := map[string]string{
		lifecyclev1alpha1.EnvVarUserID:       marker.Spec.UserID,
		lifecyclev1alpha1.EnvVarUsername:     "jane",
		lifecyclev1alpha1.EnvVarDeletedAt:    "2026-09-29T08:10:47Z",
		lifecyclev1alpha1.EnvVarStage:        "delete",
		lifecyclev1alpha1.EnvVarDryRun:       "true",
		lifecyclev1alpha1.EnvVarUserDeletion: marker.Name,
	}
	all := append([]corev1.Container{}, job.Spec.Template.Spec.Containers...)
	all = append(all, job.Spec.Template.Spec.InitContainers...)
	for _, c := range all {
		for name, value := range want {
			got, ok := envValue(c.Env, name)
			if !ok || got != value {
				t.Errorf("container %s: %s = %q, want %q", c.Name, name, got, value)
			}
		}
	}
	if got, _ := envValue(job.Spec.Template.Spec.Containers[0].Env, "EXISTING"); got != "keep" {
		t.Errorf("existing env var must be preserved, got %q", got)
	}

	// The hook must not have been modified by rendering.
	if len(hook.Spec.Template.Spec.Containers[0].Env) != 1 {
		t.Errorf("buildJob mutated the hook's template: %v", hook.Spec.Template.Spec.Containers[0].Env)
	}
}

func TestJobName(t *testing.T) {
	marker := testMarker("91fb5e10-5017-48ad-8568-0e9210a306cb")
	hook := testHook("jupyterhub", "data-science", lifecyclev1alpha1.CleanupStageDelete)

	name := jobName(marker, hook)

	if !strings.HasPrefix(name, "userdel-91fb5e10-") {
		t.Errorf("name %q should start with prefix and user id segment", name)
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		t.Errorf("name %q is not a valid DNS label: %v", name, errs)
	}
	if name != jobName(marker, hook) {
		t.Errorf("name must be deterministic")
	}

	// Anything that identifies the Job changes the name.
	otherStage := testHook("jupyterhub", "data-science", lifecyclev1alpha1.CleanupStageDisable)
	otherNS := testHook("jupyterhub", "other", lifecyclev1alpha1.CleanupStageDelete)
	otherHook := testHook("nebi", "data-science", lifecyclev1alpha1.CleanupStageDelete)
	otherUser := testMarker("aaaaaaaa-0000-0000-0000-000000000000")
	for _, n := range []string{
		jobName(marker, otherStage), jobName(marker, otherNS), jobName(marker, otherHook), jobName(otherUser, hook),
	} {
		if n == name {
			t.Errorf("name %q collided with a different job identity", n)
		}
	}

	// A very long hook name must not push the Job name over the limit.
	long := testHook(strings.Repeat("x", 200), "data-science", lifecyclev1alpha1.CleanupStageDelete)
	if n := jobName(marker, long); len(n) > validation.DNS1123LabelMaxLength {
		t.Errorf("name %q exceeds %d characters", n, validation.DNS1123LabelMaxLength)
	}
}
