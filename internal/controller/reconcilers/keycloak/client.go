// Copyright 2025 Nebari Development Team.
// SPDX-License-Identifier: Apache-2.0

package keycloak

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/Nerzal/gocloak/v13"
	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ClientProvisioner struct {
	Client        client.Client
	KeycloakURL   string
	KeycloakRealm string
	AdminUsername string
	AdminPassword string
}

func (p *ClientProvisioner) ProvisionClient(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	kcClient := gocloak.NewClient(p.KeycloakURL)

	token, err := kcClient.LoginAdmin(ctx, p.AdminUsername, p.AdminPassword, "master")
	if err != nil {
		return fmt.Errorf("failed to login to Keycloak: %w", err)
	}

	realm := p.KeycloakRealm
	if nicApp.Spec.Auth != nil && nicApp.Spec.Auth.Provider != "" {
		realm = nicApp.Spec.Auth.Provider
	}

	clientID := naming.ClientID(nicApp)
	redirectURL := fmt.Sprintf("https://%s/oauth2/callback", nicApp.Spec.Hostname)

	// For local development, also allow HTTP redirects
	redirectURLs := []string{
		redirectURL,
		fmt.Sprintf("http://%s/oauth2/callback", nicApp.Spec.Hostname),
	}

	logger.Info("Provisioning Keycloak client", "clientID", clientID, "realm", realm, "redirectURL", redirectURL)

	clients, err := kcClient.GetClients(ctx, token.AccessToken, realm, gocloak.GetClientsParams{
		ClientID: &clientID,
	})
	if err != nil {
		return fmt.Errorf("failed to get clients: %w", err)
	}

	var clientSecret string
	var clientUUID string

	if len(clients) > 0 {
		clientUUID = *clients[0].ID
		logger.Info("Client already exists, updating", "clientID", clientID, "uuid", clientUUID)

		existingSecret, err := kcClient.GetClientSecret(ctx, token.AccessToken, realm, clientUUID)
		if err != nil {
			return fmt.Errorf("failed to get client secret: %w", err)
		}
		clientSecret = *existingSecret.Value

		clients[0].RedirectURIs = &redirectURLs
		clients[0].WebOrigins = &[]string{"*"}
		clients[0].PublicClient = gocloak.BoolP(false)
		clients[0].StandardFlowEnabled = gocloak.BoolP(true)
		clients[0].DirectAccessGrantsEnabled = gocloak.BoolP(false)

		err = kcClient.UpdateClient(ctx, token.AccessToken, realm, *clients[0])
		if err != nil {
			return fmt.Errorf("failed to update client: %w", err)
		}
	} else {
		logger.Info("Creating new client", "clientID", clientID)

		clientSecret, err = generateSecret(32)
		if err != nil {
			return fmt.Errorf("failed to generate client secret: %w", err)
		}

		newClient := gocloak.Client{
			ClientID:                  gocloak.StringP(clientID),
			Name:                      gocloak.StringP(fmt.Sprintf("%s OIDC Client", nicApp.Name)),
			Description:               gocloak.StringP(fmt.Sprintf("Auto-provisioned by nic-operator for %s", nicApp.Name)),
			Secret:                    gocloak.StringP(clientSecret),
			RedirectURIs:              &redirectURLs,
			WebOrigins:                &[]string{"*"},
			PublicClient:              gocloak.BoolP(false),
			StandardFlowEnabled:       gocloak.BoolP(true),
			DirectAccessGrantsEnabled: gocloak.BoolP(false),
			ServiceAccountsEnabled:    gocloak.BoolP(false),
			Protocol:                  gocloak.StringP("openid-connect"),
			Enabled:                   gocloak.BoolP(true),
		}

		createdClientUUID, err := kcClient.CreateClient(ctx, token.AccessToken, realm, newClient)
		if err != nil {
			return fmt.Errorf("failed to create client: %w", err)
		}
		clientUUID = createdClientUUID

		logger.Info("Client created successfully", "clientID", clientID, "uuid", clientUUID)
	}

	if err := p.storeClientSecret(ctx, nicApp, clientSecret); err != nil {
		return fmt.Errorf("failed to store client secret: %w", err)
	}

	logger.Info("Keycloak client provisioning completed", "clientID", clientID)
	return nil
}

func (p *ClientProvisioner) DeleteClient(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	kcClient := gocloak.NewClient(p.KeycloakURL)

	token, err := kcClient.LoginAdmin(ctx, p.AdminUsername, p.AdminPassword, "master")
	if err != nil {
		return fmt.Errorf("failed to login to Keycloak: %w", err)
	}

	realm := p.KeycloakRealm
	if nicApp.Spec.Auth != nil && nicApp.Spec.Auth.Provider != "" {
		realm = nicApp.Spec.Auth.Provider
	}

	clientID := naming.ClientID(nicApp)

	clients, err := kcClient.GetClients(ctx, token.AccessToken, realm, gocloak.GetClientsParams{
		ClientID: &clientID,
	})
	if err != nil {
		return fmt.Errorf("failed to get clients: %w", err)
	}

	if len(clients) == 0 {
		logger.Info("Client does not exist, skipping deletion", "clientID", clientID)
		return nil
	}

	clientUUID := *clients[0].ID
	err = kcClient.DeleteClient(ctx, token.AccessToken, realm, clientUUID)
	if err != nil {
		return fmt.Errorf("failed to delete client: %w", err)
	}

	logger.Info("Keycloak client deleted", "clientID", clientID)
	return nil
}

func (p *ClientProvisioner) storeClientSecret(ctx context.Context, nicApp *appsv1.NicApp, clientSecret string) error {
	logger := log.FromContext(ctx)

	secretName := naming.ClientSecretName(nicApp)
	secret := &corev1.Secret{}

	err := p.Client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: nicApp.Namespace,
	}, secret)

	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get secret: %w", err)
		}

		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: nicApp.Namespace,
				Labels: map[string]string{
					"app.kubernetes.io/name":       "nicapp",
					"app.kubernetes.io/instance":   nicApp.Name,
					"app.kubernetes.io/managed-by": "nic-operator",
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				constants.ClientSecretKey: []byte(clientSecret),
			},
		}

		if err := p.Client.Create(ctx, secret); err != nil {
			return fmt.Errorf("failed to create secret: %w", err)
		}

		logger.Info("Client secret created", "secret", secretName)
	} else {
		secret.Data[constants.ClientSecretKey] = []byte(clientSecret)

		if err := p.Client.Update(ctx, secret); err != nil {
			return fmt.Errorf("failed to update secret: %w", err)
		}

		logger.Info("Client secret updated", "secret", secretName)
	}

	return nil
}

func generateSecret(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes)[:length], nil
}
