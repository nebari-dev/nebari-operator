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

package v1

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSPAClientConfig_Defaults(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected SPAClientConfig
	}{
		{
			name: "SPAClient enabled without custom clientID",
			input: `{
				"enabled": true
			}`,
			expected: SPAClientConfig{
				Enabled:  true,
				ClientID: "",
			},
		},
		{
			name: "SPAClient enabled with custom clientID",
			input: `{
				"enabled": true,
				"clientId": "my-custom-spa-client"
			}`,
			expected: SPAClientConfig{
				Enabled:  true,
				ClientID: "my-custom-spa-client",
			},
		},
		{
			name: "SPAClient disabled",
			input: `{
				"enabled": false
			}`,
			expected: SPAClientConfig{
				Enabled: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config SPAClientConfig
			err := json.Unmarshal([]byte(tt.input), &config)
			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if config.Enabled != tt.expected.Enabled {
				t.Errorf("Enabled: expected %v, got %v", tt.expected.Enabled, config.Enabled)
			}
			if config.ClientID != tt.expected.ClientID {
				t.Errorf("ClientID: expected %q, got %q", tt.expected.ClientID, config.ClientID)
			}
		})
	}
}

func TestAuthConfig_WithSPAClient(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		validate func(t *testing.T, auth AuthConfig)
	}{
		{
			name: "Auth with both confidential and SPA client",
			input: `{
				"enabled": true,
				"provider": "keycloak",
				"redirectURI": "/oauth2/callback",
				"spaClient": {
					"enabled": true,
					"clientId": "my-app-spa"
				}
			}`,
			validate: func(t *testing.T, auth AuthConfig) {
				if !auth.Enabled {
					t.Error("Expected auth.Enabled to be true")
				}
				if auth.Provider != "keycloak" {
					t.Errorf("Expected provider to be 'keycloak', got %q", auth.Provider)
				}
				if auth.SPAClient == nil {
					t.Fatal("Expected SPAClient to be non-nil")
				}
				if !auth.SPAClient.Enabled {
					t.Error("Expected SPAClient.Enabled to be true")
				}
				if auth.SPAClient.ClientID != "my-app-spa" {
					t.Errorf("Expected SPAClient.ClientID to be 'my-app-spa', got %q", auth.SPAClient.ClientID)
				}
			},
		},
		{
			name: "Auth without SPA client",
			input: `{
				"enabled": true,
				"provider": "keycloak"
			}`,
			validate: func(t *testing.T, auth AuthConfig) {
				if auth.SPAClient != nil {
					t.Error("Expected SPAClient to be nil when not specified")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var auth AuthConfig
			err := json.Unmarshal([]byte(tt.input), &auth)
			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, auth)
			}
		})
	}
}

func TestNebariAppSpec_WithSPAClient(t *testing.T) {
	tests := []struct {
		name      string
		nebariApp *NebariApp
		validate  func(t *testing.T, app *NebariApp)
	}{
		{
			name: "Complete NebariApp with SPA client configuration",
			nebariApp: &NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: NebariAppSpec{
					Hostname: "test-app.nebari.local",
					Service: ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					Auth: &AuthConfig{
						Enabled:  true,
						Provider: "keycloak",
						SPAClient: &SPAClientConfig{
							Enabled:  true,
							ClientID: "test-app-spa",
						},
					},
				},
			},
			validate: func(t *testing.T, app *NebariApp) {
				if app.Spec.Auth == nil {
					t.Fatal("Expected Spec.Auth to be non-nil")
				}
				if app.Spec.Auth.SPAClient == nil {
					t.Fatal("Expected Spec.Auth.SPAClient to be non-nil")
				}
				if !app.Spec.Auth.SPAClient.Enabled {
					t.Error("Expected SPAClient.Enabled to be true")
				}
				if app.Spec.Auth.SPAClient.ClientID != "test-app-spa" {
					t.Errorf("Expected SPAClient.ClientID to be 'test-app-spa', got %q", app.Spec.Auth.SPAClient.ClientID)
				}
			},
		},
		{
			name: "NebariApp with enforceAtGateway=false and SPA client enabled",
			nebariApp: &NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "grafana-app",
					Namespace: "default",
				},
				Spec: NebariAppSpec{
					Hostname: "grafana.nebari.local",
					Service: ServiceReference{
						Name: "grafana",
						Port: 3000,
					},
					Auth: &AuthConfig{
						Enabled:          true,
						Provider:         "keycloak",
						EnforceAtGateway: boolPtr(false),
						SPAClient: &SPAClientConfig{
							Enabled:  true,
							ClientID: "grafana-spa",
						},
					},
				},
			},
			validate: func(t *testing.T, app *NebariApp) {
				if app.Spec.Auth == nil {
					t.Fatal("Expected Spec.Auth to be non-nil")
				}
				if app.Spec.Auth.EnforceAtGateway == nil {
					t.Fatal("Expected EnforceAtGateway to be non-nil")
				}
				if *app.Spec.Auth.EnforceAtGateway {
					t.Error("Expected EnforceAtGateway to be false")
				}
				if app.Spec.Auth.SPAClient == nil {
					t.Fatal("Expected Spec.Auth.SPAClient to be non-nil")
				}
				if !app.Spec.Auth.SPAClient.Enabled {
					t.Error("Expected SPAClient.Enabled to be true")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal and unmarshal to ensure JSON serialization works
			data, err := json.Marshal(tt.nebariApp)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var decoded NebariApp
			err = json.Unmarshal(data, &decoded)
			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if tt.validate != nil {
				tt.validate(t, &decoded)
			}
		})
	}
}

func TestSPAClientConfig_JSONSerialization(t *testing.T) {
	tests := []struct {
		name   string
		config *SPAClientConfig
	}{
		{
			name: "Enabled SPA client with custom ID",
			config: &SPAClientConfig{
				Enabled:  true,
				ClientID: "custom-spa-client",
			},
		},
		{
			name: "Disabled SPA client",
			config: &SPAClientConfig{
				Enabled: false,
			},
		},
		{
			name: "Enabled without custom ID",
			config: &SPAClientConfig{
				Enabled: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.config)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			// Test round-trip
			var decoded SPAClientConfig
			err = json.Unmarshal(data, &decoded)
			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if tt.config.Enabled != decoded.Enabled {
				t.Errorf("Enabled mismatch: expected %v, got %v", tt.config.Enabled, decoded.Enabled)
			}
			if tt.config.ClientID != decoded.ClientID {
				t.Errorf("ClientID mismatch: expected %q, got %q", tt.config.ClientID, decoded.ClientID)
			}
		})
	}
}

func TestDeviceFlowClientConfig_JSON(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected DeviceFlowClientConfig
	}{
		{
			name:     "enabled true",
			json:     `{"enabled": true}`,
			expected: DeviceFlowClientConfig{Enabled: true},
		},
		{
			name:     "enabled false",
			json:     `{"enabled": false}`,
			expected: DeviceFlowClientConfig{Enabled: false},
		},
		{
			name:     "empty object",
			json:     `{}`,
			expected: DeviceFlowClientConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got DeviceFlowClientConfig
			if err := json.Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if got != tt.expected {
				t.Errorf("got %+v, want %+v", got, tt.expected)
			}
		})
	}
}

func TestNebariAppSpec_ServiceAccountName(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected string
	}{
		{
			name:     "omitted defaults to empty",
			json:     `{"hostname":"test.example.com","service":{"name":"svc","port":80}}`,
			expected: "",
		},
		{
			name:     "explicitly set",
			json:     `{"hostname":"test.example.com","service":{"name":"svc","port":80},"serviceAccountName":"my-sa"}`,
			expected: "my-sa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got NebariAppSpec
			if err := json.Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if got.ServiceAccountName != tt.expected {
				t.Errorf("ServiceAccountName = %q, want %q", got.ServiceAccountName, tt.expected)
			}
		})
	}
}

// boolPtr is a helper function to create a pointer to a bool
func boolPtr(b bool) *bool {
	return &b
}
