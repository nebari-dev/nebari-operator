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
	"fmt"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

// AuthConfig holds authentication configuration for the operator.
type AuthConfig struct {
	// Keycloak configuration
	Keycloak KeycloakConfig
}

// KeycloakConfig holds Keycloak-specific configuration.
type KeycloakConfig struct {
	// Enabled determines if Keycloak integration is enabled
	Enabled bool

	// URL is the internal cluster URL for Keycloak admin API
	// Example: http://keycloak.keycloak.svc.cluster.local:8080
	URL string

	// Realm is the Keycloak realm to use for OIDC clients
	Realm string

	// AdminSecretName is the name of the secret containing admin credentials
	// The secret should contain 'username' and 'password' keys
	AdminSecretName string

	// AdminSecretNamespace is the namespace where the admin secret is located
	AdminSecretNamespace string

	// AdminUsername is the admin username (if not using secret)
	AdminUsername string

	// AdminPassword is the admin password (if not using secret)
	AdminPassword string

	// Issuer URL components (used by Envoy Gateway for OIDC)
	// These configure how the issuer URL is built for SecurityPolicy

	// IssuerServiceName is the Kubernetes service name for Keycloak
	IssuerServiceName string

	// IssuerServiceNamespace is the namespace where Keycloak is deployed
	IssuerServiceNamespace string

	// IssuerServicePort is the HTTP port for the Keycloak service
	IssuerServicePort int

	// IssuerContextPath is the HTTP context path for Keycloak (e.g., "/auth")
	IssuerContextPath string
}

// LoadAuthConfig loads authentication configuration from environment variables.
func LoadAuthConfig() AuthConfig {
	return AuthConfig{
		Keycloak: KeycloakConfig{
			Enabled:              getEnvBool("KEYCLOAK_ENABLED", true),
			URL:                  getEnv("KEYCLOAK_URL", fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s", constants.DefaultKeycloakServiceName, constants.DefaultKeycloakNamespace, constants.DefaultKeycloakServicePort, constants.DefaultKeycloakContextPath)),
			Realm:                getEnv("KEYCLOAK_REALM", "nebari"),
			AdminSecretName:      getEnv("KEYCLOAK_ADMIN_SECRET_NAME", "nebari-realm-admin-credentials"),
			AdminSecretNamespace: getEnv("KEYCLOAK_ADMIN_SECRET_NAMESPACE", "keycloak"),
			AdminUsername:        getEnv("KEYCLOAK_ADMIN_USERNAME", ""),
			AdminPassword:        getEnv("KEYCLOAK_ADMIN_PASSWORD", ""),
			// Issuer URL components (for Envoy Gateway SecurityPolicy)
			IssuerServiceName:      getEnv("KEYCLOAK_ISSUER_SERVICE_NAME", constants.DefaultKeycloakServiceName),
			IssuerServiceNamespace: getEnv("KEYCLOAK_ISSUER_SERVICE_NAMESPACE", constants.DefaultKeycloakNamespace),
			IssuerServicePort:      getEnvInt("KEYCLOAK_ISSUER_SERVICE_PORT", constants.DefaultKeycloakServicePort),
			IssuerContextPath:      getEnv("KEYCLOAK_ISSUER_CONTEXT_PATH", constants.DefaultKeycloakContextPath),
		},
	}
}

// LoadKeycloakCredentials loads Keycloak admin credentials from a secret or environment variables.
// Priority: Secret > Environment Variables
func (c *KeycloakConfig) LoadKeycloakCredentials(ctx context.Context, k8sClient client.Client) error {
	// If secret name is provided, try to load from secret
	if c.AdminSecretName != "" && c.AdminSecretNamespace != "" {
		secret := &corev1.Secret{}
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      c.AdminSecretName,
			Namespace: c.AdminSecretNamespace,
		}, secret)

		if err != nil {
			// If secret not found, fall back to environment variables if they're set
			if c.AdminUsername != "" && c.AdminPassword != "" {
				// Already have credentials from environment, continue
				return nil
			}
			return fmt.Errorf("failed to get Keycloak admin secret %s/%s: %w", c.AdminSecretNamespace, c.AdminSecretName, err)
		}

		// Read username (try both key formats)
		usernameFound := false
		if username, ok := secret.Data["username"]; ok {
			c.AdminUsername = string(username)
			usernameFound = true
		} else if username, ok := secret.Data["admin-username"]; ok {
			c.AdminUsername = string(username)
			usernameFound = true
		}

		if !usernameFound {
			return fmt.Errorf("keycloak admin secret %s/%s missing 'username' or 'admin-username' key", c.AdminSecretNamespace, c.AdminSecretName)
		}

		// Read password (required)
		passwordFound := false
		if password, ok := secret.Data["password"]; ok {
			c.AdminPassword = string(password)
			passwordFound = true
		} else if password, ok := secret.Data["admin-password"]; ok {
			c.AdminPassword = string(password)
			passwordFound = true
		}

		if !passwordFound {
			return fmt.Errorf("keycloak admin secret %s/%s missing 'password' or 'admin-password' key", c.AdminSecretNamespace, c.AdminSecretName)
		}
	}

	// Validate that we have credentials
	if c.AdminUsername == "" || c.AdminPassword == "" {
		return fmt.Errorf("keycloak admin credentials not configured. Set KEYCLOAK_ADMIN_SECRET_NAME or KEYCLOAK_ADMIN_USERNAME/PASSWORD")
	}

	return nil
}

// getEnv gets an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool gets a boolean environment variable or returns a default value.
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1" || value == "yes"
	}
	return defaultValue
}

// getEnvInt gets an integer environment variable or returns a default value.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
