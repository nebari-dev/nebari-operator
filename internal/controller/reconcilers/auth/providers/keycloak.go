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
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/Nerzal/gocloak/v13"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// KeycloakConfig contains configuration for the Keycloak OIDC provider.
type KeycloakConfig struct {
	// URL is the HTTP URL to access Keycloak (internal cluster DNS).
	// Example: http://keycloak.keycloak.svc.cluster.local:8080
	URL string

	// Realm is the Keycloak realm to use for OIDC clients.
	Realm string

	// AdminSecretName is the name of the secret containing Keycloak admin credentials.
	AdminSecretName string

	// AdminSecretNamespace is the namespace of the admin secret.
	AdminSecretNamespace string

	// AdminUsername is the Keycloak admin username (loaded from secret).
	AdminUsername string

	// AdminPassword is the Keycloak admin password (loaded from secret).
	AdminPassword string
}

// KeycloakProvider implements the OIDCProvider interface for Keycloak.
type KeycloakProvider struct {
	Client client.Client
	Config KeycloakConfig
}

// GetIssuerURL returns the internal cluster URL for the Keycloak realm.
// Envoy uses this to fetch OIDC configuration from within the cluster.
func (p *KeycloakProvider) GetIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error) {
	realm := p.Config.Realm
	// Use internal cluster DNS for Envoy to fetch OIDC config
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d/realms/%s",
		constants.DefaultKeycloakServiceName,
		constants.DefaultKeycloakNamespace,
		constants.DefaultKeycloakServicePort,
		realm), nil
}

// GetClientID returns the OIDC client ID for the NebariApp.
func (p *KeycloakProvider) GetClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string {
	return naming.ClientID(nebariApp)
}

// SupportsProvisioning returns true as Keycloak supports automatic client provisioning.
func (p *KeycloakProvider) SupportsProvisioning() bool {
	return true
}

// loadCredentials loads admin credentials from the Kubernetes secret if not already loaded.
func (p *KeycloakProvider) loadCredentials(ctx context.Context) error {
	// If credentials are already loaded, skip
	if p.Config.AdminUsername != "" && p.Config.AdminPassword != "" {
		return nil
	}

	logger := log.FromContext(ctx)

	// Load credentials from secret
	secret := &corev1.Secret{}
	err := p.Client.Get(ctx, types.NamespacedName{
		Name:      p.Config.AdminSecretName,
		Namespace: p.Config.AdminSecretNamespace,
	}, secret)
	if err != nil {
		return fmt.Errorf("failed to get Keycloak admin secret %s/%s: %w",
			p.Config.AdminSecretNamespace, p.Config.AdminSecretName, err)
	}

	// Extract credentials from secret (support both key formats)
	var username, password []byte
	var ok bool

	// Try 'username' first, then 'admin-username'
	username, ok = secret.Data["username"]
	if !ok {
		username, ok = secret.Data["admin-username"]
		if !ok {
			return fmt.Errorf("secret %s/%s missing 'username' or 'admin-username' field",
				p.Config.AdminSecretNamespace, p.Config.AdminSecretName)
		}
	}

	// Try 'password' first, then 'admin-password'
	password, ok = secret.Data["password"]
	if !ok {
		password, ok = secret.Data["admin-password"]
		if !ok {
			return fmt.Errorf("secret %s/%s missing 'password' or 'admin-password' field",
				p.Config.AdminSecretNamespace, p.Config.AdminSecretName)
		}
	}

	p.Config.AdminUsername = string(username)
	p.Config.AdminPassword = string(password)

	logger.Info("Loaded Keycloak admin credentials from secret",
		"secretName", p.Config.AdminSecretName,
		"secretNamespace", p.Config.AdminSecretNamespace)

	return nil
}

// ProvisionClient creates or updates a Keycloak OIDC client for the NebariApp.
func (p *KeycloakProvider) ProvisionClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)
	clientID := p.GetClientID(ctx, nebariApp)

	// Load admin credentials from secret if not already loaded
	if err := p.loadCredentials(ctx); err != nil {
		return fmt.Errorf("failed to load Keycloak credentials: %w", err)
	}

	// Authenticate to Keycloak
	kcClient, token, err := p.authenticate(ctx)
	if err != nil {
		return err
	}

	// Check if client exists
	existingClient, err := p.findClient(ctx, kcClient, token, clientID)
	if err != nil {
		return err
	}

	var clientSecret string
	if existingClient != nil {
		// Client exists - retrieve existing secret and update configuration
		clientSecret, err = p.updateExistingClient(ctx, kcClient, token, existingClient, nebariApp)
		if err != nil {
			return err
		}
		logger.Info("Updated existing client", "clientID", clientID)
	} else {
		// Client doesn't exist - create new one
		clientSecret, err = p.createNewClient(ctx, kcClient, token, clientID, nebariApp)
		if err != nil {
			return err
		}
		logger.Info("Created new client", "clientID", clientID)
	}

	// Store secret in Kubernetes
	return p.storeClientSecret(ctx, nebariApp, clientSecret)
}

// DeleteClient removes the Keycloak OIDC client.
func (p *KeycloakProvider) DeleteClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	clientID := p.GetClientID(ctx, nebariApp)

	// Load admin credentials from secret if not already loaded
	if err := p.loadCredentials(ctx); err != nil {
		return fmt.Errorf("failed to load Keycloak credentials: %w", err)
	}

	// Authenticate to Keycloak
	kcClient, token, err := p.authenticate(ctx)
	if err != nil {
		return err
	}

	// Find the client
	existingClient, err := p.findClient(ctx, kcClient, token, clientID)
	if err != nil {
		return err
	}

	if existingClient == nil {
		// Client doesn't exist, nothing to delete
		return nil
	}

	// Delete the client
	err = kcClient.DeleteClient(ctx, token.AccessToken, p.Config.Realm, *existingClient.ID)
	if err != nil {
		return fmt.Errorf("failed to delete client: %w", err)
	}

	return nil
}

// authenticate creates a Keycloak client and obtains an admin token.
func (p *KeycloakProvider) authenticate(ctx context.Context) (*gocloak.GoCloak, *gocloak.JWT, error) {
	kcClient := gocloak.NewClient(p.Config.URL)
	token, err := kcClient.LoginAdmin(ctx, p.Config.AdminUsername, p.Config.AdminPassword, "master")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to authenticate to Keycloak: %w", err)
	}
	return kcClient, token, nil
}

// findClient looks up a client by clientID, returns nil if not found.
func (p *KeycloakProvider) findClient(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, clientID string) (*gocloak.Client, error) {
	clients, err := kcClient.GetClients(ctx, token.AccessToken, p.Config.Realm, gocloak.GetClientsParams{
		ClientID: &clientID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query clients: %w", err)
	}

	if len(clients) == 0 {
		return nil, nil
	}
	return clients[0], nil
}

// updateExistingClient updates an existing client's configuration and returns its secret.
func (p *KeycloakProvider) updateExistingClient(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, existingClient *gocloak.Client, nebariApp *appsv1.NebariApp) (string, error) {
	// Get existing client secret
	secretResp, err := kcClient.GetClientSecret(ctx, token.AccessToken, p.Config.Realm, *existingClient.ID)
	if err != nil {
		return "", fmt.Errorf("failed to get client secret: %w", err)
	}

	// Update client configuration
	redirectURIs := p.buildRedirectURLs(nebariApp)
	existingClient.RedirectURIs = &redirectURIs
	existingClient.WebOrigins = &[]string{"*"}
	existingClient.StandardFlowEnabled = gocloak.BoolP(true)

	err = kcClient.UpdateClient(ctx, token.AccessToken, p.Config.Realm, *existingClient)
	if err != nil {
		return "", fmt.Errorf("failed to update client: %w", err)
	}

	return *secretResp.Value, nil
}

// createNewClient creates a new Keycloak client and returns its secret.
func (p *KeycloakProvider) createNewClient(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, clientID string, nebariApp *appsv1.NebariApp) (string, error) {
	// Generate client secret
	clientSecret, err := generateSecret(32)
	if err != nil {
		return "", fmt.Errorf("failed to generate secret: %w", err)
	}

	// Build redirect URLs
	redirectURIs := p.buildRedirectURLs(nebariApp)

	// Create client
	newClient := gocloak.Client{
		ClientID:                  gocloak.StringP(clientID),
		Name:                      gocloak.StringP(fmt.Sprintf("%s OIDC Client", nebariApp.Name)),
		Secret:                    gocloak.StringP(clientSecret),
		RedirectURIs:              &redirectURIs,
		WebOrigins:                &[]string{"*"},
		PublicClient:              gocloak.BoolP(false),
		StandardFlowEnabled:       gocloak.BoolP(true),
		DirectAccessGrantsEnabled: gocloak.BoolP(false),
		Protocol:                  gocloak.StringP("openid-connect"),
		Enabled:                   gocloak.BoolP(true),
	}

	_, err = kcClient.CreateClient(ctx, token.AccessToken, p.Config.Realm, newClient)
	if err != nil {
		return "", fmt.Errorf("failed to create client: %w", err)
	}

	return clientSecret, nil
}

// buildRedirectURLs constructs the OAuth2 redirect URLs for the client.
func (p *KeycloakProvider) buildRedirectURLs(nebariApp *appsv1.NebariApp) []string {
	redirectPath := constants.DefaultOAuthCallbackPath
	if nebariApp.Spec.Auth != nil && nebariApp.Spec.Auth.RedirectURI != "" {
		redirectPath = nebariApp.Spec.Auth.RedirectURI
	}

	return []string{
		fmt.Sprintf("https://%s%s", nebariApp.Spec.Hostname, redirectPath),
		fmt.Sprintf("http://%s%s", nebariApp.Spec.Hostname, redirectPath),
	}
}

// storeClientSecret creates or updates the Kubernetes secret containing the OIDC client secret.
func (p *KeycloakProvider) storeClientSecret(ctx context.Context, nebariApp *appsv1.NebariApp, clientSecret string) error {
	secretName := naming.ClientSecretName(nebariApp)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: nebariApp.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "nebariapp",
				"app.kubernetes.io/instance":   nebariApp.Name,
				"app.kubernetes.io/managed-by": "nebari-operator",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			constants.ClientSecretKey: []byte(clientSecret),
		},
	}

	// Check if secret exists
	existingSecret := &corev1.Secret{}
	err := p.Client.Get(ctx, types.NamespacedName{Name: secretName, Namespace: nebariApp.Namespace}, existingSecret)

	if apierrors.IsNotFound(err) {
		// Create new secret
		return p.Client.Create(ctx, secret)
	} else if err != nil {
		return fmt.Errorf("failed to check for existing secret: %w", err)
	}

	// Update existing secret
	existingSecret.Data = secret.Data
	return p.Client.Update(ctx, existingSecret)
}

// generateSecret generates a random secret string of the specified length.
func generateSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length], nil
}
