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
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

// TestSecretToNebariApp covers the map function backing the user-provided TLS
// secret watch (see #115): a secret enqueues only the NebariApp(s) that
// reference it via routing.tls.secretName, and unrelated secrets enqueue
// nothing. Namespace filtering is handled by the watch predicate, not the map
// function; TestInGatewayNamespace covers it.
func TestSecretToNebariApp(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	appWithSecret := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "app-a", Namespace: "ns-a"},
		Spec: appsv1.NebariAppSpec{
			Routing: &appsv1.RoutingConfig{TLS: &appsv1.RoutingTLSConfig{SecretName: "my-tls"}},
		},
	}
	appNoSecretName := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "app-b", Namespace: "ns-b"},
		Spec: appsv1.NebariAppSpec{
			Routing: &appsv1.RoutingConfig{TLS: &appsv1.RoutingTLSConfig{}},
		},
	}
	appNoRouting := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "app-c", Namespace: "ns-c"},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(appWithSecret, appNoSecretName, appNoRouting).Build()
	r := &NebariAppReconciler{Client: c}

	tests := []struct {
		name       string
		secretName string
		want       []reconcile.Request
	}{
		{
			name:       "secret referenced by a NebariApp enqueues that app",
			secretName: "my-tls",
			want:       []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "app-a", Namespace: "ns-a"}}},
		},
		{
			name:       "unrelated secret enqueues nothing",
			secretName: "other-tls",
			want:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: tt.secretName, Namespace: constants.GatewayNamespace},
			}
			got := r.secretToNebariApp(context.Background(), secret)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("secretToNebariApp(%q) = %v, want %v", tt.secretName, got, tt.want)
			}
		})
	}
}

// TestInGatewayNamespace covers the predicate on the user-provided TLS secret
// watch (see #115): only secrets in the Gateway namespace reach the map function.
func TestInGatewayNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		want      bool
	}{
		{name: "secret in the Gateway namespace passes", namespace: constants.GatewayNamespace, want: true},
		{name: "secret in an app namespace is filtered out", namespace: "ns-a", want: false},
		{name: "secret with no namespace is filtered out", namespace: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: tt.namespace},
			}
			if got := inGatewayNamespace(secret); got != tt.want {
				t.Errorf("inGatewayNamespace(%q) = %v, want %v", tt.namespace, got, tt.want)
			}
		})
	}
}
