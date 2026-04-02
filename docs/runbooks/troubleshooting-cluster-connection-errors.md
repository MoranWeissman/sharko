# Troubleshooting: ArgoCD Cluster Connection Timeout Errors

## Problem Description

ArgoCD applications were failing to sync to remote EKS clusters with the following error:

```
ComparisonError: Failed to load live state: failed to get cluster info for "https://[EKS-API-URL]":
error synchronizing cache state : failed to get server version:
Get "https://[EKS-API-URL]/version?timeout=32s":
getting credentials: exec: executable argocd-k8s-auth failed with exit code 20
(Client.Timeout exceeded while awaiting headers)
```

**Affected Applications:**
- `istiod-example-target-cluster`
- Any application targeting remote EKS clusters

**Symptoms:**
- Applications show `ComparisonError` status
- Cluster connection fails in ArgoCD UI
- Timeout errors when ArgoCD tries to authenticate

---

## Root Cause Analysis

### Initial Investigation

The error message `argocd-k8s-auth failed with exit code 20 (Client.Timeout exceeded)` initially appeared to be a network timeout, but deeper investigation revealed it was an **IAM authentication failure**.

### True Root Cause

The `argocd-application-controller` service account was **missing the IAM role annotation**, preventing it from obtaining AWS credentials needed to authenticate to remote EKS clusters.

### Why This Happened

1. **Missing IRSA Configuration**: The `application-controller` service account didn't have the `eks.amazonaws.com/role-arn` annotation
2. **No AWS Credentials**: Without IRSA, pods couldn't get AWS credentials to call STS
3. **Cross-Account Authentication Failure**: `argocd-k8s-auth` couldn't assume the cross-account IAM role
4. **Timeout**: The actual error was `AccessDenied: Not authorized to perform sts:AssumeRoleWithWebIdentity` (found in pod logs)

---

## Authentication Architecture

### How ArgoCD Authenticates to EKS Clusters

```
┌─────────────────────────────────────────────────────────────────────┐
│ Management Account (123456789012) - ArgoCD Control Plane           │
│                                                                      │
│  ┌────────────────────────────────────────────┐                    │
│  │ ArgoCD Application Controller Pod          │                    │
│  │ ├─ Service Account: argocd-application-... │                    │
│  │ ├─ IRSA Annotation: eks.amazonaws.com/...  │ ← Step 1: Get AWS │
│  │ └─ IAM Role: ArgoCD-Cluster-Addons         │   credentials     │
│  └────────────────┬───────────────────────────┘                    │
│                   │                                                 │
│                   │ Step 2: Has sts:AssumeRole permission          │
└───────────────────┼─────────────────────────────────────────────────┘
                    │
                    │ Step 3: Calls STS AssumeRole
                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│ Remote Account (123456789012) - Target EKS Cluster                 │
│                                                                      │
│  ┌────────────────────────────────────────────┐                    │
│  │ EKS-Admin-Assume-Role             │                    │
│  │ ├─ Trust Policy: Allows ArgoCD role        │ ← Step 4: Trust   │
│  │ └─ Permissions: EKS cluster access         │                    │
│  └────────────────┬───────────────────────────┘                    │
│                   │                                                 │
│                   │ Step 5: Returns temporary credentials          │
│                   ▼                                                 │
│  ┌────────────────────────────────────────────┐                    │
│  │ EKS Cluster: Remote Cluster                │                    │
│  │ ├─ aws-auth ConfigMap: Maps IAM role       │ ← Step 6: Accept  │
│  │ └─ API Server validates token              │   connection      │
│  └────────────────────────────────────────────┘                    │
└─────────────────────────────────────────────────────────────────────┘
```

### Cluster Secret Configuration

Each cluster secret contains an `execProviderConfig` that tells ArgoCD how to authenticate:

```yaml
config: |
  {
    "execProviderConfig": {
      "command": "argocd-k8s-auth",
      "args": [
        "aws",
        "--cluster-name", "cluster-name",
        "--role-arn", "arn:aws:iam::ACCOUNT-ID:role/EKS-Admin-Assume-Role"
      ],
      "env": {
        "AWS_REGION": "eu-west-1"
      }
    },
    "tlsClientConfig": {
      "insecure": false,
      "caData": "..."
    }
  }
```

**Key Point**: `argocd-k8s-auth` requires AWS credentials to call STS, which come from IRSA (IAM Roles for Service Accounts).

---

## Solution Steps

### Step 1: Create Dedicated IAM Role

We created a new IAM role specifically for cluster addons management to avoid trust policy size limits.

**Role Name**: `ArgoCD-Cluster-Addons`

**Trust Policy** (`/tmp/argocd-cluster-addons-trust-policy.json`):
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
- **Attached Policy**: `getSecretValuePolicy` (for Secrets Manager access)
- **Inline Policy**: `AssumeRole` (for cross-account access)
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
- **Inline Policy**: `CodeConnectionsRead` (for GitHub connections)

**Creation Commands**:
```bash
# Create role with trust policy
aws iam create-role \
  --role-name ArgoCD-Cluster-Addons \
  --assume-role-policy-document file:///tmp/argocd-cluster-addons-trust-policy.json \
  --description "IAM role for ArgoCD application-controller to manage cluster addons" \
  --profile your-profile

# Attach managed policy
aws iam attach-role-policy \
  --role-name ArgoCD-Cluster-Addons \
  --policy-arn arn:aws:iam::123456789012:policy/getSecretValuePolicy \
  --profile your-profile

# Add inline policies
aws iam put-role-policy \
  --role-name ArgoCD-Cluster-Addons \
  --policy-name AssumeRole \
  --policy-document file:///tmp/argocd-assume-role-policy.json \
  --profile your-profile

aws iam put-role-policy \
  --role-name ArgoCD-Cluster-Addons \
  --policy-name CodeConnectionsRead \
  --policy-document file:///tmp/argocd-codeconnections-policy.json \
  --profile your-profile
```

### Step 2: Update Application-Controller Service Account

**File**: `/path/to/ArgoFleet/fleet-configuration/argocd-values/your-argocd-cluster.yaml`

**Change Made** (around line 165):
```yaml
controller:
  # === IAM Role for Cross-Account Cluster Access ===
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons

  # === High Availability via StatefulSet Sharding ===
  replicas: 2
  # ... rest of config
```

**Commit and Push**:
```bash
cd /path/to/ArgoFleet
git add fleet-configuration/argocd-values/your-argocd-cluster.yaml
git commit -m "Add IAM role to application-controller for cluster addons management"
git push
```

### Step 3: Update Cross-Account Trust Policy

**Role**: `EKS-Admin-Assume-Role` (in management account 123456789012)

**Updated Trust Policy**:
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
        "AWS": "arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons"
      },
      "Action": "sts:AssumeRole"
    }
    // ... other statements
  ]
}
```

**Note**: We also removed an orphaned invalid principal (`EXAMPLE_PRINCIPAL_ID`) that was causing validation errors.

### Step 4: Restart Application-Controller Pods

After updating the service account annotation, pods must be restarted to pick up the new IRSA credentials:

```bash
# Restart the StatefulSet
kubectl rollout restart statefulset argocd-your-cluster-application-controller -n argocd

# Wait for rollout to complete
kubectl rollout status statefulset argocd-your-cluster-application-controller -n argocd

# Verify AWS credentials are injected
kubectl get pod argocd-your-cluster-application-controller-0 -n argocd \
  -o jsonpath='{.spec.containers[0].env[?(@.name=="AWS_ROLE_ARN")].value}'
# Expected: arn:aws:iam::123456789012:role/ArgoCD-Cluster-Addons

kubectl get pod argocd-your-cluster-application-controller-0 -n argocd \
  -o jsonpath='{.spec.containers[0].env[?(@.name=="AWS_WEB_IDENTITY_TOKEN_FILE")].value}'
# Expected: /var/run/secrets/eks.amazonaws.com/serviceaccount/token
```

---

## Verification

### Check Application Status

```bash
# Check sync status
kubectl get application istiod-example-target-cluster -n argocd \
  -o jsonpath='{.status.sync.status}'
# Expected: Synced or OutOfSync (no ComparisonError)

# Check health status
kubectl get application istiod-example-target-cluster -n argocd \
  -o jsonpath='{.status.health.status}'
# Expected: Healthy

# Check for errors
kubectl get application istiod-example-target-cluster -n argocd \
  -o jsonpath='{.status.conditions[?(@.type=="ComparisonError")].message}'
# Expected: Empty (no errors)
```

### Check Application-Controller Logs

```bash
# Check for successful cluster connections
kubectl logs argocd-your-cluster-application-controller-0 -n argocd --tail=100 | \
  grep -i "example-target-cluster"

# Look for successful reconciliation
# Expected logs:
# ✓ "Reconciliation completed"
# ✓ "Update successful"
# ✓ No timeout or authentication errors
```

### Check Cluster Connection in ArgoCD UI

1. Go to **Settings** → **Clusters**
2. Find `example-target-cluster`
3. Status should show **Connected** (green)
4. No error messages

---

## Key Learnings

### 1. ArgoCD Component Roles

| Component | Purpose | Needs IAM Role For |
|-----------|---------|-------------------|
| **argocd-server** | API server and Web UI | AWS Secrets Manager, S3, SSO (optional) |
| **argocd-application-controller** | Reconciliation engine | **Connecting to remote EKS clusters** (required) |
| **argocd-repo-server** | Manifest generation | Repository credentials (optional) |
| **external-secrets** | Fetching secrets | AWS Secrets Manager (required) |

**Key Point**: The `application-controller` is the component that actually connects to remote clusters and performs sync operations.

### 2. Why Connection Details Alone Aren't Enough

Having the cluster's:
- API server URL
- CA certificate
- Region

Only tells ArgoCD **WHERE** to connect, not **WHO** is connecting (authentication credentials).

**Authentication requires**:
1. AWS credentials (from IRSA)
2. Permission to assume cross-account IAM role (from IAM policy)
3. Trust relationship allowing the assumption (from target account trust policy)

### 3. IRSA (IAM Roles for Service Accounts) Requirements

When you add `eks.amazonaws.com/role-arn` to a service account:
- **Existing pods DO NOT automatically get the credentials**
- Pods must be **restarted** for the mutating webhook to inject:
  - `AWS_ROLE_ARN` environment variable
  - `AWS_WEB_IDENTITY_TOKEN_FILE` environment variable
  - Web identity token volume mount

### 4. Cross-Account Trust Chain

For ArgoCD in account A to manage clusters in account B:

**Account A** (Management):
- IAM role with `sts:AssumeRole` permission
- OIDC provider trust policy for EKS service accounts

**Account B** (Target):
- IAM role with EKS cluster access permissions
- Trust policy allowing Account A's IAM role to assume it
- `aws-auth` ConfigMap mapping the role to Kubernetes RBAC

### 5. IAM Trust Policy Size Limits

AWS has a **2,048 byte limit** on trust policies. If you need to support many clusters:
- Create **dedicated IAM roles** per purpose (e.g., `ArgoCD-Cluster-Addons`)
- Clean up **orphaned OIDC providers** from deleted clusters
- Use **wildcard service accounts** carefully (`system:serviceaccount:argocd:*`)

---

## Troubleshooting Commands

### Debug Cluster Authentication Issues

```bash
# 1. Check if service account has IAM role annotation
kubectl get sa argocd-application-controller -n argocd -o yaml | grep -i role-arn

# 2. Check if pods have AWS environment variables
kubectl get pod <pod-name> -n argocd \
  -o jsonpath='{.spec.containers[0].env[?(@.name=="AWS_ROLE_ARN")].value}'

# 3. Check application-controller logs for auth errors
kubectl logs <pod-name> -n argocd --tail=200 | \
  grep -i "AccessDenied\|timeout\|credentials\|assumerolewithwebidentity"

# 4. Test AWS credentials inside pod
kubectl exec -it <pod-name> -n argocd -- \
  aws sts get-caller-identity

# 5. Get OIDC provider ID for current cluster
aws eks describe-cluster \
  --name <cluster-name> \
  --region <region> \
  --profile <profile> \
  --query 'cluster.identity.oidc.issuer' \
  --output text

# 6. List all cluster secrets and their server URLs
kubectl get secrets -n argocd \
  -l argocd.argoproj.io/secret-type=cluster \
  -o json | jq -r '.items[] | "\(.metadata.name): \(.data.server | @base64d)"'

# 7. Check cluster secret configuration
kubectl get secret <cluster-name> -n argocd \
  -o jsonpath='{.data.config}' | base64 -d | jq .
```

### Find IAM Roles Trusting Specific OIDC Provider

```bash
OIDC_ID="YOUR-OIDC-ID"

aws iam list-roles --profile <profile> --query 'Roles[].RoleName' --output json | \
  jq -r '.[]' | while read role; do
    policy=$(aws iam get-role --role-name "$role" --profile <profile> \
      --query 'Role.AssumeRolePolicyDocument' --output json 2>/dev/null)
    if echo "$policy" | grep -q "$OIDC_ID"; then
      echo "✓ Found: $role"
    fi
  done
```

---

## Prevention / Best Practices

### 1. Always Configure IRSA for Application-Controller

When deploying ArgoCD, ensure the `application-controller` has an IAM role annotation:

```yaml
controller:
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::<account-id>:role/<role-name>
```

### 2. Document Cross-Account Trust Relationships

Maintain a mapping of:
- Which ArgoCD clusters manage which target clusters
- IAM role names and ARNs
- Trust relationships between accounts

### 3. Monitor IAM Trust Policy Size

If you manage many clusters:
- Periodically clean up orphaned OIDC providers
- Consider using multiple IAM roles for different purposes
- Monitor trust policy size before adding new clusters

### 4. Test New Cluster Connections

After adding a new cluster:
```bash
# 1. Verify cluster secret was created
kubectl get secret <cluster-name> -n argocd

# 2. Check for connection errors in logs
kubectl logs -l app.kubernetes.io/name=argocd-application-controller \
  -n argocd --tail=100 | grep <cluster-name>

# 3. Verify in ArgoCD UI (Settings → Clusters)
```

### 5. Use Naming Conventions

Standardize IAM role names across accounts:
- Management account: `ArgoCD-<Purpose>`
- Target accounts: `<Project>-EKS-Admin-Assume-Role`

---

## Related Documentation

- [Cluster Registration Design](../DESIGN.md#cluster-registration-with-external-secrets-operator)
- [Values Configuration Guide](../VALUES_GUIDE.md)
- [Day 1 Workflow](../diagrams/2-day1-complete-workflow.drawio)
- [AWS IAM Roles for Service Accounts](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
- [ArgoCD Cluster Management](https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/#clusters)

---

## Summary

**Problem**: ArgoCD couldn't connect to remote EKS clusters (timeout errors)
**Root Cause**: Missing IAM role annotation on `argocd-application-controller` service account
**Solution**: Created dedicated IAM role with IRSA, updated service account, configured cross-account trust
**Result**: Successful cluster connections and application syncing

**Key Takeaway**: The application-controller needs AWS credentials (via IRSA) to execute `argocd-k8s-auth` for cross-account EKS cluster authentication.
