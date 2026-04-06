# API Endpoints

Key endpoints reference. For full request/response schemas, use the interactive [Swagger UI](../api/overview.md#interactive-documentation) or see [docs/api-contract.md](https://github.com/MoranWeissman/sharko/blob/main/docs/api-contract.md).

## Read Endpoints

### Clusters

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/clusters` | List all registered clusters with health stats |
| `GET` | `/api/v1/clusters/{name}` | Cluster detail with addon status |
| `GET` | `/api/v1/clusters/available` | Discover available clusters from the secrets provider |

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
| `POST` | `/api/v1/init` | Initialize addons repo from templates |

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
