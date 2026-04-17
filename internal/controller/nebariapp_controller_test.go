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

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	reconcilersv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/routing"
	tlsreconciler "github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/tls"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
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

			// Always ensure test service exists
			service := &corev1.Service{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-service", Namespace: "default"}, service)
			if err != nil && errors.IsNotFound(err) {
				service = &corev1.Service{
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
				Expect(k8sClient.Create(ctx, service)).To(Succeed())
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
			fakeRecorder := record.NewFakeRecorder(10)
			controllerReconciler := &NebariAppReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: fakeRecorder,
				CoreReconciler: &core.CoreReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: fakeRecorder,
				},
				RoutingReconciler: &routing.RoutingReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: fakeRecorder,
				},
				AuthReconciler: &auth.AuthReconciler{
					Client:    k8sClient,
					Scheme:    k8sClient.Scheme(),
					Recorder:  fakeRecorder,
					Providers: nil,
				},
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

			// Check that RoutingReady condition is set to False (routing not configured)
			var routingCondition *metav1.Condition
			for i := range updatedApp.Status.Conditions {
				if updatedApp.Status.Conditions[i].Type == "RoutingReady" {
					routingCondition = &updatedApp.Status.Conditions[i]
					break
				}
			}
			Expect(routingCondition).NotTo(BeNil())
			Expect(routingCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(routingCondition.Reason).To(Equal("RoutingNotConfigured"))
		})

		It("should create HTTPRoute when routing is configured", func() {
			By("Creating a NebariApp with routing configuration")
			tlsEnabled := true
			appWithRouting := &reconcilersv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-with-routing",
					Namespace: "default",
				},
				Spec: reconcilersv1.NebariAppSpec{
					Hostname: "test-with-routing.nebari.local",
					Service: reconcilersv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Routing: &reconcilersv1.RoutingConfig{
						TLS: &reconcilersv1.RoutingTLSConfig{
							Enabled: &tlsEnabled,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, appWithRouting)).To(Succeed())

			By("Reconciling the resource with routing")
			fakeRecorder2 := record.NewFakeRecorder(10)
			controllerReconciler := &NebariAppReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: fakeRecorder2,
				CoreReconciler: &core.CoreReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: fakeRecorder2,
				},
				RoutingReconciler: &routing.RoutingReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: fakeRecorder2,
				},
				AuthReconciler: &auth.AuthReconciler{
					Client:    k8sClient,
					Scheme:    k8sClient.Scheme(),
					Recorder:  fakeRecorder2,
					Providers: nil,
				},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-with-routing",
					Namespace: "default",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify RoutingReady condition - might be True or have an error about Gateway
			updatedApp := &reconcilersv1.NebariApp{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "test-with-routing", Namespace: "default"}, updatedApp)
			Expect(err).NotTo(HaveOccurred())

			var routingCondition *metav1.Condition
			for i := range updatedApp.Status.Conditions {
				if updatedApp.Status.Conditions[i].Type == "RoutingReady" {
					routingCondition = &updatedApp.Status.Conditions[i]
					break
				}
			}
			// RoutingReady should be set (either True or False depending on Gateway availability)
			Expect(routingCondition).NotTo(BeNil())

			By("Cleaning up the test resource")
			Expect(k8sClient.Delete(ctx, appWithRouting)).To(Succeed())
		})

		It("uses a user-provided TLS secret when routing.tls.secretName is set", func() {
			const appName = "byo-secret"
			const userSecretName = "my-user-tls"

			// Ensure the Gateway namespace exists (envtest starts empty).
			gwNS := &corev1.Namespace{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: constants.GatewayNamespace}, gwNS); errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: constants.GatewayNamespace},
				})).To(Succeed())
			}

			// Pre-create the user's TLS secret in the Gateway namespace.
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: userSecretName, Namespace: constants.GatewayNamespace},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					"tls.crt": []byte("dummy-cert"),
					"tls.key": []byte("dummy-key"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secret)
			})

			// Ensure the target Gateway exists - the TLS reconciler's listener-patch
			// step requires it.
			gw := &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.PublicGatewayName,
					Namespace: constants.GatewayNamespace,
				},
				Spec: gatewayv1.GatewaySpec{
					GatewayClassName: gatewayv1.ObjectName(constants.GatewayClassName),
					Listeners: []gatewayv1.Listener{
						{
							Name:     "http",
							Port:     80,
							Protocol: gatewayv1.HTTPProtocolType,
						},
					},
				},
			}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: constants.PublicGatewayName, Namespace: constants.GatewayNamespace}, &gatewayv1.Gateway{}); errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, gw)).To(Succeed())
				DeferCleanup(func() {
					_ = k8sClient.Delete(ctx, gw)
				})
			}

			app := &reconcilersv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: "default"},
				Spec: reconcilersv1.NebariAppSpec{
					Hostname: "byo.example.com",
					Service:  reconcilersv1.ServiceReference{Name: "test-service", Port: 8080},
					Routing: &reconcilersv1.RoutingConfig{
						TLS: &reconcilersv1.RoutingTLSConfig{
							Enabled:    boolPtr(true),
							SecretName: userSecretName,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, app)
			})

			By("triggering reconcile")
			fakeRecorder := record.NewFakeRecorder(10)
			controllerReconciler := &NebariAppReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: fakeRecorder,
				CoreReconciler: &core.CoreReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: fakeRecorder,
				},
				TLSReconciler: &tlsreconciler.TLSReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: fakeRecorder,
				},
				RoutingReconciler: &routing.RoutingReconciler{
					Client:   k8sClient,
					Scheme:   k8sClient.Scheme(),
					Recorder: fakeRecorder,
				},
				AuthReconciler: &auth.AuthReconciler{
					Client:    k8sClient,
					Scheme:    k8sClient.Scheme(),
					Recorder:  fakeRecorder,
					Providers: nil,
				},
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: appName, Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying no Certificate was created for this NebariApp")
			cert := &certmanagerv1.Certificate{}
			getErr := k8sClient.Get(ctx, types.NamespacedName{
				Name:      naming.CertificateName(app),
				Namespace: constants.GatewayNamespace,
			}, cert)
			Expect(errors.IsNotFound(getErr)).To(BeTrue(),
				"no Certificate should exist on the user-provided-secret path")

			By("verifying TLSReady=True/UserProvidedSecretReady")
			got := &reconcilersv1.NebariApp{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: appName, Namespace: "default"}, got)).To(Succeed())
			c := conditions.GetCondition(got, reconcilersv1.ConditionTypeTLSReady)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionTrue))
			Expect(c.Reason).To(Equal(reconcilersv1.ReasonUserProvidedSecretReady))
		})
	})
})

func boolPtr(b bool) *bool { return &b }
