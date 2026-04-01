{{/*
Build a Datadog IN filter from addonNamespaces for monitor queries.
Input: ",datadog,keda,istio-system" → Output: "kube_namespace IN (datadog,keda,istio-system)"
Deduplicates namespaces (e.g. multiple istio addons → single istio-system).
*/}}
{{- define "monitors.namespaceIn" -}}
{{- $raw := .Values.addonNamespaces | trimPrefix "," -}}
{{- $namespaces := splitList "," $raw | uniq -}}
kube_namespace IN ({{ join "," $namespaces }})
{{- end -}}

{{/*
Build a Datadog event query namespace filter from addonNamespaces.
Input: ",datadog,keda,istio-system" → Output: "kube_namespace:(datadog OR keda OR istio-system)"
Used in event-v2 monitors where IN syntax is not supported.
*/}}
{{- define "monitors.namespaceInEvents" -}}
{{- $raw := .Values.addonNamespaces | trimPrefix "," -}}
{{- $namespaces := splitList "," $raw | uniq -}}
kube_namespace:({{ join " OR " $namespaces }})
{{- end -}}

{{/*
Render notification handles for monitor messages.
Outputs the handles string if configured, empty string otherwise.
*/}}
{{- define "monitors.notificationHandles" -}}
{{- with .Values.notificationHandles }}{{ . }}{{ end -}}
{{- end -}}
