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

package routing

import (
	"context"
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Edge case tests for routing reconciliation

func TestReconcileRoutingEdgeCases(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)
	_ = corev1.AddToScheme(scheme)

	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name              string
		nebariApp         *appsv1.NebariApp
		gateway           *gatewayv1.Gateway
		existingHTTPRoute *gatewayv1.HTTPRoute
		expectError       bool
		validate          func(*testing.T, client.Client, *appsv1.NebariApp)
	}{
		{
			name: "Update existing HTTPRoute when spec changes",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
					UID:       "test-uid-123",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled: boolPtr(true),
						},
					},
				},
			},
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.PublicGatewayName,
					Namespace: constants.GatewayNamespace,
				},
			},
			existingHTTPRoute: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app-route",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "reconcilers.nebari.dev/v1",
							Kind:       "NebariApp",
							Name:       "test-app",
							UID:        "test-uid-123",
							Controller: boolPtr(true),
						},
					},
				},
				Spec: gatewayv1.HTTPRouteSpec{
					Hostnames: []gatewayv1.Hostname{"old-hostname.example.com"},
				},
			},
			expectError: false,
			validate: func(t *testing.T, c client.Client, app *appsv1.NebariApp) {
				route := &gatewayv1.HTTPRoute{}
				err := c.Get(context.Background(), client.ObjectKey{
					Name:      "test-app-route",
					Namespace: "default",
				}, route)
				if err != nil {
					t.Fatalf("failed to get HTTPRoute: %v", err)
				}
				if len(route.Spec.Hostnames) == 0 || string(route.Spec.Hostnames[0]) != "test.example.com" {
					t.Errorf("expected hostname to be updated to test.example.com, got %v", route.Spec.Hostnames)
				}
			},
		},
		{
			name: "Gateway becomes unavailable during reconciliation",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
					UID:       "test-uid-789",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled: boolPtr(true),
						},
					},
				},
			},
			gateway:     nil, // Gateway not available
			expectError: true,
			validate: func(t *testing.T, c client.Client, app *appsv1.NebariApp) {
				// HTTPRoute should not be created when Gateway is missing
				route := &gatewayv1.HTTPRoute{}
				err := c.Get(context.Background(), client.ObjectKey{
					Name:      "test-app-route",
					Namespace: "default",
				}, route)
				if err == nil {
					t.Error("HTTPRoute should not exist when Gateway is unavailable")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.gateway != nil {
				builder = builder.WithObjects(tt.gateway)
			}
			if tt.existingHTTPRoute != nil {
				builder = builder.WithObjects(tt.existingHTTPRoute)
			}
			c := builder.Build()

			reconciler := &RoutingReconciler{
				Client:   c,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.ReconcileRouting(context.Background(), tt.nebariApp)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}

			if tt.validate != nil {
				tt.validate(t, c, tt.nebariApp)
			}
		})
	}
}

func TestHTTPRouteOwnerReference(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)

	boolPtr := func(b bool) *bool { return &b }

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			UID:       "test-uid-owner",
		},
		Spec: appsv1.NebariAppSpec{
			Hostname: "test.example.com",
			Service: appsv1.ServiceReference{
				Name: "test-service",
				Port: 8080,
			},
			Routing: &appsv1.RoutingConfig{
				TLS: &appsv1.RoutingTLSConfig{
					Enabled: boolPtr(true),
				},
			},
		},
	}

	gateway := &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.PublicGatewayName,
			Namespace: constants.GatewayNamespace,
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(gateway).
		Build()

	reconciler := &RoutingReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
	}

	err := reconciler.ReconcileRouting(context.Background(), nebariApp)
	if err != nil {
		t.Fatalf("ReconcileRouting failed: %v", err)
	}

	route := &gatewayv1.HTTPRoute{}
	err = c.Get(context.Background(), client.ObjectKey{
		Name:      "test-app-route",
		Namespace: "default",
	}, route)
	if err != nil {
		t.Fatalf("failed to get HTTPRoute: %v", err)
	}

	if len(route.OwnerReferences) != 1 {
		t.Fatalf("expected 1 owner reference, got %d", len(route.OwnerReferences))
	}

	ownerRef := route.OwnerReferences[0]
	if ownerRef.Name != "test-app" {
		t.Errorf("expected owner name 'test-app', got '%s'", ownerRef.Name)
	}
	if ownerRef.UID != "test-uid-owner" {
		t.Errorf("expected owner UID 'test-uid-owner', got '%s'", ownerRef.UID)
	}
	if ownerRef.Controller == nil || !*ownerRef.Controller {
		t.Error("owner reference should have Controller=true")
	}
	if ownerRef.BlockOwnerDeletion == nil || !*ownerRef.BlockOwnerDeletion {
		t.Error("owner reference should have BlockOwnerDeletion=true")
	}
}
