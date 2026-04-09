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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/config"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
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

func TestKeycloakProvider_SyncGroups_Deduplication(t *testing.T) {
	// Verify that groups from auth.groups and keycloakConfig.groups are deduplicated
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			Hostname: "test.example.com",
			Auth: &appsv1.AuthConfig{
				Enabled: true,
				Groups:  []string{"admin", "viewer"},
				KeycloakConfig: &appsv1.KeycloakClientConfig{
					Groups: []appsv1.KeycloakGroup{
						{Name: "admin", Members: []string{"admin-user"}},
						{Name: "editor"},
					},
				},
			},
		},
	}

	// Use the exported MergeGroupMembers helper to test the deduplication logic
	groupMembers := MergeGroupMembers(nebariApp.Spec.Auth.Groups, nebariApp.Spec.Auth.KeycloakConfig)

	if len(groupMembers) != 3 {
		t.Errorf("expected 3 deduplicated groups, got %d", len(groupMembers))
	}

	// "admin" from keycloakConfig should override auth.groups (has members)
	if members, ok := groupMembers["admin"]; !ok {
		t.Error("expected 'admin' group to exist")
	} else if len(members) != 1 || members[0] != "admin-user" {
		t.Errorf("expected 'admin' group members to be [admin-user], got %v", members)
	}

	// "viewer" from auth.groups only (no members)
	if members, ok := groupMembers["viewer"]; !ok {
		t.Error("expected 'viewer' group to exist")
	} else if members != nil {
		t.Errorf("expected 'viewer' group members to be nil, got %v", members)
	}

	// "editor" from keycloakConfig only
	if _, ok := groupMembers["editor"]; !ok {
		t.Error("expected 'editor' group to exist")
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
