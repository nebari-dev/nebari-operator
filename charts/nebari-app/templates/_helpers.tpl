{{- define "nebari-app.internal.deepTplJson" -}}
{{- $ctx := .ctx -}}
{{- $value := .value -}}
{{- $tplValue := "" -}}

{{- /* maps: recursively handle keys and values */ -}}
{{- if kindIs "map" $value -}}
    {{- $tplValue = dict -}}
    {{- range $k, $v := $value -}}
        {{- $_ := set $tplValue (tpl $k $ctx) (include "nebari-app.internal.deepTplJson" (dict "ctx" $ctx "value" $v) | fromJson).tplValue -}}
    {{- end -}}

{{- /* slices: recursively handle elements */ -}}
{{- else if kindIs "slice" $value -}}
    {{- $tplValue = list -}}
    {{- range $v := $value -}}
        {{- $tplValue = append $tplValue (include "nebari-app.internal.deepTplJson" (dict "ctx" $ctx "value" $v) | fromJson).tplValue -}}
    {{- end -}}

{{- /* templates: render */ -}}
{{- else if and (kindIs "string" $value) (contains "{{" $value) -}}
    {{- $probe := printf "{\"ok\": true, \"tplValue\": %s}" (tpl $value $ctx) | fromJson -}}
    {{- if (not $probe.ok) }}
        {{- fail (printf "rendering the template %s does not result in valid JSON" ($value | quote)) -}}
    {{- end -}}
    {{- $tplValue = $probe.tplValue -}}

{{- /* any other type: return as-is */ -}}
{{- else -}}
    {{- $tplValue = $value -}}
{{- end -}}

{{- dict "tplValue" $tplValue | toJson -}}
{{- end -}}

{{/*
    Apply template expansion to all strings in a nested structure.

    Template expressions must render to valid JSON.

    Usage:
        {{ include "nebari-app.deepTplJson" (dict "ctx" $ctx "value" $value) }}

    Parameters:
        - $ctx: Context for template rendering.
        - $value: Arbitrarily nested structure.

    Returns:
        JSON string of the rendered $value.

    Example:
        {{ include "nebari-app.deepTplJson" (dict "ctx" . "value" .Values.config) | fromJson }}
*/}}
{{- define "nebari-app.deepTplJson" -}}
{{- $_ := required "ctx is required" .ctx -}}
{{- $_ := required "value is required" .value -}}
{{- (include "nebari-app.internal.deepTplJson" . | fromJson).tplValue | toJson  -}}
{{- end -}}
