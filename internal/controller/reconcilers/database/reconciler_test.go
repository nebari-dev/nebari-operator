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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// mainCredentials is the credentials Secret of the database "main" of the
// app newApp returns.
const mainCredentials = "myapp-main-db-credentials"

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

func newApp(databases ...appsv1.DatabaseConfig) *appsv1.NebariApp {
	return &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "team-a", UID: "uid-1"},
		Spec: appsv1.NebariAppSpec{
			Hostname:  "myapp.example.com",
			Service:   appsv1.ServiceReference{Name: "myapp", Port: 8080},
			Databases: databases,
		},
	}
}

func newReconciler(c client.Client, scheme *runtime.Scheme) *DatabaseReconciler {
	return &DatabaseReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(32),
	}
}

// readyCluster returns the CNPG Cluster the reconciler would have created for
// the database called dbName, with the Ready condition set (as the CNPG
// operator would in a real cluster).
func readyCluster(t *testing.T, app *appsv1.NebariApp, scheme *runtime.Scheme, dbName string) *cnpgv1.Cluster {
	t.Helper()
	name := app.Name + "-" + dbName + "-db"
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: app.Namespace,
			UID:       types.UID("cluster-uid-" + name),
			Labels: map[string]string{
				labelAppName:      app.Name,
				labelAppNamespace: app.Namespace,
				labelDatabaseName: dbName,
			},
		},
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

// cnpgAppSecret returns the app-user Secret CNPG generates for cluster.
func cnpgAppSecret(t *testing.T, cluster *cnpgv1.Cluster, scheme *runtime.Scheme) *corev1.Secret {
	t.Helper()
	host := cluster.Name + "-rw"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cluster.Name + "-app", Namespace: cluster.Namespace},
		Data: map[string][]byte{
			"host":     []byte(host),
			"port":     []byte("5432"),
			"username": []byte("app"),
			"password": []byte("hunter2"),
			"dbname":   []byte("app"),
			"uri":      []byte("postgresql://app:hunter2@" + host + ".team-a:5432/app"),
			"jdbc-uri": []byte("jdbc:postgresql://" + host + ".team-a:5432/app"),
		},
	}
	if err := controllerReference(cluster, secret, scheme); err != nil {
		t.Fatalf("set owner ref: %v", err)
	}
	return secret
}

// readyDatabase returns the objects that make the database dbName fully
// provisioned from CNPG's side.
func readyDatabase(t *testing.T, app *appsv1.NebariApp, scheme *runtime.Scheme, dbName string) []client.Object {
	t.Helper()
	cluster := readyCluster(t, app, scheme, dbName)
	return []client.Object{cluster, cnpgAppSecret(t, cluster, scheme)}
}

func getCluster(t *testing.T, c client.Client, name string) *cnpgv1.Cluster {
	t.Helper()
	cluster := &cnpgv1.Cluster{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "team-a"}, cluster); err != nil {
		t.Fatalf("get Cluster %s: %v", name, err)
	}
	return cluster
}

func secretReaderNames(t *testing.T, c client.Client) []string {
	t.Helper()
	role := &rbacv1.Role{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db-secret-reader", Namespace: "team-a"}, role); err != nil {
		t.Fatalf("expected Role: %v", err)
	}
	if len(role.Rules) != 1 {
		t.Fatalf("Role rules = %+v, want exactly one", role.Rules)
	}
	if got := role.Rules[0].Verbs; len(got) != 1 || got[0] != "get" {
		t.Errorf("Role verbs = %v, want [get]", got)
	}
	return role.Rules[0].ResourceNames
}

func databaseStatus(app *appsv1.NebariApp, name string) *appsv1.DatabaseStatus {
	for i := range app.Status.Databases {
		if app.Status.Databases[i].Name == name {
			return &app.Status.Databases[i]
		}
	}
	return nil
}

func countEvents(recorder *record.FakeRecorder, reason string) int {
	n := 0
	for {
		select {
		case e := <-recorder.Events:
			if strings.Contains(e, reason) {
				n++
			}
		default:
			return n
		}
	}
}

func TestReconcileDatabase_CreatesCluster(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(appsv1.DatabaseConfig{Name: "main", Instances: 3, Size: "10Gi"})

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result == nil || result.RequeueAfter != 15*time.Second {
		t.Fatalf("expected 15s provisioning requeue, got %+v", result)
	}

	cluster := getCluster(t, c, "myapp-main-db")
	if cluster.Spec.Instances != 3 {
		t.Errorf("Instances = %d, want 3", cluster.Spec.Instances)
	}
	if cluster.Spec.StorageConfiguration.Size != "10Gi" {
		t.Errorf("storage size = %q, want 10Gi", cluster.Spec.StorageConfiguration.Size)
	}
	if cluster.Spec.EnablePDB == nil || !*cluster.Spec.EnablePDB {
		t.Errorf("EnablePDB = %v, want true for a database with replicas", cluster.Spec.EnablePDB)
	}
	if len(cluster.OwnerReferences) != 1 || cluster.OwnerReferences[0].UID != app.UID {
		t.Errorf("expected controller owner reference to the NebariApp, got %+v", cluster.OwnerReferences)
	}
	wantLabels := map[string]string{labelAppName: "myapp", labelAppNamespace: "team-a", labelDatabaseName: "main"}
	for k, v := range wantLabels {
		if cluster.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, cluster.Labels[k], v)
		}
	}

	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonDatabaseProvisioning {
		t.Errorf("expected DatabaseReady=False/DatabaseProvisioning, got %+v", cond)
	}
	if st := databaseStatus(app, "main"); st == nil || st.SecretRef != nil || st.Retained {
		t.Errorf("status.databases[main] = %+v, want an entry with no secretRef yet", st)
	}
}

func TestReconcileDatabase_Defaults(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	// Zero-valued instances/size simulate a spec that skipped API defaulting.
	app := newApp(appsv1.DatabaseConfig{Name: "main"})

	if _, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app); err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}

	cluster := getCluster(t, c, "myapp-main-db")
	if cluster.Spec.Instances != 1 {
		t.Errorf("Instances = %d, want default 1", cluster.Spec.Instances)
	}
	if cluster.Spec.StorageConfiguration.Size != "1Gi" {
		t.Errorf("storage size = %q, want default 1Gi", cluster.Spec.StorageConfiguration.Size)
	}
	// A single instance must not get a PodDisruptionBudget: it would make the
	// node hosting it impossible to drain.
	if cluster.Spec.EnablePDB == nil || *cluster.Spec.EnablePDB {
		t.Errorf("EnablePDB = %v, want false for a single-instance database", cluster.Spec.EnablePDB)
	}
}

func TestReconcileDatabase_PodDisruptionBudgetFollowsInstances(t *testing.T) {
	tests := []struct {
		name      string
		instances int32
		want      bool
	}{
		{name: "unset instances defaults to one", instances: 0, want: false},
		{name: "one instance", instances: 1, want: false},
		{name: "two instances", instances: 2, want: true},
		{name: "nine instances", instances: 9, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newScheme(t, true)
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			app := newApp(appsv1.DatabaseConfig{Name: "main", Instances: tt.instances})

			if _, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app); err != nil {
				t.Fatalf("ReconcileDatabase: %v", err)
			}

			got := getCluster(t, c, "myapp-main-db").Spec.EnablePDB
			if got == nil || *got != tt.want {
				t.Errorf("EnablePDB = %v, want %t", got, tt.want)
			}
		})
	}
}

func TestReconcileDatabase_PodDisruptionBudgetTracksScaling(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := newReconciler(c, scheme)
	app := newApp(appsv1.DatabaseConfig{Name: "main", Instances: 1})

	// One instance, then scaled up, then back down: the setting has to move
	// both ways on a Cluster that already exists.
	for _, step := range []struct {
		instances int32
		want      bool
	}{{1, false}, {3, true}, {1, false}} {
		app.Spec.Databases[0].Instances = step.instances
		if _, err := r.ReconcileDatabase(context.Background(), app); err != nil {
			t.Fatalf("ReconcileDatabase with %d instances: %v", step.instances, err)
		}
		got := getCluster(t, c, "myapp-main-db").Spec.EnablePDB
		if got == nil || *got != step.want {
			t.Errorf("with %d instances EnablePDB = %v, want %t", step.instances, got, step.want)
		}
	}
}

func TestReconcileDatabase_ReadyWritesSecretAndRBAC(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(readyDatabase(t, app, scheme, "main")...).
		Build()

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result != nil {
		t.Fatalf("expected completion (nil result), got %+v", result)
	}

	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: mainCredentials, Namespace: "team-a"}, secret); err != nil {
		t.Fatalf("expected credentials secret: %v", err)
	}
	want := map[string]string{
		"host":     "myapp-main-db-rw",
		"port":     "5432",
		"username": "app",
		"password": "hunter2",
		"database": "app",
		"uri":      "postgresql://app:hunter2@myapp-main-db-rw.team-a:5432/app",
	}
	for k, v := range want {
		if got := string(secret.Data[k]); got != v {
			t.Errorf("credentials[%s] = %q, want %q", k, got, v)
		}
	}
	if len(secret.Data) != len(want) {
		t.Errorf("credentials secret has %d keys, want %d (no CNPG-internal keys copied)", len(secret.Data), len(want))
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != app.UID {
		t.Errorf("credentials secret must be owned by the NebariApp, got %+v", secret.OwnerReferences)
	}

	if got := secretReaderNames(t, c); len(got) != 1 || got[0] != mainCredentials {
		t.Errorf("Role resourceNames = %v, want [myapp-main-db-credentials]", got)
	}
	rb := &rbacv1.RoleBinding{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-db-secret-reader", Namespace: "team-a"}, rb); err != nil {
		t.Fatalf("expected RoleBinding: %v", err)
	}
	if rb.Subjects[0].Name != "myapp" {
		t.Errorf("RoleBinding subject = %q, want the default ServiceAccount name myapp", rb.Subjects[0].Name)
	}

	st := databaseStatus(app, "main")
	if st == nil || st.SecretRef == nil || st.SecretRef.Name != mainCredentials || st.SecretRef.Namespace != "team-a" {
		t.Errorf("status.databases[main] = %+v, want secretRef team-a/myapp-main-db-credentials", st)
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != appsv1.ReasonAvailable {
		t.Errorf("expected DatabaseReady=True/Available, got %+v", cond)
	}
}

func TestReconcileDatabase_ReadyButSourceSecretMissing(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(readyCluster(t, app, scheme, "main")).
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

// One database that is still provisioning must not hold back another that is
// ready: its credentials are published and readable straight away.
func TestReconcileDatabase_MultipleDatabasesProgressIndependently(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(
		appsv1.DatabaseConfig{Name: "metadata"},
		appsv1.DatabaseConfig{Name: "warehouse", Instances: 3, Size: "20Gi"},
	)
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(readyDatabase(t, app, scheme, "metadata")...).
		Build()
	r := newReconciler(c, scheme)

	result, err := r.ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result == nil || result.RequeueAfter != 15*time.Second {
		t.Fatalf("expected 15s requeue while warehouse provisions, got %+v", result)
	}

	if got := getCluster(t, c, "myapp-warehouse-db"); got.Spec.Instances != 3 || got.Spec.StorageConfiguration.Size != "20Gi" {
		t.Errorf("warehouse cluster spec = %d instances / %s, want 3 / 20Gi", got.Spec.Instances, got.Spec.StorageConfiguration.Size)
	}
	if st := databaseStatus(app, "metadata"); st == nil || st.SecretRef == nil {
		t.Errorf("metadata is ready and must report its secretRef, got %+v", st)
	}
	if st := databaseStatus(app, "warehouse"); st == nil || st.SecretRef != nil {
		t.Errorf("warehouse is provisioning and must not report a secretRef, got %+v", st)
	}
	if got := secretReaderNames(t, c); len(got) != 1 || got[0] != "myapp-metadata-db-credentials" {
		t.Errorf("Role resourceNames = %v, want only the published metadata secret", got)
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonDatabaseProvisioning {
		t.Fatalf("expected DatabaseReady=False/DatabaseProvisioning, got %+v", cond)
	}
	if !strings.Contains(cond.Message, "warehouse") || strings.Contains(cond.Message, "metadata") {
		t.Errorf("message should name only the pending database, got %q", cond.Message)
	}

	// CNPG finishes the second cluster.
	warehouse := getCluster(t, c, "myapp-warehouse-db")
	warehouse.UID = "cluster-uid-warehouse"
	warehouse.Status.Conditions = []metav1.Condition{{
		Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue,
		Reason: "ClusterIsReady", LastTransitionTime: metav1.Now(),
	}}
	if err := c.Update(context.Background(), warehouse); err != nil {
		t.Fatalf("mark warehouse ready: %v", err)
	}
	if err := c.Create(context.Background(), cnpgAppSecret(t, warehouse, scheme)); err != nil {
		t.Fatalf("create warehouse app secret: %v", err)
	}

	result, err = r.ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected completion, got result=%+v err=%v", result, err)
	}
	if got := secretReaderNames(t, c); len(got) != 2 || got[0] != "myapp-metadata-db-credentials" || got[1] != "myapp-warehouse-db-credentials" {
		t.Errorf("Role resourceNames = %v, want both credentials secrets in spec order", got)
	}
	cond = conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != appsv1.ReasonAvailable {
		t.Errorf("expected DatabaseReady=True/Available, got %+v", cond)
	}
}

// Removing every entry is the old "enabled: false": nothing is deleted.
func TestReconcileDatabase_RemovedEntryRetainsCluster(t *testing.T) {
	scheme := newScheme(t, true)
	withDB := newApp(appsv1.DatabaseConfig{Name: "main", Instances: 3})
	objs := readyDatabase(t, withDB, scheme, "main")
	objs[0].(*cnpgv1.Cluster).Spec.Instances = 3
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	recorder := record.NewFakeRecorder(32)
	r := &DatabaseReconciler{Client: c, Scheme: scheme, Recorder: recorder}

	if result, err := r.ReconcileDatabase(context.Background(), withDB); err != nil || result != nil {
		t.Fatalf("initial reconcile: result=%+v err=%v", result, err)
	}

	// The entry is removed; status carries over as it would on a real object.
	app := newApp()
	app.Status = withDB.Status

	for i := 0; i < 2; i++ {
		result, err := r.ReconcileDatabase(context.Background(), app)
		if err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
		if result != nil {
			t.Fatalf("a retained database must not requeue, got %+v", result)
		}
	}

	cluster := getCluster(t, c, "myapp-main-db")
	if cluster.Spec.Instances != 3 {
		t.Errorf("retained cluster must not be modified, instances = %d", cluster.Spec.Instances)
	}
	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: mainCredentials, Namespace: "team-a"}, secret); err != nil {
		t.Errorf("credentials secret must survive removal: %v", err)
	}
	if got := secretReaderNames(t, c); len(got) != 1 || got[0] != mainCredentials {
		t.Errorf("Role resourceNames = %v, want the retained database's secret to stay readable", got)
	}

	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonDatabaseRetained {
		t.Errorf("expected DatabaseReady=False/DatabaseRetained, got %+v", cond)
	}
	st := databaseStatus(app, "main")
	if st == nil || !st.Retained || st.SecretRef == nil || st.SecretRef.Name != mainCredentials {
		t.Errorf("status.databases[main] = %+v, want retained with its secretRef", st)
	}
	if n := countEvents(recorder, appsv1.EventReasonDatabaseRetained); n != 1 {
		t.Errorf("DatabaseRetained events = %d, want exactly 1 (first transition only)", n)
	}
}

func TestReconcileDatabase_RemovedEntryAlongsideActiveOne(t *testing.T) {
	scheme := newScheme(t, true)
	both := newApp(appsv1.DatabaseConfig{Name: "main"}, appsv1.DatabaseConfig{Name: "old"})
	objs := append(readyDatabase(t, both, scheme, "main"), readyDatabase(t, both, scheme, "old")...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	r := newReconciler(c, scheme)

	if result, err := r.ReconcileDatabase(context.Background(), both); err != nil || result != nil {
		t.Fatalf("initial reconcile: result=%+v err=%v", result, err)
	}

	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	app.Status = both.Status
	result, err := r.ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected completion, got result=%+v err=%v", result, err)
	}

	getCluster(t, c, "myapp-old-db")
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != appsv1.ReasonAvailable {
		t.Fatalf("the requested database is ready, expected DatabaseReady=True/Available, got %+v", cond)
	}
	if !strings.Contains(cond.Message, "myapp-old-db") {
		t.Errorf("message should mention the retained cluster, got %q", cond.Message)
	}
	if st := databaseStatus(app, "main"); st == nil || st.Retained {
		t.Errorf("status.databases[main] = %+v, want managed", st)
	}
	if st := databaseStatus(app, "old"); st == nil || !st.Retained {
		t.Errorf("status.databases[old] = %+v, want retained", st)
	}
	if got := secretReaderNames(t, c); len(got) != 2 {
		t.Errorf("Role resourceNames = %v, want both secrets", got)
	}
}

func TestReconcileDatabase_ReAddedEntryResumesManagement(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main", Instances: 3, Size: "5Gi"})
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(readyDatabase(t, app, scheme, "main")...).
		Build()
	app.Status.Databases = []appsv1.DatabaseStatus{{
		Name:      "main",
		Retained:  true,
		SecretRef: &appsv1.ResourceReference{Name: mainCredentials, Namespace: "team-a"},
	}}

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected completion, got result=%+v err=%v", result, err)
	}
	cluster := getCluster(t, c, "myapp-main-db")
	if cluster.Spec.Instances != 3 || cluster.Spec.StorageConfiguration.Size != "5Gi" {
		t.Errorf("re-added entry must be applied, got %d instances / %s", cluster.Spec.Instances, cluster.Spec.StorageConfiguration.Size)
	}
	if st := databaseStatus(app, "main"); st == nil || st.Retained {
		t.Errorf("status.databases[main] = %+v, want managed again", st)
	}
}

func TestReconcileDatabase_NoDatabasesNoCondition(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp()

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected clean no-op, got result=%+v err=%v", result, err)
	}
	if cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady); cond != nil {
		t.Errorf("expected no DatabaseReady condition when no database is requested, got %+v", cond)
	}
}

// Once a retained Cluster has been deleted by hand, nothing is left to report.
func TestReconcileDatabase_RetainedClusterDeletedClearsStatus(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp()
	conditions.SetCondition(app, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
		appsv1.ReasonDatabaseRetained, "No database is requested; retained and no longer managed: Cluster myapp-main-db")
	app.Status.Databases = []appsv1.DatabaseStatus{{Name: "main", Retained: true}}

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected clean no-op, got result=%+v err=%v", result, err)
	}
	if cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady); cond != nil {
		t.Errorf("stale condition must be cleared, got %+v", cond)
	}
	if len(app.Status.Databases) != 0 {
		t.Errorf("stale status.databases must be cleared, got %+v", app.Status.Databases)
	}
}

func TestReconcileDatabase_CNPGNotInstalled(t *testing.T) {
	// Scheme WITHOUT cnpg types: the fake client returns a not-registered
	// error, standing in for the real cluster's no-CRD NoKindMatchError.
	scheme := newScheme(t, false)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(appsv1.DatabaseConfig{Name: "main"})

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
	if !strings.Contains(cond.Message, "CloudNativePG") {
		t.Errorf("message should tell the user the CloudNativePG operator is missing, got %q", cond.Message)
	}
}

func TestReconcileDatabase_CNPGNotInstalledNothingRequestedClearsStatus(t *testing.T) {
	scheme := newScheme(t, false)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp()
	conditions.SetCondition(app, appsv1.ConditionTypeDatabaseReady, metav1.ConditionFalse,
		appsv1.ReasonCNPGNotInstalled, "leftover from when a database was requested")

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil || result != nil {
		t.Fatalf("expected clean no-op, got result=%+v err=%v", result, err)
	}
	if cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady); cond != nil {
		t.Errorf("stale condition must be cleared, got %+v", cond)
	}
}

func TestReconcileDatabase_NameTooLong(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	app.Name = strings.Repeat("a", 43) // "<app>-main-db" becomes 51 chars, over CNPG's 50 cap

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

// A blocked entry must not stop a valid one in the same list.
func TestReconcileDatabase_BlockedEntryDoesNotStopOthers(t *testing.T) {
	scheme := newScheme(t, true)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	app := newApp(
		appsv1.DatabaseConfig{Name: "a-very-long-db-name"}, // 19 chars: within the CRD's cap
		appsv1.DatabaseConfig{Name: "ok"},
	)
	app.Name = strings.Repeat("a", 30) // 30+1+19+3 = 53 for the first, 30+1+2+3 = 36 for the second

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result == nil || result.RequeueAfter != 15*time.Second {
		t.Fatalf("expected the provisioning poll for the valid entry, got %+v", result)
	}
	getCluster(t, c, app.Name+"-ok-db")
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Reason != appsv1.ReasonFailed {
		t.Fatalf("expected DatabaseReady=False/Failed, got %+v", cond)
	}
	if !strings.Contains(cond.Message, "a-very-long-db-name") || !strings.Contains(cond.Message, "ok: ") {
		t.Errorf("message should cover both entries, got %q", cond.Message)
	}
}

// A Cluster that happens to carry the name an entry needs is never adopted,
// resized, or exposed: it may be hand-made, or belong to another NebariApp
// whose app and database names concatenate to the same string.
func TestReconcileDatabase_ForeignClusterNotAdopted(t *testing.T) {
	scheme := newScheme(t, true)
	other := &appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "myapp-main", Namespace: "team-a", UID: "uid-other"}}

	unowned := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-main-db", Namespace: "team-a"},
		Spec:       cnpgv1.ClusterSpec{Instances: 5},
	}
	otherApps := readyCluster(t, other, scheme, "x")
	otherApps.Name = "myapp-main-db"
	otherApps.Spec.Instances = 5
	// Worst case: the other owner even carries our app label.
	otherApps.Labels[labelAppName] = "myapp"

	for name, foreign := range map[string]*cnpgv1.Cluster{"unowned": unowned, "owned by another NebariApp": otherApps} {
		t.Run(name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(foreign, cnpgAppSecret(t, foreign, scheme)).
				Build()
			app := newApp(appsv1.DatabaseConfig{Name: "main"})

			result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
			if err != nil {
				t.Fatalf("a foreign cluster must degrade to a condition, not an error: %v", err)
			}
			if result == nil || result.RequeueAfter != 5*time.Minute {
				t.Fatalf("expected 5m requeue, got %+v", result)
			}

			got := getCluster(t, c, "myapp-main-db")
			if got.Spec.Instances != 5 {
				t.Errorf("foreign cluster must not be resized, instances = %d", got.Spec.Instances)
			}
			if metav1.IsControlledBy(got, app) {
				t.Error("foreign cluster must not be adopted")
			}
			secret := &corev1.Secret{}
			err = c.Get(context.Background(), types.NamespacedName{Name: mainCredentials, Namespace: "team-a"}, secret)
			if !apierrors.IsNotFound(err) {
				t.Errorf("no credentials may be published from a foreign cluster, get err = %v", err)
			}
			role := &rbacv1.Role{}
			err = c.Get(context.Background(), types.NamespacedName{Name: "myapp-db-secret-reader", Namespace: "team-a"}, role)
			if !apierrors.IsNotFound(err) {
				t.Errorf("no Role may be created when nothing is published, get err = %v", err)
			}

			cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
			if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonFailed {
				t.Fatalf("expected DatabaseReady=False/Failed, got %+v", cond)
			}
			if !strings.Contains(cond.Message, "not managed by this NebariApp") {
				t.Errorf("message should explain the conflict, got %q", cond.Message)
			}
			if st := databaseStatus(app, "main"); st == nil || st.SecretRef != nil || st.Retained {
				t.Errorf("status.databases[main] = %+v, want an entry with no secretRef", st)
			}
		})
	}
}

func TestReconcileDatabase_ReadySteadyStateNoDuplicateEvent(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main"}, appsv1.DatabaseConfig{Name: "other"})
	objs := append(readyDatabase(t, app, scheme, "main"), readyDatabase(t, app, scheme, "other")...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	recorder := record.NewFakeRecorder(32)
	r := &DatabaseReconciler{Client: c, Scheme: scheme, Recorder: recorder}

	for i := 0; i < 2; i++ {
		result, err := r.ReconcileDatabase(context.Background(), app)
		if err != nil || result != nil {
			t.Fatalf("reconcile %d: result=%+v err=%v", i, result, err)
		}
	}

	if n := countEvents(recorder, appsv1.EventReasonDatabaseProvisioned); n != 2 {
		t.Errorf("DatabaseProvisioned events = %d, want exactly 2 (one per database, first transition only)", n)
	}
}

func TestReconcileDatabase_CustomServiceAccount(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	app.Spec.ServiceAccountName = "custom-sa"
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(readyDatabase(t, app, scheme, "main")...).
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
	app := newApp(appsv1.DatabaseConfig{Name: "main"}, appsv1.DatabaseConfig{Name: "other"})
	unowned := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-main-db-app", Namespace: "team-a"},
		Data:       map[string][]byte{"host": []byte("h"), "port": []byte("5432"), "username": []byte("u"), "password": []byte("p"), "dbname": []byte("d"), "uri": []byte("postgresql://u:p@h:5432/d")},
	}
	objs := append(readyDatabase(t, app, scheme, "other"), readyCluster(t, app, scheme, "main"), unowned)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	recorder := record.NewFakeRecorder(32)
	r := &DatabaseReconciler{Client: c, Scheme: scheme, Recorder: recorder}

	for i := 0; i < 2; i++ {
		if _, err := r.ReconcileDatabase(context.Background(), app); err == nil {
			t.Fatal("expected error for a source secret not owned by our Cluster")
		}
	}

	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Reason != appsv1.ReasonFailed {
		t.Errorf("expected DatabaseReady=False/Failed, got %+v", cond)
	}
	if n := countEvents(recorder, appsv1.EventReasonDatabaseProvisionFailed); n != 1 {
		t.Errorf("DatabaseProvisionFailed events = %d, want exactly 1 for a repeated identical failure", n)
	}
	// The healthy database in the same list is still served.
	if st := databaseStatus(app, "other"); st == nil || st.SecretRef == nil {
		t.Errorf("status.databases[other] = %+v, want its secretRef despite the failing sibling", st)
	}
	if st := databaseStatus(app, "main"); st == nil || st.SecretRef != nil {
		t.Errorf("status.databases[main] = %+v, want no secretRef", st)
	}
}

// A same-named Secret that the operator did not create is left alone.
func TestReconcileDatabase_ForeignCredentialsSecretNotOverwritten(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	handMade := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: mainCredentials, Namespace: "team-a"},
		Data:       map[string][]byte{"password": []byte("mine")},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(append(readyDatabase(t, app, scheme, "main"), handMade)...).
		Build()

	if _, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app); err == nil {
		t.Fatal("expected error for a credentials secret the operator does not own")
	}
	got := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: mainCredentials, Namespace: "team-a"}, got); err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(got.Data["password"]) != "mine" || len(got.Data) != 1 {
		t.Errorf("hand-made secret must be left untouched, got keys %v", got.Data)
	}
}

// notReady clears the Ready condition CNPG sets, as it does while a Cluster
// adds a replica or rolls its instances.
func notReady(cluster *cnpgv1.Cluster) *cnpgv1.Cluster {
	cluster.Status.Conditions[0].Status = metav1.ConditionFalse
	cluster.Status.Phase = "Creating a new replica"
	return cluster
}

// A database that already published credentials keeps them current while its
// Cluster is temporarily not Ready, so a rotated password is not held back
// until the Cluster settles.
func TestReconcileDatabase_NotReadyClusterStillRefreshesPublishedCredentials(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	cluster := readyCluster(t, app, scheme, "main")
	source := cnpgAppSecret(t, cluster, scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster, source).Build()
	r := newReconciler(c, scheme)

	if result, err := r.ReconcileDatabase(context.Background(), app); err != nil || result != nil {
		t.Fatalf("initial reconcile: result=%+v err=%v", result, err)
	}

	// The Cluster goes busy and CNPG rotates the password.
	current := getCluster(t, c, "myapp-main-db")
	if err := c.Update(context.Background(), notReady(current)); err != nil {
		t.Fatalf("mark cluster not ready: %v", err)
	}
	rotated := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "myapp-main-db-app", Namespace: "team-a"}, rotated); err != nil {
		t.Fatalf("get source secret: %v", err)
	}
	rotated.Data["password"] = []byte("rotated")
	if err := c.Update(context.Background(), rotated); err != nil {
		t.Fatalf("rotate password: %v", err)
	}

	result, err := r.ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result == nil || result.RequeueAfter != 15*time.Second {
		t.Fatalf("expected the 15s poll while the cluster is busy, got %+v", result)
	}
	cond := conditions.GetCondition(app, appsv1.ConditionTypeDatabaseReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonDatabaseProvisioning {
		t.Errorf("expected DatabaseReady=False/DatabaseProvisioning, got %+v", cond)
	}
	credentials := &corev1.Secret{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: mainCredentials, Namespace: "team-a"}, credentials); err != nil {
		t.Fatalf("get credentials secret: %v", err)
	}
	if got := string(credentials.Data["password"]); got != "rotated" {
		t.Errorf("published password = %q, want the rotated one", got)
	}
	if st := databaseStatus(app, "main"); st == nil || st.SecretRef == nil {
		t.Errorf("status.databases[main] = %+v, want its secretRef kept", st)
	}
}

// Credentials are published for the first time only once the Cluster is
// Ready, even though CNPG creates its own Secret earlier.
func TestReconcileDatabase_NotReadyClusterDoesNotPublishFirstCredentials(t *testing.T) {
	scheme := newScheme(t, true)
	app := newApp(appsv1.DatabaseConfig{Name: "main"})
	cluster := notReady(readyCluster(t, app, scheme, "main"))
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cluster, cnpgAppSecret(t, cluster, scheme)).
		Build()

	result, err := newReconciler(c, scheme).ReconcileDatabase(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileDatabase: %v", err)
	}
	if result == nil || result.RequeueAfter != 15*time.Second {
		t.Fatalf("expected 15s provisioning requeue, got %+v", result)
	}
	err = c.Get(context.Background(), types.NamespacedName{Name: mainCredentials, Namespace: "team-a"}, &corev1.Secret{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("credentials must not be published before the cluster is ready, get err = %v", err)
	}
	if st := databaseStatus(app, "main"); st == nil || st.SecretRef != nil {
		t.Errorf("status.databases[main] = %+v, want no secretRef yet", st)
	}
}
