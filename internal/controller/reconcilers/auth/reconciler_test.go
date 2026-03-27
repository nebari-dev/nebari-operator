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
	provisionCount       int // tracks how many times ProvisionClient was called
}

func (m *mockProvider) GetIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error) {
	if m.issuerError != nil {
		return "", m.issuerError
	}
	return m.issuerURL, nil
}

func (m *mockProvider) GetExternalIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error) {
	if m.issuerError != nil {
		return "", m.issuerError
	}
	return m.issuerURL, nil
}

func (m *mockProvider) GetClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string {
	return m.clientID
}

func (m *mockProvider) ProvisionClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	m.provisionCount++
	return m.provisionError
}

func (m *mockProvider) DeleteClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	return m.deleteError
}

func (m *mockProvider) SupportsProvisioning() bool {
	return m.supportsProvisioning
}

func (m *mockProvider) ConfigureTokenExchange(ctx context.Context, nebariApp *appsv1.NebariApp, peerClientIDs []string) error {
	return nil
}

func (m *mockProvider) CleanupTokenExchange(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	return nil
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
				if spec.OIDC.ClientID != "test-client" {
					t.Errorf("expected clientID test-client, got %s", spec.OIDC.ClientID)
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
		name                   string
		nebariApp              *appsv1.NebariApp
		existingSecret         *corev1.Secret
		existingSecurityPolicy *egv1alpha1.SecurityPolicy
		provider               *mockProvider
		expectError            bool
		// validate runs additional assertions after reconciliation (optional)
		validate func(*testing.T, *mockProvider, *appsv1.NebariApp)
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
			name: "Auth disabled with pre-existing SecurityPolicy - deletes it",
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
			existingSecurityPolicy: &egv1alpha1.SecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.SecurityPolicyName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"}}),
					Namespace: "default",
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
		{
			name: "EnforceAtGateway false - SecurityPolicy not created",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:          true,
						Provider:         constants.ProviderKeycloak,
						ProvisionClient:  boolPtr(false),
						EnforceAtGateway: boolPtr(false),
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
			name: "EnforceAtGateway false with pre-existing SecurityPolicy - deletes it",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled:          true,
						Provider:         constants.ProviderKeycloak,
						ProvisionClient:  boolPtr(false),
						EnforceAtGateway: boolPtr(false),
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
			existingSecurityPolicy: &egv1alpha1.SecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app-security-policy",
					Namespace: "default",
				},
			},
			provider: &mockProvider{
				issuerURL:            "https://keycloak.example.com/realms/test",
				clientID:             "test-client",
				supportsProvisioning: true,
			},
			expectError: false,
		},
		// --- Hash-guard tests ---
		{
			name: "Skip provisioning when hash matches and AuthReady=True",
			nebariApp: func() *appsv1.NebariApp {
				app := &appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-app",
						Namespace: "default",
					},
					Spec: appsv1.NebariAppSpec{
						Hostname: "test.example.com",
						Auth: &appsv1.AuthConfig{
							Enabled:         true,
							Provider:        constants.ProviderKeycloak,
							ProvisionClient: boolPtr(true),
						},
					},
				}
				// Pre-set a matching hash and AuthReady=True to trigger the skip path
				app.Status.AuthConfigHash = computeAuthConfigHash(app)
				app.Status.Conditions = []metav1.Condition{{
					Type:               appsv1.ConditionTypeAuthReady,
					Status:             metav1.ConditionTrue,
					Reason:             "AuthConfigured",
					LastTransitionTime: metav1.Now(),
				}}
				return app
			}(),
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
			validate: func(t *testing.T, p *mockProvider, app *appsv1.NebariApp) {
				if p.provisionCount != 0 {
					t.Errorf("expected ProvisionClient to be skipped, called %d time(s)", p.provisionCount)
				}
			},
		},
		{
			name: "Re-provision when spec changes (hash mismatch)",
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
						ProvisionClient: boolPtr(true),
					},
				},
				Status: appsv1.NebariAppStatus{
					AuthConfigHash: "stale-hash-will-not-match",
					Conditions: []metav1.Condition{{
						Type:               appsv1.ConditionTypeAuthReady,
						Status:             metav1.ConditionTrue,
						Reason:             "AuthConfigured",
						LastTransitionTime: metav1.Now(),
					}},
				},
			},
			// No secret pre-created; validateAuthConfig will fail after provisioning
			provider: &mockProvider{
				issuerURL:            "https://keycloak.example.com/realms/test",
				clientID:             "test-client",
				supportsProvisioning: true,
			},
			expectError: true, // validateAuthConfig fails (mock doesn't create the secret)
			validate: func(t *testing.T, p *mockProvider, app *appsv1.NebariApp) {
				if p.provisionCount != 1 {
					t.Errorf("expected ProvisionClient called once, got %d", p.provisionCount)
				}
				if app.Status.AuthConfigHash == "stale-hash-will-not-match" {
					t.Error("expected AuthConfigHash to be updated after provisioning")
				}
			},
		},
		{
			name: "Provision on first reconcile (no stored hash, AuthReady unset)",
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
						ProvisionClient: boolPtr(true),
					},
				},
			},
			// No secret; validateAuthConfig will fail but provisioning must run first
			provider: &mockProvider{
				issuerURL:            "https://keycloak.example.com/realms/test",
				clientID:             "test-client",
				supportsProvisioning: true,
			},
			expectError: true,
			validate: func(t *testing.T, p *mockProvider, app *appsv1.NebariApp) {
				if p.provisionCount != 1 {
					t.Errorf("expected ProvisionClient called once on first reconcile, got %d", p.provisionCount)
				}
			},
		},
		{
			name: "Force re-provision via annotation overrides matching hash",
			nebariApp: func() *appsv1.NebariApp {
				app := &appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-app",
						Namespace: "default",
						Annotations: map[string]string{
							constants.AnnotationForceReprovision: "true",
						},
					},
					Spec: appsv1.NebariAppSpec{
						Hostname: "test.example.com",
						Auth: &appsv1.AuthConfig{
							Enabled:         true,
							Provider:        constants.ProviderKeycloak,
							ProvisionClient: boolPtr(true),
						},
					},
				}
				// Hash matches and AuthReady=True, but annotation forces re-provision
				app.Status.AuthConfigHash = computeAuthConfigHash(app)
				app.Status.Conditions = []metav1.Condition{{
					Type:               appsv1.ConditionTypeAuthReady,
					Status:             metav1.ConditionTrue,
					Reason:             "AuthConfigured",
					LastTransitionTime: metav1.Now(),
				}}
				return app
			}(),
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
			validate: func(t *testing.T, p *mockProvider, app *appsv1.NebariApp) {
				if p.provisionCount != 1 {
					t.Errorf("expected forced ProvisionClient called once, got %d", p.provisionCount)
				}
				if _, ok := app.Annotations[constants.AnnotationForceReprovision]; ok {
					t.Error("expected force-reprovision annotation to be cleared after reconcile")
				}
			},
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
			if tt.existingSecurityPolicy != nil {
				builder = builder.WithObjects(tt.existingSecurityPolicy)
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

			// If auth is disabled and there was a pre-existing SecurityPolicy, verify it was deleted
			if !tt.expectError && tt.nebariApp.Spec.Auth != nil && !tt.nebariApp.Spec.Auth.Enabled && tt.existingSecurityPolicy != nil {
				securityPolicy := &egv1alpha1.SecurityPolicy{}
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      naming.SecurityPolicyName(tt.nebariApp),
					Namespace: tt.nebariApp.Namespace,
				}, securityPolicy)
				if err == nil {
					t.Error("expected SecurityPolicy to be deleted when auth is disabled, but it still exists")
				}
			}

			// If auth is enabled and no error, verify SecurityPolicy based on enforceAtGateway setting
			if !tt.expectError && tt.nebariApp.Spec.Auth != nil && tt.nebariApp.Spec.Auth.Enabled {
				securityPolicy := &egv1alpha1.SecurityPolicy{}
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      naming.SecurityPolicyName(tt.nebariApp),
					Namespace: tt.nebariApp.Namespace,
				}, securityPolicy)

				if shouldEnforceAtGateway(tt.nebariApp.Spec.Auth) {
					if err != nil {
						t.Errorf("expected SecurityPolicy to be created, got error: %v", err)
					}
				} else {
					if err == nil {
						t.Error("expected SecurityPolicy to NOT be created when enforceAtGateway=false")
					}
				}
			}

			// Run additional per-test assertions if provided
			if tt.validate != nil {
				tt.validate(t, tt.provider, tt.nebariApp)
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

// TestComputeAuthConfigHash verifies the hash function's sensitivity and stability.
// Each relevant spec field must produce a distinct hash when changed, and the hash
// must be stable (same input → same output regardless of slice ordering).
func TestComputeAuthConfigHash(t *testing.T) {
	base := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: appsv1.NebariAppSpec{
			Hostname: "app.example.com",
			Auth: &appsv1.AuthConfig{
				Provider:    constants.ProviderKeycloak,
				RedirectURI: "/oauth2/callback",
				Scopes:      []string{"openid", "profile"},
				Groups:      []string{"admins"},
			},
		},
	}

	baseHash := computeAuthConfigHash(base)

	t.Run("same spec produces same hash", func(t *testing.T) {
		same := base.DeepCopy()
		if computeAuthConfigHash(same) != baseHash {
			t.Error("expected identical spec to produce the same hash")
		}
	})

	t.Run("scope order does not change hash", func(t *testing.T) {
		reordered := base.DeepCopy()
		reordered.Spec.Auth.Scopes = []string{"profile", "openid"} // reversed
		if computeAuthConfigHash(reordered) != baseHash {
			t.Error("expected scope reordering to produce the same hash")
		}
	})

	t.Run("group order does not change hash", func(t *testing.T) {
		app := base.DeepCopy()
		app.Spec.Auth.Groups = []string{"viewers", "admins"}
		base2 := base.DeepCopy()
		base2.Spec.Auth.Groups = []string{"admins", "viewers"}
		if computeAuthConfigHash(app) != computeAuthConfigHash(base2) {
			t.Error("expected group reordering to produce the same hash")
		}
	})

	t.Run("different redirect URI changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Spec.Auth.RedirectURI = "/auth/callback"
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected different redirectURI to produce a different hash")
		}
	})

	t.Run("adding a group changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Spec.Auth.Groups = append(changed.Spec.Auth.Groups, "data-scientists")
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected added group to produce a different hash")
		}
	})

	t.Run("adding a scope changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Spec.Auth.Scopes = append(changed.Spec.Auth.Scopes, "groups")
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected added scope to produce a different hash")
		}
	})

	t.Run("different hostname changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Spec.Hostname = "other.example.com"
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected different hostname to produce a different hash")
		}
	})

	t.Run("different namespace changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Namespace = "production"
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected different namespace to produce a different hash")
		}
	})

	t.Run("different provider changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Spec.Auth.Provider = constants.ProviderGenericOIDC
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected different provider to produce a different hash")
		}
	})

	t.Run("adding keycloakConfig group changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Spec.Auth.KeycloakConfig = &appsv1.KeycloakClientConfig{
			Groups: []appsv1.KeycloakGroup{{Name: "admins", Members: []string{"alice"}}},
		}
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected keycloakConfig change to produce a different hash")
		}
	})

	t.Run("issuerURL change (generic-oidc) changes hash", func(t *testing.T) {
		changed := base.DeepCopy()
		changed.Spec.Auth.IssuerURL = "https://accounts.google.com"
		if computeAuthConfigHash(changed) == baseHash {
			t.Error("expected different issuerURL to produce a different hash")
		}
	})
}

// TestReconcileAuth_SpecChangeCycle simulates a realistic reconcile lifecycle:
//  1. First reconcile: no stored hash, provisions and records the hash.
//  2. Second reconcile: spec unchanged, provisioning is skipped.
//  3. Spec mutation (redirect URI / group / scope): hash mismatches, provisions again.
//
// This validates the end-to-end guard behaviour for the changes described in #29.
func TestReconcileAuth_SpecChangeCycle(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = egv1alpha1.AddToScheme(scheme)

	// Helper: build a reconciler with a pre-populated fake client secret so
	// validateAuthConfig passes after the mock ProvisionClient returns nil.
	newReconcilerWithSecret := func(app *appsv1.NebariApp) (*AuthReconciler, *mockProvider) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      naming.ClientSecretName(app),
				Namespace: app.Namespace,
			},
			Data: map[string][]byte{constants.ClientSecretKey: []byte("s3cr3t")},
		}
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(app, secret).
			WithStatusSubresource(app).
			Build()
		provider := &mockProvider{
			issuerURL:            "https://keycloak.example.com/realms/test",
			clientID:             "test-app",
			supportsProvisioning: true,
		}
		return &AuthReconciler{
			Client:    fakeClient,
			Scheme:    scheme,
			Recorder:  record.NewFakeRecorder(32),
			Providers: map[string]providers.OIDCProvider{constants.ProviderKeycloak: provider},
		}, provider
	}

	tests := []struct {
		name    string
		initial appsv1.AuthConfig
		mutate  func(*appsv1.AuthConfig)
	}{
		{
			name: "redirect URI change triggers re-provisioning",
			initial: appsv1.AuthConfig{
				Enabled:         true,
				Provider:        constants.ProviderKeycloak,
				ProvisionClient: boolPtr(true),
				RedirectURI:     "/oauth2/callback",
			},
			mutate: func(a *appsv1.AuthConfig) { a.RedirectURI = "/auth/callback" },
		},
		{
			name: "adding a group triggers re-provisioning",
			initial: appsv1.AuthConfig{
				Enabled:         true,
				Provider:        constants.ProviderKeycloak,
				ProvisionClient: boolPtr(true),
				Groups:          []string{"admins"},
			},
			mutate: func(a *appsv1.AuthConfig) {
				a.Groups = append(a.Groups, "data-scientists")
			},
		},
		{
			name: "adding a scope triggers re-provisioning",
			initial: appsv1.AuthConfig{
				Enabled:         true,
				Provider:        constants.ProviderKeycloak,
				ProvisionClient: boolPtr(true),
				Scopes:          []string{"openid", "profile"},
			},
			mutate: func(a *appsv1.AuthConfig) {
				a.Scopes = append(a.Scopes, "groups")
			},
		},
		{
			name: "adding a keycloakConfig group member triggers re-provisioning",
			initial: appsv1.AuthConfig{
				Enabled:         true,
				Provider:        constants.ProviderKeycloak,
				ProvisionClient: boolPtr(true),
				KeycloakConfig: &appsv1.KeycloakClientConfig{
					Groups: []appsv1.KeycloakGroup{{Name: "admins", Members: []string{"alice"}}},
				},
			},
			mutate: func(a *appsv1.AuthConfig) {
				a.KeycloakConfig.Groups[0].Members = append(a.KeycloakConfig.Groups[0].Members, "bob")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth:     tt.initial.DeepCopy(),
				},
			}
			reconciler, provider := newReconcilerWithSecret(app)

			// --- Reconcile 1: first time, no stored hash ---
			if err := reconciler.ReconcileAuth(context.Background(), app); err != nil {
				t.Fatalf("reconcile 1 failed: %v", err)
			}
			if provider.provisionCount != 1 {
				t.Fatalf("reconcile 1: expected ProvisionClient called once, got %d", provider.provisionCount)
			}
			hashAfterFirst := app.Status.AuthConfigHash
			if hashAfterFirst == "" {
				t.Fatal("reconcile 1: expected AuthConfigHash to be set after provisioning")
			}

			// --- Reconcile 2: same spec, AuthReady=True → must skip ---
			if err := reconciler.ReconcileAuth(context.Background(), app); err != nil {
				t.Fatalf("reconcile 2 failed: %v", err)
			}
			if provider.provisionCount != 1 {
				t.Errorf("reconcile 2: expected provisioning to be skipped (count=1), got %d", provider.provisionCount)
			}

			// --- Spec mutation ---
			tt.mutate(app.Spec.Auth)

			// --- Reconcile 3: spec changed, hash mismatch → must re-provision ---
			if err := reconciler.ReconcileAuth(context.Background(), app); err != nil {
				t.Fatalf("reconcile 3 failed: %v", err)
			}
			if provider.provisionCount != 2 {
				t.Errorf("reconcile 3: expected ProvisionClient called again (count=2), got %d", provider.provisionCount)
			}
			if app.Status.AuthConfigHash == hashAfterFirst {
				t.Error("reconcile 3: expected AuthConfigHash to be updated after spec change")
			}

			// --- Reconcile 4: spec stable again → skip ---
			if err := reconciler.ReconcileAuth(context.Background(), app); err != nil {
				t.Fatalf("reconcile 4 failed: %v", err)
			}
			if provider.provisionCount != 2 {
				t.Errorf("reconcile 4: expected provisioning to be skipped again (count=2), got %d", provider.provisionCount)
			}
		})
	}
}
