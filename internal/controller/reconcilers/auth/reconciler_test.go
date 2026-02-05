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
	"errors"
	"testing"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth/providers"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// boolPtr returns a pointer to a bool value
func boolPtr(b bool) *bool {
	return &b
}

// mockProvider implements OIDCProvider for testing
type mockProvider struct {
	issuerURL            string
	clientID             string
	supportsProvisioning bool
	provisionError       error
	deleteError          error
	issuerError          error
}

func (m *mockProvider) GetIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error) {
	if m.issuerError != nil {
		return "", m.issuerError
	}
	return m.issuerURL, nil
}

func (m *mockProvider) GetClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string {
	return m.clientID
}

func (m *mockProvider) ProvisionClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	return m.provisionError
}

func (m *mockProvider) DeleteClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	return m.deleteError
}

func (m *mockProvider) SupportsProvisioning() bool {
	return m.supportsProvisioning
}

func TestGetProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	keycloakProvider := &mockProvider{
		issuerURL: "https://keycloak.example.com/realms/test",
		clientID:  "test-client",
	}
	genericProvider := &mockProvider{
		issuerURL: "https://accounts.google.com",
		clientID:  "generic-client",
	}

	reconciler := &AuthReconciler{
		Scheme: scheme,
		Providers: map[string]providers.OIDCProvider{
			constants.ProviderKeycloak:    keycloakProvider,
			constants.ProviderGenericOIDC: genericProvider,
		},
	}

	tests := []struct {
		name             string
		nebariApp        *appsv1.NebariApp
		expectError      bool
		expectedProvider providers.OIDCProvider
	}{
		{
			name: "Keycloak provider specified",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:  true,
						Provider: constants.ProviderKeycloak,
					},
				},
			},
			expectError:      false,
			expectedProvider: keycloakProvider,
		},
		{
			name: "Generic OIDC provider specified",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:  true,
						Provider: constants.ProviderGenericOIDC,
					},
				},
			},
			expectError:      false,
			expectedProvider: genericProvider,
		},
		{
			name: "Default provider (empty string)",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:  true,
						Provider: "",
					},
				},
			},
			expectError:      false,
			expectedProvider: keycloakProvider,
		},
		{
			name: "Unsupported provider",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:  true,
						Provider: "unsupported-provider",
					},
				},
			},
			expectError:      true,
			expectedProvider: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := reconciler.getProvider(tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if !tt.expectError && provider != tt.expectedProvider {
				t.Errorf("expected provider %v, got %v", tt.expectedProvider, provider)
			}
		})
	}
}

func TestValidateAuthConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		nebariApp   *appsv1.NebariApp
		secret      *corev1.Secret
		expectError bool
	}{
		{
			name: "Valid secret exists",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: naming.ClientSecretName(&appsv1.NebariApp{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-app",
							Namespace: "default",
						},
					}),
					Namespace: "default",
				},
				Data: map[string][]byte{
					constants.ClientSecretKey: []byte("test-secret"),
				},
			},
			expectError: false,
		},
		{
			name: "Secret not found",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
			},
			secret:      nil,
			expectError: true,
		},
		{
			name: "Secret missing required key",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: naming.ClientSecretName(&appsv1.NebariApp{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-app",
							Namespace: "default",
						},
					}),
					Namespace: "default",
				},
				Data: map[string][]byte{
					"wrong-key": []byte("test-secret"),
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp)

			if tt.secret != nil {
				builder = builder.WithObjects(tt.secret)
			}

			client := builder.Build()

			reconciler := &AuthReconciler{
				Client:   client,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.validateAuthConfig(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestBuildSecurityPolicySpec(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = egv1alpha1.AddToScheme(scheme)

	tests := []struct {
		name         string
		nebariApp    *appsv1.NebariApp
		provider     *mockProvider
		expectError  bool
		validateSpec func(*testing.T, egv1alpha1.SecurityPolicySpec)
	}{
		{
			name: "Basic auth config",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:  true,
						Provider: constants.ProviderKeycloak,
					},
				},
			},
			provider: &mockProvider{
				issuerURL: "https://keycloak.example.com/realms/test",
				clientID:  "test-client",
			},
			expectError: false,
			validateSpec: func(t *testing.T, spec egv1alpha1.SecurityPolicySpec) {
				if spec.OIDC == nil {
					t.Error("OIDC config is nil")
					return
				}
				if spec.OIDC.Provider.Issuer != "https://keycloak.example.com/realms/test" {
					t.Errorf("expected issuer https://keycloak.example.com/realms/test, got %s", spec.OIDC.Provider.Issuer)
				}
				if *spec.OIDC.ClientID != "test-client" {
					t.Errorf("expected clientID test-client, got %s", *spec.OIDC.ClientID)
				}
				if *spec.OIDC.RedirectURL != "https://test.example.com/oauth2/callback" {
					t.Errorf("expected redirectURL https://test.example.com/oauth2/callback, got %s", *spec.OIDC.RedirectURL)
				}
				if len(spec.OIDC.Scopes) != 3 {
					t.Errorf("expected 3 default scopes, got %d", len(spec.OIDC.Scopes))
				}
			},
		},
		{
			name: "Custom redirect URI",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:     true,
						Provider:    constants.ProviderKeycloak,
						RedirectURI: "/custom/callback",
					},
				},
			},
			provider: &mockProvider{
				issuerURL: "https://keycloak.example.com/realms/test",
				clientID:  "test-client",
			},
			expectError: false,
			validateSpec: func(t *testing.T, spec egv1alpha1.SecurityPolicySpec) {
				if spec.OIDC == nil {
					t.Error("OIDC config is nil")
					return
				}
				if *spec.OIDC.RedirectURL != "https://test.example.com/custom/callback" {
					t.Errorf("expected redirectURL https://test.example.com/custom/callback, got %s", *spec.OIDC.RedirectURL)
				}
			},
		},
		{
			name: "Custom scopes",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:  true,
						Provider: constants.ProviderKeycloak,
						Scopes:   []string{"openid", "profile", "email", "groups"},
					},
				},
			},
			provider: &mockProvider{
				issuerURL: "https://keycloak.example.com/realms/test",
				clientID:  "test-client",
			},
			expectError: false,
			validateSpec: func(t *testing.T, spec egv1alpha1.SecurityPolicySpec) {
				if spec.OIDC == nil {
					t.Error("OIDC config is nil")
					return
				}
				if len(spec.OIDC.Scopes) != 4 {
					t.Errorf("expected 4 custom scopes, got %d", len(spec.OIDC.Scopes))
				}
			},
		},
		{
			name: "Provider returns error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:  true,
						Provider: constants.ProviderKeycloak,
					},
				},
			},
			provider: &mockProvider{
				issuerURL:   "",
				clientID:    "test-client",
				issuerError: errors.New("failed to get issuer URL"),
			},
			expectError: true,
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

			spec, err := reconciler.buildSecurityPolicySpec(context.Background(), tt.nebariApp, tt.provider)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			if !tt.expectError && tt.validateSpec != nil {
				tt.validateSpec(t, spec)
			}
		})
	}
}

func TestReconcileAuth(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = egv1alpha1.AddToScheme(scheme)

	tests := []struct {
		name           string
		nebariApp      *appsv1.NebariApp
		existingSecret *corev1.Secret
		provider       *mockProvider
		expectError    bool
	}{
		{
			name: "Auth not enabled",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: false,
					},
				},
			},
			expectError: false,
		},
		{
			name: "Auth enabled with valid config",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:         true,
						Provider:        constants.ProviderKeycloak,
						ProvisionClient: boolPtr(false),
					},
				},
			},
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app-oidc-client",
					Namespace: "default",
				},
				Data: map[string][]byte{
					constants.ClientSecretKey: []byte("test-secret"),
				},
			},
			provider: &mockProvider{
				issuerURL:            "https://keycloak.example.com/realms/test",
				clientID:             "test-client",
				supportsProvisioning: true,
			},
			expectError: false,
		},
		{
			name: "Auth enabled but secret missing",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:         true,
						Provider:        constants.ProviderKeycloak,
						ProvisionClient: boolPtr(false),
					},
				},
			},
			existingSecret: nil,
			provider: &mockProvider{
				issuerURL:            "https://keycloak.example.com/realms/test",
				clientID:             "test-client",
				supportsProvisioning: true,
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp)

			if tt.existingSecret != nil {
				builder = builder.WithObjects(tt.existingSecret)
			}

			client := builder.Build()

			providers := map[string]providers.OIDCProvider{}
			if tt.provider != nil {
				providers[constants.ProviderKeycloak] = tt.provider
			}

			reconciler := &AuthReconciler{
				Client:    client,
				Scheme:    scheme,
				Recorder:  record.NewFakeRecorder(10),
				Providers: providers,
			}

			err := reconciler.ReconcileAuth(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			// If auth is enabled and no error, verify SecurityPolicy was created
			if !tt.expectError && tt.nebariApp.Spec.Auth != nil && tt.nebariApp.Spec.Auth.Enabled {
				securityPolicy := &egv1alpha1.SecurityPolicy{}
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      naming.SecurityPolicyName(tt.nebariApp),
					Namespace: tt.nebariApp.Namespace,
				}, securityPolicy)

				if err != nil {
					t.Errorf("expected SecurityPolicy to be created, got error: %v", err)
				}
			}
		})
	}
}

func TestCleanupAuth(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name        string
		nebariApp   *appsv1.NebariApp
		provider    *mockProvider
		expectError bool
	}{
		{
			name: "Auth not enabled",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: false,
					},
				},
			},
			expectError: false,
		},
		{
			name: "Cleanup with provisioning enabled",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:         true,
						Provider:        constants.ProviderKeycloak,
						ProvisionClient: boolPtr(true),
					},
				},
			},
			provider: &mockProvider{
				supportsProvisioning: true,
				deleteError:          nil,
			},
			expectError: false,
		},
		{
			name: "Cleanup with delete error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:         true,
						Provider:        constants.ProviderKeycloak,
						ProvisionClient: boolPtr(true),
					},
				},
			},
			provider: &mockProvider{
				supportsProvisioning: true,
				deleteError:          errors.New("failed to delete client"),
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp).
				Build()

			providers := map[string]providers.OIDCProvider{}
			if tt.provider != nil {
				providers[constants.ProviderKeycloak] = tt.provider
			}

			reconciler := &AuthReconciler{
				Client:    client,
				Scheme:    scheme,
				Recorder:  record.NewFakeRecorder(10),
				Providers: providers,
			}

			err := reconciler.CleanupAuth(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}
