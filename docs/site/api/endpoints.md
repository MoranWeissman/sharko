# API Endpoints

Key endpoints reference. For full request/response schemas, use the interactive [Swagger UI](../api/overview.md#interactive-documentation) or see [docs/api-contract.md](https://github.com/MoranWeissman/sharko/blob/main/docs/api-contract.md).

## Read Endpoints

### Clusters

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/clusters` | List all registered clusters with health stats |
| `GET` | `/api/v1/clusters/{name}` | Cluster detail with addon status |
| `GET` | `/api/v1/clusters/available` | Discover available clusters from the secrets provider |

List endpoints support pagination via `?page=<n>&limit=<n>` query params (default: `limit=50`).

#### Filtering and Sorting

`GET /api/v1/clusters` and `GET /api/v1/addons/catalog` accept additional query params:

| Param | Example | Description |
|-------|---------|-------------|
| `?sort=<field>` | `?sort=name` | Sort by field. Prefix with `-` for descending: `?sort=-health` |
| `?filter=<pred>` | `?filter=env:prod` | Filter predicate. Multiple params are AND-joined |

Supported sort fields for clusters: `name`, `env`, `health`, `addon_count`.
Supported filter predicates for clusters: `env:<value>`, `health:<value>`, `addon:<name>`.

### Addons

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/addons/catalog` | Addon catalog with deployment stats |
| `GET` | `/api/v1/addons/catalog?ai_summary=true` | Catalog with AI-generated summaries included inline (requires `ai.enabled: true`) |
| `GET` | `/api/v1/addons/version-matrix` | Version matrix: addon × cluster grid |

### Fleet

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/fleet/status` | Cluster status overview |
| `GET` | `/api/v1/observability/overview` | ArgoCD health groups and sync activity |

### Notifications

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/notifications` | List all notifications (upgrade available, version drift, security advisories) |
| `POST` | `/api/v1/notifications/{id}/read` | Mark a notification as read |

### Tokens & Secrets

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/tokens` | List API keys (admin only) |
| `GET` | `/api/v1/clusters/{name}/secrets` | List managed secrets on a cluster |

### Audit Log

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/audit` | List audit log entries (admin only) — records all write operations with actor, timestamp, and result |
| `GET` | `/api/v1/audit?cluster=<name>` | Filter audit entries by cluster |
| `GET` | `/api/v1/audit?addon=<name>` | Filter audit entries by addon |
| `GET` | `/api/v1/audit?limit=<n>&before=<cursor>` | Paginate results (default `limit=100`) |

Audit log entries include: `id`, `timestamp`, `actor` (username or API key name), `action` (e.g., `register_cluster`, `upgrade_addon`), `target` (cluster or addon name), `result` (`success` / `failure`), and an optional `detail` string.

Sharko holds this activity history in memory only. It starts empty again after a restart, so it is not a durable record — the durable record of what changed is the Git/PR history in your repo. See [Activity history](../operator/audit-log.md) for what it keeps and for how long.

---

## Write Endpoints

All write endpoints require the `admin` role.

### Clusters

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/clusters` | Register a cluster |
| `POST` | `/api/v1/clusters/batch` | Batch register up to 10 clusters |
| `DELETE` | `/api/v1/clusters/{name}` | Deregister a cluster |
| `PATCH` | `/api/v1/clusters/{name}` | Update addon labels |
| `POST` | `/api/v1/clusters/{name}/refresh` | Refresh cluster credentials |
| `POST` | `/api/v1/clusters/{name}/secrets/refresh` | Deliver the Git-defined addon secrets to a cluster now (optionally `?addon=` for one addon; the addon must be defined in the Git catalog) |
| `POST` | `/api/v1/clusters/{name}/test` | Test cluster connectivity (returns `{"reachable": bool, "version": "..."}`) |
| `POST` | `/api/v1/clusters/adopt` | Adopt one or more discovered ArgoCD clusters into Sharko management (body: `{"clusters": [...]}`) |
| `POST` | `/api/v1/clusters/{name}/doctor` | Run the [connection doctor](../operator/connection-doctor.md) — five real-attempt checks with plain-English fixes |
| `POST` | `/api/v1/clusters/{name}/reconcile` | Trigger a manual cluster-secret reconcile ("Sync now") — returns `202`, read the result from `last_reconcile` on the next `GET`. This is a fleet-wide reconcile pass, not scoped to just the named cluster. |

### Addons

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/addons` | Add addon to catalog |
| `DELETE` | `/api/v1/addons/{name}?confirm=true` | Remove addon from catalog and all clusters |
| `POST` | `/api/v1/addons/{name}/upgrade` | Upgrade addon (global or per-cluster) |
| `POST` | `/api/v1/addons/upgrade-batch` | Upgrade multiple addons in one PR |
| `POST` | `/api/v1/upgrade/ai-summary` | Generate an AI summary of an addon's upgrade impact (body: `{"addon_name": "...", "target_version": "..."}`) |

### Addon Secrets

The `/addon-secrets` definition endpoints were removed in v4.0.0. Addon secret
definitions live only in the Git catalog (the `secrets:` block on a v3 catalog
entry, the `push:` block on a v4 one); the background addon-secret sync and
`POST /api/v1/clusters/{name}/secrets/refresh` both read from there.

### Tokens

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/tokens` | Create an API key |
| `POST` | `/api/v1/tokens/{name}/renew` | Give an existing API key a fresh window |
| `DELETE` | `/api/v1/tokens/{name}` | Revoke an API key |

### Initialization

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/init` | Initialize addons repo from templates (async — returns `operation_id`) |

### Operations

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/operations/{id}` | Get operation status and log lines |
| `POST` | `/api/v1/operations/{id}/heartbeat` | Keep-alive for an active operation session |

### Secrets

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/secrets/reconcile` | Trigger immediate secrets reconcile (all clusters or specific cluster) |
| `GET` | `/api/v1/secrets/status` | Addon-secret sync status per cluster (last run, hash result, errors) |

### Webhooks

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/webhooks/git` | Git push webhook. Every call must carry an HMAC-SHA256 signature matching the shared secret an operator configured; with no shared secret configured this endpoint refuses every call |

---

## Example Requests

### Register a Cluster

```bash
curl -X POST https://sharko.your-domain.com/api/v1/clusters \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "prod-eu",
    "addons": ["cert-manager", "monitoring"],
    "region": "eu-west-1",
    "env": "prod"
  }'
```

**Request fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Cluster name (used as the directory name in Git) |
| `addons` | string[] | No | Addons to enable on registration |
| `region` | string | No | AWS region (for `aws-sm` provider) |
| `env` | string | No | Environment label (`dev`, `staging`, `prod`, etc.) |
| `secret_path` | string | No | Override the path used to look up credentials in the secrets provider. Defaults to the cluster `name`. Use when the secret key differs from the cluster name (e.g., `"clusters/prod/prod-eu"`). |

When a cluster cannot be found at the expected path, the API returns 404 with a `suggestions` array of close matches found in the provider:

```json
{
  "error": "cluster not found",
  "suggestions": ["clusters/prod/prod-eu", "clusters/staging/prod-eu"]
}
```

### Upgrade an Addon

```bash
curl -X POST https://sharko.your-domain.com/api/v1/addons/cert-manager/upgrade \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "version": "1.15.0"
  }'
```

### Batch Upgrade

```bash
curl -X POST https://sharko.your-domain.com/api/v1/addons/upgrade-batch \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "upgrades": [
      {"name": "cert-manager", "version": "1.15.0"},
      {"name": "ingress-nginx", "version": "4.9.0"}
    ]
  }'
```

### API Keys (Tokens)

API keys are the machine door into Sharko: a CI job, a Terraform run, or an
internal tool holds one instead of a username and password.

#### How long a key lasts

**Every key you create expires after 90 days.** That is the default. Ask for a
different window with `expires_in_days` — anything from **1 to 365 days**.
There is no "never expires" option for new keys.

#### Create a key

```bash
curl -X POST https://sharko.your-domain.com/api/v1/tokens \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "ci-pipeline",
    "role": "viewer",
    "expires_in_days": 30
  }'
```

```json
{
  "name": "ci-pipeline",
  "token": "sharko_a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6",
  "role": "viewer",
  "expires_at": "2026-08-29T09:14:22Z"
}
```

The `token` value is shown **once**, right here. Store it immediately — Sharko
keeps only a bcrypt hash and cannot show it to you again.

#### List keys

```bash
curl https://sharko.your-domain.com/api/v1/tokens \
  -H "Authorization: Bearer <token>"
```

Each entry carries a `status`:

| `status` | What it means |
|----------|---------------|
| `active` | Has an expiry date that is still ahead. Works normally. |
| `expired` | The expiry date has passed. Every request with it is refused. |
| `legacy-no-expiry` | Stored before Sharko put expiry dates on keys, so it has none. It keeps working — we do not force it to expire. Create a replacement when you get the chance so it picks up an expiry. |

Keys within 14 days of their expiry also report `expiring_soon: true`, so the
UI can nudge you before anything breaks.

The list never contains the key value or its hash — only the name, role,
dates, and status.

#### Renew a key

Renewing pushes the expiry out by a fresh window, counted from now. **The key
value does not change**, so every pipeline already holding it keeps working —
nothing to redeploy.

```bash
curl -X POST https://sharko.your-domain.com/api/v1/tokens/ci-pipeline/renew \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"expires_in_days": 180}'
```

Leave the body out entirely to get the default 90 days. Renewing works on an
expired key too — it starts working again straight away.

#### Revoke a key

```bash
curl -X DELETE https://sharko.your-domain.com/api/v1/tokens/ci-pipeline \
  -H "Authorization: Bearer <token>"
```

Revoking is immediate. There is no grace period and no undo — make a new key
if you need one.

#### What a refused key looks like

An **expired** key gets a `401` that names it, because whoever sent it already
holds it:

```json
{"error": "API token \"ci-pipeline\" expired on 2026-08-29 — renew it or create a new one, then try again"}
```

A key that was **revoked, mistyped, or never existed** gets a flat `401`:

```json
{"error": "unauthorized"}
```

That difference is deliberate: someone guessing at key names learns nothing.

#### Who can do what

| Action | Minimum role |
|--------|--------------|
| List keys | viewer |
| Create a key | operator |
| Renew a key | operator |
| Revoke a key | admin |

### Poll an Operation

```bash
# Start init (returns operation_id):
curl -X POST https://sharko.your-domain.com/api/v1/init \
  -H "Authorization: Bearer <token>"
# Response: {"operation_id": "op_a1b2c3d4", "status": "running"}

# Poll until done:
curl https://sharko.your-domain.com/api/v1/operations/op_a1b2c3d4 \
  -H "Authorization: Bearer <token>"
# Response: {"id": "op_a1b2c3d4", "status": "succeeded", "log": [...]}

# Send heartbeat (required every 15s to keep session alive):
curl -X POST https://sharko.your-domain.com/api/v1/operations/op_a1b2c3d4/heartbeat \
  -H "Authorization: Bearer <token>"
```

### Trigger Secrets Reconcile

```bash
# Reconcile all clusters:
curl -X POST https://sharko.your-domain.com/api/v1/secrets/reconcile \
  -H "Authorization: Bearer <token>"

# Reconcile a specific cluster:
curl -X POST https://sharko.your-domain.com/api/v1/secrets/reconcile \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"cluster": "prod-eu"}'
```

### Check Secrets Status

```bash
curl https://sharko.your-domain.com/api/v1/secrets/status \
  -H "Authorization: Bearer <token>"
# Response: [{"cluster": "prod-eu", "last_run": "2026-04-06T10:00:00Z", "status": "ok", "secrets_pushed": 2}]
```

### Test Cluster Connectivity

```bash
curl -X POST https://sharko.your-domain.com/api/v1/clusters/prod-eu/test \
  -H "Authorization: Bearer <token>"
# Response (reachable):    {"reachable": true, "version": "v1.29.3"}
# Response (unreachable):  {"reachable": false, "error": "connection refused"}
```

### Adopt a Discovered Cluster

```bash
curl -X POST https://sharko.your-domain.com/api/v1/clusters/adopt \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"clusters": ["prod-eu"]}'
# Response: {"results": [{"name": "prod-eu", "status": "success", "git": {"pr_url": "https://github.com/.../pull/55"}}]}
```

### List Notifications

```bash
curl https://sharko.your-domain.com/api/v1/notifications \
  -H "Authorization: Bearer <token>"
# Response: [{"id": "notif_1", "type": "security_advisory", "title": "cert-manager major version available", "read": false, ...}]
```

### Filter and Sort Clusters

```bash
# Only prod clusters, sorted by health descending
curl "https://sharko.your-domain.com/api/v1/clusters?filter=env:prod&sort=-health" \
  -H "Authorization: Bearer <token>"
```
