{{- define "nebari-app.internal.deepTplJson" -}}
{{- $ctx := .ctx -}}
{{- $value := .value -}}
{{- $tplValue := "" -}}

{{- /* maps: recursively template keys and values */ -}}
{{- if kindIs "map" $value -}}
    {{- $tplValue = dict -}}
    {{- range $k, $v := $value -}}
    {{- $_ := set $tplValue (tpl $k $ctx) (include "nebari-app.internal.deepTplJson" (dict "ctx" $ctx "value" $v) | fromJson).tplValue -}}
{{- end -}}

{{- /* slices: recursively template elements */ -}}
{{- else if kindIs "slice" $value -}}
    {{- $tplValue = list -}}
    {{- range $v := $value -}}
    {{- $tplValue = append $tplValue (include "nebari-app.internal.deepTplJson" (dict "ctx" $ctx "value" $v) | fromJson).tplValue -}}
{{- end -}}

{{- /* strings: expand templates */ -}}
{{- else if kindIs "string" $value -}}
    {{- $tplValue = tpl $value $ctx -}}
    {{- /* try parse output as JSON and use it if valid */ -}}
    {{- $tplValueFromJson := (printf "{\"tplValue\": %s}" $tplValue | fromJson).tplValue -}}
    {{- if $tplValueFromJson }}
        {{- $tplValue = $tplValueFromJson -}}
{{- end -}}

{{- /* any other type: return as-is */ -}}
{{- else -}}
    {{- $tplValue = $value -}}
{{- end -}}

{{- dict "tplValue" $tplValue | toJson -}}
{{- end -}}

{{/*
 Apply tpl to all strings in a nested structure.
 
 If any template output is valid JSON, it is automatically parsed.

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
