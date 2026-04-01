# Bootstrap Guide

This guide explains how to bootstrap the ArgoCD cluster addons solution from scratch.

## Table of Contents
- [Prerequisites](#prerequisites)
- [Standard Bootstrap (Self-Managed ArgoCD)](#standard-bootstrap-self-managed-argocd)
- [AWS-Managed ArgoCD Bootstrap (EKS Auto Mode)](#aws-managed-argocd-bootstrap-eks-auto-mode)
- [Verification](#verification)
- [Troubleshooting](#troubleshooting)

---

## Prerequisites

### Required Tools
- `kubectl` configured for your ArgoCD cluster
- `helm` v3.x installed
- `argocd` CLI (optional, for verification)

### ArgoCD Requirements
- ArgoCD installed and running
- Version 2.10+ (for `ignoreMissingValueFiles` feature)
- ApplicationSet controller enabled

### AWS Requirements
- AWS CLI configured
- IAM role for External Secrets Operator (ESO)
- AWS Secrets Manager access

### Repository Access
- Git repository cloned locally
- **GitHub PAT stored in AWS Secrets Manager** (required for private repo access)

#### GitHub Credentials Secret

**CRITICAL:** Before bootstrapping, create the following secret in AWS Secrets Manager:

**Secret Details:**
- **AWS Account:** 627176949220 (DevOps account)
- **Secret Name:** `argocd/devops-argocd-addons-dev-eks`
- **Region:** `eu-west-1` (or your configured region)

**Secret Structure:**
```json
{
  "github_user": "YOUR_GITHUB_USERNAME",
  "github_token": "ghp_YOUR_PERSONAL_ACCESS_TOKEN"
}
```

**Create Secret via AWS CLI:**
```bash
aws secretsmanager create-secret \
  --name argocd/devops-argocd-addons-dev-eks \
  --description "GitHub credentials for ArgoCD to access argocd-cluster-addons repo" \
  --secret-string '{"github_user":"YOUR_USERNAME","github_token":"ghp_YOUR_TOKEN"}' \
  --region eu-west-1 \
  --profile devops-account
```

**PAT Requirements:**
- Scope: `repo` (Full control of private repositories)
- Expiration: Set according to security policy
- Organization: `merck-ahtl`

**How It Works:**
1. Bootstrap deploys ESO with ClusterSecretStore
2. ESO fetches GitHub credentials from Secrets Manager
3. Creates ArgoCD repository secret automatically
4. ArgoCD uses credentials for all repo operations

---

## Important: ArgoCD Management

**This solution does NOT manage ArgoCD itself.**

ArgoCD installation and configuration are managed by the [ArgoFleet solution](https://github.com/YOUR_ORG/ArgoFleet). This includes:
- ArgoCD installation and upgrades
- ArgoCD configuration (ConfigMaps, RBAC, etc.)
- Plugin configuration (if any)
- IAM roles and service accounts
- Ingress and networking

This cluster-addons solution **only** manages:
- Cluster registration in ArgoCD (via cluster secrets)
- Addon deployments via ApplicationSets
- External Secrets Operator for addon secrets

**Before bootstrapping this solution**, ensure ArgoCD is already installed and configured by ArgoFleet.

---

## Standard Bootstrap (Self-Managed ArgoCD)

Use this method when ArgoCD is self-managed (installed via Helm, manifests, etc.)

### Step 1: Configure ESO IAM Role

Create an IAM role for ESO with trust policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::ACCOUNT_ID:oidc-provider/oidc.eks.REGION.amazonaws.com/id/OIDC_ID"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.REGION.amazonaws.com/id/OIDC_ID:sub": "system:serviceaccount:external-secrets:external-secrets"
        }
      }
    }
  ]
}
```

Attach policy for Secrets Manager access:

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
        "arn:aws:secretsmanager:*:*:secret:k8s-*",
        "arn:aws:secretsmanager:*:*:secret:datadog-*"
      ]
    }
  ]
}
```

### Step 2: Update ESO Configuration

In `configuration/addons-catalog.yaml`, update the ESO configuration:

```yaml
applicationsets:
  - appName: external-secrets
    repoURL: https://charts.external-secrets.io
    chart: external-secrets
    version: 0.19.2
    # Optional: Configure IAM role directly in catalog
    # valuesObject:
    #   serviceAccount:
    #     annotations:
    #       eks.amazonaws.com/role-arn: "arn:aws:iam::YOUR_ACCOUNT_ID:role/YOUR_ESO_ROLE"
```

Or configure the IAM role in `configuration/global-values.yaml`:

```yaml
external-secrets:
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: "arn:aws:iam::YOUR_ACCOUNT_ID:role/YOUR_ESO_ROLE"
```

### Step 3: Configure Bootstrap Settings

Update the repository URL and target revision in `configuration/bootstrap-config.yaml`:

```bash
# Navigate to repository root
cd argocd-cluster-addons

# Edit configuration/bootstrap-config.yaml and update:
# - repoURL: https://github.com/YOUR_ORG/argocd-cluster-addons.git
# - targetRevision: HEAD (tracks default branch) or specific branch/tag/commit
#
# Note: All bootstrap components (ESO, clusters, ApplicationSets) will use
# the same targetRevision for consistency. This prevents version mismatches.
```

**Important:** The `targetRevision` setting is used consistently across:
- Root application (`bootstrap/root-app.yaml`)
- ESO bootstrap (`bootstrap/templates/eso.yaml`)
- Cluster registration (`bootstrap/templates/clusters.yaml`)
- ApplicationSets (`bootstrap/templates/applicationset.yaml`)

This ensures all components fetch from the same Git revision.

### Step 4: Deploy Root Application

```bash
# Apply the root application (NOT self-managing)
kubectl apply -f bootstrap/root-app.yaml -n argocd

# The root app will automatically deploy the bootstrap chart which includes:
# - ApplicationSets for dynamic addon management
# - External Secrets Operator
# - Remote cluster registration
```

### Step 5: Monitor Deployment

```bash
# Watch applications
kubectl get applications -n argocd -w

# Expected order:
# 1. external-secrets-argocd-control-plane (syncs first)
# 2. clusters (syncs after ESO is ready)
# 3. Addon ApplicationSets (istio-*, etc.)
```

---

## AWS-Managed ArgoCD Bootstrap (EKS Auto Mode)

⚠️ **Special handling required for EKS Auto Mode ArgoCD!**

### The Problem

AWS-managed ArgoCD in EKS Auto Mode has limitations:
1. Cannot easily deploy to `https://kubernetes.default.svc` (in-cluster)
2. Self-cluster must be registered as a cluster secret FIRST
3. ESO needs this cluster secret to exist before it can work

### Solution: Two-Stage Bootstrap

#### Stage 1: Manual Self-Cluster Registration

Create the self-cluster secret manually:

```yaml
# Save as: self-cluster-secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: argocd-control-plane
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: cluster
    env: argocd-control-plane
    external-secrets: enabled
type: Opaque
stringData:
  name: argocd-control-plane
  server: https://kubernetes.default.svc
  config: |
    {
      "tlsClientConfig": {
        "insecure": false
      }
    }
```

Apply it:

```bash
kubectl apply -f self-cluster-secret.yaml -n argocd
```

**Verify:**
```bash
kubectl get secret argocd-control-plane -n argocd
argocd cluster list  # Should show in-cluster
```

#### Stage 2: Deploy Bootstrap

Now follow the standard bootstrap steps from Step 2 onwards.

**Key Difference:** ESO will now deploy successfully because:
1. Self-cluster secret exists
2. ESO ApplicationSet can target it
3. ESO installs to ArgoCD's own cluster

---

## Verification

### Check ESO Installation

```bash
# Check ESO is deployed
kubectl get pods -n external-secrets

# Expected output:
# NAME                                                READY   STATUS
# external-secrets-*                                  1/1     Running
# external-secrets-cert-controller-*                  1/1     Running
# external-secrets-webhook-*                          1/1     Running
```

### Check ClusterSecretStore

```bash
# Check global secret store exists
kubectl get clustersecretstore

# Expected output:
# NAME                  AGE   STATUS
# global-secret-store   1m    Valid
```

### Check Cluster Registration

```bash
# Check remote cluster secrets are created
kubectl get externalsecrets -n argocd

# Expected output:
# NAME                           STORE                 REFRESH INTERVAL   STATUS
# devops-automation-dev-eks      global-secret-store   1h                 SecretSynced

# Check actual secrets
kubectl get secrets -n argocd | grep cluster

# Expected:
# devops-automation-dev-eks    Opaque    3      1m
```

### Check ApplicationSets

```bash
# Check ApplicationSets created
kubectl get applicationsets -n argocd

# Expected:
# istio-base-dev
# istiod-dev
# istio-cni-dev
# istio-ingress-dev
```

### Check Applications

```bash
# Check Applications generated by ApplicationSets
kubectl get applications -n argocd

# Expected:
# istio-base-devops-automation-dev-eks
# istiod-devops-automation-dev-eks
# istio-cni-devops-automation-dev-eks
# istio-ingress-devops-automation-dev-eks
```

---

## Troubleshooting

### ESO Not Starting

**Symptom:** ESO pods not running

**Check:**
```bash
kubectl describe pod -n external-secrets <pod-name>
```

**Common Issues:**
- IAM role not configured correctly
- Service account annotation missing
- IRSA (IAM Roles for Service Accounts) not set up

### Cluster Secrets Not Created

**Symptom:** ExternalSecrets show status `SecretSyncedError`

**Check:**
```bash
kubectl describe externalsecret -n argocd <secret-name>
```

**Common Issues:**
- AWS secret doesn't exist in Secrets Manager (should be named `k8s-<cluster-name>`)
- IAM role lacks permissions

### ApplicationSets Not Generating Applications

**Symptom:** No Applications created from ApplicationSets

**Check:**
```bash
kubectl describe applicationset -n argocd istio-base-dev
```

**Common Issues:**
- Cluster labels don't match selector
- Cluster secret not labeled correctly
- ApplicationSet syntax error

### Istio Applications Not Syncing

**Symptom:** Applications created but stuck in `OutOfSync`

**Check:**
```bash
kubectl describe application -n argocd istio-base-devops-automation-dev-eks
```

**Common Issues:**
- Values files missing (should use `ignoreMissingValueFiles`)
- Helm chart version doesn't exist
- Repository not accessible

---

## Post-Bootstrap

### What Happens After Bootstrap

1. ✅ ESO is installed and managing secrets
2. ✅ Cluster secrets are automatically created from AWS Secrets Manager
3. ✅ ApplicationSets are generating Applications
4. ✅ Addons are deploying to clusters

### Making Changes

All changes are now GitOps-driven:

```bash
# Example: Update Istio version
# Edit configuration/addons-catalog.yaml
# Change version: 1.28.0 → 1.29.0

git add configuration/addons-catalog.yaml
git commit -m "Update Istio to 1.29.0"
git push

# ArgoCD automatically:
# 1. Detects change
# 2. Updates ApplicationSet
# 3. Istio applications sync to new version
```

### Adding a New Cluster

```bash
# 1. Create AWS secret in Secrets Manager
# 2. Add cluster to configuration/cluster-addons.yaml
# 3. Create cluster values file: configuration/addons-clusters-values/<cluster-name>.yaml
# 4. Commit and push

# ESO automatically:
# - Creates cluster secret
# - ArgoCD registers cluster
# - ApplicationSets deploy addons based on labels
```

---

## Next Steps

- Read [MIGRATION_AVP_TO_ESO.md](MIGRATION_AVP_TO_ESO.md) for migrating from ArgoCD Vault Plugin
- Read [README.md](README.md) for detailed architecture explanation
- Configure cluster and addon-specific values in `configuration/addons-clusters-values/<cluster-name>.yaml`
