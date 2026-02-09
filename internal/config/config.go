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

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Config holds all operator configuration
type Config struct {
	Auth AuthConfig
}

// LoadConfig loads all configuration from environment variables and secrets
func LoadConfig(ctx context.Context, k8sClient client.Client) (*Config, error) {
	// Load auth configuration
	authConfig := LoadAuthConfig()

	// Load credentials from secrets if configured
	if authConfig.Keycloak.Enabled {
		if err := authConfig.Keycloak.LoadKeycloakCredentials(ctx, k8sClient); err != nil {
			return nil, err
		}
	}

	return &Config{
		Auth: authConfig,
	}, nil
}
