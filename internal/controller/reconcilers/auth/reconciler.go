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

package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth/providers"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/ptr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// AuthReconciler reconciles authentication resources for NebariApps.
// It follows the same pattern as CoreReconciler and RoutingReconciler.
type AuthReconciler struct {
	Client    client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Providers map[string]providers.OIDCProvider // Provider name -> provider implementation
}

// shouldProvisionClient returns true if the operator should automatically provision an OIDC client.
// Defaults to true if not explicitly set to false.
func shouldProvisionClient(auth *appsv1.AuthConfig) bool {
	if auth == nil || auth.ProvisionClient == nil {
		return true // default
	}
	return *auth.ProvisionClient
}

// authProvisionState is the subset of NebariApp fields that affect OIDC client
// provisioning. It is JSON-marshalled and SHA-256 hashed to produce AuthConfigHash.
//
// EnforceAtGateway and ProvisionClient are intentionally excluded: they control
// operator behaviour (whether to create a SecurityPolicy / call the provider)
// but do not alter what is actually provisioned inside Keycloak. SecurityPolicy
// reconciliation runs unconditionally regardless of the hash.
type authProvisionState struct {
	Namespace      string                       `json:"namespace"`
	Name           string                       `json:"name"`
	Hostname       string                       `json:"hostname"`
	Provider       string                       `json:"provider"`
	RedirectURI    string                       `json:"redirectURI"`
	IssuerURL      string                       `json:"issuerURL"`
	Scopes         []string                     `json:"scopes"`
	Groups         []string                     `json:"groups"`
	SPAClient      *appsv1.SPAClientConfig      `json:"spaClient,omitempty"`
	KeycloakConfig *appsv1.KeycloakClientConfig `json:"keycloakConfig,omitempty"`
}

// computeAuthConfigHash returns a SHA-256 hex digest of the NebariApp fields that
// influence OIDC client provisioning. Slices are sorted before hashing to produce
// a stable result regardless of field ordering in the spec.
func computeAuthConfigHash(nebariApp *appsv1.NebariApp) string {
	auth := nebariApp.Spec.Auth

	scopes := append([]string(nil), auth.Scopes...)
	sort.Strings(scopes)

	groups := append([]string(nil), auth.Groups...)
	sort.Strings(groups)

	state := authProvisionState{
		Namespace:      nebariApp.Namespace,
		Name:           nebariApp.Name,
		Hostname:       nebariApp.Spec.Hostname,
		Provider:       auth.Provider,
		RedirectURI:    auth.RedirectURI,
		IssuerURL:      auth.IssuerURL,
		Scopes:         scopes,
		Groups:         groups,
		SPAClient:      auth.SPAClient,
		KeycloakConfig: auth.KeycloakConfig,
	}

	data, err := json.Marshal(state)
	if err != nil {
		// json.Marshal should never fail on this struct (all fields are strings or
		// serialisable nested types), but if it somehow does we fall back to a
		// name-based value. Log a warning so the unexpected event is not silent:
		// a fallback hash matching a previously stored real hash would cause a
		// false skip of provisioning.
		log.Log.Error(err, "failed to marshal auth provision state; using name-based fallback hash",
			"namespace", nebariApp.Namespace, "name", nebariApp.Name)
		data = []byte(naming.ClientSecretName(nebariApp))
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// shouldEnforceAtGateway returns true if the operator should create an Envoy SecurityPolicy.
// Defaults to true if not explicitly set to false.
func shouldEnforceAtGateway(auth *appsv1.AuthConfig) bool {
	if auth == nil || auth.EnforceAtGateway == nil {
		return true // default
	}
	return *auth.EnforceAtGateway
}

// ReconcileAuth handles authentication configuration for a NebariApp.
// It validates the auth configuration, provisions OIDC clients if needed,
// and creates/updates Envoy SecurityPolicy resources.
func (r *AuthReconciler) ReconcileAuth(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	// Skip if auth is not enabled, but clean up any existing SecurityPolicy first
	if nebariApp.Spec.Auth == nil || !nebariApp.Spec.Auth.Enabled {
		logger.Info("Auth not enabled, cleaning up any existing SecurityPolicy")
		if err := r.deleteSecurityPolicyIfExists(ctx, nebariApp); err != nil {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"SecurityPolicyCleanupFailed", fmt.Sprintf("Failed to delete existing SecurityPolicy: %v", err))
			return err
		}
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"AuthDisabled", "Authentication is not enabled for this app")
		return nil
	}

	logger.Info("Reconciling auth",
		"provider", nebariApp.Spec.Auth.Provider,
		"hostname", nebariApp.Spec.Hostname,
		"provisionClient", shouldProvisionClient(nebariApp.Spec.Auth))

	// Get the OIDC provider
	provider, err := r.getProvider(nebariApp)
	if err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"InvalidProvider", fmt.Sprintf("Invalid OIDC provider: %v", err))
		return err
	}

	// Provision OIDC client if requested and supported
	if shouldProvisionClient(nebariApp.Spec.Auth) {
		if !provider.SupportsProvisioning() {
			err := fmt.Errorf("provider %s does not support automatic client provisioning", nebariApp.Spec.Auth.Provider)
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"ProvisioningNotSupported", err.Error())
			return err
		}

		currentHash := computeAuthConfigHash(nebariApp)
		forceAnnotation := nebariApp.Annotations[constants.AnnotationForceReprovision]
		authReady := conditions.IsConditionTrue(nebariApp, appsv1.ConditionTypeAuthReady)

		if authReady && nebariApp.Status.AuthConfigHash == currentHash && forceAnnotation == "" {
			logger.Info("Auth config unchanged and AuthReady=True, skipping OIDC client provisioning")
		} else {
			if forceAnnotation != "" {
				logger.Info("Force re-provision annotation present, re-provisioning")
			}

			logger.Info("Provisioning OIDC client")
			if err := provider.ProvisionClient(ctx, nebariApp); err != nil {
				conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
					"ProvisioningFailed", fmt.Sprintf("Failed to provision OIDC client: %v", err))
				return err
			}
			logger.Info("OIDC client provisioned successfully")
			r.Recorder.Event(nebariApp, corev1.EventTypeNormal, "Provisioned", "OIDC client provisioned successfully")

			// Clear the force-reprovision annotation only after provisioning succeeds.
			// Clearing it before would silently lose the annotation if ProvisionClient
			// returned an error, leaving the user unaware they need to re-annotate.
			// Note: Update() modifies the object and triggers an extra reconcile cycle;
			// this is expected and harmless for an infrequent manual operation.
			if forceAnnotation != "" {
				if nebariApp.Annotations != nil {
					delete(nebariApp.Annotations, constants.AnnotationForceReprovision)
				}
				if err := r.Client.Update(ctx, nebariApp); err != nil {
					return fmt.Errorf("failed to clear force-reprovision annotation: %w", err)
				}
			}

			// Store hash so subsequent reconciles can skip provisioning when nothing has changed
			nebariApp.Status.AuthConfigHash = currentHash
		}

		// Reconcile RBAC for OIDC secret access (runs unconditionally so externally-deleted
		// RBAC resources are always restored).
		logger.Info("Reconciling Secret RBAC")
		if err := r.reconcileSecretRBAC(ctx, nebariApp); err != nil {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"RBACFailed", fmt.Sprintf("Failed to reconcile Secret RBAC: %v", err))
			return err
		}
	}

	// Configure token exchange if requested
	if nebariApp.Spec.Auth.TokenExchange != nil && nebariApp.Spec.Auth.TokenExchange.Enabled {
		if err := r.reconcileTokenExchange(ctx, nebariApp, provider); err != nil {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"TokenExchangeFailed", fmt.Sprintf("Failed to configure token exchange: %v", err))
			return err
		}
		logger.Info("Token exchange configured")
	}

	// Validate auth configuration (check client secret exists)
	if err := r.validateAuthConfig(ctx, nebariApp); err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"ValidationFailed", fmt.Sprintf("Auth configuration validation failed: %v", err))
		return err
	}

	// Reconcile SecurityPolicy (only if enforceAtGateway is enabled)
	if shouldEnforceAtGateway(nebariApp.Spec.Auth) {
		if err := r.reconcileSecurityPolicy(ctx, nebariApp, provider); err != nil {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"SecurityPolicyFailed", fmt.Sprintf("Failed to reconcile SecurityPolicy: %v", err))
			return err
		}
	} else {
		logger.Info("enforceAtGateway disabled, skipping SecurityPolicy creation")
		// Delete existing SecurityPolicy if transitioning from enforceAtGateway=true to false
		if err := r.deleteSecurityPolicyIfExists(ctx, nebariApp); err != nil {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"SecurityPolicyCleanupFailed", fmt.Sprintf("Failed to delete existing SecurityPolicy: %v", err))
			return err
		}
	}

	// Auth configured successfully
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionTrue,
		"AuthConfigured", fmt.Sprintf("Authentication configured with provider %s", nebariApp.Spec.Auth.Provider))
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, "Configured", "Authentication configured successfully")

	return nil
}

// CleanupAuth removes authentication resources for a NebariApp.
func (r *AuthReconciler) CleanupAuth(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	if nebariApp.Spec.Auth == nil || !nebariApp.Spec.Auth.Enabled {
		return nil
	}

	// Get the provider
	provider, err := r.getProvider(nebariApp)
	if err != nil {
		logger.Error(err, "Failed to get provider for cleanup, continuing anyway")
	} else if shouldProvisionClient(nebariApp.Spec.Auth) && provider.SupportsProvisioning() {
		// Clean up token exchange resources before deleting the client
		if nebariApp.Spec.Auth.TokenExchange != nil && nebariApp.Spec.Auth.TokenExchange.Enabled {
			if err := provider.CleanupTokenExchange(ctx, nebariApp); err != nil {
				logger.Error(err, "Failed to clean up token exchange (continuing with client deletion)")
			} else {
				logger.Info("Token exchange resources cleaned up")
			}
		}

		// Delete the provisioned client
		logger.Info("Deleting provisioned OIDC client")
		if err := provider.DeleteClient(ctx, nebariApp); err != nil {
			logger.Error(err, "Failed to delete OIDC client")
			return err
		}
		logger.Info("OIDC client deleted successfully")
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, "Deleted", "OIDC client deleted")
	}

	// SecurityPolicy will be garbage collected via ownerReferences

	return nil
}

// getProvider returns the appropriate provider for the NebariApp.
func (r *AuthReconciler) getProvider(nebariApp *appsv1.NebariApp) (providers.OIDCProvider, error) {
	providerName := nebariApp.Spec.Auth.Provider
	if providerName == "" {
		providerName = constants.ProviderKeycloak // Default
	}

	provider, exists := r.Providers[providerName]
	if !exists {
		return nil, fmt.Errorf("unsupported OIDC provider: %s", providerName)
	}

	return provider, nil
}

// validateAuthConfig validates that the OIDC client secret exists and is valid.
func (r *AuthReconciler) validateAuthConfig(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	clientSecretName := naming.ClientSecretName(nebariApp)
	logger.Info("Validating auth configuration", "secretName", clientSecretName, "namespace", nebariApp.Namespace)

	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      clientSecretName,
		Namespace: nebariApp.Namespace,
	}, secret)

	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("OIDC client secret not found - validation failed", "secretName", clientSecretName)
			return fmt.Errorf("OIDC client secret '%s' not found in namespace '%s'", clientSecretName, nebariApp.Namespace)
		}
		logger.Error(err, "Failed to get OIDC client secret")
		return fmt.Errorf("failed to get OIDC client secret: %w", err)
	}

	logger.Info("OIDC client secret found", "secretName", clientSecretName)

	if _, ok := secret.Data[constants.ClientSecretKey]; !ok {
		logger.Info("OIDC client secret missing required key", "secretName", clientSecretName, "requiredKey", constants.ClientSecretKey)
		return fmt.Errorf("OIDC client secret '%s' missing required key '%s'", clientSecretName, constants.ClientSecretKey)
	}

	logger.Info("OIDC client secret validated successfully", "secret", clientSecretName)
	return nil
}

// deleteSecurityPolicyIfExists deletes the SecurityPolicy for a NebariApp if it exists.
// This is used when transitioning from enforceAtGateway=true to enforceAtGateway=false.
func (r *AuthReconciler) deleteSecurityPolicyIfExists(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	securityPolicy := &egv1alpha1.SecurityPolicy{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      naming.SecurityPolicyName(nebariApp),
		Namespace: nebariApp.Namespace,
	}, securityPolicy)

	if apierrors.IsNotFound(err) {
		return nil // Nothing to delete
	}
	if err != nil {
		return fmt.Errorf("failed to get SecurityPolicy: %w", err)
	}

	logger.Info("Deleting SecurityPolicy (enforceAtGateway disabled)", "name", securityPolicy.Name)
	if err := r.Client.Delete(ctx, securityPolicy); err != nil {
		return fmt.Errorf("failed to delete SecurityPolicy: %w", err)
	}

	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, "SecurityPolicyDeleted", "SecurityPolicy deleted (enforceAtGateway disabled)")
	return nil
}

// reconcileSecurityPolicy creates or updates the Envoy SecurityPolicy for OIDC authentication.
func (r *AuthReconciler) reconcileSecurityPolicy(ctx context.Context, nebariApp *appsv1.NebariApp, provider providers.OIDCProvider) error {
	logger := log.FromContext(ctx)

	securityPolicyName := naming.SecurityPolicyName(nebariApp)
	securityPolicy := &egv1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      securityPolicyName,
			Namespace: nebariApp.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, securityPolicy, func() error {
		if err := controllerutil.SetControllerReference(nebariApp, securityPolicy, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		spec, err := r.buildSecurityPolicySpec(ctx, nebariApp, provider)
		if err != nil {
			return fmt.Errorf("failed to build SecurityPolicy spec: %w", err)
		}
		securityPolicy.Spec = spec

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create or update SecurityPolicy: %w", err)
	}

	logger.Info("SecurityPolicy reconciled", "name", securityPolicyName, "operation", op)
	return nil
}

// buildSecurityPolicySpec constructs the SecurityPolicy specification for OIDC.
func (r *AuthReconciler) buildSecurityPolicySpec(ctx context.Context, nebariApp *appsv1.NebariApp, provider providers.OIDCProvider) (egv1alpha1.SecurityPolicySpec, error) {
	// Get provider-specific values
	issuerURL, err := provider.GetIssuerURL(ctx, nebariApp)
	if err != nil {
		return egv1alpha1.SecurityPolicySpec{}, fmt.Errorf("failed to get issuer URL: %w", err)
	}

	clientID := provider.GetClientID(ctx, nebariApp)
	clientSecretName := naming.ClientSecretName(nebariApp)

	// Determine redirect URL
	redirectPath := constants.DefaultOAuthCallbackPath
	if nebariApp.Spec.Auth.RedirectURI != "" {
		redirectPath = nebariApp.Spec.Auth.RedirectURI
	}
	redirectURL := fmt.Sprintf("https://%s%s", nebariApp.Spec.Hostname, redirectPath)

	// Target the HTTPRoute for this NebariApp
	group := gwapiv1.Group("gateway.networking.k8s.io")
	kind := gwapiv1.Kind("HTTPRoute")
	routeName := gwapiv1.ObjectName(naming.HTTPRouteName(nebariApp))
	httpRouteRef := gwapiv1.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{
			Group: group,
			Kind:  kind,
			Name:  routeName,
		},
	}

	// Secret reference for OIDC client credentials
	secretGroup := gwapiv1.Group("")
	secretKind := gwapiv1.Kind("Secret")
	secretNamespace := gwapiv1.Namespace(nebariApp.Namespace)

	// Build OIDC configuration
	oidcProvider := egv1alpha1.OIDCProvider{
		Issuer: issuerURL,
	}

	// Apply explicit endpoint overrides from the provider. This ensures Envoy
	// uses internal cluster URLs instead of external URLs from the OIDC discovery
	// document, which may use TLS certificates not trusted by Envoy.
	overrides, err := provider.GetEndpointOverrides(ctx, nebariApp)
	if err != nil {
		return egv1alpha1.SecurityPolicySpec{}, fmt.Errorf("failed to get endpoint overrides: %w", err)
	}
	if overrides.Token != nil {
		log.FromContext(ctx).Info("Overriding OIDC endpoint from discovery", "endpoint", "token", "url", *overrides.Token)
		oidcProvider.TokenEndpoint = overrides.Token
	}
	if overrides.Authorization != nil {
		log.FromContext(ctx).Info("Overriding OIDC endpoint from discovery", "endpoint", "authorization", "url", *overrides.Authorization)
		oidcProvider.AuthorizationEndpoint = overrides.Authorization
	}
	if overrides.EndSession != nil {
		log.FromContext(ctx).Info("Overriding OIDC endpoint from discovery", "endpoint", "endSession", "url", *overrides.EndSession)
		oidcProvider.EndSessionEndpoint = overrides.EndSession
	}

	oidcConfig := &egv1alpha1.OIDC{
		Provider: oidcProvider,
		ClientID: ptr.To(clientID),
		ClientSecret: gwapiv1.SecretObjectReference{
			Group:     &secretGroup,
			Kind:      &secretKind,
			Name:      gwapiv1.ObjectName(clientSecretName),
			Namespace: &secretNamespace,
		},
		RedirectURL: ptr.To(redirectURL),
		LogoutPath:  ptr.To(constants.DefaultLogoutPath),
	}

	// Set OIDC scopes
	if len(nebariApp.Spec.Auth.Scopes) > 0 {
		oidcConfig.Scopes = nebariApp.Spec.Auth.Scopes
	} else {
		oidcConfig.Scopes = []string{"openid", "profile", "email"}
	}

	// Forward the OAuth2 access token to the upstream as Authorization: Bearer
	// when the user opts in. Applications that read the JWT for per-user
	// authorization (e.g. group claim filtering) need this; without it the
	// gateway only stores the token in an encrypted session cookie that
	// backends cannot decode.
	if nebariApp.Spec.Auth.ForwardAccessToken != nil {
		oidcConfig.ForwardAccessToken = ptr.To(*nebariApp.Spec.Auth.ForwardAccessToken)
		if *nebariApp.Spec.Auth.ForwardAccessToken {
			log.FromContext(ctx).V(1).Info("forwarding access token to upstream as Authorization: Bearer")
		}
	}

	// Set DenyRedirect headers to prevent PKCE race conditions from concurrent requests
	if len(nebariApp.Spec.Auth.DenyRedirect) > 0 {
		headers := make([]egv1alpha1.OIDCDenyRedirectHeader, 0, len(nebariApp.Spec.Auth.DenyRedirect))
		for _, h := range nebariApp.Spec.Auth.DenyRedirect {
			header := egv1alpha1.OIDCDenyRedirectHeader{
				StringMatch: egv1alpha1.StringMatch{
					Value: h.Value,
				},
			}
			header.Name = h.Name
			if h.Type != "" {
				matchType := egv1alpha1.StringMatchType(h.Type)
				header.Type = &matchType
			}
			headers = append(headers, header)
		}
		oidcConfig.DenyRedirect = &egv1alpha1.OIDCDenyRedirect{
			Headers: headers,
		}
	}

	spec := egv1alpha1.SecurityPolicySpec{
		PolicyTargetReferences: egv1alpha1.PolicyTargetReferences{
			TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{httpRouteRef},
		},
		OIDC: oidcConfig,
	}

	return spec, nil
}

// reconcileTokenExchange discovers all other NebariApp OIDC clients in the same
// Keycloak realm and configures token exchange permissions on this client.
func (r *AuthReconciler) reconcileTokenExchange(ctx context.Context, nebariApp *appsv1.NebariApp, provider providers.OIDCProvider) error {
	logger := log.FromContext(ctx)

	// List all NebariApps across all namespaces
	var allApps appsv1.NebariAppList
	if err := r.Client.List(ctx, &allApps); err != nil {
		return fmt.Errorf("failed to list NebariApps: %w", err)
	}

	// Collect internal Keycloak client IDs for all peer apps that have auth enabled
	peerClientIDs := make([]string, 0, len(allApps.Items))
	for i := range allApps.Items {
		peer := &allApps.Items[i]

		// Skip self
		if peer.Namespace == nebariApp.Namespace && peer.Name == nebariApp.Name {
			continue
		}

		// Skip peers without auth enabled or using a different provider
		if peer.Spec.Auth == nil || !peer.Spec.Auth.Enabled {
			continue
		}
		peerProvider := peer.Spec.Auth.Provider
		if peerProvider == "" {
			peerProvider = constants.ProviderKeycloak
		}
		if peerProvider != constants.ProviderKeycloak {
			continue
		}

		// Look up the peer's Keycloak client ID from its OIDC secret
		peerSecretName := naming.ClientSecretName(peer)
		var peerSecret corev1.Secret
		if err := r.Client.Get(ctx, types.NamespacedName{
			Namespace: peer.Namespace,
			Name:      peerSecretName,
		}, &peerSecret); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("Peer OIDC secret not found, skipping", "peer", peer.Name, "namespace", peer.Namespace)
				continue
			}
			return fmt.Errorf("failed to get peer OIDC secret %s/%s: %w", peer.Namespace, peerSecretName, err)
		}

		peerClientID := string(peerSecret.Data[constants.ClientIDKey])
		if peerClientID == "" {
			logger.Info("Peer OIDC secret has no client-id, skipping", "peer", peer.Name)
			continue
		}

		peerClientIDs = append(peerClientIDs, peerClientID)
		logger.Info("Found peer client for token exchange", "peer", peer.Name, "namespace", peer.Namespace)
	}

	if len(peerClientIDs) == 0 {
		logger.Info("No peer clients found for token exchange")
		return nil
	}

	return provider.ConfigureTokenExchange(ctx, nebariApp, peerClientIDs)
}
