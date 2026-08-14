# Onboarding an app with the `nebari-app` Helm chart

This is a task-oriented walkthrough: it takes you from an empty chart to a running, operator-managed app using the [`nebari-app`](../charts/nebari-app) library chart. It packages the same `NebariApp` resource you write by hand in the [Quick Start](quickstart.md), so it is reusable, versioned, and shippable as a Nebari Software Pack.

- For the **template contract and every option** (required fields, `mergeOverwrite`, multiple apps per chart), see the chart's own [`charts/nebari-app/README.md`](../charts/nebari-app/README.md).
- For **complete, copy-me examples** in several styles (raw YAML, Helm, Kustomize, wrapping an existing chart), see [`nebari-dev/software-pack-template`](https://github.com/nebari-dev/software-pack-template).

This page is the connective tissue between those two: the end-to-end path, in order.

## What the chart is

`nebari-app` is a Helm **library** chart. It is not installable on its own — it exposes one named template, `nebari-app.nebariApp`, that renders a `NebariApp` custom resource from a `metadata` dict and a `spec` dict. You consume it as a dependency of *your* chart (a "Software Pack") and call the template from your own manifest.

Everything the operator does for the app — routing, TLS, SSO, landing-page registration — is driven by the `spec` you pass. The chart is a thin, validated wrapper around that one resource.

## Before you start

- A cluster with the Nebari Operator installed and the `NebariApp` CRD present (`make deploy`, or see the [Quick Start](quickstart.md)).
- Helm 3.8+ (for OCI registry support).
- Your app already has, or will have, a `Service` for the operator to route to.

## Step 1 — Add `nebari-app` as a dependency

In your consumer chart's `Chart.yaml`:

```yaml
apiVersion: v2
name: my-pack
description: My Nebari Software Pack
type: application
version: 0.1.0
appVersion: "1.0.0"

dependencies:
  - name: nebari-app
    version: ">=0.1.0-0"
    repository: oci://quay.io/nebari/charts
```

During local development you can point at a checked-out copy instead:

```yaml
dependencies:
  - name: nebari-app
    version: ">=0.1.0-0"
    repository: file://../nebari-app
```

Then pull it in:

```bash
helm dependency build
```

## Step 2 — Put the app's config in `values.yaml`

Keep the whole `NebariApp` spec under one key so callers can override it cleanly:

```yaml
# values.yaml
nebariApp:
  hostname: my-app.nebari.example.com
  service:
    name: my-app
    port: 8080
  routing:
    routes:
      - pathPrefix: /
  # auth, landingPage, etc. are optional — add them as you need them.
  # Full field list: ../charts/nebari-app/README.md and docs/api-reference.md
```

## Step 3 — Render the `NebariApp` from a template

Add one manifest that calls the library template, passing `metadata` and the `spec` from values:

```yaml
# templates/nebariapp.yaml
{{ include "nebari-app.nebariApp" (dict
    "metadata" (dict
      "name"      .Release.Name
      "namespace" .Release.Namespace
      "labels"    (dict "app.kubernetes.io/name" .Chart.Name)
    )
    "spec" .Values.nebariApp
) }}
```

The template **aborts the render** if any required field is missing or empty: `metadata.name`, `spec.hostname`, `spec.service.name`, `spec.service.port` (and rejects `port < 1`). Everything else in the spec is validated API-server-side at apply time.

> Shipping the workload too? Add your `Deployment` and `Service` as normal templates in the same chart. The `service.name`/`service.port` in the spec must match that `Service`. See the `basic-nginx` example in `software-pack-template` for a chart that bundles both.

## Step 4 — Opt the namespace in

The operator **ignores** `NebariApp` resources in namespaces that are not opted in. Label the target namespace once:

```bash
kubectl label namespace my-namespace nebari.dev/managed=true
```

The chart does not template this label (it would need cluster-admin over a shared resource); do it out-of-band, or add a `Namespace` resource to your chart if it owns the namespace.

## Step 5 — Validate before you apply

Render locally and, ideally, dry-run against the API server to catch schema errors the library template does not check:

```bash
# Render only — catches missing required fields:
helm template my-release . -n my-namespace

# Full schema check — needs a cluster with the CRD installed:
helm template my-release . -n my-namespace | kubectl apply --dry-run=server -f -
```

## Step 6 — Install and verify

```bash
helm install my-release . -n my-namespace --create-namespace
```

Watch the operator reconcile it. The `NebariApp` walks its conditions to `Ready`:

```bash
kubectl get nebariapp -n my-namespace
kubectl describe nebariapp my-release -n my-namespace
```

A healthy app reports `Ready=True` with `RoutingReady`, `TLSReady`, and (if auth is enabled) `AuthReady` all true, and populates `status.serviceDiscovery`. The operator will have created the `HTTPRoute` (and `Certificate`, `SecurityPolicy`, etc. as configured):

```bash
kubectl get httproute -n my-namespace
```

## Troubleshooting

| Symptom | Cause | Fix |
| --- | --- | --- |
| `helm template` fails with `spec.hostname is required` (or `service.name`/`service.port`) | A required field is missing or empty in your values | Set it in `values.yaml`; these are enforced at render time by the library template |
| `spec.service.port must be >= 1` | Port rendered as `0` or negative | `required` treats `0` as present — the template rejects it explicitly; pass a real port |
| Resource applies but the operator never touches it (no conditions, no HTTPRoute) | Namespace not opted in | `kubectl label namespace <ns> nebari.dev/managed=true` |
| `no matches for kind "NebariApp"` on apply | CRD not installed | Install the operator / CRDs (`make install` or `make deploy`) |
| `AuthReady=False` | Keycloak unreachable or auth config wrong | Check the operator logs and the `auth` block; see [docs/configuration-reference.md](configuration-reference.md) |

## Where to go next

- [`charts/nebari-app/README.md`](../charts/nebari-app/README.md) — the full template contract: `mergeOverwrite` for computed service values, emitting multiple `NebariApp`s from one chart, and the exact required-field list.
- [docs/configuration-reference.md](configuration-reference.md) — every `spec` field with examples.
- [docs/api-reference.md](api-reference.md) — the generated CRD reference.
- [`nebari-dev/software-pack-template`](https://github.com/nebari-dev/software-pack-template) — complete runnable Software Packs to copy from.
