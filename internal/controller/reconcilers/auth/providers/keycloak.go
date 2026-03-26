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
	"net/http"
	"strings"
	"time"

	"github.com/Nerzal/gocloak/v13"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/config"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// KeycloakProvider implements the OIDCProvider interface for Keycloak.
type KeycloakProvider struct {
	Client client.Client
	Config config.KeycloakConfig
}

// GetIssuerURL returns the internal cluster URL for the Keycloak realm.
// Envoy uses this to fetch OIDC configuration from within the cluster.
func (p *KeycloakProvider) GetIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error) {
	realm := p.Config.Realm
	// Use internal cluster DNS for Envoy to fetch OIDC config
	// All components are now configurable via environment variables
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d%s/realms/%s",
		p.Config.IssuerServiceName,
		p.Config.IssuerServiceNamespace,
		p.Config.IssuerServicePort,
		p.Config.IssuerContextPath,
		realm), nil
}

// GetExternalIssuerURL returns the publicly routable Keycloak issuer URL.
func (p *KeycloakProvider) GetExternalIssuerURL(ctx context.Context, nebariApp *appsv1.NebariApp) (string, error) {
	if p.Config.ExternalURL == "" {
		return "", fmt.Errorf("KEYCLOAK_EXTERNAL_URL not configured; required for external issuer URL")
	}
	return fmt.Sprintf("%s/realms/%s", strings.TrimRight(p.Config.ExternalURL, "/"), p.Config.Realm), nil
}

// GetClientID returns the OIDC client ID for the NebariApp.
func (p *KeycloakProvider) GetClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string {
	return naming.ClientID(nebariApp)
}

// GetSPAClientID returns the SPA client ID for the NebariApp.
// The SPA client ID can be customized via spec.auth.spaClient.clientId,
// otherwise defaults to the standard client ID with "-spa" suffix.
func (p *KeycloakProvider) GetSPAClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string {
	if nebariApp.Spec.Auth != nil && nebariApp.Spec.Auth.SPAClient != nil && nebariApp.Spec.Auth.SPAClient.ClientID != "" {
		return nebariApp.Spec.Auth.SPAClient.ClientID
	}
	return naming.ClientID(nebariApp) + "-spa"
}

// SupportsProvisioning returns true as Keycloak supports automatic client provisioning.
func (p *KeycloakProvider) SupportsProvisioning() bool {
	return true
}

// loadCredentials loads admin credentials from the Kubernetes secret.
// When AdminSecretName is configured, credentials are always read fresh from the
// secret to support secret rotation without pod restarts.
// When no secret is configured, falls back to direct credentials (env vars).
func (p *KeycloakProvider) loadCredentials(ctx context.Context) error {
	// If no secret is configured, use direct credentials from config (env vars)
	if p.Config.AdminSecretName == "" {
		if p.Config.AdminUsername == "" || p.Config.AdminPassword == "" {
			return fmt.Errorf("keycloak admin credentials not configured: set KEYCLOAK_ADMIN_SECRET_NAME or KEYCLOAK_ADMIN_USERNAME/PASSWORD")
		}
		return nil
	}

	logger := log.FromContext(ctx)

	// Always read fresh from the secret to support rotation
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
	ctx, cancel := p.withAPITimeout(ctx)
	defer cancel()

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
	var clientInternalID string
	if existingClient != nil {
		// Client exists - retrieve existing secret and update configuration
		clientSecret, clientInternalID, err = p.updateExistingClient(ctx, kcClient, token, existingClient, nebariApp)
		if err != nil {
			return err
		}
		logger.Info("Updated existing client", "clientID", clientID)
	} else {
		// Client doesn't exist - create new one
		clientSecret, clientInternalID, err = p.createNewClient(ctx, kcClient, token, clientID, nebariApp)
		if err != nil {
			return err
		}
		logger.Info("Created new client", "clientID", clientID)
	}

	// Sync requested OIDC scopes to the client
	if err := p.syncClientScopes(ctx, kcClient, token, clientInternalID, nebariApp); err != nil {
		return fmt.Errorf("failed to sync client scopes: %w", err)
	}

	// Sync client-level protocol mappers (isolated per-client, not on shared scopes)
	if err := p.syncClientProtocolMappers(ctx, kcClient, token, clientInternalID, nebariApp); err != nil {
		return fmt.Errorf("failed to sync client protocol mappers: %w", err)
	}

	// Sync Keycloak groups and member assignments
	if err := p.syncGroups(ctx, kcClient, token, nebariApp); err != nil {
		return fmt.Errorf("failed to sync groups: %w", err)
	}

	// Provision SPA client if requested
	var spaClientID string
	if p.shouldProvisionSPAClient(nebariApp) {
		spaClientID, err = p.provisionSPAClient(ctx, kcClient, token, nebariApp)
		if err != nil {
			return fmt.Errorf("failed to provision SPA client: %w", err)
		}
		logger.Info("Provisioned SPA client", "clientID", spaClientID)
	}

	// Provision device flow client if requested
	var deviceClientID string
	if p.shouldProvisionDeviceFlowClient(nebariApp) {
		deviceClientID, err = p.provisionDeviceFlowClient(ctx, kcClient, token, nebariApp)
		if err != nil {
			return fmt.Errorf("failed to provision device flow client: %w", err)
		}
		logger.Info("Provisioned device flow client", "clientID", deviceClientID)
	}

	// Get external issuer URL for the Secret (only needed when external consumers exist)
	var externalIssuerURL string
	if p.shouldProvisionDeviceFlowClient(nebariApp) || p.shouldProvisionSPAClient(nebariApp) {
		externalIssuerURL, err = p.GetExternalIssuerURL(ctx, nebariApp)
		if err != nil {
			return fmt.Errorf("failed to get external issuer URL (KEYCLOAK_EXTERNAL_URL must be set when deviceFlowClient or spaClient is enabled): %w", err)
		}
	}

	// Store all credentials in Kubernetes Secret
	return p.storeClientSecret(ctx, nebariApp, clientID, clientSecret, externalIssuerURL, spaClientID, deviceClientID)
}

// ConfigureTokenExchange enables OAuth 2.0 Token Exchange (RFC 8693) on this client.
// It enables authorization services on the client, then creates a client policy and
// token-exchange permission for each peer client that should be allowed to exchange.
func (p *KeycloakProvider) ConfigureTokenExchange(ctx context.Context, nebariApp *appsv1.NebariApp, peerClientIDs []string) error {
	ctx, cancel := p.withAPITimeout(ctx)
	defer cancel()

	logger := log.FromContext(ctx)
	clientID := p.GetClientID(ctx, nebariApp)

	if err := p.loadCredentials(ctx); err != nil {
		return fmt.Errorf("failed to load Keycloak credentials: %w", err)
	}

	kcClient, token, err := p.authenticate(ctx)
	if err != nil {
		return err
	}

	// Find this client's internal UUID
	existingClient, err := p.findClient(ctx, kcClient, token, clientID)
	if err != nil {
		return err
	}
	if existingClient == nil {
		return fmt.Errorf("client %s not found, provision it first", clientID)
	}
	internalID := gocloak.PString(existingClient.ID)

	// Enable authorization services and service accounts on this client
	existingClient.AuthorizationServicesEnabled = gocloak.BoolP(true)
	existingClient.ServiceAccountsEnabled = gocloak.BoolP(true)
	if err := kcClient.UpdateClient(ctx, token.AccessToken, p.Config.Realm, *existingClient); err != nil {
		return fmt.Errorf("failed to enable authorization services: %w", err)
	}
	logger.Info("Enabled authorization services", "clientID", clientID)

	// Look up the "token-exchange" scope on the authorization resource server.
	// Keycloak creates this scope automatically when authorization services are enabled.
	scopes, err := kcClient.GetScopes(ctx, token.AccessToken, p.Config.Realm, internalID, gocloak.GetScopeParams{
		Name: gocloak.StringP("token-exchange"),
	})
	if err != nil {
		return fmt.Errorf("failed to list authorization scopes: %w", err)
	}

	var tokenExchangeScopeID string
	for _, s := range scopes {
		if gocloak.PString(s.Name) == "token-exchange" {
			tokenExchangeScopeID = gocloak.PString(s.ID)
			break
		}
	}

	// Create the scope if it doesn't exist
	if tokenExchangeScopeID == "" {
		scope, err := kcClient.CreateScope(ctx, token.AccessToken, p.Config.Realm, internalID, gocloak.ScopeRepresentation{
			Name: gocloak.StringP("token-exchange"),
		})
		if err != nil {
			return fmt.Errorf("failed to create token-exchange scope: %w", err)
		}
		tokenExchangeScopeID = gocloak.PString(scope.ID)
		logger.Info("Created token-exchange scope", "id", tokenExchangeScopeID)
	}

	// For each peer client, create a client policy and token-exchange permission
	for _, peerID := range peerClientIDs {
		policyName := fmt.Sprintf("%s-token-exchange", peerID)

		// Create client policy allowing this peer
		policy := gocloak.PolicyRepresentation{
			Name:  gocloak.StringP(policyName),
			Type:  gocloak.StringP("client"),
			Logic: gocloak.POSITIVE,
			ClientPolicyRepresentation: gocloak.ClientPolicyRepresentation{
				Clients: &[]string{peerID},
			},
		}
		createdPolicy, err := kcClient.CreatePolicy(ctx, token.AccessToken, p.Config.Realm, internalID, policy)
		var policyID string
		if err != nil {
			if !strings.Contains(err.Error(), "409") {
				return fmt.Errorf("failed to create token-exchange policy for peer %s: %w", peerID, err)
			}
			// Policy exists — look up its ID
			logger.Info("Token exchange policy already exists", "peer", peerID)
			policies, lookupErr := kcClient.GetPolicies(ctx, token.AccessToken, p.Config.Realm, internalID, gocloak.GetPolicyParams{
				Name: gocloak.StringP(policyName),
			})
			if lookupErr != nil || len(policies) == 0 {
				return fmt.Errorf("failed to look up existing policy %s: %w", policyName, lookupErr)
			}
			policyID = gocloak.PString(policies[0].ID)
		} else {
			policyID = gocloak.PString(createdPolicy.ID)
			logger.Info("Created token exchange policy", "peer", peerID, "policyID", policyID)
		}

		// Create the token-exchange scoped permission via raw HTTP.
		// gocloak's CreatePermission doesn't properly link scopes/policies in the
		// request body. Keycloak expects plain string arrays of IDs.
		permName := fmt.Sprintf("%s-token-exchange-permission", peerID)
		permBody := fmt.Sprintf(
			`{"name":"%s","type":"scope","decisionStrategy":"AFFIRMATIVE","scopes":["%s"],"policies":["%s"]}`,
			permName, tokenExchangeScopeID, policyID,
		)
		permURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s/authz/resource-server/permission/scope",
			p.Config.URL, p.Config.Realm, internalID)
		req, reqErr := http.NewRequestWithContext(ctx, "POST", permURL, strings.NewReader(permBody))
		if reqErr != nil {
			return fmt.Errorf("failed to build permission request: %w", reqErr)
		}
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to create token-exchange permission for peer %s: %w", peerID, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
			if resp.StatusCode == http.StatusConflict {
				logger.Info("Token exchange permission already exists", "peer", peerID)
			} else {
				logger.Info("Created token exchange permission", "peer", peerID)
			}
		} else {
			return fmt.Errorf("failed to create token-exchange permission for peer %s: HTTP %d", peerID, resp.StatusCode)
		}
	}

	return nil
}

// DeleteClient removes the Keycloak OIDC client.
func (p *KeycloakProvider) DeleteClient(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	ctx, cancel := p.withAPITimeout(ctx)
	defer cancel()

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

	// Delete the confidential client
	existingClient, err := p.findClient(ctx, kcClient, token, clientID)
	if err != nil {
		return err
	}

	if existingClient != nil {
		err = kcClient.DeleteClient(ctx, token.AccessToken, p.Config.Realm, *existingClient.ID)
		if err != nil {
			return fmt.Errorf("failed to delete client: %w", err)
		}
		logger.Info("Deleted confidential client", "clientID", clientID)
	}

	// Delete SPA client if it exists
	if p.shouldProvisionSPAClient(nebariApp) {
		spaClientID := p.GetSPAClientID(ctx, nebariApp)
		existingSPAClient, err := p.findClient(ctx, kcClient, token, spaClientID)
		if err != nil {
			return err
		}

		if existingSPAClient != nil {
			err = kcClient.DeleteClient(ctx, token.AccessToken, p.Config.Realm, *existingSPAClient.ID)
			if err != nil {
				return fmt.Errorf("failed to delete SPA client: %w", err)
			}
			logger.Info("Deleted SPA client", "clientID", spaClientID)
		}
	}

	// Delete device flow client if it was provisioned
	// Always attempt cleanup (don't gate on shouldProvisionDeviceFlowClient)
	// so that disabling device flow in the spec also cleans up the Keycloak client.
	deviceFlowClientID := p.GetDeviceFlowClientID(ctx, nebariApp)
	existingDeviceClient, err := p.findClient(ctx, kcClient, token, deviceFlowClientID)
	if err != nil {
		return err
	}
	if existingDeviceClient != nil {
		err = kcClient.DeleteClient(ctx, token.AccessToken, p.Config.Realm, *existingDeviceClient.ID)
		if err != nil {
			return fmt.Errorf("failed to delete device flow client: %w", err)
		}
		logger.Info("Deleted device flow client", "clientID", deviceFlowClientID)
	}

	return nil
}

// defaultAPITimeout is used when APITimeout is not configured.
const defaultAPITimeout = 30 * time.Second

// withAPITimeout returns a context with the configured API timeout applied.
// If the context already has an earlier deadline, it is preserved.
func (p *KeycloakProvider) withAPITimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := p.Config.APITimeout
	if timeout <= 0 {
		timeout = defaultAPITimeout
	}
	return context.WithTimeout(ctx, timeout)
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

// updateExistingClient updates an existing client's configuration and returns its secret and internal ID.
func (p *KeycloakProvider) updateExistingClient(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, existingClient *gocloak.Client, nebariApp *appsv1.NebariApp) (string, string, error) {
	// Get existing client secret
	secretResp, err := kcClient.GetClientSecret(ctx, token.AccessToken, p.Config.Realm, *existingClient.ID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get client secret: %w", err)
	}

	// Update client configuration
	redirectURIs := p.buildRedirectURLs(nebariApp)
	existingClient.RedirectURIs = &redirectURIs
	existingClient.WebOrigins = &[]string{"*"}
	existingClient.StandardFlowEnabled = gocloak.BoolP(true)

	err = kcClient.UpdateClient(ctx, token.AccessToken, p.Config.Realm, *existingClient)
	if err != nil {
		return "", "", fmt.Errorf("failed to update client: %w", err)
	}

	return *secretResp.Value, *existingClient.ID, nil
}

// createNewClient creates a new Keycloak client and returns its secret and internal ID.
func (p *KeycloakProvider) createNewClient(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, clientID string, nebariApp *appsv1.NebariApp) (string, string, error) {
	// Generate client secret
	clientSecret, err := generateSecret(32)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate secret: %w", err)
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

	internalID, err := kcClient.CreateClient(ctx, token.AccessToken, p.Config.Realm, newClient)
	if err != nil {
		return "", "", fmt.Errorf("failed to create client: %w", err)
	}

	return clientSecret, internalID, nil
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

// storeClientSecret creates or updates the Kubernetes secret containing the OIDC client credentials.
// Optional fields (spaClientID, deviceClientID) are only written when non-empty.
func (p *KeycloakProvider) storeClientSecret(ctx context.Context, nebariApp *appsv1.NebariApp, clientID, clientSecret, externalIssuerURL, spaClientID, deviceClientID string) error {
	secretName := naming.ClientSecretName(nebariApp)

	secretData := map[string][]byte{
		constants.ClientIDKey:     []byte(clientID),
		constants.ClientSecretKey: []byte(clientSecret),
		constants.IssuerURLKey:    []byte(externalIssuerURL),
	}

	// Add SPA client ID if present
	if spaClientID != "" {
		secretData[constants.SPAClientIDKey] = []byte(spaClientID)
	}

	// Add device flow client ID if present
	if deviceClientID != "" {
		secretData[constants.DeviceClientIDKey] = []byte(deviceClientID)
	}

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
		Data: secretData,
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

// syncClientScopes ensures that the OIDC scopes requested by the NebariApp
// exist in the Keycloak realm and are assigned as default scopes to the client.
func (p *KeycloakProvider) syncClientScopes(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, clientInternalID string, nebariApp *appsv1.NebariApp) error {
	if nebariApp.Spec.Auth == nil || len(nebariApp.Spec.Auth.Scopes) == 0 {
		return nil
	}

	logger := log.FromContext(ctx)

	// Get all existing client scopes in the realm
	realmScopes, err := kcClient.GetClientScopes(ctx, token.AccessToken, p.Config.Realm)
	if err != nil {
		return fmt.Errorf("failed to get realm client scopes: %w", err)
	}

	// Build name→scope lookup
	scopesByName := make(map[string]*gocloak.ClientScope, len(realmScopes))
	for _, s := range realmScopes {
		if s.Name != nil {
			scopesByName[*s.Name] = s
		}
	}

	// Get scopes already assigned as defaults on this client
	currentDefaults, err := kcClient.GetClientsDefaultScopes(ctx, token.AccessToken, p.Config.Realm, clientInternalID)
	if err != nil {
		return fmt.Errorf("failed to get client default scopes: %w", err)
	}

	assignedIDs := make(map[string]bool, len(currentDefaults))
	for _, s := range currentDefaults {
		if s.ID != nil {
			assignedIDs[*s.ID] = true
		}
	}

	for _, scopeName := range nebariApp.Spec.Auth.Scopes {
		// "openid" is always implicit in OIDC - skip it
		if scopeName == "openid" {
			continue
		}

		var scopeID string

		if existing, ok := scopesByName[scopeName]; ok {
			scopeID = *existing.ID
		} else {
			// Create the scope in the realm
			includeInToken := "true"
			newScope := gocloak.ClientScope{
				Name:        gocloak.StringP(scopeName),
				Description: gocloak.StringP("Managed by nebari-operator"),
				Protocol:    gocloak.StringP("openid-connect"),
				ClientScopeAttributes: &gocloak.ClientScopeAttributes{
					IncludeInTokenScope: &includeInToken,
				},
			}
			scopeID, err = kcClient.CreateClientScope(ctx, token.AccessToken, p.Config.Realm, newScope)
			if err != nil {
				return fmt.Errorf("failed to create client scope %q: %w", scopeName, err)
			}
			logger.Info("Created client scope in realm", "scope", scopeName, "id", scopeID)
		}

		// Note: Protocol mappers are applied at client level (not scope level)
		// via syncClientProtocolMappers, so each NebariApp is isolated.

		// Assign as default scope to the client if not already assigned
		if !assignedIDs[scopeID] {
			if err := kcClient.AddDefaultScopeToClient(ctx, token.AccessToken, p.Config.Realm, clientInternalID, scopeID); err != nil {
				return fmt.Errorf("failed to add default scope %q to client: %w", scopeName, err)
			}
			logger.Info("Assigned default scope to client", "scope", scopeName)
		}
	}

	return nil
}

// syncClientProtocolMappers ensures protocol mappers are configured directly on the
// OIDC client (not on shared client scopes). This gives each NebariApp isolated
// mapper configuration.
//
// If keycloakConfig.protocolMappers is specified, those mappers are used.
// Otherwise, if "groups" is in the requested scopes, a default group-membership
// mapper is created with full.path=false.
func (p *KeycloakProvider) syncClientProtocolMappers(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, clientInternalID string, nebariApp *appsv1.NebariApp) error {
	if nebariApp.Spec.Auth == nil {
		return nil
	}

	logger := log.FromContext(ctx)

	// Determine desired mappers
	var desiredMappers []appsv1.KeycloakProtocolMapperConfig

	if nebariApp.Spec.Auth.KeycloakConfig != nil && len(nebariApp.Spec.Auth.KeycloakConfig.ProtocolMappers) > 0 {
		desiredMappers = nebariApp.Spec.Auth.KeycloakConfig.ProtocolMappers
	} else if hasScope(nebariApp, "groups") {
		// Default: group-membership mapper with full.path=false
		desiredMappers = []appsv1.KeycloakProtocolMapperConfig{
			{
				Name:           "group-membership",
				ProtocolMapper: "oidc-group-membership-mapper",
				Config: map[string]string{
					"claim.name":           "groups",
					"full.path":            "false",
					"id.token.claim":       "true",
					"access.token.claim":   "true",
					"userinfo.token.claim": "true",
				},
			},
		}
	}

	if len(desiredMappers) == 0 {
		return nil
	}

	// Get current client to read existing protocol mappers
	kcClientObj, err := kcClient.GetClient(ctx, token.AccessToken, p.Config.Realm, clientInternalID)
	if err != nil {
		return fmt.Errorf("failed to get client: %w", err)
	}

	// Build lookup of existing client-level mappers by name
	existingByName := make(map[string]gocloak.ProtocolMapperRepresentation)
	if kcClientObj.ProtocolMappers != nil {
		for _, m := range *kcClientObj.ProtocolMappers {
			if m.Name != nil {
				existingByName[*m.Name] = m
			}
		}
	}

	for _, desired := range desiredMappers {
		cfg := desired.Config
		mapper := gocloak.ProtocolMapperRepresentation{
			Name:           gocloak.StringP(desired.Name),
			Protocol:       gocloak.StringP("openid-connect"),
			ProtocolMapper: gocloak.StringP(desired.ProtocolMapper),
			Config:         &cfg,
		}

		if existing, ok := existingByName[desired.Name]; ok {
			// Update existing mapper
			mapper.ID = existing.ID
			err = kcClient.UpdateClientProtocolMapper(ctx, token.AccessToken, p.Config.Realm, clientInternalID, *existing.ID, mapper)
			if err != nil {
				return fmt.Errorf("failed to update client protocol mapper %q: %w", desired.Name, err)
			}
			logger.Info("Updated client protocol mapper", "mapper", desired.Name)
		} else {
			// Create new mapper
			_, err = kcClient.CreateClientProtocolMapper(ctx, token.AccessToken, p.Config.Realm, clientInternalID, mapper)
			if err != nil {
				return fmt.Errorf("failed to create client protocol mapper %q: %w", desired.Name, err)
			}
			logger.Info("Created client protocol mapper", "mapper", desired.Name)
		}
	}

	return nil
}

// hasScope returns true if the NebariApp requests the given OIDC scope.
func hasScope(nebariApp *appsv1.NebariApp, scope string) bool {
	if nebariApp.Spec.Auth == nil {
		return false
	}
	for _, s := range nebariApp.Spec.Auth.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// syncGroups ensures that Keycloak groups exist in the realm and that
// specified users are members of those groups.
// Groups are collected from both spec.auth.groups (simple list) and
// spec.auth.keycloakConfig.groups (detailed config with members).
func (p *KeycloakProvider) syncGroups(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, nebariApp *appsv1.NebariApp) error {
	if nebariApp.Spec.Auth == nil {
		return nil
	}

	groupMembers := MergeGroupMembers(nebariApp.Spec.Auth.Groups, nebariApp.Spec.Auth.KeycloakConfig)
	if len(groupMembers) == 0 {
		return nil
	}

	logger := log.FromContext(ctx)
	realm := p.Config.Realm

	for groupName, members := range groupMembers {
		groupID, err := p.ensureGroup(ctx, kcClient, token, realm, groupName)
		if err != nil {
			return fmt.Errorf("failed to ensure group %q: %w", groupName, err)
		}

		if len(members) == 0 {
			continue
		}

		if err := p.syncGroupMembers(ctx, kcClient, token, realm, groupID, groupName, members); err != nil {
			return fmt.Errorf("failed to sync members for group %q: %w", groupName, err)
		}

		logger.Info("Synced group members", "group", groupName, "members", members)
	}

	return nil
}

// MergeGroupMembers builds a deduplicated map of group name -> members from
// spec.auth.groups and keycloakConfig.groups. When the same group name appears
// in both, keycloakConfig.groups takes precedence.
func MergeGroupMembers(groups []string, keycloakConfig *appsv1.KeycloakClientConfig) map[string][]string {
	groupMembers := make(map[string][]string)

	// Collect from spec.auth.groups (no members, just ensure groups exist)
	for _, g := range groups {
		if _, ok := groupMembers[g]; !ok {
			groupMembers[g] = nil
		}
	}

	// Collect from keycloakConfig.groups (with optional members).
	// Note: keycloakConfig.groups takes precedence - if the same group name appears
	// in both spec.auth.groups and keycloakConfig.groups, the keycloakConfig entry wins.
	if keycloakConfig != nil {
		for _, g := range keycloakConfig.Groups {
			groupMembers[g.Name] = g.Members
		}
	}

	return groupMembers
}

// ensureGroup checks if a group exists in the realm and creates it if missing.
// Returns the group's Keycloak ID.
func (p *KeycloakProvider) ensureGroup(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, realm, groupName string) (string, error) {
	logger := log.FromContext(ctx)

	// Search for existing group by exact name
	groups, err := kcClient.GetGroups(ctx, token.AccessToken, realm, gocloak.GetGroupsParams{
		Search: &groupName,
		Exact:  gocloak.BoolP(true),
	})
	if err != nil {
		return "", fmt.Errorf("failed to search for group %q: %w", groupName, err)
	}

	for _, g := range groups {
		if g.Name != nil && *g.Name == groupName {
			return *g.ID, nil
		}
	}

	// Group doesn't exist - create it
	newGroup := gocloak.Group{
		Name: gocloak.StringP(groupName),
	}
	groupID, err := kcClient.CreateGroup(ctx, token.AccessToken, realm, newGroup)
	if err != nil {
		return "", fmt.Errorf("failed to create group %q: %w", groupName, err)
	}

	logger.Info("Created Keycloak group", "group", groupName, "id", groupID)
	return groupID, nil
}

// syncGroupMembers ensures the specified users are members of the given group.
// This function is additive-only: it adds missing users to the group but does not
// remove users who are in Keycloak but not in the members list. Manual removal
// in Keycloak is required if full membership sync is desired.
// Users that don't exist in Keycloak are logged as warnings but don't cause errors.
func (p *KeycloakProvider) syncGroupMembers(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, realm, groupID, groupName string, members []string) error {
	logger := log.FromContext(ctx)

	// Get current group members with explicit max to avoid truncation from Keycloak's default page size
	currentMembers, err := kcClient.GetGroupMembers(ctx, token.AccessToken, realm, groupID, gocloak.GetGroupsParams{
		Max: gocloak.IntP(1000),
	})
	if err != nil {
		return fmt.Errorf("failed to get group members: %w", err)
	}

	// Build set of current member usernames
	existingMembers := make(map[string]bool, len(currentMembers))
	for _, m := range currentMembers {
		if m.Username != nil {
			existingMembers[*m.Username] = true
		}
	}

	for _, username := range members {
		if existingMembers[username] {
			continue // Already a member
		}

		// Look up user by username
		users, err := kcClient.GetUsers(ctx, token.AccessToken, realm, gocloak.GetUsersParams{
			Username: &username,
			Exact:    gocloak.BoolP(true),
		})
		if err != nil {
			return fmt.Errorf("failed to search for user %q: %w", username, err)
		}

		if len(users) == 0 {
			logger.Info("User not found in Keycloak, skipping group assignment",
				"username", username, "group", groupName)
			continue
		}

		// Add user to group
		if err := kcClient.AddUserToGroup(ctx, token.AccessToken, realm, *users[0].ID, groupID); err != nil {
			return fmt.Errorf("failed to add user %q to group %q: %w", username, groupName, err)
		}

		logger.Info("Added user to group", "username", username, "group", groupName)
	}

	return nil
}

// shouldProvisionSPAClient returns true if the operator should provision a public SPA client.
func (p *KeycloakProvider) shouldProvisionSPAClient(nebariApp *appsv1.NebariApp) bool {
	return nebariApp.Spec.Auth != nil &&
		nebariApp.Spec.Auth.SPAClient != nil &&
		nebariApp.Spec.Auth.SPAClient.Enabled
}

// provisionSPAClient creates or updates a public OIDC client for browser-based SPA authentication.
// The SPA client is configured with:
// - publicClient: true (no secret, safe for browser)
// - Redirect URIs: https://<hostname>/* and https://<hostname>
// - PKCE enforcement (S256 code challenge method)
// Returns the SPA client ID.
func (p *KeycloakProvider) provisionSPAClient(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, nebariApp *appsv1.NebariApp) (string, error) {
	logger := log.FromContext(ctx)
	spaClientID := p.GetSPAClientID(ctx, nebariApp)

	// Check if SPA client exists
	existingSPAClient, err := p.findClient(ctx, kcClient, token, spaClientID)
	if err != nil {
		return "", fmt.Errorf("failed to query for SPA client: %w", err)
	}

	// Build wildcard redirect URLs for SPA
	hostname := nebariApp.Spec.Hostname
	redirectURIs := []string{
		fmt.Sprintf("https://%s/*", hostname),
		fmt.Sprintf("https://%s", hostname),
		fmt.Sprintf("http://%s/*", hostname),
		fmt.Sprintf("http://%s", hostname),
	}

	if existingSPAClient != nil {
		// Update existing SPA client
		existingSPAClient.RedirectURIs = &redirectURIs
		existingSPAClient.WebOrigins = &[]string{"*"}
		existingSPAClient.PublicClient = gocloak.BoolP(true)
		existingSPAClient.StandardFlowEnabled = gocloak.BoolP(true)

		// Ensure PKCE is enforced
		if existingSPAClient.Attributes == nil {
			existingSPAClient.Attributes = &map[string]string{}
		}
		(*existingSPAClient.Attributes)["pkce.code.challenge.method"] = "S256"

		err = kcClient.UpdateClient(ctx, token.AccessToken, p.Config.Realm, *existingSPAClient)
		if err != nil {
			return "", fmt.Errorf("failed to update SPA client: %w", err)
		}
		logger.Info("Updated existing SPA client", "clientID", spaClientID)
	} else {
		// Create new SPA client
		newSPAClient := gocloak.Client{
			ClientID:                  gocloak.StringP(spaClientID),
			Name:                      gocloak.StringP(fmt.Sprintf("%s SPA Client", nebariApp.Name)),
			RedirectURIs:              &redirectURIs,
			WebOrigins:                &[]string{"*"},
			PublicClient:              gocloak.BoolP(true),
			StandardFlowEnabled:       gocloak.BoolP(true),
			DirectAccessGrantsEnabled: gocloak.BoolP(false),
			Protocol:                  gocloak.StringP("openid-connect"),
			Enabled:                   gocloak.BoolP(true),
			Attributes: &map[string]string{
				"pkce.code.challenge.method": "S256",
			},
		}

		_, err := kcClient.CreateClient(ctx, token.AccessToken, p.Config.Realm, newSPAClient)
		if err != nil {
			return "", fmt.Errorf("failed to create SPA client: %w", err)
		}
		logger.Info("Created new SPA client", "clientID", spaClientID)
	}

	return spaClientID, nil
}

// shouldProvisionDeviceFlowClient returns true if the operator should provision a public device flow client.
func (p *KeycloakProvider) shouldProvisionDeviceFlowClient(nebariApp *appsv1.NebariApp) bool {
	return nebariApp.Spec.Auth != nil &&
		nebariApp.Spec.Auth.DeviceFlowClient != nil &&
		nebariApp.Spec.Auth.DeviceFlowClient.Enabled
}

// GetDeviceFlowClientID returns the device flow client ID for the NebariApp.
func (p *KeycloakProvider) GetDeviceFlowClientID(ctx context.Context, nebariApp *appsv1.NebariApp) string {
	return naming.DeviceFlowClientID(nebariApp)
}

// provisionDeviceFlowClient creates or updates a public OIDC client with device authorization grant enabled.
// The device flow client is configured with:
// - publicClient: true (no secret, safe for CLI/native apps)
// - standardFlowEnabled: false (device flow only)
// - directAccessGrantsEnabled: false
// - oauth2.device.authorization.grant.enabled: true
// - An audience mapper that includes the confidential client's ID in the aud claim
// Returns the device flow client ID.
func (p *KeycloakProvider) provisionDeviceFlowClient(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, nebariApp *appsv1.NebariApp) (string, error) {
	logger := log.FromContext(ctx)
	realm := p.Config.Realm
	deviceClientID := p.GetDeviceFlowClientID(ctx, nebariApp)
	confidentialClientID := p.GetClientID(ctx, nebariApp)

	// Check if device flow client exists
	existingClient, err := p.findClient(ctx, kcClient, token, deviceClientID)
	if err != nil {
		return "", fmt.Errorf("failed to query for device flow client: %w", err)
	}

	var internalID string

	if existingClient != nil {
		// Update existing device flow client
		existingClient.PublicClient = gocloak.BoolP(true)
		existingClient.StandardFlowEnabled = gocloak.BoolP(false)
		existingClient.DirectAccessGrantsEnabled = gocloak.BoolP(false)

		if existingClient.Attributes == nil {
			existingClient.Attributes = &map[string]string{}
		}
		(*existingClient.Attributes)["oauth2.device.authorization.grant.enabled"] = "true"

		err = kcClient.UpdateClient(ctx, token.AccessToken, realm, *existingClient)
		if err != nil {
			return "", fmt.Errorf("failed to update device flow client: %w", err)
		}
		internalID = *existingClient.ID
		logger.Info("Updated existing device flow client", "clientID", deviceClientID)
	} else {
		// Create new device flow client
		newClient := gocloak.Client{
			ClientID:                  gocloak.StringP(deviceClientID),
			Name:                      gocloak.StringP(fmt.Sprintf("%s Device Flow Client", nebariApp.Name)),
			PublicClient:              gocloak.BoolP(true),
			StandardFlowEnabled:       gocloak.BoolP(false),
			DirectAccessGrantsEnabled: gocloak.BoolP(false),
			Protocol:                  gocloak.StringP("openid-connect"),
			Enabled:                   gocloak.BoolP(true),
			Attributes: &map[string]string{
				"oauth2.device.authorization.grant.enabled": "true",
			},
		}

		internalID, err = kcClient.CreateClient(ctx, token.AccessToken, realm, newClient)
		if err != nil {
			return "", fmt.Errorf("failed to create device flow client: %w", err)
		}
		logger.Info("Created new device flow client", "clientID", deviceClientID)
	}

	// Sync scopes from spec to the device flow client
	if err := p.syncClientScopes(ctx, kcClient, token, internalID, nebariApp); err != nil {
		return "", fmt.Errorf("failed to sync device flow client scopes: %w", err)
	}

	// Add audience mapper so tokens include the confidential client's ID in aud claim.
	// Fetch the client to read existing protocol mappers.
	deviceClient, err := kcClient.GetClient(ctx, token.AccessToken, realm, internalID)
	if err != nil {
		return "", fmt.Errorf("failed to get device flow client: %w", err)
	}

	audienceMapperName := "audience-confidential-client"
	audienceMapper := gocloak.ProtocolMapperRepresentation{
		Name:           gocloak.StringP(audienceMapperName),
		Protocol:       gocloak.StringP("openid-connect"),
		ProtocolMapper: gocloak.StringP("oidc-audience-mapper"),
		Config: &map[string]string{
			"included.client.audience": confidentialClientID,
			"id.token.claim":           "true",
			"access.token.claim":       "true",
		},
	}

	// Check if the audience mapper already exists
	var existingMapperID *string
	if deviceClient.ProtocolMappers != nil {
		for _, m := range *deviceClient.ProtocolMappers {
			if m.Name != nil && *m.Name == audienceMapperName {
				existingMapperID = m.ID
				break
			}
		}
	}

	if existingMapperID != nil {
		// Update existing mapper
		audienceMapper.ID = existingMapperID
		err = kcClient.UpdateClientProtocolMapper(ctx, token.AccessToken, realm, internalID, *existingMapperID, audienceMapper)
		if err != nil {
			return "", fmt.Errorf("failed to update audience mapper on device flow client: %w", err)
		}
		logger.Info("Updated audience mapper on device flow client", "clientID", deviceClientID)
	} else {
		// Create new mapper
		_, err = kcClient.CreateClientProtocolMapper(ctx, token.AccessToken, realm, internalID, audienceMapper)
		if err != nil {
			return "", fmt.Errorf("failed to create audience mapper on device flow client: %w", err)
		}
		logger.Info("Created audience mapper on device flow client", "clientID", deviceClientID)
	}

	return deviceClientID, nil
}

// generateSecret generates a random secret string of the specified length.
func generateSecret(length int) (string, error) {
	randBytes := make([]byte, length)
	if _, err := rand.Read(randBytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(randBytes)[:length], nil
}
