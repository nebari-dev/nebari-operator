# Design: Support pre-provisioned TLS secrets via `routing.tls.secretName`

**Date:** 2026-04-16
**Issue:** [nebari-operator#90](https://github.com/nebari-dev/nebari-operator/issues/90)
**Status:** Approved, ready for implementation planning

## Problem

The operator currently has only two TLS modes:

1. `routing.tls.enabled: true` (default): create a cert-manager `Certificate` via the operator's configured `ClusterIssuer`, add a per-app HTTPS listener pointing at the cert-manager-managed secret.
2. `routing.tls.enabled: false`: HTTP-only listener, no TLS.

This excludes users who:

- Have pre-provisioned certificates from external sources (wildcard certs, corporate PKI).
- Run in air-gapped or restricted environments without ACME access.
- Manage certificate rotation outside Kubernetes.
- Cannot run cert-manager at all.

Workarounds via a CA-based ClusterIssuer exist but are awkward. Users need a first-class way to say "use this existing TLS secret."

## Goals

- Allow a NebariApp to reference a pre-existing Kubernetes TLS secret for its HTTPS listener.
- Preserve existing cert-manager behavior as the default. No breaking changes to current deployments.
- Give users a clear status signal when their supplied secret is missing or malformed.
- Allow operators to deploy the controller without a `ClusterIssuer` configured, as long as every NebariApp brings its own secret (or disables TLS).

## Non-goals

- Supporting arbitrary secret namespaces (stays in the Gateway namespace).
- Validating the certificate's SAN/hostname match against `spec.hostname`.
- Creating or rotating the secret on the user's behalf.
- Integrating with external PKI or secret-management systems (Vault, AWS Secrets Manager, etc.).

## Design

### API change

Extend `RoutingTLSConfig` in `api/v1/nebariapp_types.go` with one optional field:

```go
type RoutingTLSConfig struct {
    // Enabled determines whether TLS termination should be used. (existing)
    Enabled *bool `json:"enabled,omitempty"`

    // SecretName optionally references a pre-existing Kubernetes TLS secret
    // (type kubernetes.io/tls) in the Gateway's namespace (envoy-gateway-system).
    // When set, the operator will NOT create a cert-manager Certificate; instead
    // it points the per-app HTTPS listener at this secret. Use this for
    // pre-provisioned or externally managed certificates (e.g., wildcard certs,
    // air-gapped environments without ACME access). The secret must be created
    // and maintained by the user in the envoy-gateway-system namespace.
    // Mutually exclusive with cert-manager-managed certificates.
    // +optional
    // +kubebuilder:validation:MinLength=1
    SecretName string `json:"secretName,omitempty"`
}
```

**Semantics:**

| Spec                                            | Behavior                                                        |
| ----------------------------------------------- | --------------------------------------------------------------- |
| `tls` omitted, or `tls: {}`, or `enabled: true` | cert-manager Certificate created, HTTPS listener added          |
| `enabled: true, secretName: <name>`             | No Certificate; HTTPS listener points at user's secret          |
| `enabled: false`                                | HTTP-only; `secretName` ignored (documented in godoc)           |

**Naming rationale:** `secretName` matches the well-known `Ingress.tls[].secretName` pattern from core Kubernetes, minimizing docs lookup.

**Namespace:** Secret must live in `envoy-gateway-system` (the Gateway's namespace). Users are responsible for creating and maintaining it. This avoids the complexity of Gateway API `ReferenceGrant` resources and matches where cert-manager-managed secrets already land today.

### Reconciler behavior

Changes in `internal/controller/reconcilers/tls/reconciler.go`.

**Branching in `ReconcileTLS`:**

```text
if !isTLSEnabled:
    # unchanged: skip + TLSReady=False (TLSDisabled)

elif spec.Routing.TLS.SecretName != "":
    # NEW: user-supplied secret path
    cleanupOwnedCertificate(app)           # migration from cert-manager
    ensurePerAppListener(app.Spec.Routing.TLS.SecretName)
    setTLSReadyFromSecretCheck(app.Spec.Routing.TLS.SecretName)

else:
    # existing cert-manager path, unchanged
    validateClusterIssuer()
    ensureCertificate(app)
    ensurePerAppListener(naming.CertificateSecretName(app))
    setTLSReadyFromCertCondition()
```

**Cluster-issuer bypass:** When `SecretName` is set, skip the `ClusterIssuerName == ""` hard-fail at `reconciler.go:94-98`. An install without cert-manager and without a `ClusterIssuer` becomes valid as long as every NebariApp either supplies its own `secretName` or disables TLS.

**Migration cleanup (`cleanupOwnedCertificate`):** New helper that deletes the `Certificate` resource named by `naming.CertificateSecretName(app)` in the app's namespace if it exists and is owned by this NebariApp. Runs every reconcile when `secretName` is set - idempotent no-op if nothing to clean. Cert-manager's owner-reference GC then removes the Certificate's managed secret.

**Listener construction:** The existing Gateway listener patch at `reconciler.go:252-254` already takes a secret name parameter. Pass the user's `secretName` instead of `naming.CertificateSecretName(app)`. No new listener logic required.

**Soft-fail status check:** Best-effort `Get` of the secret in `envoy-gateway-system`:

- Found + type `kubernetes.io/tls`: `TLSReady=True`, reason `UserProvidedSecretReady`
- Missing: `TLSReady=False`, reason `UserProvidedSecretNotFound`, warning event. **Listener is still added** so Envoy picks the secret up the moment the user creates it.
- Found but wrong type: `TLSReady=False`, reason `UserProvidedSecretInvalidType`, warning event. Listener still added.

The operator does not block reconciliation on the secret - it reports status and lets Envoy Gateway surface the actual attachment error downstream.

### Deletion & finalizer

No changes to the finalizer path in `nebariapp_controller.go`.

- User's secret is **never deleted** by the operator (it didn't create it).
- Per-app listener is removed from the Gateway as it is today.
- Any owned `Certificate` left behind from a prior cert-manager configuration is GC'd via owner references.

### Status & events

**New condition reasons** in `api/v1/nebariapp_types.go`:

```go
ReasonUserProvidedSecretReady       = "UserProvidedSecretReady"
ReasonUserProvidedSecretNotFound    = "UserProvidedSecretNotFound"
ReasonUserProvidedSecretInvalidType = "UserProvidedSecretInvalidType"
```

**Condition matrix:**

| Scenario                                   | `TLSReady` | Reason                          |
| ------------------------------------------ | ---------- | ------------------------------- |
| `tls.enabled: false`                       | False      | `TLSDisabled` (existing)        |
| cert-manager, cert ready                   | True       | existing                        |
| cert-manager, cert not ready               | False      | existing                        |
| `secretName` set, secret ready             | True       | `UserProvidedSecretReady`       |
| `secretName` set, secret missing           | False      | `UserProvidedSecretNotFound`    |
| `secretName` set, secret wrong type        | False      | `UserProvidedSecretInvalidType` |

**New events** (via `record.EventRecorder`):

- `EventReasonUserProvidedSecretInUse` - Normal, on transition to Ready
- `EventReasonUserProvidedSecretNotFound` - Warning
- `EventReasonUserProvidedSecretInvalid` - Warning

**Logging:** Add `logger.Info("using user-provided TLS secret", "secret", secretName)` on the user-secret path.

### RBAC

The operator already has RBAC to patch the Gateway in `envoy-gateway-system`. Verify it has `get` permission on `secrets` in that namespace; if not, add a kubebuilder RBAC marker to the TLS reconciler and regenerate `config/rbac/role.yaml` via `make manifests`.

### Testing

**Unit tests** in `internal/controller/reconcilers/tls/reconciler_test.go`, table-driven per project convention:

1. `secretName` set + valid `kubernetes.io/tls` secret exists: listener added with user's secret name, no Certificate created, `TLSReady=True` / `UserProvidedSecretReady`.
2. `secretName` set + secret missing: listener still added, `TLSReady=False` / `UserProvidedSecretNotFound`, warning event.
3. `secretName` set + secret exists with type `Opaque`: listener still added, `TLSReady=False` / `UserProvidedSecretInvalidType`.
4. `secretName` set + `ClusterIssuerName` empty: reconcile succeeds (bypass of empty-issuer hard-fail).
5. Migration: existing owned `Certificate` + `secretName` newly set: Certificate deleted, listener repointed at user's secret.
6. `secretName` unset (regression): existing cert-manager path unchanged.

**Controller integration test** in `internal/controller/nebariapp_controller_test.go`: end-to-end NebariApp with `secretName` set produces the expected HTTPRoute + Gateway listener + no Certificate.

**E2E test** in `test/e2e/`:

- Pre-create a self-signed TLS secret in `envoy-gateway-system`.
- Create a NebariApp with `routing.tls.secretName` pointing at it.
- Assert `TLSReady=True`, listener attached, HTTPS request succeeds and serves the expected cert.
- Excluded from the smoke subset to keep it fast; included in the full e2e matrix.

**CRD regen:** run `make manifests generate` after editing `nebariapp_types.go` per `CLAUDE.md`. Also run `make docs` to regenerate `docs/api-reference.md` from the Go types.

**Docs:** update `docs/configuration-reference.md` and `docs/reconcilers/routing.md` with a `secretName` example and the "user must create the secret in `envoy-gateway-system`" note.

## Alternatives considered

**Secret in the NebariApp's own namespace via `ReferenceGrant`.** Operationally friendlier - users have permissions in their own namespace - but adds a second managed resource type (`ReferenceGrant`) and another failure mode. Rejected in favor of matching cert-manager's existing namespace.

**Hard-fail when the supplied secret is missing or malformed.** Clearer error, but fragile: if a user creates the NebariApp before the secret, reconciliation blocks until the secret appears. Soft-fail + status reporting gives the same signal without breaking the "operator manages what's in spec" flow.

**Immutable TLS mode (no flipping between cert-manager and `secretName`).** Simpler reconciler, but surprising to users - every other field on `NebariApp` is mutable. Accepted the small cost of migration cleanup to keep the UX consistent.

**Structured `SecretRef { name, namespace }` field.** More extensible, but we've locked the namespace to `envoy-gateway-system`, so a plain `secretName: string` is simpler and matches `Ingress.tls[].secretName` precedent.

## Open questions

None.
