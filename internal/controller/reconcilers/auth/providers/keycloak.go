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

	// Sync Keycloak groups and member assignments
	if err := p.syncGroups(ctx, kcClient, token, nebariApp); err != nil {
		return fmt.Errorf("failed to sync groups: %w", err)
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
		// "openid" is always implicit in OIDC — skip it
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

		// Check for custom mapper configuration from keycloakConfig
		if customConfig := getCustomScopeConfig(nebariApp, scopeName); customConfig != nil && len(customConfig.ProtocolMappers) > 0 {
			if err := p.syncCustomMappers(ctx, kcClient, token, scopeID, customConfig); err != nil {
				return fmt.Errorf("failed to sync custom mappers for scope %q: %w", scopeName, err)
			}
		} else if scopeName == "groups" {
			// Default: ensure the group membership mapper with full.path=false
			if err := p.ensureGroupMapper(ctx, kcClient, token, scopeID); err != nil {
				return fmt.Errorf("failed to ensure group mapper for scope %q: %w", scopeName, err)
			}
		}

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

// ensureGroupMapper ensures that a "groups" client scope has an
// oidc-group-membership-mapper protocol mapper that includes group
// names in the "groups" token claim.
func (p *KeycloakProvider) ensureGroupMapper(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, scopeID string) error {
	logger := log.FromContext(ctx)
	const mapperName = "group-membership"

	// Check if the mapper already exists
	mappers, err := kcClient.GetClientScopeProtocolMappers(ctx, token.AccessToken, p.Config.Realm, scopeID)
	if err != nil {
		return fmt.Errorf("failed to get protocol mappers: %w", err)
	}

	for _, m := range mappers {
		if m.Name != nil && *m.Name == mapperName {
			return nil // already exists
		}
	}

	// Create the group membership mapper
	mapper := gocloak.ProtocolMappers{
		Name:           gocloak.StringP(mapperName),
		Protocol:       gocloak.StringP("openid-connect"),
		ProtocolMapper: gocloak.StringP("oidc-group-membership-mapper"),
		ProtocolMappersConfig: &gocloak.ProtocolMappersConfig{
			ClaimName:          gocloak.StringP("groups"),
			FullPath:           gocloak.StringP("false"),
			IDTokenClaim:       gocloak.StringP("true"),
			AccessTokenClaim:   gocloak.StringP("true"),
			UserinfoTokenClaim: gocloak.StringP("true"),
		},
	}

	_, err = kcClient.CreateClientScopeProtocolMapper(ctx, token.AccessToken, p.Config.Realm, scopeID, mapper)
	if err != nil {
		return fmt.Errorf("failed to create group membership mapper: %w", err)
	}

	logger.Info("Created group membership protocol mapper", "scopeID", scopeID)
	return nil
}

// syncGroups ensures that Keycloak groups exist in the realm and that
// specified users are members of those groups.
// Groups are collected from both spec.auth.groups (simple list) and
// spec.auth.keycloakConfig.groups (detailed config with members).
func (p *KeycloakProvider) syncGroups(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, nebariApp *appsv1.NebariApp) error {
	if nebariApp.Spec.Auth == nil {
		return nil
	}

	// Build deduplicated map of group name -> members
	groupMembers := make(map[string][]string)

	// Collect from spec.auth.groups (no members, just ensure groups exist)
	for _, g := range nebariApp.Spec.Auth.Groups {
		if _, ok := groupMembers[g]; !ok {
			groupMembers[g] = nil
		}
	}

	// Collect from keycloakConfig.groups (with optional members)
	if nebariApp.Spec.Auth.KeycloakConfig != nil {
		for _, g := range nebariApp.Spec.Auth.KeycloakConfig.Groups {
			groupMembers[g.Name] = g.Members
		}
	}

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

	// Group doesn't exist — create it
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
// Users that don't exist in Keycloak are logged as warnings but don't cause errors.
func (p *KeycloakProvider) syncGroupMembers(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, realm, groupID, groupName string, members []string) error {
	logger := log.FromContext(ctx)

	// Get current group members
	currentMembers, err := kcClient.GetGroupMembers(ctx, token.AccessToken, realm, groupID, gocloak.GetGroupsParams{})
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
			logger.Info("WARNING: User not found in Keycloak, skipping group assignment",
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

// getCustomScopeConfig returns the custom client scope configuration for the given scope name,
// or nil if no custom config is specified.
func getCustomScopeConfig(nebariApp *appsv1.NebariApp, scopeName string) *appsv1.KeycloakClientScopeConfig {
	if nebariApp.Spec.Auth == nil || nebariApp.Spec.Auth.KeycloakConfig == nil {
		return nil
	}
	for i := range nebariApp.Spec.Auth.KeycloakConfig.ClientScopes {
		if nebariApp.Spec.Auth.KeycloakConfig.ClientScopes[i].Name == scopeName {
			return &nebariApp.Spec.Auth.KeycloakConfig.ClientScopes[i]
		}
	}
	return nil
}

// syncCustomMappers ensures protocol mappers on a client scope match the user-specified configuration.
func (p *KeycloakProvider) syncCustomMappers(ctx context.Context, kcClient *gocloak.GoCloak, token *gocloak.JWT, scopeID string, scopeConfig *appsv1.KeycloakClientScopeConfig) error {
	logger := log.FromContext(ctx)

	// Get existing mappers on this scope
	existingMappers, err := kcClient.GetClientScopeProtocolMappers(ctx, token.AccessToken, p.Config.Realm, scopeID)
	if err != nil {
		return fmt.Errorf("failed to get protocol mappers: %w", err)
	}

	existingByName := make(map[string]*gocloak.ProtocolMappers, len(existingMappers))
	for _, m := range existingMappers {
		if m.Name != nil {
			existingByName[*m.Name] = m
		}
	}

	for _, mapperCfg := range scopeConfig.ProtocolMappers {
		// Build gocloak ProtocolMappersConfig from user-provided map
		protoConfig := &gocloak.ProtocolMappersConfig{}
		for k, v := range mapperCfg.Config {
			val := v // capture for pointer
			switch k {
			case "claim.name":
				protoConfig.ClaimName = &val
			case "full.path":
				protoConfig.FullPath = &val
			case "id.token.claim":
				protoConfig.IDTokenClaim = &val
			case "access.token.claim":
				protoConfig.AccessTokenClaim = &val
			case "userinfo.token.claim":
				protoConfig.UserinfoTokenClaim = &val
			case "multivalued":
				protoConfig.Multivalued = &val
			case "aggregate.attrs":
				protoConfig.AggregateAttrs = &val
			case "user.attribute":
				protoConfig.UserAttribute = &val
			case "jsonType.label":
				protoConfig.JSONTypeLabel = &val
			}
		}

		if existing, ok := existingByName[mapperCfg.Name]; ok {
			// Update existing mapper
			existing.ProtocolMapper = gocloak.StringP(mapperCfg.ProtocolMapper)
			existing.ProtocolMappersConfig = protoConfig
			err = kcClient.UpdateClientScopeProtocolMapper(ctx, token.AccessToken, p.Config.Realm, scopeID, *existing)
			if err != nil {
				return fmt.Errorf("failed to update protocol mapper %q: %w", mapperCfg.Name, err)
			}
			logger.Info("Updated protocol mapper", "mapper", mapperCfg.Name, "scope", scopeConfig.Name)
		} else {
			// Create new mapper
			mapper := gocloak.ProtocolMappers{
				Name:                  gocloak.StringP(mapperCfg.Name),
				Protocol:              gocloak.StringP("openid-connect"),
				ProtocolMapper:        gocloak.StringP(mapperCfg.ProtocolMapper),
				ProtocolMappersConfig: protoConfig,
			}
			_, err = kcClient.CreateClientScopeProtocolMapper(ctx, token.AccessToken, p.Config.Realm, scopeID, mapper)
			if err != nil {
				return fmt.Errorf("failed to create protocol mapper %q: %w", mapperCfg.Name, err)
			}
			logger.Info("Created protocol mapper", "mapper", mapperCfg.Name, "scope", scopeConfig.Name)
		}
	}

	return nil
}

// generateSecret generates a random secret string of the specified length.
func generateSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length], nil
}
