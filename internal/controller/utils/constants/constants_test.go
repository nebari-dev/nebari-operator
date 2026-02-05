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

package constants

import "testing"

// TestConstants verifies that all package constants are defined and non-empty
func TestConstants(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"PublicGatewayName", PublicGatewayName},
		{"InternalGatewayName", InternalGatewayName},
		{"GatewayNamespace", GatewayNamespace},
		{"GatewayClassName", GatewayClassName},
		{"DefaultTLSSecretName", DefaultTLSSecretName},
		{"HTTPRouteSuffix", HTTPRouteSuffix},
		{"SecurityPolicySuffix", SecurityPolicySuffix},
		{"CertificateSuffix", CertificateSuffix},
		{"ClientSecretSuffix", ClientSecretSuffix},
		{"ClientSecretKey", ClientSecretKey},
		{"NebariAppFinalizer", NebariAppFinalizer},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value == "" {
				t.Errorf("%s is empty", tt.name)
			}
		})
	}
}

// TestGatewayConstants verifies gateway-related constants
func TestGatewayConstants(t *testing.T) {
	if PublicGatewayName != "nebari-gateway" {
		t.Errorf("PublicGatewayName = %q, want %q", PublicGatewayName, "nebari-gateway")
	}

	if GatewayNamespace != "envoy-gateway-system" {
		t.Errorf("GatewayNamespace = %q, want %q", GatewayNamespace, "envoy-gateway-system")
	}

	if GatewayClassName != "envoy-gateway" {
		t.Errorf("GatewayClassName = %q, want %q", GatewayClassName, "envoy-gateway")
	}
}

// TestResourceSuffixes verifies resource naming suffixes
func TestResourceSuffixes(t *testing.T) {
	if HTTPRouteSuffix != "route" {
		t.Errorf("HTTPRouteSuffix = %q, want %q", HTTPRouteSuffix, "route")
	}

	if SecurityPolicySuffix != "security" {
		t.Errorf("SecurityPolicySuffix = %q, want %q", SecurityPolicySuffix, "security")
	}

	if CertificateSuffix != "cert" {
		t.Errorf("CertificateSuffix = %q, want %q", CertificateSuffix, "cert")
	}

	if ClientSecretSuffix != "oidc-client" {
		t.Errorf("ClientSecretSuffix = %q, want %q", ClientSecretSuffix, "oidc-client")
	}
}
