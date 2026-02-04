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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	reconcilersv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

var _ = Describe("NebariApp Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		nebariapp := &reconcilersv1.NebariApp{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind NebariApp")
			err := k8sClient.Get(ctx, typeNamespacedName, nebariapp)
			if err != nil && errors.IsNotFound(err) {
				// Ensure namespace has the required label
				ns := &corev1.Namespace{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "default"}, ns)
				Expect(err).NotTo(HaveOccurred())

				if ns.Labels == nil {
					ns.Labels = make(map[string]string)
				}
				ns.Labels["nebari.dev/managed"] = "true"
				Expect(k8sClient.Update(ctx, ns)).To(Succeed())

				// Create a test service
				service := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-service",
						Namespace: "default",
					},
					Spec: corev1.ServiceSpec{
						Ports: []corev1.ServicePort{
							{
								Port: 8080,
								Name: "http",
							},
						},
						Selector: map[string]string{
							"app": "test",
						},
					},
				}
				_ = k8sClient.Create(ctx, service)

				resource := &reconcilersv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: reconcilersv1.NebariAppSpec{
						Hostname: "test-app.nebari.local",
						Service: reconcilersv1.ServiceReference{
							Name: "test-service",
							Port: 8080,
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &reconcilersv1.NebariApp{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance NebariApp")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			// Clean up test service
			service := &corev1.Service{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-service", Namespace: "default"}, service)
			if err == nil {
				_ = k8sClient.Delete(ctx, service)
			}
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &NebariAppReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: record.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify the NebariApp status was updated
			updatedApp := &reconcilersv1.NebariApp{}
			err = k8sClient.Get(ctx, typeNamespacedName, updatedApp)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedApp.Status.Conditions).NotTo(BeEmpty())

			// Check that Ready condition is set
			var readyCondition *metav1.Condition
			for i := range updatedApp.Status.Conditions {
				if updatedApp.Status.Conditions[i].Type == "Ready" {
					readyCondition = &updatedApp.Status.Conditions[i]
					break
				}
			}
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
		})
	})
})
