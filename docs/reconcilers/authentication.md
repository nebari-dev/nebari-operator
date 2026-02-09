# Authentication Reconciler

> **Part of:** [Reconciler Architecture](README.md) **Phase:** 3 of 3 (Validation → Routing → Authentication)
> **Purpose:** Configure OIDC authentication and authorization

## Overview

The authentication reconciler is the third stage of the NebariApp reconciliation process, after core validation and
routing configuration. It handles OIDC authentication integration, provisioning OIDC clients when needed, and creating
Envoy Gateway SecurityPolicy resources.

The authentication logic is encapsulated in the `AuthReconciler` located at `internal/controller/reconcilers/auth/`.

## Architecture

```
NebariAppReconciler
  └─> AuthReconciler.ReconcileAuth()
       ├─> getProvider() - Get OIDC provider implementation
       ├─> ProvisionClient() - Optionally create OIDC client
       ├─> validateAuthConfig() - Verify client secret exists
       └─> reconcileSecurityPolicy() - Create/update SecurityPolicy
```

### AuthReconciler

The `AuthReconciler` is responsible for:
- Managing OIDC provider integrations (Keycloak, generic OIDC)
- Provisioning OIDC clients when requested
- Validating authentication configuration
- Creating and updating Envoy Gateway SecurityPolicy resources
- Recording Kubernetes events for authentication outcomes
- Setting appropriate status conditions

**Fields:**
- `Client`: Kubernetes client for API interactions
- `Scheme`: Runtime scheme for object type registration
- `Recorder`: Event recorder for emitting Kubernetes events
- `Providers`: Map of OIDC provider implementations (keycloak, generic-oidc)

## OIDC Provider Architecture

The reconciler uses a provider pattern to support multiple OIDC identity providers:

```go
type OIDCProvider interface {
    GetIssuerURL(ctx, nebariApp) (string, error)
    GetClientID(ctx, nebariApp) string
    ProvisionClient(ctx, nebariApp) error
    DeleteClient(ctx, nebariApp) error
    SupportsProvisioning() bool
}
```

### Supported Providers

#### 1. Keycloak Provider

**Features:**
- Automatic OIDC client provisioning via Keycloak Admin API
- Internal cluster DNS resolution for issuer URL
- Client secret management in Kubernetes secrets

**Configuration:**
- Loaded from environment variables or Kubernetes secrets
- See `internal/config/auth.go` for configuration options

**Issuer URL Format:**
```
http://keycloak.keycloak.svc.cluster.local:8080/realms/{realm}
```

**Client ID Format:**
```
{namespace}-{nebariapp-name}
```

#### 2. Generic OIDC Provider

**Features:**
- Support for any OIDC-compliant provider (Google, Azure AD, Okta, Auth0, etc.)
- Manual client provisioning (no automatic client creation)
- Flexible issuer URL configuration

**Requirements:**
- OIDC client must be created manually in the identity provider
- Client credentials must be stored in a Kubernetes secret
- `spec.auth.issuerURL` must be provided

## Authentication Flow

### 1. Provider Selection

**Logic:**
```go
func (r *AuthReconciler) getProvider(nebariApp *appsv1.NebariApp) (OIDCProvider, error)
```

**Selection Criteria:**
- Uses `spec.auth.provider` field (defaults to `keycloak`)
- Returns appropriate provider implementation from `Providers` map
- Fails if provider is not supported

**Supported Values:**
- `keycloak`: Uses KeycloakProvider
- `generic-oidc`: Uses GenericOIDCProvider

### 2. Client Provisioning (Optional)

**Enabled When:**
- `spec.auth.enabled: true`
- `spec.auth.provisionClient: true`
- Provider supports provisioning (`SupportsProvisioning() == true`)

**For Keycloak:**
1. Authenticates to Keycloak Admin API
2. Checks if client already exists
3. Creates new client or updates existing client
4. Configures redirect URLs based on hostname
5. Stores client secret in Kubernetes Secret

**Client Secret Storage:**
- Secret name: `{nebariapp-name}-oidc-client`
- Secret key: `client-secret`
- Labels: Standard app.kubernetes.io labels

**On Failure:**
- Event: `Warning` with reason `ProvisioningFailed`
- Condition: `AuthReady=False` with reason `ProvisioningFailed`
- Error message includes provider error details

### 3. Configuration Validation

**Purpose**: Ensures OIDC client credentials exist before creating SecurityPolicy.

**Validation Logic:**
```go
func (r *AuthReconciler) validateAuthConfig(ctx, nebariApp) error
```

**Requirements:**
- Client secret must exist in the same namespace
- Secret must contain the `client-secret` key

**Secret Naming:**
- Name: `{nebariapp-name}-oidc-client`
- Namespace: Same as NebariApp

**On Failure:**
- Event: `Warning` with reason `ValidationFailed`
- Condition: `AuthReady=False` with reason `ValidationFailed`
- Error message: "OIDC client secret '{name}' not found in namespace '{namespace}'"

### 4. SecurityPolicy Creation

**Purpose**: Creates or updates an Envoy Gateway SecurityPolicy resource to enforce OIDC authentication.

**Resource Naming:**
- Name: `{nebariapp-name}-security`
- Namespace: Same as NebariApp
- Owner reference: NebariApp (for garbage collection)

**SecurityPolicy Spec:**
```yaml
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: {nebariapp-name}-route
  oidc:
    provider:
      issuer: {provider-specific-issuer-url}
    clientID: {client-id}
    clientSecret:
      name: {nebariapp-name}-oidc-client
      namespace: {namespace}
    redirectURL: https://{hostname}{redirect-path}
    logoutPath: /logout
    scopes:
      - openid
      - profile
      - email
```

**Configuration:**
- `issuer`: Retrieved from provider's `GetIssuerURL()`
- `clientID`: Retrieved from provider's `GetClientID()`
- `redirectURL`: Defaults to `https://{hostname}/oauth2/callback`
- `scopes`: Uses `spec.auth.scopes` or defaults to `["openid", "profile", "email"]`

**On Failure:**
- Event: `Warning` with reason `SecurityPolicyFailed`
- Condition: `AuthReady=False` with reason `SecurityPolicyFailed`
- Error message includes underlying error

## Status Management

### Conditions

The authentication process sets the `AuthReady` condition on the NebariApp status:

**When Auth is Disabled:**
```yaml
conditions:
  - type: AuthReady
    status: "False"
    reason: AuthDisabled
    message: "Authentication is not enabled for this app"
```

**During Provisioning:**
```yaml
conditions:
  - type: AuthReady
    status: "False"
    reason: Reconciling
    message: "Provisioning OIDC client"
```

**On Success:**
```yaml
conditions:
  - type: AuthReady
    status: "True"
    reason: AuthConfigured
    message: "Authentication configured with provider keycloak"
```

**On Failure:**
```yaml
conditions:
  - type: AuthReady
    status: "False"
    reason: ProvisioningFailed | ValidationFailed | SecurityPolicyFailed
    message: "<detailed error message>"
```

### Events

**Success Events:**
- `Normal/Provisioned`: "OIDC client provisioned successfully"
- `Normal/Configured`: "Authentication configured successfully"

**Warning Events:**
- `Warning/ProvisioningFailed`: "Failed to provision OIDC client: {error}"
- `Warning/ValidationFailed`: "Auth configuration validation failed: {error}"
- `Warning/SecurityPolicyFailed`: "Failed to reconcile SecurityPolicy: {error}"

## Cleanup Process

When a NebariApp is deleted, the `CleanupAuth()` method handles resource cleanup:

### Cleanup Logic

```go
func (r *AuthReconciler) CleanupAuth(ctx, nebariApp) error
```

**Steps:**
1. Skip if auth is not enabled
2. Get the configured provider
3. If client was provisioned (`provisionClient: true`):
   - Call provider's `DeleteClient()` method
   - Keycloak: Deletes the OIDC client from Keycloak
   - Generic OIDC: No-op (clients managed externally)
4. SecurityPolicy is automatically garbage collected via owner references

**On Failure:**
- Logs error but continues cleanup
- Returns error to prevent finalizer removal

## Configuration

### Environment Variables

The auth reconciler can be configured via environment variables:

**Keycloak Provider:**
- `KEYCLOAK_ENABLED`: Enable Keycloak integration (default: `true`)
- `KEYCLOAK_URL`: Keycloak URL (default: `http://keycloak-keycloakx-http.keycloak.svc.cluster.local/auth`)
- `KEYCLOAK_REALM`: Keycloak realm (default: `nebari`)
- `KEYCLOAK_ADMIN_SECRET_NAME`: Secret containing master realm admin credentials (default: `keycloak-admin-credentials`)
- `KEYCLOAK_ADMIN_SECRET_NAMESPACE`: Namespace of admin secret (default: `keycloak`)

**Alternative (for testing):**
- `KEYCLOAK_ADMIN_USERNAME`: Admin username (takes precedence over secret)
- `KEYCLOAK_ADMIN_PASSWORD`: Admin password (takes precedence over secret)

### Kubernetes Secret Format

**Admin Credentials Secret (Master Realm):**

The operator requires admin access to Keycloak's master realm to provision clients in any realm:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: keycloak-admin-credentials
  namespace: keycloak
type: Opaque
data:
  # Supports both key formats:
  # Standard format:
  admin-username: <base64-encoded-username>  # or 'username'
  admin-password: <base64-encoded-password>  # or 'password'
```

**Client Credentials Secret (auto-generated):**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: my-app-oidc-client
  namespace: my-namespace
  labels:
    app.kubernetes.io/name: nebariapp
    app.kubernetes.io/instance: my-app
    app.kubernetes.io/managed-by: nebari-operator
type: Opaque
data:
  client-secret: <base64-encoded-secret>
```

## Examples

### Complete Auth Configuration (Keycloak)

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: protected-app
  namespace: production
spec:
  hostname: app.example.com
  service:
    name: backend-service
    port: 8080
  routing:
    tls:
      enabled: true
  auth:
    enabled: true
    provider: keycloak
    provisionClient: true
    scopes:
      - openid
      - profile
      - email
      - groups
    redirectURI: /oauth2/callback
```

### Manual Client Configuration (Generic OIDC)

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: google-auth-app
  namespace: production
spec:
  hostname: app.example.com
  service:
    name: backend-service
    port: 8080
  routing:
    tls:
      enabled: true
  auth:
    enabled: true
    provider: generic-oidc
    provisionClient: false
    issuerURL: https://accounts.google.com
    scopes:
      - openid
      - profile
      - email
---
# Manually created secret with Google OAuth credentials
apiVersion: v1
kind: Secret
metadata:
  name: google-auth-app-oidc-client
  namespace: production
type: Opaque
stringData:
  client-secret: "your-google-oauth-client-secret"
```

## Error Handling

### Common Errors

**1. Provider Not Supported**
```
Error: unsupported OIDC provider: custom-provider
Reason: InvalidProvider
Fix: Use supported provider: 'keycloak' or 'generic-oidc'
```

**2. Provisioning Not Supported**
```
Error: provider generic-oidc does not support automatic client provisioning
Reason: ProvisioningNotSupported
Fix: Set provisionClient: false and create client manually
```

**3. Client Secret Not Found**
```
Error: OIDC client secret 'my-app-oidc-client' not found in namespace 'production'
Reason: ValidationFailed
Fix: Create secret or enable provisionClient
```

**4. Keycloak Connection Failed**
```
Error: failed to authenticate to Keycloak: connection refused
Reason: ProvisioningFailed
Fix: Check Keycloak is running and accessible
```

### Debugging

**Check Auth Reconciler Logs:**
```bash
kubectl logs -n nebari-operator-system deployment/nebari-operator-controller-manager \
  | grep "auth"
```

**Check SecurityPolicy:**
```bash
kubectl get securitypolicy -n <namespace> <app-name>-security -o yaml
```

**Check Client Secret:**
```bash
kubectl get secret -n <namespace> <app-name>-oidc-client
```

**Check Events:**
```bash
kubectl get events -n <namespace> \
  --field-selector involvedObject.name=<nebariapp-name> \
  --sort-by='.lastTimestamp'
```

## Integration with Other Reconcilers

The auth reconciler integrates with:

1. **CoreReconciler**: Runs after core validation passes
2. **RoutingReconciler**: Requires HTTPRoute to exist (SecurityPolicy targets it)
3. **Status Management**: Updates `AuthReady` condition and aggregate `Ready` condition

**Reconciliation Order:**
1. Core validation
2. Routing (HTTPRoute creation)
3. **Authentication (SecurityPolicy creation)**
4. Status aggregation

## Testing

### Unit Tests

Located in `internal/controller/reconcilers/auth/`:
- `reconciler_test.go`: Tests reconciler logic
- `providers/keycloak_test.go`: Tests Keycloak provider
- `providers/generic_oidc_test.go`: Tests generic OIDC provider

**Coverage:**
- Auth reconciler: 81.2%
- Providers: 40.2%

### Integration Tests

Add E2E tests to verify:
- OIDC authentication flow works end-to-end
- SecurityPolicy is created correctly
- Client provisioning succeeds
- Cleanup removes resources properly

## Future Enhancements

Potential improvements:
1. **Additional Providers**: Auth0, Azure AD, Okta direct integration
2. **Group-based Authorization**: Map OIDC groups to Kubernetes RBAC
3. **Token Refresh**: Configure token refresh behavior
4. **Session Management**: Configure session timeout and persistence
5. **Multi-Provider**: Support multiple providers per operator instance
