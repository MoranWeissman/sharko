# Secrets Provider

The Secrets Provider is **where Sharko gets cluster credentials** — the kubeconfig or token it needs to reach each registered cluster.

You configure it in **Settings → Secrets Provider** (Type / Region / Prefix). Supported today:

- **AWS Secrets Manager** (`aws-sm`)
- **Kubernetes Secrets** (`k8s-secrets`)
- **ArgoCD** (`argocd`) — auto mode, reads from the ArgoCD cluster Secrets already in your cluster

**Azure Key Vault and GCP Secret Manager are not yet supported** for cluster credentials. They work fine for addon secrets (license keys, DB passwords), but not for the cluster kubeconfigs themselves.

## One source, three jobs

Those fetched credentials feed three different things:

1. **Test and reach the cluster directly.**  
   When you click **Test connection** or **Diagnose connection**, Sharko connects to the cluster on its own — independent of ArgoCD — to run permission checks and confirm it can create Kubernetes Secrets there.

2. **Write the ArgoCD cluster Secret.**  
   Once a cluster is registered, Sharko hands the connection to ArgoCD by creating the ArgoCD cluster Secret (the same Secret you'd get from `argocd cluster add`). That's how ArgoCD gets access to deploy to the cluster.

3. **Deploy addon secrets.**  
   If an addon needs a Kubernetes Secret — a Datadog API key, a license file, a database password — Sharko fetches that secret value and deploys it onto the target cluster for you.

**The flow:**

```
Register a cluster
  ↓
Sharko fetches credentials from the Secrets Provider
  ↓
Tests the cluster directly (Test connection / Diagnose)
  ↓
Writes the ArgoCD cluster Secret
  ↓
ArgoCD deploys addons to the cluster
  ↓
Addon secrets get deployed as needed
```

One credential source feeds all of it.

## Important caveat: "argocd" is cluster-credentials-only

If you choose **`argocd`** as the Secrets Provider, that covers reaching clusters and writing ArgoCD cluster Secrets, but it **does NOT provide addon secret values**.

For addon secrets you need a separate secret backend — AWS Secrets Manager, Kubernetes Secrets, GCP Secret Manager, Azure Key Vault, or Vault.

This is by design: the ArgoCD provider reads from ArgoCD's own cluster Secrets to get cluster credentials, but those Secrets don't contain addon-specific values like Datadog API keys or license files. You need a dedicated secret store for those.

**What you'll see if you try:**  
If you select `argocd` as the Secrets Provider and then try to deploy an addon that references a secret, Sharko will return this error:

> argocd provider is cluster-credentials-only; configure a separate SecretProvider (aws-sm, k8s-secrets, gcp-sm, azure-kv, or vault) for addon secret values.

**How to fix it:**  
Configure a second Secrets Provider for addon secrets. The two can coexist — one for cluster credentials (e.g., `argocd`), another for addon secret values (e.g., `aws-sm`).
