# Nebari Pack Specification

**Specification version:** `0.1.0`
**API version:** `reconcilers.nebari.dev/v1`
**Kind:** `NebariApp`

This is the contract between the Nebari platform and a software pack. It defines what a pack has to do to run on the platform and inherit single sign-on, routing, and TLS from it.

It lives here, next to the custom resource definition it describes and the reconcilers that enforce it, so that the contract and its implementation version together. A specification maintained in a different repository from the code drifts, and the drift is invisible until a pack breaks.

## What this document is, and is not

This document is **normative**. It says what a pack must do.

It is deliberately not three other things, each of which has a home already:

| For | Read | Why not here |
| --- | ---- | ------------ |
| Every field, type, and default | [`api-reference.md`](api-reference.md) | Generated from the Go types by `make docs`. Hand-copying it guarantees it goes stale |
| How the operator behaves, condition by condition | [`reconcilers/`](reconcilers/) | The source of truth for reconciler behavior |
| How to onboard an app step by step | [`using-the-nebari-app-chart.md`](using-the-nebari-app-chart.md) | A tutorial, which ages differently from a contract |

Where this document and `api-reference.md` disagree about a field, the generated reference is correct and this document has a bug.

## Conformance language

**MUST**, **MUST NOT**, **SHOULD**, and **MAY** are used as defined in [RFC 2119](https://tools.ietf.org/html/rfc2119).

A pack that satisfies every MUST in this document is a **conforming pack**. Conformance is what makes a pack work; it is not the same as being an official pack, which is a question of endorsement and is decided by the [pack policy](https://github.com/nebari-dev/governance/blob/main/pack-policy.md).

## 1. Registration

A pack MUST register with the platform by creating exactly one `NebariApp` resource.

- The resource MUST be `apiVersion: reconcilers.nebari.dev/v1`, `kind: NebariApp`.
- It MUST live in the namespace the pack's workloads run in.
- That namespace MUST carry the label `nebari.dev/managed=true`. Without it the operator does not process the resource, and the `NebariApp` reports `NamespaceNotOptedIn` while creating nothing.

A pack MAY ship the resource by any mechanism: plain YAML, Kustomize, its own Helm template, or the `nebari-app` library chart. The platform does not care how the resource arrives, only that it is there and valid.

### 1.1 Required fields

Three fields are required by the schema. Everything else has a default or is optional.

| Field | Type | Meaning |
| ----- | ---- | ------- |
| `spec.hostname` | string | The external hostname the pack is served on |
| `spec.service` | object | The in-cluster service to route to, requiring `name` and `port` |
| `spec.routing` | object | How requests reach the service |

A pack MUST NOT assume any other field is present by default. Consult [`api-reference.md`](api-reference.md) for the current defaults rather than hardcoding the ones a pack was written against.

## 2. Routing

Routing is declared, not implemented. A pack MUST NOT create its own `HTTPRoute`, `Gateway`, or ingress resource for traffic the platform is meant to route.

- A pack MUST declare its route rules under `spec.routing.routes`.
- A pack MUST declare any route that has to bypass authentication under `spec.routing.publicRoutes`, and MUST keep that set as small as the app genuinely requires. Health and readiness endpoints are the normal case.
- A pack SHOULD prefer an exact path match over a prefix match for a public route, so that a bypass cannot widen unintentionally.

## 3. Authentication

The platform provides identity. A pack MUST NOT implement its own login flow for platform users, and MUST NOT ask users for platform credentials.

- A pack that needs authentication MUST declare it under `spec.auth`, rather than wiring an identity provider itself.
- The identity provider is Keycloak. A pack MUST NOT depend on a different one.
- A pack SHOULD let the operator provision its client (`provisionClient`), so that client lifecycle follows the resource lifecycle.
- A pack MUST request the minimum OIDC scopes it needs. Scope creep here is a security regression that no reviewer sees.
- A pack MUST read credentials from the Secret the operator creates. It MUST NOT hardcode a client secret in chart values, templates, or an image.

Enforcement can happen at the gateway, in the application, or both. [`reconcilers/authentication.md`](reconcilers/authentication.md) describes what the operator creates in each case and is the reference for choosing.

## 4. TLS

A pack MUST NOT manage its own certificates for its platform hostname.

- A pack requests TLS by setting `spec.routing.tls.enabled`.
- Certificates are issued through cert-manager by the platform.
- A pack MUST NOT terminate TLS itself for traffic arriving through the platform gateway.

## 5. Landing page

A pack MAY appear on the platform landing page by setting `spec.landingPage`. If the block is present, `enabled` MUST be set explicitly.

A pack that opts in SHOULD provide a health check endpoint, so the landing page can distinguish "installed" from "working".

## 6. Security baseline

These requirements apply to every pack. They are not new: they consolidate the security items that the [release readiness checklist](https://github.com/nebari-dev/software-pack-template/blob/main/docs/release-readiness-checklist.md) already gates promotion on, so there is one list rather than two that can disagree.

The maturity level in brackets is the level at which the checklist makes each one a blocker. A pack below that level SHOULD still meet them.

- `[B]` Containers MUST NOT run as root, or MUST carry a documented justification.
- `[B]` Secrets MUST NOT be hardcoded in templates or default values.
- `[B]` OIDC scopes MUST be limited to what the app needs.
- `[B]` Upstream container images MUST be pinned to a specific tag or digest. `latest` is not a pin.
- `[GA]` `securityContext` MUST set `readOnlyRootFilesystem`, `runAsNonRoot`, and `allowPrivilegeEscalation: false` where the application permits.
- `[GA]` A `NetworkPolicy` or equivalent SHOULD restrict unnecessary pod-to-pod communication.

The checklist remains the authority on when a pack is promoted. This section is the authority on what the requirements are.

## 7. Versioning and compatibility

This specification is versioned with [EffVer](https://jacobtomlinson.dev/effver/), the convention the operator adopted in [`decisions/2026-08-14-adopt-effver.md`](decisions/2026-08-14-adopt-effver.md). The version measures the effort an upgrade costs a pack author, not how much work the change was to make:

| Segment | Means for a pack author |
| ------- | ----------------------- |
| Micro | Nothing to do. Clarifications, and requirements that were already true in practice |
| Meso | Some work. A new SHOULD, or a MUST that most packs already satisfy |
| Macro | Real work. A new MUST, a removed field, or changed behavior a pack depends on |

While this specification is pre-1.0, EffVer collapses to `0.Macro.Micro`.

Packs declare the specification version they target, as described in the [pack policy](https://github.com/nebari-dev/governance/blob/main/pack-policy.md#maturity-and-release-readiness). A pack targeting an older version keeps working until a Macro release says otherwise.

**This is version `0.1.0`, the first published version.** It describes the contract the operator already enforces rather than proposing changes to it. A pack that works today is conforming today.

## 8. Changing this specification

This specification is a platform contract. Changing it requires an accepted platform RFD, filed in [`nebari-dev/governance`](https://github.com/nebari-dev/governance) and decided by the Core team, as described in [the governance](https://github.com/nebari-dev/governance/blob/main/GOVERNANCE.md#platform-rfds).

That applies to the requirements in this document. It does not apply to fixing a sentence that describes the operator's behavior incorrectly: that is a bug in the document, and correcting it needs only a pull request.

Changing the `NebariApp` Go types is what changes the contract in practice. A pull request that adds, removes, or repurposes a field in `api/v1/` SHOULD say which specification version the change lands in, and update this document in the same change.
