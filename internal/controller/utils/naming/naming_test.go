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

package naming

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

func TestResourceName(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-app",
		},
	}

	tests := []struct {
		name         string
		resourceType string
		expected     string
	}{
		{"route resource", "route", "test-app-route"},
		{"security resource", "security", "test-app-security"},
		{"certificate resource", "certificate", "test-app-certificate"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ResourceName(nebariApp, tt.resourceType)
			if result != tt.expected {
				t.Errorf("ResourceName(%q) = %q, want %q", tt.resourceType, result, tt.expected)
			}
		})
	}
}

func TestSecurityPolicyName(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-app",
		},
	}

	expected := "my-app-security"
	result := SecurityPolicyName(nebariApp)

	if result != expected {
		t.Errorf("SecurityPolicyName() = %q, want %q", result, expected)
	}
}

func TestHTTPRouteName(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-app",
		},
	}

	expected := "my-app-route"
	result := HTTPRouteName(nebariApp)

	if result != expected {
		t.Errorf("HTTPRouteName() = %q, want %q", result, expected)
	}
}

func TestClientSecretName(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name: "auth-app",
		},
	}

	expected := "auth-app-oidc-client"
	result := ClientSecretName(nebariApp)

	if result != expected {
		t.Errorf("ClientSecretName() = %q, want %q", result, expected)
	}
}

func TestClientID(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "staging",
		},
	}

	expected := "staging-my-app"
	result := ClientID(nebariApp)

	if result != expected {
		t.Errorf("ClientID() = %q, want %q", result, expected)
	}
}

func TestCertificateName(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		namespace string
		expected  string
	}{
		{
			name:      "standard app",
			appName:   "my-app",
			namespace: "default",
			expected:  "my-app-default-cert",
		},
		{
			name:      "app in custom namespace",
			appName:   "web-ui",
			namespace: "production",
			expected:  "web-ui-production-cert",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nebariApp := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.appName,
					Namespace: tt.namespace,
				},
			}
			result := CertificateName(nebariApp)
			if result != tt.expected {
				t.Errorf("CertificateName() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestCertificateSecretName(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		namespace string
		expected  string
	}{
		{
			name:      "standard app",
			appName:   "my-app",
			namespace: "default",
			expected:  "my-app-default-tls",
		},
		{
			name:      "app in custom namespace",
			appName:   "web-ui",
			namespace: "production",
			expected:  "web-ui-production-tls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nebariApp := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.appName,
					Namespace: tt.namespace,
				},
			}
			result := CertificateSecretName(nebariApp)
			if result != tt.expected {
				t.Errorf("CertificateSecretName() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestListenerName(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		namespace string
		expected  string
	}{
		{
			name:      "standard app",
			appName:   "my-app",
			namespace: "default",
			expected:  "tls-my-app-default",
		},
		{
			name:      "app in custom namespace",
			appName:   "web-ui",
			namespace: "production",
			expected:  "tls-web-ui-production",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nebariApp := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.appName,
					Namespace: tt.namespace,
				},
			}
			result := ListenerName(nebariApp)
			if result != tt.expected {
				t.Errorf("ListenerName() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestValidateResourceNames(t *testing.T) {
	tests := []struct {
		name        string
		appName     string
		namespace   string
		expectError bool
	}{
		{
			name:        "short names pass validation",
			appName:     "my-app",
			namespace:   "default",
			expectError: false,
		},
		{
			name:        "long app name exceeds limit",
			appName:     strings.Repeat("a", 250),
			namespace:   "default",
			expectError: true,
		},
		{
			name:        "names just under limit pass",
			appName:     strings.Repeat("a", 120),
			namespace:   strings.Repeat("b", 60),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nebariApp := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.appName,
					Namespace: tt.namespace,
				},
			}
			err := ValidateResourceNames(nebariApp)
			if (err != nil) != tt.expectError {
				t.Errorf("ValidateResourceNames() error = %v, expectError = %v", err, tt.expectError)
			}
		})
	}
}

func TestGatewayName(t *testing.T) {
	tests := []struct {
		name     string
		gateway  string
		expected string
	}{
		{"public gateway (explicit)", "public", constants.PublicGatewayName},
		{"public gateway (empty)", "", constants.PublicGatewayName},
		{"internal gateway", "internal", constants.InternalGatewayName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nebariApp := &appsv1.NebariApp{
				Spec: appsv1.NebariAppSpec{
					Gateway: tt.gateway,
				},
			}
			result := GatewayName(nebariApp)
			if result != tt.expected {
				t.Errorf("GatewayName(%q) = %q, want %q", tt.gateway, result, tt.expected)
			}
		})
	}
}
