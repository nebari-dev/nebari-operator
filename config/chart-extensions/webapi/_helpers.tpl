{{/*
Navigator component name.
If webapi.nameOverride is set use it directly (useful for local dev / E2E rendering
where simple, predictable resource names are preferred).
Otherwise falls back to <fullname>-webapi.
*/}}
{{- define "nebari-operator.webapiName" -}}
{{- if and .Values.webapi .Values.webapi.nameOverride }}
{{- .Values.webapi.nameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-webapi" (include "nebari-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Navigator RBAC reader role/binding name.
*/}}
{{- define "nebari-operator.webapiReaderName" -}}
{{- printf "%s-reader" (include "nebari-operator.webapiName" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
