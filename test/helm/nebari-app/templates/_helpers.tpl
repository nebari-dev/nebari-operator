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
      "name"      $component
      "namespace" $top.Release.Namespace
      "labels"    (include "fixture.labels" (dict "top" $top "component" $component) | fromYaml)
    )
    "spec" .spec
) -}}
{{- end -}}
