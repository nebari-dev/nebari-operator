{{- define "nebari-app.nebariApp" -}}
{{- $_ := required "metadata.name is required" .metadata.name -}}
{{- $_ := required "spec.hostname is required" .spec.hostname -}}
{{- $_ := required "spec.service.name is required" .spec.service.name -}}
{{- $_ := required "spec.service.port is required" .spec.service.port -}}
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  {{- toYaml .metadata | nindent 2 }}
spec:
  {{- toYaml .spec | nindent 2 }}
{{- end -}}
