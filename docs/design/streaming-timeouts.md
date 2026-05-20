# Streaming Timeouts on NebariApp

**Status:** Draft **Author:** @viniciusdc
**Created:** 2026-05-20

This document proposes a small extension to the `NebariApp` routing
contract: an opt-in `routing.streaming: true` flag that makes the
operator emit an Envoy Gateway `BackendTrafficPolicy` covering the
NebariApp's HTTPRoutes, disabling Envoy's default 15-second HTTP
request timeout and setting a 5-minute connection idle timeout. The
flag is the only surface change; the policy values are fixed canned
defaults, not exposed on the CRD.

The companion proposal
[`multi-backend-routes.md`](./multi-backend-routes.md) (per-route port
overrides and `ServiceReference.Namespace` removal) is independent and
can land in either order; their only intersection is that this design's
policy targets the HTTPRoutes the other one produces.

## Problem

Envoy Gateway's default HTTP request timeout is **15 seconds**. Any
request that doesn't complete its full request/response cycle within
15s gets cut off. That breaks every common long-lived HTTP pattern:

- **Server-Sent Events (SSE).** The whole point is an open response
  stream that emits over minutes or hours.
- **Long-poll.** Clients hold connections open waiting for server
  events; a 15s cap forces constant reconnects and breaks the
  semantics.
- **gRPC streaming** (server-streaming, bidi). Same shape, same
  failure.
- **WebSocket upgrades.** Less affected once the upgrade completes
  (Envoy handles those separately), but the initial request still
  needs to complete within the timeout.

Today, packs that need any of the above have two options, both bad:

1. **Hand-roll a `BackendTrafficPolicy`.** Authoritative example:
   PR `openteams-ai/nebari.openteams.ai#12` adds two separate
   `BackendTrafficPolicy` resources, each `targetRefs`-ing the
   operator-generated HTTPRoute by name (`nebari-chat-backend-route`,
   `nebari-chat-frontend-route`). The contract is fragile — the
   operator could rename its HTTPRoute and the policy silently stops
   matching — and pack authors shouldn't need to learn the Envoy
   `gateway.envoyproxy.io/v1alpha1` schema to ship a chat app.
2. **Bypass the operator entirely.** Hand-roll the HTTPRoute too,
   losing TLS / auth / landing-page integration. Strictly worse.

The fix is small: a boolean intent on `RoutingConfig` that the
operator translates into a managed `BackendTrafficPolicy` with the
right `targetRefs` and a fixed timeout spec.

## Goals

- Let a NebariApp opt into long-lived connection support with a single
  boolean, without forcing pack authors to learn Envoy timeout
  semantics.
- Express the intent ("this app holds connections open"), not the
  mechanism — the operator picks canned timeout values that match
  what every streaming workload actually wants.
- Keep the lifecycle owned by the operator: the policy is created,
  updated, and garbage-collected with the NebariApp.

## Non-goals

- **Per-route streaming.** Streaming is a per-app concern, not a
  per-route concern. The policy covers every HTTPRoute the operator
  owns for the NebariApp.
- **Exposing Envoy timeout knobs directly on the CRD.** The flag is
  intentionally a boolean intent. Rationale below.
- **Other `BackendTrafficPolicy` features** (rate limiting, circuit
  breaking, retries, fault injection). All out of scope until a
  concrete use case demands them. Adding them later as additional
  fields under `routing` is additive.
- **WebSocket-specific tuning.** Envoy treats upgraded connections
  separately; this design covers the request-timeout path, which is
  what breaks SSE / long-poll / gRPC streaming. WebSocket-specific
  knobs (if ever needed) are a separate proposal.

## Design principles

Same principles that govern the rest of the NebariApp contract:

1. **No is temporary, yes is forever.** One boolean field. No
   speculation about what other timeouts users might someday want to
   tune.
2. **Contract independence.** A boolean is expressible against any
   future gateway implementation. Envoy-typed timeout fields would
   pin the CRD to the current gateway choice.
3. **Graceful degradation.** Absent or `false` → no policy created;
   existing apps are unaffected. The operator removes any
   previously-created policy when the flag is unset.
4. **Operator owns its resources.** The policy is owner-referenced to
   the NebariApp, so deletion of the NebariApp tears it down.

## Proposed contract

### `RoutingConfig` gains an opt-in `streaming` flag

```go
type RoutingConfig struct {
    // ... existing fields (Routes, PublicRoutes, TLS, Annotations) ...

    // Streaming enables long-lived connection support for this NebariApp.
    // When true, the operator emits an Envoy Gateway BackendTrafficPolicy
    // targeting this app's operator-owned HTTPRoutes that:
    //   - disables the default HTTP request timeout (15s -> no limit), and
    //   - sets the connection idle timeout to 5 minutes.
    // Use this for Server-Sent Events (SSE), long-poll endpoints,
    // gRPC streaming, or any workload that holds a connection open
    // beyond a few seconds. When false or omitted, no policy is created
    // and Envoy's default timeouts apply.
    // +optional
    Streaming bool `json:"streaming,omitempty"`
}
```

When `streaming: true`, the operator manages a single
`BackendTrafficPolicy` (group `gateway.envoyproxy.io/v1alpha1`) in the
NebariApp's namespace. Spec:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: <nebariapp-name>-streaming
  namespace: <nebariapp-namespace>
  ownerReferences:
    - apiVersion: reconcilers.nebari.dev/v1
      kind: NebariApp
      name: <nebariapp-name>
      uid: <nebariapp-uid>
      controller: true
      blockOwnerDeletion: true
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: <main-httproute-name>
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: <public-httproute-name>   # only when publicRoutes is set
  timeout:
    http:
      requestTimeout: "0s"           # disable the 15s default
      connectionIdleTimeout: 300s    # cap idle connections at 5m
```

One policy, one or two `targetRefs` entries.

### Why not expose Envoy timeout knobs directly?

`routing.streaming: bool` is a high-level intent ("this app holds
connections open") rather than a passthrough of Envoy's timeout schema:

- **Contract independence.** The NebariApp surface should be
  expressible against a different gateway in the future. Two
  Envoy-named timeout fields would tie the CRD to the current
  implementation.
- **The canned values are what every streaming workload actually
  wants.** `requestTimeout: 0s` and `connectionIdleTimeout: 300s`
  match the downstream PR in `openteams-ai/nebari.openteams.ai#12`
  that motivated this work, the Envoy Gateway streaming
  documentation, and common practice for SSE backends. Exposing them
  as separate fields invites bikeshedding without solving a real use
  case.
- **Widening later is additive.** If a future workload genuinely
  needs different values, we can introduce
  `routing.streamingTimeouts: { requestTimeout, idleTimeout }` later
  and existing `streaming: true` consumers keep working unchanged.

### Why target both main and public HTTPRoutes?

`routing.publicRoutes` produces a separate HTTPRoute that bypasses the
operator's `SecurityPolicy`. Public routes can legitimately serve
long-lived endpoints (anonymous progress polling, health endpoints
that stream metrics, public SSE feeds). Targeting only the main route
would create a quiet "my public SSE endpoint times out" failure mode.
Cost of targeting both: one extra `targetRefs` entry in the same
policy resource. No new resources.

When `publicRoutes` is empty, only the main HTTPRoute exists, and the
policy carries one `targetRefs` entry.

## End-to-end example

A chat app with an SSE endpoint:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: chat
  namespace: chat
spec:
  hostname: chat.example.com
  service:
    name: chat-svc
    port: 80
  routing:
    streaming: true
    routes:
      - pathPrefix: /
  auth:
    enabled: true
    provider: keycloak
```

Produces, in addition to the usual HTTPRoute / Certificate /
SecurityPolicy / landing-page card:

```yaml
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: chat-streaming
  namespace: chat
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: chat
  timeout:
    http:
      requestTimeout: "0s"
      connectionIdleTimeout: 300s
```

## Operator changes

### `api/v1/nebariapp_types.go`

- Add `Streaming bool` to `RoutingConfig`, optional, default false.

### `internal/controller/reconcilers/routing/streaming.go` (new)

A small reconciler dedicated to the policy lifecycle, paralleling the
existing TLS / Routing / Auth reconcilers:

- **When `routing.streaming: true`:** ensure a
  `BackendTrafficPolicy` named `<nebariapp-name>-streaming` exists in
  the NebariApp's namespace. Spec is the fixed canned form above.
  `targetRefs` enumerates every HTTPRoute the operator owns for this
  app: the main one always, the public one when present. Set
  `OwnerReferences` to the NebariApp for GC.
- **When `routing.streaming: false` or absent:** delete any
  operator-owned policy with that name. Idempotent — no error if
  already gone.
- **A `StreamingReady` condition** surfaces success or last-known
  error, mirroring the existing `RoutingReady` / `TLSReady` pattern.
  Failures (apiserver errors, CRD missing in the cluster) don't block
  the rest of the reconcile.

### `internal/controller/nebariapp_controller.go`

- Wire `StreamingReconciler` into the main controller's reconcile
  chain, after the routing reconciler (so HTTPRoutes exist by the time
  we try to reference them in `targetRefs`).
- Register the Envoy Gateway scheme on startup. The existing
  `SecurityPolicy` code path already pulls `gateway.envoyproxy.io`
  types via `go.mod`, so no new module dependency — just the
  registration call.

### RBAC

Add to the operator's ClusterRole:

```yaml
- apiGroups: ["gateway.envoyproxy.io"]
  resources: ["backendtrafficpolicies"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

### Generated artifacts

- `config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml`
  regenerates with the new `routing.streaming` field.
- `docs/api-reference.md` regenerates.
- `config/rbac/role.yaml` regenerates with the
  `backendtrafficpolicies` permission.

## Validation

- `routing.streaming` is a boolean; no CRD-level validation beyond
  the type.
- The reconciler doesn't pre-validate Envoy Gateway is installed —
  if the `BackendTrafficPolicy` CRD is missing, the apiserver returns
  `NoKindMatchError` on `Create` and the reconciler surfaces that
  on the `StreamingReady` condition (`Reason: CRDMissing`). The rest
  of the NebariApp reconcile continues so the app still gets its
  HTTPRoute / TLS / auth; only the streaming guarantee is degraded.
- If a `BackendTrafficPolicy` already exists in the namespace with
  the operator's chosen name but without the operator's owner
  reference (e.g. a hand-rolled one left over from
  pre-`routing.streaming` workarounds), the reconciler logs a warning
  and leaves it alone — refuses to take ownership of a foreign
  resource. The condition reports `ForeignPolicyExists`. Users
  resolve by deleting the hand-rolled policy.

## Backwards compatibility

- **Existing manifests:** unaffected. `routing.streaming` is
  additive, optional, default false.
- **Apps that hand-rolled their own `BackendTrafficPolicy`:** see
  the `ForeignPolicyExists` case in *Validation*. The operator
  doesn't touch foreign resources; users migrate by deleting their
  hand-rolled policy and setting `routing.streaming: true`.
- **API version:** unchanged (`v1`). Field addition is fully
  additive.

## Migration

For the downstream PR (`openteams-ai/nebari.openteams.ai#12`) that
motivated this design:

1. Set `routing.streaming: true` on the relevant NebariApp(s).
2. Delete the hand-rolled `BackendTrafficPolicy` YAML(s) from the
   GitOps repo.
3. Re-sync.

For any other internal user that may have hand-rolled a similar
policy: same recipe.

## Open questions

- **Naming.** `routing.streaming` reads cleanly but is slightly
  imprecise: it doesn't actually enable a new feature, it disables a
  timeout. Alternatives considered: `routing.longLived: true`,
  `routing.disableTimeouts: true`. Decision lean: keep `streaming`
  because it matches what users will Google for (SSE, streaming,
  long-poll) and the docstring makes the mechanism explicit.
- **Canned idle timeout value.** 300s matches
  `openteams-ai/nebari.openteams.ai#12` and is a common SSE choice,
  but it's a guess about what users actually want. If a real workload
  shows up that needs something different, that's the signal to add
  `routing.streamingTimeouts: { requestTimeout, idleTimeout }`. Until
  then, one knob.
- **Should `StreamingReady` block `Ready`?** Lean: no — degrade
  gracefully. A streaming-enabled app whose `BackendTrafficPolicy`
  fails to create still gets its HTTPRoute and serves traffic; the
  long-lived connections just time out at 15s like they did before
  the flag existed. The `StreamingReady=False` condition makes the
  degradation visible.

## References

- Envoy Gateway HTTP timeouts: <https://gateway.envoyproxy.io/docs/tasks/traffic/http-timeouts/>
- Envoy timeouts background: <https://www.envoyproxy.io/docs/envoy/latest/faq/configuration/timeouts>
- Companion design: [`multi-backend-routes.md`](./multi-backend-routes.md)
- Downstream PR that motivated this work:
  `openteams-ai/nebari.openteams.ai#12` (hand-rolled
  `BackendTrafficPolicy` targeting operator-generated HTTPRoutes by
  name).
