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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenericOIDCProvider_GetIssuerURL(t *testing.T) {
	provider := &GenericOIDCProvider{}

	tests := []struct {
		name        string
		nebariApp   *appsv1.NebariApp
		expectError bool
		expectedURL string
	}{
		{
			name: "Valid issuer URL",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:   true,
						Provider:  "generic-oidc",
						IssuerURL: "https://accounts.google.com",
					},
				},
			},
			expectError: false,
			expectedURL: "https://accounts.google.com",
		},
		{
			name: "Missing issuer URL",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: &appsv1.AuthConfig{
						Enabled:   true,
						Provider:  "generic-oidc",
						IssuerURL: "",
					},
				},
			},
			expectError: true,
		},
		{
			name: "Auth config is nil",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Auth: nil,
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := provider.GetIssuerURL(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if !tt.expectError && url != tt.expectedURL {
				t.Errorf("expected URL %s, got %s", tt.expectedURL, url)
			}
		})
	}
}

func TestGenericOIDCProvider_GetClientID(t *testing.T) {
	provider := &GenericOIDCProvider{}

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

func TestGenericOIDCProvider_SupportsProvisioning(t *testing.T) {
	provider := &GenericOIDCProvider{}

	if provider.SupportsProvisioning() {
		t.Error("expected GenericOIDCProvider to not support provisioning")
	}
}

func TestGenericOIDCProvider_ProvisionClient(t *testing.T) {
	provider := &GenericOIDCProvider{}

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
	}

	err := provider.ProvisionClient(context.Background(), nebariApp)
	if err == nil {
		t.Error("expected error when provisioning with GenericOIDCProvider, got nil")
	}
}

func TestGenericOIDCProvider_DeleteClient(t *testing.T) {
	provider := &GenericOIDCProvider{}

	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
	}

	err := provider.DeleteClient(context.Background(), nebariApp)
	if err != nil {
		t.Errorf("expected no error when deleting with GenericOIDCProvider, got: %v", err)
	}
}
