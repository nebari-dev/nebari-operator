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
- Split-horizon endpoints: token back-channel in-cluster, browser-facing endpoints public
- Client secret management in Kubernetes secrets

**Configuration:**
- Loaded from environment variables or Kubernetes secrets
- See `internal/config/auth.go` for configuration options

**Issuer URL Format (Keycloak 26+, root context path):**
```
# KEYCLOAK_EXTERNAL_URL set (required on Envoy Gateway v1.9.1+)
https://keycloak.example.com/realms/{realm}
# KEYCLOAK_EXTERNAL_URL unset (Envoy Gateway < v1.9.1 only)
http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080/realms/{realm}
```
See [Issuer Semantics](#issuer-semantics-keycloak) for why the issuer depends on it.

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

**Partial Success — `GroupsNotResolved`:**

When `spec.auth.groups` lists a path that does not exist in Keycloak (e.g. `/missing-team`),
the client, its Secret and the SecurityPolicy are still reconciled, so a new app is never
served without OIDC in front of it. The reconciler then:

- Emits `Warning/GroupsNotResolved` naming the missing paths.
- Sets `AuthReady=False` with reason `GroupsNotResolved` (the controller mirrors this onto
  `Ready=False/GroupsNotResolved` on the NebariApp).
- Leaves `status.AuthConfigHash` unset, so the one-minute requeue retries provisioning
  until the group is created, at which point the condition clears and the hash is stored.
- Still publishes `status.serviceDiscovery` so routing stays functional.

Any other provisioning error still fails as `ProvisioningFailed`. Bare-name entries (a
top-level group the operator creates on 404) never trigger `GroupsNotResolved`; only
path-form entries that fail resolution do.

If a custom mapper conflict makes the policy deny-all, `GroupsClaimConflict` is reported instead of `GroupsNotResolved`,
since creating the missing group would not admit anyone.

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

**Important:** The SecurityPolicy targets only the **main** HTTPRoute (`{nebariapp-name}-route`), not the public
HTTPRoute (`{nebariapp-name}-public-route`). This is how `spec.routing.publicRoutes` bypass authentication — they are
served by a separate HTTPRoute that has no SecurityPolicy attached. See the
[Routing Reconciler](routing.md#public-routes-authentication-bypass) for details.

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

### Issuer Semantics (Keycloak)

For Keycloak, `provider.issuer` depends on whether `KEYCLOAK_EXTERNAL_URL` is set, because
that decides whether Envoy Gateway runs OIDC discovery
([#112](https://github.com/nebari-dev/nebari-operator/issues/112),
[#186](https://github.com/nebari-dev/nebari-operator/issues/186)):

| `KEYCLOAK_EXTERNAL_URL` | Endpoint overrides | Discovery | `provider.issuer` |
|---|---|---|---|
| set | token (in-cluster) + authorization, endSession (public) | never runs | public realm URL (`https://…`) |
| unset | token only | runs against the issuer | in-cluster realm URL (`http://…`) |

Why this is safe:

- Envoy Gateway uses the issuer **only at control-plane time** to fetch
  `/.well-known/openid-configuration`, and only when `authorizationEndpoint` or
  `tokenEndpoint` is not explicitly set. With `KEYCLOAK_EXTERNAL_URL` configured the
  operator sets both, so the issuer is never contacted and never reaches the data plane.
  The gateway never resolves the public hostname, so private-domain and air-gapped
  clusters are unaffected.
- The token back-channel protocol is taken from `tokenEndpoint` when it is set, so an
  `https` issuer does not move token calls off the in-cluster URL.
- The Envoy `oauth2` filter has no issuer field in its configuration and never inspects
  the token's `iss` claim (its only JWT decode is an unverified `exp` read for cookie
  lifetimes). Envoy Gateway also ignores the discovery document's `issuer` field.
- Envoy Gateway **v1.9.1** constrains the issuer to `^https://[^/?#@]+(/[^?#]*)?$` in both
  the CRD and its translator. The public URL satisfies it and matches the `iss` claim
  Keycloak stamps into tokens.

When `KEYCLOAK_EXTERNAL_URL` is unset, discovery does run, so the issuer stays in-cluster:
pointing discovery at a public hostname fails on clusters where that hostname does not
resolve from inside the cluster (the private-domain failure seen in
nebari-dev/nebari-infrastructure-core#568). That mode only works on Envoy Gateway
< v1.9.1. On v1.9.1+ the SecurityPolicy is rejected at admission, and the `AuthReady`
condition message says to set `KEYCLOAK_EXTERNAL_URL`.

Consumers that **do** strictly validate `iss` — e.g. a SecurityPolicy `spec.jwt` provider
configured by a downstream pack — read the public issuer from the client Secret's
`issuer-url` key, which is populated from `GetExternalIssuerURL()`. That key is empty when
`KEYCLOAK_EXTERNAL_URL` is unset, so strict-`iss` consumers require it to be set.

**On Failure:**
- Event: `Warning` with reason `SecurityPolicyFailed`
- Condition: `AuthReady=False` with reason `SecurityPolicyFailed`
- Error message includes underlying error

## Group-based access control

When `spec.auth.groups` is non-empty, the provider is `keycloak`, and `enforceAtGateway` is `true`, the operator adds
three sections to the SecurityPolicy so that the gateway itself rejects users outside the listed groups. For app
`finance-dash` in namespace `finance` with `groups: [finance, /ops/oncall]`:

```yaml
spec:
  oidc:
    # ...as above, plus:
    cookieNames:
      accessToken: nebari-at-finance-finance-dash
  jwt:
    providers:
      - name: nebari-groups
        remoteJWKS:
          uri: http://<service>.<namespace>.svc.cluster.local:<port><context-path>/realms/<realm>/protocol/openid-connect/certs
        extractFrom:
          cookies:
            - nebari-at-finance-finance-dash
  authorization:
    defaultAction: Deny
    rules:
      - name: allow-groups
        action: Allow
        principal:
          jwt:
            provider: nebari-groups
            claims:
              - name: groups
                valueType: StringArray
                values: ["/finance", "/ops/oncall"]
              - name: azp
                valueType: String
                values: ["finance-finance-dash"]
```

Claims in one rule are ANDed. The `azp` claim must equal the app's own client ID, because every SecurityPolicy shares
one oauth2 HMAC secret, so the cookie check alone does not tie a token to this app.

The `oidc` section's access-token cookie is renamed to `nebari-at-<namespace>-<name>` so the `jwt` provider can read a
known cookie and verify the token against the realm JWKS. Apps that enforce groups at the gateway get this pinned cookie, so their users are logged out once when the app
starts enforcing (for example on upgrade); other apps keep the default cookie name. A token with no `groups` claim matches no rule and receives 403. The operator puts at
most 16 values in one rule (Envoy Gateway's limit on a claim before v1.6.0), so with more than 16 groups the values are
split across rules `allow-groups`, `allow-groups-2`, and so on.

**Claim format.** The `groups` claim carries full group paths such as `/finance` or `/ops/oncall`. The default
`group-membership` mapper is switched to `full.path=true` for every app, not only apps that set `auth.groups`. Entries
in `auth.groups` are normalized to full paths (`finance` becomes `/finance`) and matched exactly and case-sensitively.
A parent group does not admit members of its subgroups; list the subgroup itself. When the app defines custom
`keycloakConfig.protocolMappers`, sets `auth.groups` and enforces them at the gateway, the operator appends a mapper
named `nebari-group-membership` unless a custom group-membership mapper already emits full paths into the access token.
With `enforceAtGateway: false`, a custom mapper that emits the `groups` claim keeps owning it, since the application
reads that claim and only the gateway needs full paths.

**Other mappers on the client.** The operator merges its mappers with what is already on the app's client instead of
replacing it. It records the mappers it manages in the client attribute `nebari.dev/managed-protocol-mappers` and
deletes only those once the spec stops listing them, plus its own `nebari-group-membership` whenever it is not needed.
Mappers added any other way, for example by hand in Keycloak, are left in place, unless one has the same name as a
mapper the spec lists, which the operator then updates, or the app enforces its groups itself (below).

While `auth.groups` is set, the gateway only trusts the `groups` claim if every other mapper that may write it into the
access token is an `oidc-group-membership-mapper` with `full.path: "true"`. Keycloak merges the values of
group-membership mappers, so such a mapper only adds the user's real group paths. With `full.path: "false"` it emits
group *names*, and Keycloak lets a group be named `/finance`, so whoever can name a group could hand its members a rule
value. Any other mapper type can inject values or replace the claim. The check follows how Keycloak places a claim: it
also covers claim names with backslashes or dots that land in the top-level `groups` claim, and mappers that only
write lightweight access tokens. When a mapper fails it, and the groups are enforced at the gateway, the policy is
deny-all with reason `GroupsClaimConflict`, naming the mapper, until the mapper is removed or fixed. The deny-all
holds even when a later provisioning step fails, and when provisioning fails before the check can run while the
policy is already deny-all.

With `enforceAtGateway: false` the gateway enforces nothing and has no deny-all to fall back on, while the application
trusts the claim. So the operator deletes such mappers instead, as it did before mappers were merged, and reports each
deletion as a Warning event with reason `GroupsMappersRemoved` naming the mapper. `AuthReady` stays `True`. The
application may read the ID token or userinfo as well as the access token, so in this mode a mapper writing the claim
into any of them counts. Full-path group-membership mappers, mappers for other claims, and the mappers the spec lists
are kept.

The check runs whenever the operator provisions the client: when the auth config changes, on a forced reprovision,
and on every pass while `AuthReady` is not `True`. A mapper added by hand after that is only seen on the next
provisioning pass.

`full.path: "true"` is required, not a guarantee for nested paths: with Keycloak's escape-slashes option off (the
default), a top-level group named `team/admins` has the same path as the real `/team/admins`. Top-level rule paths are
not affected, because sibling group names are unique. Mappers on the client's realm client scopes are not checked.
Keycloak attaches every realm default and optional client scope to a new client, and the optional `microprofile-jwt`
scope writes the user's realm-role names into the `groups` claim of the access token when a login requests it, so a
realm role named like a rule path would pass the gateway rule.

**Removal delay.** Group membership is read from the access token, so removing a user from a group takes effect when
their token expires: the realm's access token lifespan, 5 minutes by default.

**Token size.** A user in many groups produces a large access token, and browsers cap a cookie near 4 KB. Users with
very many group memberships may fail to sign in.

**Unsupported configurations.** The operator writes a deny-all policy (`authorization.defaultAction: Deny` with no
rules) rather than deleting the policy or leaving it open:

| Reason | When | Conditions |
|--------|------|------------|
| `GroupsRequireKeycloak` | `generic-oidc` with groups and `enforceAtGateway: true` | `AuthReady=False`, `Ready=False` |
| `GroupsClaimConflict` | `enforceAtGateway: true`, and a mapper in `keycloakConfig.protocolMappers` or on the app's Keycloak client may write the `groups` claim into the access token and is not an `oidc-group-membership-mapper` with `full.path: "true"` | `AuthReady=False`, `Ready=False` |
| `GroupsEmpty` | Every `groups` entry is empty after normalization, for example `"/"` or whitespace | `AuthReady=False`, `Ready=False` |

In all three cases `Ready` carries the same reason, and `observedGeneration` and `status.serviceDiscovery` are still
published.

With `enforceAtGateway: false` the operator provisions the claim but writes no authorization rules, and `AuthReady` is
`True` with reason `GroupsEnforcedByApplication`.

**Fail closed.** If provisioning, RBAC, token exchange or validation fails while groups are enforced (for example,
Keycloak is unreachable during provisioning), the operator still tries to write the enforced policy, so the app denies
users instead of opening up. This does not hold when the SecurityPolicy write itself is rejected, when the provider is
not registered, or when reconcile stops before the auth step (missing Service, namespace opt-in removed, TLS
certificate pending). The previous policy then stays in place, and while the certificate is pending `Ready` stays
`True`. A path that does not exist is not a failure: see `GroupsNotResolved` above.

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

With `auth.groups` set and `enforceAtGateway: false`, the reason is `GroupsEnforcedByApplication` instead:

```yaml
conditions:
  - type: AuthReady
    status: "True"
    reason: GroupsEnforcedByApplication
    message: "Authentication configured with provider keycloak; enforceAtGateway is false, so the application must check the groups claim"
```

**On Failure:**
```yaml
conditions:
  - type: AuthReady
    status: "False"
    reason: ProvisioningFailed | ValidationFailed | SecurityPolicyFailed | GroupsNotResolved | GroupsRequireKeycloak | GroupsClaimConflict
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
- `Warning/GroupsRequireKeycloak`: "spec.auth.groups requires provider \"keycloak\" for gateway enforcement; all requests are denied"
- `Warning/GroupsClaimConflict`: "{mapper conflict}; all requests are denied"
- `Warning/GroupsNotResolved`: "spec.auth.groups path(s) do not exist in Keycloak: {paths}"
  — partial-success condition; the SecurityPolicy is still created and the reconciler
  retries on the next requeue (see [Client Provisioning](#2-client-provisioning)).

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
- `KEYCLOAK_URL`: Keycloak URL for admin API (default:
  `http://keycloak-keycloakx-http.keycloak.svc.cluster.local:8080`)
- `KEYCLOAK_REALM`: Keycloak realm (default: `nebari`)
- `KEYCLOAK_ADMIN_SECRET_NAME`: Secret containing master realm admin credentials (default:
  `nebari-realm-admin-credentials`)
- `KEYCLOAK_ADMIN_SECRET_NAMESPACE`: Namespace of admin secret (default: `keycloak`)
- `KEYCLOAK_EXTERNAL_URL`: Publicly routable Keycloak base URL, including any context path
  (e.g. `https://keycloak.example.com`). Drives the browser-facing OIDC endpoints, the
  SecurityPolicy issuer and the client Secret's `issuer-url`. Required on Envoy Gateway
  v1.9.1+ (see [Issuer Semantics](#issuer-semantics-keycloak)).

**Keycloak Issuer URL Components (for Envoy Gateway):**

These build the in-cluster realm URL, used for the SecurityPolicy `tokenEndpoint` and, when
`KEYCLOAK_EXTERNAL_URL` is unset, for `provider.issuer`:
- `KEYCLOAK_ISSUER_SERVICE_NAME`: Kubernetes service name (default: `keycloak-keycloakx-http`)
- `KEYCLOAK_ISSUER_SERVICE_NAMESPACE`: Keycloak namespace (default: `keycloak`)
- `KEYCLOAK_ISSUER_SERVICE_PORT`: Service port (default: `8080`)
- `KEYCLOAK_ISSUER_CONTEXT_PATH`: HTTP context path (default: `""` for Keycloak 26+; set to `/auth` for older versions)

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
