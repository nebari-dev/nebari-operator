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
	"fmt"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

// GenericOIDCProvider implements the OIDCProvider interface for generic OIDC providers.
// This provider supports any OIDC-compliant identity provider (Google, Azure AD, Okta, etc.)
// but does not support automatic client provisioning.
type GenericOIDCProvider struct{}

// GetIssuerURL returns the configured issuer URL from the NebariApp spec.
func (p *GenericOIDCProvider) GetIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error) {
	if nebariApp.Spec.Auth == nil || nebariApp.Spec.Auth.IssuerURL == "" {
		return "", fmt.Errorf("issuerURL is required for generic-oidc provider")
	}
	return nebariApp.Spec.Auth.IssuerURL, nil
}

// GetClientID returns the OIDC client ID based on the NebariApp name.
func (p *GenericOIDCProvider) GetClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string {
	return naming.ClientID(nebariApp)
}

// SupportsProvisioning returns false as generic OIDC providers don't support automatic provisioning.
func (p *GenericOIDCProvider) SupportsProvisioning() bool {
	return false
}

// ProvisionClient always returns an error as generic OIDC doesn't support provisioning.
func (p *GenericOIDCProvider) ProvisionClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	return fmt.Errorf("generic-oidc provider does not support automatic client provisioning")
}

// DeleteClient is a no-op for generic OIDC providers.
func (p *GenericOIDCProvider) DeleteClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	// No-op: generic OIDC clients are managed externally
	return nil
}
