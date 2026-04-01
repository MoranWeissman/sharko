{{/*
============================================================================
Helper Functions for ApplicationSet Template
============================================================================
These helpers generate Helm configuration blocks for addon deployments.
Keeps the main appset template clean by extracting addon-specific logic.
*/}}

{{/*
Generate Datadog parameters block.
Includes: dd_tags (dynamic per-cluster, from ArgoCD cluster secret annotations).

Usage: {{ include "datadog.parameters" . | nindent 12 }}
*/}}
{{- define "datadog.parameters" -}}
parameters:
  - name: datadog.tags
    value: '{{`{{index .metadata.annotations "dd_tags"}}`}}'
{{- end -}}

{{/*
Generate ESO valuesObject block.
Injects IRSA role ARN for the ESO service account using cluster annotations.
Convention: arn:aws:iam::<accountId>:role/EKS-ESO-<clusterName>

Usage: {{ include "eso.valuesObject" . | nindent 12 }}
*/}}
{{- define "eso.valuesObject" -}}
valuesObject:
  serviceAccount:
    name: external-secrets
    annotations:
      eks.amazonaws.com/role-arn: 'arn:aws:iam::{{`{{.metadata.annotations.accountId}}`}}:role/EKS-ESO-{{`{{.name}}`}}'
{{- end -}}

{{/*
Generate comma-separated list of addon namespaces enabled on a cluster.
Outputs ArgoCD go-template conditionals that evaluate at ApplicationSet render time.
Each enabled addon emits ",<namespace>" — the leading comma is stripped by the consumer.
Deduplication (e.g. multiple istio addons → single istio-system) is handled in the dashboard chart.

Usage: {{ include "addon.namespacesCSV" $ }}
*/}}
{{- define "addon.namespacesCSV" -}}
{{- range $a := $.Values.applicationsets -}}
  {{- $ns := ($a.namespace | default $a.appName) -}}
  {{`{{- if eq (index .metadata.labels "`}}{{ $a.appName }}{{`") "enabled" }},`}}{{ $ns }}{{`{{- end }}`}}
{{- end -}}
{{- end -}}

{{/*
Generate Datadog ignoreDifferences items.
The Datadog operator regenerates certain values at runtime, causing
constant out-of-sync state. These rules tell ArgoCD to ignore those diffs.
Returns list items only (no ignoreDifferences: key) for composability.

Usage: {{ include "datadog.ignoreDifferencesItems" . | nindent 8 }}
*/}}
{{- define "datadog.ignoreDifferencesItems" -}}
# Operator regenerates install_id and install_time on each reconciliation
- kind: ConfigMap
  name: datadog-kpi-telemetry-configmap
  jsonPointers:
    - /data/install_id
    - /data/install_time
# Cluster-agent token is auto-generated and rotated at runtime
- kind: Secret
  name: datadog-cluster-agent
  jsonPointers:
    - /data/token
# Token checksum annotations update when the cluster-agent token changes
- group: apps
  kind: DaemonSet
  name: datadog
  jqPathExpressions:
    - '.spec.template.metadata.annotations."checksum/clusteragent_token"'
- group: apps
  kind: Deployment
  name: datadog-cluster-agent
  jqPathExpressions:
    - '.spec.template.metadata.annotations."checksum/clusteragent_token"'
# Datadog Operator injects "generated:kubernetes" into tags on its CRDs
- group: datadoghq.com
  kind: DatadogMonitor
  jqPathExpressions:
    - .spec.tags
{{- end -}}
