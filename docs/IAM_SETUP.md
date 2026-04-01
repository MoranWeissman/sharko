# IAM Setup for External Secrets Operator

This document describes how to configure IAM roles for External Secrets Operator (ESO) to access AWS Secrets Manager.

## Overview

ESO uses **IRSA (IAM Roles for Service Accounts)** to authenticate with AWS Secrets Manager. This requires:
1. An EKS OIDC provider
2. An IAM role with Secrets Manager permissions
3. A trust relationship between the OIDC provider and the IAM role

## Prerequisites

- EKS cluster with OIDC provider enabled
- AWS CLI installed and configured
- Appropriate IAM permissions to modify roles

## IAM Role Configuration

### Role Details

**Role Name:** `EKS-devops-argocd-addons-secret-manager`
**Role ARN:** `arn:aws:iam::627176949220:role/EKS-devops-argocd-addons-secret-manager`
**Account:** 627176949220 (DevOps account)
**Region:** eu-west-1

### Service Account

The ESO service account that assumes this role:
- **Namespace:** `external-secrets`
- **Service Account:** `external-secrets`
- **Annotation:** `eks.amazonaws.com/role-arn: arn:aws:iam::627176949220:role/EKS-devops-argocd-addons-secret-manager`

## Adding a New Cluster

When deploying this solution to a new EKS cluster, you need to add the cluster's OIDC provider to the IAM role trust policy.

### Step 1: Get the OIDC Provider ID

```bash
# Get cluster OIDC provider
aws eks describe-cluster \
  --name YOUR_CLUSTER_NAME \
  --region eu-west-1 \
  --profile devops-switch \
  --query 'cluster.identity.oidc.issuer' \
  --output text

# Example output: https://oidc.eks.eu-west-1.amazonaws.com/id/95A350C4A098114287BB76A160415A4A
# Extract the ID: 95A350C4A098114287BB76A160415A4A
```

### Step 2: Get Current Trust Policy

```bash
aws iam get-role \
  --role-name EKS-devops-argocd-addons-secret-manager \
  --profile devops-switch \
  --query 'Role.AssumeRolePolicyDocument' \
  --output json > current-trust-policy.json
```

### Step 3: Add New OIDC Provider to Trust Policy

Add a new statement to the trust policy JSON:

```json
{
    "Effect": "Allow",
    "Principal": {
        "Federated": "arn:aws:iam::627176949220:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/YOUR_OIDC_ID"
    },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
        "StringEquals": {
            "oidc.eks.eu-west-1.amazonaws.com/id/YOUR_OIDC_ID:sub": "system:serviceaccount:external-secrets:external-secrets",
            "oidc.eks.eu-west-1.amazonaws.com/id/YOUR_OIDC_ID:aud": "sts.amazonaws.com"
        }
    }
}
```

**Important Fields:**
- `Federated`: OIDC provider ARN (replace `YOUR_OIDC_ID`)
- `sub`: Service account in format `system:serviceaccount:NAMESPACE:SERVICE_ACCOUNT_NAME`
- `aud`: Should be `sts.amazonaws.com` for AssumeRoleWithWebIdentity

### Step 4: Update the Trust Policy

```bash
aws iam update-assume-role-policy \
  --role-name EKS-devops-argocd-addons-secret-manager \
  --policy-document file://updated-trust-policy.json \
  --profile devops-switch
```

### Step 5: Verify the Update

```bash
aws iam get-role \
  --role-name EKS-devops-argocd-addons-secret-manager \
  --profile devops-switch \
  --query 'Role.AssumeRolePolicyDocument' \
  --output json
```

## Example: Current Trust Policy

The role currently trusts these OIDC providers:

| OIDC ID | Region | Cluster |
|---------|--------|---------|
| `35AEED386B175FD014B759341E41636C` | eu-west-1 | Previous cluster |
| `7A5DA82A88A2DCD8CED6D79D77216026` | eu-west-1 | Previous cluster |
| `5FD847A0086F2E41F75D3330AFBF9A1E` | eu-central-1 | Previous cluster |
| `95A350C4A098114287BB76A160415A4A` | eu-west-1 | **Current ArgoCD cluster** |

## Complete Trust Policy Example

```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "Federated": "arn:aws:iam::627176949220:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/95A350C4A098114287BB76A160415A4A"
            },
            "Action": "sts:AssumeRoleWithWebIdentity",
            "Condition": {
                "StringEquals": {
                    "oidc.eks.eu-west-1.amazonaws.com/id/95A350C4A098114287BB76A160415A4A:sub": "system:serviceaccount:external-secrets:external-secrets",
                    "oidc.eks.eu-west-1.amazonaws.com/id/95A350C4A098114287BB76A160415A4A:aud": "sts.amazonaws.com"
                }
            }
        }
    ]
}
```

## IAM Permissions Policy

The role should have a permissions policy that allows ESO to read secrets from AWS Secrets Manager:

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
                "arn:aws:secretsmanager:*:*:secret:datadog-api-keys-integration*",
                "arn:aws:secretsmanager:*:*:secret:k8s-*",
                "arn:aws:secretsmanager:*:*:secret:argocd/*"
            ]
        }
    ]
}
```

**Note:** The wildcard patterns should be adjusted based on your secret naming conventions.

## Troubleshooting

### ESO Cannot Assume Role

**Symptom:** ExternalSecret shows error "cannot assume role"

**Checks:**
1. Verify OIDC provider exists in IAM:
   ```bash
   aws iam list-open-id-connect-providers --profile devops-switch
   ```

2. Verify service account has the annotation:
   ```bash
   kubectl get sa external-secrets -n external-secrets -o yaml
   ```

3. Verify the trust relationship includes your cluster's OIDC ID

4. Check ESO pod logs:
   ```bash
   kubectl logs -n external-secrets deployment/external-secrets-operator
   ```

### OIDC Provider Not Found

**Symptom:** Error about OIDC provider not existing

**Solution:** Ensure the OIDC provider is registered in IAM:
```bash
# Get OIDC issuer URL
OIDC_ISSUER=$(aws eks describe-cluster \
  --name YOUR_CLUSTER \
  --region eu-west-1 \
  --profile devops-switch \
  --query 'cluster.identity.oidc.issuer' \
  --output text)

# Create OIDC provider if it doesn't exist
# (This is usually done automatically by eksctl or Terraform)
aws iam create-open-id-connect-provider \
  --url $OIDC_ISSUER \
  --client-id-list sts.amazonaws.com \
  --profile devops-switch
```

## Security Best Practices

1. **Principle of Least Privilege**: Only grant access to secrets that ESO actually needs
2. **Namespace Isolation**: Use different service accounts for different namespaces if needed
3. **Audit**: Enable CloudTrail logging for Secrets Manager access
4. **Rotation**: Regularly rotate secrets in Secrets Manager
5. **Condition Keys**: Use additional condition keys (like `sts:ExternalId`) for enhanced security

## References

- [EKS IAM Roles for Service Accounts](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html)
- [External Secrets Operator AWS Provider](https://external-secrets.io/latest/provider/aws-secrets-manager/)
- [IAM AssumeRoleWithWebIdentity](https://docs.aws.amazon.com/STS/latest/APIReference/API_AssumeRoleWithWebIdentity.html)
