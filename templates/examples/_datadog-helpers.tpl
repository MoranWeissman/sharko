{{/*
Datadog containerIncludeLogs helper.

Computes the container include/exclude logs configuration based on enabled addon namespaces.
Uses the _sharko.enabledAddonNamespaces value computed by Sharko server.

Usage in your datadog global values:
  containerIncludeLogs: '{{ include "datadog.containerIncludeLogs" . }}'
*/}}
{{- define "datadog.containerIncludeLogs" -}}
{{- $namespaces := splitList "," (default "" .Values._sharko.enabledAddonNamespaces) -}}
{{- $rules := list -}}
{{- range $ns := $namespaces -}}
  {{- if $ns -}}
    {{- $rules = append $rules (printf "{\"source\":\"kube_%s\",\"service\":\"%s\"}" $ns $ns) -}}
  {{- end -}}
{{- end -}}
[{{ join "," $rules }}]
{{- end -}}

{{/*
Datadog ignoreDifferences helper.

Returns a YAML block of ignoreDifferences entries for Datadog-specific resources
that ArgoCD should not track (managed by the Datadog operator).

Usage in your addon catalog entry:
  ignoreDifferences:
    {{ include "datadog.ignoreDifferences" . | nindent 4 }}
*/}}
{{- define "datadog.ignoreDifferences" -}}
- group: apps
  kind: DaemonSet
  name: datadog
  jsonPointers:
    - /spec/template/metadata/annotations
    - /spec/template/spec/containers/0/env
- group: apps
  kind: Deployment
  name: datadog-cluster-agent
  jsonPointers:
    - /spec/template/metadata/annotations
{{- end -}}
