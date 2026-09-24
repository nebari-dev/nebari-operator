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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

const testNamespace = "default"

// newHook returns a minimal valid hook. Tests mutate it to produce the
// invalid variants they need.
func newHook(name string) *lifecyclev1alpha1.UserCleanupHook {
	return &lifecyclev1alpha1.UserCleanupHook{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: lifecyclev1alpha1.UserCleanupHookSpec{
			Stage: lifecyclev1alpha1.CleanupStageDelete,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "cleanup",
						Image: "python:3.12-slim",
					}},
				},
			},
		},
	}
}

// createHook stores the hook and registers its deletion with the current spec.
func createHook(hook *lifecyclev1alpha1.UserCleanupHook) {
	Expect(k8sClient.Create(ctx, hook)).To(Succeed())
	DeferCleanup(func() {
		err := k8sClient.Delete(ctx, hook)
		Expect(client.IgnoreNotFound(err)).To(Succeed())
	})
}

// getHook fetches the current state of a hook by name.
func getHook(name string) *lifecyclev1alpha1.UserCleanupHook {
	hook := &lifecyclev1alpha1.UserCleanupHook{}
	key := types.NamespacedName{Name: name, Namespace: testNamespace}
	Expect(k8sClient.Get(ctx, key, hook)).To(Succeed())
	return hook
}

// withDanglingVolumeMount adds a mount for a volume that is never declared.
// The CRD schema accepts this and only the Job validator rejects it, which
// makes it the canonical "stored but not accepted" case.
func withDanglingVolumeMount(hook *lifecyclev1alpha1.UserCleanupHook) {
	hook.Spec.Template.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
		Name:      "scripts",
		MountPath: "/opt/nebari",
	}}
}

var _ = Describe("UserCleanupHook CRD schema", func() {
	// These tests never run the reconciler. They check what the API server
	// enforces from the generated CRD alone: enums, CEL rules, minimums and
	// defaults.

	It("rejects an unknown stage", func() {
		hook := newHook("schema-bad-stage")
		hook.Spec.Stage = "deleted"
		err := k8sClient.Create(ctx, hook)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got: %v", err)
	})

	It("rejects restartPolicy Always", func() {
		hook := newHook("schema-restart-always")
		hook.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyAlways
		err := k8sClient.Create(ctx, hook)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got: %v", err)
		Expect(err.Error()).To(ContainSubstring("restartPolicy must be Never or OnFailure"))
	})

	It("rejects an empty container list", func() {
		hook := newHook("schema-no-containers")
		hook.Spec.Template.Spec.Containers = []corev1.Container{}
		err := k8sClient.Create(ctx, hook)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got: %v", err)
		Expect(err.Error()).To(ContainSubstring("containers must not be empty"))
	})

	It("rejects a container without an image", func() {
		hook := newHook("schema-no-image")
		hook.Spec.Template.Spec.Containers[0].Image = ""
		err := k8sClient.Create(ctx, hook)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got: %v", err)
		Expect(err.Error()).To(ContainSubstring("every container needs an image"))
	})

	It("rejects a TTL below the one hour floor", func() {
		hook := newHook("schema-low-ttl")
		hook.Spec.TTLSecondsAfterFinished = ptr.To(int32(60))
		err := k8sClient.Create(ctx, hook)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got: %v", err)
	})

	It("rejects negative Job knobs", func() {
		hook := newHook("schema-negative-backoff")
		hook.Spec.BackoffLimit = ptr.To(int32(-1))
		err := k8sClient.Create(ctx, hook)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got: %v", err)

		hook = newHook("schema-zero-deadline")
		hook.Spec.ActiveDeadlineSeconds = ptr.To(int64(0))
		err = k8sClient.Create(ctx, hook)
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected Invalid, got: %v", err)
	})

	It("applies defaults to the optional Job knobs", func() {
		createHook(newHook("schema-defaults"))

		stored := getHook("schema-defaults")
		Expect(stored.Spec.BackoffLimit).To(HaveValue(Equal(int32(3))))
		Expect(stored.Spec.ActiveDeadlineSeconds).To(HaveValue(Equal(int64(900))))
		Expect(stored.Spec.TTLSecondsAfterFinished).To(HaveValue(Equal(int32(604800))))
		Expect(stored.Spec.DryRun).To(BeFalse())
	})

	It("accepts a template the schema cannot fully validate", func() {
		// The dangling mount passes the schema. Proving that here is what
		// justifies the dry-run in the reconciler.
		hook := newHook("schema-dangling-mount")
		withDanglingVolumeMount(hook)
		createHook(hook)
	})
})

var _ = Describe("UserCleanupHook Controller", func() {
	var reconciler *UserCleanupHookReconciler

	BeforeEach(func() {
		reconciler = &UserCleanupHookReconciler{
			Client: k8sClient,
			Scheme: scheme.Scheme,
		}
	})

	// reconcile runs one pass for the named hook and returns the result.
	reconcile := func(name string) ctrl.Result {
		req := ctrl.Request{NamespacedName: types.NamespacedName{
			Name:      name,
			Namespace: testNamespace,
		}}
		result, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	accepted := func(hook *lifecyclev1alpha1.UserCleanupHook) *metav1.Condition {
		cond := meta.FindStatusCondition(hook.Status.Conditions, lifecyclev1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil(), "Accepted condition missing")
		return cond
	}

	It("accepts a valid template", func() {
		createHook(newHook("valid"))

		result := reconcile("valid")
		Expect(result).To(Equal(ctrl.Result{}))

		hook := getHook("valid")
		cond := accepted(hook)
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonTemplateValid))
		Expect(cond.ObservedGeneration).To(Equal(hook.Generation))
		Expect(hook.Status.ObservedGeneration).To(Equal(hook.Generation))
	})

	It("rejects a template the Job validator refuses", func() {
		hook := newHook("invalid-mount")
		withDanglingVolumeMount(hook)
		createHook(hook)

		result := reconcile("invalid-mount")
		Expect(result).To(Equal(ctrl.Result{}), "an invalid template must not requeue")

		stored := getHook("invalid-mount")
		cond := accepted(stored)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonTemplateInvalid))
		Expect(cond.Message).To(ContainSubstring("scripts"))
		Expect(stored.Status.ObservedGeneration).To(Equal(stored.Generation))
	})

	It("does not re-evaluate an unchanged spec", func() {
		createHook(newHook("idempotent"))

		reconcile("idempotent")
		first := accepted(getHook("idempotent"))

		// The second pass hits the generation check and returns before the
		// dry-run. If it did not, SetStatusCondition would still keep the
		// transition time because the status value is unchanged, so also
		// assert on the resource version to prove no write happened.
		before := getHook("idempotent").ResourceVersion
		reconcile("idempotent")
		after := getHook("idempotent")

		Expect(after.ResourceVersion).To(Equal(before))
		Expect(accepted(after).LastTransitionTime).To(Equal(first.LastTransitionTime))
	})

	It("re-evaluates when the spec changes", func() {
		createHook(newHook("spec-change"))
		reconcile("spec-change")

		hook := getHook("spec-change")
		Expect(accepted(hook).Status).To(Equal(metav1.ConditionTrue))
		firstGeneration := hook.Generation

		withDanglingVolumeMount(hook)
		Expect(k8sClient.Update(ctx, hook)).To(Succeed())

		reconcile("spec-change")
		updated := getHook("spec-change")

		Expect(updated.Generation).To(BeNumerically(">", firstGeneration))
		Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
		cond := accepted(updated)
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(lifecyclev1alpha1.ReasonTemplateInvalid))
		Expect(cond.ObservedGeneration).To(Equal(updated.Generation))
	})

	It("returns cleanly when the hook no longer exists", func() {
		result := reconcile("never-created")
		Expect(result).To(Equal(ctrl.Result{}))
	})

	// The ValidationUnavailable branch is not covered here. envtest's API
	// server is always reachable, so a dry-run can only succeed or be
	// rejected.
})
