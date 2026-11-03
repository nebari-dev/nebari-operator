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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	reconcilersv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/routing"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

var _ = Describe("Route timeouts", func() {
	ctx := context.Background()
	const ns = "default"

	timeoutsApp := func(name string, timeouts *reconcilersv1.RouteTimeouts) *reconcilersv1.NebariApp {
		tlsEnabled := false
		return &reconcilersv1.NebariApp{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: reconcilersv1.NebariAppSpec{
				Hostname: name + ".nebari.local",
				Service:  reconcilersv1.ServiceReference{Name: "timeouts-service", Port: 8080},
				Routing: &reconcilersv1.RoutingConfig{
					TLS:    &reconcilersv1.RoutingTLSConfig{Enabled: &tlsEnabled},
					Routes: []reconcilersv1.RouteMatch{{PathPrefix: "/"}, {PathPrefix: "/v2", Timeouts: timeouts}},
				},
			},
		}
	}

	It("rejects invalid timeouts at admission", func() {
		for _, tc := range []struct {
			name     string
			timeouts *reconcilersv1.RouteTimeouts
			message  string
		}{
			{"timeouts-backend-longer", &reconcilersv1.RouteTimeouts{Request: "30s", BackendRequest: "1m"}, "backendRequest timeout cannot be longer than request timeout"},
			{"timeouts-no-unit", &reconcilersv1.RouteTimeouts{Request: "30"}, "timeouts.request"},
			{"timeouts-empty", &reconcilersv1.RouteTimeouts{}, "timeouts"},
		} {
			err := k8sClient.Create(ctx, timeoutsApp(tc.name, tc.timeouts))
			Expect(err).To(HaveOccurred(), tc.name)
			Expect(errors.IsInvalid(err)).To(BeTrue(), "%s: %v", tc.name, err)
			Expect(err.Error()).To(ContainSubstring(tc.message), tc.name)
		}
	})

	It("accepts a disabled request timeout with a backend timeout", func() {
		app := timeoutsApp("timeouts-disabled", &reconcilersv1.RouteTimeouts{Request: "0s", BackendRequest: "1h"})
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		Expect(k8sClient.Delete(ctx, app)).To(Succeed())
	})

	It("writes the timeouts to the generated HTTPRoutes", func() {
		By("setting up the opted-in namespace, the Service and the shared Gateway")
		nsObj := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ns}, nsObj)).To(Succeed())
		if nsObj.Labels == nil {
			nsObj.Labels = map[string]string{}
		}
		nsObj.Labels["nebari.dev/managed"] = "true"
		Expect(k8sClient.Update(ctx, nsObj)).To(Succeed())

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "timeouts-service", Namespace: ns},
			Spec: corev1.ServiceSpec{
				Ports:    []corev1.ServicePort{{Name: "http", Port: 8080}},
				Selector: map[string]string{"app": "timeouts"},
			},
		}
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

		gwNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: constants.GatewayNamespace}}
		if err := k8sClient.Create(ctx, gwNS); err != nil {
			Expect(errors.IsAlreadyExists(err)).To(BeTrue(), err.Error())
		}
		gw := &gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: constants.PublicGatewayName, Namespace: constants.GatewayNamespace},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: constants.GatewayClassName,
				Listeners:        []gatewayv1.Listener{{Name: "http", Port: 80, Protocol: gatewayv1.HTTPProtocolType}},
			},
		}
		if err := k8sClient.Create(ctx, gw); err != nil {
			Expect(errors.IsAlreadyExists(err)).To(BeTrue(), err.Error())
		}

		By("creating a NebariApp with a long timeout on one route and none on another")
		// Public routes use the same rule builder but need auth enabled; they
		// are covered by the routing package's unit tests.
		app := timeoutsApp("timeouts-routes", &reconcilersv1.RouteTimeouts{Request: "1830s", BackendRequest: "1800s"})
		app.Spec.Routing.Routes = append(app.Spec.Routing.Routes,
			reconcilersv1.RouteMatch{PathPrefix: "/events", Timeouts: &reconcilersv1.RouteTimeouts{Request: "0s"}})
		Expect(k8sClient.Create(ctx, app)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, app) })

		recorder := record.NewFakeRecorder(20)
		r := &NebariAppReconciler{
			Client:            k8sClient,
			Scheme:            k8sClient.Scheme(),
			Recorder:          recorder,
			CoreReconciler:    &core.CoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder},
			RoutingReconciler: &routing.RoutingReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder},
			AuthReconciler:    &auth.AuthReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder},
		}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: app.Name, Namespace: ns}})
		Expect(err).NotTo(HaveOccurred())

		By("reading back the HTTPRoute the API server accepted")
		route := &gatewayv1.HTTPRoute{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: naming.HTTPRouteName(app), Namespace: ns}, route)).To(Succeed())
		Expect(route.Spec.Rules).To(HaveLen(3))
		Expect(route.Spec.Rules[0].Timeouts).To(BeNil())
		Expect(*route.Spec.Rules[0].Matches[0].Path.Value).To(Equal("/"))
		Expect(route.Spec.Rules[1].Timeouts).NotTo(BeNil())
		Expect(*route.Spec.Rules[1].Matches[0].Path.Value).To(Equal("/v2"))
		Expect(string(*route.Spec.Rules[1].Timeouts.Request)).To(Equal("1830s"))
		Expect(string(*route.Spec.Rules[1].Timeouts.BackendRequest)).To(Equal("1800s"))
		Expect(*route.Spec.Rules[2].Matches[0].Path.Value).To(Equal("/events"))
		Expect(string(*route.Spec.Rules[2].Timeouts.Request)).To(Equal("0s"))
		Expect(route.Spec.Rules[2].Timeouts.BackendRequest).To(BeNil())
	})
})
