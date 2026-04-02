# ArgoCD IAM Role Trust Policy Changes

## 2026-03-29 — Removed stale OIDC provider, added new addons cluster

### Role
`arn:aws:iam::123456789012:role/ArgoCD`

### Removed OIDC Providers (2)
Both OIDC providers are deleted from IAM and have no matching EKS cluster. Removed to make room for the new addons cluster (trust policy has 2048 char limit).

#### 1. `EXAMPLE_OIDC_STALE_1` (eu-west-1)
- **IAM Provider Status:** DELETED
- **Matching EKS Cluster:** None (searched eu-west-1, eu-central-1, us-east-1)
- **Trust Statement Removed:**
```json
{
  "Effect": "Allow",
  "Principal": {
    "Federated": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE_OIDC_STALE_1"
  },
  "Action": "sts:AssumeRoleWithWebIdentity",
  "Condition": {
    "StringLike": {
      "oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE_OIDC_STALE_1:sub": "system:serviceaccount:argocd:*"
    }
  }
}
```

#### 2. `EXAMPLE_OIDC_STALE_2` (eu-west-1)
- **IAM Provider Status:** DELETED
- **Matching EKS Cluster:** None (searched eu-west-1, eu-central-1, us-east-1)
- **Trust Statement Removed:**
```json
{
  "Effect": "Allow",
  "Principal": {
    "Federated": "arn:aws:iam::123456789012:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE_OIDC_STALE_2"
  },
  "Action": "sts:AssumeRoleWithWebIdentity",
  "Condition": {
    "StringLike": {
      "oidc.eks.eu-west-1.amazonaws.com/id/EXAMPLE_OIDC_STALE_2:sub": "system:serviceaccount:argocd:*"
    }
  }
}
```

### Added OIDC Provider
- **OIDC ID:** `EXAMPLE_OIDC_ID_1`
- **Region:** eu-west-1
- **Cluster:** `your-argocd-cluster`
- **Scoped to:** `system:serviceaccount:argocd:argocd-application-controller`

### Other Stale Entry (not removed, for future cleanup)
- `EXAMPLE_OIDC_ORPHANED` (eu-central-1) — IAM provider EXISTS (since 2021-05-10) but no matching EKS cluster found. Orphaned.
