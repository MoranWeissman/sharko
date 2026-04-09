# Sharko User Guide

This guide covers installing Sharko, configuring it, and using the CLI and dashboard to manage addons across your Kubernetes clusters.

---

## Installation

### Prerequisites

- A Kubernetes cluster (1.27+) with ArgoCD installed
- Helm 3.x
- A GitHub Personal Access Token (PAT) with repo access

### Helm Install

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<github-pat>
```

Optional flags:

```bash
# Enable AI assistant (e.g., with OpenAI)
--set ai.enabled=true \
--set ai.provider=openai \
--set ai.apiKey=<openai-api-key> \
--set ai.cloudModel=gpt-4o

# Enable GitOps write operations (PR creation from UI/AI)
--set gitops.actions.enabled=true

# Use an existing secret instead of chart-managed secrets
--set existingSecret=my-sharko-secret
```

### Verify Installation

```bash
kubectl get pods -n sharko
kubectl get svc -n sharko
```

The Sharko server should be running and accessible on port 80 (ClusterIP by default).

---

## First Login

On first install, Sharko creates an admin account with a random password. Retrieve it:

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

Then log in via the CLI:

```bash
sharko login --server https://sharko.your-cluster.com
# Enter: admin / <initial-password>
```

Or open the UI in a browser and use the same credentials. The login page displays the Sharko banner with a product description. You will be prompted to change the password on first login.

---

## Configuring Connections

Sharko manages connections to ArgoCD and Git providers through the Settings UI. After install:

1. Open the Sharko UI in your browser
2. Navigate to **Settings** (left sidebar, Configure section — admin only)
3. Select the **Connections** tab in the left navigation panel
4. Add an **ArgoCD connection**: provide the ArgoCD server URL and an account token
5. Add a **Git connection**: select GitHub (or Azure DevOps), provide the token and repo URL
6. Set both connections as **active**

You can test each connection from the Settings page before activating it. Connections are stored in an encrypted Kubernetes Secret.

For local development, set `config.devMode: true` in Helm values to enable environment variable fallback (`ARGOCD_TOKEN`, `GITHUB_TOKEN`, etc.).

---

## CLI Usage

The Sharko CLI is a thin HTTP client. Every command sends a request to the Sharko server API.

### Login

```bash
sharko login --server https://sharko.your-cluster.com
# Prompts for username and password, stores token in ~/.sharko/config
```

### Initialize Addons Repo

```bash
sharko init
```

This creates the addons repository structure from the embedded starter templates and pushes it to Git via the configured connection. The generated structure includes bootstrap ApplicationSet templates, directory layout for cluster values, and global values. The change is made via a pull request (auto-merged if `SHARKO_GITOPS_PR_AUTO_MERGE=true`).

### Add an Addon

```bash
sharko add-addon cert-manager \
  --chart cert-manager \
  --repo https://charts.jetstack.io \
  --version 1.14.5 \
  --namespace cert-manager
```

### Register a Cluster

```bash
sharko add-cluster prod-eu \
  --addons cert-manager,metrics-server \
  --region eu-west-1
```

The server fetches cluster credentials from the configured secrets provider, registers the cluster in ArgoCD, creates a values file, and commits to Git as a pull request.

### Batch Register Clusters

Register multiple clusters in one call (up to 10):

```bash
sharko add-clusters prod-eu,prod-us,staging-eu \
  --addons cert-manager,metrics-server \
  --region eu-west-1
```

Each cluster is registered sequentially. Results are reported per-cluster; failures do not stop remaining registrations.

### Remove a Cluster

```bash
sharko remove-cluster prod-eu
```

### Update Cluster Addons

```bash
sharko update-cluster prod-eu --add-addon istio --remove-addon logging
```

### Upgrade an Addon

Upgrade an addon globally (all clusters that pick up the catalog version):

```bash
sharko upgrade-addon cert-manager --version 1.15.0
```

Upgrade an addon on a specific cluster only:

```bash
sharko upgrade-addon cert-manager --version 1.15.0 --cluster prod-eu
```

### Batch Upgrade Addons

Upgrade multiple addons in a single pull request:

```bash
sharko upgrade-addons cert-manager=1.15.0,metrics-server=0.7.1
```

### Cluster Status Overview

```bash
sharko status
```

Example output:

```
Cluster Status Overview
============
Clusters: 12 total, 11 healthy, 1 degraded
Addons:   8 in catalog
Sync:     94 synced, 2 out-of-sync, 1 unknown

Degraded Clusters:
  staging-us  2 addons out-of-sync (cert-manager, monitoring)

Version Drift:
  metrics-server  3 versions across clusters (0.6.3, 0.6.4, 0.7.0)
```

### Check Version

```bash
sharko version
```

---

## API Keys

API keys provide long-lived authentication for non-interactive consumers such as Backstage plugins, Terraform providers, and CI/CD pipelines. Unlike session tokens (which expire after 24 hours), API keys are valid until explicitly revoked.

### Create an API Key

```bash
sharko token create --name backstage --role admin
```

The token value is printed once. Store it immediately in a secure location (e.g., a Kubernetes Secret or your CI secrets store).

### List API Keys

```bash
sharko token list
```

Token values are not shown — only names, roles, and creation timestamps.

### Revoke an API Key

```bash
sharko token revoke backstage
```

The key is invalidated immediately.

### Using an API Key

Pass the API key as a Bearer token in the `Authorization` header:

```bash
curl -H "Authorization: Bearer shr_abc123..." \
  https://sharko.your-cluster.com/api/v1/fleet/status
```

Or configure it in `~/.sharko/config` in place of a session token:

```yaml
server: https://sharko.your-cluster.com
token: shr_abc123...
```

---

## Addon Secrets

Sharko can deliver secrets from your secrets provider (AWS Secrets Manager, Kubernetes Secrets) to remote clusters as Kubernetes Secrets. This is used for addons that need API keys or credentials on the cluster (e.g., Datadog agent API keys, New Relic license keys).

### How It Works

1. Define an **addon secret template** that maps a K8s Secret name/namespace to provider paths
2. When a cluster is registered, Sharko fetches the secret values and creates the K8s Secret on the remote cluster
3. When secrets rotate, call the refresh endpoint to re-push updated values

### Define an Addon Secret Template

Via CLI using the API directly (or via the UI):

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/addon-secrets \
  -d '{
    "addon_name": "datadog",
    "secret_name": "datadog-keys",
    "namespace": "datadog",
    "keys": {
      "api-key": "secrets/datadog/api-key",
      "app-key": "secrets/datadog/app-key"
    }
  }'
```

Or configure at startup via `SHARKO_ADDON_SECRETS` (JSON):

```yaml
extraEnv:
  - name: SHARKO_ADDON_SECRETS
    value: '{"datadog":{"addon_name":"datadog","secret_name":"datadog-keys","namespace":"datadog","keys":{"api-key":"secrets/datadog/api-key"}}}'
```

### List Addon Secret Definitions

```bash
curl -H "Authorization: Bearer $TOKEN" \
  https://sharko.your-cluster.com/api/v1/addon-secrets
```

### View Managed Secrets on a Cluster

```bash
curl -H "Authorization: Bearer $TOKEN" \
  https://sharko.your-cluster.com/api/v1/clusters/prod-eu/secrets
```

### Refresh Secrets on a Cluster

Re-fetch values from the provider and upsert the K8s Secrets on the remote cluster:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/clusters/prod-eu/secrets/refresh
```

---

## ArgoCD Cluster Secret Management

Sharko creates and reconciles ArgoCD cluster secrets automatically. You do not need External Secrets Operator (ESO) or any other operator to make ArgoCD aware of your clusters.

### How It Works

When a cluster is registered via `sharko add-cluster`, Sharko writes a Kubernetes Secret in the `argocd` namespace with the label `argocd.argoproj.io/secret-type: cluster`. ArgoCD's ApplicationSet cluster generator picks up this secret and starts deploying addons to the cluster.

A background reconciler runs every 3 minutes (configurable) and keeps all ArgoCD cluster secrets in sync with `configuration/cluster-addons.yaml` in Git. If a cluster is added or removed from that file, the reconciler creates or deletes the corresponding secret automatically.

### RBAC

The Helm chart automatically creates a namespaced Role + RoleBinding granting Sharko write access to Secrets in the `argocd` namespace. No manual RBAC setup is needed. The ArgoCD namespace is configurable:

```yaml
rbac:
  argocdNamespace: argocd   # default
```

### Adopting Pre-Existing Cluster Secrets

If a cluster secret already exists in the `argocd` namespace but was not created by Sharko (i.e., it lacks the `app.kubernetes.io/managed-by: sharko` label), clicking **"Start Managing"** in the UI — or registering the cluster via `sharko add-cluster` — will adopt it. Sharko overwrites the labels and credentials to match its desired state and begins managing the secret going forward.

### Reconcile Interval

The reconciler interval defaults to 3 minutes. Override via environment variable:

```yaml
extraEnv:
  - name: SHARKO_ARGOCD_RECONCILE_INTERVAL
    value: "5m"
```

---

## Batch Operations

### Batch Cluster Registration

Register up to 10 clusters in a single API call:

```bash
# Via CLI
sharko add-clusters prod-eu,prod-us,staging-eu

# Via API
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/clusters/batch \
  -d '{
    "clusters": [
      {"name": "prod-eu", "addons": {"monitoring": true}, "region": "eu-west-1"},
      {"name": "prod-us", "addons": {"monitoring": true}, "region": "us-east-1"}
    ]
  }'
```

Clusters are registered sequentially. Each cluster gets its own PR. If one cluster fails, the remaining clusters are still attempted. The response includes per-cluster results and aggregate counts.

### Discover Available Clusters

Find clusters that exist in the secrets provider but are not yet registered:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  https://sharko.your-cluster.com/api/v1/clusters/available
```

---

## Addon Upgrades

### Global Upgrade

Updates the version in `addons-catalog.yaml`. All clusters that inherit the global version will pick up the new version when ArgoCD next syncs.

```bash
# Via CLI
sharko upgrade-addon cert-manager --version 1.15.0

# Via API
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/addons/cert-manager/upgrade \
  -d '{"version": "1.15.0"}'
```

### Per-Cluster Upgrade

Updates the version in the cluster's values file only. The cluster will have a different version from the global catalog.

```bash
# Via CLI
sharko upgrade-addon cert-manager --version 1.15.0 --cluster prod-eu

# Via API
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/addons/cert-manager/upgrade \
  -d '{"version": "1.15.0", "cluster": "prod-eu"}'
```

### Batch Upgrade

Upgrade multiple addons in a single PR. All upgrades are global.

```bash
# Via CLI
sharko upgrade-addons cert-manager=1.15.0,metrics-server=0.7.1

# Via API
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/addons/upgrade-batch \
  -d '{"upgrades": {"cert-manager": "1.15.0", "metrics-server": "0.7.1"}}'
```

Each upgrade creates a PR (or multiple PRs for per-cluster upgrades). Use the version matrix in the UI to identify clusters with version drift before planning upgrades.

---

## GitOps PR Flow

Every write operation (cluster registration, addon changes, upgrades) creates a Git pull request. Sharko never commits directly to the base branch.

**With `SHARKO_GITOPS_PR_AUTO_MERGE=false` (default):**
The PR is created and left open. A human reviews and merges it. This is the recommended workflow for production changes.

**With `SHARKO_GITOPS_PR_AUTO_MERGE=true`:**
The PR is created and immediately merged. Suitable for automated pipelines where human review is handled elsewhere (e.g., CI policy checks).

The PR URL is included in every write operation response and CLI output, so you can always navigate directly to the change.

---

## Dashboard (UI)

The Sharko UI is a React-based dashboard accessible via the Sharko service URL. It provides a sky-blue themed interface with a dark sidebar and light content area.

### Login Page

The login page displays the Sharko mascot and brand banner with a description of the product. Enter your username and password to authenticate.

### Navigation

The left sidebar provides navigation organized into sections:
- **Overview**: Dashboard, Clusters, Addons
- **Manage**: Observability, Dashboards
- **Configure** (admin only): Settings

The sidebar can be collapsed to show only icons.

### Dashboard

The main dashboard shows aggregated stats: total clusters, healthy/degraded counts, total addons, sync status breakdown, and recent pull requests.

### Clusters View

- List of all registered clusters displayed as cards with health status indicators
- Click a cluster to open the **Cluster Detail** page
- Cluster Detail uses a **left navigation panel** with tabs: Overview, Addons, Config Diff, Comparison, etc.
- Register clusters, update addon assignments, and trigger credential refreshes from the detail page

### Addon Catalog

- All addons with chart name, version, and deployment count across clusters
- Click an addon to open the **Addon Detail** page
- Addon Detail uses a **left navigation panel** with tabs: Overview, Version Matrix, Upgrade Checker, etc.
- Version drift and upgrade checking are inside the addon detail page (no separate pages)
- Add addons to the catalog and trigger upgrades from the detail page

### Settings Page

The Settings page uses a **left navigation panel** with sections:
- **Connections**: ArgoCD and Git connection management (add, test, activate, delete)
- **Users**: User management (admin only) — create, edit, delete users
- **API Keys**: API key management — create, list, revoke keys
- **AI Provider**: AI assistant configuration (provider, model, API key)

Previously separate pages for Users and API Keys now redirect to the Settings page with the appropriate section selected.

### Notification Bell

A bell icon in the top bar shows notifications with an unread count badge. Click it to see a dropdown list of notifications including:
- Upgrade available alerts
- Version drift warnings
- Security advisories
- Sync failure alerts

Currently populated with mock data; will be connected to the notification API when implemented.

### AI Assistant

The AI assistant is accessed via:
- **Floating button** in the bottom-right corner of every page
- **"Ask AI" button** in the top bar

Both open a right-side panel (not a separate page) that provides a chat interface. The AI is context-aware — it knows which page you are viewing and can answer questions about the current cluster, addon, or dashboard data.

There is no dedicated AI page. The AI is always available as a side panel from any page.

### Observability

- ArgoCD health groups (healthy, degraded, missing)
- Sync activity timeline
- Attention items: clusters or addons that need action

### Embedded Dashboards

Embed external dashboards (Grafana, Datadog, etc.) in the UI:
1. Navigate to **Dashboards** in the sidebar
2. Add a dashboard URL (e.g., a Grafana iframe URL)
3. Dashboards are persisted in a Kubernetes ConfigMap

### Swagger UI

Interactive API documentation is available at `/swagger/index.html`. This provides a browsable interface for all 71+ API endpoints with request/response schemas, parameter descriptions, and try-it-out functionality.

---

## AI Assistant Configuration

Sharko includes an AI assistant for troubleshooting and cluster insights. Configure it via Helm values or the Settings UI.

### Supported Providers

| Provider | Helm Key | Notes |
|----------|----------|-------|
| Ollama | `ai.provider: ollama` | Self-hosted, runs alongside Sharko |
| OpenAI | `ai.provider: openai` | Requires API key |
| Claude | `ai.provider: claude` | Requires API key |
| Gemini | `ai.provider: gemini` | Requires API key |
| Custom OpenAI | `ai.provider: custom-openai` | Any OpenAI-compatible endpoint |

### Helm Configuration Example

```yaml
ai:
  enabled: true
  provider: openai
  apiKey: "sk-..."
  cloudModel: "gpt-4o"
  maxIterations: 8
```

### Ollama (Self-Hosted)

```yaml
ai:
  enabled: true
  provider: ollama
  ollama:
    deploy: true              # Auto-deploy Ollama pod
    model: "llama3.2"         # Default model
    agentModel: "llama3.1:8b" # Larger model for agent tool calling
    persistence: true         # Persist downloaded models across restarts
    storageSize: "10Gi"
```

### Runtime Configuration

The AI provider can also be configured at runtime via the Settings UI (AI Provider tab) without redeploying. Runtime settings are persisted in an encrypted Kubernetes Secret and override Helm values.

---

## Embedded Dashboards

Sharko supports embedding external dashboards (Grafana, Datadog, etc.) in the UI.

1. Open the Sharko UI
2. Navigate to **Dashboards** in the sidebar
3. Add a dashboard URL (e.g., a Grafana dashboard iframe URL)
4. Dashboards are persisted in a Kubernetes ConfigMap

---

## Troubleshooting

### Common Errors

**"no active ArgoCD connection"**
No ArgoCD connection is configured or set as active. Go to Settings > Connections and add/activate an ArgoCD connection.

**"no active Git connection"**
Same as above, but for Git. Configure a Git connection in Settings > Connections.

**"secrets provider not configured"**
No secrets provider is configured. Go to **Settings > Provider** in the UI or use the API to configure a provider backend (`aws-sm` or `k8s-secrets`).

**"template filesystem not configured"**
Internal error. The StarterFS should always be available. Check that the Sharko binary was built correctly.

### Check Logs

```bash
kubectl logs -n sharko deployment/sharko -f
```

### Check Health

```bash
curl https://sharko.your-cluster.com/api/v1/health
```

### Reset Admin Password

If you lose the admin password, delete the secret and restart:

```bash
kubectl delete secret sharko -n sharko
kubectl rollout restart deployment/sharko -n sharko
```

A new random admin password will be generated. Retrieve it with the `kubectl get secret` command shown in the First Login section.

### Connection Issues

If ArgoCD or Git connections fail:

1. Test the connection from the Settings UI (Connections tab, click "Test")
2. Check that the ArgoCD account token has sufficient permissions
3. Verify the GitHub PAT has `repo` scope
4. Check network connectivity from the Sharko pod to ArgoCD/GitHub

---

## Environment Variables Reference

| Variable | Description | Default |
|----------|-------------|---------|
| `SHARKO_PORT` | HTTP server port | `8080` |
| Provider type | Secrets provider backend (`aws-sm`, `k8s-secrets`) — configure via **Settings UI** or API | (none) |
| `SHARKO_PROVIDER_REGION` | AWS region for secrets provider | (none) |
| `SHARKO_ENCRYPTION_KEY` | Encryption key for connection store (required in K8s) | (none) |
| `SHARKO_DEV_MODE` | Enable env var fallback for credentials | `false` |
| `SHARKO_GITOPS_PR_AUTO_MERGE` | Auto-merge PRs after creation | `false` |
| `SHARKO_GITOPS_BRANCH_PREFIX` | Branch prefix for PR branches | `sharko/` |
| `SHARKO_GITOPS_COMMIT_PREFIX` | Commit message prefix | `sharko:` |
| `SHARKO_GITOPS_BASE_BRANCH` | Target branch for PRs | `main` |
| `SHARKO_GITOPS_REPO_URL` | Git repo URL for template placeholders | (none) |
| `SHARKO_ADDON_SECRETS` | JSON-encoded addon secret definitions (see Addon Secrets section) | (none) |
| `SHARKO_ARGOCD_RECONCILE_INTERVAL` | Interval for ArgoCD cluster secret reconciliation (e.g. `3m`, `5m`) | `3m` |
| `SHARKO_DEFAULT_ADDONS` | Comma-separated default addons applied to new clusters | (none) |
| `SHARKO_HOST_CLUSTER_NAME` | Name of the host cluster running Sharko (for in-cluster deployment) | (none) |
| `SHARKO_INIT_AUTO_BOOTSTRAP` | Auto-bootstrap ArgoCD during init (not yet implemented, post-v1) | `false` |
| `GITHUB_TOKEN` | GitHub PAT | (none) |
| `AI_PROVIDER` | AI provider (`ollama`, `openai`, `claude`, `gemini`, `custom-openai`) | (none) |
| `AI_API_KEY` | API key for cloud AI provider | (none) |
| `AI_CLOUD_MODEL` | Model name for cloud AI provider | (none) |
