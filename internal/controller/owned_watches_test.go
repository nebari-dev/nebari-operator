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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	reconcilersv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/routing"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

// These specs run the controller through SetupWithManager, so they exercise the
// watches themselves rather than calling Reconcile directly.
var _ = Describe("NebariApp owned-resource watches", func() {
	const (
		namespace = "owned-watches"
		appName   = "watched-app"
	)

	var (
		mgrCancel context.CancelFunc
		mgrDone   chan struct{}
	)

	BeforeEach(func() {
		By("creating an opted-in namespace, the backing Service and the shared Gateway")
		createIfMissing(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   namespace,
			Labels: map[string]string{core.ManagedNamespaceLabel: "true"},
		}})
		createIfMissing(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "watched-service", Namespace: namespace},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
		})
		createIfMissing(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: constants.GatewayNamespace}})
		createIfMissing(&gatewayv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Name: constants.PublicGatewayName, Namespace: constants.GatewayNamespace},
			Spec: gatewayv1.GatewaySpec{
				GatewayClassName: gatewayv1.ObjectName(constants.GatewayClassName),
				Listeners: []gatewayv1.Listener{{
					Name:     "http",
					Port:     80,
					Protocol: gatewayv1.HTTPProtocolType,
				}},
			},
		})

		By("starting a manager running the NebariApp controller")
		skipNameValidation := true
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:     scheme.Scheme,
			Metrics:    metricsserver.Options{BindAddress: "0"},
			Controller: config.Controller{SkipNameValidation: &skipNameValidation},
		})
		Expect(err).NotTo(HaveOccurred())

		// A FakeRecorder with a nil Events channel discards events instead of
		// blocking once a buffer fills up.
		recorder := &record.FakeRecorder{}
		reconciler := &NebariAppReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: recorder,
			CoreReconciler: &core.CoreReconciler{
				Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Recorder: recorder,
			},
			RoutingReconciler: &routing.RoutingReconciler{
				Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Recorder: recorder,
			},
			AuthReconciler: &auth.AuthReconciler{
				Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Recorder: recorder,
			},
		}
		Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

		var mgrCtx context.Context
		mgrCtx, mgrCancel = context.WithCancel(ctx)
		mgrDone = make(chan struct{})
		go func() {
			defer GinkgoRecover()
			defer close(mgrDone)
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()
	})

	AfterEach(func() {
		By("deleting the NebariApp while the manager can still run its finalizer")
		app := &reconcilersv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: namespace}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, app))).To(Succeed())
		Eventually(func() bool {
			return errors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: appName, Namespace: namespace}, app))
		}, 20*time.Second, 250*time.Millisecond).Should(BeTrue())

		mgrCancel()
		Eventually(mgrDone, 10*time.Second).Should(BeClosed())
	})

	It("re-creates an owned HTTPRoute promptly after it is deleted", func() {
		app := &reconcilersv1.NebariApp{
			ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: namespace},
			Spec: reconcilersv1.NebariAppSpec{
				Hostname: "watched-app.nebari.local",
				Service:  reconcilersv1.ServiceReference{Name: "watched-service", Port: 8080},
				Routing:  &reconcilersv1.RoutingConfig{},
			},
		}
		Expect(k8sClient.Create(ctx, app)).To(Succeed())

		By("waiting for the NebariApp to settle at Ready=True")
		Eventually(func(g Gomega) {
			current := &reconcilersv1.NebariApp{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: appName, Namespace: namespace}, current)).To(Succeed())
			g.Expect(meta.IsStatusConditionTrue(current.Status.Conditions, reconcilersv1.ConditionTypeReady)).To(BeTrue())
		}, 20*time.Second, 250*time.Millisecond).Should(Succeed())

		routeKey := types.NamespacedName{Name: naming.HTTPRouteName(app), Namespace: namespace}
		original := &gatewayv1.HTTPRoute{}
		Expect(k8sClient.Get(ctx, routeKey, original)).To(Succeed())
		Expect(metav1.GetControllerOf(original)).NotTo(BeNil())

		By("deleting the owned HTTPRoute")
		Expect(k8sClient.Delete(ctx, original)).To(Succeed())

		// The periodic requeue after a successful reconcile is one minute, so a
		// re-created route well inside that window can only come from the watch.
		By("expecting a new HTTPRoute well before the periodic requeue")
		Eventually(func(g Gomega) {
			recreated := &gatewayv1.HTTPRoute{}
			g.Expect(k8sClient.Get(ctx, routeKey, recreated)).To(Succeed())
			g.Expect(recreated.UID).NotTo(Equal(original.UID))
		}, 10*time.Second, 250*time.Millisecond).Should(Succeed())
	})
})

// createIfMissing creates obj, tolerating it already existing from an earlier spec
// (envtest has no namespace controller, so namespaces are never removed).
func createIfMissing(obj client.Object) {
	GinkgoHelper()
	err := k8sClient.Create(ctx, obj)
	if !errors.IsAlreadyExists(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}
