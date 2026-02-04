# NebariApp Configuration Reference

This document provides a comprehensive reference for all available configuration options in the NebariApp Custom
Resource Definition (CRD).

## Table of Contents

- [Overview](#overview)
- [Basic Structure](#basic-structure)
- [Spec Fields](#spec-fields)
  - [hostname](#hostname)
  - [service](#service)
  - [routes](#routes)
  - [tls](#tls)
  - [auth](#auth)
  - [gateway](#gateway)
- [Status Fields](#status-fields)
- [Complete Examples](#complete-examples)

## Overview

The `NebariApp` resource represents an application onboarding intent, specifying how an application should be:
- **Exposed** (routing via HTTPRoute)
- **Secured** (TLS/HTTPS certificates)
- **Protected** (OIDC authentication)

## Basic Structure

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: <app-name>
  namespace: <namespace>
spec:
  hostname: <hostname>
  service:
    name: <service-name>
    port: <port>
  # ... additional configuration
```

## Spec Fields

### hostname

**Type:** `string` (required)

The fully qualified domain name (FQDN) where the application should be accessible. This will be used to generate the
HTTPRoute and configure TLS.

**Validation:**
- Minimum length: 1
- Pattern: Must be a valid DNS hostname (lowercase letters, numbers, hyphens, and dots)
- Examples: `myapp.nebari.local`, `api.example.com`

**Example:**
```yaml
spec:
  hostname: myapp.nebari.local
```



### service

**Type:** `object` (required)

Defines the backend Kubernetes Service that should receive traffic.

#### service.name

**Type:** `string` (required)

The name of the Kubernetes Service in the same namespace as the NebariApp.

**Validation:**
- Minimum length: 1

#### service.port

**Type:** `integer` (required)

The port number on the Service to route traffic to.

**Validation:**
- Minimum: 1
- Maximum: 65535

**Example:**
```yaml
spec:
  service:
    name: my-backend-service
    port: 8080
```



### routes

**Type:** `array` (optional)

Defines path-based routing rules for the application. If not specified, all traffic to the hostname will be routed to
the service. When specified, only traffic matching these path prefixes will be routed.

#### routes[].pathPrefix

**Type:** `string` (required)

The path prefix to match for routing. Traffic matching this prefix will be routed to the service.

**Validation:**
- Must start with `/`
- Examples: `/app-1`, `/api/v1`

#### routes[].pathType

**Type:** `string` (optional)

Specifies how the path should be matched.

**Valid values:**
- `PathPrefix` (default): Match requests with the specified path prefix
- `Exact`: Match requests with the exact path

**Default:** `PathPrefix`

**Example:**
```yaml
spec:
  routes:
    - pathPrefix: /api/v1
      pathType: PathPrefix
    - pathPrefix: /admin
      pathType: Exact
```



### tls

**Type:** `object` (optional)

Configures TLS/HTTPS for the application. If not specified, the application will use the default wildcard certificate.

#### tls.enabled

**Type:** `boolean` (optional)

Determines whether TLS should be configured for this application. When true, the operator will ensure HTTPS is
available.

**Default:** `true`

#### tls.mode

**Type:** `string` (optional)

Determines how TLS certificates are provisioned.

**Valid values:**
- `wildcard` (default): Use the shared wildcard certificate (e.g., `*.nebari.local`)
- `perHost`: Request a dedicated certificate from cert-manager for this hostname

**Default:** `wildcard`

#### tls.issuerRef

**Type:** `object` (optional)

Specifies the cert-manager Issuer/ClusterIssuer to use when `mode` is `perHost`. If not specified, uses the default
ClusterIssuer `nebari-ca-issuer`.

##### tls.issuerRef.name

**Type:** `string` (required)

Name of the Issuer or ClusterIssuer.

##### tls.issuerRef.kind

**Type:** `string` (optional)

Kind of the issuer.

**Valid values:**
- `Issuer`
- `ClusterIssuer`

**Default:** `ClusterIssuer`

**Example:**
```yaml
spec:
  tls:
    enabled: true
    mode: perHost
    issuerRef:
      name: letsencrypt-prod
      kind: ClusterIssuer
```



### auth

**Type:** `object` (optional)

Configures authentication/authorization for the application. When enabled, the application will require OIDC
authentication via a supporting OIDC Provider (currently Keycloak).

#### auth.enabled

**Type:** `boolean` (optional)

Determines whether authentication should be enforced for this application. When true, users must authenticate via OIDC
before accessing the application.

**Default:** `false`

#### auth.provider

**Type:** `string` (optional)

Specifies the authentication provider to use.

**Valid values:**
- `keycloak`

**Default:** `keycloak`

#### auth.clientSecretRef

**Type:** `string` (optional)

References a Kubernetes Secret containing OIDC client credentials. The secret must be in the same namespace as the
NebariApp and contain:
- `client-id`: The OIDC client ID
- `client-secret`: The OIDC client secret

If not specified and Keycloak provisioning is enabled, the operator will create a secret named
`<nebariapp-name>-oidc-client`.

#### auth.scopes

**Type:** `array of strings` (optional)

Defines the OIDC scopes to request during authentication.

**Common scopes:** `openid`, `profile`, `email`, `roles`

#### auth.provisionClient

**Type:** `boolean` (optional)

Determines whether the operator should automatically provision an OIDC client in Keycloak. When true, the operator will
create a Keycloak client and store the credentials in a Secret.

**Default:** `true`

**Example:**
```yaml
spec:
  auth:
    enabled: true
    provider: keycloak
    provisionClient: true
    scopes:
      - openid
      - profile
      - email
```



### gateway

**Type:** `string` (optional)

Specifies which shared Gateway to use for routing.

**Valid values:**
- `public` (default): Use the public-facing gateway
- `internal`: Use the internal gateway

**Default:** `public`

**Example:**
```yaml
spec:
  gateway: public
```



## Status Fields

The status section is managed by the operator and reflects the observed state of the NebariApp.

### conditions

**Type:** `array`

Represents the current state of the NebariApp resource.

**Standard condition types:**
- `RoutingReady`: HTTPRoute has been created and is functioning
- `TLSReady`: TLS certificate is available and configured
- `AuthReady`: Authentication policy is configured (if auth is enabled)
- `Ready`: All components are ready (aggregate condition)

**Common reasons:**
- `Reconciling`: Reconciliation is in progress
- `Available`: The resource is functioning correctly
- `Failed`: Reconciliation failed
- `NamespaceNotOptedIn`: The namespace doesn't have the required label
- `ServiceNotFound`: The referenced service doesn't exist
- `SecretNotFound`: The referenced secret doesn't exist
- `GatewayNotFound`: The target gateway doesn't exist
- `CertificateNotReady`: The cert-manager Certificate is not ready

### hostname

**Type:** `string`

The actual hostname where the application is accessible. This mirrors `spec.hostname` for easy reference.

### gatewayRef

**Type:** `object`

Identifies the Gateway resource that routes traffic to this application.

Fields:
- `name`: Name of the Gateway
- `namespace`: Namespace of the Gateway

### clientSecretRef

**Type:** `object`

Identifies the Secret containing OIDC client credentials (when auth is enabled).

Fields:
- `name`: Name of the Secret
- `namespace`: Namespace of the Secret (optional)



## Complete Examples

### Minimal Configuration

The simplest possible NebariApp that exposes a service with default settings:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: simple-app
  namespace: default
spec:
  hostname: simple-app.nebari.local
  service:
    name: my-service
    port: 8080
```

This will:
- Route all traffic from `simple-app.nebari.local` to `my-service:8080`
- Use the default wildcard TLS certificate
- Use the public gateway
- No authentication required



### Path-Based Routing

Multiple path prefixes routing to the same service:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: api-app
  namespace: default
spec:
  hostname: api.nebari.local
  service:
    name: api-service
    port: 3000
  routes:
    - pathPrefix: /api/v1
      pathType: PathPrefix
    - pathPrefix: /api/v2
      pathType: PathPrefix
    - pathPrefix: /health
      pathType: Exact
```



### Custom TLS Certificate

Using a dedicated certificate from a specific issuer:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: secure-app
  namespace: production
spec:
  hostname: secure.example.com
  service:
    name: secure-service
    port: 443
  tls:
    enabled: true
    mode: perHost
    issuerRef:
      name: letsencrypt-prod
      kind: ClusterIssuer
```



### Protected Application with OIDC

Application requiring authentication via Keycloak:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: protected-app
  namespace: default
spec:
  hostname: protected.nebari.local
  service:
    name: protected-service
    port: 8080
  auth:
    enabled: true
    provider: keycloak
    provisionClient: true
    scopes:
      - openid
      - profile
      - email
      - roles
```



### Full Configuration Example

A comprehensive example using all available options:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: full-featured-app
  namespace: production
spec:
  hostname: app.example.com
  service:
    name: backend-service
    port: 8080
  routes:
    - pathPrefix: /app
      pathType: PathPrefix
    - pathPrefix: /api
      pathType: PathPrefix
  tls:
    enabled: true
    mode: perHost
    issuerRef:
      name: letsencrypt-prod
      kind: ClusterIssuer
  auth:
    enabled: true
    provider: keycloak
    provisionClient: true
    clientSecretRef: my-oidc-secret
    scopes:
      - openid
      - profile
      - email
      - groups
  gateway: public
```



### Internal Gateway Example

Application accessible only through the internal gateway:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: internal-app
  namespace: default
spec:
  hostname: internal.nebari.local
  service:
    name: internal-service
    port: 8080
  gateway: internal
  tls:
    enabled: true
    mode: wildcard
```



## Additional Notes

### Namespace Requirements

The namespace where you deploy a NebariApp may need to be opted-in with specific labels. Check with your cluster
administrator for namespace requirements.

### Service Requirements

- The referenced Kubernetes Service must exist in the same namespace as the NebariApp
- The Service must be listening on the specified port
- The Service should be ready to handle traffic before creating the NebariApp

### Gateway Requirements

- The specified gateway (public or internal) must exist in the cluster
- The gateway must be properly configured with listeners for HTTP/HTTPS traffic

### Certificate Requirements

When using `tls.mode: perHost`:
- The specified cert-manager Issuer/ClusterIssuer must exist
- The Issuer must be able to provision certificates for the specified hostname
- DNS must be properly configured for certificate validation (if using ACME/Let's Encrypt)

### Authentication Requirements

When `auth.enabled: true`:
- A Keycloak instance must be available and configured
- If `provisionClient: true`, the operator needs permissions to create Keycloak clients
- If `clientSecretRef` is specified, the secret must exist and contain valid credentials
