# Migration Guide: ArgoCD Vault Plugin (AVP) to External Secrets Operator (ESO)

## Table of Contents
- [Why Migrate?](#why-migrate)
- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Migration Steps](#migration-steps)
- [Example Migrations](#example-migrations)
- [Rollback Plan](#rollback-plan)
- [FAQ](#faq)

---

## Why Migrate?

### Security Concerns with AVP

The ArgoCD team and security experts have identified significant security issues with ArgoCD Vault Plugin:

1. **Plaintext Secrets in Redis Cache**
   - AVP-rendered manifests are cached in Redis in plaintext
   - Secrets are stored unencrypted in ArgoCD's cache
   - Potential exposure if Redis is compromised

2. **Limited Audit Trail**
   - Difficult to track secret access and usage
   - No native secret rotation support
   - Limited visibility into secret lifecycle

3. **Official Recommendation**
   - ArgoCD team recommends External Secrets Operator
   - Better security posture
   - Native Kubernetes integration

### Benefits of ESO

1. **Enhanced Security**
   - Secrets stay in AWS Secrets Manager until needed
   - No plaintext caching in ArgoCD
   - Kubernetes-native secret rotation

2. **Better Performance**
   - No template rendering overhead during sync
   - Faster ArgoCD sync operations
   - Independent secret refresh cycles

3. **Improved Maintainability**
   - No plugin installation required in ArgoCD
   - Standard Kubernetes resources
   - Better GitOps practices

---

## Overview

### What Changes

**Before (AVP):**
```yaml
# Application uses AVP plugin
spec:
  source:
    plugin:
      name: argocd-vault-plugin-helm
    helm:
      values: |
        secret: <path:secret/data/myapp#password>
```

**After (ESO):**
```yaml
# Application uses standard Helm
spec:
  source:
    helm:
      values: |
        secretName: myapp-secret  # References ESO-created secret

# ESO creates the secret
---
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: myapp-secret
spec:
  secretStoreRef:
    name: aws-secretsmanager
  target:
    name: myapp-secret
  data:
    - secretKey: password
      remoteRef:
        key: secret/myapp
        property: password
```

### Migration Strategy

1. **Phase 1**: Install ESO alongside AVP (no disruption)
2. **Phase 2**: Migrate secrets one application at a time
3. **Phase 3**: Verify all applications using ESO
4. **Phase 4**: Remove AVP plugin from ArgoCD

---

## Prerequisites

### Required Components

1. **External Secrets Operator**
   - Version: 0.9.10+
   - Installed in target clusters
   - IAM role configured for AWS Secrets Manager access

2. **AWS Secrets Manager**
   - Secrets migrated to AWS Secrets Manager format
   - IAM policies configured
   - Proper secret naming conventions

3. **ArgoCD**
   - Version 2.10+ (recommended)
   - Access to modify applications
   - Ability to create ExternalSecret resources

### IAM Role Configuration

Create/update IAM role for ESO:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "secretsmanager:GetSecretValue",
        "secretsmanager:DescribeSecret"
      ],
      "Resource": [
        "arn:aws:secretsmanager:*:*:secret:datadog-*",
        "arn:aws:secretsmanager:*:*:secret:k8s-*",
        "arn:aws:secretsmanager:*:*:secret:cluster-*"
      ]
    }
  ]
}
```

---

## Migration Steps

### Step 1: Install ESO (If Not Already Installed)

ESO should already be installed via the cluster-addons solution. Verify:

```bash
# Check ESO is running
kubectl get pods -n external-secrets

# Expected output:
# external-secrets-*                    1/1     Running
# external-secrets-cert-controller-*    1/1     Running
# external-secrets-webhook-*            1/1     Running
```

If not installed, enable it in `values/addons-list.yaml`:

```yaml
- appName: external-secrets
  repoURL: https://charts.external-secrets.io
  chart: external-secrets
  environments:
    - env: dev
      version: 0.9.10
```

### Step 2: Create ClusterSecretStore or SecretStore

For cluster-wide secret access, create a ClusterSecretStore:

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: aws-secretsmanager
spec:
  provider:
    aws:
      service: SecretsManager
      region: eu-west-1
      auth:
        jwt:
          serviceAccountRef:
            name: external-secrets
            namespace: external-secrets
```

### Step 3: Migrate Secret References

For each application using AVP:

#### 3.1 Identify AVP Usage

Look for AVP path patterns in values files:
```yaml
# AVP pattern
apiKey: <path:secret-path#key>
password: <path:vault/data/myapp#password>
```

#### 3.2 Create ExternalSecret Resource

Create an ExternalSecret to fetch the secret:

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: myapp-credentials
  namespace: myapp
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secretsmanager
    kind: ClusterSecretStore
  target:
    name: myapp-credentials
    creationPolicy: Owner
  data:
    - secretKey: apiKey
      remoteRef:
        key: myapp-secrets
        property: apiKey
    - secretKey: password
      remoteRef:
        key: myapp-secrets
        property: password
```

#### 3.3 Update Application Values

Change application to reference the ESO-created secret:

```yaml
# Before (AVP)
apiKey: <path:myapp-secrets#apiKey>

# After (ESO)
existingSecret: myapp-credentials
# OR
apiKeySecret:
  name: myapp-credentials
  key: apiKey
```

#### 3.4 Remove AVP Plugin Reference

Update ArgoCD Application to use standard Helm:

```yaml
# Before
spec:
  source:
    plugin:
      name: argocd-vault-plugin-helm
    helm:
      valueFiles:
        - values.yaml

# After
spec:
  source:
    helm:
      valueFiles:
        - values.yaml
```

### Step 4: Verify Migration

```bash
# Check ExternalSecret status
kubectl get externalsecret -n myapp

# Expected output:
# NAME                  STORE                 REFRESH INTERVAL   STATUS
# myapp-credentials     aws-secretsmanager    1h                 SecretSynced

# Verify secret created
kubectl get secret myapp-credentials -n myapp

# Check application syncs successfully
argocd app get myapp
```

### Step 5: Monitor Application

```bash
# Watch application sync
argocd app sync myapp --watch

# Check application health
argocd app get myapp

# Verify pods are running with new secrets
kubectl get pods -n myapp
kubectl describe pod <pod-name> -n myapp
```

---

## Example Migrations

### Example 1: Datadog API Key Migration

This example shows how we migrated Datadog from AVP to ESO in this solution.

#### Before (AVP)

**Application: `bootstrap/templates/apps/datadog-apikey-secret.yaml`**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: datadog-apikey-secret
spec:
  source:
    plugin:
      name: argocd-vault-plugin-helm
    path: charts/datadog-apikey-secret
```

**Chart: `charts/datadog-apikey-secret/templates/secret.yaml`**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: {{ .Values.clusterName }}
  namespace: datadog
stringData:
  api-key: <path:datadog-api-keys-integration#{{ .Values.projectName }}-{{ .Values.env }}>
```

#### After (ESO)

**ExternalSecret: `charts/datadog-configuration/templates/datadog-apikey-externalsecret.yaml`**
```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: datadog-apikey-{{ .Values.clusterName }}
  namespace: datadog
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: {{ .Values.secretStore.name }}
    kind: {{ .Values.secretStore.kind }}
  target:
    name: {{ .Values.clusterName }}
    creationPolicy: Owner
    template:
      engineVersion: v2
      data:
        api-key: "{{`{{ .apikey }}`}}"
  data:
    - secretKey: apikey
      remoteRef:
        key: datadog-api-keys-integration
        property: "{{ .Values.projectName }}-{{ .Values.env }}"
```

**ApplicationSet: Multi-source configuration**
```yaml
sources:
  # Source 1: Official Datadog Helm chart
  - repoURL: https://helm.datadoghq.com
    chart: datadog
    targetRevision: 3.70.7
    helm:
      parameters:
        - name: 'datadog.apiKeyExistingSecret'
          value: '{{`{{.name}}`}}'

  # Source 2: Datadog configuration (ESO secrets)
  - repoURL: https://github.com/YOUR_ORG/argocd-cluster-addons.git
    targetRevision: dev
    path: charts/datadog-configuration
    helm:
      parameters:
        - name: 'clusterName'
          value: '{{`{{.name}}`}}'
```

### Example 2: Application Database Credentials

#### Before (AVP)

```yaml
# Application values.yaml
database:
  host: postgres.example.com
  username: <path:db/credentials#username>
  password: <path:db/credentials#password>
```

#### After (ESO)

**ExternalSecret:**
```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: db-credentials
  namespace: myapp
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secretsmanager
    kind: ClusterSecretStore
  target:
    name: db-credentials
    creationPolicy: Owner
  data:
    - secretKey: username
      remoteRef:
        key: db/credentials
        property: username
    - secretKey: password
      remoteRef:
        key: db/credentials
        property: password
```

**Updated values.yaml:**
```yaml
database:
  host: postgres.example.com
  existingSecret: db-credentials
  usernameKey: username
  passwordKey: password
```

---

## Rollback Plan

If issues occur during migration:

### Immediate Rollback

1. **Revert Application to AVP**
   ```bash
   # Re-enable AVP plugin in application
   kubectl patch application myapp -n argocd --type=merge -p '
   spec:
     source:
       plugin:
         name: argocd-vault-plugin-helm
   '
   ```

2. **Sync Application**
   ```bash
   argocd app sync myapp
   ```

### Full Rollback

1. Delete ExternalSecret resources
2. Restore original application manifests from Git
3. Re-sync applications

### Backup Before Migration

```bash
# Backup current application definition
kubectl get application myapp -n argocd -o yaml > myapp-backup.yaml

# Backup existing secrets
kubectl get secret -n myapp -o yaml > myapp-secrets-backup.yaml
```

---

## FAQ

### Q: Can I migrate gradually?

**A:** Yes! ESO and AVP can coexist. Migrate applications one at a time, testing each before proceeding.

### Q: What happens to existing secrets during migration?

**A:** ESO creates new secrets. Your applications will reference these new secrets. Old AVP-rendered secrets can be cleaned up after successful migration.

### Q: Do I need to change AWS Secrets Manager?

**A:** Generally no, ESO can read the same secrets that AVP used. However, you may need to adjust the secret structure if using complex paths.

### Q: How do I handle secret rotation?

**A:** ESO automatically refreshes secrets based on `refreshInterval`. Set this appropriately (e.g., `1h`, `15m`). When secrets change in AWS Secrets Manager, ESO will update the Kubernetes secret.

### Q: What if my Helm chart expects inline values, not secret references?

**A:** Use ESO's template feature to create secrets in the exact format your application expects:

```yaml
spec:
  target:
    template:
      engineVersion: v2
      data:
        config.yaml: |
          apiKey: {{ .apikey }}
          password: {{ .password }}
```

### Q: Can I test ESO before removing AVP?

**A:** Yes! Create test ExternalSecrets alongside AVP-managed secrets. Verify ESO-created secrets work correctly before switching your applications over.

### Q: How do I monitor ESO?

**A:**
```bash
# Check ExternalSecret status
kubectl get externalsecrets --all-namespaces

# Check for errors
kubectl describe externalsecret <name> -n <namespace>

# View ESO logs
kubectl logs -n external-secrets -l app.kubernetes.io/name=external-secrets
```

### Q: What about multi-region deployments?

**A:** Configure SecretStores per region or use ClusterSecretStore with appropriate AWS region configuration:

```yaml
spec:
  provider:
    aws:
      region: eu-west-1  # Specify region
```

---

## Additional Resources

- [External Secrets Operator Documentation](https://external-secrets.io/)
- [AWS Secrets Manager Integration](https://external-secrets.io/latest/provider/aws-secrets-manager/)
- [ArgoCD Security Best Practices](https://argo-cd.readthedocs.io/en/stable/operator-manual/security/)
- [This Solution's Architecture](./README.md)

---

**Last Updated:** 2025-12-29
**Status:** Active Migration Guide
