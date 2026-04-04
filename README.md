<p align="center">
  <img src="assets/logo/sharko-banner.png" alt="Sharko" width="400">
</p>

<h1 align="center">Sharko</h1>

<p align="center">
  <strong>Addon management for Kubernetes clusters, built on ArgoCD</strong>
</p>

<p align="center">
  <a href="https://github.com/MoranWeissman/sharko/releases"><img src="https://img.shields.io/github/v/release/MoranWeissman/sharko" alt="Release"></a>
  <a href="https://github.com/MoranWeissman/sharko/blob/main/LICENSE"><img src="https://img.shields.io/github/license/MoranWeissman/sharko" alt="License"></a>
  <img src="https://img.shields.io/badge/go-1.25-blue" alt="Go">
</p>

---

Sharko is a server that runs in your Kubernetes cluster, next to ArgoCD, and manages the lifecycle of addons across your clusters. It provides a REST API, a dashboard UI, a thin CLI client, and an AI assistant for troubleshooting.

## What Sharko Does

- **Register clusters** from secrets providers (AWS Secrets Manager, K8s Secrets), including remote cluster secrets (API keys delivered to remote clusters)
- **Manage addons** across your clusters (cert-manager, monitoring, logging, etc.)
- **Observe cluster health** with drift detection, version matrix, and sync status
- **Automate GitOps workflows** — every change creates a PR (auto-merged if `SHARKO_GITOPS_PR_AUTO_MERGE=true`)
- **Upgrade addons** globally or per-cluster, with batch multi-addon upgrades
- **Batch register clusters** — register up to 10 clusters in a single API call
- **Manage API keys** for non-interactive consumers (Backstage, Terraform, CI/CD)
- **Full UI write capabilities** — register clusters, add addons, upgrade versions, manage secrets from the browser
- **Integrate with IDPs** — Backstage, Port.io, Terraform, CI/CD all use the same API

## Architecture

```
Developer laptop:
  sharko CLI ---------> Sharko Server API

Backstage / Port.io:
  plugin -------------> Sharko Server API

Terraform / CI:
  curl / CLI ---------> Sharko Server API

Sharko Server (in-cluster):
  +-- UI (React dashboard)
  +-- API (read + write endpoints)
  +-- Orchestrator (workflow engine, Git-serialized via mutex)
  +-- ArgoCD client (account token auth)
  +-- Git client (GitHub, Azure DevOps)
  +-- Secrets provider (AWS SM, K8s Secrets)
  +-- Remote client (deliver secrets to remote clusters)
  +-- AI assistant (OpenAI, Ollama, Claude)
```

The server holds all credentials. The CLI is a thin HTTP client — like `kubectl` to the Kubernetes API server. No credentials on developer laptops.

## Quickstart

### 1. Install Sharko

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set secrets.GITHUB_TOKEN=<github-pat>
```

### 2. Connect the CLI

```bash
sharko login --server https://sharko.your-cluster.com
```

### 3. Configure connections

Open the Sharko UI and configure your ArgoCD and Git connections in Settings.

### 4. Initialize your addons repo

```bash
sharko init
```

### 5. Add addons and clusters

```bash
sharko add-addon cert-manager --chart cert-manager --repo https://charts.jetstack.io --version 1.14.5
sharko add-cluster prod-eu --addons cert-manager,metrics-server --region eu-west-1
sharko status
```

## API

Sharko exposes a REST API that every consumer uses — the CLI, the UI, and external integrations.

### Read Operations (observability)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/clusters` | List clusters with health stats |
| GET | `/api/v1/clusters/{name}` | Cluster detail + addon status |
| GET | `/api/v1/clusters/available` | Discover available clusters from the secrets provider |
| GET | `/api/v1/addons/catalog` | Addon catalog with deployment stats |
| GET | `/api/v1/addons/version-matrix` | Version matrix: addon x cluster grid |
| GET | `/api/v1/fleet/status` | Cluster status overview |
| GET | `/api/v1/observability/overview` | ArgoCD health groups + sync activity |
| GET | `/api/v1/tokens` | List API keys (admin only) |
| GET | `/api/v1/addon-secrets` | List addon secret definitions |
| GET | `/api/v1/clusters/{name}/secrets` | List managed secrets on a cluster |

### Write Operations (management)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/clusters` | Register a cluster |
| POST | `/api/v1/clusters/batch` | Batch register up to 10 clusters |
| DELETE | `/api/v1/clusters/{name}` | Deregister a cluster |
| PATCH | `/api/v1/clusters/{name}` | Update addon labels |
| POST | `/api/v1/clusters/{name}/refresh` | Refresh cluster credentials |
| POST | `/api/v1/clusters/{name}/secrets/refresh` | Refresh managed secrets on a cluster |
| POST | `/api/v1/addons` | Add addon to catalog |
| DELETE | `/api/v1/addons/{name}?confirm=true` | Remove addon (with safety gate) |
| POST | `/api/v1/addons/{name}/upgrade` | Upgrade an addon (global or per-cluster) |
| POST | `/api/v1/addons/upgrade-batch` | Upgrade multiple addons in one PR |
| POST | `/api/v1/addon-secrets` | Define an addon secret template |
| DELETE | `/api/v1/addon-secrets/{addon}` | Remove an addon secret definition |
| POST | `/api/v1/tokens` | Create an API key |
| DELETE | `/api/v1/tokens/{name}` | Revoke an API key |
| POST | `/api/v1/init` | Initialize addons repo from templates |

See [docs/api-contract.md](docs/api-contract.md) for full API reference with request/response shapes.

## CLI Commands

| Command | Description |
|---------|-------------|
| `sharko login --server <url>` | Authenticate with the server |
| `sharko version` | Show CLI + server version |
| `sharko init` | Initialize the addons repo |
| `sharko add-cluster <name>` | Register a cluster |
| `sharko add-clusters <n1,n2,...>` | Batch register multiple clusters |
| `sharko remove-cluster <name>` | Deregister a cluster |
| `sharko update-cluster <name>` | Update addon assignments |
| `sharko list-clusters` | List all clusters |
| `sharko add-addon <name>` | Add addon to catalog |
| `sharko remove-addon <name>` | Remove addon (dry-run without `--confirm`) |
| `sharko upgrade-addon <name>` | Upgrade an addon version (global or per-cluster) |
| `sharko upgrade-addons <addon=ver,...>` | Batch upgrade multiple addons |
| `sharko token create` | Create an API key |
| `sharko token list` | List API keys |
| `sharko token revoke <name>` | Revoke an API key |
| `sharko status` | Cluster status overview |

## Secrets Providers

Sharko uses a pluggable provider interface to fetch cluster kubeconfigs:

| Provider | Description |
|----------|-------------|
| `aws-sm` | AWS Secrets Manager (IRSA for auth) |
| `k8s-secrets` | Kubernetes Secrets (no cloud dependency) |

Configure via environment variables (set via Helm `extraEnv` or the created Secret):

| Env Var | Description |
|---------|-------------|
| `SHARKO_PROVIDER_TYPE` | Provider backend (`aws-sm` or `k8s-secrets`) |
| `SHARKO_PROVIDER_REGION` | AWS region (for `aws-sm`) |
| `SHARKO_PROVIDER_NAMESPACE` | K8s namespace for secrets (for `k8s-secrets`) |

### Writing Your Own Provider

Implement the `ClusterCredentialsProvider` interface:

```go
type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (*Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}
```

See [internal/providers/](internal/providers/) for implementation examples.

## Configuration

All configuration is server-side via Helm values and environment variables. No config files in the addons repo.

| Env Var | Description | Default |
|---------|-------------|---------|
| `SHARKO_PROVIDER_TYPE` | Secrets provider backend (`aws-sm`, `k8s-secrets`) | (none) |
| `SHARKO_PROVIDER_REGION` | AWS region for secrets provider | (none) |
| `SHARKO_ENCRYPTION_KEY` | Encryption key for connection secrets (required in K8s) | (none) |
| `SHARKO_DEV_MODE` | Enable env var fallback for credentials | `false` |
| `SHARKO_GITOPS_PR_AUTO_MERGE` | Auto-merge PRs after creation | `false` |
| `SHARKO_GITOPS_BRANCH_PREFIX` | Branch prefix for PR branches | `sharko/` |
| `SHARKO_GITOPS_COMMIT_PREFIX` | Commit message prefix | `sharko:` |
| `SHARKO_GITOPS_BASE_BRANCH` | Target branch for PRs | `main` |
| `SHARKO_GITOPS_REPO_URL` | Git repo URL for init placeholder replacement | (none) |
| `SHARKO_ADDON_SECRETS` | JSON-encoded addon secret definitions | (none) |
| `SHARKO_DEFAULT_ADDONS` | Comma-separated default addons for new clusters | (none) |
| `SHARKO_HOST_CLUSTER_NAME` | Name of the host cluster (for in-cluster deployment) | (none) |
| `SHARKO_INIT_AUTO_BOOTSTRAP` | Auto-bootstrap ArgoCD during init (not yet implemented, post-v1) | `false` |
| `GITHUB_TOKEN` | GitHub PAT (set via `secrets.GITHUB_TOKEN` in Helm) | (none) |

## Development

```bash
# Run server + UI in dev mode
make dev

# Build
go build -o sharko ./cmd/sharko

# Test
go test ./...

# Docker
docker build -t sharko:dev .
```

## License

[MIT](LICENSE)
