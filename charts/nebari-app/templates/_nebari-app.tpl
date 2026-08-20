{{/*
  Render a complete `NebariApp` custom resource.

  Usage:
    {{ include "nebari-app.nebariApp" (dict "metadata" $metadata "spec" $spec "ctx" $ctx) }}

  Parameters:
    - `$metadata`: Metadata mapping, such as name, namespace, and labels. Templates will be expanded using ebari-app.deepTplJson.
    - `$spec`: `NebariApp` CR specification. Templates will be expanded using nebari-app.deepTplJson.
    - `$ctx`: Optional templating context. If omitted, an empty context is used.

  Returns:
    rendered NebariApp CR.
*/}}
{{- define "nebari-app.nebariApp" -}}
{{- $ctx := .ctx | default dict -}}
{{- $metadata := include "nebari-app.deepTplJson" (dict "ctx" $ctx "value" .metadata) | fromJson -}}
{{- $_ := required "metadata.name is required" $metadata.name -}}
{{- $spec := include "nebari-app.deepTplJson" (dict "ctx" $ctx "value" .spec) | fromJson -}}
{{- $_ := required "spec.hostname is required" $spec.hostname -}}
{{- $_ := required "spec.service is required" $spec.service -}}
{{- $_ := required "spec.service.name is required" $spec.service.name -}}
{{- $_ := required "spec.service.port is required" $spec.service.port -}}
{{- if lt (int $spec.service.port) 1 -}}{{- fail "spec.service.port must be >= 1" -}}{{- end -}}
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  {{- toYaml $metadata | nindent 2 }}
spec:
  {{- toYaml $spec | nindent 2 }}
{{- end -}}
