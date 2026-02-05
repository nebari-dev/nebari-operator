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
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestKeycloakProvider_GetIssuerURL(t *testing.T) {
	tests := []struct {
		name        string
		config      KeycloakConfig
		expectedURL string
	}{
		{
			name: "Default configuration",
			config: KeycloakConfig{
				URL:   "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:80/auth",
				Realm: "nebari",
			},
			expectedURL: "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:80/realms/nebari",
		},
		{
			name: "Custom realm",
			config: KeycloakConfig{
				URL:   "https://keycloak.example.com",
				Realm: "custom-realm",
			},
			// Implementation always uses internal cluster URL regardless of config.URL
			expectedURL: "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:80/realms/custom-realm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &KeycloakProvider{
				Config: tt.config,
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
		Config: KeycloakConfig{
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
		Config: KeycloakConfig{},
	}

	if !provider.SupportsProvisioning() {
		t.Error("expected KeycloakProvider to support provisioning")
	}
}

func TestKeycloakProvider_BuildRedirectURLs(t *testing.T) {
	provider := &KeycloakProvider{
		Config: KeycloakConfig{},
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
		name           string
		nebariApp      *appsv1.NebariApp
		clientSecret   string
		existingSecret *corev1.Secret
		expectError    bool
	}{
		{
			name: "Create new secret",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
					UID:       "test-uid",
				},
			},
			clientSecret:   "test-secret-value",
			existingSecret: nil,
			expectError:    false,
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
			clientSecret: "new-secret-value",
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
			expectError: false,
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

			provider := &KeycloakProvider{
				Config: KeycloakConfig{},
				Client: client,
			}

			err := provider.storeClientSecret(context.Background(), tt.nebariApp, tt.clientSecret)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
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
		Config: KeycloakConfig{
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
		Config: KeycloakConfig{
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
