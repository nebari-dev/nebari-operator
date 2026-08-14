# AGENTS.md

Guidance for contributors and AI coding agents working in this repository.

This file follows the [AGENTS.md](https://agents.md) convention and is read by Claude Code, Codex, Cursor, Aider, Jules, and other agent tooling, as well as by humans.

## Project Overview

**Nebari Operator** is a Go Kubernetes operator (kubebuilder-scaffolded, built on [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)) that provides self-service application onboarding for GitOps-friendly Kubernetes platforms for the Nebari application ecosystem.

A team declares a single `NebariApp` custom resource, and the operator provisions and continuously reconciles everything that app needs to be reachable and secured:

- **Routing** — a Gateway API `HTTPRoute` on the shared Nebari gateway.
- **TLS** — a cert-manager `Certificate` and a per-app HTTPS listener on the gateway.
- **Auth** — an OIDC `SecurityPolicy` (Envoy Gateway) wired to Keycloak, including automatic Keycloak client provisioning and per-app RBAC.
- **Landing-page registration** — surfaced to nebari-landing so the app appears on the platform's landing page. See https://github.com/nebari-dev/nebari-landing for extra details.

It is part of **Nebari Infrastructure Core (NIC)**. The sibling repository [`nebari-infrastructure-core`](https://github.com/nebari-dev/nebari-infrastructure-core) provisions the cluster and bootstraps foundational software; this operator runs *on* that cluster and manages application-level resources. APIs and behavior are still under active development and may change without notice.

### Core Architecture Principles

1. **One CRD, a pipeline of focused reconcilers.** A single `NebariApp` spec fans out to independent sub-reconcilers (Core, TLS, Routing, Auth), each owning exactly one concern.
2. **Continuously reconciled.** The control loop re-runs on a steady cadence; drift in owned resources is auto-corrected. Reconcile is idempotent.
3. **Namespace opt-in.** The operator only acts in namespaces explicitly labeled `nebari.dev/managed=true` — no accidental adoption.
4. **Observable by contract.** Every reconciler updates `status.conditions` and emits typed Kubernetes Events. Condition/event vocabularies are centralized as Go constants.
5. **Contract independence.** The routing, TLS, auth, and landing-page contracts each degrade gracefully and do not depend on one another being present.

### Who applies the `NebariApp` CRD

The operator is the producer of one contract — the `NebariApp` custom resource. Any Helm chart that includes it among its manifests and configuration is called a ["Software Pack"](https://github.com/nebari-dev/software-pack-template#what-is-a-nebari-software-pack). Below are a few software packs that consume the `NebariApp` CRD — the best place to see it used in anger:

- **[`software-pack-template`](https://github.com/nebari-dev/software-pack-template)** — the canonical example collection. Shows the same app onboarded via raw YAML, a Helm chart, Kustomize (base + dev/production overlays), and wrapping an existing chart, plus a standalone `docs/nebariapp-crd-reference.md`. Start here when you need a worked example of any field.
- **[`data-science-pack`](https://github.com/nebari-dev/data-science-pack)** — a live consumer (multi-user JupyterHub). Ships a `templates/nebariapp.yaml` and an integration guide under `docs/`.
- **[`nebi-pack`](https://github.com/nebari-dev/nebi-pack)** — a live consumer (team Pixi-environment management, Keycloak SSO + PostgreSQL). Ships a `templates/nebariapp.yaml`.

When you change the CRD's shape or behavior, these are the downstreams that feel it: check their manifests still validate.

## Common Development Commands

Dependencies (kustomize, controller-gen, setup-envtest, golangci-lint, crd-ref-docs) are auto-installed into `bin/` on first use — you do not need to install them globally.

The tables below are generated from the Makefile (see [Keeping this file current](#keeping-this-file-current)).

<!-- BEGIN GENERATED: make-targets (source: Makefile `##@`/`## ` help text -- run `make agents` to refresh) -->

_Generated from the Makefile; do not edit by hand. Change a target's `## ` help comment and run `make agents`._

### General

| Target | Description |
| --- | --- |
| `make help` | Display this help. |

### Development

| Target | Description |
| --- | --- |
| `make manifests` | Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects. |
| `make generate` | Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations. |
| `make fmt` | Run go fmt against code. |
| `make vet` | Run go vet against code. |
| `make test` | Run tests. |
| `make test-unit` | Run controller unit tests with coverage. |
| `make test-unit-html` | Generate HTML coverage report for unit tests. |
| `make test-e2e` | Run all e2e tests. |
| `make test-e2e-parallel` | Run e2e tests in parallel (faster). |
| `make test-e2e-smoke` | Run quick smoke tests only. |
| `make lint` | Run golangci-lint linter |
| `make lint-fix` | Run golangci-lint linter and perform fixes |
| `make lint-config` | Verify golangci-lint linter configuration |

### Documentation

| Target | Description |
| --- | --- |
| `make docs` | Generate API reference documentation from Go types in api/v1/. |
| `make crd-ref-docs` | Download crd-ref-docs locally if necessary. |
| `make agents` | Regenerate the machine-owned make-targets block in AGENTS.md. |

### Build

| Target | Description |
| --- | --- |
| `make build` | Build manager binary. |
| `make run` | Run a controller from your host. |
| `make docker-build` | Build docker image with the manager. |
| `make docker-push` | Push docker image with the manager. |
| `make docker-buildx` | Build and push docker image for the manager for cross-platform support |

### Installer

| Target | Description |
| --- | --- |
| `make build-installer` | Generate a consolidated YAML with CRDs and deployment. |
| `make helm-chart` | Generate Helm chart from manifests using kubebuilder. |
| `make helm-package` | Package the Helm charts. |
| `make helm-chart-version` | Update Helm chart version and appVersion (requires VERSION and APP_VERSION vars). |
| `make helm-lint-library` | Lint the nebari-app library chart. |
| `make helm-lint` | Lint the helm charts |
| `make helm-test-generate-golden` | Generate golden files for nebari-app chart tests. |
| `make helm-test` | Render the nebari-app library chart for all cases and verify against golden files. |
| `make generate-dev` | Generate for development (CRDs + deepcopy code only) |
| `make generate-all` | Generate all artifacts (CRDs + manifests + Helm chart) |
| `make prepare-release` | [DEPRECATED] Use automated GitHub Actions workflow instead |

### Deployment

| Target | Description |
| --- | --- |
| `make install` | Install CRDs into the K8s cluster specified in ~/.kube/config. |
| `make uninstall` | Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion. |
| `make deploy` | Deploy controller to the K8s cluster specified in ~/.kube/config. |
| `make undeploy` | Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion. |

<!-- END GENERATED: make-targets -->

**After editing anything in `api/v1/`, run `make generate-dev` (and `make docs`) and commit the generated files alongside your source change.** CI fails if generated files, manifests, or the API reference are out of sync.

### Local development cluster

`dev/` holds a self-contained local Kind setup with foundational services (Envoy Gateway, cert-manager, Keycloak):

```bash
make -C dev setup             # create the kind cluster + install foundational services
make -C dev teardown          # tear it down
```

See `dev/scripts/{cluster,networking,services,testing}` and `dev/examples/` for the moving parts.

## High-Level Architecture

### Component Structure

```
cmd/operator/            CLI entry point — kubebuilder manager setup
  main.go                scheme registration, manager, leader election, wires up all reconcilers

api/v1/                  CRD types (group reconcilers.nebari.dev, version v1)
  nebariapp_types.go     NebariApp spec/status + all condition/reason/event constants
  groupversion_info.go
  zz_generated.deepcopy.go   generated — do not hand-edit

internal/
  config/                operator runtime config loaders
    auth.go              LoadAuthConfig — Keycloak/OIDC env config
    tls.go               LoadTLSConfig — ClusterIssuer selection
  controller/
    nebariapp_controller.go   NebariAppReconciler — orchestrates the sub-reconcilers
    reconcilers/
      core/              CoreReconciler — namespace opt-in + service-existence validation
      tls/               TLSReconciler — cert-manager Certificate + per-app HTTPS listener
      routing/           RoutingReconciler — Gateway API HTTPRoute + public routes
      auth/              AuthReconciler — OIDC SecurityPolicy + Keycloak client + per-app RBAC
        providers/       OIDCProvider interface, keycloak.go, generic_oidc.go
    utils/
      conditions/        SetCondition/GetCondition helpers over meta.SetStatusCondition
      constants/         gateway/namespace names, name suffixes, finalizer, annotations
      naming/            per-app resource naming
      ptr/

config/                  kustomize bases (crd, rbac, manager, default, samples, prometheus, ...)
dist/                    generated install.yaml + Helm chart (do not hand-edit)
docs/                    api-reference, reconciler docs, design docs, ADRs, plans, maintainer guides
test/                    e2e Ginkgo suite (test/e2e) + shared test utils
```

### The `NebariApp` CRD

The operator manages a **single** custom resource:

- **Group / Version / Kind:** `reconcilers.nebari.dev/v1`, `NebariApp` (shortName `nebariapp`), namespaced, with a `status` subresource.

`NebariAppSpec` top-level fields: `hostname`, `service` (name/port/namespace), `routing` (routes, publicRoutes, TLS, annotations), `auth` (OIDC/Keycloak config), `gateway` (`public`|`internal`), `serviceAccountName`, `landingPage`. Validation is expressed via kubebuilder markers (Required/Enum/Min/Max/Pattern/default) plus a CEL `XValidation` on `AuthConfig`.

`NebariAppStatus`: `conditions[]` (`Ready`, `RoutingReady`, `TLSReady`, `AuthReady`), `observedGeneration`, `hostname`, `gatewayRef`, `clientSecretRef`, `authConfigHash`, `serviceDiscovery`.

### The Reconciler Pipeline

`NebariAppReconciler` (`internal/controller/nebariapp_controller.go`) holds the four sub-reconcilers as struct fields and drives them in order:

```
Reconcile:
  fetch NebariApp
  -> finalizer handling
  -> initialize status
  -> CoreReconciler.ValidateSpec        (namespace opt-in + service exists; fail -> requeue 5m)
  -> TLSReconciler.ReconcileTLS         (cert-manager Certificate + HTTPS listener)
  -> RoutingReconciler.ReconcileRouting (HTTPRoute; cleanup when spec.routing == nil)
  -> RoutingReconciler.ReconcilePublicRoute
  -> AuthReconciler.ReconcileAuth       (SecurityPolicy + Keycloak client + Role/RoleBinding)
  -> set Ready=True, populate status.serviceDiscovery
  -> requeue after 1m
```

Cleanup (finalizer) runs the pipeline in reverse: **Auth -> Routing -> TLS**. Owned resources carry ownerReferences for garbage collection; the finalizer is `apps.nebari.dev/finalizer`.

Each sub-reconciler is a struct embedding a controller-runtime `client.Client`, a `*runtime.Scheme`, and a `record.EventRecorder`, and exposes a `ReconcileX` and a `CleanupX` method. They do not import one another.

`SetupWithManager` watches the primary `NebariApp` plus cert-manager `Certificate` objects, mapped back to their owning `NebariApp` via the `nebari.dev/nebariapp-name` / `nebari.dev/nebariapp-namespace` labels.

### Platform Assumptions

The operator targets a Nebari platform laid down by NIC, and encodes those assumptions as constants (`internal/controller/utils/constants`):

- A shared Gateway `nebari-gateway` in namespace `envoy-gateway-system`, GatewayClass `envoy-gateway`, wildcard TLS secret `nebari-gateway-tls`.
- Keycloak reachable in the `keycloak` namespace.
- Per-app resources are named `<app>-<suffix>` (route, public-route, security, cert, tls, oidc-client).

## Key Development Patterns

### Adding or changing a CRD field

1. Edit `api/v1/nebariapp_types.go`; add kubebuilder validation markers.
2. If it needs a new condition or event, add the constant alongside the existing ones in the same file — do not scatter string literals.
3. Run `make generate-dev` (regenerates DeepCopy + CRD manifests) and `make docs`.
4. Commit the source change **and** the generated files (`zz_generated.deepcopy.go`, `config/crd/bases/*`, `config/rbac/role.yaml`, `docs/api-reference.md`).

### Adding behavior to a reconciler

1. Put the logic in the sub-reconciler that owns that concern (`core`/`tls`/`routing`/`auth`). Do not cross concerns — reconcilers are independent by design.
2. Keep it idempotent: create-or-update, and reconcile drift on every pass.
3. Update `status.conditions` and emit a typed Event (Normal on success, Warning on failure) using the constants from `api/v1` and the `utils/conditions` helpers.
4. If cleanup is needed on deletion, extend the corresponding `CleanupX` (remember: cleanup runs reverse order).
5. Cover it with table-driven unit tests against envtest; add e2e coverage under `test/e2e` for cross-resource behavior.

### RBAC

The manager's RBAC is generated, not hand-written. `+kubebuilder:rbac:...` markers live on `NebariAppReconciler.Reconcile` in `nebariapp_controller.go`. To grant or narrow a permission, edit the markers and run `make manifests` — never edit `config/rbac/role.yaml` by hand. Prefer the narrowest verb set that the reconcilers actually use.

### Adding an OIDC provider

1. Implement the `OIDCProvider` interface in `internal/controller/reconcilers/auth/providers/`.
2. Register it where the providers map is built in `cmd/operator/main.go`.
3. Existing implementations: `keycloak.go` (uses `Nerzal/gocloak`) and `generic_oidc.go`.

## Conventions

### Status Conditions

- Standardized types: `Ready` (aggregate), `RoutingReady`, `TLSReady`, `AuthReady`.
- Managed through `utils/conditions`, which wraps `k8s.io/apimachinery/pkg/api/meta` and only bumps `LastTransitionTime` on an actual status change.
- Track `observedGeneration` so consumers can tell a stale status from a current one.
- Condition types, reasons, and event reasons are Go constants in `api/v1/nebariapp_types.go` — reference them, never re-type the strings.

### Generated Files

`zz_generated.deepcopy.go`, everything under `config/crd/bases/`, `config/rbac/role.yaml`, `dist/`, and `docs/api-reference.md` are generated. Change the source (types, markers, kustomize inputs) and regenerate — do not hand-edit the output. CI enforces that the committed tree matches a fresh regeneration.

### Namespace Opt-In

Never reconcile a resource in a namespace that lacks `nebari.dev/managed=true`. `CoreReconciler.ValidateSpec` is the gate; keep it the single enforcement point.

## Testing

Three layers, each with its own make target:

- **Unit tests** — `make test` (alias `make test-unit`). Table-driven Go tests run against **envtest** (a real kube-apiserver + etcd, no kubelet), so reconcilers are exercised against a live API without a full cluster. Every sub-reconciler and utility is covered next to its code: `internal/controller/reconcilers/{core,tls,routing,auth}` (and `auth/providers`), `internal/config`, `internal/controller/utils/*`, and `api/v1`. This is the fast gate — run it before every push.
- **End-to-end tests** — `make test-e2e` (plus `make test-e2e-smoke` for a quick subset, `make test-e2e-parallel`). A **Ginkgo/Gomega** suite under `test/e2e/` (`-tags=e2e`) that deploys the operator to a **real cluster** and asserts end-to-end behavior: `auth_test.go`, `routing_test.go`, `tls_user_secret_test.go`, `gateway_test.go`, `validation_test.go`, `connectivity_test.go`, `conditions_test.go`, `manager_test.go`. Fixtures live in `test/e2e/testdata/`; shared helpers in `test/e2e/e2e_utils.go` and `test/utils/`. Bring up a local cluster with `make -C dev setup` first (see [Local development cluster](#local-development-cluster)).
- **Helm chart golden tests** — `make helm-test` renders the `nebari-app` library chart for each case in `test/helm/nebari-app/templates/` and diffs it against the committed goldens in `test/helm/nebari-app/golden/`. When a template change is intentional, regenerate with `make helm-test-generate-golden`; `make helm-lint-library` lints the chart.

CI (`build-pr.yml`) runs the unit, e2e, and chart suites on every PR. **Never disable or skip a test to get CI green — fix the underlying cause.** New reconciler behavior needs unit coverage; cross-resource behavior needs an e2e case.

## Documentation

**Before opening a PR, read the design docs and decision records for the area you are touching.** They carry the rationale and cross-component contracts the code assumes but does not restate. If a change contradicts a recorded decision or contract, update that record in the same PR — do not let the code and the docs silently diverge.

**Authoritative context — consult before changing the area it covers:**

- **`docs/decisions/`** — ADR-style records of choices already made, and why. Currently `2026-03-05-webapi-extracted-to-nebari-landing.md` (why the web API moved out to nebari-landing). Don't re-litigate a decision recorded here without amending it.
- **`docs/design/`** — living design docs and cross-component contracts: `auth-app-contract.md`, `epic-routing-securitypolicy.md`, `landing-page.md`, `user-workloads-discovery.md`, `user-workloads-discovery-contract.md`. Read the matching one before touching auth, routing, landing-page, or service discovery — these define the contract the reconcilers implement.
- **`docs/plans/`** — in-flight implementation plans (e.g. `2026-02-20-tls-certificate-management*.md`). Check whether the work is already planned here before starting.
- **`docs/reconcilers/`** — per-reconciler architecture with condition/reason/event tables and pipeline diagrams (`README.md`, `routing.md`, `validation.md`, `authentication.md`). The source of truth for reconciler behavior.

**Reference material:**

- **`docs/api-reference.md`** — generated CRD reference (`make docs`).
- **`docs/maintainers/`** — release checklist / process / setup.
- **`docs/quickstart.md`, `docs/configuration-reference.md`, `docs/troubleshooting.md`, `docs/makefile-reference.md`** — user-facing guides.
- **[CONTRIBUTING.md](CONTRIBUTING.md)** — the human-facing contribution process.

When you add a new design doc, ADR, or plan, link it here so it is discoverable — an unreferenced doc is one nobody consults.

## Dependencies

Core libraries (see `go.mod`; module `github.com/nebari-dev/nebari-operator`, Go 1.25):

- `sigs.k8s.io/controller-runtime` — operator framework.
- `k8s.io/api` / `apimachinery` / `client-go` — Kubernetes API machinery.
- `sigs.k8s.io/gateway-api` — HTTPRoute / Gateway types.
- `github.com/cert-manager/cert-manager` — Certificate types.
- `github.com/envoyproxy/gateway` — `SecurityPolicy` (OIDC at the gateway).
- `github.com/Nerzal/gocloak/v13` — Keycloak admin client.
- `github.com/onsi/ginkgo/v2` + `gomega` — test framework.

## Releases

Releases are fully automated. Publishing a GitHub Release from a version tag (`vX.Y.Z`) triggers `.github/workflows/release.yml`, which:

1. **tests** — runs `fmt`/`vet`/`test`/`lint` as a gate.
2. **build-manifests** — regenerates CRDs + RBAC, builds `dist/install.yaml` pinned to the release image, and attaches it to the Release.
3. **goreleaser** — builds and pushes multi-arch images to `quay.io/nebari/nebari-operator` and Go binaries for linux/darwin/windows × amd64/arm64 (via `.goreleaser.yml`).
4. **publish-helm-chart** — regenerates the chart, stamps the version/appVersion, packages it, and attaches the `.tgz` to the Release.
5. **sync-helm-repository** — pushes the packaged chart to [`nebari-dev/helm-repository`](https://github.com/nebari-dev/helm-repository) so it is installable from the shared Helm repo.

You do not build or push anything by hand — cutting the Release is the whole trigger. Maintainer runbooks live in `docs/maintainers/release-process.md`, `release-checklist.md`, and `release-setup.md`.

**Versioning:** the operator uses **[EffVer](https://jacobtomlinson.dev/effver/)** from `v0.1.0` onward (ADR-003), not SemVer. Read the version as *macro.meso.micro* by the effort a bump implies for consumers. Note `docs/maintainers/release-process.md` still says "semantic versioning" and is due an update to match.

## Contribution Workflow

1. Fork and branch from `main` with a conventional prefix (`feat/…`, `fix/…`, `chore/…`, `docs/…`, `test/…`).
2. Make the change; add tests.
3. If you touched `api/v1/`, run `make generate-dev` and `make docs`, and commit the generated files.
4. Run the local gate before pushing: `make fmt vet lint test`.
5. Use **Conventional Commits** for messages.
6. Open a PR. CI must pass: linter, unit tests, e2e tests, and the generated-files-up-to-date checks.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full process.

## Pre-Commit Checklist

Run before every commit:

1. **Format:** `make fmt`
2. **Vet:** `make vet`
3. **Lint:** `make lint`
4. **Unit tests:** `make test` (or `make test-unit`)
5. **Codegen in sync** (if `api/v1/` changed): `make generate-dev` **and** `make docs`, with generated files committed.
6. **RBAC via markers:** permission changes come from `+kubebuilder:rbac` markers + `make manifests`, never hand-edits to `config/rbac/role.yaml`.
7. **Conditions & events** use the constants in `api/v1/nebariapp_types.go`, not inline strings.

## Keeping This File Current

This file is part generated, part hand-authored, and the two are handled differently:

- **Generated:** the make-targets tables between the `<!-- BEGIN GENERATED: make-targets -->` / `<!-- END GENERATED -->` markers are produced by `hack/gen-agents.sh` from the Makefile's `##@` section headers and `## ` help comments. **Do not edit them by hand.** Change a target's help comment in the Makefile and run `make agents`. CI regenerates and runs `git diff --exit-code AGENTS.md`, so a stale table fails the build — the same drift-gate used for `make manifests` / `make docs`.
- **Hand-authored:** everything else (architecture, the reconcile pipeline, patterns, conventions, releases, downstream consumers). Generation does **not** keep this fresh — if you change the reconcile order, a convention, or the release flow, update the prose in the same PR.

To add a new generated block later, wrap it in its own `BEGIN GENERATED: <name>` / `END GENERATED` markers and teach `hack/gen-agents.sh` to fill it.
