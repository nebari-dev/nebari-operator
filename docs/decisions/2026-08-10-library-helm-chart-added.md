# ADR-002: Add Helm chart to nebari-operator repository

| Field       | Value                               |
|-------------|-------------------------------------|
| Date        | 2026-08-10                          |
| Status      | **Accepted**                        |
| Deciders    | NIC maintainers                     |
| Supersedes  | None                                |



## Context

ADR-001 noted, as a positive consequence of extracting the Web API, that "the operator repository is now purely a Kubernetes controller." This ADR nuances that note rather than overturning it (ADR-001 remains valid): the nebari-operator repository now also includes:

1. A Helm library chart (`charts/nebari-app/`) — a reusable Helm chart that users can include in their own charts
2. Helm tests (`test/helm/nebari-app/`) with golden file validation
3. Helm packaging targets in the Makefile

This creates a scope tension: the repository is no longer "purely a Kubernetes controller" — it also distributes a Helm chart that wraps the `NebariApp` CRD.

### Why add a Helm chart?

The Helm chart serves as:

- **A distribution mechanism** — Users can install NebariApps via `helm install` in addition to `kubectl apply -f`
- **A validation layer** — The chart's `required` functions provide client-side validation before K8s API submission
- **A configuration abstraction** — Users don't need to write full `NebariApp` CR manifests; they set values like `hostname`, `service.port`

### Why keep it in nebari-operator?

- **Version alignment** — The chart version should match the operator version because the CRD schema evolves with releases
- **Version-tagged CRD target** - The template pins the target apiVersion (`reconcilers.nebari.dev/v1`) and enforces `required` guards on the fields the operator depends on; keeping the chart beside the CRD lets those pins track the schema as it is versioned across releases
- **Simplified release process** — One repository to tag and release, rather than separate operator + chart repos

## Decision

Keep the Helm chart in the `nebari-operator` repository as a **library chart** (`charts/nebari-app/`) that users include via `dependencies:` in their own charts, rather than as a standalone installable chart.

Key implementation choices:

- **Library chart only** — `charts/nebari-app/` can be referenced by users via `dependencies:` in their parent chart
- **No `dist/chart/` dependency** — `helm-package` packages `charts/nebari-app` directly; `helm-chart` generates `dist/chart` but it's not the source of truth
- **Golden file tests** — `make helm-test-generate-golden` and `make helm-test` ensure chart rendering matches expected output for all case configurations
- **`dist/chart/` kept as generated output only** - `helm-chart` still generates `dist/chart` for CI convenience, but it is Kubebuilder-generated deploy output for the controller, not the source of truth for the library chart
- **Chart published as an OCI artifact to `quay.io/nebari/charts`** - consumers reference it via `repository: oci://quay.io/nebari/charts` in their `dependencies:`; the canonical registry is `quay.io`, matching the operator image, and the operator README was aligned to reference `quay.io` consistently

## Consequences

### Positive

- Users can install NebariApps via Helm in addition to `kubectl apply`
- Client-side validation catches configuration errors before API submission
- Single repository lets the chart version and CRD schema be tagged and released together
- Golden file tests catch chart render drift only; the goldens self-generate from the template and do not reference the CRD, so they verify template output stability, not agreement with CRD validation

### Negative / Mitigations

| Consequence | Mitigation |
|-------------|------------|
| Repository is no longer "purely a Kubernetes controller" | Documented in this ADR; the chart is a convenience wrapper, not the core controller |
| Two release artifacts (`nebari-operator-*.tgz` + `nebari-app-*.tgz`) | Update docs to mention both archives; `helm-package` emits both but they serve different purposes |
| Chart validation must mirror CRD validation | ADR-001's mitigation applies: use `unstructured` helpers in chart templates to stay resilient to additive changes |
| Documentation must mention Helm chart | Added step 7 to `CONTRIBUTING.md` pointing to chart guards and `helm-test-generate-golden` |

## Alternatives considered

### A - Library chart consumed via `dependencies:` (chosen)

Ship `charts/nebari-app/` as a Helm library chart that consumer charts pull in via `dependencies:`. It has no install surface of its own, versions alongside the operator, and leaves each consumer to own its `metadata`/`spec` composition. Accepted trade-off: it is not runnable on its own, so it cannot be `helm install`-ed directly for a quick smoke test.

### B - Standalone installable chart

Package the NebariApp wrapper as an application chart users install directly. Rejected: it would need its own values contract, release-name-to-resource mapping, and namespace opt-in handling, duplicating decisions that consumer charts already make; the library approach keeps that composition in the consumer's hands.

### C - Fold into the operator's `dist/chart` application chart behind `enabled: false`

Add the NebariApp templates to the generated `dist/chart` with an `enabled: false` kill switch. Rejected: `dist/chart` is Kubebuilder-generated deploy output for the controller itself; mixing user-facing NebariApp templates into it couples the chart's lifecycle to the operator deployment manifests and blurs the "generated, not source of truth" boundary.

### D - Separate chart repository

Publish the chart from its own dedicated repository. Rejected: it reintroduces the multi-repo release coordination ADR-001 worked to avoid, and decouples the chart version from the CRD schema it wraps.
