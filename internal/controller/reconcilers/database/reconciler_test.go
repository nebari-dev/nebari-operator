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

package database

import (
	"context"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
)

func newScheme(t *testing.T, withCNPG bool) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add appsv1 scheme: %v", err)
	}
	if withCNPG {
		if err := cnpgv1.AddToScheme(scheme); err != nil {
			t.Fatalf("add cnpg scheme: %v", err)
		}
	}
	return scheme
}

func newApp(database *appsv1.DatabaseConfig) *appsv1.NebariApp {
	return &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "team-a", UID: "uid-1"},
		Spec: appsv1.NebariAppSpec{
			Hostname: "myapp.example.com",
			Service:  appsv1.ServiceReference{Name: "myapp", Port: 8080},
			Database: database,
		},
	}
}

func newReconciler(c client.Client, scheme *runtime.Scheme) *DatabaseReconciler {
	return &DatabaseReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(16),
	}
}

// readyCluster returns a CNPG Cluster as the reconciler would have created it,
// with the Ready condition set (as the CNPG operator would in a real cluster).
func readyCluster(app *appsv1.NebariApp, scheme *runtime.Scheme, t *testing.T) *cnpgv1.Cluster {
	t.Helper()
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-db", Namespace: app.Namespace, UID: "cluster-uid-1"},
		Spec: cnpgv1.ClusterSpec{
			Instances:            1,
			StorageConfiguration: cnpgv1.StorageConfiguration{Size: "1Gi"},
		},
		Status: cnpgv1.ClusterStatus{
			Conditions: []metav1.Condition{{
				Type:               string(cnpgv1.ConditionClusterReady),
				Status:             metav1.ConditionTrue,
				Reason:             "ClusterIsReady",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	if err := controllerReference(app, cluster, scheme); err != nil {
		t.Fatalf("set owner ref: %v", err)
	}
	return cluster
}

func cnpgAppSecret(t *testing.T, app *appsv1.NebariApp, cluster *cnpgv1.Cluster, scheme *runtime.Scheme) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-db-app", Namespace: app.Namespace},
		Data: map[string][]byte{
			"host":     []byte("myapp-db-rw"),
			"port":     []byte("5432"),
			"username": []byte("app"),
			"password": []byte("hunter2"),
			"dbname":   []byte("app"),
			"uri":      []byte("postgresql://app:hunter2@myapp-db-rw.team-a:5432/app"),
		},
	}
	if err := controllerReference(cluster, secret, scheme); err != nil {
		t.Fatalf("set owner ref: %v", err)
	}
	return secret
}

func TestReconcileDatabase_CreatesCluster(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(&appsv1.DatabaseConfig{Enabled: true, Instances: 3, Size: "10Gi"})

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result == nil || result.RequeueAfter != 15*time.Second {
		t.Fatalf("expected 15s provisioning requeue, got %+v", result)
	}

	cluster := &cnpgv1.Cluster{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db", Namespace: "team-a"}, cluster); err != nil {
		t.Fatalf("expected Cluster to be created: %v", err)
	}
	if cluster.Spec.Instances != 3 {
		t.Errorf("Instances = %d, want 3", cluster.Spec.Instances)
	}
	if cluster.Spec.StorageConfiguration.Size != "10Gi" {
		t.Errorf("storage size = %q, want 10Gi", cluster.Spec.StorageConfiguration.Size)
	}
	if len(cluster.OwnerReferences) != 1 || cluster.OwnerReferences[0].UID != app.UID {
		t.Errorf("expected controller owner reference to the NebariApp, got %+v", cluster.OwnerReferences)
	}

	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonDatabaseProvisioning {
		t.Errorf("expected DatabaseReady=False/DatabaseProvisioning, got %+v", cond)
	}
}

func TestReconcileDatabase_Defaults(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	// Zero-valued instances/size simulate a spec that skipped API defaulting.
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})

	if _, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app); err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}

	cluster := &cnpgv1.Cluster{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db", Namespace: "team-a"}, cluster); err != nil {
		t.Fatalf("get cluster: %v", err)
	}
	if cluster.Spec.Instances != 1 {
		t.Errorf("Instances = %d, want default 1", cluster.Spec.Instances)
	}
	if cluster.Spec.StorageConfiguration.Size != "1Gi" {
		t.Errorf("storage size = %q, want default 1Gi", cluster.Spec.StorageConfiguration.Size)
	}
}

func TestReconcileDatabase_ReadyWritesSecretAndRBAC(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})
	cluster := readyCluster(app, scheme, t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cluster, cnpgAppSecret(t, app, cluster, scheme)).
		Build()

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result != nil {
		t.Fatalf("expected completion (nil result), got %+v", result)
	}

	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db-credentials", Namespace: "team-a"}, secret); err != nil {
		t.Fatalf("expected credentials secret: %v", err)
	}
	want := map[string]string{
		"host":     "myapp-db-rw",
		"port":     "5432",
		"username": "app",
		"password": "hunter2",
		"database": "app",
		"uri":      "postgresql://app:hunter2@myapp-db-rw.team-a:5432/app",
	}
	for k, v := range want {
		if got := string(secret.Data[k]); got != v {
			t.Errorf("credentials[%s] = %q, want %q", k, got, v)
		}
	}
	if len(secret.Data) != len(want) {
		t.Errorf("credentials secret has %d keys, want %d (no CNPG-internal keys copied)", len(secret.Data), len(want))
	}

	role := &rbacv1.Role{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db-secret-reader", Namespace: "team-a"}, role); err != nil {
		t.Fatalf("expected Role: %v", err)
	}
	if len(role.Rules) != 1 || role.Rules[0].ResourceNames[0] != "myapp-db-credentials" {
		t.Errorf("Role rules = %+v, want get on myapp-db-credentials", role.Rules)
	}
	rb := &rbacv1.RoleBinding{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db-secret-reader", Namespace: "team-a"}, rb); err != nil {
		t.Fatalf("expected RoleBinding: %v", err)
	}
	if rb.Subjects[0].Name != "myapp" {
		t.Errorf("RoleBinding subject = %q, want the default ServiceAccount name myapp", rb.Subjects[0].Name)
	}

	if app.Status.DatabaseSecretRef == nil || app.Status.DatabaseSecretRef.Name != "myapp-db-credentials" {
		t.Errorf("status.databaseSecretRef = %+v, want myapp-db-credentials", app.Status.DatabaseSecretRef)
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != appsv1.ReasonAvailable {
		t.Errorf("expected DatabaseReady=True/Available, got %+v", cond)
	}
}

func TestReconcileDatabase_ReadyButSourceSecretMissing(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(readyCluster(app, scheme, t)).
		Build()

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("expected requeue, not error: %v", err)
	}
	if result == nil || result.RequeueAfter != 15*time.Second {
		t.Fatalf("expected 15s requeue while CNPG secret is missing, got %+v", result)
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Reason != appsv1.ReasonDatabaseProvisioning {
		t.Errorf("expected DatabaseProvisioning, got %+v", cond)
	}
}

func TestReconcileDatabase_DisabledKeepsExistingCluster(t *testing.T) {
	scheme := newScheme(t, true)
	enabledApp := newApp(&appsv1.DatabaseConfig{Enabled: true})
	cluster := readyCluster(enabledApp, scheme, t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cluster, cnpgAppSecret(t, enabledApp, cluster, scheme)).
		Build()

	app := newApp(&appsv1.DatabaseConfig{Enabled: false})
	// Status evidence of the previously provisioned database, as a real app
	// that was enabled and then disabled would carry.
	conditions.SetCondition(app, appsv1.ConditionTypeDatabaseReady, metav1.ConditionTrue,
		appsv1.ReasonAvailable, "Database is ready and credentials are available")
	app.Status.DatabaseSecretRef = &appsv1.ResourceReference{Name: "myapp-db-credentials", Namespace: "team-a"}

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result != nil {
		t.Fatalf("disable must not requeue, got %+v", result)
	}

	// Nothing deleted
	got := &cnpgv1.Cluster{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db", Namespace: "team-a"}, got); err != nil {
		t.Fatalf("cluster must survive disable: %v", err)
	}

	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonDatabaseDisabled {
		t.Errorf("expected DatabaseReady=False/DatabaseDisabled, got %+v", cond)
	}
}

func TestReconcileDatabase_AbsentBlockNoCondition(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(nil)

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected clean no-op, got result=%+v err=%v", result, err)
	}
	if cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady); cond != nil {
		t.Errorf("expected no DatabaseReady condition for absent block, got %+v", cond)
	}
}

func TestReconcileDatabase_CNPGNotInstalled(t *testing.T) {
	// Scheme WITHOUT cnpg types: the fake client returns a not-registered
	// error, standing in for the real cluster's no-CRD NoKindMatchError.
	scheme := newScheme(t, false)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("missing CRD must degrade to a condition, not an error: %v", err)
	}
	if result == nil || result.RequeueAfter != 5*time.Minute {
		t.Fatalf("expected 5m requeue, got %+v", result)
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Reason != appsv1.ReasonCNPGNotInstalled {
		t.Fatalf("expected CNPGNotInstalled, got %+v", cond)
	}
	if !strings.Contains(cond.Message, "database.enabled") {
		t.Errorf("message should point at NIC's database.enabled toggle, got %q", cond.Message)
	}
}

func TestReconcileDatabase_NameTooLong(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})
	app.Name = strings.Repeat("a", 48) // cluster name becomes 51 chars, over CNPG's 50 cap

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("invalid name must degrade to a condition, not an error: %v", err)
	}
	if result == nil || result.RequeueAfter != 5*time.Minute {
		t.Fatalf("expected 5m requeue, got %+v", result)
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonFailed {
		t.Fatalf("expected DatabaseReady=False/Failed, got %+v", cond)
	}
	if !strings.Contains(cond.Message, "50") {
		t.Errorf("message should state the 50-character limit, got %q", cond.Message)
	}
	clusters := &cnpgv1.ClusterList{}
	if err := c.List(context.Background(), clusters); err != nil {
		t.Fatalf("list clusters: %v", err)
	}
	if len(clusters.Items) != 0 {
		t.Errorf("no Cluster may be created for an invalid name, found %d", len(clusters.Items))
	}
}

func TestReconcileDatabase_DisabledForeignClusterClearsStatus(t *testing.T) {
	scheme := newScheme(t, true)
	foreign := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-db", Namespace: "team-a"},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(foreign).Build()

	app := newApp(&appsv1.DatabaseConfig{Enabled: false})
	conditions.SetCondition(app, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
		appsv1.ReasonFailed, "leftover from a failed enable attempt")
	app.Status.DatabaseSecretRef = &appsv1.ResourceReference{Name: "stale"}

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected clean no-op, got result=%+v err=%v", result, err)
	}
	if cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady); cond != nil {
		t.Errorf("stale condition must be cleared for a foreign cluster, got %+v", cond)
	}
	if app.Status.DatabaseSecretRef != nil {
		t.Errorf("stale DatabaseSecretRef must be cleared, got %+v", app.Status.DatabaseSecretRef)
	}
}

func TestReconcileDatabase_ReadySteadyStateNoDuplicateEvent(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})
	cluster := readyCluster(app, scheme, t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cluster, cnpgAppSecret(t, app, cluster, scheme)).
		Build()
	recorder := record.NewFakeRecorder(16)
	r := &DatabaseReconciler{Client: c, Scheme: scheme, Recorder: recorder}

	for i := 0; i < 2; i++ {
		result, err := r.ReconcileDatabase(context.Background(), app)
		if err != nil || result != nil {
			t.Fatalf("reconcile %d: result=%+v err=%v", i, result, err)
		}
	}

	events := 0
	for {
		select {
		case e := <-recorder.Events:
			if strings.Contains(e, appsv1.EventReasonDatabaseProvisioned) {
				events++
			}
			continue
		default:
		}
		break
	}
	if events != 1 {
		t.Errorf("DatabaseProvisioned events = %d, want exactly 1 (first transition only)", events)
	}
}

func TestReconcileDatabase_CustomServiceAccount(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})
	app.Spec.ServiceAccountName = "custom-sa"
	cluster := readyCluster(app, scheme, t)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cluster, cnpgAppSecret(t, app, cluster, scheme)).
		Build()

	if _, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app); err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	rb := &rbacv1.RoleBinding{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db-secret-reader", Namespace: "team-a"}, rb); err != nil {
		t.Fatalf("expected RoleBinding: %v", err)
	}
	if rb.Subjects[0].Name != "custom-sa" {
		t.Errorf("RoleBinding subject = %q, want custom-sa", rb.Subjects[0].Name)
	}
}

func TestNormalizeCredentials_MissingKey(t *testing.T) {
	src := map[string][]byte{"host": []byte("h"), "port": []byte("5432")}
	if _, err := normalizeCredentials(src); err == nil {
		t.Fatal("expected error for missing source keys")
	}
}

func TestReconcileDatabase_ForeignSourceSecretRejected(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(&appsv1.DatabaseConfig{Enabled: true})
	cluster := readyCluster(app, scheme, t)
	unowned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-db-app", Namespace: "team-a"},
		Data:       map[string][]byte{"host": []byte("h"), "port": []byte("5432"), "username": []byte("u"), "password": []byte("p"), "dbname": []byte("d"), "uri": []byte("postgresql://u:p@h:5432/d")},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, unowned).Build()

	_, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err == nil {
		t.Fatal("expected error for a source secret not owned by our Cluster")
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Reason != appsv1.ReasonFailed {
		t.Errorf("expected DatabaseReady=False/Failed, got %+v", cond)
	}
}
