# IAM Setup Guide for EKS

This guide covers IAM configuration for running Sharko on EKS and connecting to target EKS clusters. It assumes you have `kubectl`, `aws`, and `eksctl` available.

---

## 1. Sharko Pod IRSA Setup

Sharko uses [IAM Roles for Service Accounts (IRSA)](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) to authenticate with AWS services from inside EKS.

### 1.1 Enable OIDC Provider

If your Sharko cluster doesn't have an OIDC provider yet:

```bash
eksctl utils associate-iam-oidc-provider \
  --cluster sharko-cluster \
  --region us-east-1 \
  --approve
```

Verify:

```bash
aws eks describe-cluster --name sharko-cluster --region us-east-1 \
  --query "cluster.identity.oidc.issuer" --output text
```

### 1.2 IAM Policy for Sharko

Create a policy that grants Sharko access to Secrets Manager and EKS discovery.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SecretsManagerReadOnly",
      "Effect": "Allow",
      "Action": [
        "secretsmanager:GetSecretValue",
        "secretsmanager:DescribeSecret",
        "secretsmanager:ListSecrets"
      ],
      "Resource": "arn:aws:secretsmanager:*:123456789012:secret:sharko/*"
    },
    {
      "Sid": "EKSDiscovery",
      "Effect": "Allow",
      "Action": [
        "eks:ListClusters",
        "eks:DescribeCluster"
      ],
      "Resource": "*"
    }
  ]
}
```

Save as `sharko-policy.json` and create:

```bash
aws iam create-policy \
  --policy-name SharkoPolicy \
  --policy-document file://sharko-policy.json
```

### 1.3 IAM Role with Trust Policy

The trust policy allows the Sharko pod's service account to assume this role via OIDC.

Replace `OIDC_PROVIDER_ID` with the ID from your OIDC issuer URL (the part after `https://oidc.eks.<region>.amazonaws.com/id/`).

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/OIDC_PROVIDER_ID"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.us-east-1.amazonaws.com/id/OIDC_PROVIDER_ID:sub": "system:serviceaccount:sharko:sharko",
          "oidc.eks.us-east-1.amazonaws.com/id/OIDC_PROVIDER_ID:aud": "sts.amazonaws.com"
        }
      }
    }
  ]
}
```

Create the role and attach the policy:

```bash
aws iam create-role \
  --role-name SharkoRole \
  --assume-role-policy-document file://trust-policy.json

aws iam attach-role-policy \
  --role-name SharkoRole \
  --policy-arn arn:aws:iam::123456789012:policy/SharkoPolicy
```

### 1.4 Annotate the ServiceAccount

Add the IRSA annotation to the Sharko service account so pods receive AWS credentials automatically.

If using Helm, set in your `values.yaml`:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/SharkoRole
```

Or patch manually:

```bash
kubectl annotate serviceaccount sharko -n sharko \
  eks.amazonaws.com/role-arn=arn:aws:iam::123456789012:role/SharkoRole
```

Restart the Sharko pod to pick up the new credentials:

```bash
kubectl rollout restart deployment sharko -n sharko
```

Verify the pod has credentials:

```bash
kubectl exec -n sharko deploy/sharko -- \
  aws sts get-caller-identity
```

Expected output should show `SharkoRole` as the assumed role.

---

## 2. Target Cluster Access (Same Account)

When the target EKS cluster is in the same AWS account as Sharko, grant access via the `aws-auth` ConfigMap.

### 2.1 Patch aws-auth ConfigMap

Add Sharko's IAM role to the target cluster's `aws-auth` ConfigMap so it can authenticate as a Kubernetes identity.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: aws-auth
  namespace: kube-system
data:
  mapRoles: |
    - rolearn: arn:aws:iam::123456789012:role/SharkoRole
      username: sharko
      groups:
        - sharko-managers
```

Apply with:

```bash
kubectl apply -f aws-auth-patch.yaml --context <target-cluster-context>
```

Or use `eksctl`:

```bash
eksctl create iamidentitymapping \
  --cluster target-cluster \
  --region us-east-1 \
  --arn arn:aws:iam::123456789012:role/SharkoRole \
  --username sharko \
  --group sharko-managers
```

### 2.2 Kubernetes RBAC

Create a Role and RoleBinding in the target cluster that grants Sharko the minimum permissions it needs: namespace creation and secret management in the test namespace.

```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: sharko-manager
rules:
  # Namespace creation for sharko-test
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list", "create"]
  # Secret CRUD in sharko-test namespace (used for connectivity verification)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list", "create", "update", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: sharko-manager-binding
subjects:
  - kind: Group
    name: sharko-managers
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: sharko-manager
  apiGroup: rbac.authorization.k8s.io
```

Apply to the target cluster:

```bash
kubectl apply -f sharko-rbac.yaml --context <target-cluster-context>
```

### 2.3 Verify Access

From the Sharko pod, test that it can reach the target cluster:

```bash
# From your workstation, assuming Sharko's role
aws eks update-kubeconfig --name target-cluster --region us-east-1 \
  --role-arn arn:aws:iam::123456789012:role/SharkoRole

kubectl auth can-i create namespaces --as sharko
kubectl auth can-i create secrets -n sharko-test --as sharko
```

Or use the Sharko UI/CLI to run a cluster connectivity test after registration.

---

## 3. Target Cluster Access (Cross-Account)

When the target cluster is in a different AWS account, Sharko must assume a role in the target account first.

### 3.1 Create IAM Role in Target Account

In the target account (`987654321098`), create a role that trusts Sharko's account:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::123456789012:role/SharkoRole"
      },
      "Action": "sts:AssumeRole",
      "Condition": {
        "StringEquals": {
          "sts:ExternalId": "sharko-cross-account"
        }
      }
    }
  ]
}
```

```bash
# Run in the target account (987654321098)
aws iam create-role \
  --role-name SharkoTargetRole \
  --assume-role-policy-document file://cross-account-trust.json

aws iam attach-role-policy \
  --role-name SharkoTargetRole \
  --policy-arn arn:aws:iam::987654321098:policy/EKSReadOnly
```

The `SharkoTargetRole` needs at minimum `eks:DescribeCluster` on the target cluster.

### 3.2 Grant AssumeRole to Sharko's Policy

Add a statement to Sharko's IAM policy (in account `123456789012`) allowing it to assume the target role:

```json
{
  "Sid": "CrossAccountAssumeRole",
  "Effect": "Allow",
  "Action": "sts:AssumeRole",
  "Resource": "arn:aws:iam::987654321098:role/SharkoTargetRole"
}
```

Update the policy:

```bash
aws iam create-policy-version \
  --policy-arn arn:aws:iam::123456789012:policy/SharkoPolicy \
  --policy-document file://sharko-policy-v2.json \
  --set-as-default
```

### 3.3 Patch aws-auth in Target Cluster

In the target account's EKS cluster, add the target role to `aws-auth`:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: aws-auth
  namespace: kube-system
data:
  mapRoles: |
    - rolearn: arn:aws:iam::987654321098:role/SharkoTargetRole
      username: sharko
      groups:
        - sharko-managers
```

Then create the same RBAC resources from [Section 2.2](#22-kubernetes-rbac) in the target cluster.

### 3.4 Verify Cross-Account Access

```bash
# Assume the target role
aws sts assume-role \
  --role-arn arn:aws:iam::987654321098:role/SharkoTargetRole \
  --role-session-name sharko-test \
  --external-id sharko-cross-account

# Use the returned credentials
export AWS_ACCESS_KEY_ID=<from-output>
export AWS_SECRET_ACCESS_KEY=<from-output>
export AWS_SESSION_TOKEN=<from-output>

aws eks update-kubeconfig --name target-cluster --region us-east-1
kubectl get namespaces
```

---

## 4. Discovery Mode (Multi-Account Scan)

Discovery mode lets Sharko scan multiple AWS accounts for EKS clusters. This requires a central role that can assume roles across all target accounts.

### 4.1 Central Discovery Role

Extend Sharko's IAM policy with `sts:AssumeRole` for all target accounts:

```json
{
  "Sid": "DiscoveryAssumeRole",
  "Effect": "Allow",
  "Action": "sts:AssumeRole",
  "Resource": [
    "arn:aws:iam::987654321098:role/SharkoTargetRole",
    "arn:aws:iam::111222333444:role/SharkoTargetRole",
    "arn:aws:iam::555666777888:role/SharkoTargetRole"
  ]
}
```

For organizations with many accounts, use a wildcard pattern (requires consistent role naming):

```json
{
  "Sid": "DiscoveryAssumeRoleOrg",
  "Effect": "Allow",
  "Action": "sts:AssumeRole",
  "Resource": "arn:aws:iam::*:role/SharkoTargetRole"
}
```

Each target account must have a `SharkoTargetRole` with a trust policy as shown in [Section 3.1](#31-create-iam-role-in-target-account).

### 4.2 Organizational SCP Considerations

If your AWS Organization uses Service Control Policies (SCPs), verify that:

1. **`sts:AssumeRole` is not denied** at the OU or account level for cross-account calls.
2. **`eks:ListClusters` and `eks:DescribeCluster` are allowed** in target accounts. Some orgs restrict EKS API access.
3. **Session duration limits** in SCPs don't conflict with Sharko's role session (default 1 hour).

Common SCP issues:

```json
{
  "Sid": "DenyExternalAssumeRole",
  "Effect": "Deny",
  "Action": "sts:AssumeRole",
  "Condition": {
    "StringNotEquals": {
      "aws:PrincipalOrgID": "o-your-org-id"
    }
  }
}
```

If this SCP exists, Sharko's account must be in the same organization as the target accounts, or you must add an exception.

### 4.3 Testing Before First Scan

Before running discovery mode, verify the chain works for each target account:

```bash
# 1. Verify Sharko's identity
aws sts get-caller-identity

# 2. Test assume-role into each target account
aws sts assume-role \
  --role-arn arn:aws:iam::987654321098:role/SharkoTargetRole \
  --role-session-name sharko-discovery-test \
  --external-id sharko-cross-account

# 3. With assumed credentials, verify EKS access
aws eks list-clusters --region us-east-1
aws eks describe-cluster --name <cluster-name> --region us-east-1
```

Run this for each target account. If any step fails, fix it before enabling discovery mode in Sharko.

---

## 5. Troubleshooting

Sharko uses structured error codes during cluster connectivity verification. Each code maps to a specific category of failure.

### Error Code Reference

#### ERR_NETWORK

**Symptom:** Sharko cannot reach the target cluster API server.

**Fixes:**
- Verify VPC peering or Transit Gateway between Sharko's VPC and the target cluster's VPC.
- Check security group rules — the target cluster's API server security group must allow inbound HTTPS (port 443) from Sharko's VPC CIDR or security group.
- If the target cluster has a private endpoint, ensure DNS resolution works from Sharko's VPC.

```bash
# Test network connectivity from Sharko pod
kubectl exec -n sharko deploy/sharko -- \
  curl -sk https://<cluster-api-endpoint>/healthz
```

#### ERR_TLS

**Symptom:** TLS handshake fails when connecting to the target cluster.

**Fixes:**
- Verify the CA bundle configured for the cluster matches the target cluster's certificate authority.
- Check if a corporate proxy is intercepting TLS and injecting its own certificate.
- Ensure the cluster endpoint hostname matches the certificate's SAN.

```bash
# Check the certificate chain
openssl s_client -connect <cluster-api-endpoint>:443 -showcerts </dev/null 2>/dev/null | \
  openssl x509 -noout -subject -issuer
```

#### ERR_AUTH

**Symptom:** Authentication rejected — 401 Unauthorized from the Kubernetes API server.

**Fixes:**
- Verify the token Sharko is using hasn't expired. EKS tokens are valid for 15 minutes.
- Confirm the `aws-auth` ConfigMap in the target cluster includes Sharko's IAM role (see [Section 2.1](#21-patch-aws-auth-configmap)).
- Check that Sharko's IRSA is working:

```bash
kubectl exec -n sharko deploy/sharko -- aws sts get-caller-identity
```

The output should show Sharko's IAM role, not the node's instance profile.

#### ERR_RBAC

**Symptom:** Authenticated but forbidden — 403 Forbidden on specific API calls.

**Fixes:**
- The Kubernetes RBAC resources from [Section 2.2](#22-kubernetes-rbac) may not be applied to the target cluster.
- Verify the group name in `aws-auth` matches the RoleBinding subject:

```bash
# On the target cluster
kubectl get clusterrolebinding sharko-manager-binding -o yaml
kubectl auth can-i create namespaces --as system:serviceaccount:sharko:sharko
```

- If using namespaced Role instead of ClusterRole, ensure the namespace exists.

#### ERR_AWS_STS

**Symptom:** Sharko cannot get AWS credentials via IRSA.

**Fixes:**
- Verify the OIDC provider is associated with the cluster:

```bash
aws eks describe-cluster --name sharko-cluster --region us-east-1 \
  --query "cluster.identity.oidc.issuer"
```

- Confirm the service account annotation exists:

```bash
kubectl get sa sharko -n sharko -o yaml | grep eks.amazonaws.com/role-arn
```

- Check the trust policy on the IAM role matches the OIDC provider ID and service account name exactly.
- Verify the pod has the projected token volume:

```bash
kubectl get pod -n sharko -l app=sharko -o jsonpath='{.items[0].spec.volumes}' | jq .
```

#### ERR_AWS_ASSUME

**Symptom:** `sts:AssumeRole` call fails when trying to access a cross-account cluster.

**Fixes:**
- Verify the trust policy in the target account trusts Sharko's role ARN (see [Section 3.1](#31-create-iam-role-in-target-account)).
- Check that Sharko's IAM policy includes `sts:AssumeRole` for the target role (see [Section 3.2](#32-grant-assumerole-to-sharkos-policy)).
- If using `ExternalId`, confirm it matches on both sides.
- Test manually:

```bash
aws sts assume-role \
  --role-arn arn:aws:iam::987654321098:role/SharkoTargetRole \
  --role-session-name debug-test \
  --external-id sharko-cross-account
```

#### ERR_QUOTA

**Symptom:** API throttling or quota exceeded errors from AWS or Kubernetes.

**Fixes:**
- Check AWS API throttling. If Sharko is scanning many clusters, requests may be rate-limited.
- Use `aws cloudwatch get-metric-statistics` to check for `ThrottledRequests` on STS/EKS.
- Consider adding retry logic or reducing the number of concurrent discovery scans.
- For Kubernetes API throttling, check the target cluster's API server metrics.

#### ERR_NAMESPACE

**Symptom:** Sharko cannot create the test namespace on the target cluster.

**Fixes:**
- Check if an admission webhook is blocking namespace creation:

```bash
kubectl get validatingwebhookconfigurations --context <target-cluster>
kubectl get mutatingwebhookconfigurations --context <target-cluster>
```

- Verify the namespace doesn't already exist with a conflicting state (e.g., Terminating).
- Check if a policy engine (OPA/Gatekeeper, Kyverno) restricts namespace creation.

#### ERR_TIMEOUT

**Symptom:** Connection established but requests time out.

**Fixes:**
- Check network latency and firewall rules between Sharko and the target cluster.
- Verify security group rules allow return traffic.
- Check if the target cluster's API server is under heavy load.
- If using VPC peering across regions, check for DNS resolution and routing issues.

```bash
# Test latency from Sharko pod
kubectl exec -n sharko deploy/sharko -- \
  curl -sk -o /dev/null -w "time_total: %{time_total}s\n" \
  https://<cluster-api-endpoint>/healthz
```

### Common Misconfigurations

| Symptom | Likely Cause | Fix |
|---|---|---|
| `could not get token` on pod startup | IRSA not configured | Check OIDC provider + SA annotation |
| `AccessDenied` on `sts:AssumeRole` | Trust policy doesn't trust Sharko's role | Update trust policy in target account |
| 401 from target cluster | IAM role not in `aws-auth` | Patch `aws-auth` ConfigMap |
| 403 on namespace create | RBAC missing or wrong group name | Apply RBAC, verify group in `aws-auth` |
| Timeout connecting to private cluster | No VPC peering / wrong route table | Set up peering, add routes |
| `x509: certificate signed by unknown authority` | Wrong CA bundle or proxy interception | Update CA data or add proxy CA |

### Verification Commands

Run these from the Sharko pod or a workstation with the same IAM role:

```bash
# Check who Sharko thinks it is
aws sts get-caller-identity

# Verify EKS access in Sharko's account
aws eks list-clusters --region us-east-1

# Test cross-account assume-role
aws sts assume-role \
  --role-arn arn:aws:iam::987654321098:role/SharkoTargetRole \
  --role-session-name verify-test \
  --external-id sharko-cross-account

# Verify Secrets Manager access
aws secretsmanager list-secrets --region us-east-1 \
  --filters Key=name,Values=sharko/
```

---

## 6. Terraform Modules (Reference)

These Terraform examples provide a starting point. A full `sharko-terraform` module repository is planned for the future.

### 6.1 Single-Account Setup

```hcl
# sharko-iam.tf — IAM resources for Sharko in a single-account setup

data "aws_eks_cluster" "sharko" {
  name = "sharko-cluster"
}

data "aws_iam_openid_connect_provider" "sharko" {
  url = data.aws_eks_cluster.sharko.identity[0].oidc[0].issuer
}

resource "aws_iam_role" "sharko" {
  name = "SharkoRole"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          Federated = data.aws_iam_openid_connect_provider.sharko.arn
        }
        Action = "sts:AssumeRoleWithWebIdentity"
        Condition = {
          StringEquals = {
            "${replace(data.aws_iam_openid_connect_provider.sharko.url, "https://", "")}:sub" = "system:serviceaccount:sharko:sharko"
            "${replace(data.aws_iam_openid_connect_provider.sharko.url, "https://", "")}:aud" = "sts.amazonaws.com"
          }
        }
      }
    ]
  })
}

resource "aws_iam_role_policy" "sharko" {
  name = "SharkoPolicy"
  role = aws_iam_role.sharko.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "SecretsManagerReadOnly"
        Effect = "Allow"
        Action = [
          "secretsmanager:GetSecretValue",
          "secretsmanager:DescribeSecret",
          "secretsmanager:ListSecrets",
        ]
        Resource = "arn:aws:secretsmanager:*:${data.aws_caller_identity.current.account_id}:secret:sharko/*"
      },
      {
        Sid    = "EKSDiscovery"
        Effect = "Allow"
        Action = [
          "eks:ListClusters",
          "eks:DescribeCluster",
        ]
        Resource = "*"
      },
    ]
  })
}

data "aws_caller_identity" "current" {}

output "sharko_role_arn" {
  value = aws_iam_role.sharko.arn
}
```

### 6.2 Cross-Account Setup

```hcl
# sharko-cross-account.tf — Target account role that trusts Sharko

variable "sharko_role_arn" {
  description = "ARN of the SharkoRole in the central account"
  type        = string
  default     = "arn:aws:iam::123456789012:role/SharkoRole"
}

variable "external_id" {
  description = "External ID for cross-account trust"
  type        = string
  default     = "sharko-cross-account"
}

resource "aws_iam_role" "sharko_target" {
  name = "SharkoTargetRole"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Principal = {
          AWS = var.sharko_role_arn
        }
        Action = "sts:AssumeRole"
        Condition = {
          StringEquals = {
            "sts:ExternalId" = var.external_id
          }
        }
      }
    ]
  })
}

resource "aws_iam_role_policy" "sharko_target" {
  name = "SharkoTargetPolicy"
  role = aws_iam_role.sharko_target.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "EKSAccess"
        Effect = "Allow"
        Action = [
          "eks:DescribeCluster",
          "eks:ListClusters",
        ]
        Resource = "*"
      },
    ]
  })
}

output "sharko_target_role_arn" {
  value = aws_iam_role.sharko_target.arn
}
```

### 6.3 Applying the RBAC with Terraform

```hcl
# sharko-rbac.tf — Kubernetes RBAC on the target cluster

resource "kubernetes_cluster_role" "sharko_manager" {
  metadata {
    name = "sharko-manager"
  }

  rule {
    api_groups = [""]
    resources  = ["namespaces"]
    verbs      = ["get", "list", "create"]
  }

  rule {
    api_groups = [""]
    resources  = ["secrets"]
    verbs      = ["get", "list", "create", "update", "delete"]
  }
}

resource "kubernetes_cluster_role_binding" "sharko_manager" {
  metadata {
    name = "sharko-manager-binding"
  }

  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "ClusterRole"
    name      = kubernetes_cluster_role.sharko_manager.metadata[0].name
  }

  subject {
    kind      = "Group"
    name      = "sharko-managers"
    api_group = "rbac.authorization.k8s.io"
  }
}
```

> **Note:** Full production-ready Terraform modules with variable validation, tagging, and multi-region support will be published in the `sharko-terraform` repository (PLANNED).
