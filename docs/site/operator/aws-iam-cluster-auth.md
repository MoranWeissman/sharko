# AWS IAM Cluster Authentication

## What this page is for

This guide covers AWS-managed EKS clusters (or any Kubernetes cluster using AWS IAM authentication) registered with Sharko. If you registered such a cluster and clicked **Test cluster** in the UI, you saw a clear error pointing here:

> *"This cluster uses AWS IAM authentication. Configure AWS credentials for the Sharko pod's role to enable Test."*

That error means the ArgoCD cluster Secret Sharko wrote during `register-cluster` has the `awsAuthConfig` shape — Sharko cannot run Test against it until the cloud-creds plumbing for IAM token minting ships.

## Status

**v1.x does not ship the cloud-creds plumbing for IAM token minting.** The runbook for "how to make this work end-to-end" is incomplete and tracked for v2. This page is a stub so the in-app error link does not 404, and so operators understand the v1.x limitation up-front.

## What's needed (when v2 lands)

The end-to-end IAM-auth Test path requires three pieces that v1.x does not have:

1. **IAM role for the Sharko pod's K8s ServiceAccount.** Use IRSA (IAM Roles for Service Accounts) — annotate the `sharko` ServiceAccount with the role ARN, configure an OIDC trust relationship between the EKS cluster hosting Sharko and the IAM role.
2. **`eks:GetToken` (and `sts:AssumeRole` if cross-account) permissions on that role.** The role needs to be able to mint EKS tokens for the target cluster's API server. For cross-account targets (the ArgoCD cluster Secret's `awsAuthConfig.roleARN` references a role in a different AWS account), the Sharko pod's role also needs `sts:AssumeRole` permission on the cross-account role.
3. **Sharko learns to call `aws-iam-authenticator` or the AWS SDK to mint a token before the Test cluster API call.** This is the v2 implementation work — it does not exist in v1.x.

Once all three are in place, the Test cluster 12-step flow runs end-to-end against IAM-auth EKS clusters identically to the self-hosted bearer-token happy path.

## What you can do today (v1.x workaround)

If you need to verify connectivity to an IAM-auth EKS cluster while running v1.x, do it manually outside Sharko:

```bash
# Verify your kubeconfig works
kubectl --kubeconfig=<your-eks-kubeconfig> get nodes

# Verify the ArgoCD cluster Secret Sharko wrote
kubectl --namespace=argocd get secret cluster-<cluster-name> -o yaml
```

The `register-cluster` flow itself works for IAM-auth EKS clusters in v1.x — Sharko writes the ArgoCD cluster Secret with the `awsAuthConfig` shape, ArgoCD itself can use the Secret to deploy addons via ApplicationSets. The only gap is Sharko's own connectivity-verification (Test) feature.

## Tracking

The IAM token-minting work is part of the V2.0.0 production-launch epic. See [`docs/design/2026-05-13-cluster-connectivity-test-redesign.md`](https://github.com/MoranWeissman/sharko/blob/main/docs/design/2026-05-13-cluster-connectivity-test-redesign.md) §3 (out-of-scope items) and the architectural roadmap for context.
