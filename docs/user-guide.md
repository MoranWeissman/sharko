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

## Cluster Status Model

Sharko tracks five cluster states. The status is computed from connectivity test results and ArgoCD health data.

| Status | Meaning |
|--------|---------|
| `Unknown` | No observation recorded yet. The cluster was just registered and has not been tested. |
| `Connected` | Stage 1 connectivity test passed. The Kubernetes API is reachable and RBAC permissions are sufficient. |
| `Verified` | Stage 2 ArgoCD round-trip passed (confirms ArgoCD can manage the cluster). |
| `Operational` | The cluster has at least one healthy addon deployed via ArgoCD. This is the steady-state for a working cluster. |
| `Unreachable` | The last connectivity test failed. Check credentials, network, or RBAC. |

Status is computed by a pure function: `ComputeStatus(observation, hasHealthyAddon)`. Results are cached for 30 seconds (configurable via `SHARKO_CLUSTER_STATUS_CACHE_TTL`). To force a fresh check, click **Test** in the UI or call `POST /api/v1/clusters/{name}/test`.

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

### Register a Cluster (Direct Mode)

```bash
sharko add-cluster prod-eu \
  --addons cert-manager,metrics-server \
  --region eu-west-1
```

The server fetches cluster credentials from the configured secrets provider, verifies connectivity (Stage 1 test), registers the cluster in ArgoCD, creates a values file, and commits to Git as a pull request.

### Cluster Discovery and Adoption

Sharko supports two modes for onboarding clusters: **direct registration** (above) and **discovery + adoption**.

#### Discover Available Clusters

Find clusters that exist in the secrets provider but are not yet registered:

```bash
# GET-based discovery (Kubernetes Secrets provider)
curl -H "Authorization: Bearer $TOKEN" \
  https://sharko.your-cluster.com/api/v1/clusters/available

# POST-based discovery (EKS — requires region + role)
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/clusters/discover \
  -d '{"region": "us-east-1"}'
```

In the UI, navigate to **Clusters** and click **Discover**. Unregistered clusters appear as candidates for adoption.

#### Adopt Existing Clusters

Adoption is for clusters that already have an ArgoCD cluster secret but are not managed by Sharko. It verifies connectivity, creates a values file, adds the cluster to `managed-clusters.yaml`, and creates a PR.

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/clusters/adopt \
  -d '{"clusters": ["prod-eu", "staging-us"], "auto_merge": false}'
```

In the UI, discovered clusters show a **Start Managing** button. Clicking it triggers adoption.

Key behaviors:
- Adoption rejects clusters managed by a different tool (non-`sharko` `managed-by` label).
- A Stage 1 connectivity test runs before adoption proceeds.
- If a PR already exists for the cluster (idempotent retry), it returns the existing PR instead of creating a duplicate.
- After the PR is merged, Sharko sets the `sharko.sharko.io/adopted` annotation on the ArgoCD cluster secret.

#### Un-adopt a Cluster

Reverses an adoption without deleting the ArgoCD cluster secret:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/clusters/prod-eu/unadopt
```

Steps:
1. Verifies the cluster has the `adopted` annotation (errors if not -- use `remove-cluster` instead).
2. Removes the `managed-by` label and `adopted` annotation from the ArgoCD secret (keeps the secret).
3. Deletes Sharko-created addon secrets from the remote cluster (best-effort).
4. Creates a PR to remove the cluster from `managed-clusters.yaml` and delete its values file.

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

Cluster removal supports three cleanup scopes:

| Scope | What it does |
|-------|-------------|
| `all` (default) | Removes from `managed-clusters.yaml`, deletes values file via PR, deletes addon secrets from remote cluster, deletes ArgoCD cluster secret. |
| `git` | Same Git changes as `all`, but skips remote addon secret deletion and ArgoCD secret deletion. |
| `none` | Only removes the `managed-clusters.yaml` entry. Values file and ArgoCD secret are kept. |

Via the API, pass `cleanup` in the request body:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -X DELETE https://sharko.your-cluster.com/api/v1/clusters/prod-eu \
  -d '{"yes": true, "cleanup": "git"}'
```

### Disable an Addon on a Cluster

Remove a specific addon from a single cluster without removing it from the catalog:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -X DELETE https://sharko.your-cluster.com/api/v1/clusters/prod-eu/addons/monitoring \
  -d '{"yes": true, "cleanup": "all"}'
```

Cleanup scopes for addon disable:

| Scope | What it does |
|-------|-------------|
| `all` (default) | Updates values file, updates `managed-clusters.yaml` labels, deletes addon secrets from remote cluster. |
| `labels` | Updates values file and `managed-clusters.yaml` labels only. |
| `none` | Updates the values file only. |

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

### PR Management

Every write operation creates a tracked pull request. Use the `sharko pr` subcommands to manage them.

#### List Tracked PRs

```bash
sharko pr list
sharko pr list --status open --cluster prod-eu
sharko pr list -o json
```

#### Check PR Status

```bash
sharko pr status 42
```

#### Force Refresh a PR

Poll the Git provider for the latest status:

```bash
sharko pr refresh 42
```

#### Wait for a PR to Complete

Block until the PR is merged (exit 0), closed without merge (exit 1), or timeout expires (exit 2):

```bash
sharko pr wait 42 --timeout 10m
```

This is useful in CI/CD pipelines where you need to wait for a Sharko-created PR to be merged before proceeding.

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

## RBAC (Role-Based Access Control)

Sharko uses three roles with hierarchical permissions. Higher roles include all permissions of lower roles.

| Role | Level | Description |
|------|-------|-------------|
| **Admin** | 2 | Full access. Can manage users, tokens, connections, and perform destructive operations (remove cluster, remove addon). |
| **Operator** | 1 | Write access. Can register clusters, adopt clusters, update addons, trigger upgrades, and perform day-to-day management. |
| **Viewer** | 0 | Read-only access. Can view clusters, addons, dashboard, audit log, and status. |

Example permission mapping:

| Action | Minimum Role |
|--------|-------------|
| `cluster.list`, `addon.list` | Viewer |
| `cluster.register`, `cluster.adopt`, `addon.upgrade` | Operator |
| `cluster.remove`, `token.create`, `user.create` | Admin |

Actions not explicitly mapped default to **Admin** (fail-closed). The role is determined from the authenticated user's role and passed via the `X-Sharko-Role` header by the auth middleware.

---

## API Keys (Token Management)

API keys provide long-lived authentication for non-interactive consumers such as Backstage plugins, Terraform providers, and CI/CD pipelines. Unlike session tokens (which expire after 24 hours), API keys have a configurable expiry.

### Create an API Key

```bash
sharko token create --name backstage --role admin
```

Key behaviors:
- The token value is printed once. Store it immediately in a secure location (e.g., a Kubernetes Secret or your CI secrets store).
- **Role bounding:** You can only create tokens with a role equal to or lower than your own. An Operator cannot create Admin tokens.
- **Default expiry:** 365 days. Admins can set no-expiry with duration `-1`.
- Expired tokens are automatically rejected during validation.

### List API Keys

```bash
sharko token list
```

Token values are not shown -- only names, roles, creation timestamps, and last-used timestamps.

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

## Audit Log

Sharko records significant events in an in-memory audit log (default capacity: 1000 entries, configurable via `SHARKO_AUDIT_BUFFER_SIZE`). Every cluster registration, adoption, removal, upgrade, PR merge, and configuration change is logged.

### Query the Audit Log

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "https://sharko.your-cluster.com/api/v1/audit?action=register&source=cli&limit=50"
```

Supported filter parameters: `user`, `action`, `source`, `result`, `cluster`, `since`, `limit` (default 50).

### Real-Time Event Stream (SSE)

Subscribe to audit events in real time via Server-Sent Events:

```bash
curl -H "Authorization: Bearer $TOKEN" -N \
  https://sharko.your-cluster.com/api/v1/audit/stream
```

Events are delivered as they happen. The stream uses a buffered channel (capacity 64); slow subscribers may miss events.

### UI Viewer

The audit log is accessible in the Sharko UI. Events are displayed with level (info/warn/error), timestamp, user, action, resource, source, and result.

Each audit entry includes:

| Field | Description |
|-------|-------------|
| `event` | Event type (e.g., `cluster_registered`, `pr_created`, `pr_merged`) |
| `user` | Username or `system` for background operations |
| `action` | Operation type (register, remove, update, test, adopt, etc.) |
| `resource` | Target (e.g., `cluster:prod-eu`, `addon:cert-manager`) |
| `source` | Origin (ui, cli, api, reconciler, webhook, prtracker) |
| `result` | Outcome (success, failure, partial) |

---

## Diagnose Tool

When a cluster connectivity test fails, the diagnose tool helps identify the root cause by running a series of IAM and RBAC permission checks.

### CLI / API

```bash
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/clusters/prod-eu/diagnose
```

The response includes:
- **Identity:** The caller ARN (who Sharko is authenticating as).
- **Role assumption:** The assumed role ARN, or "N/A" if not using role assumption.
- **Namespace access:** Pass/fail results for each permission check (list namespaces, get namespace, create/get/delete secret).
- **Suggested fixes:** Copy-paste-ready Kubernetes RBAC YAML (ClusterRole/ClusterRoleBinding for namespace access, Role/RoleBinding for secret CRUD) using a `sharko-access` group as the subject.

### UI

In the cluster detail page, when a test fails, a **"Why did this fail?"** button appears. Clicking it runs the diagnose tool and displays the results inline with the suggested fixes.

---

## Upgrade Checker

The upgrade checker helps you evaluate available chart versions before upgrading an addon.

### Upgrade Recommendations

When you open the **Upgrade Checker** tab on an addon detail page, Sharko automatically fetches smart recommendations for the current catalog version. Three candidate target versions are offered when available:

| Recommendation | Meaning |
|----------------|---------|
| **Next patch** | Lowest version that increments only the patch digit (e.g., 1.14.5 → 1.14.6) |
| **Next minor** | Lowest version that increments the minor digit (e.g., 1.14.5 → 1.15.0) |
| **Latest stable** | Highest non-pre-release version in the Helm repo index |

Click a recommendation to jump to that version in the version list. You can also search for a specific version using the search box in the version list — useful when you need to analyze an arbitrary target.

### Analyze-Before-Upgrade

The UI enforces an **analyze-before-upgrade** workflow. Before the **Upgrade** button becomes active, you must:

1. Select a target version from the version list or recommendations.
2. Click **Analyze** to fetch the values diff and optionally the AI-generated summary.
3. Review the analysis result.

Only after analysis completes does the **Upgrade** button become clickable. This prevents accidental blind upgrades.

### Step-by-Step Upgrade Progress

After clicking **Upgrade**, the UI shows a step-by-step progress view that auto-refreshes until the upgrade PR is created. Steps displayed:

1. Validating version
2. Generating values diff
3. Creating pull request
4. Done (with PR URL)

### Ask AI on Upgrade Errors

When an upgrade error banner appears, an **Ask AI** button is shown alongside the error. Clicking it opens the AI assistant panel with a pre-filled prompt that includes the addon name, current version, target version, and the error message. This saves you from manually copying context into the AI chat.

### API Endpoints

```bash
# Smart recommendations for an addon
curl -H "Authorization: Bearer $TOKEN" \
  https://sharko.your-cluster.com/api/v1/upgrade/cert-manager/recommendations

# List all available versions
curl -H "Authorization: Bearer $TOKEN" \
  https://sharko.your-cluster.com/api/v1/upgrade/cert-manager/versions

# Check upgrade impact (values diff)
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/upgrade/check \
  -d '{"addon": "cert-manager", "from_version": "1.14.5", "to_version": "1.15.0"}'
```

---

## Prometheus Metrics

Sharko exposes Prometheus metrics at `GET /metrics`. 20 metrics across 6 categories:

| Category | Example Metric |
|----------|---------------|
| Cluster | `sharko_cluster_count{status="Connected"}`, `sharko_cluster_test_duration_seconds` |
| Addon | `sharko_addon_sync_status{cluster,addon,status}`, `sharko_addon_health` |
| Reconciler | `sharko_reconciler_runs_total{reconciler,result}` |
| PR | `sharko_pr_merge_duration_seconds` |
| HTTP | `sharko_api_requests_total{method,path,status}` |
| Auth | `sharko_auth_login_total{result}` |

Dynamic path segments (cluster names, addon names) are normalized to placeholders (e.g., `/clusters/{name}`) to prevent cardinality explosion.

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
# GET — Kubernetes Secrets provider
curl -H "Authorization: Bearer $TOKEN" \
  https://sharko.your-cluster.com/api/v1/clusters/available

# POST — EKS discovery (requires region)
curl -H "Authorization: Bearer $TOKEN" \
  -X POST https://sharko.your-cluster.com/api/v1/clusters/discover \
  -d '{"region": "us-east-1"}'
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

**Bootstrap app health banner:** If the Sharko bootstrap ApplicationSet is degraded or out-of-sync in ArgoCD, the dashboard displays a warning banner at the top. This indicates that ArgoCD may not be deploying addons correctly. Check the Observability view for details; the dedicated **Bootstrap App** section there shows the full health and sync state.

**Auto-refresh:** The dashboard refreshes automatically every 30 seconds. A **Refresh** button in the top-right corner triggers an immediate refresh without navigating away.

### Clusters View

- List of all registered clusters displayed as cards with health status indicators
- Click a cluster to open the **Cluster Detail** page
- Cluster Detail uses a **left navigation panel** with tabs: Overview, Addons, Config Diff, Comparison, etc.
- Register clusters, update addon assignments, and trigger credential refreshes from the detail page
- **ArgoCD diagnostics:** When Sharko cannot reach ArgoCD for a cluster, a single consolidated error banner appears at the top of the cluster detail page with the connection error message. This is surfaced via the `argocd_connection_status` field returned by the comparison endpoint.
- **Auto-refresh:** Cluster Detail and Clusters Overview pages refresh every 30 seconds. A **Refresh** button is available on both pages for manual refresh.

### Addon Catalog

- All addons with chart name, version, and deployment count across clusters
- Click an addon to open the **Addon Detail** page
- Addon Detail uses a **left navigation panel** with tabs: Overview, Version Matrix, Upgrade Checker, etc.
- Version drift and upgrade checking are inside the addon detail page (no separate pages)
- Add addons to the catalog and trigger upgrades from the detail page
- **Auto-refresh:** Addon Catalog auto-refreshes every 60 seconds. Addon Detail auto-refreshes every 30 seconds. Both pages have a **Refresh** button.

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
- **"Ask AI" button** on error banners (pre-fills context from the error)

Both open a right-side panel (not a separate page) that provides a chat interface. The AI is context-aware — it knows which page you are viewing and can answer questions about the current cluster, addon, or dashboard data.

There is no dedicated AI page. The AI is always available as a side panel from any page.

**Resizable panel:** Drag the left edge of the AI panel to resize it. The panel can be dragged between 320 px and 700 px wide. The main content area maintains a minimum width of 400 px regardless of panel size.

**Pre-filled context prompts:** When you click **Ask AI** on an error banner (e.g., a cluster connectivity error or upgrade failure), the chat opens with a pre-filled message that includes the resource name, the error text, and relevant version or context information. This eliminates manual copy-paste when asking the AI to diagnose a specific problem.

**Investigation-first protocol:** The AI uses a tool-first approach — it queries live cluster and ArgoCD data before providing advice, rather than speculating based on the error message alone. For example, when investigating an upgrade error, the AI checks the actual ArgoCD application events and pod logs before suggesting a fix.

**AI tools include** (as of v1.16.0): list clusters, get cluster detail, get addon status, query ArgoCD app health/events/logs, compare Helm versions, fetch changelog, get ArgoCD cluster connection status (`get_argocd_cluster_connection`), enable/disable addons, sync/refresh ArgoCD apps.

### Observability

- ArgoCD health groups (healthy, degraded, missing)
- Sync activity timeline
- Attention items: clusters or addons that need action
- **Bootstrap App section:** A dedicated section shows the health and sync status of the Sharko bootstrap ApplicationSet. When the bootstrap app is degraded or out-of-sync, the section expands with the ArgoCD message to help identify the root cause. This is the canonical place to diagnose bootstrapping failures when the dashboard banner fires.

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
