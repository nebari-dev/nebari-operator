# AGENTS.md

Guidance for contributors and AI coding agents working in this repository.

This file follows the [AGENTS.md](https://agents.md) convention and is read by Claude Code, Codex, Cursor, Aider, Jules, and other agent tooling, as well as by humans.

## Project Overview

**Nebari Operator** is a Go Kubernetes operator (kubebuilder-scaffolded, built on [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime)) that provides self-service application onboarding for GitOps-friendly Kubernetes platforms.

A team declares a single `NebariApp` custom resource, and the operator provisions and continuously reconciles everything that app needs to be reachable and secured:

- **Routing** — a Gateway API `HTTPRoute` on the shared Nebari gateway.
- **TLS** — a cert-manager `Certificate` and a per-app HTTPS listener on the gateway.
- **Auth** — an OIDC `SecurityPolicy` (Envoy Gateway) wired to Keycloak, including automatic Keycloak client provisioning and per-app RBAC.
- **Landing-page registration** — surfaced to nebari-landing so the app appears on the platform's landing page.

It is part of **Nebari Infrastructure Core (NIC)**. The sibling repository [`nebari-infrastructure-core`](https://github.com/nebari-dev/nebari-infrastructure-core) provisions the cluster and bootstraps foundational software; this operator runs *on* that cluster and manages application-level resources. APIs and behavior are still under active development and may change without notice.

### Core Architecture Principles

1. **One CRD, a pipeline of focused reconcilers.** A single `NebariApp` spec fans out to independent sub-reconcilers (Core, TLS, Routing, Auth), each owning exactly one concern.
2. **Continuously reconciled.** The control loop re-runs on a steady cadence; drift in owned resources is auto-corrected. Reconcile is idempotent.
3. **Namespace opt-in.** The operator only acts in namespaces explicitly labeled `nebari.dev/managed=true` — no accidental adoption.
4. **Observable by contract.** Every reconciler updates `status.conditions` and emits typed Kubernetes Events. Condition/event vocabularies are centralized as Go constants.
5. **Contract independence.** The routing, TLS, auth, and landing-page contracts each degrade gracefully and do not depend on one another being present.

## Common Development Commands

Dependencies (kustomize, controller-gen, setup-envtest, golangci-lint, crd-ref-docs) are auto-installed into `bin/` on first use — you do not need to install them globally.

### Building & Running

```bash
make build                    # build bin/manager from ./cmd/operator
make run                      # run the controller locally against your kubecontext
make docker-build             # build the operator image (IMG=... to override tag)
make docker-buildx            # multi-arch build (arm64,amd64,s390x,ppc64le)
```

### Testing

```bash
make test                     # unit tests via envtest, with coverage (cover.out)
make test-unit                # controller unit tests with coverage
make test-unit-html           # same, plus an HTML coverage report
make test-e2e                 # Ginkgo e2e suite (-tags=e2e); requires a live cluster
make test-e2e-smoke           # focused smoke subset
make test-e2e-parallel        # e2e in parallel (procs=4)
```

### Code Quality

```bash
make fmt                      # go fmt
make vet                      # go vet
make lint                     # golangci-lint run
make lint-fix                 # golangci-lint run --fix
```

### Codegen (run after API changes)

```bash
make manifests                # controller-gen: CRDs + ClusterRole from kubebuilder markers
make generate                 # controller-gen: DeepCopy code (zz_generated.deepcopy.go)
make generate-dev             # shortcut: manifests + generate
make docs                     # crd-ref-docs: regenerate docs/api-reference.md from api/v1
make generate-all             # manifests + generate + build-installer + helm-chart
```

**After editing anything in `api/v1/`, run `make generate-dev` (and `make docs`) and commit the generated files alongside your source change.** CI fails if generated files, manifests, or the API reference are out of sync.

### Installer / Helm

```bash
make build-installer          # kustomize build config/default -> dist/install.yaml
make helm-chart               # regenerate the Helm chart into dist/chart/ (kubebuilder helm plugin)
make helm-package             # helm package dist/chart -> dist/
```

### Deploying to a cluster

```bash
make install                  # apply the CRDs (kustomize config/crd)
make deploy IMG=<registry>/<image>:<tag>   # apply the full operator (config/default)
make undeploy                 # tear the operator down
make uninstall                # remove the CRDs
```

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

## Documentation

- **`docs/api-reference.md`** — generated CRD reference (`make docs`).
- **`docs/reconcilers/`** — reconciler architecture, condition/reason/event tables, pipeline diagrams (`routing.md`, `validation.md`, `authentication.md`).
- **`docs/design/`** — living design docs (auth contract, user-workload discovery, landing-page, routing/SecurityPolicy epic).
- **`docs/decisions/`** — ADR-style records (e.g. the webapi extraction to nebari-landing).
- **`docs/plans/`** — in-flight implementation plans.
- **`docs/maintainers/`** — release checklist / process / setup.
- **`docs/quickstart.md`, `docs/configuration-reference.md`, `docs/troubleshooting.md`, `docs/makefile-reference.md`** — user-facing guides.
- **[CONTRIBUTING.md](CONTRIBUTING.md)** — the human-facing contribution process.

## Dependencies

Core libraries (see `go.mod`; module `github.com/nebari-dev/nebari-operator`, Go 1.25):

- `sigs.k8s.io/controller-runtime` — operator framework.
- `k8s.io/api` / `apimachinery` / `client-go` — Kubernetes API machinery.
- `sigs.k8s.io/gateway-api` — HTTPRoute / Gateway types.
- `github.com/cert-manager/cert-manager` — Certificate types.
- `github.com/envoyproxy/gateway` — `SecurityPolicy` (OIDC at the gateway).
- `github.com/Nerzal/gocloak/v13` — Keycloak admin client.
- `github.com/onsi/ginkgo/v2` + `gomega` — test framework.

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
