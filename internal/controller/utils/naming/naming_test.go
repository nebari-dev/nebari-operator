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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
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
