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

package validation

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

func TestValidateNamespaceOptIn(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		namespace   *corev1.Namespace
		expectError bool
	}{
		{
			name: "Valid namespace with opt-in label",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns",
					Labels: map[string]string{
						ManagedNamespaceLabel: "true",
					},
				},
			},
			expectError: false,
		},
		{
			name: "Namespace without opt-in label",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-ns",
					Labels: map[string]string{},
				},
			},
			expectError: true,
		},
		{
			name: "Namespace with wrong label value",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ns",
					Labels: map[string]string{
						ManagedNamespaceLabel: "false",
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.namespace).
				Build()

			nicApp := &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: tt.namespace.Name,
				},
			}

			err := ValidateNamespaceOptIn(context.Background(), client, nicApp)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}
		})
	}
}

func TestValidateService(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		service     *corev1.Service
		nicApp      *appsv1.NicApp
		expectError bool
	}{
		{
			name: "Valid service with matching port",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: "default",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Port: 8080},
					},
				},
			},
			nicApp: &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NicAppSpec{
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
				},
			},
			expectError: false,
		},
		{
			name:    "Service not found",
			service: nil,
			nicApp: &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NicAppSpec{
					Service: appsv1.ServiceReference{
						Name: "nonexistent-service",
						Port: 8080,
					},
				},
			},
			expectError: true,
		},
		{
			name: "Service with wrong port",
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: "default",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{Port: 9090},
					},
				},
			},
			nicApp: &appsv1.NicApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NicAppSpec{
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nicApp)

			if tt.service != nil {
				builder = builder.WithObjects(tt.service)
			}

			client := builder.Build()

			err := ValidateService(context.Background(), client, tt.nicApp)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}
		})
	}
}
