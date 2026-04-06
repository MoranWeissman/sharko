# First Run

After installing Sharko, this guide walks you through the initial configuration wizard.

## 1. Retrieve the Admin Password

Sharko generates a random admin password on first install:

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

Save this password — you will use it to log in for the first time.

## 2. Access the UI

**Option A: Port-forward (quickest)**

```bash
kubectl port-forward svc/sharko -n sharko 8080:80
```

Open [http://localhost:8080](http://localhost:8080).

**Option B: Via Ingress (production)**

If you configured ingress during installation, open `https://sharko.your-domain.com` directly.

## 3. Log In

On the login page, enter:

- **Username:** `admin`
- **Password:** the initial password from step 1

You will be prompted to change the password after first login.

## 4. Configure Your ArgoCD Connection

Navigate to **Settings → Connections → ArgoCD** and fill in:

| Field | Value |
|-------|-------|
| Server URL | e.g., `https://argocd.your-cluster.com` |
| Token | An ArgoCD account token with read access |

Click **Save** and set the connection as **active**. Sharko will verify connectivity immediately.

!!! tip "In-cluster ArgoCD"
    If ArgoCD is in the same cluster, use the in-cluster service URL: `https://argocd-server.argocd.svc.cluster.local`

## 5. Configure Your Git Connection

Navigate to **Settings → Connections → Git** and fill in:

| Field | Value |
|-------|-------|
| Provider | GitHub or Azure DevOps |
| Token | PAT with `repo` read/write |
| Repository URL | e.g., `https://github.com/your-org/your-addons-repo` |

Click **Save** and set the connection as **active**.

## 6. Install the CLI

```bash
# macOS (Homebrew)
brew install moranweissman/tap/sharko

# Or download the binary from GitHub releases
# https://github.com/MoranWeissman/sharko/releases
```

Then log in:

```bash
sharko login --server https://sharko.your-domain.com
# Enter: admin / <your-password>
```

## 7. Initialize the Addons Repository

The `sharko init` command creates the initial folder structure in your Git repo:

```bash
sharko init
```

This creates:

- An **ApplicationSet** that manages all cluster addons
- A **base values directory** with per-addon default configuration
- A **clusters directory** where per-cluster overrides live

Merge the PR that Sharko creates in your Git repo, then wait for ArgoCD to sync.

## 8. Register Your First Cluster

```bash
sharko add-cluster my-cluster \
  --addons cert-manager,metrics-server
```

Or use the UI: navigate to **Clusters → Register Cluster**.

## What's Next

- [Add more addons to the catalog](../user-guide/addons.md)
- [Register additional clusters](../user-guide/clusters.md)
- [Set up notifications](../user-guide/notifications.md)
- [Enable the AI assistant](../operator/configuration.md#ai-provider)
