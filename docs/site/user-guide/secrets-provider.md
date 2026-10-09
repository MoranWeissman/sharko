# Secrets Provider

Sharko uses secrets providers for two distinct concerns:

1. **How Sharko reaches your clusters** — the cluster-credentials provider (argocd / aws-sm / k8s-secrets)
2. **Where addon secret values come from** — a separate addon-secret backend (aws-sm / k8s-secrets; gcp/azure planned but not yet supported)

The split exists because cluster credentials and addon secret values have different lifecycles and sources. This page explains both.

---

## Cluster Credentials Provider

The **cluster-credentials provider** is where Sharko gets the kubeconfig or token it needs to reach each registered cluster.

You configure it in **Settings → Secrets Provider** (Type / Region / Prefix). Supported today:

- **AWS Secrets Manager** (`aws-sm`)
- **Kubernetes Secrets** (`k8s-secrets`)
- **ArgoCD** (`argocd`) — auto mode, reads from the ArgoCD cluster Secrets already in your cluster

**Azure Key Vault and GCP Secret Manager are planned but not yet supported** for cluster credentials.

### What the cluster-credentials provider does

Those fetched credentials feed two different jobs:

1. **Test and reach the cluster directly.**  
   When you click **Test connection** or **Diagnose connection**, Sharko connects to the cluster on its own — independent of ArgoCD — to run permission checks and confirm it can create Kubernetes Secrets there.

2. **Write the ArgoCD cluster Secret.**  
   Once a cluster is registered, Sharko hands the connection to ArgoCD by creating the ArgoCD cluster Secret (the same Secret you'd get from `argocd cluster add`). That's how ArgoCD gets access to deploy to the cluster.

These are the two cluster-credentials jobs.

---

## Addon Secret Values Provider

The **addon-secret-values provider** is a separate concern. This is where the secret VALUES that addons need — API keys, tokens, license files, database passwords — are read from and pushed to the target cluster.

Supported today for addon secret values:

- **AWS Secrets Manager** (`aws-sm`)
- **Kubernetes Secrets** (`k8s-secrets`)
- **GCP Secret Manager** and **Azure Key Vault** — planned but not yet supported

### Important: "argocd" is cluster-credentials-ONLY

If you choose **`argocd`** as the cluster-credentials provider, it covers reaching clusters and writing ArgoCD cluster Secrets, but it **does NOT provide addon secret values**.

This is by design and enforced in code. The ArgoCD provider reads from ArgoCD's own cluster Secrets to get cluster credentials, but those Secrets don't contain addon-specific values like Datadog API keys or license files. You need a dedicated secret store for those.

**What you'll see if you try:**  
If you select `argocd` as the Secrets Provider and then try to deploy an addon that references a secret, Sharko will return this error:

> argocd provider is cluster-credentials-only; configure a separate SecretProvider (aws-sm, k8s-secrets, gcp-sm, azure-kv, or vault) for addon secret values.

**How to fix it:**  
Configure a second Secrets Provider for addon secrets. The two can coexist — one for cluster credentials (e.g., `argocd`), another for addon secret values (e.g., `aws-sm`).

---

## The Flow (reconciled view)

Here's how the two concerns fit together:

```
Register a cluster
  ↓
Sharko fetches cluster credentials from the cluster-credentials provider
  ↓
Tests the cluster directly (Test connection / Diagnose)
  ↓
Writes the ArgoCD cluster Secret
  ↓
ArgoCD deploys addons to the cluster
  ↓
Addon secret values (if any) are fetched from the addon-secret-values provider
  and deployed to the target cluster
```

The first two jobs are the cluster-credentials provider's. The last job — deploying addon secret values — is the separate addon-secret backend's, which is why "argocd" can't do it.

