{{/*
WebAPI resource name.
Production Helm installs use the scoped name (release-chart-webapi).
Set webapi.nameOverride to get a fixed name (useful for dev/E2E rendering).
*/}}
{{- define "nebari-operator.webapiName" -}}
{{- if and .Values.webapi .Values.webapi.nameOverride -}}
{{- .Values.webapi.nameOverride -}}
{{- else -}}
{{- include "nebari-operator.resourceName" (dict "suffix" "webapi" "context" .) -}}
{{- end -}}
{{- end }}

{{/*
WebAPI RBAC resource name (reader ClusterRole / ClusterRoleBinding).
Appends -reader to nameOverride when set, otherwise uses the scoped helper.
*/}}
{{- define "nebari-operator.webapiReaderName" -}}
{{- if and .Values.webapi .Values.webapi.nameOverride -}}
{{- printf "%s-reader" .Values.webapi.nameOverride -}}
{{- else -}}
{{- include "nebari-operator.resourceName" (dict "suffix" "webapi-reader" "context" .) -}}
{{- end -}}
{{- end }}
