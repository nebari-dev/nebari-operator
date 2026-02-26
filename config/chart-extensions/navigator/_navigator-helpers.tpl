{{/*
Navigator resource name.
Production Helm installs use the scoped name (release-chart-navigator).
Set navigator.nameOverride to get a fixed name (useful for dev/E2E rendering).
*/}}
{{- define "nebari-operator.navigatorName" -}}
{{- if and .Values.navigator .Values.navigator.nameOverride -}}
{{- .Values.navigator.nameOverride -}}
{{- else -}}
{{- include "nebari-operator.resourceName" (dict "suffix" "navigator" "context" .) -}}
{{- end -}}
{{- end }}

{{/*
Navigator RBAC resource name (reader ClusterRole / ClusterRoleBinding).
Appends -reader to nameOverride when set, otherwise uses the scoped helper.
*/}}
{{- define "nebari-operator.navigatorReaderName" -}}
{{- if and .Values.navigator .Values.navigator.nameOverride -}}
{{- printf "%s-reader" .Values.navigator.nameOverride -}}
{{- else -}}
{{- include "nebari-operator.resourceName" (dict "suffix" "navigator-reader" "context" .) -}}
{{- end -}}
{{- end }}
