# nebari-app

A [library](https://helm.sh/docs/chart_template_guide/getting_started/#the-chart-yaml-file) Helm chart providing reusable templates for rendering `NebariApp` custom resource instances.

## Install as a dependency

Add the chart to your consumer chart's `Chart.yaml`:

```yaml
dependencies:
  - name: nebari-app
    version: ">=0.1.0"
    repository: oci://quay.io/nebari/charts
```

or via a local file path during development:

```yaml
dependencies:
  - name: nebari-app
    version: ">=0.1.0"
    repository: file://../nebari-app
```

then run `helm dependency build`.

## Templates

| Template | Description |
|----------|-------------|
| [`nebari-app.nebariApp`](#nebari-appnebariapp) | Renders a complete `NebariApp` resource from `metadata` and `spec` dicts |
| [`nebari-app.deepTplJson`](#nebari-appdeeptpljson) | Applies template expansion to all strings in a nested structure, automatically parsing JSON outputs |

> New here? For a step-by-step, end-to-end walkthrough (dependency → values → template → apply → verify), start with [Onboarding an app with the nebari-app Helm chart](../../docs/using-the-nebari-app-chart.md). This README is the reference for the template contracts.

### nebari-app.nebariApp

Renders a complete `NebariApp` custom resource.

#### Usage

```yaml
{{ include "nebari-app.nebariApp" (dict "metadata" $metadata "spec" $spec "ctx" $ctx) }}
```

#### Parameters

- `$metadata`: Metadata mapping, such as name, namespace, and labels. Templates will be expanded using [`nebari-app.deepTplJson`](#nebari-appdeeptpljson).
- `$spec`: `NebariApp` CR specification. Templates will be expanded using [`nebari-app.deepTplJson`](#nebari-appdeeptpljson).
- `$ctx`: Optional templating context. If omitted, an empty context is used.

#### Required fields

The template enforces the presence of the following fields

- `$metadata.name`
- `$spec.hostname`
- `$spec.service.name`
- `$spec.service.port`

as well as additionally the validity if the following fields

- `$spec.service.port`

All other validation (the rest of the `NebariAppSpec` schema) happens API-server-side at apply time. To catch schema errors before deployment, pipe `helm template` output through `kubectl apply --dry-run=server -f -`. This requires a cluster with the NebariApp CRD installed.

#### Dynamic defaults

The template uses `nebari-app.deepTplJson` internally to render both `$metadata` and `$spec`. This means you can embed template expressions directly in your values, and they will be expanded at render time:

```yaml
# values.yaml
service:
  port: 80

nebariApp:
  hostname: "{{ printf "%s.example.com" .Release.Name }}"
  service:
    name: "{{ printf "%s-service" .Release.Name }}"
    port: "{{ .Values.service.port }}"
```

```yaml
# template.yaml
{{ include "nebari-app.nebariApp" (dict
    "metadata" (dict
      "name"      .Release.Name
      "namespace" .Release.Namespace
      "labels"    (dict "app.kubernetes.io/name" .Chart.Name)
    )
    "spec" .Values.nebariApp
    "ctx"  .
) }}
```

#### Multiple NebariApps from one chart

To avoid repeating `metadata` construction at every call site, define a thin consumer-owned wrapper template that builds `metadata` from `top` and `component` and forwards `spec`:

```yaml
{{ define "mychart.nebariApp" -}}
{{- $top := .top -}}
{{- $component := .component -}}
{{- include "nebari-app.nebariApp" (dict
    "metadata" (dict
      "name"      $component
      "namespace" $top.Release.Namespace
    )
    "spec" .spec
) -}}
{{- end }}
```

Then each call site passes only `top`, `component`, and `spec`:

```yaml
{{ include "mychart.nebariApp" (dict "top" . "component" "frontend" "spec" .Values.nebariApp) }}
```

#### Namespace requirement

The Nebari operator only reconciles `NebariApp` resources whose target namespace carries the label `nebari.dev/managed=true`. This chart does not template that label. Ensure the namespace is opted in separately (e.g. via a `Namespace` resource in your consumer chart).

### nebari-app.deepTplJson

Apply template expansion to all strings in a nested structure, automatically parsing JSON outputs. This is useful for embedding dynamic values (computed at render time) directly in your values, eliminating the need for manual merging patterns.

#### Usage

```yaml
{{ include "nebari-app.deepTplJson" (dict "ctx" $ctx "value" $value) | fromJson }}
```

#### Parameters

- `$ctx`: Context for template rendering.
- `$value`: Arbitrarily nested structure.

#### Behavior

- Recursively traverses maps and slices
- Applies `tpl` to all strings
- If a template output is valid JSON, parses it and uses the parsed value
- Returns JSON string of the fully rendered structure

#### Example

```yaml
{{ include "nebari-app.deepTplJson" (dict "ctx" . "value" .Values.config) | fromJson }}
```
