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
| `GET` | `/api/v1/addon-secrets` | List addon secret definitions |
| `GET` | `/api/v1/clusters/{name}/secrets` | List managed secrets on a cluster |

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
| `POST` | `/api/v1/clusters/{name}/secrets/refresh` | Refresh managed secrets on a cluster |
| `POST` | `/api/v1/clusters/{name}/test` | Test cluster connectivity (returns `{"reachable": bool, "version": "..."}`) |
| `POST` | `/api/v1/clusters/{name}/adopt` | Adopt a discovered ArgoCD cluster into Sharko management |

### Addons

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/addons` | Add addon to catalog |
| `DELETE` | `/api/v1/addons/{name}?confirm=true` | Remove addon from catalog and all clusters |
| `POST` | `/api/v1/addons/{name}/upgrade` | Upgrade addon (global or per-cluster) |
| `POST` | `/api/v1/addons/upgrade-batch` | Upgrade multiple addons in one PR |

### Addon Secrets

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/addon-secrets` | Define an addon secret template |
| `DELETE` | `/api/v1/addon-secrets/{addon}` | Remove an addon secret definition |

### Tokens

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/tokens` | Create an API key |
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
| `GET` | `/api/v1/secrets/status` | Reconciler status per cluster (last run, hash result, errors) |

### Webhooks

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/webhooks/git` | Git push webhook — triggers secrets reconcile (requires HMAC-SHA256 signature) |

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

### Create an API Key

```bash
curl -X POST https://sharko.your-domain.com/api/v1/tokens \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "ci-pipeline",
    "role": "viewer"
  }'
```

Response includes the plaintext key — store it immediately.

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
curl -X POST https://sharko.your-domain.com/api/v1/clusters/prod-eu/adopt \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"addons": ["cert-manager", "metrics-server"]}'
# Response: {"pr_url": "https://github.com/.../pull/55", "cluster": "prod-eu"}
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
