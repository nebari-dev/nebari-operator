{{/* fixture.component-name — deterministic name for a component. */}}
{{- define "fixture.component-name" -}}
{{- .component -}}
{{- end -}}

{{/* fixture.labels — minimal label set for a component. */}}
{{- define "fixture.labels" -}}
app.kubernetes.io/name: {{ .component }}
app.kubernetes.io/instance: {{ .top.Release.Name }}
{{- end -}}

{{/* fixture.nebariApp — wrapper building metadata and forwarding spec. */}}
{{- define "fixture.nebariApp" -}}
{{- $top := .top -}}
{{- $component := .component -}}
{{- include "nebari-app.nebariApp" (dict
    "metadata" (dict
      "name"      (include "fixture.component-name" (dict "top" $top "component" $component))
      "namespace" $top.Release.Namespace
      "labels"    (include "fixture.labels" (dict "top" $top "component" $component) | fromYaml)
    )
    "spec" .spec
) -}}
{{- end -}}
