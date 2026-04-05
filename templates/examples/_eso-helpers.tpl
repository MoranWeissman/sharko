{{/*
External Secrets Operator IRSA helper.

Injects the AWS IAM Role ARN as a service account annotation for IRSA authentication.
The role ARN should be provided in per-cluster values.

Usage in your ESO global values:
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: '{{ .Values.external-secrets.irsaRoleArn | default "" }}'
*/}}
{{- define "eso.serviceAccountAnnotations" -}}
{{- if .Values.irsaRoleArn -}}
eks.amazonaws.com/role-arn: {{ .Values.irsaRoleArn }}
{{- end -}}
{{- end -}}

{{/*
External Secrets ClusterSecretStore values helper.

Generates the values block for deploying a ClusterSecretStore alongside ESO.
This is typically used as an additional source in the addon catalog.

Usage: add as an additional source in the ESO addon catalog entry:
  additionalSources:
    - path: "charts/eso-config"
      parameters:
        awsRegion: "{{ .Values.clusterGlobalValues.region }}"
*/}}
{{- define "eso.clusterSecretStoreValues" -}}
provider:
  aws:
    service: SecretsManager
    region: {{ .Values.awsRegion | default "us-east-1" }}
    auth:
      jwt:
        serviceAccountRef:
          name: external-secrets
          namespace: external-secrets
{{- end -}}
