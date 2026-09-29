/*
Copyright (c) 2026, OpenTeams

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

package keycloak

import (
	"context"
	"fmt"
	"time"

	"github.com/Nerzal/gocloak/v13"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/nebari-dev/nebari-operator/internal/config"
)

// adminRealm is the realm the admin user logs in to. It is separate from the
// realm the operator manages, which comes from config.
const adminRealm = "master"

// defaultAPITimeout bounds a Keycloak call when config does not set one.
const defaultAPITimeout = 30 * time.Second

// Client talks to the Keycloak admin API on behalf of the operator.
type Client struct {
	// cfg is held by value because LoadKeycloakCredentials writes into it.
	cfg config.KeycloakConfig
	// k8sClient is only used to read the admin credentials secret.
	k8sClient client.Client
}

// NewClient returns a Client for the given Keycloak configuration. No
// credentials are read and no connection is made until the first call.
func NewClient(cfg config.KeycloakConfig, k8sClient client.Client) *Client {
	return &Client{cfg: cfg, k8sClient: k8sClient}
}

// withTimeout bounds ctx by the configured API timeout.
func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.cfg.APITimeout
	if timeout <= 0 {
		timeout = defaultAPITimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// login reads the admin credentials and obtains an admin token. Credentials
// are re-read on every call so a rotated secret is picked up without a restart.
func (c *Client) login(ctx context.Context) (*gocloak.GoCloak, *gocloak.JWT, error) {
	if err := c.cfg.LoadKeycloakCredentials(ctx, c.k8sClient); err != nil {
		return nil, nil, fmt.Errorf("failed to load Keycloak credentials: %w", err)
	}

	kc := gocloak.NewClient(c.cfg.URL)
	token, err := kc.LoginAdmin(ctx, c.cfg.AdminUsername, c.cfg.AdminPassword, adminRealm)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to authenticate to Keycloak: %w", err)
	}

	return kc, token, nil
}
