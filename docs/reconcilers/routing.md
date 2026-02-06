# Routing Reconciler

> **Part of:** [Reconciler Architecture](README.md) **Phase:** 2 of 3 (Validation → Routing → Authentication)
> **Purpose:** Configure HTTP/HTTPS routing via Gateway API

## Overview

The NIC Operator integrates with the foundational infrastructure's Gateway API resources to provide dynamic routing for
NebariApp instances. This document explains how the operator interacts with the pre-configured Envoy Gateway,
cert-manager, and related resources.

## Foundational Infrastructure Components

### Gateway Configuration

The foundational infrastructure deploys a shared Gateway resource that serves as the entry point for all applications:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: nebari-gateway
  namespace: envoy-gateway-system
spec:
  gatewayClassName: envoy-gateway
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes:
        namespaces:
          from: All
    - name: https
      protocol: HTTPS
      port: 443
      allowedRoutes:
        namespaces:
          from: All
      tls:
        mode: Terminate
        certificateRefs:
          - name: nebari-gateway-tls
            kind: Secret
```

**Key Properties:**
- **Name**: `nebari-gateway`
- **Namespace**: `envoy-gateway-system`
- **Gateway Class**: `envoy-gateway`
- **Listeners**: HTTP (80) and HTTPS (443)
- **TLS Certificate**: `nebari-gateway-tls` (wildcard cert for `*.nebari.local`)
- **Route Namespaces**: Allows routes from all namespaces

### GatewayClass

The Gateway uses the Envoy Gateway controller:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: envoy-gateway
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
```

### TLS Certificate Management

#### Wildcard Certificate

The foundational infrastructure provisions a wildcard certificate for all subdomains:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nebari-gateway-cert
  namespace: envoy-gateway-system
spec:
  secretName: nebari-gateway-tls
  duration: 8760h # 1 year
  renewBefore: 720h # 30 days
  issuerRef:
    name: selfsigned-issuer  # or letsencrypt-issuer for production
    kind: ClusterIssuer
  commonName: "*.nebari.local"
  dnsNames:
    - "*.nebari.local"
    - "nebari.local"
    - "keycloak.nebari.local"
    - "argocd.nebari.local"
```

**Certificate Details:**
- **Secret Name**: `nebari-gateway-tls`
- **Pattern**: Wildcard (`*.nebari.local`)
- **Renewal**: 30 days before expiration
- **Storage**: Secret in `envoy-gateway-system` namespace

#### ClusterIssuer

For production environments with Let's Encrypt:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-issuer
spec:
  acme:
    email: admin@example.com
    server: https://acme-v02.api.letsencrypt.org/directory
    privateKeySecretRef:
      name: letsencrypt-account-key
    solvers:
      - http01:
          gatewayHTTPRoute:
            parentRefs:
              - name: nebari-gateway
                namespace: envoy-gateway-system
                kind: Gateway
```

**HTTP-01 Challenge**: Uses the Gateway API HTTP-01 solver, which creates temporary HTTPRoutes for ACME challenges.

## NebariApp HTTPRoute Generation

### Basic HTTPRoute Structure

When a NebariApp is created, the operator generates an HTTPRoute resource:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: myapp-route
  namespace: myapp-namespace
  labels:
    app.kubernetes.io/name: nicapp
    app.kubernetes.io/instance: myapp
    app.kubernetes.io/managed-by: nic-operator
spec:
  parentRefs:
    - name: nebari-gateway
      namespace: envoy-gateway-system
  hostnames:
    - "myapp.nebari.local"
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: myapp-service
          port: 8080
```

### Parent Reference Configuration

**Critical**: HTTPRoutes must reference the Gateway in the `envoy-gateway-system` namespace:

```yaml
spec:
  parentRefs:
    - name: nebari-gateway
      namespace: envoy-gateway-system
      sectionName: https  # or "http" when TLS is disabled
```

The `sectionName` field determines which Gateway listener to use:
- `https` - Uses the HTTPS listener (port 443) with TLS termination (default)
- `http` - Uses the HTTP listener (port 80) without TLS (when `routing.tls.enabled: false`)

This allows the operator to create HTTPRoutes in application namespaces while referencing the shared Gateway.

### Hostname Configuration

The operator uses the `hostname` field from the NebariApp spec:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: myapp
  namespace: myapp-namespace
spec:
  hostname: myapp.nebari.local  # Must match wildcard cert pattern
  service:
    name: myapp-service
    port: 8080
```

**Hostname Requirements:**
- Must be a subdomain of the wildcard certificate (`*.nebari.local`)
- Must be a valid DNS name
- Must be unique across all NebariApp resources

### Path-Based Routing

#### Default Routing (No Paths Specified)

When no routes are specified in the NebariApp, the operator creates an HTTPRoute with an empty `matches` array. The
Gateway API (Envoy Gateway) automatically adds a default path match of `"/"` with type `PathPrefix` when the matches
array is empty or null.

**NebariApp spec (no routes):**
```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
spec:
  hostname: myapp.nebari.local
  service:
    name: myapp-service
    port: 8080
  routing:
    tls:
      enabled: true
  # No routes specified
```

**Resulting HTTPRoute (after Gateway API adds default):**
```yaml
rules:
  - matches:
      - path:
          type: PathPrefix
          value: /
    backendRefs:
      - name: myapp-service
        port: 8080
```

**Note:** The `"/"` path match is added automatically by the Gateway API implementation, not by the operator.

#### Custom Path Routing

When specific paths are configured:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
spec:
  hostname: myapp.nebari.local
  service:
    name: myapp-service
    port: 8080
  routing:
    routes:
      - pathPrefix: /api/v1
        pathType: PathPrefix
      - pathPrefix: /app
        pathType: Exact
```

**Generates (single rule with multiple matches):**

```yaml
rules:
  - matches:
      - path:
          type: PathPrefix
          value: /api/v1
      - path:
          type: Exact
          value: /app
    backendRefs:
      - name: myapp-service
        port: 8080
```

**Note:** All path rules are combined into a single HTTPRoute rule with multiple matches, following Gateway API best
practices. All matches route to the same backend service.

### Backend References

The operator creates backend references using the service details from the NebariApp spec:

```yaml
backendRefs:
  - name: myapp-service  # From spec.service.name
    port: 8080           # From spec.service.port
```

**Important**: The backend service must:
- Exist in the same namespace as the NebariApp
- Expose the specified port
- Be validated during core reconciliation

## TLS Configuration

### TLS Termination (Default)

By default, NebariApps use TLS termination with the wildcard certificate provisioned by the foundational infrastructure:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
spec:
  hostname: myapp.nebari.local
  service:
    name: myapp-service
    port: 8080
  # TLS enabled by default
  routing:
    tls:
      enabled: true  # default, can be omitted
```

**Generated HTTPRoute:**
```yaml
spec:
  parentRefs:
    - name: nebari-gateway
      namespace: envoy-gateway-system
      sectionName: https  # References HTTPS listener
```

**Flow:**
1. HTTPRoute created with `sectionName: https` to reference HTTPS listener
2. Gateway HTTPS listener (443) uses existing `nebari-gateway-tls` secret
3. TLS termination handled at Gateway level by envoy-gateway
4. Traffic forwarded to backend service over HTTP

**Important**: The operator does **not** manage TLS certificates or Gateway TLS configuration. This is handled by:
- **cert-manager**: Provisions and renews certificates
- **Gateway**: Configures TLS listeners and termination
- **NebariApp**: Only controls whether HTTPRoute references HTTPS (`sectionName: https`) or HTTP (`sectionName: http`)
  listeners via the `routing.tls.enabled` field

### Disable TLS (HTTP Only)

To disable TLS termination and use HTTP only:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
spec:
  hostname: http-app.nebari.local
  service:
    name: myapp-service
    port: 8080
  routing:
    tls:
      enabled: false  # Use HTTP listener only
```

**Generated HTTPRoute:**
```yaml
metadata:
  annotations:
    nebari.dev/tls-enabled: "false"
spec:
  parentRefs:
    - name: nebari-gateway
      namespace: envoy-gateway-system
      sectionName: http  # References HTTP listener (port 80)
```

**Note**: This is typically only needed for:
- Development/testing environments
- Internal services that handle their own TLS
- Services that require HTTP for specific protocols

## Gateway Selection

### Public Gateway (Default)

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
spec:
  gateway: public  # or omit for default
  hostname: myapp.nebari.local
```

Routes to: `nebari-gateway` in `envoy-gateway-system`

### Internal Gateway (Future)

For internal-only services:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
spec:
  gateway: internal
  hostname: internal-app.nebari.local
```

Routes to: `nebari-internal-gateway` in `envoy-gateway-system` (if deployed)

## Routing Reconciliation Flow

### 1. Gateway Validation

Before creating HTTPRoute, the operator validates:
- Gateway exists in `envoy-gateway-system`
- Gateway is in Ready state
- Gateway has appropriate listeners configured

### 2. HTTPRoute Creation

```go
func (r *RoutingReconciler) ReconcileRouting(ctx context.Context, nebariApp *NebariApp) error {
    // 1. Determine gateway name based on spec.gateway
    gatewayName := r.getGatewayName(nebariApp)

    // 2. Validate gateway exists
    if err := r.validateGateway(ctx, gatewayName); err != nil {
        // Set condition: RoutingReady=False, Reason=GatewayNotFound
        return err
    }

    // 3. Build desired HTTPRoute
    desiredRoute := r.buildHTTPRoute(nebariApp, gatewayName)

    // 4. Create or update HTTPRoute
    // ...

    // 5. Set condition: RoutingReady=True
    return nil
}
```

### 3. Status Updates

The operator maintains the `RoutingReady` condition:

**Success:**
```yaml
status:
  conditions:
    - type: RoutingReady
      status: "True"
      reason: HTTPRouteReady
      message: "HTTPRoute is configured and ready"
```

**Failure:**
```yaml
status:
  conditions:
    - type: RoutingReady
      status: "False"
      reason: GatewayNotFound
      message: "Gateway nebari-gateway not found in namespace envoy-gateway-system"
```

## Example Integration

### Full NebariApp Example

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: jupyter-app
  namespace: jupyter
spec:
  hostname: jupyter.nebari.local
  service:
    name: jupyterhub
    port: 8000
  routing:
    routes:
      - pathPrefix: /
        pathType: PathPrefix
    tls:
      enabled: true
  gateway: public
```

### Generated HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: jupyter-app-route
  namespace: jupyter
  ownerReferences:
    - apiVersion: reconcilers.nebari.dev/v1
      kind: NebariApp
      name: jupyter-app
      controller: true
spec:
  parentRefs:
    - name: nebari-gateway
      namespace: envoy-gateway-system
  hostnames:
    - jupyter.nebari.local
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - name: jupyterhub
          port: 8000
```

### Traffic Flow

```
User Request: https://jupyter.nebari.local/
              ↓
[Gateway Listener (443)]
  - TLS Termination (nebari-gateway-tls)
              ↓
[HTTPRoute Matching]
  - Hostname: jupyter.nebari.local
  - Path: / (PathPrefix)
              ↓
[Backend Service]
  - Service: jupyter/jupyterhub:8000
              ↓
[Pod]
  - JupyterHub application
```

## Troubleshooting

### HTTPRoute Not Created

**Check:**
1. Gateway exists: `kubectl get gateway nebari-gateway -n envoy-gateway-system`
2. NebariApp events: `kubectl describe nebariapp <name> -n <namespace>`
3. Controller logs for validation errors

### Hostname Not Resolving

**Check:**
1. HTTPRoute created: `kubectl get httproute -n <namespace>`
2. Gateway has HTTPS listener configured
3. TLS secret exists: `kubectl get secret nebari-gateway-tls -n envoy-gateway-system`
4. DNS resolves to Gateway external IP

### Certificate Errors

**Check:**
1. Hostname matches wildcard pattern (`*.nebari.local`)
2. Certificate is valid: `kubectl get certificate -n envoy-gateway-system`
3. TLS secret contains valid cert data

### Backend Connection Failures

**Check:**
1. Service exists and is in same namespace
2. Service exposes the correct port
3. Pods are running and healthy
4. HTTPRoute backendRefs match service name/port

## Configuration Constants

The operator uses these constants for infrastructure integration:

```go
const (
    PublicGatewayName    = "nebari-gateway"
    InternalGatewayName  = "nebari-internal-gateway"
    GatewayNamespace     = "envoy-gateway-system"
    GatewayClassName     = "envoy-gateway"
    DefaultTLSSecretName = "nebari-gateway-tls"
)
```

These match the resources deployed by the foundational infrastructure via ArgoCD.

## Related Documentation

- [Core Validation](./core-validation.md)
- [NebariApp API Specification](../api/v1/nebariapp_types.go)
- [Gateway API Documentation](https://gateway-api.sigs.k8s.io/)
- [Envoy Gateway](https://gateway.envoyproxy.io/)
- [cert-manager](https://cert-manager.io/)
