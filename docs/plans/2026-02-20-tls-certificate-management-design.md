# TLS Certificate Management Design

**Issue:** [#46](https://github.com/nebari-dev/nebari-operator/issues/46)
**Date:** 2026-02-20
**Status:** Approved

## Problem

The operator claims to handle TLS management, but it only toggles between HTTP and HTTPS Gateway listeners. It does not create cert-manager Certificate resources or manage Gateway TLS listeners. In production setups with per-subdomain certificates, every new NebariApp hostname requires manual certificate intervention.

## Approach

Create a new TLS reconciler that:
1. Creates a per-app cert-manager Certificate in the Gateway namespace
2. Patches the shared Gateway to add a per-app HTTPS listener
3. Updates the HTTPRoute to reference the per-app listener

## API Changes

No new fields on `RoutingTLSConfig`. The existing `routing.tls.enabled` field continues to control whether TLS is used.

The ClusterIssuer name is configured at the operator level via environment variable (`TLS_CLUSTER_ISSUER_NAME`), not per-app. A new `TLSConfig` struct in `internal/config/` loads this.

The already-defined `ConditionTypeTLSReady` and `ReasonCertificateNotReady` constants are now used.

## TLS Reconciler

New package: `internal/controller/reconcilers/tls/`

### Struct

```go
type TLSReconciler struct {
    Client            client.Client
    Scheme            *runtime.Scheme
    Recorder          record.EventRecorder
    ClusterIssuerName string
}
```

### ReconcileTLS Flow

1. Skip if TLS is disabled (`routing.tls.enabled == false`). Set `TLSReady=False` with reason "TLSDisabled".
2. Create/update a cert-manager Certificate in `envoy-gateway-system`:
   - Name: `<nebariapp-name>-<namespace>-cert`
   - `spec.secretName`: `<nebariapp-name>-<namespace>-tls`
   - `spec.dnsNames`: `[nebariApp.Spec.Hostname]`
   - `spec.issuerRef`: configured ClusterIssuer
   - Labels for tracking ownership
3. Patch the shared Gateway to add a per-hostname HTTPS listener:
   - Listener name: `tls-<nebariapp-name>-<namespace>`
   - `hostname`: the NebariApp hostname
   - `protocol`: HTTPS, `port`: 443
   - `tls.certificateRefs`: the per-app secret
   - `tls.mode`: Terminate
4. Check Certificate readiness:
   - Ready: set `TLSReady=True`
   - Not ready: set `TLSReady=False` with `ReasonCertificateNotReady`, return `RequeueAfter: 30s`
5. Return `TLSResult` with listener name for the Routing reconciler.

### TLSResult

```go
type TLSResult struct {
    ListenerName string  // e.g., "tls-myapp-default"
    SecretName   string  // e.g., "myapp-default-tls"
    CertReady    bool
}
```

### CleanupTLS Flow

1. Remove the per-app listener from the Gateway (patch).
2. Delete the Certificate resource from the gateway namespace.
3. Secret cleanup is handled automatically by cert-manager.

### Cross-Namespace Ownership

The Certificate lives in the Gateway namespace, not the NebariApp namespace. Since `SetControllerReference` requires same-namespace resources, ownership is tracked via labels (`nebari.dev/nebariapp-name`, `nebari.dev/nebariapp-namespace`). Cleanup is explicit via the existing finalizer.

### Gateway Patching

Multiple NebariApps may concurrently patch the shared Gateway. The reconciler uses optimistic concurrency: read the Gateway, modify listeners, update with the resource version. On conflict, return error to trigger retry via the controller's requeue mechanism.

## Pipeline Integration

Pipeline changes from `Core -> Routing -> Auth` to `Core -> TLS -> Routing -> Auth`.

TLS runs before Routing because:
- TLS creates the Gateway listener
- Routing needs the listener name for HTTPRoute's `parentRef.sectionName`

### Changes to NebariAppReconciler

- New field: `TLSReconciler *tls.TLSReconciler`
- Initialized in `main.go` with ClusterIssuer name from env
- Called between Core and Routing reconciliation
- Cleanup called in the finalizer cleanup function

### Changes to RoutingReconciler

- `buildHTTPRoute` accepts an optional listener name override
- When TLS provides a per-app listener name, use it as `sectionName`
- When TLS is disabled, fall back to `"http"`
- When no TLS reconciler is configured (backwards compat), fall back to `"https"`

### RBAC Changes

- Gateway verbs: add `update` and `patch` to existing `get;list;watch`

## Configuration

### New Environment Variable

- `TLS_CLUSTER_ISSUER_NAME`: name of the ClusterIssuer (required when TLS is enabled)

### New Config Struct

```go
// internal/config/tls.go
type TLSConfig struct {
    ClusterIssuerName string
}
```

### New Go Dependency

- `github.com/cert-manager/cert-manager` for Certificate CRD types

## Testing

### Unit Tests (table-driven, fake client)

- TLS disabled: skip, set TLSReady=False
- TLS enabled: creates Certificate with correct spec
- TLS enabled: patches Gateway with per-app listener
- Certificate already exists: updates idempotently
- Certificate ready: TLSReady=True, correct listener name returned
- Certificate not ready: TLSReady=False, CertificateNotReady reason
- Gateway conflict: retries
- Cleanup: removes listener and deletes Certificate
- Cleanup when already deleted: no error
- Multiple apps: unique names per app

### E2E Tests

- Certificate created in envoy-gateway-system
- Certificate reaches Ready state
- Gateway has per-app listener
- HTTPRoute references per-app listener
- Cleanup on deletion removes listener and Certificate

### Existing Test Updates

- Routing tests: account for sectionName coming from TLS result
- Gateway tests: account for dynamically-added listeners
