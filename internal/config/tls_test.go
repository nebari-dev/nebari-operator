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
	"os"
	"testing"
)

func TestLoadTLSConfig(t *testing.T) {
	tests := []struct {
		name               string
		envVars            map[string]string
		expectedIssuerName string
	}{
		{
			name:               "Default values",
			envVars:            map[string]string{},
			expectedIssuerName: "",
		},
		{
			name: "Custom issuer name",
			envVars: map[string]string{
				"TLS_CLUSTER_ISSUER_NAME": "letsencrypt-prod",
			},
			expectedIssuerName: "letsencrypt-prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("TLS_CLUSTER_ISSUER_NAME")
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}
			config := LoadTLSConfig()
			if config.ClusterIssuerName != tt.expectedIssuerName {
				t.Errorf("expected ClusterIssuerName %q, got %q", tt.expectedIssuerName, config.ClusterIssuerName)
			}
		})
	}
}
