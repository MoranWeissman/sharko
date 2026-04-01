# ArgoCD IAM Role Trust Policy Changes

## 2026-03-29 — Removed stale OIDC provider, added new addons cluster

### Role
`arn:aws:iam::627176949220:role/ArgoCD`

### Removed OIDC Providers (2)
Both OIDC providers are deleted from IAM and have no matching EKS cluster. Removed to make room for the new addons cluster (trust policy has 2048 char limit).

#### 1. `95C485AFAD1F609C88DD459494CFABE9` (eu-west-1)
- **IAM Provider Status:** DELETED
- **Matching EKS Cluster:** None (searched eu-west-1, eu-central-1, us-east-1)
- **Trust Statement Removed:**
```json
{
  "Effect": "Allow",
  "Principal": {
    "Federated": "arn:aws:iam::627176949220:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/95C485AFAD1F609C88DD459494CFABE9"
  },
  "Action": "sts:AssumeRoleWithWebIdentity",
  "Condition": {
    "StringLike": {
      "oidc.eks.eu-west-1.amazonaws.com/id/95C485AFAD1F609C88DD459494CFABE9:sub": "system:serviceaccount:argocd:*"
    }
  }
}
```

#### 2. `E22E836378488A50B79BEDEB84AE5529` (eu-west-1)
- **IAM Provider Status:** DELETED
- **Matching EKS Cluster:** None (searched eu-west-1, eu-central-1, us-east-1)
- **Trust Statement Removed:**
```json
{
  "Effect": "Allow",
  "Principal": {
    "Federated": "arn:aws:iam::627176949220:oidc-provider/oidc.eks.eu-west-1.amazonaws.com/id/E22E836378488A50B79BEDEB84AE5529"
  },
  "Action": "sts:AssumeRoleWithWebIdentity",
  "Condition": {
    "StringLike": {
      "oidc.eks.eu-west-1.amazonaws.com/id/E22E836378488A50B79BEDEB84AE5529:sub": "system:serviceaccount:argocd:*"
    }
  }
}
```

### Added OIDC Provider
- **OIDC ID:** `95A350C4A098114287BB76A160415A4A`
- **Region:** eu-west-1
- **Cluster:** `devops-argocd-addons-dev-eks`
- **Scoped to:** `system:serviceaccount:argocd:argocd-application-controller`

### Other Stale Entry (not removed, for future cleanup)
- `059AE9551488F59276BB612E7D0DF379` (eu-central-1) — IAM provider EXISTS (since 2021-05-10) but no matching EKS cluster found. Orphaned.
