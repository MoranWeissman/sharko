# Quick Start

**Sharko is a GitOps agent with an API: your portal or pipeline asks for "a cluster with these addons," and Sharko opens a pull request — it never changes your cluster behind your back.**

Registering a cluster is an API call that tells ArgoCD about a new cluster and its addons. Get Sharko running on your cluster in about 5 minutes.

## Prerequisites

- Kubernetes 1.27+ with ArgoCD installed
- Helm 3.x

## 1. Install Sharko

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace
```

## 2. Get the Admin Password

```bash
kubectl get secret sharko-initial-admin-secret -n sharko \
  -o jsonpath='{.data.password}' | base64 -d
```

Sharko stores the generated admin password in a dedicated `sharko-initial-admin-secret` Secret — the same pattern ArgoCD uses with `argocd-initial-admin-secret`.

## 3. Open the UI

```bash
kubectl port-forward svc/sharko 8080:80 -n sharko
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` and the password from step 2.

## 4. Complete the First-Run Wizard

The wizard appears automatically on first access.

**Step 1 — Welcome:** Overview of what the wizard will configure.

**Step 2 — Git connection:** Enter your addons repo URL and a personal access token with read/write access.

**Step 3 — ArgoCD connection:** Sharko auto-discovers the ArgoCD service in-cluster. Confirm the server URL and enter an ArgoCD account token. Optionally configure a secrets provider (AWS Secrets Manager or Kubernetes Secrets) for cluster credentials.

**Step 4 — Initialize repository:** Sharko opens a pull request with the starting layout — `managed-clusters.yaml`, `catalog.yaml`, `sharko-engine.yaml`, and the empty `cluster-addons/` and `values/` folders, nothing else. Choose **auto-merge** to merge the PR immediately, or review it yourself in GitHub/Azure DevOps first.

**Step 5 — Catalog:** Your Catalog starts **empty** — nothing runs in your org that nobody chose. This step offers two doors into it: pick addons from the Marketplace (common picks like cert-manager are highlighted, but nothing is pre-selected — you choose), or add your own in-house chart by filling in its location, version, and namespace. Pick as many as you like; Sharko opens **one** pull request for all of them, not one per addon. You can skip this step and add addons later — an empty Catalog is a normal, working state, not an error.

After the wizard completes, the dashboard loads with clusters discovered from ArgoCD.

## 5. Start Managing Clusters

From the dashboard, you will see:

- **Managed clusters** — clusters registered and managed by Sharko
- A hint line for **discovered clusters** — existing ArgoCD clusters not yet managed by Sharko, with a count

Click the hint to open **Register New Cluster** pre-set to adopt one of them.

## Next Steps

- [Add addons to the catalog](../user-guide/addons.md)
- [Configure ingress for production access](installation.md#access-the-ui)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
- [Set up API keys for CI/CD](../user-guide/connections.md#api-keys)
