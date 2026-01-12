# NIC Operator Authentication Design

## Philosophy: Opt-in Security by Default

The NIC Operator follows an **opt-in** approach to authentication:

- **Authentication is DISABLED by default** (`auth.enabled: false`)
- Applications must explicitly enable authentication in their NicApp spec
- This provides flexibility for different use cases:
  - Public-facing apps (no auth needed)
  - Internal tools (may use different auth)
  - Development/testing (auth can be cumbersome)
  - Production apps (explicitly enable auth)

## Automatic Client Provisioning

When `auth.enabled: true` and `auth.provisionClient: true`, the operator automatically:

1. ✅ Creates a Keycloak client in the configured realm
2. ✅ Configures redirect URIs based on the app's hostname
3. ✅ Sets up appropriate scopes and client settings
4. ✅ Configures Envoy Gateway's OAuth2 filter
5. ✅ Updates the HTTPRoute with authentication requirements

## Example Configurations

### Public Application (No Auth)

```yaml
apiVersion: apps.nic.nebari.dev/v1
kind: NicApp
metadata:
  name: public-app
  namespace: demo
spec:
  hostname: public-app.nic.local
  service:
    name: public-app
    port: 80
  auth:
    enabled: false  # Explicitly no authentication
```

### Protected Application (With Auth)

```yaml
apiVersion: apps.nic.nebari.dev/v1
kind: NicApp
metadata:
  name: protected-app
  namespace: demo
spec:
  hostname: protected-app.nic.local
  service:
    name: protected-app
    port: 80
  auth:
    enabled: true              # Require authentication
    provider: keycloak
    provisionClient: true      # Auto-create Keycloak client
```

### Custom Client Configuration

```yaml
apiVersion: apps.nic.nebari.dev/v1
kind: NicApp
metadata:
  name: custom-auth-app
  namespace: demo
spec:
  hostname: custom-app.nic.local
  service:
    name: custom-app
    port: 80
  auth:
    enabled: true
    provider: keycloak
    provisionClient: true
    clientId: my-custom-client    # Custom client ID
    scopes:
      - openid
      - profile
      - email
      - custom-scope
```

### Using Existing Client

```yaml
apiVersion: apps.nic.nebari.dev/v1
kind: NicApp
metadata:
  name: existing-client-app
  namespace: demo
spec:
  hostname: existing-app.nic.local
  service:
    name: existing-app
    port: 80
  auth:
    enabled: true
    provider: keycloak
    provisionClient: false       # Don't auto-create
    clientId: pre-existing-client # Use existing client
```

## Testing Authentication

### Deploy Sample with Auth

```bash
# Deploy the authenticated version
kubectl apply -f dev/local-cluster/sample-apps/nginx-demo-with-auth.yaml

# Add to /etc/hosts
echo "127.0.0.1  nginx-demo-auth.nic.local" | sudo tee -a /etc/hosts

# Access the app - should redirect to Keycloak login
curl -L http://nginx-demo-auth.nic.local

# Or in browser
open http://nginx-demo-auth.nic.local
```

### Verify Client Creation

```bash
# Check NicApp status
kubectl get nicapp -n demo-app nginx-demo-auth -o yaml

# Look for AuthReady condition
kubectl get nicapp -n demo-app nginx-demo-auth -o jsonpath='{.status.conditions[?(@.type=="AuthReady")]}'
```

## Architecture

When authentication is enabled, the operator configures:

1. **Keycloak Client** (if `provisionClient: true`)
   - Client ID: `<app-name>-<namespace>` or custom
   - Client type: `public` (for web apps)
   - Valid redirect URIs: `https://<hostname>/*`
   - Web origins: `https://<hostname>`

2. **Envoy Gateway Security Policy**
   - OAuth2 filter for OIDC authentication
   - Token validation
   - Session management
   - Cookie-based authentication

3. **HTTPRoute Updates**
   - Adds authentication requirements
   - Configures callback routes
   - Sets up proper headers

## Benefits of Opt-in Approach

✅ **Flexibility**: Not all apps need authentication ✅ **Simplicity**: Quick prototyping without auth overhead ✅
**Explicit**: Clear intent in the NicApp spec ✅ **Gradual**: Can enable auth when ready for production ✅ **Choice**:
Apps can use different auth mechanisms if needed

## Future Enhancements

- Support for multiple auth providers (OAuth2, LDAP, etc.)
- Group/role-based access control
- Per-path authentication requirements
- Custom authentication filters
- Authentication bypass for health checks
