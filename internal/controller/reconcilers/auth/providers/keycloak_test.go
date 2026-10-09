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

package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Nerzal/gocloak/v13"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/config"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/ptr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKeycloakProvider_GetIssuerURL(t *testing.T) {
	tests := []struct {
		name        string
		kcConfig    config.KeycloakConfig
		expectedURL string
	}{
		{
			name: "Default configuration (Keycloak 26+ root context path)",
			kcConfig: config.KeycloakConfig{
				URL:                    "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080",
				Realm:                  "nebari",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "",
			},
			expectedURL: "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/realms/nebari",
		},
		{
			name: "Legacy /auth context path",
			kcConfig: config.KeycloakConfig{
				URL:                    "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/auth",
				Realm:                  "nebari",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "/auth",
			},
			expectedURL: "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/auth/realms/nebari",
		},
		{
			name: "Custom realm with /auth context path",
			kcConfig: config.KeycloakConfig{
				URL:                    "https://keycloak.example.com",
				Realm:                  "custom-realm",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "/auth",
			},
			// Issuer URL is built from config components, not from config.URL
			expectedURL: "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/auth/realms/custom-realm",
		},
		{
			name: "Custom deployment configuration",
			kcConfig: config.KeycloakConfig{
				URL:                    "http://custom-keycloak.auth.svc.cluster.local:9090",
				Realm:                  "custom-realm",
				IssuerServiceName:      "custom-keycloak",
				IssuerServiceNamespace: "auth",
				IssuerServicePort:      9090,
				IssuerContextPath:      "",
			},
			expectedURL: "http://custom-keycloak.auth.svc.cluster.local:9090/realms/custom-realm",
		},
		{
			// Issue #112: the issuer deliberately stays in-cluster even when
			// KEYCLOAK_EXTERNAL_URL is set. The external URL only affects
			// browser-facing endpoint overrides and the client Secret's
			// issuer-url key, never SecurityPolicy.spec.oidc.provider.issuer.
			name: "ExternalURL set: issuer remains in-cluster",
			kcConfig: config.KeycloakConfig{
				URL:                    "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080",
				Realm:                  "nebari",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "",
				ExternalURL:            "https://keycloak.example.com",
			},
			expectedURL: "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/realms/nebari",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &KeycloakProvider{
				Config: tt.kcConfig,
			}

			nebariApp := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
			}

			url, err := provider.GetIssuerURL(context.Background(), nebariApp)
			if err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			if url != tt.expectedURL {
				t.Errorf("expected URL %s, got %s", tt.expectedURL, url)
			}
		})
	}
}

func TestKeycloakProvider_GetEndpointOverrides(t *testing.T) {
	tests := []struct {
		name     string
		kcConfig config.KeycloakConfig
		expected OIDCEndpointOverrides
	}{
		{
			name: "Default configuration with ExternalURL (Keycloak 26+ root context path)",
			kcConfig: config.KeycloakConfig{
				Realm:                  "nebari",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "",
				ExternalURL:            "https://keycloak.example.com",
			},
			expected: OIDCEndpointOverrides{
				// Token endpoint: server-to-server, Envoy proxy hits this in
				// the back-channel. Stays on the in-cluster URL for latency.
				Token: ptr.To("http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/realms/nebari/protocol/openid-connect/token"),
				// Authorization + EndSession: browser front-channel. Must be
				// the publicly routable URL, otherwise the browser cannot
				// reach Keycloak.
				Authorization: ptr.To("https://keycloak.example.com/realms/nebari/protocol/openid-connect/auth"),
				EndSession:    ptr.To("https://keycloak.example.com/realms/nebari/protocol/openid-connect/logout"),
			},
		},
		{
			name: "Legacy /auth context path with ExternalURL",
			kcConfig: config.KeycloakConfig{
				Realm:                  "nebari",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "/auth",
				ExternalURL:            "https://keycloak.example.com/auth",
			},
			expected: OIDCEndpointOverrides{
				Token:         ptr.To("http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/auth/realms/nebari/protocol/openid-connect/token"),
				Authorization: ptr.To("https://keycloak.example.com/auth/realms/nebari/protocol/openid-connect/auth"),
				EndSession:    ptr.To("https://keycloak.example.com/auth/realms/nebari/protocol/openid-connect/logout"),
			},
		},
		{
			name: "Custom deployment configuration with ExternalURL",
			kcConfig: config.KeycloakConfig{
				Realm:                  "custom-realm",
				IssuerServiceName:      "custom-keycloak",
				IssuerServiceNamespace: "auth",
				IssuerServicePort:      9090,
				IssuerContextPath:      "",
				ExternalURL:            "https://auth.custom.example.com",
			},
			expected: OIDCEndpointOverrides{
				Token:         ptr.To("http://custom-keycloak.auth.svc.cluster.local:9090/realms/custom-realm/protocol/openid-connect/token"),
				Authorization: ptr.To("https://auth.custom.example.com/realms/custom-realm/protocol/openid-connect/auth"),
				EndSession:    ptr.To("https://auth.custom.example.com/realms/custom-realm/protocol/openid-connect/logout"),
			},
		},
		{
			name: "ExternalURL with trailing slash is normalized",
			kcConfig: config.KeycloakConfig{
				Realm:                  "nebari",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "",
				ExternalURL:            "https://keycloak.example.com/",
			},
			expected: OIDCEndpointOverrides{
				Token:         ptr.To("http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/realms/nebari/protocol/openid-connect/token"),
				Authorization: ptr.To("https://keycloak.example.com/realms/nebari/protocol/openid-connect/auth"),
				EndSession:    ptr.To("https://keycloak.example.com/realms/nebari/protocol/openid-connect/logout"),
			},
		},
		{
			name: "No ExternalURL: only Token override is set; Envoy falls back to discovery for Authorization and EndSession",
			kcConfig: config.KeycloakConfig{
				Realm:                  "nebari",
				IssuerServiceName:      "keycloak-keycloakx-http",
				IssuerServiceNamespace: "keycloak",
				IssuerServicePort:      8080,
				IssuerContextPath:      "",
				ExternalURL:            "",
			},
			expected: OIDCEndpointOverrides{
				Token:         ptr.To("http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/realms/nebari/protocol/openid-connect/token"),
				Authorization: nil,
				EndSession:    nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &KeycloakProvider{Config: tt.kcConfig}
			got, err := provider.GetEndpointOverrides(context.Background(), nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Token == nil || *got.Token != *tt.expected.Token {
				t.Errorf("token: expected %q, got %v", *tt.expected.Token, got.Token)
			}
			switch {
			case tt.expected.Authorization == nil && got.Authorization != nil:
				t.Errorf("authorization: expected nil, got %q", *got.Authorization)
			case tt.expected.Authorization != nil && got.Authorization == nil:
				t.Errorf("authorization: expected %q, got nil", *tt.expected.Authorization)
			case tt.expected.Authorization != nil && got.Authorization != nil && *got.Authorization != *tt.expected.Authorization:
				t.Errorf("authorization: expected %q, got %q", *tt.expected.Authorization, *got.Authorization)
			}
			switch {
			case tt.expected.EndSession == nil && got.EndSession != nil:
				t.Errorf("endSession: expected nil, got %q", *got.EndSession)
			case tt.expected.EndSession != nil && got.EndSession == nil:
				t.Errorf("endSession: expected %q, got nil", *tt.expected.EndSession)
			case tt.expected.EndSession != nil && got.EndSession != nil && *got.EndSession != *tt.expected.EndSession:
				t.Errorf("endSession: expected %q, got %q", *tt.expected.EndSession, *got.EndSession)
			}
		})
	}
}

func TestKeycloakProvider_GetClientID(t *testing.T) {
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{
			URL:   "http://keycloak.keycloak.svc.cluster.local:8080",
			Realm: "nebari",
		},
	}

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
	}

	clientID := provider.GetClientID(context.Background(), nebariApp)
	expectedClientID := naming.ClientID(nebariApp)

	if clientID != expectedClientID {
		t.Errorf("expected client ID %s, got %s", expectedClientID, clientID)
	}
}

func TestKeycloakProvider_SupportsProvisioning(t *testing.T) {
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{},
	}

	if !provider.SupportsProvisioning() {
		t.Error("expected KeycloakProvider to support provisioning")
	}
}

func TestKeycloakProvider_BuildRedirectURLs(t *testing.T) {
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{},
	}

	tests := []struct {
		name         string
		nebariApp    *appsv1.NebariApp
		expectedURLs []string
	}{
		{
			name: "Default redirect URI",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
			// buildRedirectURLs returns both HTTP and HTTPS for dev/prod support
			expectedURLs: []string{
				"https://test.example.com/oauth2/callback",
				"http://test.example.com/oauth2/callback",
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
						RedirectURI: "/custom/callback",
					},
				},
			},
			expectedURLs: []string{
				"https://test.example.com/custom/callback",
				"http://test.example.com/custom/callback",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls := provider.buildRedirectURLs(tt.nebariApp)

			if len(urls) != len(tt.expectedURLs) {
				t.Errorf("expected %d URLs, got %d", len(tt.expectedURLs), len(urls))
				return
			}

			for i, url := range urls {
				if url != tt.expectedURLs[i] {
					t.Errorf("expected URL[%d] %s, got %s", i, tt.expectedURLs[i], url)
				}
			}
		})
	}
}

func TestKeycloakProvider_StoreClientSecret(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name              string
		nebariApp         *appsv1.NebariApp
		clientID          string
		clientSecret      string
		externalIssuer    string
		spaClientID       string
		deviceClientID    string
		existingSecret    *corev1.Secret
		expectError       bool
		expectedSecretLen int // expected number of keys in the secret
	}{
		{
			name: "Create new secret with basic fields",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
					UID:       "test-uid",
				},
			},
			clientID:          "default-test-app",
			clientSecret:      "test-secret-value",
			externalIssuer:    "https://keycloak.example.com/realms/nebari",
			existingSecret:    nil,
			expectError:       false,
			expectedSecretLen: 3, // client-id, client-secret, issuer-url
		},
		{
			name: "Update existing secret",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
					UID:       "test-uid",
				},
			},
			clientID:       "default-test-app",
			clientSecret:   "new-secret-value",
			externalIssuer: "https://keycloak.example.com/realms/nebari",
			existingSecret: &corev1.Secret{
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
					"client-secret": []byte("old-secret-value"),
				},
			},
			expectError:       false,
			expectedSecretLen: 3,
		},
		{
			name: "Create secret with all fields including SPA and device client",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
					UID:       "test-uid",
				},
			},
			clientID:          "default-test-app",
			clientSecret:      "test-secret-value",
			externalIssuer:    "https://keycloak.example.com/realms/nebari",
			spaClientID:       "default-test-app-spa",
			deviceClientID:    "default-test-app-device",
			existingSecret:    nil,
			expectError:       false,
			expectedSecretLen: 5, // client-id, client-secret, issuer-url, spa-client-id, device-client-id
		},
		{
			name: "Create secret with empty issuer URL stores empty value",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
					UID:       "test-uid",
				},
			},
			clientID:          "default-test-app",
			clientSecret:      "test-secret-value",
			externalIssuer:    "",
			existingSecret:    nil,
			expectError:       false,
			expectedSecretLen: 3, // client-id, client-secret, issuer-url (even when empty)
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

			k8sClient := builder.Build()

			provider := &KeycloakProvider{
				Config: config.KeycloakConfig{},
				Client: k8sClient,
			}

			err := provider.storeClientSecret(context.Background(), tt.nebariApp, tt.clientID, tt.clientSecret, tt.externalIssuer, tt.spaClientID, tt.deviceClientID)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			if !tt.expectError {
				// Verify the secret was created/updated with correct keys
				secret := &corev1.Secret{}
				secretName := naming.ClientSecretName(tt.nebariApp)
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Name:      secretName,
					Namespace: tt.nebariApp.Namespace,
				}, secret)
				if err != nil {
					t.Fatalf("failed to get secret: %v", err)
				}

				if len(secret.Data) != tt.expectedSecretLen {
					t.Errorf("expected %d keys in secret, got %d", tt.expectedSecretLen, len(secret.Data))
				}

				// Verify required keys
				if string(secret.Data[constants.ClientIDKey]) != tt.clientID {
					t.Errorf("expected client-id %q, got %q", tt.clientID, string(secret.Data[constants.ClientIDKey]))
				}
				if string(secret.Data[constants.ClientSecretKey]) != tt.clientSecret {
					t.Errorf("expected client-secret %q, got %q", tt.clientSecret, string(secret.Data[constants.ClientSecretKey]))
				}
				if string(secret.Data[constants.IssuerURLKey]) != tt.externalIssuer {
					t.Errorf("expected issuer-url %q, got %q", tt.externalIssuer, string(secret.Data[constants.IssuerURLKey]))
				}

				// Verify optional keys
				if tt.spaClientID != "" {
					if string(secret.Data[constants.SPAClientIDKey]) != tt.spaClientID {
						t.Errorf("expected spa-client-id %q, got %q", tt.spaClientID, string(secret.Data[constants.SPAClientIDKey]))
					}
				}
				if tt.deviceClientID != "" {
					if string(secret.Data[constants.DeviceClientIDKey]) != tt.deviceClientID {
						t.Errorf("expected device-client-id %q, got %q", tt.deviceClientID, string(secret.Data[constants.DeviceClientIDKey]))
					}
				}
			}
		})
	}
}

func TestKeycloakProvider_LoadCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name             string
		kcConfig         config.KeycloakConfig
		secret           *corev1.Secret
		expectError      bool
		expectedUsername string
		expectedPassword string
	}{
		{
			name: "Loads credentials from secret with standard keys",
			kcConfig: config.KeycloakConfig{
				AdminSecretName:      "kc-admin",
				AdminSecretNamespace: "keycloak",
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kc-admin",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("secret123"),
				},
			},
			expectError:      false,
			expectedUsername: "admin",
			expectedPassword: "secret123",
		},
		{
			name: "Loads credentials from secret with admin- prefixed keys",
			kcConfig: config.KeycloakConfig{
				AdminSecretName:      "kc-admin",
				AdminSecretNamespace: "keycloak",
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kc-admin",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"admin-username": []byte("admin2"),
					"admin-password": []byte("secret456"),
				},
			},
			expectError:      false,
			expectedUsername: "admin2",
			expectedPassword: "secret456",
		},
		{
			name: "Uses direct credentials when no secret configured",
			kcConfig: config.KeycloakConfig{
				AdminSecretName: "",
				AdminUsername:   "direct-admin",
				AdminPassword:   "direct-pass",
			},
			secret:           nil,
			expectError:      false,
			expectedUsername: "direct-admin",
			expectedPassword: "direct-pass",
		},
		{
			name: "Error when no secret and no direct credentials",
			kcConfig: config.KeycloakConfig{
				AdminSecretName: "",
				AdminUsername:   "",
				AdminPassword:   "",
			},
			secret:      nil,
			expectError: true,
		},
		{
			name: "Error when secret not found",
			kcConfig: config.KeycloakConfig{
				AdminSecretName:      "nonexistent",
				AdminSecretNamespace: "keycloak",
			},
			secret:      nil,
			expectError: true,
		},
		{
			name: "Error when secret missing username",
			kcConfig: config.KeycloakConfig{
				AdminSecretName:      "kc-admin",
				AdminSecretNamespace: "keycloak",
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kc-admin",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"password": []byte("secret123"),
				},
			},
			expectError: true,
		},
		{
			name: "Error when secret missing password",
			kcConfig: config.KeycloakConfig{
				AdminSecretName:      "kc-admin",
				AdminSecretNamespace: "keycloak",
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kc-admin",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"username": []byte("admin"),
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.secret != nil {
				builder = builder.WithObjects(tt.secret)
			}
			k8sClient := builder.Build()

			provider := &KeycloakProvider{
				Config: tt.kcConfig,
				Client: k8sClient,
			}

			err := provider.loadCredentials(context.Background())

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if !tt.expectError {
				if provider.Config.AdminUsername != tt.expectedUsername {
					t.Errorf("expected username %q, got %q", tt.expectedUsername, provider.Config.AdminUsername)
				}
				if provider.Config.AdminPassword != tt.expectedPassword {
					t.Errorf("expected password %q, got %q", tt.expectedPassword, provider.Config.AdminPassword)
				}
			}
		})
	}
}

func TestKeycloakProvider_LoadCredentials_RefreshesOnSecretChange(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kc-admin",
			Namespace: "keycloak",
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("old-password"),
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(secret).
		Build()

	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{
			AdminSecretName:      "kc-admin",
			AdminSecretNamespace: "keycloak",
		},
		Client: k8sClient,
	}

	// First load
	if err := provider.loadCredentials(context.Background()); err != nil {
		t.Fatalf("first loadCredentials failed: %v", err)
	}
	if provider.Config.AdminPassword != "old-password" {
		t.Fatalf("expected old-password, got %s", provider.Config.AdminPassword)
	}

	// Simulate secret rotation by updating the secret in the fake client
	secret.Data["password"] = []byte("new-password")
	if err := k8sClient.Update(context.Background(), secret); err != nil {
		t.Fatalf("failed to update secret: %v", err)
	}

	// Second load should pick up the new password
	if err := provider.loadCredentials(context.Background()); err != nil {
		t.Fatalf("second loadCredentials failed: %v", err)
	}
	if provider.Config.AdminPassword != "new-password" {
		t.Errorf("expected credentials to be refreshed to 'new-password', got %q", provider.Config.AdminPassword)
	}
}

func TestKeycloakProvider_SyncClientScopes_NoScopes(t *testing.T) {
	// syncClientScopes should return nil immediately when no scopes are configured
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{
			Realm: "test",
		},
	}

	tests := []struct {
		name      string
		nebariApp *appsv1.NebariApp
	}{
		{
			name: "Nil auth config",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
				},
			},
		},
		{
			name: "Empty scopes",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						Scopes:  []string{},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// syncClientScopes should return nil without making any Keycloak calls
			// (passing nil kcClient and token proves no API calls are made)
			err := provider.syncClientScopes(context.Background(), nil, nil, "fake-id", tt.nebariApp)
			if err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestKeycloakProvider_SyncGroups_NoGroups(t *testing.T) {
	// syncGroups should return nil immediately when no groups are configured
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{
			Realm: "test",
		},
	}

	tests := []struct {
		name      string
		nebariApp *appsv1.NebariApp
	}{
		{
			name: "Nil auth config",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
				},
			},
		},
		{
			name: "Auth enabled but no groups",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
		},
		{
			name: "Empty groups and nil keycloakConfig",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						Groups:  []string{},
					},
				},
			},
		},
		{
			name: "Empty keycloakConfig groups",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						KeycloakConfig: &appsv1.KeycloakClientConfig{
							Groups: []appsv1.KeycloakGroup{},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// syncGroups should return nil without making any Keycloak calls
			// (passing nil kcClient and token proves no API calls are made)
			err := provider.syncGroups(context.Background(), nil, nil, tt.nebariApp)
			if err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestMergeGroupMembers(t *testing.T) {
	tests := []struct {
		name           string
		groups         []string
		keycloakConfig *appsv1.KeycloakClientConfig
		want           map[string]GroupSpec
	}{
		{
			name:   "keycloakConfig members take precedence",
			groups: []string{"admin", "viewer"},
			keycloakConfig: &appsv1.KeycloakClientConfig{Groups: []appsv1.KeycloakGroup{
				{Name: "admin", Members: []string{"admin-user"}},
				{Name: "editor"},
			}},
			want: map[string]GroupSpec{
				"/admin":  {Members: []string{"admin-user"}, Create: true},
				"/viewer": {Create: true},
				"/editor": {Create: true},
			},
		},
		{
			name:   "bare name and path are the same group",
			groups: []string{"team-example"},
			keycloakConfig: &appsv1.KeycloakClientConfig{Groups: []appsv1.KeycloakGroup{
				{Name: "/team-example", Members: []string{"alice"}},
			}},
			want: map[string]GroupSpec{
				"/team-example": {Members: []string{"alice"}, Create: true},
			},
		},
		{
			name:   "keycloakConfig wins over spec.auth.groups listed as a path",
			groups: []string{"/team-example"},
			keycloakConfig: &appsv1.KeycloakClientConfig{Groups: []appsv1.KeycloakGroup{
				{Name: "team-example", Members: []string{"alice"}},
			}},
			want: map[string]GroupSpec{
				"/team-example": {Members: []string{"alice"}, Create: true},
			},
		},
		{
			name:   "paths are never created",
			groups: []string{"/team-example", "/parent/child", "parent/other"},
			want: map[string]GroupSpec{
				"/team-example": {},
				"/parent/child": {},
				"/parent/other": {},
			},
		},
		{
			name:   "trailing slash, whitespace and empty entries",
			groups: []string{" finance ", "/ops/", "", "/"},
			want: map[string]GroupSpec{
				"/finance": {Create: true},
				"/ops":     {},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeGroupMembers(tt.groups, tt.keycloakConfig)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeGroupMembers() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestHasScope(t *testing.T) {
	tests := []struct {
		name     string
		app      *appsv1.NebariApp
		scope    string
		expected bool
	}{
		{
			name:     "Nil auth config",
			app:      &appsv1.NebariApp{Spec: appsv1.NebariAppSpec{}},
			scope:    "groups",
			expected: false,
		},
		{
			name: "Scope not present",
			app: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						Scopes:  []string{"openid", "profile", "email"},
					},
				},
			},
			scope:    "groups",
			expected: false,
		},
		{
			name: "Scope present",
			app: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						Scopes:  []string{"openid", "profile", "email", "groups"},
					},
				},
			},
			scope:    "groups",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasScope(tt.app, tt.scope)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestKeycloakProvider_SyncClientProtocolMappers_NoMappers(t *testing.T) {
	// syncClientProtocolMappers should return nil when no mappers are needed
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{Realm: "test"},
	}

	tests := []struct {
		name      string
		nebariApp *appsv1.NebariApp
	}{
		{
			name: "Nil auth config",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{Hostname: "test.example.com"},
			},
		},
		{
			name: "No groups scope and no keycloakConfig mappers",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						Scopes:  []string{"openid", "profile", "email"},
					},
				},
			},
		},
		{
			name: "Empty keycloakConfig protocolMappers",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						KeycloakConfig: &appsv1.KeycloakClientConfig{
							ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should return nil without making any Keycloak calls
			err := provider.syncClientProtocolMappers(context.Background(), nil, nil, "fake-id", tt.nebariApp)
			if err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestKeycloakProvider_DeleteClient(t *testing.T) {
	// Note: This test is limited because it requires a live Keycloak instance
	// In a real test environment, you would use httptest to mock the Keycloak API
	// For now, we'll just ensure the method exists and has the correct signature

	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{
			URL:           "http://keycloak.test",
			Realm:         "test",
			AdminUsername: "admin",
			AdminPassword: "admin",
		},
	}

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
	}

	// This will fail without a real Keycloak instance, but that's expected
	// The important part is that the method has the correct signature
	_ = provider.DeleteClient(context.Background(), nebariApp)
}

func TestKeycloakProvider_ProvisionClient(t *testing.T) {
	// Note: This test is limited because it requires a live Keycloak instance
	// In a real test environment, you would use httptest to mock the Keycloak API
	// For now, we'll just ensure the method exists and has the correct signature

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()

	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{
			URL:           "http://keycloak.test",
			Realm:         "test",
			AdminUsername: "admin",
			AdminPassword: "admin",
		},
		Client: client,
	}

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: appsv1.NebariAppSpec{
			Hostname: "test.example.com",
			Auth: &appsv1.AuthConfig{
				Enabled: true,
			},
		},
	}

	// This will fail without a real Keycloak instance, but that's expected
	// The important part is that the method has the correct signature
	_ = provider.ProvisionClient(context.Background(), nebariApp)
}

func TestKeycloakProvider_APITimeout(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: appsv1.NebariAppSpec{
			Hostname: "test.example.com",
			Auth: &appsv1.AuthConfig{
				Enabled: true,
			},
		},
	}

	tests := []struct {
		name        string
		timeout     time.Duration
		serverFunc  func(w http.ResponseWriter, r *http.Request)
		callFunc    string
		wantErr     bool
		wantTimeout bool
	}{
		{
			name:    "ProvisionClient times out with slow server",
			timeout: 100 * time.Millisecond,
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				// Simulate a slow Keycloak by sleeping longer than the timeout
				time.Sleep(500 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			},
			callFunc:    "ProvisionClient",
			wantErr:     true,
			wantTimeout: true,
		},
		{
			name:    "DeleteClient times out with slow server",
			timeout: 100 * time.Millisecond,
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				time.Sleep(500 * time.Millisecond)
				w.WriteHeader(http.StatusOK)
			},
			callFunc:    "DeleteClient",
			wantErr:     true,
			wantTimeout: true,
		},
		{
			name:    "ProvisionClient fails with auth error, not timeout",
			timeout: 5 * time.Second,
			serverFunc: func(w http.ResponseWriter, r *http.Request) {
				// Respond immediately with an auth error
				w.WriteHeader(http.StatusUnauthorized)
			},
			callFunc:    "ProvisionClient",
			wantErr:     true,
			wantTimeout: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverFunc))
			defer server.Close()

			provider := &KeycloakProvider{
				Config: config.KeycloakConfig{
					URL:           server.URL,
					Realm:         "test",
					AdminUsername: "admin",
					AdminPassword: "admin",
					APITimeout:    tt.timeout,
				},
				Client: k8sClient,
			}

			var err error
			switch tt.callFunc {
			case "ProvisionClient":
				err = provider.ProvisionClient(context.Background(), nebariApp)
			case "DeleteClient":
				err = provider.DeleteClient(context.Background(), nebariApp)
			}

			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}

			// gocloak does not use %w for error wrapping, so errors.Is cannot
			// traverse the chain. Check the error string instead.
			isTimeout := err != nil && strings.Contains(err.Error(), "context deadline exceeded")
			if tt.wantTimeout && !isTimeout {
				t.Errorf("expected timeout error, got: %v", err)
			}
			if !tt.wantTimeout && isTimeout {
				t.Errorf("expected non-timeout error, got: %v", err)
			}
		})
	}
}

func TestKeycloakProvider_WithAPITimeout(t *testing.T) {
	tests := []struct {
		name          string
		configTimeout time.Duration
		expectDefault bool
	}{
		{
			name:          "Uses configured timeout",
			configTimeout: 45 * time.Second,
			expectDefault: false,
		},
		{
			name:          "Falls back to default when zero",
			configTimeout: 0,
			expectDefault: true,
		},
		{
			name:          "Falls back to default when negative",
			configTimeout: -1 * time.Second,
			expectDefault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &KeycloakProvider{
				Config: config.KeycloakConfig{
					APITimeout: tt.configTimeout,
				},
			}

			ctx, cancel := provider.withAPITimeout(context.Background())
			defer cancel()

			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("expected context to have a deadline")
			}

			remaining := time.Until(deadline)
			if tt.expectDefault {
				// Should be close to 30s (the default)
				if remaining < 29*time.Second || remaining > 31*time.Second {
					t.Errorf("expected ~30s deadline, got %v", remaining)
				}
			} else {
				// Should be close to configured value
				expected := tt.configTimeout
				if remaining < expected-time.Second || remaining > expected+time.Second {
					t.Errorf("expected ~%v deadline, got %v", expected, remaining)
				}
			}
		})
	}
}

func TestKeycloakProvider_GetSPAClientID(t *testing.T) {
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{},
	}

	tests := []struct {
		name       string
		nebariApp  *appsv1.NebariApp
		expectedID string
	}{
		{
			name: "Default SPA client ID (no custom override)",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						SPAClient: &appsv1.SPAClientConfig{
							Enabled: true,
						},
					},
				},
			},
			expectedID: "default-test-app-spa",
		},
		{
			name: "Custom SPA client ID",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						SPAClient: &appsv1.SPAClientConfig{
							Enabled:  true,
							ClientID: "my-custom-spa-client",
						},
					},
				},
			},
			expectedID: "my-custom-spa-client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientID := provider.GetSPAClientID(context.Background(), tt.nebariApp)
			if clientID != tt.expectedID {
				t.Errorf("expected SPA client ID %s, got %s", tt.expectedID, clientID)
			}
		})
	}
}

func TestKeycloakProvider_GetExternalIssuerURL(t *testing.T) {
	tests := []struct {
		name        string
		config      config.KeycloakConfig
		nebariApp   *appsv1.NebariApp
		expected    string
		expectError bool
	}{
		{
			name: "External URL configured with context path",
			config: config.KeycloakConfig{
				ExternalURL: "https://keycloak.example.com/auth",
				Realm:       "nebari",
			},
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			},
			expected: "https://keycloak.example.com/auth/realms/nebari",
		},
		{
			name: "External URL without trailing slash",
			config: config.KeycloakConfig{
				ExternalURL: "https://keycloak.example.com",
				Realm:       "myrealm",
			},
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			},
			expected: "https://keycloak.example.com/realms/myrealm",
		},
		{
			name: "External URL with trailing slash",
			config: config.KeycloakConfig{
				ExternalURL: "https://keycloak.example.com/",
				Realm:       "nebari",
			},
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			},
			expected: "https://keycloak.example.com/realms/nebari",
		},
		{
			name: "External URL not configured",
			config: config.KeycloakConfig{
				Realm: "nebari",
			},
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &KeycloakProvider{Config: tt.config}
			got, err := provider.GetExternalIssuerURL(context.Background(), tt.nebariApp)
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestKeycloakProvider_ShouldProvisionSPAClient(t *testing.T) {
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{},
	}

	tests := []struct {
		name      string
		nebariApp *appsv1.NebariApp
		expected  bool
	}{
		{
			name: "SPA client enabled",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						SPAClient: &appsv1.SPAClientConfig{
							Enabled: true,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "SPA client disabled",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						SPAClient: &appsv1.SPAClientConfig{
							Enabled: false,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "SPA client not configured",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := provider.shouldProvisionSPAClient(tt.nebariApp)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestKeycloakProvider_ShouldProvisionDeviceFlowClient(t *testing.T) {
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{},
	}

	tests := []struct {
		name      string
		nebariApp *appsv1.NebariApp
		expected  bool
	}{
		{
			name: "Device flow client enabled",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						DeviceFlowClient: &appsv1.DeviceFlowClientConfig{
							Enabled: true,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "Device flow client disabled",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
						DeviceFlowClient: &appsv1.DeviceFlowClientConfig{
							Enabled: false,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "Device flow client not configured",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled: true,
					},
				},
			},
			expected: false,
		},
		{
			name: "Nil auth config",
			nebariApp: &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := provider.shouldProvisionDeviceFlowClient(tt.nebariApp)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestKeycloakProvider_GetDeviceFlowClientID(t *testing.T) {
	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{},
	}

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
	}

	clientID := provider.GetDeviceFlowClientID(context.Background(), nebariApp)
	expected := "default-test-app-device"

	if clientID != expected {
		t.Errorf("expected device flow client ID %q, got %q", expected, clientID)
	}
}

func TestKeycloakProvider_ConfigureTokenExchange(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	provider := &KeycloakProvider{
		Config: config.KeycloakConfig{
			URL:           "http://keycloak.test",
			Realm:         "test",
			AdminUsername: "admin",
			AdminPassword: "admin",
		},
		Client: k8sClient,
	}

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: appsv1.NebariAppSpec{
			Hostname: "test.example.com",
			Auth: &appsv1.AuthConfig{
				Enabled: true,
				TokenExchange: &appsv1.TokenExchangeConfig{
					Enabled: true,
				},
			},
		},
	}

	// Will fail without a live Keycloak instance, but verifies the method
	// has the correct signature and handles the call path
	_ = provider.ConfigureTokenExchange(context.Background(), nebariApp, []string{"peer-client-uuid"})
}

// fakeGroupsKeycloak serves group-by-path lookups for the given path -> ID map
// and records the names of groups created through POST /groups. Any other
// path gets a 404. It is a bare handler (no ServeMux) so a "//" in the URL
// reaches it unchanged.
type fakeGroupsKeycloak struct {
	t        *testing.T
	groups   map[string]string
	status   int // when non-zero, every lookup fails with this status
	created  []string
	requests []string
}

func (f *fakeGroupsKeycloak) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.requests = append(f.requests, r.Method+" "+r.RequestURI)
	const byPath = "/admin/realms/test/group-by-path/"
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.RequestURI, byPath):
		if f.status != 0 {
			w.WriteHeader(f.status)
			return
		}
		groupPath := "/" + strings.TrimPrefix(r.RequestURI, byPath)
		id, ok := f.groups[groupPath]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + id + `","path":"` + groupPath + `"}`))
	case r.Method == http.MethodPost && r.RequestURI == "/admin/realms/test/groups":
		var g gocloak.Group
		if err := json.NewDecoder(r.Body).Decode(&g); err != nil || g.Name == nil {
			f.t.Errorf("bad CreateGroup body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.created = append(f.created, *g.Name)
		w.Header().Set("Location", "/admin/realms/test/groups/created-"+*g.Name)
		w.WriteHeader(http.StatusCreated)
	default:
		f.t.Errorf("unexpected request: %s %s", r.Method, r.RequestURI)
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func newGroupsTestProvider(t *testing.T, kcServer *fakeGroupsKeycloak) (*KeycloakProvider, *gocloak.GoCloak) {
	t.Helper()
	kcServer.t = t
	server := httptest.NewServer(kcServer)
	t.Cleanup(server.Close)
	provider := &KeycloakProvider{Config: config.KeycloakConfig{URL: server.URL, Realm: "test"}}
	return provider, gocloak.NewClient(server.URL)
}

// TestEnsureGroup covers lookup by full path and when a missing group is
// created. Regression coverage for upstream issue #191.
func TestEnsureGroup(t *testing.T) {
	token := &gocloak.JWT{AccessToken: "test-token"}
	existing := map[string]string{
		"/team-example":      "top-uuid",
		"/parent/team-child": "nested-uuid",
	}

	tests := []struct {
		name        string
		groupPath   string
		create      bool
		status      int
		wantID      string
		wantCreated []string
		wantErr     bool
		wantMissing bool
	}{
		{name: "top-level group exists", groupPath: "/team-example", create: true, wantID: "top-uuid"},
		{name: "top-level group exists, create not allowed", groupPath: "/team-example", wantID: "top-uuid"},
		{name: "nested group exists", groupPath: "/parent/team-child", wantID: "nested-uuid"},
		{name: "missing group is created when allowed", groupPath: "/team-fresh", create: true, wantID: "created-team-fresh", wantCreated: []string{"team-fresh"}},
		{name: "missing group is not created when not allowed", groupPath: "/team-missing", wantErr: true, wantMissing: true},
		{name: "missing nested group is not created", groupPath: "/parent/missing", wantErr: true, wantMissing: true},
		{name: "lookup error is not treated as missing", groupPath: "/team-fresh", create: true, status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kcServer := &fakeGroupsKeycloak{groups: existing, status: tt.status}
			provider, kc := newGroupsTestProvider(t, kcServer)

			gotID, err := provider.ensureGroup(context.Background(), kc, token, "test", tt.groupPath, tt.create)

			for _, req := range kcServer.requests {
				if strings.Contains(req, "//") {
					t.Errorf("request URI contains \"//\": %q", req)
				}
			}
			if !reflect.DeepEqual(kcServer.created, tt.wantCreated) {
				t.Errorf("created groups = %q, want %q", kcServer.created, tt.wantCreated)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got id=%q", gotID)
				}
				if got := errors.Is(err, errGroupNotFound); got != tt.wantMissing {
					t.Errorf("errors.Is(err, errGroupNotFound) = %v, want %v (err: %v)", got, tt.wantMissing, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotID != tt.wantID {
				t.Errorf("got id=%q, want %q", gotID, tt.wantID)
			}
		})
	}
}

// TestSyncGroups_UnresolvedPaths verifies that a missing path does not stop
// the other groups from syncing and comes back as a GroupsNotResolvedError.
func TestSyncGroups_UnresolvedPaths(t *testing.T) {
	token := &gocloak.JWT{AccessToken: "test-token"}
	kcServer := &fakeGroupsKeycloak{groups: map[string]string{"/exists": "exists-uuid"}}
	provider, kc := newGroupsTestProvider(t, kcServer)

	app := &appsv1.NebariApp{Spec: appsv1.NebariAppSpec{Auth: &appsv1.AuthConfig{
		Enabled: true,
		Groups:  []string{"/exists", "/does-not-exist", "/parent/missing", "fresh"},
	}}}

	err := provider.syncGroups(context.Background(), kc, token, app)

	var notResolved *GroupsNotResolvedError
	if !errors.As(err, &notResolved) {
		t.Fatalf("expected *GroupsNotResolvedError, got %v", err)
	}
	if want := []string{"/does-not-exist", "/parent/missing"}; !reflect.DeepEqual(notResolved.Paths, want) {
		t.Errorf("Paths = %q, want %q", notResolved.Paths, want)
	}
	if notResolved.Realm != "test" {
		t.Errorf("Realm = %q, want %q", notResolved.Realm, "test")
	}
	if want := []string{"fresh"}; !reflect.DeepEqual(kcServer.created, want) {
		t.Errorf("created groups = %q, want %q", kcServer.created, want)
	}
}

// TestEnsureGroup_PathLookupURL verifies the group-by-path request URL never
// contains a double slash. gocloak joins path segments with "/", so passing the
// leading "/" through yields ".../group-by-path//name", which Keycloak rejects
// with HTTP 400 missingNormalization even when the group exists.
func TestEnsureGroup_PathLookupURL(t *testing.T) {
	token := &gocloak.JWT{AccessToken: "test-token"}

	tests := []struct {
		name      string
		groupName string
		wantPath  string
		wantID    string
	}{
		{
			name:      "top-level path",
			groupName: "/team-example",
			wantPath:  "/admin/realms/test/group-by-path/team-example",
			wantID:    "top-uuid",
		},
		{
			name:      "nested path",
			groupName: "/parent/child",
			wantPath:  "/admin/realms/test/group-by-path/parent/child",
			wantID:    "nested-uuid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotURI string
			// A bare handler (no ServeMux) so "//" is not cleaned or redirected
			// before we see it.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotURI = r.RequestURI
				if strings.Contains(r.RequestURI, "//") {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"missingNormalization"}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"` + tt.wantID + `","path":"` + tt.groupName + `"}`))
			}))
			defer server.Close()

			provider := &KeycloakProvider{Config: config.KeycloakConfig{URL: server.URL, Realm: "test"}}
			kc := gocloak.NewClient(server.URL)

			gotID, err := provider.ensureGroup(context.Background(), kc, token, "test", tt.groupName, false)

			if strings.Contains(gotURI, "//") {
				t.Errorf("group-by-path request URI contains \"//\": %q", gotURI)
			}
			if gotURI != tt.wantPath {
				t.Errorf("request URI = %q, want %q", gotURI, tt.wantPath)
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotID != tt.wantID {
				t.Errorf("got id=%q, want %q", gotID, tt.wantID)
			}
		})
	}
}
