# Sharko API Contract

> This document defines every API endpoint, request/response shape, error code, and
> orchestration behavior. It is the single source of truth for v1.0.0 implementation.
> CLI commands, UI write features, and IDP integrations all derive from this contract.
>
> **Gate:** No implementation code is written until this contract is reviewed and approved.

---

## 1. Conventions

### Base URL

All endpoints are under `/api/v1/`. The server listens on the configured port (default 8080).

```
https://sharko.example.com/api/v1/clusters
```

### Authentication

All endpoints except `GET /api/v1/health` and `POST /api/v1/auth/login` require authentication.

**How to authenticate:**
```
Authorization: Bearer <token>
```

**How to get a token:**
```bash
POST /api/v1/auth/login
Content-Type: application/json

{"username": "admin", "password": "secret"}
```

Response: `{"token": "abc123...", "username": "admin", "role": "admin"}`

The CLI stores this token in `~/.sharko/config`. The UI stores it in sessionStorage.
Tokens expire after 24 hours.

### Response Format

**Success:**
```json
{
  "clusters": [...],
  "health_stats": {...}
}
```

**Error:**
```json
{
  "error": "human-readable error message"
}
```

### Partial Success

Write operations that involve multiple steps (e.g., register cluster) can return partial success.
This is NOT an error — it means some steps completed and others failed.

```json
{
  "status": "partial",
  "completed_steps": ["fetch_credentials", "verify_connectivity", "register_argocd"],
  "failed_step": "git_commit",
  "error": "Git push failed: authentication error",
  "message": "Cluster registered in ArgoCD but Git commit failed. Run 'sharko remove-cluster prod-eu' to clean up, or retry.",
  "cluster": { "name": "prod-eu", "server": "https://..." }
}
```

HTTP status for partial success: **207 Multi-Status**

### Standard Error Codes

| Code | Meaning |
|------|---------|
| 400 | Bad request — invalid input, missing required fields |
| 401 | Unauthorized — missing or invalid token |
| 404 | Not found — resource doesn't exist |
| 409 | Conflict — resource already exists |
| 422 | Unprocessable — valid JSON but business rule violation |
| 429 | Too many requests — rate limited (login only) |
| 500 | Internal server error |
| 502 | Bad gateway — upstream service (ArgoCD, Git, provider) unreachable |
| 207 | Partial success — some steps completed, see response body |

---

## 2. Existing Read API

These endpoints are already implemented and working. Listed here for completeness — the v1.0.0 implementation does NOT modify these.

### Health

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/health` | Server health + ArgoCD connectivity. No auth required. |

### Connections

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/connections/` | List configured connections (Git + ArgoCD) |
| POST | `/api/v1/connections/` | Create a new connection |
| PUT | `/api/v1/connections/{name}` | Update a connection |
| DELETE | `/api/v1/connections/{name}` | Delete a connection |
| POST | `/api/v1/connections/active` | Set the active connection |
| POST | `/api/v1/connections/test` | Test a connection |
| POST | `/api/v1/connections/test-credentials` | Test credentials without saving |
| GET | `/api/v1/connections/discover-argocd` | Auto-discover ArgoCD in-cluster |

### Clusters (Read)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/clusters` | List all clusters with health stats |
| GET | `/api/v1/clusters/{name}` | Cluster detail + addon list |
| GET | `/api/v1/clusters/{name}/values` | Raw cluster values YAML |
| GET | `/api/v1/clusters/{name}/config-diff` | Config diff: cluster overrides vs global defaults |
| GET | `/api/v1/clusters/{name}/comparison` | Git vs ArgoCD comparison for this cluster |

**Response: `GET /api/v1/clusters`**
```json
{
  "clusters": [
    {
      "name": "prod-eu",
      "labels": {"monitoring": "enabled", "logging": "enabled"},
      "region": "eu-west-1",
      "server_version": "1.29.3",
      "connection_status": "connected"
    }
  ],
  "health_stats": {
    "total_in_git": 12,
    "connected": 10,
    "failed": 1,
    "missing_from_argocd": 1,
    "not_in_git": 0
  }
}
```

**Response: `GET /api/v1/clusters/{name}`**
```json
{
  "cluster": {
    "name": "prod-eu",
    "labels": {"monitoring": "enabled"},
    "region": "eu-west-1",
    "server_version": "1.29.3",
    "connection_status": "connected"
  },
  "addons": [
    {
      "addon_name": "monitoring",
      "chart": "kube-prometheus-stack",
      "repo_url": "https://prometheus-community.github.io/helm-charts",
      "current_version": "56.6.2",
      "enabled": true,
      "namespace": "monitoring",
      "argocd_sync_status": "Synced",
      "argocd_health_status": "Healthy",
      "argocd_version": "56.6.2"
    }
  ]
}
```

### Addons (Read)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/addons/list` | List all addons from Git config |
| GET | `/api/v1/addons/catalog` | Addon catalog with deployment stats across clusters |
| GET | `/api/v1/addons/version-matrix` | Version matrix: addon x cluster grid |
| GET | `/api/v1/addons/{name}/values` | Raw global values YAML for an addon |
| GET | `/api/v1/addons/{name}` | Addon detail: which clusters have it, version spread |

### Dashboard

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dashboard/stats` | Aggregated fleet statistics |
| GET | `/api/v1/dashboard/attention` | Items needing attention (degraded, out-of-sync) |
| GET | `/api/v1/dashboard/pull-requests` | Recent PRs from the Git provider |

### Observability

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/observability/overview` | Fleet health overview (from ArgoCD) |

### Upgrade Checker

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/upgrade/{addonName}/versions` | Available chart versions for an addon |
| POST | `/api/v1/upgrade/check` | Check upgrade impact (values diff) |
| POST | `/api/v1/upgrade/ai-summary` | AI-generated upgrade summary |
| GET | `/api/v1/upgrade/ai-status` | AI summary generation status |

### AI Agent

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/agent/chat` | Send message to AI assistant |
| POST | `/api/v1/agent/reset` | Reset AI agent conversation |

### AI Configuration

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/ai/config` | Get AI provider configuration |
| POST | `/api/v1/ai/config` | Save AI provider configuration |
| POST | `/api/v1/ai/provider` | Set active AI provider |
| POST | `/api/v1/ai/test` | Test AI connectivity |
| POST | `/api/v1/ai/test-config` | Test AI config without saving |

### Other

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/embedded-dashboards` | List saved dashboard embeds |
| POST | `/api/v1/embedded-dashboards` | Save dashboard embeds |
| GET | `/api/v1/docs/list` | List available docs |
| GET | `/api/v1/docs/{slug}` | Get a specific doc |
| GET | `/api/v1/cluster/nodes` | Cluster node info |

### Auth & Users

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/auth/login` | Login, returns Bearer token |
| POST | `/api/v1/auth/update-password` | Change password |
| POST | `/api/v1/auth/hash` | Generate bcrypt hash (setup only) |
| GET | `/api/v1/users` | List users (admin only) |
| POST | `/api/v1/users` | Create user (admin only) |
| PUT | `/api/v1/users/{username}` | Update user (admin only) |
| DELETE | `/api/v1/users/{username}` | Delete user (admin only) |
| POST | `/api/v1/users/{username}/reset-password` | Reset user password (admin only) |

---

## 3. New Write API — Cluster Operations

These endpoints are new in v1.0.0. Each is handled by the orchestrator (`internal/orchestrator/`).

### POST /api/v1/clusters — Register a Cluster

Register a new cluster: fetch credentials from the secrets provider, verify connectivity, register in ArgoCD, create values file, commit to Git.

**Request:**
```json
{
  "name": "prod-eu",
  "addons": {
    "monitoring": true,
    "logging": true,
    "cert-manager": true
  },
  "region": "eu-west-1"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | yes | Cluster name. Must match the values file name (coupling contract). Alphanumeric + hyphens. |
| addons | map[string]bool | no | Addon labels to set. Defaults to none. |
| region | string | no | Cluster region metadata. |

**Orchestration Steps:**
1. Validate input (name format, no existing cluster with same name)
2. Fetch credentials from secrets provider (`provider.GetCredentials(name)`)
3. Verify Kubernetes connectivity (connect to cluster API, get version)
4. Register cluster in ArgoCD (create cluster secret with addon labels)
5. Generate cluster values file
6. Commit to Git (direct or PR, based on server gitops config)

**Success Response (201 Created):**
```json
{
  "status": "success",
  "cluster": {
    "name": "prod-eu",
    "server": "https://ABCD.eu-west-1.eks.amazonaws.com",
    "server_version": "1.29.3",
    "node_count": 12,
    "addons": {"monitoring": true, "logging": true, "cert-manager": true}
  },
  "git": {
    "mode": "pr",
    "pr_url": "https://github.com/org/addons/pull/42",
    "branch": "sharko/add-cluster-prod-eu",
    "values_file": "configuration/addons-clusters-values/prod-eu.yaml"
  }
}
```

**Partial Success Response (207 Multi-Status):**
```json
{
  "status": "partial",
  "completed_steps": ["validate", "fetch_credentials", "verify_connectivity", "register_argocd"],
  "failed_step": "git_commit",
  "error": "Git push failed: remote rejected (branch protection)",
  "message": "Cluster registered in ArgoCD but Git commit failed. Run 'sharko remove-cluster prod-eu' to clean up, or retry.",
  "cluster": {
    "name": "prod-eu",
    "server": "https://ABCD.eu-west-1.eks.amazonaws.com"
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Invalid cluster name or request body |
| 404 | Cluster not found in secrets provider |
| 409 | Cluster already registered in ArgoCD |
| 502 | Secrets provider, ArgoCD, or Git unreachable |

**Rollback Rules:**
- Steps 1-3 fail → no cleanup needed (nothing was created)
- Step 4 fails → no cleanup needed (ArgoCD registration didn't happen)
- Steps 5-6 fail → **DO NOT auto-rollback ArgoCD registration.** Return partial success. ArgoCD may have already started deploying addons; deregistering could trigger cascade deletion.

---

### DELETE /api/v1/clusters/{name} — Deregister a Cluster

Remove a cluster from ArgoCD and delete its values file from Git.

**Path Parameters:**
- `name` — cluster name

**Orchestration Steps:**
1. Verify cluster exists in ArgoCD
2. Remove cluster from ArgoCD (delete cluster secret)
3. Delete values file from Git
4. Commit to Git (direct or PR)

**Success Response (200 OK):**
```json
{
  "status": "success",
  "message": "Cluster prod-eu deregistered",
  "git": {
    "mode": "direct",
    "commit_sha": "abc123",
    "deleted_file": "configuration/addons-clusters-values/prod-eu.yaml"
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 404 | Cluster not found in ArgoCD |
| 502 | ArgoCD or Git unreachable |

**Warning:** Deregistering a cluster from ArgoCD will cause ArgoCD to stop managing addons on that cluster. Depending on ArgoCD's cascade delete policy, this MAY delete the addon resources from the cluster. The API response should include a warning about this.

---

### PATCH /api/v1/clusters/{name} — Update Cluster Addon Labels

Update which addons are enabled/disabled on a cluster by modifying its ArgoCD labels and cluster values file.

**Path Parameters:**
- `name` — cluster name

**Request:**
```json
{
  "addons": {
    "istio": true,
    "keda": false
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| addons | map[string]bool | yes | Addons to enable (true) or disable (false). Existing labels not mentioned are untouched. |

**Orchestration Steps:**
1. Verify cluster exists in ArgoCD
2. Update ArgoCD cluster secret labels
3. Update cluster values file in Git (enable/disable addon sections)
4. Commit to Git (direct or PR)

**Success Response (200 OK):**
```json
{
  "status": "success",
  "cluster": "prod-eu",
  "updated_addons": {"istio": true, "keda": false},
  "git": {
    "mode": "pr",
    "pr_url": "https://github.com/org/addons/pull/43"
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Invalid request body |
| 404 | Cluster not found in ArgoCD |
| 422 | Addon not in catalog |
| 502 | ArgoCD or Git unreachable |

---

### POST /api/v1/clusters/{name}/refresh — Refresh Cluster Credentials

Re-fetch credentials from the secrets provider and update the ArgoCD cluster secret.

**Path Parameters:**
- `name` — cluster name

**Orchestration Steps:**
1. Verify cluster exists in ArgoCD
2. Fetch fresh credentials from secrets provider
3. Update ArgoCD cluster secret with new credentials
4. Verify connectivity with new credentials

**Success Response (200 OK):**
```json
{
  "status": "success",
  "cluster": "prod-eu",
  "message": "Credentials refreshed and connectivity verified"
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 404 | Cluster not found in ArgoCD or secrets provider |
| 502 | Secrets provider or ArgoCD unreachable |

---

## 4. New Write API — Addon Operations

### POST /api/v1/addons — Add Addon to Catalog

Add a new addon to the addons catalog configuration. This registers the addon — it does NOT deploy it to any cluster (that happens when a cluster has the addon label enabled).

**Request:**
```json
{
  "name": "cert-manager",
  "chart": "cert-manager",
  "repo_url": "https://charts.jetstack.io",
  "version": "1.14.5",
  "namespace": "cert-manager"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | yes | Addon name. Used as the label key on clusters. |
| chart | string | yes | Helm chart name |
| repo_url | string | yes | Helm repo URL |
| version | string | yes | Chart version |
| namespace | string | no | Target namespace. Defaults to addon name. |

**Orchestration Steps:**
1. Validate input (name format, version exists in chart repo)
2. Add entry to `addons-catalog.yaml` in Git
3. Create global values file at `configuration/addons-global-values/{name}.yaml`
4. Commit to Git (direct or PR)

**Success Response (201 Created):**
```json
{
  "status": "success",
  "addon": {
    "name": "cert-manager",
    "chart": "cert-manager",
    "repo_url": "https://charts.jetstack.io",
    "version": "1.14.5",
    "namespace": "cert-manager"
  },
  "git": {
    "mode": "pr",
    "pr_url": "https://github.com/org/addons/pull/44"
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Invalid addon name or missing required fields |
| 409 | Addon already exists in catalog |
| 502 | Git unreachable |

---

### DELETE /api/v1/addons/{name}?confirm=true — Remove Addon from Catalog

Remove an addon from the catalog. **This is destructive:** when the addon entry is removed from the catalog and the AppSet template no longer references it, ArgoCD WILL cascade-delete the Application from every cluster that had it enabled.

**Requires `?confirm=true` query parameter.** Without it, returns 400 with a warning explaining the impact. This follows the same safety pattern as Kubernetes cascade deletion.

**Path Parameters:**
- `name` — addon name

**Query Parameters:**
- `confirm` — must be `true` to proceed. Without it, returns a dry-run showing what would be affected.

**Without `?confirm=true` — Dry Run Response (400):**
```json
{
  "error": "Destructive operation requires ?confirm=true",
  "impact": {
    "addon": "cert-manager",
    "affected_clusters": ["prod-eu", "prod-us", "staging"],
    "total_deployments_to_remove": 3,
    "warning": "ArgoCD will cascade-delete cert-manager from all 3 clusters when the ApplicationSet entry is removed."
  }
}
```

**Orchestration Steps (with `?confirm=true`):**
1. Verify addon exists in catalog
2. Remove entry from `addons-catalog.yaml`
3. Remove global values file
4. Commit to Git (direct or PR)

**Success Response (200 OK):**
```json
{
  "status": "success",
  "message": "Addon cert-manager removed from catalog",
  "warning": "ArgoCD will cascade-delete cert-manager from 3 clusters as the ApplicationSet entry is removed.",
  "affected_clusters": ["prod-eu", "prod-us", "staging"],
  "git": {
    "mode": "direct",
    "commit_sha": "def456"
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing `?confirm=true` (returns dry-run impact) |
| 404 | Addon not found in catalog |
| 502 | Git unreachable |

---

## 5. New System API

### POST /api/v1/init — Initialize Addons Repo

Create the addons repo structure, push to Git, and bootstrap the root-app into ArgoCD.

**Preconditions:**
- Sharko server is running (installed via `helm install`)
- Git connection is configured and working (test via `POST /api/v1/connections/test`)
- ArgoCD connection is configured and working
- The target Git repo exists and is empty (or has no conflicting files)

**Request:**
```json
{
  "bootstrap_argocd": true
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| bootstrap_argocd | bool | no | Whether to apply root-app to ArgoCD after pushing. Default: true. |

**Orchestration Steps:**
1. Verify Git connection (can push to repo)
2. Verify ArgoCD connection (can create applications)
3. Generate repo structure from embedded `templates/starter/`, using server-side path config to determine directory layout
4. Commit and push to Git
5. If `bootstrap_argocd`: create ArgoCD repo connection + apply root-app
6. Verify ArgoCD synced the bootstrap

**Success Response (201 Created):**
```json
{
  "status": "success",
  "repo": {
    "url": "https://github.com/org/addons",
    "branch": "main",
    "files_created": [
      "bootstrap/root-app.yaml",
      "bootstrap/templates/addons-appset.yaml",
      "configuration/addons-catalog.yaml",
      "configuration/addons-clusters-values/cluster-example.yaml"
    ]
  },
  "argocd": {
    "bootstrapped": true,
    "root_app": "sharko-bootstrap"
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 409 | Repo already has addon catalog and bootstrap files (already initialized) |
| 502 | Git or ArgoCD unreachable |

---

### GET /api/v1/fleet/status — Fleet Overview

Aggregated fleet health. Combines data from existing cluster and addon services.

**Response (200 OK):**
```json
{
  "total_clusters": 12,
  "healthy_clusters": 10,
  "degraded_clusters": 1,
  "disconnected_clusters": 1,
  "total_addons": 15,
  "total_deployments": 120,
  "healthy_deployments": 115,
  "degraded_deployments": 3,
  "out_of_sync_deployments": 2,
  "clusters": [
    {
      "name": "prod-eu",
      "connection_status": "connected",
      "total_addons": 8,
      "healthy_addons": 8,
      "degraded_addons": 0
    }
  ]
}
```

---

### GET /api/v1/providers — List Secrets Providers

List the configured secrets provider and its status.

**Response (200 OK):**
```json
{
  "configured_provider": {
    "type": "aws-sm",
    "region": "eu-west-1",
    "status": "connected"
  },
  "available_types": ["aws-sm", "k8s-secrets"]
}
```

---

### POST /api/v1/providers/test — Test Provider Connectivity

**Request:**
```json
{
  "type": "aws-sm",
  "region": "eu-west-1"
}
```

**Response (200 OK):**
```json
{
  "status": "connected",
  "clusters_found": 20,
  "message": "Connected to AWS Secrets Manager (eu-west-1), found 20 cluster secrets"
}
```

---

### GET /api/v1/config — Server Configuration

Returns non-sensitive server configuration. Does NOT expose tokens or secrets.
All repo paths and gitops settings are server-side config (Helm values / env vars), not read from a file in Git.

**Response (200 OK):**
```json
{
  "version": "1.0.0",
  "provider": {
    "type": "aws-sm",
    "region": "eu-west-1"
  },
  "git": {
    "provider": "github",
    "repo": "org/addons",
    "branch": "main"
  },
  "argocd": {
    "server": "https://argocd.example.com",
    "connected": true,
    "version": "2.13.1"
  },
  "repo_paths": {
    "cluster_values": "configuration/addons-clusters-values",
    "global_values": "configuration/addons-global-values",
    "charts": "charts/",
    "bootstrap": "bootstrap/"
  },
  "gitops": {
    "default_mode": "pr",
    "branch_prefix": "sharko/",
    "commit_prefix": "sharko:"
  }
}
```

---

## 6. CLI Command Mapping

Every CLI command is a thin HTTP client call to the Sharko API.

| CLI Command | Method | API Endpoint | Notes |
|---|---|---|---|
| `sharko login --server <url>` | POST | `/api/v1/auth/login` | Prompts for username/password, saves token to `~/.sharko/config` |
| `sharko version` | GET | `/api/v1/health` | Prints CLI version (ldflags) + server version from health response |
| `sharko init` | POST | `/api/v1/init` | Bootstrap the addons repo |
| `sharko add-cluster <name> [--addons a,b,c]` | POST | `/api/v1/clusters` | `--addons` maps to `addons` field |
| `sharko remove-cluster <name>` | DELETE | `/api/v1/clusters/{name}` | |
| `sharko update-cluster <name> --add-addon x --remove-addon y` | PATCH | `/api/v1/clusters/{name}` | Flags map to `addons` map |
| `sharko list-clusters` | GET | `/api/v1/clusters` | Formatted table output |
| `sharko add-addon <name> --chart --repo --version` | POST | `/api/v1/addons` | Flags map to request fields |
| `sharko remove-addon <name>` | DELETE | `/api/v1/addons/{name}` | |
| `sharko status` | GET | `/api/v1/fleet/status` | Formatted terminal output |

### CLI Auth Flow

```
$ sharko login --server https://sharko.internal.company.com
Username: admin
Password: ****
Logged in. Token saved to ~/.sharko/config
```

`~/.sharko/config` format:
```yaml
server: https://sharko.internal.company.com
token: abc123...
```

All subsequent commands read this file and send `Authorization: Bearer <token>`.

### CLI Output Format

Write commands show step-by-step progress:
```
$ sharko add-cluster prod-eu --addons monitoring,logging

Fetching credentials from AWS Secrets Manager...  done
Verifying cluster connectivity...                  done (v1.29.3, 12 nodes)
Registering in ArgoCD...                           done
Creating cluster values file...                    done
Committing to Git...                               done
Created PR #42: "sharko: add cluster prod-eu"

Cluster prod-eu is live.
ArgoCD will deploy monitoring, logging within ~3 minutes.
Run 'sharko status' to watch progress.
```

---

## 7. Failure Behavior Summary

| Operation | Step Fails | Behavior |
|---|---|---|
| Register cluster | Fetch credentials | Return 404/502. Nothing to clean up. |
| Register cluster | Verify connectivity | Return 502. Nothing to clean up. |
| Register cluster | Register in ArgoCD | Return 502. Nothing to clean up. |
| Register cluster | Create values file / Git commit | Return **207 partial success**. DO NOT deregister from ArgoCD. User decides: retry or `sharko remove-cluster`. |
| Deregister cluster | Remove from ArgoCD | Return 502. Values file untouched. |
| Deregister cluster | Delete values file / Git commit | Return 207 partial success. Cluster already removed from ArgoCD. |
| Update cluster | Update ArgoCD labels | Return 502. Git untouched. |
| Update cluster | Git commit | Return 207 partial success. ArgoCD labels already updated. |
| Add addon | Git commit | Return 502. Nothing to clean up. |
| Init repo | Git push | Return 502. Nothing to clean up. |
| Init repo | ArgoCD bootstrap | Return 207 partial success. Repo pushed but ArgoCD not bootstrapped. |

### Why No Auto-Rollback of ArgoCD State

When a cluster is registered in ArgoCD (step 4 of register), the ApplicationSet controller may immediately detect the new cluster and start deploying addons. If we auto-deregister the cluster because a later step (Git commit) failed, ArgoCD may cascade-delete the addons it just started deploying. This causes more damage than the original failure.

Partial success lets the user decide: retry the failed step, or explicitly clean up with `sharko remove-cluster`.

---

## 8. Future Considerations (Not in v1.0.0)

- **API keys** for non-interactive consumers (`sharko token create --name "backstage" --scope read,write`)
- **Async operations** for batch workflows (return 202 Accepted + job ID, poll for status)
- **Webhooks** for event notifications (cluster.registered, addon.drift, etc.)
- **Batch cluster registration** (`POST /api/v1/clusters/batch`)
