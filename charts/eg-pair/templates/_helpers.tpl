{{/*
Name assembly helper.

Rules:
  - namePrefix: auto-appends "-" if non-empty and not already ending with one.
  - nameSuffix: coerced to string. Appended as-is; caller controls separator.
  - nameSuffix empty + index > 0 → suffix defaults to "-{index}".
  - nameSuffix empty + index == 0 → no suffix (single/unnamed pair).

Examples (role = "system"):
  namePrefix=tr,   index=1 → tr-system-1
  namePrefix=tars, index=1 → tars-system-1
  namePrefix=tars, index=0 → tars-system
  namePrefix=tars, index=1, nameSuffix=1 → tars-system1
  namePrefix="",   index=1 → system-1
  namePrefix="",   index=0 → system
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

{{/*
System namespace: {prefix}-system-{id}
This is ALSO the Helm release namespace (--namespace flag at install time).
Controller, proxy, Gateway, HTTPRoutes all live here. One namespace per pair.
*/}}
{{- define "eg-pair.systemNamespace" -}}
{{- list "system" . | include "eg-pair.nameFor" -}}
{{- end }}

{{/*
eg-pair.dataplaneNamespace retained for backward compat -- unused.
In GatewayNamespace mode proxy lands in the Gateway's namespace (= systemNS).
*/}}
{{- define "eg-pair.dataplaneNamespace" -}}
{{- list "dataplane" . | include "eg-pair.nameFor" -}}
{{- end }}

{{/*
GatewayClass name: prefix + suffix only (no role fragment).
e.g. tr-1, tars-1, tars-prod
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
Cluster-scoped resource prefix (for ClusterRoles, ClusterRoleBindings).
e.g. eg-pair-tr-1, eg-pair-tars-prod
*/}}
{{- define "eg-pair.prefix" -}}
eg-pair-{{ include "eg-pair.gatewayClassName" . }}
{{- end }}
