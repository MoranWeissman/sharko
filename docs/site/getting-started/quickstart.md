# Quick Start

!!! warning "Sharko v4 is a technical preview, for evaluation and staging environments. It is not supported in production."
    Start with the
    [latest published v4 release](https://github.com/MoranWeissman/sharko/releases/latest)
    and read the [current limitations](../technical-preview.md) before deploying
    it more widely.

**Sharko is a GitOps agent with an API: your portal or pipeline asks for "a cluster with these addons," and Sharko opens a pull request — it never changes your cluster behind your back.**

Registering a cluster is an API call that tells ArgoCD about a new cluster and its addons. Get Sharko running on your cluster.

## Prerequisites

- Kubernetes with ArgoCD installed (tested against 1.31 in CI — see [Installation prerequisites](installation.md#prerequisites) for the full range)
- Helm 3.x

## 1. Install Sharko

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace
```

!!! warning "Install only published `v4.0.1`-or-later artifacts"
    Do not install any Sharko chart version below `v4.0.1` — all earlier
    release lines are retired and unsupported. There is no patch for the `v3`
    line.
    See [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md#why-v300-is-retired).

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

**Step 4 — Initialize repository:** Sharko opens a pull request with the starting layout — the empty `cluster-addons/` and `values/` folders, the engine pin `sharko-engine.yaml` (the one file that says which version of Sharko's engine chart runs), and a `README.md` explaining the layout. That's it: no `catalog.yaml` and no `managed-clusters.yaml` yet. Sharko writes each of those the first time it has something to put in them — `catalog.yaml` when you add your first addon, `managed-clusters.yaml` when you register your first cluster. Choose **auto-merge** to merge the PR immediately, or review it yourself in GitHub/Azure DevOps first.

**Step 5 — Catalog:** Your Catalog starts **empty** — nothing runs in your org that nobody chose. This step offers two doors into it: pick addons from the Marketplace (common picks like cert-manager are highlighted, but nothing is pre-selected — you choose), or add your own in-house chart by filling in its location, version, and namespace. Pick as many as you like; Sharko opens **one** pull request for all of them, not one per addon. You can skip this step and add addons later — an empty Catalog is a normal, working state, not an error.

After the wizard completes, the dashboard loads with clusters discovered from ArgoCD.

## 5. Register a Cluster

From the dashboard, you will see:

- **Managed clusters** — clusters registered and managed by Sharko
- A hint line for **discovered clusters** — existing ArgoCD clusters not yet managed by Sharko, with a count

The fastest first cluster is one **adopted** from that discovered list — a cluster ArgoCD already knows how to reach, so there is no credentials step to fill in. (Sharko's own credentials — a kubeconfig, a stored secret, or an EKS token — only matter later, for addons that push their own secrets to the cluster; a plain registration or adoption works without any of them.)

**Via UI:** Click the discovered-clusters hint to open **Register New Cluster**, pre-set to adopt. Confirm the cluster and click **Adopt**.

**Via CLI:**

```bash
sharko adopt my-cluster
```

Either path opens a pull request that creates `managed-clusters.yaml` (if this is your first cluster) with an entry for `my-cluster`, plus its values directory. Merge the PR — or tick auto-merge — and the cluster moves from "discovered" to **Managed Clusters** once Sharko confirms the connection.

Registering a brand-new cluster ArgoCD doesn't already know works the same way, with `sharko add-cluster my-cluster --region us-east-1` in place of `adopt`; see [Managing Clusters](../user-guide/clusters.md) for the three ways to hand Sharko its credentials.

## 6. Deploy Your First Addon

Adding an addon to your Catalog and turning it on for a cluster are normally two separate changes — but the Marketplace can do both in one pull request.

**Via UI:** Go to **Addons → Marketplace**, open an addon (e.g. cert-manager), and use the **Also enable on a cluster** dropdown to pick `my-cluster` before clicking **Add to catalog**.

**Via CLI:**

```bash
sharko add-to-catalog cert-manager \
  --from-marketplace --version 1.14.5 \
  --enable-on my-cluster
```

This opens one pull request that adds `cert-manager` to `catalog.yaml` (created now, if this is your first catalog entry) and turns it on for `my-cluster` in `cluster-addons/my-cluster.yaml`. Merge the PR and ArgoCD deploys it — open the cluster's detail page and watch cert-manager go from syncing to healthy in the addon table.

## Next Steps

- [Add more addons to the catalog](../user-guide/addons.md)
- [Configure ingress so the UI and API are reachable](installation.md#access-the-ui)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
- [Set up API keys for CI/CD](../user-guide/connections.md#api-keys)
