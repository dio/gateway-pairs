{{/*
System namespace: tr-system-<index>
*/}}
{{- define "eg-pair.systemNamespace" -}}
{{- printf "tr-system-%d" (.Values.pair.index | int) -}}
{{- end }}

{{/*
Dataplane namespace: tr-dataplane-<index>
*/}}
{{- define "eg-pair.dataplaneNamespace" -}}
{{- printf "tr-dataplane-%d" (.Values.pair.index | int) -}}
{{- end }}

{{/*
GatewayClass name: tr-<index>
*/}}
{{- define "eg-pair.gatewayClassName" -}}
{{- printf "tr-%d" (.Values.pair.index | int) -}}
{{- end }}

{{/*
Release-scoped prefix for cluster-scoped resource names.
*/}}
{{- define "eg-pair.prefix" -}}
{{- printf "eg-pair-%d" (.Values.pair.index | int) -}}
{{- end }}
