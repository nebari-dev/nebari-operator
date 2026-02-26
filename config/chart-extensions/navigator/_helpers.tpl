{{/*
Navigator component name.
If navigator.nameOverride is set use it directly (useful for local dev / E2E rendering
where simple, predictable resource names are preferred).
Otherwise falls back to <fullname>-navigator.
*/}}
{{- define "nebari-operator.navigatorName" -}}
{{- if and .Values.navigator .Values.navigator.nameOverride }}
{{- .Values.navigator.nameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-navigator" (include "nebari-operator.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Navigator RBAC reader role/binding name.
*/}}
{{- define "nebari-operator.navigatorReaderName" -}}
{{- printf "%s-reader" (include "nebari-operator.navigatorName" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
