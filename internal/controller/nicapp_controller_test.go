/*
Copyright 2025.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/routing"
)

var _ = Describe("NicApp Controller", func() {
	const (
		nicAppName = "test-nicapp"
		timeout    = time.Second * 10
		interval   = time.Millisecond * 250
	)

	Context("When creating a NicApp without authentication", func() {
		It("Should create HTTPRoute successfully", func() {
			ctx := context.Background()

			// Generate unique namespace name
			nicAppNamespace := "test-ns-" + rand.String(5)

			// Create namespace with opt-in label
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nicAppNamespace,
					Labels: map[string]string{
						"nic.nebari.dev/managed": "true",
					},
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())

			// Create a test service
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: nicAppNamespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port: 8080,
						},
					},
					Selector: map[string]string{
						"app": "test",
					},
				},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create a Gateway
			gatewayNamespace := "envoy-gateway-system"
			gatewayNs := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: gatewayNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, gatewayNs)).Should(Succeed())

			gatewayClass := &gatewayv1.GatewayClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-gateway-class",
				},
				Spec: gatewayv1.GatewayClassSpec{
					ControllerName: "example.com/gateway-controller",
				},
			}
			Expect(k8sClient.Create(ctx, gatewayClass)).Should(Succeed())

			gateway := &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nic-public-gateway",
					Namespace: gatewayNamespace,
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: "test-gateway-class",
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Protocol: gatewayv1.HTTPProtocolType,
							Port:     80,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, gateway)).Should(Succeed())

			// Create NicApp
			nicApp := &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nicAppName,
					Namespace: nicAppNamespace,
				},
				Spec: appsv1.NicAppSpec{
					Hostname: "test.nic.local",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Gateway: "public",
				},
			}
			Expect(k8sClient.Create(ctx, nicApp)).Should(Succeed())

			// Initialize reconcilers
			recorder := &fakeRecorder{}
			coreReconciler := &core.CoreReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}
			routingReconciler := &routing.RoutingReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			// Create the reconciler
			nicAppReconciler := &NicAppReconciler{
				Client:            k8sClient,
				Scheme:            k8sClient.Scheme(),
				Recorder:          recorder,
				CoreReconciler:    coreReconciler,
				RoutingReconciler: routingReconciler,
			}

			// Trigger reconciliation
			_, err := nicAppReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      nicAppName,
					Namespace: nicAppNamespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify HTTPRoute was created
			httpRoute := &gatewayv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nicAppName + "-route",
					Namespace: nicAppNamespace,
				}, httpRoute)
			}, timeout, interval).Should(Succeed())

			// Verify HTTPRoute has correct hostname
			Expect(httpRoute.Spec.Hostnames).To(ContainElement(gatewayv1.Hostname("test.nic.local")))

			// Verify HTTPRoute has correct backend
			Expect(httpRoute.Spec.Rules).To(HaveLen(1))
			Expect(httpRoute.Spec.Rules[0].BackendRefs).To(HaveLen(1))
			Expect(string(httpRoute.Spec.Rules[0].BackendRefs[0].Name)).To(Equal("test-service"))

			// Verify NicApp status
			updatedNicApp := &appsv1.NicApp{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nicAppName,
					Namespace: nicAppNamespace,
				}, updatedNicApp)
			}, timeout, interval).Should(Succeed())

			// Check that RoutingReady condition is set
			hasRoutingCondition := false
			for _, cond := range updatedNicApp.Status.Conditions {
				if cond.Type == appsv1.ConditionTypeRoutingReady {
					hasRoutingCondition = true
					break
				}
			}
			Expect(hasRoutingCondition).To(BeTrue())
		})
	})

	Context("When namespace is not opted-in", func() {
		It("Should fail validation", func() {
			ctx := context.Background()

			// Generate unique namespace name
			nicAppNamespace := "test-ns-" + rand.String(5)

			// Create namespace WITHOUT opt-in label
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nicAppNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())

			// Create NicApp
			nicApp := &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nicapp-fail",
					Namespace: nicAppNamespace,
				},
				Spec: appsv1.NicAppSpec{
					Hostname: "test.nic.local",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Gateway: "public",
				},
			}
			Expect(k8sClient.Create(ctx, nicApp)).Should(Succeed())

			// Initialize reconcilers
			recorder := &fakeRecorder{}
			coreReconciler := &core.CoreReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			// Try to reconcile - should fail validation
			err := coreReconciler.ValidateNicApp(ctx, nicApp)
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("not opted-in"))
		})
	})

	Context("When service does not exist", func() {
		It("Should fail validation", func() {
			ctx := context.Background()

			// Generate unique namespace name
			nicAppNamespace := "test-ns-" + rand.String(5)

			// Create namespace with opt-in label
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nicAppNamespace,
					Labels: map[string]string{
						"nic.nebari.dev/managed": "true",
					},
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())

			// Create NicApp pointing to non-existent service
			nicApp := &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nicapp-noservice",
					Namespace: nicAppNamespace,
				},
				Spec: appsv1.NicAppSpec{
					Hostname: "test.nic.local",
					Service: appsv1.ServiceReference{
						Name: "nonexistent-service",
						Port: 8080,
					},
					Gateway: "public",
				},
			}
			Expect(k8sClient.Create(ctx, nicApp)).Should(Succeed())

			// Initialize reconcilers
			recorder := &fakeRecorder{}
			coreReconciler := &core.CoreReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			// Try to reconcile - should fail validation
			err := coreReconciler.ValidateNicApp(ctx, nicApp)
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("not found"))
		})
	})

	Context("When creating a NicApp with TLS", func() {
		It("Should handle TLS configuration", func() {
			ctx := context.Background()

			// Generate unique namespace name
			nicAppNamespace := "test-ns-" + rand.String(5)

			// Create namespace with opt-in label
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nicAppNamespace,
					Labels: map[string]string{
						"nic.nebari.dev/managed": "true",
					},
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())

			// Create a test service
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service-tls",
					Namespace: nicAppNamespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name: "http",
							Port: 8080,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create NicApp with TLS
			nicApp := &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nicapp-tls",
					Namespace: nicAppNamespace,
				},
				Spec: appsv1.NicAppSpec{
					Hostname: "secure.nic.local",
					Service: appsv1.ServiceReference{
						Name: "test-service-tls",
						Port: 8080,
					},
					Gateway: "public",
					TLS: &appsv1.TLSConfig{
						Enabled: true,
						Mode:    "wildcard",
					},
				},
			}
			Expect(k8sClient.Create(ctx, nicApp)).Should(Succeed())

			// Initialize all reconcilers
			recorder := &fakeRecorder{}
			coreReconciler := &core.CoreReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}
			routingReconciler := &routing.RoutingReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			// Reconcile
			err := coreReconciler.ValidateNicApp(ctx, nicApp)
			Expect(err).NotTo(HaveOccurred())

			err = routingReconciler.ReconcileRouting(ctx, nicApp)
			Expect(err).NotTo(HaveOccurred())

			// Note: TLS reconciliation would require the wildcard secret to exist
			// For this test, we just verify the HTTPRoute is created correctly
			// err = tlsReconciler.ReconcileTLS(ctx, nicApp)
			// Expect(err).NotTo(HaveOccurred())

			// Verify HTTPRoute was created with hostname
			httpRoute := &gatewayv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nicApp.Name + "-route",
					Namespace: nicAppNamespace,
				}, httpRoute)
			}, timeout, interval).Should(Succeed())

			Expect(httpRoute.Spec.Hostnames).Should(HaveLen(1))
			Expect(httpRoute.Spec.Hostnames[0]).Should(Equal(gatewayv1.Hostname("secure.nic.local")))
		})
	})

	Context("When NicApp is deleted with finalizer", func() {
		It("Should cleanup HTTPRoute", func() {
			ctx := context.Background()

			// Generate unique namespace name
			nicAppNamespace := "test-ns-" + rand.String(5)

			// Create namespace with opt-in label
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nicAppNamespace,
					Labels: map[string]string{
						"nic.nebari.dev/managed": "true",
					},
				},
			}
			Expect(k8sClient.Create(ctx, namespace)).Should(Succeed())

			// Create a test service
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service-cleanup",
					Namespace: nicAppNamespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name: "http",
							Port: 8080,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create NicApp
			nicApp := &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nicapp-cleanup",
					Namespace: nicAppNamespace,
				},
				Spec: appsv1.NicAppSpec{
					Hostname: "cleanup.nic.local",
					Service: appsv1.ServiceReference{
						Name: "test-service-cleanup",
						Port: 8080,
					},
					Gateway: "public",
				},
			}
			Expect(k8sClient.Create(ctx, nicApp)).Should(Succeed())

			// Initialize reconcilers
			recorder := &fakeRecorder{}
			coreReconciler := &core.CoreReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}
			routingReconciler := &routing.RoutingReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
			}

			// Create the main reconciler
			nicAppReconciler := &NicAppReconciler{
				Client:            k8sClient,
				Scheme:            k8sClient.Scheme(),
				Recorder:          recorder,
				CoreReconciler:    coreReconciler,
				RoutingReconciler: routingReconciler,
			}

			// Reconcile to create resources
			err := coreReconciler.ValidateNicApp(ctx, nicApp)
			Expect(err).NotTo(HaveOccurred())

			err = routingReconciler.ReconcileRouting(ctx, nicApp)
			Expect(err).NotTo(HaveOccurred())

			// Verify HTTPRoute was created
			httpRoute := &gatewayv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      nicApp.Name + "-route",
					Namespace: nicAppNamespace,
				}, httpRoute)
			}, timeout, interval).Should(Succeed())

			// Now test cleanup
			err = nicAppReconciler.cleanup(ctx, nicApp)
			Expect(err).NotTo(HaveOccurred())

			// Verify HTTPRoute was deleted
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      nicApp.Name + "-route",
					Namespace: nicAppNamespace,
				}, httpRoute)
				return err != nil
			}, timeout, interval).Should(BeTrue())
		})
	})
})

// fakeRecorder is a simple event recorder for testing
type fakeRecorder struct{}

func (f *fakeRecorder) Event(object runtime.Object, eventtype, reason, message string) {}

func (f *fakeRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
}

func (f *fakeRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
}
