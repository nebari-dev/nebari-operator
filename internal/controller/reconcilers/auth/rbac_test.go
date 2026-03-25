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

package auth

import (
	"context"
	"fmt"
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	corev1 "k8s.io/api/core/v1"
)

func TestReconcileSecretRBAC(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name            string
		nebariApp       *appsv1.NebariApp
		expectedSAName  string
		validateRole    func(*testing.T, *rbacv1.Role, string)
		validateBinding func(*testing.T, *rbacv1.RoleBinding, string)
	}{
		{
			name: "Default ServiceAccount name - uses NebariApp name when ServiceAccountName is empty",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
			expectedSAName: "my-app",
		},
		{
			name: "Custom ServiceAccount name",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					ServiceAccountName: "custom-sa",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
			expectedSAName: "custom-sa",
		},
		{
			name: "Role has correct rules - get on specific secret by resourceNames",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "test-ns",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
			expectedSAName: "test-app",
			validateRole: func(t *testing.T, role *rbacv1.Role, secretName string) {
				if len(role.Rules) != 1 {
					t.Fatalf("expected 1 rule, got %d", len(role.Rules))
				}
				rule := role.Rules[0]
				if len(rule.APIGroups) != 1 || rule.APIGroups[0] != "" {
					t.Errorf("expected APIGroups=[\"\"], got %v", rule.APIGroups)
				}
				if len(rule.Resources) != 1 || rule.Resources[0] != "secrets" {
					t.Errorf("expected Resources=[\"secrets\"], got %v", rule.Resources)
				}
				if len(rule.ResourceNames) != 1 || rule.ResourceNames[0] != secretName {
					t.Errorf("expected ResourceNames=[%q], got %v", secretName, rule.ResourceNames)
				}
				if len(rule.Verbs) != 1 || rule.Verbs[0] != "get" {
					t.Errorf("expected Verbs=[\"get\"], got %v", rule.Verbs)
				}
			},
		},
		{
			name: "RoleBinding binds to correct ServiceAccount in correct namespace",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "test-ns",
				},
				Spec: appsv1.NebariAppSpec{
					ServiceAccountName: "my-service-account",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
			expectedSAName: "my-service-account",
			validateBinding: func(t *testing.T, rb *rbacv1.RoleBinding, rbacName string) {
				if rb.RoleRef.Kind != "Role" {
					t.Errorf("expected RoleRef.Kind=Role, got %s", rb.RoleRef.Kind)
				}
				if rb.RoleRef.APIGroup != "rbac.authorization.k8s.io" {
					t.Errorf("expected RoleRef.APIGroup=rbac.authorization.k8s.io, got %s", rb.RoleRef.APIGroup)
				}
				if rb.RoleRef.Name != rbacName {
					t.Errorf("expected RoleRef.Name=%q, got %s", rbacName, rb.RoleRef.Name)
				}
				if len(rb.Subjects) != 1 {
					t.Fatalf("expected 1 subject, got %d", len(rb.Subjects))
				}
				subject := rb.Subjects[0]
				if subject.Kind != "ServiceAccount" {
					t.Errorf("expected subject Kind=ServiceAccount, got %s", subject.Kind)
				}
				if subject.Name != "my-service-account" {
					t.Errorf("expected subject Name=my-service-account, got %s", subject.Name)
				}
				if subject.Namespace != "test-ns" {
					t.Errorf("expected subject Namespace=test-ns, got %s", subject.Namespace)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp).
				Build()

			reconciler := &AuthReconciler{
				Client:   client,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.reconcileSecretRBAC(context.Background(), tt.nebariApp)
			if err != nil {
				t.Fatalf("reconcileSecretRBAC returned unexpected error: %v", err)
			}

			rbacName := fmt.Sprintf("%s-oidc-secret-reader", tt.nebariApp.Name)
			secretName := naming.ClientSecretName(tt.nebariApp)

			// Verify Role was created
			role := &rbacv1.Role{}
			if err := client.Get(context.Background(), types.NamespacedName{
				Name:      rbacName,
				Namespace: tt.nebariApp.Namespace,
			}, role); err != nil {
				t.Fatalf("expected Role to exist, got error: %v", err)
			}

			if tt.validateRole != nil {
				tt.validateRole(t, role, secretName)
			}

			// Verify RoleBinding was created
			rb := &rbacv1.RoleBinding{}
			if err := client.Get(context.Background(), types.NamespacedName{
				Name:      rbacName,
				Namespace: tt.nebariApp.Namespace,
			}, rb); err != nil {
				t.Fatalf("expected RoleBinding to exist, got error: %v", err)
			}

			// Always verify the ServiceAccount binding
			if len(rb.Subjects) != 1 {
				t.Fatalf("expected 1 subject in RoleBinding, got %d", len(rb.Subjects))
			}
			if rb.Subjects[0].Name != tt.expectedSAName {
				t.Errorf("expected subject Name=%q, got %q", tt.expectedSAName, rb.Subjects[0].Name)
			}
			if rb.Subjects[0].Namespace != tt.nebariApp.Namespace {
				t.Errorf("expected subject Namespace=%q, got %q", tt.nebariApp.Namespace, rb.Subjects[0].Namespace)
			}

			if tt.validateBinding != nil {
				tt.validateBinding(t, rb, rbacName)
			}
		})
	}
}

func TestReconcileSecretRBAC_Update(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			Auth: &appsv1.AuthConfig{
				Enabled: true,
			},
		},
	}

	rbacName := fmt.Sprintf("%s-oidc-secret-reader", nebariApp.Name)

	// Pre-create Role and RoleBinding with stale content
	existingRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacName,
			Namespace: "default",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"list"},
			},
		},
	}
	existingRB := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacName,
			Namespace: "default",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     rbacName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "old-sa",
				Namespace: "default",
			},
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(nebariApp, existingRole, existingRB).
		Build()

	reconciler := &AuthReconciler{
		Client:   client,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
	}

	err := reconciler.reconcileSecretRBAC(context.Background(), nebariApp)
	if err != nil {
		t.Fatalf("reconcileSecretRBAC returned unexpected error: %v", err)
	}

	// Role should be updated with correct rules
	updatedRole := &rbacv1.Role{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: rbacName, Namespace: "default"}, updatedRole); err != nil {
		t.Fatalf("expected Role to exist after update: %v", err)
	}
	if len(updatedRole.Rules) != 1 || updatedRole.Rules[0].Resources[0] != "secrets" {
		t.Errorf("expected Role rules to be updated to secrets, got %v", updatedRole.Rules)
	}

	// RoleBinding should be updated with correct subject (defaults to nebariApp.Name)
	updatedRB := &rbacv1.RoleBinding{}
	if err := client.Get(context.Background(), types.NamespacedName{Name: rbacName, Namespace: "default"}, updatedRB); err != nil {
		t.Fatalf("expected RoleBinding to exist after update: %v", err)
	}
	if len(updatedRB.Subjects) != 1 || updatedRB.Subjects[0].Name != "my-app" {
		t.Errorf("expected RoleBinding subject to be updated to my-app, got %v", updatedRB.Subjects)
	}
}
