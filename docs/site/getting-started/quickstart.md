# Quick Start

Get Sharko running on your cluster in about 5 minutes.

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
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

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

**Step 4 — Initialize repository:** Sharko creates the ApplicationSet, base values directory, and clusters directory in your repo. Choose **auto-merge** to merge the PR immediately, or review it yourself in GitHub/Azure DevOps first.

After the wizard completes, the dashboard loads with clusters discovered from ArgoCD.

## 5. Start Managing Clusters

From the dashboard, you will see two sections:

- **Managed clusters** — clusters registered and managed by Sharko
- **Discovered clusters** — existing ArgoCD clusters not yet managed by Sharko

Click **Start Managing** on any discovered cluster to bring it under Sharko management.

## Next Steps

- [Add addons to the catalog](../user-guide/addons.md)
- [Configure ingress for production access](installation.md#access-the-ui)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
- [Set up API keys for CI/CD](../user-guide/connections.md#api-keys)
