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

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

// OIDCProvider defines the interface for OIDC provider implementations.
// Each provider (Keycloak, generic OIDC, etc.) must implement this interface.
type OIDCProvider interface {
	// GetIssuerURL returns the OIDC issuer URL for this provider.
	// For Keycloak, this constructs the realm-specific URL.
	// For generic OIDC, this returns the configured issuer URL.
	// The URL should be accessible from within the cluster (internal DNS).
	GetIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error)

	// GetClientID returns the OIDC client ID for the application.
	// This is typically derived from the NebariApp name/namespace.
	GetClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string

	// ProvisionClient provisions an OIDC client in the provider if supported.
	// Returns nil if provisioning is not supported or not needed.
	// The client secret should be stored in a Kubernetes Secret.
	ProvisionClient(ctx context.Context, nebariApp *appsv1.NebariApp) error

	// DeleteClient removes the OIDC client from the provider if it was provisioned.
	// Returns nil if the client doesn't exist or deletion is not applicable.
	DeleteClient(ctx context.Context, nebariApp *appsv1.NebariApp) error

	// SupportsProvisioning returns true if this provider supports automatic client provisioning.
	SupportsProvisioning() bool
}
