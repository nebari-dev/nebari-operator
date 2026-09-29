# Multi-Port Routes on NebariApp

**Status:** Draft **Author:** @viniciusdc
**Created:** 2026-05-19

This document proposes extending the `NebariApp` routing contract so a
single app can expose **multiple path-based routes that target different
ports on the same backend service** under one hostname. It also tightens
the same-namespace contract by removing `ServiceReference.Namespace`,
and codifies the "one NebariApp = one hostname = one backend Service"
boundary that has been implicit so far.

A separate concern — exposing Envoy `BackendTrafficPolicy` knobs for
streaming/SSE timeouts — is covered in
[`streaming-timeouts.md`](./streaming-timeouts.md) and is not part of
this design.

> **Note on file name.** This document was originally titled
> *multi-backend routes* and proposed a per-route `backend: {name, port}`
> override. Iteration narrowed the scope to *multi-port on a single
> service*. The filename is kept for URL stability; the content reflects
> the narrower design.

## Problem

`NebariApp.spec` today says "one hostname → one Service → one port,
optionally narrowed by path." The `routing.routes[]` list lets users
filter which paths reach the backend, but every route resolves to the
same `{spec.service.name, spec.service.port}` tuple. There is no way
to say "everything under `/api/*` should land on port `8000` and
everything else on port `80`" within a single NebariApp, even though
**a single Kubernetes Service can expose multiple ports** and routing
to different ports per path is a normal Gateway-API pattern.

Concretely:

- Services that expose a UI on one port and an admin or metrics
  endpoint on another can't differentiate routing by port today.
- Services that expose an HTTP API and a long-poll / SSE endpoint on
  separate ports can't apply path-based selection within one NebariApp.
- Charts that would naturally collapse their exposure into one Service
  with two ports are forced to either expose only one port (and lose
  the other) or split into two NebariApps (and duplicate TLS, auth,
  landing-page infrastructure).

The fix is small: let a `RouteMatch` carry an optional `port` that
overrides the default `spec.service.port` for that path.

## Goals

- Let a single NebariApp expose multiple path-based routes under one
  hostname, each optionally targeting a different port on the
  NebariApp's single backend service.
- Keep `spec.service` working unchanged as the default for routes that
  don't override the port — existing NebariApp manifests continue to
  validate and behave identically.
- Make the same-namespace boundary an explicit, enforced contract:
  the operator-generated HTTPRoute must only `backendRef` Services in
  the NebariApp's own namespace.
- Document "one NebariApp = one hostname = one backend Service" as an
  intentional constraint, not an accident of the current schema.

## Non-goals

- **Per-route backend `Service`.** This proposal is intentionally
  narrower than the earlier "per-route `backend: {name, port}`"
  iteration. A NebariApp targets exactly one Service. Use cases that
  genuinely need two Services (e.g. a chart with separate frontend and
  backend Deployments backing separate Services) should either
  consolidate into a single Service exposing multiple ports, or model
  the two halves as two separate NebariApps. Keeping the "one app =
  one Service" boundary keeps TLS, auth, and landing-page concerns
  scoped to one user-visible URL.
- **Multiple hostnames per NebariApp.** Out of scope. If a future
  use case needs it, that's a separate discussion.
- **Cross-namespace backends.** Today's `spec.service.namespace`
  permits this but the operator does not create the `ReferenceGrant`
  the Gateway API requires for it to actually work — so the field has
  been silently incomplete. This proposal *removes* it. Tools that
  need to reach into other namespaces should do so via in-cluster DNS
  (`svc.other-ns.svc.cluster.local`), not via the operator's
  HTTPRoute.
- **Weights, header/query matchers, request/response filters.** This
  proposal narrows on per-route port selection. Anything else
  Gateway API offers on `HTTPRouteRule` stays out until a use case
  demands it.
- **Envoy `BackendTrafficPolicy` (streaming/SSE timeouts).** Covered
  in a sibling design: [`streaming-timeouts.md`](./streaming-timeouts.md).
  Independent of this one.

## Design principles

Same principles that govern the rest of the NebariApp contract:

1. **No is temporary, yes is forever.** Per-route `port` is the only
   field this design adds to `RouteMatch`. Nothing speculative.
2. **Contract independence.** The CRD shape stays expressible in the
   Gateway API's mechanics without leaking Envoy specifics. A
   per-route `port` maps directly to the `port` field of an
   `HTTPRouteRule.backendRefs` entry.
3. **Graceful degradation.** A route with no `port` falls back to
   `spec.service.port`. A NebariApp with no `routing.routes` keeps
   current behavior (one rule, single backend ref, `/` prefix).
4. **Same-namespace by construction.** The CRD has no field that lets
   a user express a cross-namespace backend, so the operator never has
   to validate or refuse one.
5. **One Service per NebariApp.** The boundary is intentional: a
   NebariApp's TLS, auth, landing-page card, and routing concerns
   scope to a single backing Service. Use-cases that don't fit that
   boundary split into multiple NebariApps.

## Proposed contract

### `RouteMatch` gains an optional `port`

```go
type RouteMatch struct {
    // PathPrefix specifies the path prefix to match for routing.
    PathPrefix string `json:"pathPrefix"`

    // PathType specifies how the path should be matched.
    PathType string `json:"pathType,omitempty"`

    // Port optionally overrides the default backend port (spec.service.port)
    // for this route. The port must be exposed by spec.service. When omitted,
    // the route forwards to spec.service.port. This is the only mechanism for
    // path-based port differentiation; per-route backend Services are not
    // supported (see Non-goals).
    // +optional
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=65535
    Port *int32 `json:"port,omitempty"`
}
```

### `ServiceReference.Namespace` is removed

```go
type ServiceReference struct {
    // Name is the name of the Kubernetes Service in the NebariApp's
    // own namespace.
    // +kubebuilder:validation:Required
    Name string `json:"name"`

    // Port is the default port number on the Service to route traffic to.
    // +kubebuilder:validation:Required
    Port int32 `json:"port"`
}
```

The current `Namespace` field is dropped. Reasons:

- The operator-generated HTTPRoute would render a `BackendObjectReference`
  with a foreign `Namespace`, which Gateway API requires a
  `ReferenceGrant` to honor — and the operator does not create one.
  The field has always been a half-feature on the operator's HTTPRoute
  path.
- The architectural stance is that pack resources live in the pack's
  own namespace. Cross-namespace pod-to-pod talk goes through in-cluster
  DNS, not through an operator-managed public route.
- All known callers (helm charts under `nebari-dev`) deploy their
  Service into the same namespace as the NebariApp via Argo CD's
  per-application namespace.

### Downstream consumer: nebari-landing

[`nebari-landing`](https://github.com/nebari-dev/nebari-landing) reads
`spec.service.namespace` from the NebariApp CRD to construct in-cluster
health-probe URLs of the form `http://<svc>.<service-namespace>:<port>`.
The probe deliberately bypasses the gateway and TLS, since the goal is
to report **workload** health on the landing-page card, not network-path
health. Reference: `internal/cache/service_cache.go` and
`internal/watcher/watcher.go` in nebari-landing.

The dependency is currently dormant:

- The original consumers — Keycloak and ArgoCD NebariApps in the kind
  dev cluster — have since moved to NIC's foundational Argo-apps layer
  and are no longer modelled as NebariApps. No current production
  NebariApp sets `spec.service.namespace`.
- nebari-landing's watcher uses `unstructured.NestedString` with a
  graceful fallback: when the field is absent (after this CRD change),
  it defaults the probe namespace to the NebariApp's own namespace.
  Removal is therefore non-breaking: every NebariApp the watcher sees
  takes the fallback path, and probes resolve to a same-namespace
  Service.
- A follow-up PR on nebari-landing should drop the now-inert
  `ServiceNamespace` field on its `App` struct, since the fallback
  branch is the only path that ever fires. That cleanup is outside
  this PR but is the natural pair to it.

### `spec.service` stays required and singular

Keeping `spec.service` as a single, required `ServiceReference` means:

- Every existing NebariApp manifest continues to validate.
- Every route, with or without a per-route `port` override, resolves
  to `spec.service.name` — no ambiguity about which Service backs a
  route.
- The simple case (one hostname, one Service, one port, no path-based
  differentiation) stays one struct field.

### One hostname and one Service per NebariApp — codified

The CRD docstring on `spec.hostname` and `spec.service` gains explicit
language:

> Each NebariApp exposes exactly one public hostname and is backed by
> exactly one Kubernetes Service. Packs that need to expose multiple
> hostnames, or that genuinely need to fan out to multiple Services,
> must be split into multiple NebariApps. This is an intentional
> boundary so a NebariApp's TLS, auth, landing-page card, and routing
> concerns all scope to a single user-visible URL backed by a single
> Service.

No schema change — `hostname` and `service` are already singular —
but the constraint moves from accidental to documented.

## End-to-end example

A NebariApp whose Service exposes both a UI port and an API port:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: my-app
  namespace: my-app
spec:
  hostname: my-app.example.com
  service:
    name: my-app-svc           # one Service exposing two ports below
    port: 80                   # default port: UI
  routing:
    routes:
      - pathPrefix: /api
        port: 8000             # this route forwards to my-app-svc:8000
      - pathPrefix: /          # no port → falls back to spec.service.port (80)
  auth:
    enabled: true
    provider: keycloak
```

This emits a single HTTPRoute with two rules — `/api` →
`my-app-svc:8000`, `/` → `my-app-svc:80` — both on hostname
`my-app.example.com`. One Certificate, one SecurityPolicy, one
landing-page card, one Service.

The Service is expected to look like:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-app-svc
  namespace: my-app
spec:
  ports:
    - name: http
      port: 80
      targetPort: 80
    - name: api
      port: 8000
      targetPort: 8000
  selector: { ... }
```

If the route's `port` is not present in `service.spec.ports`, the
operator marks the NebariApp not-Ready with a clear reason (see
*Validation* below).

## Operator changes

Concrete files touched.

### `api/v1/nebariapp_types.go`

- Remove `Namespace` field from `ServiceReference`.
- Add `Port *int32` to `RouteMatch` with `Minimum=1`, `Maximum=65535`,
  optional, default-on-omit.
- Update docstrings on `spec.hostname` and `spec.service` to document
  the one-hostname / one-Service constraint.
- Tighten the existing comment on `ServiceReference` to "must be in
  the NebariApp's own namespace."

### `internal/controller/reconcilers/core/reconciler.go`

`ValidateService`:

- Drop the cross-namespace defaulting branch.
- Look up `spec.service.name` once in the NebariApp's namespace.
- Verify `spec.service.port` is exposed by it (current behavior).
- For each route in `routing.routes[]` and `routing.publicRoutes[]`
  that sets `Port`, verify that port is **also** exposed by the same
  Service. Routes that don't override `Port` inherit the already-validated
  `spec.service.port`.
- A route Port that the Service doesn't expose surfaces as a clear
  error — same pattern as today's "service does not expose port N",
  prefixed with the route's PathPrefix for diagnosability.

### `internal/controller/reconcilers/routing/httproute.go`

- `buildBackendRefs` is simplified: it always references
  `spec.service.name`, with the port resolved per call (either
  `spec.service.port` or the route's override).
- `buildHTTPRouteRules` emits one `HTTPRouteRule` per `RouteMatch`,
  each with its own `backendRefs` (resolved port). When
  `routing.routes` is empty, behavior is unchanged: one rule with
  empty matches (so Gateway API applies the `/` default) and
  `spec.service.port` as the backend.
- `buildPublicHTTPRoute` mirrors the same shape so per-route ports
  work on `publicRoutes[]` too.

### `config/rbac/`

- The `Services` ClusterRole rule can be narrowed from cluster-scoped
  to namespace-scoped reads now that no NebariApp can legally
  reference a Service outside its own namespace. The exact scoping
  belongs in implementation; the design decision is just "tighten."

### Generated artifacts

- `config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml` regenerates.
- `docs/api-reference.md` regenerates.
- The CRD diff will show `service.namespace` removed and
  `routing.routes[].port` added.

## Validation

- `RouteMatch.port` (when set) is validated at the CRD level to be in
  the range `[1, 65535]`.
- The CRD itself cannot enforce "this port is exposed by spec.service"
  — that check stays in the reconciler's `ValidateService` pass.
- Failure mode: NebariApp goes not-Ready with reason indicating which
  route's port is missing from the Service, e.g.
  `route "/api": service my-app-svc does not expose port 8000`.

## Backwards compatibility

- **Existing manifests:** any NebariApp that did not set
  `spec.service.namespace` is wholly unaffected — the field's removal
  is invisible.
- **Manifests that did set `spec.service.namespace`:** the API server
  will refuse the field on the new CRD. There is no known internal
  user. The release-notes / changelog must call this out explicitly so
  any external user catches it at upgrade time.
- **The API version stays `v1`.** Field removal would normally argue
  for a version bump, but the project's README explicitly flags the
  API as "may change without notice" during the NIC bring-up phase.
  Once the API is declared stable, this kind of removal should
  require a version bump.

## Migration

For internal callers:

1. **NebariApp manifests** — None. Argo CD installs each pack into a
   single namespace, every surveyed chart already omits
   `spec.service.namespace`, and the foundational services that used
   to set the field (Keycloak, ArgoCD) no longer exist as NebariApps.
2. **nebari-landing** — No immediate action; the watcher's graceful
   fallback covers the field's absence. A follow-up PR should remove
   the inert `ServiceNamespace` field on its `App` struct (see
   *Downstream consumer* above).

For any external caller relying on the field:

1. Move the target Service into the NebariApp's namespace (typical
   case), **or**
2. Keep the Service where it is and have the workload connect to it
   via in-cluster DNS rather than through the NebariApp's HTTPRoute.

## Follow-ups (not in this design)

- **Streaming timeouts.** Covered in
  [`streaming-timeouts.md`](./streaming-timeouts.md). Independent of
  this design; the only intersection is that the policy it proposes
  targets the same HTTPRoutes this design produces — the same policy
  covers all rules.
- **API version bump to a stable channel.** Once the surface settles,
  promoting from `v1` (currently labelled unstable) to a properly
  stable version is its own piece of work. Field removals like the
  one in this design are the last that can happen before that bump.
- **nebari-landing cleanup PR.** Drop the now-inert
  `App.ServiceNamespace` field and the cross-namespace fallback
  branch in `buildHealthCheckConfig` (see *Downstream consumer*).

## Open questions

- **Should `publicRoutes` accept per-route ports too?** For symmetry,
  yes — `publicRoutes` and `routes` share the `RouteMatch` type, so
  the field appears on both automatically. Decision lean: honor on
  both; revisit if there's a security argument against.
- **Status surface.** Should `NebariApp.status` expose per-route
  resolution (which port each route resolved to)? Useful for
  debugging but adds status surface area that the operator must
  maintain. Decision lean: not in v1 of this change; users can
  inspect the rendered HTTPRoute directly.

## References

- Gateway API `HTTPRoute`: <https://gateway-api.sigs.k8s.io/api-types/httproute/>
- `BackendObjectReference` cross-namespace mechanics
  (`ReferenceGrant`): <https://gateway-api.sigs.k8s.io/api-types/referencegrant/>
- Companion concern (streaming/SSE timeouts) PR observed in the
  `openteams-ai/nebari.openteams.ai` deployment repo (PR #12).
