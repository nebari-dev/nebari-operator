# ADR-002: Add Helm chart to nebari-operator repository

| Field       | Value                               |
|-------------|-------------------------------------|
| Date        | 2026-08-10                          |
| Status      | **Accepted**                        |
| Deciders    | NIC maintainers                     |
| Supersedes  | ADR-001                             |



## Context

ADR-001 concluded that "The operator repository is now purely a Kubernetes controller." However, the nebari-operator repository now includes:

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
- **Single source of truth** — The chart templates reference the CRD schema; keeping them together avoids drift
- **Simplified release process** — One repository to tag and release, rather than separate operator + chart repos

## Decision

Keep the Helm chart in the `nebari-operator` repository as a **library chart** (`charts/nebari-app/`) that users include via `dependencies:` in their own charts, rather than as a standalone installable chart.

Key implementation choices:

- **Library chart only** — `charts/nebari-app/` can be referenced by users via `dependencies:` in their parent chart
- **No `dist/chart/` dependency** — `helm-package` packages `charts/nebari-app` directly; `helm-chart` generates `dist/chart` but it's not the source of truth
- **Golden file tests** — `make helm-test-generate-golden` and `make helm-test` ensure chart rendering matches expected output for all case configurations

## Consequences

### Positive

- Users can install NebariApps via Helm in addition to `kubectl apply`
- Client-side validation catches configuration errors before API submission
- Single repository for both CRD schema and chart templates reduces version drift
- Golden file tests catch drift between CRD validation and chart validation

### Negative / Mitigations

| Consequence | Mitigation |
|-------------|------------|
| Repository is no longer "purely a Kubernetes controller" | Documented in this ADR; the chart is a convenience wrapper, not the core controller |
| Two release artifacts (`nebari-operator-*.tgz` + `nebari-app-*.tgz`) | Update docs to mention both archives; `helm-package` emits both but they serve different purposes |
| Chart validation must mirror CRD validation | ADR-001's mitigation applies: use `unstructured` helpers in chart templates to stay resilient to additive changes |
| Documentation must mention Helm chart | Added step 7 to `CONTRIBUTING.md` pointing to chart guards and `helm-test-generate-golden` |

### Open questions

- Should `dist/chart/` be removed entirely or kept as generated output for CI?
- Should the chart be published to a separate registry (`oci://quay.io/nebari/charts/nebari-app`) or just distributed via GitHub releases?
- Should docs reference both registries (`ghcr.io` for operator, `quay.io` for chart) or consolidate?
