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

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	reconcilersv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/database"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/routing"
)

var _ = Describe("NebariApp managed databases", func() {
	const namespace = "default"
	ctx := context.Background()

	newApp := func(name string, databases ...reconcilersv1.DatabaseConfig) *reconcilersv1.NebariApp {
		return &reconcilersv1.NebariApp{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec: reconcilersv1.NebariAppSpec{
				Hostname:  name + ".nebari.local",
				Service:   reconcilersv1.ServiceReference{Name: "db-test-service", Port: 8080},
				Databases: databases,
			},
		}
	}

	Context("spec.databases admission", func() {
		It("applies the documented defaults", func() {
			app := newApp("db-defaults", reconcilersv1.DatabaseConfig{Name: "main"})
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, app)).To(Succeed()) })

			Expect(app.Spec.Databases).To(HaveLen(1))
			Expect(app.Spec.Databases[0].Instances).To(Equal(int32(1)))
			Expect(app.Spec.Databases[0].Size).To(Equal("1Gi"))
		})

		It("accepts several databases with different shapes", func() {
			app := newApp("db-multi",
				reconcilersv1.DatabaseConfig{Name: "metadata"},
				reconcilersv1.DatabaseConfig{Name: "warehouse", Instances: 3, Size: "20Gi"},
			)
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, app)).To(Succeed()) })
		})

		DescribeTable("rejects invalid entries",
			func(databases []reconcilersv1.DatabaseConfig, wantInMessage string) {
				err := k8sClient.Create(ctx, newApp("db-invalid", databases...))
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsInvalid(err)).To(BeTrue(), "want an Invalid error, got: %v", err)
				Expect(err.Error()).To(ContainSubstring(wantInMessage))
			},
			Entry("duplicate names",
				[]reconcilersv1.DatabaseConfig{{Name: "main"}, {Name: "main"}}, "Duplicate value"),
			Entry("a missing name",
				[]reconcilersv1.DatabaseConfig{{Size: "1Gi"}}, "spec.databases[0].name"),
			Entry("an uppercase name",
				[]reconcilersv1.DatabaseConfig{{Name: "Main"}}, "spec.databases[0].name"),
			Entry("a name with a trailing hyphen",
				[]reconcilersv1.DatabaseConfig{{Name: "main-"}}, "spec.databases[0].name"),
			Entry("a name longer than 20 characters",
				[]reconcilersv1.DatabaseConfig{{Name: "abcdefghijklmnopqrstu"}}, "spec.databases[0].name"),
			Entry("more than 9 instances",
				[]reconcilersv1.DatabaseConfig{{Name: "main", Instances: 10}}, "spec.databases[0].instances"),
			Entry("a zero size",
				[]reconcilersv1.DatabaseConfig{{Name: "main", Size: "0Gi"}}, "size must be greater than zero"),
			Entry("a size that is not a quantity",
				[]reconcilersv1.DatabaseConfig{{Name: "main", Size: "big"}}, "spec.databases[0].size"),
			Entry("more than 10 databases",
				[]reconcilersv1.DatabaseConfig{
					{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"}, {Name: "e"}, {Name: "f"},
					{Name: "g"}, {Name: "h"}, {Name: "i"}, {Name: "j"}, {Name: "k"},
				}, "spec.databases"),
		)
	})

	Context("reconciling an app with databases", func() {
		const appName = "db-app"
		key := types.NamespacedName{Name: appName, Namespace: namespace}

		newReconciler := func() *NebariAppReconciler {
			recorder := record.NewFakeRecorder(64)
			return &NebariAppReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: recorder,
				CoreReconciler: &core.CoreReconciler{
					Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder,
				},
				RoutingReconciler: &routing.RoutingReconciler{
					Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder,
				},
				AuthReconciler: &auth.AuthReconciler{
					Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder,
				},
				DatabaseReconciler: &database.DatabaseReconciler{
					Client: k8sClient, Scheme: k8sClient.Scheme(), Recorder: recorder,
				},
			}
		}

		getApp := func() *reconcilersv1.NebariApp {
			app := &reconcilersv1.NebariApp{}
			Expect(k8sClient.Get(ctx, key, app)).To(Succeed())
			return app
		}

		// finishProvisioning plays CloudNativePG's part for one Cluster: it
		// marks the Cluster ready and publishes the app-user Secret.
		finishProvisioning := func(clusterName string) {
			cluster := &cnpgv1.Cluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: namespace}, cluster)).To(Succeed())
			cluster.Status.Conditions = []metav1.Condition{{
				Type:               string(cnpgv1.ConditionClusterReady),
				Status:             metav1.ConditionTrue,
				Reason:             "ClusterIsReady",
				LastTransitionTime: metav1.Now(),
			}}
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: clusterName + "-app", Namespace: namespace},
				Data: map[string][]byte{
					"host":     []byte(clusterName + "-rw"),
					"port":     []byte("5432"),
					"username": []byte("app"),
					"password": []byte("hunter2"),
					"dbname":   []byte("app"),
					"uri":      []byte("postgresql://app:hunter2@" + clusterName + "-rw:5432/app"),
				},
			}
			Expect(controllerutil.SetControllerReference(cluster, secret, k8sClient.Scheme())).To(Succeed())
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		}

		BeforeEach(func() {
			ns := &corev1.Namespace{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: namespace}, ns)).To(Succeed())
			if ns.Labels == nil {
				ns.Labels = map[string]string{}
			}
			ns.Labels["nebari.dev/managed"] = "true"
			Expect(k8sClient.Update(ctx, ns)).To(Succeed())

			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "db-test-service", Namespace: namespace},
				Spec: corev1.ServiceSpec{
					Ports:    []corev1.ServicePort{{Port: 8080, Name: "http"}},
					Selector: map[string]string{"app": "db-test"},
				},
			}
			Expect(k8sClient.Create(ctx, service)).To(Succeed())
			DeferCleanup(func() { Expect(k8sClient.Delete(ctx, service)).To(Succeed()) })
		})

		It("provisions each database, reports readiness, and retains a removed one", func() {
			app := newApp(appName,
				reconcilersv1.DatabaseConfig{Name: "metadata"},
				reconcilersv1.DatabaseConfig{Name: "warehouse", Instances: 3, Size: "20Gi"},
			)
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			r := newReconciler()
			DeferCleanup(func() {
				// envtest runs no garbage collector, so owned objects are
				// removed by hand after the finalizer is released.
				Expect(k8sClient.Delete(ctx, getApp())).To(Succeed())
				_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.DeleteAllOf(ctx, &cnpgv1.Cluster{}, client.InNamespace(namespace))).To(Succeed())
				for _, name := range []string{
					"db-app-metadata-db-app", "db-app-warehouse-db-app",
					"db-app-metadata-db-credentials", "db-app-warehouse-db-credentials",
				} {
					secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
					Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
				}
			})

			By("creating a Cluster per entry and holding Ready back while they provision")
			result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(15 * time.Second))

			metadata := &cnpgv1.Cluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "db-app-metadata-db", Namespace: namespace}, metadata)).To(Succeed())
			Expect(metadata.Spec.Instances).To(Equal(1))
			Expect(metadata.Spec.StorageConfiguration.Size).To(Equal("1Gi"))
			Expect(metadata.Spec.EnablePDB).To(HaveValue(BeFalse()), "a single instance must stay drainable")
			Expect(metav1.IsControlledBy(metadata, getApp())).To(BeTrue())

			warehouse := &cnpgv1.Cluster{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "db-app-warehouse-db", Namespace: namespace}, warehouse)).To(Succeed())
			Expect(warehouse.Spec.Instances).To(Equal(3))
			Expect(warehouse.Spec.StorageConfiguration.Size).To(Equal("20Gi"))
			Expect(warehouse.Spec.EnablePDB).To(HaveValue(BeTrue()))

			current := getApp()
			dbCond := apimeta.FindStatusCondition(current.Status.Conditions, reconcilersv1.ConditionTypeDatabaseReady)
			Expect(dbCond).NotTo(BeNil())
			Expect(dbCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(dbCond.Reason).To(Equal(reconcilersv1.ReasonDatabaseProvisioning))
			readyCond := apimeta.FindStatusCondition(current.Status.Conditions, reconcilersv1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(reconcilersv1.ReasonDatabaseProvisioning))

			By("publishing credentials for one database as soon as it is ready")
			finishProvisioning("db-app-metadata-db")
			result, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(15 * time.Second))

			credentials := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "db-app-metadata-db-credentials", Namespace: namespace}, credentials)).To(Succeed())
			Expect(credentials.Data).To(HaveKeyWithValue("database", []byte("app")))
			Expect(credentials.Data).To(HaveKeyWithValue("host", []byte("db-app-metadata-db-rw")))
			Expect(credentials.Data).To(HaveLen(6))

			By("turning Ready once every database is")
			finishProvisioning("db-app-warehouse-db")
			result, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Minute))

			current = getApp()
			Expect(apimeta.IsStatusConditionTrue(current.Status.Conditions, reconcilersv1.ConditionTypeDatabaseReady)).To(BeTrue())
			Expect(apimeta.IsStatusConditionTrue(current.Status.Conditions, reconcilersv1.ConditionTypeReady)).To(BeTrue())
			Expect(current.Status.Databases).To(Equal([]reconcilersv1.DatabaseStatus{
				{Name: "metadata", SecretRef: &reconcilersv1.ResourceReference{Name: "db-app-metadata-db-credentials", Namespace: namespace}},
				{Name: "warehouse", SecretRef: &reconcilersv1.ResourceReference{Name: "db-app-warehouse-db-credentials", Namespace: namespace}},
			}))

			role := &rbacv1.Role{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "db-app-db-secret-reader", Namespace: namespace}, role)).To(Succeed())
			Expect(role.Rules).To(HaveLen(1))
			Expect(role.Rules[0].ResourceNames).To(Equal([]string{"db-app-metadata-db-credentials", "db-app-warehouse-db-credentials"}))

			By("retaining a database whose entry is removed")
			current.Spec.Databases = current.Spec.Databases[:1]
			Expect(k8sClient.Update(ctx, current)).To(Succeed())
			result, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Minute))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "db-app-warehouse-db", Namespace: namespace}, warehouse)).To(Succeed())
			Expect(warehouse.DeletionTimestamp).To(BeNil())

			current = getApp()
			Expect(apimeta.IsStatusConditionTrue(current.Status.Conditions, reconcilersv1.ConditionTypeReady)).To(BeTrue())
			Expect(current.Status.Databases).To(HaveLen(2))
			Expect(current.Status.Databases[1].Name).To(Equal("warehouse"))
			Expect(current.Status.Databases[1].Retained).To(BeTrue())
			Expect(current.Status.Databases[1].SecretRef).NotTo(BeNil())
		})
	})
})
