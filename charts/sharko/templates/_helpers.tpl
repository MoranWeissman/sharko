{{/*
Expand the name of the chart.
*/}}
{{- define "sharko.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "sharko.fullname" -}}
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
{{- define "sharko.labels" -}}
helm.sh/chart: {{ include "sharko.name" . }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "sharko.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: sharko
{{- end }}

{{/*
Selector labels
*/}}
{{- define "sharko.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sharko.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name
*/}}
{{- define "sharko.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "sharko.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Secret name
*/}}
{{- define "sharko.secretName" -}}
{{- include "sharko.fullname" . }}
{{- end }}

{{/*
ArgoCD namespace for RBAC
*/}}
{{- define "sharko.argocdNamespace" -}}
{{- .Values.rbac.argocdNamespace | default "argocd" }}
{{- end }}
