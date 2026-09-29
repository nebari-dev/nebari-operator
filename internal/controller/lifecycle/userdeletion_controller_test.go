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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

// Placeholder until the UserDeletion controller has behavior to assert on.
// It only proves a well-formed marker can be created and reconciled.
var _ = Describe("UserDeletion Controller", func() {
	const userID = "controller-placeholder-user"

	It("reconciles a marker without error", func() {
		marker := &lifecyclev1alpha1.UserDeletion{
			ObjectMeta: metav1.ObjectMeta{Name: userID},
			Spec: lifecyclev1alpha1.UserDeletionSpec{
				UserID:    userID,
				DeletedAt: metav1.NewTime(time.Now().Truncate(time.Second)),
			},
		}
		Expect(k8sClient.Create(ctx, marker)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, marker))).To(Succeed())
		})

		reconciler := &UserDeletionReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: userID},
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
