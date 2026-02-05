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
	"fmt"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth/providers"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
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

// ReconcileAuth handles authentication configuration for a NebariApp.
// It validates the auth configuration, provisions OIDC clients if needed,
// and creates/updates Envoy SecurityPolicy resources.
func (r *AuthReconciler) ReconcileAuth(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	// Skip if auth is not enabled
	if nebariApp.Spec.Auth == nil || !nebariApp.Spec.Auth.Enabled {
		logger.Info("Auth not enabled, skipping auth reconciliation")
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

		logger.Info("Provisioning OIDC client")
		if err := provider.ProvisionClient(ctx, nebariApp); err != nil {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"ProvisioningFailed", fmt.Sprintf("Failed to provision OIDC client: %v", err))
			return err
		}
		logger.Info("OIDC client provisioned successfully")
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, "Provisioned", "OIDC client provisioned successfully")
	}

	// Validate auth configuration (check client secret exists)
	if err := r.validateAuthConfig(ctx, nebariApp); err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"ValidationFailed", fmt.Sprintf("Auth configuration validation failed: %v", err))
		return err
	}

	// Reconcile SecurityPolicy
	if err := r.reconcileSecurityPolicy(ctx, nebariApp, provider); err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"SecurityPolicyFailed", fmt.Sprintf("Failed to reconcile SecurityPolicy: %v", err))
		return err
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
	oidcConfig := &egv1alpha1.OIDC{
		Provider: egv1alpha1.OIDCProvider{
			Issuer: issuerURL,
		},
		ClientID: ptrTo(clientID),
		ClientSecret: gwapiv1.SecretObjectReference{
			Group:     &secretGroup,
			Kind:      &secretKind,
			Name:      gwapiv1.ObjectName(clientSecretName),
			Namespace: &secretNamespace,
		},
		RedirectURL: ptrTo(redirectURL),
		LogoutPath:  ptrTo(constants.DefaultLogoutPath),
	}

	// Set OIDC scopes
	if len(nebariApp.Spec.Auth.Scopes) > 0 {
		oidcConfig.Scopes = nebariApp.Spec.Auth.Scopes
	} else {
		oidcConfig.Scopes = []string{"openid", "profile", "email"}
	}

	spec := egv1alpha1.SecurityPolicySpec{
		PolicyTargetReferences: egv1alpha1.PolicyTargetReferences{
			TargetRefs: []gwapiv1.LocalPolicyTargetReferenceWithSectionName{httpRouteRef},
		},
		OIDC: oidcConfig,
	}

	return spec, nil
}

func ptrTo[T any](v T) *T {
	return &v
}
