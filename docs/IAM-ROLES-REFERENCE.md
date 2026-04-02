# IAM Roles Reference

This document describes all IAM roles used in the ArgoCD cluster addons solution and their purposes.

## Overview

The solution uses **IAM Roles for Service Accounts (IRSA)** to provide AWS credentials to Kubernetes pods. This follows the principle of least privilege and enables secure cross-account access.

---

## Management Account (123456789012)

### 1. ArgoCD-Cluster-Addons

**Purpose**: Primary IAM role for `argocd-application-controller` to manage cluster addons across EKS clusters

**Service Account**: `argocd-application-controller` (namespace: `argocd`)

**ARN**: `arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons`

**Trust Policy**:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE_OIDC_ID_1"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE_OIDC_ID_1:aud": "sts.amazonaws.com",
          "oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE_OIDC_ID_1:sub": "system:serviceaccount:argocd:argocd-application-controller"
        }
      }
    }
  ]
}
```

**Permissions**:
- **Managed Policy**: `getSecretValuePolicy` - Access to AWS Secrets Manager
- **Inline Policy**: `AssumeRole` - Cross-account role assumption
  ```json
  {
    "Version": "2012-10-17",
    "Statement": [
      {
        "Sid": "AssumeRole",
        "Effect": "Allow",
        "Action": "sts:AssumeRole",
        "Resource": "*"
      }
    ]
  }
  ```
- **Inline Policy**: `CodeConnectionsRead` - GitHub integration
  ```json
  {
    "Version": "2012-10-17",
    "Statement": [
      {
        "Effect": "Allow",
        "Action": [
          "codeconnections:GetConnectionToken",
          "codeconnections:GetConnection",
          "codeconnections:UseConnection"
        ],
        "Resource": "arn:aws:codeconnections:eu-west-1:123456789012:connection/example-connection-id"
      },
      {
        "Effect": "Allow",
        "Action": [
          "gitpull:GetConnectionToken"
        ],
        "Resource": "*"
      }
    ]
  }
  ```

**Use Case**: The application-controller uses this role to:
1. Authenticate to AWS
2. Assume cross-account IAM roles in target accounts
3. Connect to remote EKS clusters
4. Sync applications and manage resources

---

### 2. ArgoCD (Legacy)

**Purpose**: Legacy IAM role for ArgoCD server and other components

**Service Account**: `argocd-server` (namespace: `argocd`)

**ARN**: `arn:aws:iam::123456789012:role/ArgoCD`

**Trust Policy**: Contains multiple OIDC providers for different ArgoCD clusters (some orphaned from deleted clusters)

**Permissions**: Same as `ArgoCD-Cluster-Addons`

**Status**: Still in use for `argocd-server` component. May be deprecated in favor of dedicated roles.

**Note**: This role reached the trust policy size limit (2,048 bytes), which is why we created `ArgoCD-Cluster-Addons`.

---

### 3. EKS-your-cluster-secret-manager

**Purpose**: IAM role for External Secrets Operator to fetch cluster credentials from AWS Secrets Manager

**Service Account**: `external-secrets` (namespace: `external-secrets`)

**ARN**: `arn:aws:iam::123456789012:role/EKS-your-cluster-secret-manager`

**Trust Policy**: Trusts multiple EKS OIDC providers (for different ArgoCD management clusters)

**Permissions**:
- **Managed Policy**: `getSecretValuePolicy` - Read access to Secrets Manager

**Use Case**: ESO uses this role to:
1. Fetch EKS cluster credentials from Secrets Manager (secrets prefixed with `k8s-`)
2. Create ArgoCD cluster secrets in the `argocd` namespace

---

### 4. argocd-addons-vault-plugin

**Purpose**: IAM role for ArgoCD Vault Plugin (AVP) integration

**Service Account**: `argocd-repo-server` (namespace: `argocd`)

**ARN**: `arn:aws:iam::123456789012:role/argocd-addons-vault-plugin`

**Trust Policy**: Specific to `argocd-repo-server` service account

**Permissions**:
- **Managed Policy**: `getSecretValuePolicy` - Read secrets for AVP

**Use Case**: Repo-server uses this role to fetch secrets when processing Helm charts with AVP annotations

---

### 5. EKS-Admin-Assume-Role

**Purpose**: Cross-account IAM role that grants administrative access to EKS clusters

**ARN**: `arn:aws:iam::123456789012:role/EKS-Admin-Assume-Role`

**Trust Policy**: Allows multiple principals to assume this role:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "eks.amazonaws.com",
        "AWS": "arn:aws:iam::123456789012:root"
      },
      "Action": "sts:AssumeRole"
    },
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::123456789012:role/ArgoCD"
      },
      "Action": "sts:AssumeRole"
    },
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons"
      },
      "Action": "sts:AssumeRole"
    },
    // ... other principals
  ]
}
```

**Permissions**: Full administrative access to EKS clusters (mapped in `aws-auth` ConfigMap)

**Use Case**: This is the role that `argocd-k8s-auth` assumes to get credentials for connecting to EKS clusters

---

## Remote Accounts (e.g., 123456789012)

### EKS-Admin-Assume-Role (in remote account)

**Purpose**: Same as management account version, but in remote accounts for cross-account access

**Trust Policy**: Must allow management account's ArgoCD IAM roles:
```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}
```

**Permissions**: EKS cluster administrative access

**EKS aws-auth Mapping**:
```yaml
mapRoles: |
  - rolearn: arn:aws:iam::123456789012:role/EKS-Admin-Assume-Role
    username: admin
    groups:
      - system:masters
```

---

## Authentication Flow

### Same-Account Cluster Access

```
ArgoCD Pod
  ├─ Service Account: argocd-application-controller
  ├─ IRSA: ArgoCD-Cluster-Addons
  │
  └─ Executes: argocd-k8s-auth
      ├─ Uses AWS credentials from IRSA
      ├─ Calls STS: AssumeRole(EKS-Admin-Assume-Role)
      └─ Returns: EKS cluster token
          └─ Connects to: EKS cluster API
```

### Cross-Account Cluster Access

```
Management Account (123456789012)
  │
  ├─ ArgoCD Pod
  │   ├─ Service Account: argocd-application-controller
  │   └─ IRSA: ArgoCD-Cluster-Addons
  │
  └─ Executes: argocd-k8s-auth
      ├─ Uses: AWS credentials from IRSA
      │
      ├─ Calls STS: AssumeRole(
      │     arn:aws:iam::123456789012:role/EKS-Admin-Assume-Role
      │   )
      │
      └─ Returns: Temporary credentials
          │
          └─ Remote Account (123456789012)
              └─ EKS Cluster validates token against aws-auth
```

---

## Configuration Files

### Application-Controller Service Account Annotation

**File**: `ArgoFleet/fleet-configuration/argocd-values/your-cluster.yaml`

```yaml
controller:
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons
```

### External Secrets Operator Service Account Annotation

**File**: `argocd-cluster-addons/configuration/bootstrap-config.yaml`

```yaml
bootstrap:
  eso:
    serviceaccount:
      name: external-secrets
      roleArn: "arn:aws:iam::123456789012:role/EKS-your-cluster-secret-manager"
```

### Cluster Secret Configuration

**File**: `argocd-cluster-addons/charts/clusters/templates/cluster-external-secret.yaml`

```yaml
config: |
  {
    "execProviderConfig": {
      "command": "argocd-k8s-auth",
      "args": [
        "aws",
        "--cluster-name", "{{"{{ .clusterName }}"}}",
        "--role-arn", "arn:aws:iam::{{"{{ .accountId }}"}}:role/EKS-Admin-Assume-Role"
      ],
      "env": {
        "AWS_REGION": "{{"{{ .region }}"}}"
      }
    }
  }
```

---

## OIDC Provider Mapping

Current EKS clusters and their OIDC provider IDs:

| Cluster Name | OIDC Provider ID | Region | Status |
|-------------|-----------------|--------|--------|
| your-argocd-cluster | `EXAMPLE_OIDC_ID_2` | eu-west-1 | Active |
| your-argocd-cluster | `EXAMPLE_OIDC_ID_1` | eu-west-1 | Active |
| argocd-cluster-prod | `EXAMPLE_OIDC_ID_3` | eu-west-1 | Active |
| example-target-cluster | `EXAMPLE_OIDC_ID_5` | eu-west-1 | Active |

**Note**: Only ArgoCD management clusters need to be in IAM role trust policies. Target clusters (like example-target-cluster) don't need OIDC provider entries because ArgoCD connects TO them, not FROM them.

---

## Maintenance Tasks

### Adding a New ArgoCD Management Cluster

1. **Get OIDC provider ID**:
   ```bash
   aws eks describe-cluster \
     --name <cluster-name> \
     --region <region> \
     --query 'cluster.identity.oidc.issuer' \
     --output text
   ```

2. **Update IAM role trust policy**:
   Add new statement to `ArgoCD-Cluster-Addons` trust policy

3. **Update service account annotation**:
   Add IAM role ARN to ArgoCD values file for the new cluster

4. **Restart pods**:
   ```bash
   kubectl rollout restart statefulset argocd-application-controller -n argocd
   ```

### Adding Cross-Account Access

1. **In target account**, update `EKS-Admin-Assume-Role` trust policy:
   ```json
   {
     "Effect": "Allow",
     "Principal": {
       "AWS": "arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons"
     },
     "Action": "sts:AssumeRole"
   }
   ```

2. **Add cluster to cluster-addons.yaml**:
   ```yaml
   clusters:
     - name: cluster-name
       labels:
         addon-name: enabled
   ```

3. **Verify connection**:
   Check ArgoCD UI → Settings → Clusters

### Cleaning Up Orphaned OIDC Providers

1. **List all OIDC providers in trust policy**

2. **Check which clusters exist**:
   ```bash
   aws eks list-clusters --region eu-west-1
   ```

3. **Match OIDC IDs to clusters**:
   ```bash
   aws eks describe-cluster \
     --name <cluster-name> \
     --region <region> \
     --query 'cluster.identity.oidc.issuer'
   ```

4. **Remove orphaned entries** from trust policy

---

## Security Considerations

### Principle of Least Privilege

- Each IAM role has only the permissions needed for its specific purpose
- Service account conditions in trust policies ensure only specific pods can assume roles
- Cross-account roles are explicitly listed (no wildcard trust)

### Credential Management

- No long-lived AWS credentials stored in Kubernetes secrets
- All credentials are temporary (via IRSA and STS AssumeRole)
- Credentials automatically rotate
- Web identity tokens have short expiration times

### Audit Trail

- All IAM role assumptions are logged in AWS CloudTrail
- Kubernetes RBAC provides audit trail for in-cluster actions
- ArgoCD maintains application sync history

### Trust Policy Size Limits

- AWS has a 2,048 byte limit on trust policies
- Monitor size when adding new OIDC providers
- Create dedicated roles when approaching the limit
- Clean up orphaned OIDC providers regularly

---

## Troubleshooting

### Check Service Account Annotations

```bash
kubectl get sa <service-account-name> -n argocd -o yaml | grep -A 2 annotations
```

### Check Pod Environment Variables

```bash
kubectl get pod <pod-name> -n argocd \
  -o jsonpath='{.spec.containers[0].env[?(@.name=="AWS_ROLE_ARN")].value}'
```

### Test Role Assumption

```bash
# From inside the pod
kubectl exec -it <pod-name> -n argocd -- aws sts get-caller-identity
```

### Verify Cross-Account Trust

```bash
# Get trust policy
aws iam get-role --role-name EKS-Admin-Assume-Role \
  --query 'Role.AssumeRolePolicyDocument'

# Test assumption
aws sts assume-role \
  --role-arn arn:aws:iam::ACCOUNT-ID:role/EKS-Admin-Assume-Role \
  --role-session-name test
```

---

## Related Documentation

- [Troubleshooting Cluster Connection Errors](./runbooks/troubleshooting-cluster-connection-errors.md)
- [Cluster Registration Design](./DESIGN.md#cluster-registration-with-external-secrets-operator)
- [AWS IAM Roles for Service Accounts](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
- [ArgoCD Security Best Practices](https://argo-cd.readthedocs.io/en/stable/operator-manual/security/)
