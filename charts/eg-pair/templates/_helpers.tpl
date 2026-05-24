{{/*
Name assembly helper. Produces the common prefix+role+suffix fragment.

Rules:
  - namePrefix: a trailing "-" is added automatically if non-empty and not
    already ending with one.
  - nameSuffix: coerced to string (--set passes bare numbers as int64).
    Appended as-is; caller controls any separator.
  - When nameSuffix is empty and pair.index > 0, suffix defaults to "-{index}".
  - When nameSuffix is empty and index == 0, no suffix (single/unnamed pair).

Examples (role = "system"):
  namePrefix=tr,   index=1, nameSuffix=""   → tr-system-1
  namePrefix=tars, index=1, nameSuffix=""   → tars-system-1
  namePrefix=tars, index=0, nameSuffix=""   → tars-system
  namePrefix=tars, index=1, nameSuffix="1"  → tars-system1
  namePrefix="",   index=1, nameSuffix=""   → system-1
  namePrefix="",   index=0, nameSuffix=""   → system
*/}}
{{- define "eg-pair.nameFor" -}}
{{- $role := index . 0 -}}
{{- $ctx  := index . 1 -}}
{{- $p := $ctx.Values.pair.namePrefix | toString -}}
{{- if and $p (not (hasSuffix "-" $p)) -}}
  {{- $p = printf "%s-" $p -}}
{{- end -}}
{{- $s := $ctx.Values.pair.nameSuffix | toString -}}
{{- if and (not $s) (gt ($ctx.Values.pair.index | int) 0) -}}
  {{- $s = printf "-%d" ($ctx.Values.pair.index | int) -}}
{{- end -}}
{{- printf "%s%s%s" $p $role $s -}}
{{- end }}

{{- define "eg-pair.releaseNamespace" -}}
{{- list "release" . | include "eg-pair.nameFor" -}}
{{- end }}

{{- define "eg-pair.systemNamespace" -}}
{{- list "system" . | include "eg-pair.nameFor" -}}
{{- end }}

{{- define "eg-pair.dataplaneNamespace" -}}
{{- list "dataplane" . | include "eg-pair.nameFor" -}}
{{- end }}

{{/*
GatewayClass name: prefix + suffix only (no role fragment).
e.g. tr-1, tars-1, tars-prod
The GatewayClass name must be unique per pair and cluster.
When index=0 and nameSuffix="" the name is just the prefix (stripped trailing hyphen).
*/}}
{{- define "eg-pair.gatewayClassName" -}}
{{- $p := .Values.pair.namePrefix | toString -}}
{{- if and $p (not (hasSuffix "-" $p)) -}}
  {{- $p = printf "%s-" $p -}}
{{- end -}}
{{- $s := .Values.pair.nameSuffix | toString -}}
{{- if and (not $s) (gt (.Values.pair.index | int) 0) -}}
  {{- $s = printf "%d" (.Values.pair.index | int) -}}
{{- end -}}
{{- trimSuffix "-" (printf "%s%s" $p $s) -}}
{{- end }}

{{/*
Release-scoped prefix for cluster-scoped resource names.
Always eg-pair-{index} for consistent Helm release tracking.
*/}}
{{- define "eg-pair.prefix" -}}
{{- printf "eg-pair-%d" (.Values.pair.index | int) -}}
{{- end }}
