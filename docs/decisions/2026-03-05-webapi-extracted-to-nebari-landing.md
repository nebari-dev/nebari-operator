# ADR-001: Extract the Web API service into the nebari-landing repository

| Field       | Value                               |
|-------------|-------------------------------------|
| Date        | 2026-03-05                          |
| Status      | **Accepted**                        |
| Deciders    | NIC maintainers                     |
| Supersedes  | —                                   |



## Context

The Nebari Operator is a Kubernetes controller whose sole responsibility is to reconcile `NebariApp` custom resources
into the cluster objects needed to route and secure application traffic (HTTPRoutes, Keycloak clients, Certificates,
SecurityPolicies, …).

During early development a companion HTTP service — the **Web API** — was added to the same repository under
`internal/webapi/` and `cmd/webapi/`. Its purpose was different: serve a JSON API consumed by the Nebari landing page
frontend (service catalogue, notifications, access requests, pinned services, etc.), and serve the static React bundle
for that landing page itself.

Over time this created a number of practical tensions:

### 1. Mismatched operational lifecycles

The operator binary is a control-plane component deployed by cluster admins and upgraded in lock-step with the CRD
schema. The Web API is a user-facing data service that needs to be updated whenever the frontend UX changes — across
releases that are entirely independent of any CRD schema changes. Coupling both binaries in one repository means every
frontend iteration requires a full operator release cycle.

### 2. Separate deployment surface

The operator runs as a single replica with leader-election in `nebari-operator-system`. The Web API is a public-facing
HTTP service in `nebari-system` that needs its own Deployment, Service, RBAC rules, Keycloak client credentials, and
(eventually) horizontal scaling. Maintaining both sets of Kubernetes manifests in one repository blurred the deployment
model and added confusion for operators installing the chart.

### 3. Cross-cutting test concerns

The operator's e2e suite (`test/e2e/`) was extended with `servicediscovery_test.go` tests that stood up the Web API,
port-forwarded to Keycloak, and exercised JWT-gated endpoint filtering. These tests required Keycloak to be running in
the cluster, adding a hard prerequisite that had nothing to do with the operator's core reconciliation logic. CI times
grew and intermittent failures in the Keycloak port-forward blocked unrelated operator PRs.

### 4. Dependency boundary violation

The Web API watcher originally imported `api/v1.NebariApp` typed structs from the operator. This created a
circular-potential coupling: any change to the `NebariApp` type (even a backwards-compatible field addition) required
re-checking, recompiling, and re-releasing the Web API alongside the operator.

### 5. Repository ownership confusion

The `nebari-landing` repository already owns the landing-page frontend (the React/Vite app). The backend that serves it
logically belongs in the same repository so that a single PR can update both the React component and the JSON API shape
that backs it.



## Decision

Extract the entire Web API service out of `nebari-operator` and into the
[`nebari-dev/nebari-landing`](https://github.com/nebari-dev/nebari-landing) repository as a standalone Go module at
`nebari-webapi/`.

Key implementation choices made alongside this decision:

- **Unstructured Kubernetes client** — the watcher that monitors `NebariApp` resources was rewritten to use
  `unstructured.Unstructured` + `NestedString/NestedBool/NestedInt64` helpers instead of importing `api/v1.NebariApp`.
  This severs the compile-time dependency on the operator module entirely.

- **Separate Go module** — `nebari-webapi/go.mod` declares the module
  `github.com/nebari-dev/nebari-landing/nebari-webapi`. It can be versioned, built, and released independently.

- **Separate CI/CD pipeline** — `.github/workflows/webapi.yml` in the landing repository triggers only on changes under
  `nebari-webapi/**` and pushes a multi-arch image to `quay.io/nebari/nebari-webapi`.

- **Manifest ownership** — `nebari-webapi/deploy/manifest.yaml` and a `nebari-webapi/deploy/nebariapp.yaml` example live
  next to the service code.



## Consequences

### Positive

- The operator repository is now purely a Kubernetes controller. Its e2e suite no longer requires Keycloak to be
  running; CI is faster and more reliable.
- Frontend and backend of the landing page can be iterated in one PR without touching the operator release process.
- The Web API can be scaled, configured, and deployed independently of the operator version.
- No compile-time coupling between the two repositories.

### Negative / Mitigations

| Consequence | Mitigation |
|-------------|------------|
| Two repositories to update when the `NebariApp` schema changes fields consumed by the watcher | The watcher reads fields by string path via `unstructured` helpers; it is resilient to additive changes and only needs updating on field renames/removals |
| Extra image to build and publish in CI | Covered by the dedicated `webapi.yml` workflow; no impact on the operator CI |
| Cluster admins must deploy two manifests (operator + webapi) | The `nebari-webapi/deploy/` manifests are self-contained and can be included in the umbrella Helm chart as a sub-chart or optional component |



## Alternatives considered

### A — Keep both binaries in nebari-operator, separate packages only

Lowest migration effort, but does not resolve the operational lifecycle mismatch or the Keycloak CI dependency.
Deployment confusion remains.

### B — Shared API module (third repository)

Extract `api/v1` types into a standalone `nebari-api` module imported by both the operator and the Web API. This
resolves the dependency inversion but adds a third repository to manage and a three-way release coordination burden.

### C — Extract with unstructured client (chosen)

Zero cross-repo compile dependency. Accepted trade-off: field access is string-keyed rather than type-safe, mitigated by
the narrow set of fields the watcher actually reads (`spec.landingPage.*`, `spec.hostname`, `metadata.name`).
