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

package config

import (
	"context"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestLoadAuthConfig(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected AuthConfig
	}{
		{
			name:    "Default configuration",
			envVars: map[string]string{},
			expected: AuthConfig{
				Keycloak: KeycloakConfig{
					Enabled:                true,
					URL:                    "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/auth",
					Realm:                  "nebari",
					AdminSecretName:        "nebari-realm-admin-credentials",
					AdminSecretNamespace:   "keycloak",
					IssuerServiceName:      "keycloak-keycloakx-http",
					IssuerServiceNamespace: "keycloak",
					IssuerServicePort:      8080,
					IssuerContextPath:      "/auth",
				},
			},
		},
		{
			name: "Custom configuration",
			envVars: map[string]string{
				"KEYCLOAK_ENABLED":                "false",
				"KEYCLOAK_URL":                    "https://keycloak.example.com",
				"KEYCLOAK_REALM":                  "custom-realm",
				"KEYCLOAK_ADMIN_SECRET_NAME":      "custom-secret",
				"KEYCLOAK_ADMIN_SECRET_NAMESPACE": "custom-ns",
			},
			expected: AuthConfig{
				Keycloak: KeycloakConfig{
					Enabled:                false,
					URL:                    "https://keycloak.example.com",
					Realm:                  "custom-realm",
					AdminSecretName:        "custom-secret",
					AdminSecretNamespace:   "custom-ns",
					IssuerServiceName:      "keycloak-keycloakx-http",
					IssuerServiceNamespace: "keycloak",
					IssuerServicePort:      8080,
					IssuerContextPath:      "/auth",
				},
			},
		},
		{
			name: "Environment credentials",
			envVars: map[string]string{
				"KEYCLOAK_ADMIN_USERNAME": "test-user",
				"KEYCLOAK_ADMIN_PASSWORD": "test-password",
			},
			expected: AuthConfig{
				Keycloak: KeycloakConfig{
					Enabled:                true,
					URL:                    "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/auth",
					Realm:                  "nebari",
					AdminSecretName:        "nebari-realm-admin-credentials",
					AdminSecretNamespace:   "keycloak",
					AdminUsername:          "test-user",
					AdminPassword:          "test-password",
					IssuerServiceName:      "keycloak-keycloakx-http",
					IssuerServiceNamespace: "keycloak",
					IssuerServicePort:      8080,
					IssuerContextPath:      "/auth",
				},
			},
		},
		{
			name: "Custom issuer URL components",
			envVars: map[string]string{
				"KEYCLOAK_ISSUER_SERVICE_NAME":      "custom-keycloak",
				"KEYCLOAK_ISSUER_SERVICE_NAMESPACE": "auth",
				"KEYCLOAK_ISSUER_SERVICE_PORT":      "9090",
				"KEYCLOAK_ISSUER_CONTEXT_PATH":      "",
			},
			expected: AuthConfig{
				Keycloak: KeycloakConfig{
					Enabled:                true,
					URL:                    "http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/auth",
					Realm:                  "nebari",
					AdminSecretName:        "nebari-realm-admin-credentials",
					AdminSecretNamespace:   "keycloak",
					IssuerServiceName:      "custom-keycloak",
					IssuerServiceNamespace: "auth",
					IssuerServicePort:      9090,
					IssuerContextPath:      "",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment
			os.Clearenv()

			// Set test environment variables
			for key, value := range tt.envVars {
				_ = os.Setenv(key, value)
			}
			defer os.Clearenv()

			config := LoadAuthConfig()

			if config.Keycloak.Enabled != tt.expected.Keycloak.Enabled {
				t.Errorf("Enabled: expected %v, got %v", tt.expected.Keycloak.Enabled, config.Keycloak.Enabled)
			}
			if config.Keycloak.URL != tt.expected.Keycloak.URL {
				t.Errorf("URL: expected %s, got %s", tt.expected.Keycloak.URL, config.Keycloak.URL)
			}
			if config.Keycloak.Realm != tt.expected.Keycloak.Realm {
				t.Errorf("Realm: expected %s, got %s", tt.expected.Keycloak.Realm, config.Keycloak.Realm)
			}
			if config.Keycloak.AdminSecretName != tt.expected.Keycloak.AdminSecretName {
				t.Errorf("AdminSecretName: expected %s, got %s", tt.expected.Keycloak.AdminSecretName, config.Keycloak.AdminSecretName)
			}
			if config.Keycloak.AdminSecretNamespace != tt.expected.Keycloak.AdminSecretNamespace {
				t.Errorf("AdminSecretNamespace: expected %s, got %s", tt.expected.Keycloak.AdminSecretNamespace, config.Keycloak.AdminSecretNamespace)
			}
			if config.Keycloak.AdminUsername != tt.expected.Keycloak.AdminUsername {
				t.Errorf("AdminUsername: expected %s, got %s", tt.expected.Keycloak.AdminUsername, config.Keycloak.AdminUsername)
			}
			if config.Keycloak.AdminPassword != tt.expected.Keycloak.AdminPassword {
				t.Errorf("AdminPassword: expected %s, got %s", tt.expected.Keycloak.AdminPassword, config.Keycloak.AdminPassword)
			}
		})
	}
}

func TestLoadKeycloakCredentials(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name         string
		secret       *corev1.Secret
		envVars      map[string]string
		expectError  bool
		expectedUser string
		expectedPass string
	}{
		{
			name: "Load from secret with 'username' and 'password' keys",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nebari-realm-admin-credentials",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"username": []byte("secret-user"),
					"password": []byte("secret-pass"),
				},
			},
			envVars:      map[string]string{},
			expectError:  false,
			expectedUser: "secret-user",
			expectedPass: "secret-pass",
		},
		{
			name: "Load from secret with 'admin-username' and 'admin-password' keys",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nebari-realm-admin-credentials",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"admin-username": []byte("admin-user"),
					"admin-password": []byte("admin-pass"),
				},
			},
			envVars:      map[string]string{},
			expectError:  false,
			expectedUser: "admin-user",
			expectedPass: "admin-pass",
		},
		{
			name:   "Secret not found - use environment variables",
			secret: nil,
			envVars: map[string]string{
				"KEYCLOAK_ADMIN_USERNAME": "env-user",
				"KEYCLOAK_ADMIN_PASSWORD": "env-pass",
			},
			expectError:  false,
			expectedUser: "env-user",
			expectedPass: "env-pass",
		},
		{
			name:         "Secret not found - no environment variables",
			secret:       nil,
			envVars:      map[string]string{},
			expectError:  true,
			expectedUser: "",
			expectedPass: "",
		},
		{
			name: "Secret with missing username field",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nebari-realm-admin-credentials",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"password": []byte("secret-pass"),
				},
			},
			envVars:      map[string]string{},
			expectError:  true,
			expectedUser: "",
			expectedPass: "",
		},
		{
			name: "Secret with missing password field",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nebari-realm-admin-credentials",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"username": []byte("secret-user"),
				},
			},
			envVars:      map[string]string{},
			expectError:  true,
			expectedUser: "",
			expectedPass: "",
		},
		{
			name: "Secret takes priority over environment variables",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nebari-realm-admin-credentials",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"username": []byte("secret-user"),
					"password": []byte("secret-pass"),
				},
			},
			envVars: map[string]string{
				"KEYCLOAK_ADMIN_USERNAME": "env-user",
				"KEYCLOAK_ADMIN_PASSWORD": "env-pass",
			},
			expectError:  false,
			expectedUser: "secret-user",
			expectedPass: "secret-pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear and set environment
			os.Clearenv()
			for key, value := range tt.envVars {
				_ = os.Setenv(key, value)
			}
			defer os.Clearenv()

			// Create fake client
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.secret != nil {
				builder = builder.WithObjects(tt.secret)
			}
			client := builder.Build()

			// Create config with defaults - initialize with env vars if set
			config := AuthConfig{
				Keycloak: KeycloakConfig{
					AdminSecretName:      "nebari-realm-admin-credentials",
					AdminSecretNamespace: "keycloak",
					AdminUsername:        tt.envVars["KEYCLOAK_ADMIN_USERNAME"],
					AdminPassword:        tt.envVars["KEYCLOAK_ADMIN_PASSWORD"],
				},
			}

			// Load credentials
			err := config.Keycloak.LoadKeycloakCredentials(context.Background(), client)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			if !tt.expectError {
				if config.Keycloak.AdminUsername != tt.expectedUser {
					t.Errorf("AdminUsername: expected %s, got %s", tt.expectedUser, config.Keycloak.AdminUsername)
				}
				if config.Keycloak.AdminPassword != tt.expectedPass {
					t.Errorf("AdminPassword: expected %s, got %s", tt.expectedPass, config.Keycloak.AdminPassword)
				}
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name        string
		secret      *corev1.Secret
		envVars     map[string]string
		expectError bool
	}{
		{
			name: "Successful config load with secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nebari-realm-admin-credentials",
					Namespace: "keycloak",
				},
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("password"),
				},
			},
			envVars: map[string]string{
				"KEYCLOAK_ENABLED": "true",
			},
			expectError: false,
		},
		{
			name:   "Keycloak disabled - no secret needed",
			secret: nil,
			envVars: map[string]string{
				"KEYCLOAK_ENABLED": "false",
			},
			expectError: false,
		},
		{
			name:   "Keycloak enabled but secret not found",
			secret: nil,
			envVars: map[string]string{
				"KEYCLOAK_ENABLED": "true",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear and set environment
			os.Clearenv()
			for key, value := range tt.envVars {
				_ = os.Setenv(key, value)
			}
			defer os.Clearenv()

			// Create fake client
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.secret != nil {
				builder = builder.WithObjects(tt.secret)
			}
			client := builder.Build()

			// Load config
			config, err := LoadConfig(context.Background(), client)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if !tt.expectError && config == nil {
				t.Error("expected config, got nil")
			}
		})
	}
}
