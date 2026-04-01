{{/*
Expand the name of the chart.
*/}}
{{- define "aap.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "aap.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "aap.labels" -}}
helm.sh/chart: {{ include "aap.name" . }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "aap.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: argocd-addons-platform
{{- end }}

{{/*
Selector labels
*/}}
{{- define "aap.selectorLabels" -}}
app.kubernetes.io/name: {{ include "aap.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name
*/}}
{{- define "aap.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "aap.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Secret name
*/}}
{{- define "aap.secretName" -}}
{{- if .Values.existingSecret }}
{{- .Values.existingSecret }}
{{- else }}
{{- include "aap.fullname" . }}
{{- end }}
{{- end }}

{{/*
ArgoCD namespace for RBAC
*/}}
{{- define "aap.argocdNamespace" -}}
{{- .Values.rbac.argocdNamespace | default "argocd" }}
{{- end }}
