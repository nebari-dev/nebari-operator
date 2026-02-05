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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

func TestValidateGateway(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gatewayv1.Install(scheme)

	tests := []struct {
		name        string
		gateway     *gatewayv1.Gateway
		gatewayName string
		expectError bool
	}{
		{
			name: "Gateway exists",
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.PublicGatewayName,
					Namespace: constants.GatewayNamespace,
				},
			},
			gatewayName: constants.PublicGatewayName,
			expectError: false,
		},
		{
			name:        "Gateway not found",
			gateway:     nil,
			gatewayName: constants.PublicGatewayName,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.gateway != nil {
				builder = builder.WithObjects(tt.gateway)
			}
			client := builder.Build()

			reconciler := &RoutingReconciler{
				Client:   client,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.validateGateway(context.Background(), tt.gatewayName)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}
		})
	}
}

func TestGetGatewayName(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	reconciler := &RoutingReconciler{
		Scheme: scheme,
	}

	tests := []struct {
		name            string
		nebariApp       *appsv1.NebariApp
		expectedGateway string
	}{
		{
			name: "Public gateway (default)",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Gateway: "public",
				},
			},
			expectedGateway: constants.PublicGatewayName,
		},
		{
			name: "Public gateway (empty)",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Gateway: "",
				},
			},
			expectedGateway: constants.PublicGatewayName,
		},
		{
			name: "Internal gateway",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Gateway: "internal",
				},
			},
			expectedGateway: constants.InternalGatewayName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gatewayName := reconciler.getGatewayName(tt.nebariApp)
			if gatewayName != tt.expectedGateway {
				t.Errorf("expected gateway=%s, got gateway=%s", tt.expectedGateway, gatewayName)
			}
		})
	}
}

func TestBuildHTTPRoute(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)

	reconciler := &RoutingReconciler{
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
	}

	tests := []struct {
		name                string
		nebariApp           *appsv1.NebariApp
		gatewayName         string
		expectedHostname    string
		expectedBackendPort int32
		expectedRulesCount  int
	}{
		{
			name: "Basic HTTPRoute without custom routes",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.nebari.local",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
				},
			},
			gatewayName:         constants.PublicGatewayName,
			expectedHostname:    "test.nebari.local",
			expectedBackendPort: 8080,
			expectedRulesCount:  1,
		},
		{
			name: "HTTPRoute with custom routes",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.nebari.local",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Routing: &appsv1.RoutingConfig{
						Routes: []appsv1.RouteMatch{
							{
								PathPrefix: "/api",
								PathType:   "PathPrefix",
							},
							{
								PathPrefix: "/app",
								PathType:   "Exact",
							},
						},
					},
				},
			},
			gatewayName:         constants.PublicGatewayName,
			expectedHostname:    "test.nebari.local",
			expectedBackendPort: 8080,
			expectedRulesCount:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := reconciler.buildHTTPRoute(tt.nebariApp, tt.gatewayName)

			// Check basic metadata
			if route.Name == "" {
				t.Error("HTTPRoute name should not be empty")
			}
			if route.Namespace != tt.nebariApp.Namespace {
				t.Errorf("expected namespace=%s, got namespace=%s", tt.nebariApp.Namespace, route.Namespace)
			}

			// Check parent refs
			if len(route.Spec.ParentRefs) != 1 {
				t.Errorf("expected 1 parent ref, got %d", len(route.Spec.ParentRefs))
			} else {
				if string(route.Spec.ParentRefs[0].Name) != tt.gatewayName {
					t.Errorf("expected gateway=%s, got gateway=%s", tt.gatewayName, route.Spec.ParentRefs[0].Name)
				}
				if string(*route.Spec.ParentRefs[0].Namespace) != constants.GatewayNamespace {
					t.Errorf("expected namespace=%s, got namespace=%s", constants.GatewayNamespace, *route.Spec.ParentRefs[0].Namespace)
				}
			}

			// Check hostnames
			if len(route.Spec.Hostnames) != 1 {
				t.Errorf("expected 1 hostname, got %d", len(route.Spec.Hostnames))
			} else {
				if string(route.Spec.Hostnames[0]) != tt.expectedHostname {
					t.Errorf("expected hostname=%s, got hostname=%s", tt.expectedHostname, route.Spec.Hostnames[0])
				}
			}

			// Check rules count
			if len(route.Spec.Rules) != tt.expectedRulesCount {
				t.Errorf("expected %d rules, got %d", tt.expectedRulesCount, len(route.Spec.Rules))
			}

			// Check backend refs
			for i, rule := range route.Spec.Rules {
				if len(rule.BackendRefs) != 1 {
					t.Errorf("rule %d: expected 1 backend ref, got %d", i, len(rule.BackendRefs))
				} else {
					backend := rule.BackendRefs[0]
					if string(backend.Name) != tt.nebariApp.Spec.Service.Name {
						t.Errorf("rule %d: expected backend name=%s, got=%s", i, tt.nebariApp.Spec.Service.Name, backend.Name)
					}
					if *backend.Port != tt.expectedBackendPort {
						t.Errorf("rule %d: expected backend port=%d, got=%d", i, tt.expectedBackendPort, *backend.Port)
					}
				}
			}
		})
	}
}

func TestBuildHTTPRouteRules(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	reconciler := &RoutingReconciler{
		Scheme: scheme,
	}

	tests := []struct {
		name               string
		nebariApp          *appsv1.NebariApp
		expectedRulesCount int
		checkPathType      bool
		expectedPathType   gatewayv1.PathMatchType
	}{
		{
			name: "Default route (no routes specified)",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
				},
			},
			expectedRulesCount: 1,
			checkPathType:      true,
			expectedPathType:   gatewayv1.PathMatchPathPrefix,
		},
		{
			name: "Multiple custom routes with different path types",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Routing: &appsv1.RoutingConfig{
						Routes: []appsv1.RouteMatch{
							{
								PathPrefix: "/api",
								PathType:   "PathPrefix",
							},
							{
								PathPrefix: "/exact-path",
								PathType:   "Exact",
							},
						},
					},
				},
			},
			expectedRulesCount: 2,
			checkPathType:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := reconciler.buildHTTPRouteRules(tt.nebariApp)

			if len(rules) != tt.expectedRulesCount {
				t.Errorf("expected %d rules, got %d", tt.expectedRulesCount, len(rules))
			}

			if tt.checkPathType && len(rules) > 0 {
				if len(rules[0].Matches) > 0 && rules[0].Matches[0].Path != nil {
					if *rules[0].Matches[0].Path.Type != tt.expectedPathType {
						t.Errorf("expected path type=%s, got=%s", tt.expectedPathType, *rules[0].Matches[0].Path.Type)
					}
				}
			}

			// Verify all rules have backend refs
			for i, rule := range rules {
				if len(rule.BackendRefs) == 0 {
					t.Errorf("rule %d: expected backend refs, got none", i)
				}
			}
		})
	}
}

func TestReconcileRouting(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name              string
		gateway           *gatewayv1.Gateway
		existingRoute     *gatewayv1.HTTPRoute
		nebariApp         *appsv1.NebariApp
		expectError       bool
		expectRouteCreate bool
		expectRouteUpdate bool
	}{
		{
			name: "Create new HTTPRoute",
			gateway: &gatewayv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.PublicGatewayName,
					Namespace: constants.GatewayNamespace,
				},
			},
			existingRoute: nil,
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.nebari.local",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
				},
			},
			expectError:       false,
			expectRouteCreate: true,
			expectRouteUpdate: false,
		},
		{
			name:    "Gateway not found",
			gateway: nil,
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.nebari.local",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
				},
			},
			expectError:       true,
			expectRouteCreate: false,
			expectRouteUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp)

			if tt.gateway != nil {
				builder = builder.WithObjects(tt.gateway)
			}
			if tt.existingRoute != nil {
				builder = builder.WithObjects(tt.existingRoute)
			}

			client := builder.Build()
			reconciler := &RoutingReconciler{
				Client:   client,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.ReconcileRouting(context.Background(), tt.nebariApp)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}

			if !tt.expectError {
				// Verify condition was set
				var foundCondition bool
				for _, cond := range tt.nebariApp.Status.Conditions {
					if cond.Type == appsv1.ConditionTypeRoutingReady {
						foundCondition = true
						break
					}
				}
				if !foundCondition {
					t.Error("expected RoutingReady condition to be set")
				}
			}
		})
	}
}

func TestCleanupHTTPRoute(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)

	tests := []struct {
		name          string
		existingRoute *gatewayv1.HTTPRoute
		nebariApp     *appsv1.NebariApp
		expectError   bool
	}{
		{
			name: "Delete existing HTTPRoute",
			existingRoute: &gatewayv1.HTTPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app-route",
					Namespace: "default",
				},
			},
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
			},
			expectError: false,
		},
		{
			name:          "HTTPRoute already deleted",
			existingRoute: nil,
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp)
			if tt.existingRoute != nil {
				builder = builder.WithObjects(tt.existingRoute)
			}

			client := builder.Build()
			reconciler := &RoutingReconciler{
				Client:   client,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.CleanupHTTPRoute(context.Background(), tt.nebariApp)
			if (err != nil) != tt.expectError {
				t.Errorf("expected error=%v, got error=%v", tt.expectError, err)
			}
		})
	}
}
