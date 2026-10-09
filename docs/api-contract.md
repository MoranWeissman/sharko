# Sharko API Contract

> This document defines every API endpoint, request/response shape, error code, and
> orchestration behavior. It is the single source of truth for v1.0.0 implementation.
> CLI commands, UI write features, and IDP integrations all derive from this contract.
>
> **Swagger UI** is available at `/swagger/index.html` for interactive API exploration.

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

**Two token types are accepted (in priority order):**
1. **API keys** — tokens created via `POST /api/v1/tokens`. Intended for non-interactive consumers (Backstage, Terraform, CI/CD). Expire after 90 days by default (1–365 configurable); renewable without changing the key value.
2. **Session tokens** — short-lived tokens returned by `POST /api/v1/auth/login`. Used by the CLI and UI. Expire after 24 hours, with no refresh — the user logs in again.

The auth middleware checks for an API key first; if not found, it falls back to session token validation.

**How to get a session token:**
```bash
POST /api/v1/auth/login
Content-Type: application/json

{"username": "admin", "password": "secret"}
```

Response: `{"token": "abc123...", "username": "admin", "role": "admin"}`

The CLI stores this token in `~/.sharko/config`. The UI stores it in sessionStorage.

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

### Write Operation Behavior

All write endpoints are **synchronous** — they return the final result once all steps complete (or when a step fails). There are no 202 Accepted responses. Git operations always create pull requests; the `GitResult` shape reflects this:

```json
{
  "pr_url": "https://github.com/org/repo/pull/42",
  "pr_id": 42,
  "branch": "sharko/register-cluster-prod-eu-a1b2c3d4",
  "merged": false
}
```

When `SHARKO_CONN_GITOPS_PR_AUTO_MERGE=true`, the PR is merged immediately after creation and `merged` will be `true`.

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

These endpoints are already implemented and working. Listed here for completeness.

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
| GET | `/api/v1/clusters/{name}/history` | Recent sync activity for a specific cluster |

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

**Response: `GET /api/v1/clusters/{name}/comparison`**

Includes ArgoCD connection state as of v1.16.0:

```json
{
  "cluster": { "name": "prod-eu" },
  "argocd_connection_status": "Failed",
  "argocd_connection_message": "ArgoCD cannot reach this cluster. Sharko does not repeat ArgoCD's own message here, because it quotes the credentials layer's words in full. Open the cluster in ArgoCD to read the connection error.",
  "addon_comparisons": [
    {
      "addon_name": "monitoring",
      "git_version": "56.6.2",
      "git_repo_url": "https://charts.example.com/monitoring",
      "argocd_deployed_version": "56.6.2",
      "argocd_sync_status": "Synced",
      "argocd_health_status": "Healthy",
      "argocd_source_repo_url": "https://github.example/org/addons.git",
      "argocd_operation_message": "",
      "issues": []
    }
  ]
}
```

`argocd_connection_status` values: `Successful`, `Failed`, `Unknown`, or `unrecognised`
when ArgoCD reports a word Sharko does not know. The UI surfaces a failure as a single
consolidated error banner at the top of the cluster detail page.

**These fields never carry ArgoCD's own text (B7, B8).** Three of them used to, on this
ordinary 200 response with nothing having gone wrong:

- `argocd_source_repo_url` and `git_repo_url` are repository addresses, and a repository
  address is routinely written with the access token inside its userinfo section
  (`https://x-access-token:<token>@host/org/repo.git`). Both now go through
  `credsafe.SafeRepoURL`, which removes the whole userinfo section, the query and the
  fragment. When it cannot parse the value it returns an empty string, and the field is
  empty rather than falling back to the original.
- `argocd_operation_message`, `issues[]` and `argocd_connection_message` used to be
  ArgoCD's `operationState.message` and its cluster `connectionMessage`, quoted whole.
  Those messages quote the repository ArgoCD was syncing from and quote the credentials
  layer's errors word for word, so they no longer travel at all. Each field now carries a
  fixed sentence Sharko wrote plus the facts `internal/credsafe` will vouch for: the sync
  phase, the sync and health status, and the repository address with the credential
  removed. ArgoCD's own text is not kept anywhere on the Sharko side any more — not in
the response and not in the server-side log either (changed by B9, 2026-08-20). The log
line names the operation, the endpoint and the status code, plus Sharko's own
classification of the failure; it never quotes ArgoCD's words, because those words quote
the repository address with its access token inside it. To read ArgoCD's full message,
open the application or the cluster in ArgoCD.

Do NOT write a client that parses these strings for an error code. They are prose for a
person, and the operator's next move is the same in every case: open the application or
the cluster in ArgoCD.

### ArgoCD's own text never reaches a response body (B10)

The rule above is not limited to the comparison endpoint. Every place Sharko copies a
value out of an ArgoCD object and onto a response follows the same two rules, and B10
finished applying them:

- **A status word travels only when Sharko knows it.** ArgoCD's sync status, health
  status, operation phase, cluster connection state, application condition type and
  ApplicationSet condition type are all closed sets in ArgoCD's own Go types. Sharko
  echoes the value when it is a member of the set and says `unrecognised` otherwise.
  `unrecognised` and an empty string mean different things: `unrecognised` is "ArgoCD said
  something Sharko does not know", empty is "ArgoCD said nothing".
- **Free-form prose never travels at all.** An operation message, a cluster connection
  message, an application condition message, an ApplicationSet condition message and a
  managed resource's health message are whatever ArgoCD, Helm, the Kubernetes API server
  or a Git transport wrote, quoted verbatim — and they routinely name the repository
  ArgoCD was reading, which is routinely written with an access token inside it. Each of
  these fields now carries a fixed sentence Sharko wrote plus the facts Sharko will vouch
  for. To read the original, open the object in ArgoCD.

The fields B10 changed, all of them on ordinary 200 responses with nothing having gone
wrong:

| Endpoint | Field | What it carries now |
|----------|-------|---------------------|
| `GET /observability/overview` | `addon_health[].clusters[].resources[].message` | a fixed sentence, plus sync/health/repo — was ArgoCD's health-assessment text |
| `GET /observability/overview` | `addon_health[].clusters[].resources[].status` / `.health` | allow-listed ArgoCD words |
| `GET /observability/overview` | `addon_health[].clusters[].health`, `addon_groups[].child_apps[].health` / `.sync_status` | allow-listed ArgoCD words |
| `GET /observability/overview` | `control_plane.health_summary` and `addon_groups[].health_counts` **keys** | allow-listed ArgoCD words — a map key is a response value too |
| `GET /dashboard/attention` | `error` | a fixed sentence, plus phase/sync/health/repo — was the ArgoCD application condition's own text |
| `GET /dashboard/attention` | `error_type`, `health`, `sync` | allow-listed ArgoCD words |
| `GET /addons/{name}` | `application_set.conditions[].message` / `.type` / `.status` | a fixed sentence; allow-listed words |
| `GET /addons/{name}`, `GET /addons/catalog` | `addon.repo_url` | the repository address with the credential removed |
| `GET /addons/{name}`, `GET /addons/catalog` | `applications[].sync_status` / `.health_status` | allow-listed ArgoCD words |
| `POST /clusters/{name}/resync` | `message` on a failed resync | one of the reconciler's canned sentences — was the raw Kubernetes/git error |

The resource is still fully identified in every case: kind, API group, namespace and name
are Kubernetes object names, and they are not touched.

B11 closed the last one of the same shape — the endpoints that hand back a catalog entry
as it is written in the repository. A catalog entry's `repoURL` is the operator's own
value, and it is routinely written with the access token inside it:

| Endpoint | Field | What it carries now |
|----------|-------|---------------------|
| `GET /addons/list` | `applicationsets[].repoURL` | the repository address with the credential removed |
| `GET /addons/list` | `applicationsets[].additionalSources[].repoURL` | the same, for every extra chart source |
| `GET /catalog/addons` | `addons[].repo_url` and `addons[].additional_sources[].repoURL` | the same |
| `GET /catalog/addons/{name}` | `repo_url` and `additional_sources[].repoURL` | the same |

The AI assistant's own `list_addons` and `search_addons` answers carry the stripped
address too — that text is sent to the AI provider and shown in the chat.

**The file on disk is untouched.** The stripping happens only on the copy that becomes a
response. `models.AddonCatalogEntry` and `config.AddonCatalogEntry` — the structs with
yaml tags, which Sharko reads `addons-catalog.yaml` / `catalog.yaml` into and writes back
out — keep the operator's credential exactly as it was written, and so does everything
that dials the chart repository to fetch an index or a values file. The response copy is a
separate, json-only type (`models.AddonCatalogEntryView`) or an explicit boundary call
(`catalog.CatalogAddon.SafeForResponse`).

B14 closed five more of the same shape, and two of them were on ordinary successful
replies. The two version endpoints hand back the chart repository address they were
asked about, and one of them also builds a plain-English sentence around it that the
browser prints exactly as it arrives:

| Endpoint | Field | What it carries now |
|----------|-------|---------------------|
| `GET /upgrade/{addonName}/versions` | `repo_url` | the repository address with the credential removed |
| `GET /marketplace/addons/{name}/versions` | `repo` | the same |
| `GET /marketplace/addons/{name}/versions` | `no_data_reason` | still a complete sentence saying why there is no version list, naming the repository with the credential removed. When Sharko cannot take the address apart at all, the sentence says "the chart repository" rather than leaving a gap. |

Three more places carried the same value off the machine and are closed with it:

- **The server log.** A chart repository address was written to the log in full, under an
  attribute key nobody would call sensitive (`repo`, and `url` in the advisory checker).
  The fix is in the log sink, not at those two lines: `internal/logging`'s redact handler
  now rewrites any string value that net/url parses as a URL **with a userinfo section**,
  because a userinfo section is by definition credential material. A URL written without
  one is left exactly as it was, so no other log line changes.
- **The AI assistant.** A failed tool used to be reported to the configured model provider
  as `Error executing <tool>: <the raw Go error>`, and those errors quote the address they
  failed on. The model is now told the step failed and roughly what kind of failure it was
  (Go type names only, via `credsafe.LogClass`), and never the error's own words.
- **Who may ask.** `GET /upgrade/{addonName}/versions` and
  `GET /marketplace/addons/{name}/versions` were registered with no role check at all.
  They now gate on `addon.list` and `catalog.freshness.read` respectively — the same
  viewer-and-above actions their sibling read endpoints already use. No caller loses
  access; what changes is that both routes are now named in the role table instead of
  being invisible to it.

Read endpoints as a class were outside every role-gate guard in the codebase, because the
guard's route inventory only ever collected POST, PUT, PATCH and DELETE.
`internal/api/authz_read_coverage_test.go` now walks every GET route: each one is either
gated on a named action, or listed as deliberately open to any authenticated caller. A new
ungated read fails the build until somebody decides which it is.

### Addons (Read)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/addons/list` | List all addons from Git config |
| GET | `/api/v1/addons/catalog` | Addon catalog with deployment stats across clusters |
| GET | `/api/v1/addons/version-matrix` | Version matrix: addon x cluster grid |
| GET | `/api/v1/addons/{name}/values` | Raw global values YAML for an addon |
| GET | `/api/v1/addons/{name}` | Addon detail: which clusters have it, version spread |
| GET | `/api/v1/addons/{name}/changelog` | Chart versions between two semver bounds |

### Dashboard

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/dashboard/stats` | Aggregated cluster statistics |
| GET | `/api/v1/dashboard/attention` | Items needing attention (degraded, out-of-sync) |
| GET | `/api/v1/dashboard/pull-requests` | Recent PRs from the Git provider |

**Response: `GET /api/v1/dashboard/stats`**

As of v1.16.0 the response includes bootstrap app health fields:

```json
{
  "total_clusters": 12,
  "connected": 10,
  "degraded": 1,
  "unknown": 1,
  "total_addons": 8,
  "synced": 94,
  "out_of_sync": 2,
  "bootstrap_app_health": "Healthy",
  "bootstrap_app_sync": "Synced"
}
```

`bootstrap_app_health` and `bootstrap_app_sync` reflect the ArgoCD health and sync status of the Sharko bootstrap ApplicationSet. When `bootstrap_app_health` is not `Healthy`, the Dashboard shows a banner with guidance. Possible values follow ArgoCD conventions: `Healthy`, `Degraded`, `Progressing`, `Missing`, `Unknown`.

### Observability

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/observability/overview` | Cluster health overview (from ArgoCD) |

### Upgrade Checker

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/upgrade/{addonName}/versions` | Available chart versions for an addon |
| GET | `/api/v1/upgrade/{addonName}/recommendations` | Smart upgrade recommendations (next patch, next minor, latest stable) |
| POST | `/api/v1/upgrade/check` | Check upgrade impact (values diff) |
| POST | `/api/v1/upgrade/ai-summary` | AI-generated upgrade summary |
| GET | `/api/v1/upgrade/ai-status` | AI summary generation status |

**Response: `GET /api/v1/upgrade/{addonName}/recommendations`**

Returns smart upgrade recommendations with security and breaking-change context.

```json
{
  "addon": "external-secrets",
  "current_version": "0.20.4",
  "next_patch": "",
  "next_minor": "",
  "latest_stable": "2.3.0",
  "cards": [
    {
      "label": "Latest in 1.x",
      "version": "1.5.2",
      "has_security": false,
      "has_breaking": true,
      "cross_major": true,
      "is_recommended": true
    },
    {
      "label": "Latest Stable",
      "version": "2.3.0",
      "has_security": true,
      "has_breaking": true,
      "cross_major": true,
      "advisory_summary": "2 security fixes"
    }
  ],
  "recommended": "1.5.2"
}
```

**Field notes:**
- `cards` — preferred field for new clients. Each card represents a candidate upgrade target with advisory context. Present when advisory data is available.
- `next_patch`, `next_minor`, `latest_stable` — legacy flat fields kept for backwards compatibility with v1.16 and earlier clients. A field is omitted when no matching version exists.
- `recommended` — version string of the card flagged `is_recommended: true`.
- `cards[].label` — one of: `"Patch"`, `"Latest in N.x"` (in-major or next-major stepping stone), `"Latest Stable"`.
- `cards[].has_security` — version path includes security fixes sourced from ArtifactHub.
- `cards[].has_breaking` / `cards[].cross_major` — version crosses a breaking-change or major version boundary.
- `cards[].advisory_summary` — human-readable summary of advisories (e.g., "2 security fixes").
- Advisory source: ArtifactHub API (primary), release-notes keyword fallback when ArtifactHub is unreachable.

**Next-major card (v1.17.1+):** When the current version is `N.x` and there are releases in `(N+1).x` that are not yet the overall latest stable, Sharko inserts a `"Latest in (N+1).x"` card between the in-major card and `"Latest Stable"`. This gives users a stepping-stone upgrade path (e.g., 0.x → 1.x → 2.x) instead of a single large jump. If `(N+1).x` IS the latest stable, only the `"Latest Stable"` card is shown — no duplicate.

Versions are scored and ranked; the highest-scoring candidate is flagged `is_recommended`.

**Error Responses:**

| Code | Condition |
|------|-----------|
| 404 | Addon not found in catalog |
| 502 | Helm repo index unreachable |

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

### Notifications

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/notifications` | List all notifications (upgrades, drift, security) |
| POST | `/api/v1/notifications/read-all` | Mark all notifications as read |

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

Register a new cluster: fetch credentials from the secrets provider, verify connectivity, register in ArgoCD, create values file, commit to Git as a PR.

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
| addons | map[string]bool | no | Addon labels to set. Defaults to `SHARKO_CONN_GITOPS_DEFAULT_ADDONS` if configured, otherwise none. |
| region | string | no | Cluster region metadata. |

**Orchestration Steps:**
1. Validate input (name format, no existing cluster with same name)
2. Fetch credentials from secrets provider (`provider.GetCredentials(name)`)
3. Verify Kubernetes connectivity (connect to cluster API, get version)
4. Register cluster in ArgoCD (create cluster secret with addon labels)
5. Generate cluster values file
6. Commit to Git (always as a PR; auto-merged when `SHARKO_CONN_GITOPS_PR_AUTO_MERGE=true`)
7. If addon secret definitions are configured, deliver secrets to the remote cluster

**Success Response (201 Created):**
```json
{
  "status": "success",
  "cluster": {
    "name": "prod-eu",
    "server": "https://ABCD.eu-west-1.eks.amazonaws.com",
    "server_version": "1.29.3",
    "addons": {"monitoring": true, "logging": true, "cert-manager": true}
  },
  "git": {
    "pr_url": "https://github.com/example/repo/pull/42",
    "pr_id": 42,
    "branch": "sharko/register-cluster-prod-eu-a1b2c3d4",
    "merged": false,
    "values_file": "configuration/addons-clusters-values/prod-eu.yaml"
  },
  "secrets_created": ["datadog-keys"]
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
- Steps 1-3 fail -> no cleanup needed (nothing was created)
- Step 4 fails -> no cleanup needed (ArgoCD registration didn't happen)
- Steps 5-7 fail -> **DO NOT auto-rollback ArgoCD registration.** Return partial success. ArgoCD may have already started deploying addons; deregistering could trigger cascade deletion.

---

### POST /api/v1/clusters/batch — Batch Register Clusters

Register multiple clusters in a single request. Clusters are processed sequentially (not in parallel) to preserve Git serialization. Maximum 10 clusters per batch.

**Request:**
```json
{
  "clusters": [
    {"name": "prod-eu", "addons": {"monitoring": true}, "region": "eu-west-1"},
    {"name": "prod-us", "addons": {"monitoring": true}, "region": "us-east-1"}
  ]
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| clusters | array | yes | List of cluster registration requests. Max 10. |
| clusters[].name | string | yes | Cluster name. Alphanumeric + hyphens. |
| clusters[].addons | map[string]bool | no | Addon labels. |
| clusters[].region | string | no | Region metadata. |

**Success Response (200 OK):**
```json
{
  "total": 2,
  "succeeded": 2,
  "failed": 0,
  "partial": 0,
  "outcome": {
    "total": 2,
    "completed": 2,
    "partly_completed": 0,
    "failed": 0,
    "unrecognized": 0
  },
  "results": [
    {
      "status": "success",
      "cluster": {"name": "prod-eu", "server": "https://..."},
      "git": {"pr_url": "https://github.com/...", "pr_id": 42, "merged": false}
    },
    {
      "status": "success",
      "cluster": {"name": "prod-us", "server": "https://..."},
      "git": {"pr_url": "https://github.com/...", "pr_id": 43, "merged": false}
    }
  ]
}
```

**Partial Success Response (207 Multi-Status):**
When one or more clusters do not fully succeed, the response has HTTP 207 and `failed > 0`.

> **Read the body. The HTTP status is not the answer.**
>
> The status code says whether Sharko accepted and processed the batch request. It does not
> say what happened to the individual clusters. **HTTP 200 does not mean every cluster was
> registered.** Read `outcome` and treat anything other than `completed == total` as work
> that did not finish.

**Reading `outcome` — the accurate counts:**

| Field | Meaning |
|-------|---------|
| `outcome.total` | How many clusters came back. |
| `outcome.completed` | How many finished every step. |
| `outcome.partly_completed` | How many stopped part-way. **Nothing is rolled back**, so real changes may already be in Git and on the cluster. These need looking at. |
| `outcome.failed` | How many failed outright — nothing landed for them at all. |
| `outcome.unrecognized` | How many came back with a status this server does not know. Normally 0. Never counted as a success. |

The four buckets always add up to `outcome.total`.

**The older top-level counters — unchanged, and they mean something different:**

| Field | Meaning |
|-------|---------|
| `total` | How many clusters were in the request. |
| `succeeded` | How many finished every step. Same number as `outcome.completed`. |
| `failed` | How many did NOT finish every step — this counts hard failures **and** partials, and it is what the 207 status is derived from. `succeeded + failed` always equals `total`. It is **not** the same number as `outcome.failed`. |
| `partial` | How many of `failed` were partials. A subset of `failed`, not a third bucket. Same number as `outcome.partly_completed`. |

A cluster's own `results[].status` says which it was: `success`, `partial`, or `failed`.

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Invalid request body, missing cluster name, or batch size > 10 |
| 502 | ArgoCD or Git unreachable |

---

### GET /api/v1/clusters/available — Discover Available Clusters

List clusters available in the configured secrets provider that have not yet been registered.

**Response (200 OK):**
```json
{
  "clusters": [
    {"name": "staging-eu", "region": "eu-west-1"},
    {"name": "dev-us", "region": "us-east-1"}
  ]
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 501 | Secrets provider not configured |
| 502 | Secrets provider unreachable |

---

### POST /api/v1/clusters/adopt — Adopt Existing Clusters

Adopt clusters that already have ArgoCD cluster secrets but are not managed by Sharko. Runs Stage 1 connectivity verification, creates values files, adds to `managed-clusters.yaml`, and creates PRs.

**Request:**
```json
{
  "clusters": ["prod-eu", "staging-us"],
  "auto_merge": false,
  "dry_run": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| clusters | array[string] | yes | Cluster names to adopt. Must exist in ArgoCD. |
| auto_merge | bool | no | Override per-request: merge PRs immediately. |
| dry_run | bool | no | Preview what would happen without making changes. |

**Success Response (200 OK):**
```json
{
  "outcome": {
    "total": 1,
    "completed": 1,
    "partly_completed": 0,
    "failed": 0,
    "unrecognized": 0
  },
  "results": [
    {
      "name": "prod-eu",
      "status": "success",
      "verification": {"success": true, "stage": "stage1", "duration_ms": 1200},
      "git": {
        "pr_url": "https://github.com/example/repo/pull/42",
        "pr_id": 42,
        "branch": "sharko/adopt-cluster-prod-eu-a1b2c3d4",
        "merged": false
      }
    }
  ]
}
```

> **Read the body. The HTTP status is not the answer.**
>
> This endpoint answers **200 even when every cluster failed**, and 200 when every cluster
> stopped part-way. 207 appears only when at least one cluster failed outright **and** at
> least one did not. So `200` here means "Sharko processed the request", never "every cluster
> was adopted". Read `outcome` and treat anything other than `completed == total` as work
> that did not finish.
>
> That this differs from `POST /api/v1/clusters/batch`, which answers 207 for any cluster
> that did not fully succeed, is a known inconsistency. Both endpoints are stable, so
> changing either status rule is a major version bump; it is recorded as a compatibility
> item for the next major version rather than fixed here.

**Reading `outcome` — the accurate counts:**

| Field | Meaning |
|-------|---------|
| `outcome.total` | How many clusters came back. |
| `outcome.completed` | How many were fully adopted. |
| `outcome.partly_completed` | How many stopped part-way. **Nothing is rolled back** — the pull request may have merged and the ArgoCD connection may already have been handed over, so real changes may be out there. These need looking at. |
| `outcome.failed` | How many failed outright — nothing landed for them at all. |
| `outcome.unrecognized` | How many came back with a status this server does not know. Normally 0. Never counted as a success. |

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Empty clusters array |
| 404 | Cluster not found in ArgoCD |
| 409 | Cluster managed by another tool (non-sharko managed-by label) |
| 502 | Connectivity verification failed |

---

### POST /api/v1/clusters/{name}/unadopt — Un-adopt a Cluster

Reverse a previous adoption. Removes Sharko labels from the ArgoCD secret (keeps the secret), cleans up addon secrets, and creates a PR to remove from Git.

**Path Parameters:**
- `name` — cluster name

**Request:**
```json
{
  "yes": true,
  "dry_run": false
}
```

**Success Response (200 OK):**
```json
{
  "name": "prod-eu",
  "status": "success",
  "git": {
    "pr_url": "https://github.com/example/repo/pull/43",
    "pr_id": 43,
    "branch": "sharko/unadopt-cluster-prod-eu-a1b2c3d4",
    "merged": false
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing cluster name or not adopted (use remove-cluster instead) |
| 502 | ArgoCD or Git unreachable |

---

### POST /api/v1/clusters/{name}/diagnose — Diagnose Cluster

Run IAM and RBAC permission checks on a remote cluster. Returns diagnostic details and copy-paste-ready fix YAML.

**Path Parameters:**
- `name` — cluster name

**Success Response (200 OK):**
```json
{
  "identity": "arn:aws:iam::123456789012:role/SharkoRole",
  "role_assumption": "arn:aws:iam::123456789012:role/EKSReadRole",
  "namespace_access": [
    {"check": "list_namespaces", "passed": true},
    {"check": "create_secret", "passed": false, "error": "forbidden"}
  ],
  "suggested_fixes": [
    {"description": "Grant secret CRUD in sharko-test namespace", "yaml": "apiVersion: rbac.authorization.k8s.io/v1\n..."}
  ]
}
```

---

### DELETE /api/v1/clusters/{name}/addons/{addon} — Disable Addon on Cluster

Disable a specific addon on a single cluster with configurable cleanup.

**Path Parameters:**
- `name` — cluster name
- `addon` — addon name

**Request:**
```json
{
  "yes": true,
  "cleanup": "all"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| yes | bool | yes | Confirmation required. |
| cleanup | string | no | Cleanup scope: `all` (default), `labels`, `none`. |
| dry_run | bool | no | Preview mode. |

**Success Response (200 OK):**
```json
{
  "cluster": "prod-eu",
  "addon": "monitoring",
  "status": "success",
  "cleanup": "all",
  "completed_steps": ["update_values_file", "update_managed_clusters", "delete_addon_secrets"],
  "git": {
    "pr_url": "https://github.com/example/repo/pull/44",
    "pr_id": 44,
    "merged": false
  }
}
```

---

### DELETE /api/v1/clusters/{name} — Remove a Cluster

Remove a cluster with configurable cleanup scope.

**Path Parameters:**
- `name` — cluster name

**Request:**
```json
{
  "yes": true,
  "cleanup": "all",
  "dry_run": false
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| yes | bool | yes | Confirmation required. |
| cleanup | string | no | Cleanup scope: `all` (default), `git`, `none`. |
| dry_run | bool | no | Preview what would happen without making changes. |

**Cleanup scopes:**
- `all` — Remove from `managed-clusters.yaml`, delete values file via PR, delete addon secrets from remote cluster, delete ArgoCD cluster secret.
- `git` — Same Git changes, skip remote secret deletion and ArgoCD secret deletion.
- `none` — Only remove `managed-clusters.yaml` entry.

**Success Response (200 OK):**
```json
{
  "name": "prod-eu",
  "status": "success",
  "cleanup": "all",
  "completed_steps": ["remove_managed_clusters_entry", "git_commit", "delete_values_file", "delete_remote_secrets", "delete_argocd_cluster"],
  "git": {
    "pr_url": "https://github.com/example/repo/pull/42",
    "pr_id": 42,
    "branch": "sharko/remove-cluster-prod-eu-a1b2c3d4",
    "merged": false
  }
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing confirmation (`yes: true`) or invalid cleanup scope |
| 502 | ArgoCD or Git unreachable |

**Warning:** With `cleanup=all`, deregistering a cluster from ArgoCD will cause ArgoCD to stop managing addons on that cluster. Depending on ArgoCD's cascade delete policy, this MAY delete the addon resources from the cluster.

---

### PATCH /api/v1/clusters/{name} — Update Cluster Addon Labels

Update which addons are enabled/disabled on a cluster.

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
3. Update cluster values file in Git (enable/disable addon sections, as a PR)

**Success Response (200 OK):**
```json
{
  "status": "success",
  "cluster": "prod-eu",
  "updated_addons": {"istio": true, "keda": false},
  "git": {
    "pr_url": "https://github.com/example/repo/pull/42",
    "pr_id": 42,
    "branch": "sharko/update-cluster-prod-eu-a1b2c3d4",
    "merged": false,
    "values_file": "configuration/addons-clusters-values/prod-eu.yaml"
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

### GET /api/v1/clusters/{name}/secrets — List Managed Secrets

List the Sharko-managed Kubernetes Secrets on a remote cluster.

**Path Parameters:**
- `name` — cluster name

**Response (200 OK):**
```json
{
  "cluster": "prod-eu",
  "secrets": [
    {
      "name": "datadog-keys",
      "namespace": "datadog",
      "managed_by": "sharko"
    }
  ]
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing cluster name |
| 501 | Secrets provider not configured |
| 502 | Unable to connect to secrets provider or remote cluster |

---

### POST /api/v1/clusters/{name}/secrets/refresh — Refresh Remote Secrets

Re-fetch secret values from the provider and upsert them as Kubernetes Secrets on the remote cluster. Applies all defined addon secret templates for addons that are enabled on this cluster.

**Path Parameters:**
- `name` — cluster name

**Success Response (200 OK):**
```json
{
  "cluster": "prod-eu",
  "secrets_refreshed": ["datadog-keys", "newrelic-license"]
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing cluster name |
| 501 | Secrets provider not configured |
| 502 | Unable to connect to secrets provider, ArgoCD, or remote cluster |

---

## 4. New Write API — Addon Operations

### POST /api/v1/addons — Add Addon to Catalog

Add a new addon to the addons catalog configuration.

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
1. Validate input (name format)
2. Add entry to `addons-catalog.yaml` in Git
3. Create global values file at `configuration/addons-global-values/{name}.yaml`
4. Commit to Git as a PR

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
    "pr_url": "https://github.com/example/repo/pull/42",
    "pr_id": 42,
    "branch": "sharko/add-addon-cert-manager-a1b2c3d4",
    "merged": false
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

**Requires `?confirm=true` query parameter.** Without it, returns 400 with a warning explaining the impact.

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

**Success Response (200 OK, with `?confirm=true`):**
```json
{
  "status": "success",
  "message": "Addon cert-manager removed from catalog",
  "warning": "ArgoCD will cascade-delete cert-manager from 3 clusters as the ApplicationSet entry is removed.",
  "affected_clusters": ["prod-eu", "prod-us", "staging"],
  "git": {
    "pr_url": "https://github.com/example/repo/pull/42",
    "pr_id": 42,
    "branch": "sharko/remove-addon-cert-manager-a1b2c3d4",
    "merged": false
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

### POST /api/v1/addons/{name}/upgrade — Upgrade an Addon

Upgrade an addon version globally (updates `addons-catalog.yaml`) or per-cluster (updates the cluster values file). Creates a PR in both cases.

**Path Parameters:**
- `name` — addon name

**Request:**
```json
{
  "version": "1.15.0",
  "cluster": "prod-eu"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| version | string | yes | Target version to upgrade to. |
| cluster | string | no | If provided, upgrades this cluster only. If omitted, upgrades globally in the catalog. |

**Success Response (200 OK):**
```json
{
  "pr_url": "https://github.com/example/repo/pull/43",
  "pr_id": 43,
  "branch": "sharko/upgrade-cert-manager-1.15.0-a1b2c3d4",
  "merged": false
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing version or addon name |
| 502 | ArgoCD or Git unreachable |

---

### POST /api/v1/addons/upgrade-batch — Batch Upgrade Addons

Upgrade multiple addons in a single PR. All upgrades are applied to the global catalog.

**Request:**
```json
{
  "upgrades": {
    "cert-manager": "1.15.0",
    "metrics-server": "0.7.1"
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| upgrades | map[string]string | yes | Map of addon name -> target version. At least one required. |

**Success Response (200 OK):**
```json
{
  "pr_url": "https://github.com/example/repo/pull/44",
  "pr_id": 44,
  "branch": "sharko/upgrade-batch-a1b2c3d4",
  "merged": false
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Empty upgrades map or invalid request body |
| 502 | ArgoCD or Git unreachable |

---

## 5. New System API

### Addon Secrets — the definition endpoints are gone

As of v4.0.0, there is no `POST`, `GET`, or `DELETE /api/v1/addon-secrets`
any more. This was a security fix (task #152): those endpoints let a
caller define, in memory, where a secret should come from and which
cluster it should go to — no Git, no review, no trace. A stolen admin
token could use that to redirect a real production secret to a weaker
cluster.

Addon secret definitions now live **only in the Git catalog** — the
`secrets:` (or `push:`) block on a catalog entry. To change what secret
goes where, edit the catalog and open a PR, the same way you'd change
anything else Sharko manages.

`POST /api/v1/clusters/{name}/secrets/refresh` still exists, and still
re-delivers secrets on demand. It takes no request body — only the
cluster name in the path and an optional `?addon=` query string to
narrow the refresh to one addon. Everything it pushes comes straight
from the Git catalog, the same source the scheduled reconciler reads
every few minutes.

---

### POST /api/v1/tokens — Create an API Key

Create an API key for non-interactive consumers. API keys are stored hashed and the plaintext is only returned once at creation time.

Every key gets an expiry date. The default window is **90 days**; `expires_in_days` can set anything from **1 to 365** days.

**Request:**
```json
{
  "name": "backstage",
  "role": "operator",
  "expires_in_days": 30
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| name | string | yes | Unique name for the API key. |
| role | string | no | Role to assign. Defaults to `viewer`. |
| expires_in_days | int | no | How long the key stays usable, 1–365. Defaults to 90. |

**Success Response (201 Created):**
```json
{
  "name": "backstage",
  "token": "sharko_abc123...",
  "role": "operator",
  "expires_at": "2026-08-14T10:00:00Z"
}
```

**Important:** The `token` value is only returned once at creation time. Store it securely. There is no way to retrieve it later.

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing name, token with this name already exists, or expires_in_days outside 1–365 |
| 401 | Unauthorized |
| 403 | Role below operator |

---

### GET /api/v1/tokens — List API Keys

List all API keys. Token values and hashes are never returned — only names, roles, dates, and status.

**Response (200 OK):**
```json
[
  {
    "name": "backstage",
    "role": "operator",
    "created_at": "2026-01-15T10:00:00Z",
    "expires_at": "2026-04-15T10:00:00Z",
    "status": "active",
    "expired": false,
    "expiring_soon": false
  },
  {
    "name": "terraform-ci",
    "role": "admin",
    "created_at": "2025-02-01T08:30:00Z",
    "expires_at": null,
    "status": "legacy-no-expiry",
    "expired": false,
    "expiring_soon": false
  }
]
```

| Field | Description |
|-------|-------------|
| expires_at | Expiry date, or `null` for a key stored before expiry dates existed. |
| status | `active`, `expired`, or `legacy-no-expiry`. |
| expiring_soon | True when the key expires within 14 days. |

Keys with `status: legacy-no-expiry` keep working — Sharko does not force-expire them. Recreate them so they pick up an expiry date.

---

### POST /api/v1/tokens/{name}/renew — Renew an API Key

Push a key's expiry out by a fresh window, counted from now. **The key value does not change**, so consumers already holding it keep working. Renewing an expired key makes it usable again.

**Path Parameters:**
- `name` — token name

**Request (optional — an empty body means the default window):**
```json
{
  "expires_in_days": 180
}
```

**Success Response (200 OK):** the same shape as one entry from `GET /api/v1/tokens`, with the new `expires_at`. No secret is returned.

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | expires_in_days outside 1–365 |
| 401 | Unauthorized |
| 403 | Role below operator |
| 404 | No token by that name |

---

### DELETE /api/v1/tokens/{name} — Revoke an API Key

Revoke an API key by name. The key is immediately invalidated — no grace period.

**Path Parameters:**
- `name` — token name

**Success Response (200 OK):**
```json
{
  "message": "token revoked"
}
```

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Missing token name |
| 401 | Unauthorized |
| 403 | Role below admin |
| 404 | No token by that name |

---

### Refusing an API Key

A request carrying an **expired** key gets a `401` naming the key — the caller already holds it, so naming it reveals nothing:

```json
{"error": "API token \"backstage\" expired on 2026-04-15 — renew it or create a new one, then try again"}
```

A key that was **revoked, mistyped, or never existed** gets the generic refusal, so nobody can probe for real key names:

```json
{"error": "unauthorized"}
```

---

### POST /api/v1/init — Initialize Addons Repo

Create the addons repo structure, push to Git, and optionally bootstrap the root-app into ArgoCD.

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
3. Generate repo structure from embedded `templates/starter/`
4. Commit and push to Git (as a PR)
5. If `bootstrap_argocd`: create ArgoCD repo connection + apply root-app

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
    ],
    "pr_url": "https://github.com/org/addons/pull/1",
    "pr_id": 1,
    "merged": false
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
| 409 | Repo already initialized |
| 502 | Git or ArgoCD unreachable |

---

### GET /api/v1/fleet/status — Cluster Status Overview

Aggregated cluster health.

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
    "pr_auto_merge": false,
    "branch_prefix": "sharko/",
    "commit_prefix": "sharko:"
  }
}
```

Note: The `gitops` section reports `pr_auto_merge` (not a `mode` field). Git operations always create PRs; `pr_auto_merge` controls whether they are merged immediately.

---

### GET /api/v1/metrics — Prometheus Metrics

Returns Prometheus-format metrics. No auth required in default configuration.

44 metric families: 32 registered in `internal/metrics/metrics.go`, plus 12 built from the four SLO surfaces. Dynamic path segments are normalized to prevent cardinality explosion.

Every metric name, type, label and meaning is listed on one page: [Reference — Metrics, Alerts, and the Grafana Dashboard](site/operator/metrics.md). This page deliberately keeps no copy of that list.

---

## 5b. PR Tracking API

### GET /api/v1/prs — List Tracked PRs

List all PRs tracked by Sharko, with optional filters.

**Query Parameters:**
- `status` — Filter by status (`open`, `merged`, `closed`)
- `cluster` — Filter by cluster name
- `user` — Filter by user

**Response (200 OK):**
```json
{
  "prs": [
    {
      "pr_id": 42,
      "pr_url": "https://github.com/example/repo/pull/42",
      "pr_branch": "sharko/register-cluster-prod-eu-a1b2c3d4",
      "pr_title": "sharko: add cluster prod-eu",
      "pr_base": "main",
      "cluster": "prod-eu",
      "operation": "register",
      "user": "admin",
      "source": "cli",
      "created_at": "2026-04-10T10:00:00Z",
      "last_status": "open",
      "last_polled_at": "2026-04-10T10:05:00Z"
    }
  ]
}
```

---

### GET /api/v1/prs/{id} — Get Tracked PR

Get details for a single tracked PR.

**Path Parameters:**
- `id` — PR number

**Response (200 OK):** Same shape as a single element from the list response.

**Error Responses:**
| Code | Condition |
|------|-----------|
| 404 | PR not tracked |

---

### POST /api/v1/prs/{id}/refresh — Refresh PR Status

Force an immediate poll of the Git provider for the PR's current status.

**Path Parameters:**
- `id` — PR number

**Response (200 OK):** Updated PR info with latest status.

---

### DELETE /api/v1/prs/{id} — Stop Tracking a PR

Remove a PR from tracking. Does not affect the PR in the Git provider.

**Path Parameters:**
- `id` — PR number

**Response (200 OK):**
```json
{
  "message": "PR #42 removed from tracking"
}
```

---

## 5c. Audit API

**Audit coverage**: every mutating endpoint (POST/PUT/PATCH/DELETE) emits an audit entry automatically via `auditMiddleware`. The `Audit Log` panel in Settings → Audit shows all activity, filterable by user/action/source/result.

Read endpoints (GET) are NOT audited by default. The middleware skips GETs to keep the audit log focused on state changes.

Each entry contains: timestamp, level, event name, user, action (HTTP verb), resource (e.g., `cluster:prod-eu` or `addon:cert-manager`), source (`ui`/`cli`/`api`/`reconciler`), result (`success`/`failure`), duration_ms, and optional detail.

### Event Taxonomy

| Domain | Events |
|--------|--------|
| auth | `login`, `logout`, `login_failed`, `password_changed` |
| users | `user_created`, `user_updated`, `user_deleted`, `password_reset` |
| tokens | `token_created`, `token_renewed`, `token_revoked` |
| clusters | `cluster_registered`, `cluster_deregistered`, `cluster_updated`, `cluster_adopted`, `cluster_unadopted`, `cluster_tested`, `cluster_diagnosed`, `cluster_credentials_refreshed`, `cluster_secret_synced`, `cluster_discovery_run` |
| addons | `addon_added`, `addon_removed`, `addon_configured`, `addon_upgraded`, `addon_enabled_on_cluster`, `addon_disabled_on_cluster` |
| secrets | `addon_secret_set`, `addon_secret_deleted` |
| connections | `connection_created`, `connection_updated`, `connection_deleted`, `active_connection_changed`, `connection_tested` |
| settings | `ai_config_updated`, `provider_tested`, `dashboards_saved` |
| prs | `pr_refreshed`, `pr_deleted` |
| system | `init_run`, `webhook_received`, `reconcile_triggered`, `operation_cancelled` |
| upgrades | `upgrade_analyzed` |

### GET /api/v1/audit — Query Audit Log

List audit events with optional filters. Default limit is 50 entries, newest first.

**Query Parameters:**
- `user` — Filter by username
- `action` — Filter by action (register, remove, update, test, adopt, etc.)
- `source` — Filter by source (ui, cli, api, reconciler, webhook, prtracker)
- `result` — Filter by result (success, failure, partial)
- `cluster` — Filter by cluster name (matches in the resource field)
- `since` — ISO 8601 timestamp (only events after this time)
- `limit` — Max entries to return (default 50)

**Response (200 OK):**
```json
{
  "entries": [
    {
      "id": "abc123",
      "timestamp": "2026-04-10T10:00:00Z",
      "level": "info",
      "event": "cluster_registered",
      "user": "admin",
      "action": "register",
      "resource": "cluster:prod-eu",
      "source": "cli",
      "result": "success",
      "changes": "applied",
      "duration_ms": 3200,
      "detail": "3 addons enabled"
    }
  ]
}
```

**`result` and `changes` — two different questions.**

`result` is how the operation went; `changes` is whether anything was actually written.

| `changes` | Meaning |
|-----------|---------|
| `not_applicable` | Read-only. Nothing was going to change. |
| `none` | The action ran and deliberately wrote nothing. The one case where "no changes made" is true. |
| `applied` | Something really was written. |
| `may_be_applied` | The action stopped part-way with nothing rolled back, so changes may be out there and Sharko cannot say for certain either way. |
| *(absent)* | Nobody said. Render nothing — never "no changes". |

For a fan-out operation (`cluster_registered`, `cluster_adopted`), the two fields together are:

| What happened | `result` | `changes` |
|---------------|----------|-----------|
| Every cluster finished every step | `success` | `applied` |
| Some finished, some failed outright, none part-way | `partial` | `applied` |
| At least one cluster stopped part-way | `partial` | `may_be_applied` |
| Nothing landed for any cluster | `failure` | `none` |

Work that stopped part-way is never recorded as fully registered or adopted, and never as
"nothing changed".

---

### GET /api/v1/audit/stream — Real-Time Audit Stream (SSE)

Subscribe to audit events in real time via Server-Sent Events. Each event is delivered as a JSON payload. The stream uses a buffered channel (capacity 64); slow subscribers may miss events.

**Response:** `text/event-stream` with JSON-encoded audit entries.

---

## 6. CLI Command Mapping

Every CLI command is a thin HTTP client call to the Sharko API.

| CLI Command | Method | API Endpoint | Notes |
|---|---|---|---|
| `sharko login --server <url>` | POST | `/api/v1/auth/login` | Prompts for username/password, saves token to `~/.sharko/config` |
| `sharko version` | GET | `/api/v1/health` | Prints CLI version (ldflags) + server version from health response |
| `sharko init` | POST | `/api/v1/init` | Bootstrap the addons repo |
| `sharko add-cluster <name> [--addons a,b,c]` | POST | `/api/v1/clusters` | `--addons` maps to `addons` field |
| `sharko add-clusters <n1,n2,...>` | POST | `/api/v1/clusters/batch` | Comma-separated cluster names |
| `sharko remove-cluster <name>` | DELETE | `/api/v1/clusters/{name}` | |
| `sharko update-cluster <name> --add-addon x --remove-addon y` | PATCH | `/api/v1/clusters/{name}` | Flags map to `addons` map |
| `sharko list-clusters` | GET | `/api/v1/clusters` | Formatted table output |
| `sharko add-addon <name> --chart --repo --version` | POST | `/api/v1/addons` | Flags map to request fields |
| `sharko remove-addon <name>` | DELETE | `/api/v1/addons/{name}` | |
| `sharko upgrade-addon <name> --version <ver> [--cluster <c>]` | POST | `/api/v1/addons/{name}/upgrade` | `--cluster` for per-cluster upgrade |
| `sharko upgrade-addons <addon=ver,...>` | POST | `/api/v1/addons/upgrade-batch` | Comma-separated `addon=version` pairs |
| `sharko token create [--name <n> --role <r> --expires-in-days <d>]` | POST | `/api/v1/tokens` | Prints token once, plus its expiry date |
| `sharko token list` | GET | `/api/v1/tokens` | Formatted table with status + expiry |
| `sharko token renew <name> [--expires-in-days <d>]` | POST | `/api/v1/tokens/{name}/renew` | Token value unchanged |
| `sharko token revoke <name>` | DELETE | `/api/v1/tokens/{name}` | |
| `sharko status` | GET | `/api/v1/fleet/status` | Formatted terminal output |
| `sharko pr list [--status --cluster --user]` | GET | `/api/v1/prs` | Filter flags map to query params |
| `sharko pr status <id>` | GET | `/api/v1/prs/{id}` | Detailed PR info |
| `sharko pr refresh <id>` | POST | `/api/v1/prs/{id}/refresh` | Force poll Git provider |
| `sharko pr wait <id> [--timeout 10m]` | POST | `/api/v1/prs/{id}/refresh` | Polls every 5s until merged/closed/timeout |

### CLI exit codes for the multi-cluster commands

`sharko add-clusters` and `sharko adopt` act on several clusters in one call and get one
answer back per cluster. For both:

- **Exit 0 only when every requested cluster completed fully.**
- **Exit non-zero when any cluster failed OR completed only part-way.** A cluster that
  stopped part-way was not rolled back, so real changes may already be in Git and on the
  cluster — the command says so and points at the per-cluster lines.
- The summary always prints all three counts: fully completed, partly completed, failed.
- Neither command prints "done" for a run that did not complete. Both used to: they printed
  it the moment the HTTP call came back, and returned 0 even when every cluster had failed.
- `sharko adopt --dry-run` previews and adopts nothing, so a successful preview exits 0.

The exit code is decided from the response body, never from the HTTP status — `POST
/api/v1/clusters/adopt` answers 200 even when every cluster failed.

### CLI Auth Flow

```
$ sharko login --server https://sharko.internal.example.com
Username: admin
Password: ****
Logged in. Token saved to ~/.sharko/config
```

`~/.sharko/config` format:
```yaml
server: https://sharko.internal.example.com
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
| Batch register | Any individual cluster | Return 207 with per-cluster results. Continue with remaining clusters. |
| Deregister cluster | Remove from ArgoCD | Return 502. Values file untouched. |
| Deregister cluster | Delete values file / Git commit | Return 207 partial success. Cluster already removed from ArgoCD. |
| Update cluster | Update ArgoCD labels | Return 502. Git untouched. |
| Update cluster | Git commit | Return 207 partial success. ArgoCD labels already updated. |
| Add addon | Git commit | Return 502. Nothing to clean up. |
| Upgrade addon | Git commit | Return 502. Nothing changed. |
| Init repo | Git push | Return 502. Nothing to clean up. |
| Init repo | ArgoCD bootstrap | Return 207 partial success. Repo pushed but ArgoCD not bootstrapped. |

### Why No Auto-Rollback of ArgoCD State

When a cluster is registered in ArgoCD (step 4 of register), the ApplicationSet controller may immediately detect the new cluster and start deploying addons. If we auto-deregister the cluster because a later step (Git commit) failed, ArgoCD may cascade-delete the addons it just started deploying. This causes more damage than the original failure.

Partial success lets the user decide: retry the failed step, or explicitly clean up with `sharko remove-cluster`.

---

## 8. History, Changelog & Notifications API

### GET /api/v1/clusters/{name}/history — Cluster Sync History

Returns recent ArgoCD sync activity filtered to a specific cluster.

**Path Parameters:**
- `name` — cluster name

**Response (200 OK):**
```json
{
  "cluster_name": "prod-eu",
  "history": [
    {
      "timestamp": "2026-04-05T10:30:00Z",
      "duration": "1m2s",
      "duration_secs": 62.0,
      "app_name": "prod-eu-monitoring",
      "addon_name": "monitoring",
      "cluster_name": "prod-eu",
      "revision": "abc1234",
      "status": "Succeeded"
    }
  ]
}
```

`history` is an empty array when no sync events exist for the cluster. Each entry reflects a single ArgoCD application sync operation.

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Cluster name missing |
| 503 | ArgoCD connection unavailable |
| 500 | Observability service error |

---

### GET /api/v1/addons/{name}/changelog — Addon Version Changelog

Returns chart versions from the Helm repo between two semver bounds. Useful for reviewing what changed before upgrading an addon.

**Path Parameters:**
- `name` — addon name (must exist in the addons catalog)

**Query Parameters:**
- `from` — lower bound version (exclusive). Defaults to the addon's current catalog version if omitted.
- `to` — upper bound version (inclusive). If omitted, all versions above `from` are returned.

Both parameters accept semver strings with or without a leading `v` (e.g. `1.14.0` or `v1.14.0`).

**Response (200 OK):**
```json
{
  "addon_name": "cert-manager",
  "current_version": "1.14.0",
  "target_version": "1.15.0",
  "total_versions_between": 2,
  "versions": [
    {
      "version": "1.15.0",
      "app_version": "1.15.0",
      "created": "2026-03-15T00:00:00Z",
      "description": "Bug fixes and performance improvements"
    },
    {
      "version": "1.14.5",
      "app_version": "1.14.5",
      "created": "2026-02-01T00:00:00Z",
      "description": "Security patch"
    }
  ]
}
```

`versions` is an empty array when no versions fall in the specified range. `app_version`, `created`, and `description` are omitted when not present in the Helm repo index.

**Error Responses:**
| Code | Condition |
|------|-----------|
| 400 | Invalid semver for `from` or `to` |
| 404 | Addon not found in catalog |
| 503 | Git provider unavailable |
| 500 | Helm repo fetch or catalog parse error |

---

### GET /api/v1/notifications — List Notifications

Returns every notification, newest first. Notification types: `upgrade`, `security`, `drift`, `connection`.

**Response (200 OK):**
```json
{
  "notifications": [
    {
      "id": "upgrade-cert-manager-1.15.0",
      "code": "addon_upgrade_available",
      "type": "upgrade",
      "title": "cert-manager 1.15.0 available",
      "description": "Upgrade from 1.14.5 to 1.15.0",
      "timestamp": "2026-04-05T08:00:00Z",
      "read": false,
      "schema": 3
    }
  ],
  "unread_count": 1
}
```

Returns `{"notifications": [], "unread_count": 0}` when no notifications exist.

`code` is the stable identifier and the only field a client may branch on.
`reason` appears on connection alerts and is a closed enum. `schema` is the
record shape version.

#### Every word in a notification is written by the server

`id`, `type`, `title` and `description` are all rendered server-side from the
`code` plus a small set of checked identifiers — an addon name, a cluster name,
a chart version. No caller-supplied prose, no raw provider or Kubernetes error
text and no credential-bearing value can reach any of them, on any code, and
there is no flag or classification that turns that off. A code the server does
not know, or an identifier that is not the shape it claims to be, falls back to
a generic safe sentence rather than being interpolated.

The three addon alerts have distinct wording. They are not exceptions to that
rule — they are three declared codes with three server-owned templates:

| code | title | description |
|------|-------|-------------|
| `addon_upgrade_available` | `<addon> <version> available` | `Upgrade from <catalogVersion> to <version>` |
| `addon_major_update` | `Major update: <addon> <version>` | `Major version change from <catalogVersion> — review for security patches` |
| `addon_version_drift` | `Version drift: <addon> on <cluster>` | `Running <version>, catalog has <catalogVersion>` |

The five connection codes (`git_connection_broken`, `argocd_repo_broken`,
`argocd_auth_failed`, `argocd_unreachable`, `argocd_forbidden`) take a fixed
title from the same table and a description built from two catalog lookups —
the code picks the first sentence, the `reason` enum picks the second.

---

### POST /api/v1/notifications/read-all — Mark All Notifications Read

Marks every notification in the store as read.

**Request body:** none

**Response (200 OK):**
```json
{"status": "ok"}
```
