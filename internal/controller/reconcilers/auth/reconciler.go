// Copyright 2025 Nebari Development Team.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
	"github.com/nebari-dev/nic-operator/internal/controller/reconcilers/keycloak"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

const (
	SecurityPolicyNameSuffix = "-security"
)

type Reconciler struct {
	Client              client.Client
	KeycloakProvisioner *keycloak.ClientProvisioner
	KeycloakIssuerURL   string // External OIDC issuer URL (browser-accessible)
	KeycloakRealm       string // Keycloak realm name
}

func (r *Reconciler) ReconcileAuth(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	if nicApp.Spec.Auth == nil || !nicApp.Spec.Auth.Enabled {
		logger.Info("Auth not enabled, skipping auth reconciliation")
		conditions.SetCondition(nicApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"AuthDisabled", "Authentication is not enabled for this app")
		return nil
	}

	logger.Info("Reconciling auth",
		"provider", nicApp.Spec.Auth.Provider,
		"hostname", nicApp.Spec.Hostname,
		"provisionClient", nicApp.Spec.Auth.ProvisionClient)

	// Provision Keycloak client if requested
	if nicApp.Spec.Auth.ProvisionClient && r.KeycloakProvisioner != nil {
		logger.Info("Provisioning Keycloak client")
		if err := r.KeycloakProvisioner.ProvisionClient(ctx, nicApp); err != nil {
			conditions.SetCondition(nicApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
				"ProvisioningFailed", fmt.Sprintf("Failed to provision Keycloak client: %v", err))
			return err
		}
		logger.Info("Keycloak client provisioned successfully")
	}

	if err := r.validateAuthConfig(ctx, nicApp); err != nil {
		conditions.SetCondition(nicApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"ValidationFailed", fmt.Sprintf("Auth configuration validation failed: %v", err))
		return err
	}

	if err := r.reconcileSecurityPolicy(ctx, nicApp); err != nil {
		conditions.SetCondition(nicApp, appsv1.ConditionTypeAuthReady, metav1.ConditionFalse,
			"SecurityPolicyFailed", fmt.Sprintf("Failed to reconcile SecurityPolicy: %v", err))
		return err
	}

	conditions.SetCondition(nicApp, appsv1.ConditionTypeAuthReady, metav1.ConditionTrue,
		"AuthConfigured", fmt.Sprintf("Authentication configured with provider %s", nicApp.Spec.Auth.Provider))

	return nil
}

func (r *Reconciler) validateAuthConfig(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	clientSecretName := naming.ClientSecretName(nicApp)
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      clientSecretName,
		Namespace: nicApp.Namespace,
	}, secret)

	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("OIDC client secret '%s' not found in namespace '%s'", clientSecretName, nicApp.Namespace)
		}
		return fmt.Errorf("failed to get OIDC client secret: %w", err)
	}

	if _, ok := secret.Data[constants.ClientSecretKey]; !ok {
		return fmt.Errorf("OIDC client secret '%s' missing required key '%s'", clientSecretName, constants.ClientSecretKey)
	}

	logger.Info("OIDC client secret validated successfully", "secret", clientSecretName)
	return nil
}

func (r *Reconciler) reconcileSecurityPolicy(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	securityPolicyName := naming.SecurityPolicyName(nicApp)
	securityPolicy := &egv1alpha1.SecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      securityPolicyName,
			Namespace: nicApp.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, securityPolicy, func() error {
		if err := controllerutil.SetControllerReference(nicApp, securityPolicy, r.Client.Scheme()); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}
		securityPolicy.Spec = r.buildSecurityPolicySpec(nicApp)
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to create or update SecurityPolicy: %w", err)
	}

	logger.Info("SecurityPolicy reconciled", "name", securityPolicyName, "operation", op)
	return nil
}

func (r *Reconciler) buildSecurityPolicySpec(nicApp *appsv1.NicApp) egv1alpha1.SecurityPolicySpec {
	clientID := naming.ClientID(nicApp)
	clientSecretName := naming.ClientSecretName(nicApp)

	// Always use internal cluster DNS for Envoy to fetch OIDC config
	// Keycloak will return the correct external URLs in its responses
	issuerURL := fmt.Sprintf("http://keycloak.keycloak.svc.cluster.local:8080/realms/%s", r.KeycloakRealm)

	group := gwapiv1.Group("gateway.networking.k8s.io")
	kind := gwapiv1.Kind("HTTPRoute")
	routeName := gwapiv1.ObjectName(nicApp.Name + "-route")
	httpRouteRef := gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gwapiv1.LocalPolicyTargetReference{
			Group: group,
			Kind:  kind,
			Name:  routeName,
		},
	}

	secretGroup := gwapiv1.Group("")
	secretKind := gwapiv1.Kind("Secret")
	secretNamespace := gwapiv1.Namespace(nicApp.Namespace)

	oidcConfig := &egv1alpha1.OIDC{
		Provider: egv1alpha1.OIDCProvider{
			Issuer: issuerURL,
		},
		ClientID: clientID,
		ClientSecret: gwapiv1.SecretObjectReference{
			Group:     &secretGroup,
			Kind:      &secretKind,
			Name:      gwapiv1.ObjectName(clientSecretName),
			Namespace: &secretNamespace,
		},
		RedirectURL: ptrTo(fmt.Sprintf("https://%s/oauth2/callback", nicApp.Spec.Hostname)),
		LogoutPath:  ptrTo("/logout"),
	}

	if len(nicApp.Spec.Auth.Scopes) > 0 {
		oidcConfig.Scopes = nicApp.Spec.Auth.Scopes
	} else {
		oidcConfig.Scopes = []string{"openid", "profile", "email"}
	}

	spec := egv1alpha1.SecurityPolicySpec{
		PolicyTargetReferences: egv1alpha1.PolicyTargetReferences{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{httpRouteRef},
		},
		OIDC: oidcConfig,
	}

	return spec
}

func ptrTo[T any](v T) *T {
	return &v
}
