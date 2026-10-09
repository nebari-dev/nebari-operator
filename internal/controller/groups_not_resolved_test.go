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
	"testing"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth/providers"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/core"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/routing"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

// TestReconcile_GroupsNotResolved checks that a spec.auth.groups path missing
// from Keycloak leaves Ready=False with reason GroupsNotResolved, while the
// SecurityPolicy and the rest of the status are still applied.
func TestReconcile_GroupsNotResolved(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		appsv1.AddToScheme, corev1.AddToScheme, rbacv1.AddToScheme, egv1alpha1.AddToScheme, gwapiv1.Install,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}

	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "team", Generation: 1},
		Spec: appsv1.NebariAppSpec{
			Hostname: "app.nebari.local",
			Service:  appsv1.ServiceReference{Name: "app", Port: 8080},
			Auth: &appsv1.AuthConfig{
				Enabled:  true,
				Provider: constants.ProviderKeycloak,
				Groups:   []string{"/does-not-exist"},
			},
			LandingPage: &appsv1.LandingPageConfig{Enabled: true},
		},
	}
	objs := []runtime.Object{
		app,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name: "team", Labels: map[string]string{core.ManagedNamespaceLabel: "true"},
		}},
		&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "team"},
			Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: naming.ClientSecretName(app), Namespace: "team"},
			Data:       map[string][]byte{constants.ClientSecretKey: []byte("test-secret")},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).
		WithStatusSubresource(&appsv1.NebariApp{}).Build()

	recorder := record.NewFakeRecorder(20)
	r := &NebariAppReconciler{
		Client:            c,
		Scheme:            scheme,
		Recorder:          recorder,
		CoreReconciler:    &core.CoreReconciler{Client: c, Scheme: scheme, Recorder: recorder},
		RoutingReconciler: &routing.RoutingReconciler{Client: c, Scheme: scheme, Recorder: recorder},
		AuthReconciler: &auth.AuthReconciler{
			Client:   c,
			Scheme:   scheme,
			Recorder: recorder,
			Providers: map[string]providers.OIDCProvider{
				constants.ProviderKeycloak: groupsNotResolvedProvider{},
			},
		},
	}

	key := types.NamespacedName{Name: "app", Namespace: "team"}
	result, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected a requeue so the group lookup is retried")
	}

	sp := &egv1alpha1.SecurityPolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: naming.SecurityPolicyName(app), Namespace: "team"}, sp); err != nil {
		t.Fatalf("expected SecurityPolicy to be created: %v", err)
	}
	if sp.Spec.OIDC == nil {
		t.Error("expected SecurityPolicy to configure OIDC")
	}

	got := &appsv1.NebariApp{}
	if err := c.Get(ctx, key, got); err != nil {
		t.Fatal(err)
	}
	for _, condType := range []string{appsv1.ConditionTypeReady, appsv1.ConditionTypeAuthReady} {
		cond := meta.FindStatusCondition(got.Status.Conditions, condType)
		if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != appsv1.ReasonGroupsNotResolved {
			t.Errorf("expected %s=False/%s, got %+v", condType, appsv1.ReasonGroupsNotResolved, cond)
		}
	}
	if got.Status.ServiceDiscovery == nil || len(got.Status.ServiceDiscovery.RequiredGroups) != 1 ||
		got.Status.ServiceDiscovery.RequiredGroups[0] != "/does-not-exist" {
		t.Errorf("expected serviceDiscovery to publish the unresolved path, got %+v", got.Status.ServiceDiscovery)
	}
	if got.Status.ObservedGeneration != got.Generation {
		t.Errorf("ObservedGeneration = %d, want %d", got.Status.ObservedGeneration, got.Generation)
	}
}

// groupsNotResolvedProvider provisions everything except a missing group path.
type groupsNotResolvedProvider struct{}

func (groupsNotResolvedProvider) GetIssuerURL(context.Context, *appsv1.NebariApp) (string, error) {
	return "https://keycloak.example.com/realms/test", nil
}

func (groupsNotResolvedProvider) GetEndpointOverrides(context.Context, *appsv1.NebariApp) (providers.OIDCEndpointOverrides, error) {
	return providers.OIDCEndpointOverrides{}, nil
}

func (groupsNotResolvedProvider) GetExternalIssuerURL(context.Context, *appsv1.NebariApp) (string, error) {
	return "https://keycloak.example.com/realms/test", nil
}

func (groupsNotResolvedProvider) GetClientID(context.Context, *appsv1.NebariApp) string {
	return "test-client"
}

func (groupsNotResolvedProvider) ProvisionClient(context.Context, *appsv1.NebariApp) error {
	return &providers.GroupsNotResolvedError{Realm: "test", Paths: []string{"/does-not-exist"}}
}

func (groupsNotResolvedProvider) DeleteClient(context.Context, *appsv1.NebariApp) error { return nil }

func (groupsNotResolvedProvider) SupportsProvisioning() bool { return true }

func (groupsNotResolvedProvider) ConfigureTokenExchange(context.Context, *appsv1.NebariApp, []string) error {
	return nil
}

func (groupsNotResolvedProvider) CleanupTokenExchange(context.Context, *appsv1.NebariApp) error {
	return nil
}
