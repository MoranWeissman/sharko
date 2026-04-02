<p align="center">
  <img src="assets/logo/sharko-banner.png" alt="Sharko" width="400">
</p>

<h1 align="center">Sharko</h1>

<p align="center">
  <strong>Addon management for Kubernetes fleets, built on ArgoCD</strong>
</p>

<p align="center">
  <a href="https://github.com/MoranWeissman/sharko/releases"><img src="https://img.shields.io/github/v/release/MoranWeissman/sharko" alt="Release"></a>
  <a href="https://github.com/MoranWeissman/sharko/blob/main/LICENSE"><img src="https://img.shields.io/github/license/MoranWeissman/sharko" alt="License"></a>
  <img src="https://img.shields.io/badge/go-%3E%3D1.25-blue" alt="Go">
</p>

---

Sharko is a server that runs in your Kubernetes cluster, next to ArgoCD, and manages the lifecycle of addons across your fleet. It provides a REST API, a fleet dashboard UI, a thin CLI client, and an AI assistant for troubleshooting.

## What Sharko Does

- **Register clusters** from secrets providers (AWS Secrets Manager, K8s Secrets)
- **Manage addons** across your fleet (cert-manager, monitoring, logging, etc.)
- **Observe fleet health** with drift detection, version matrix, and sync status
- **Automate GitOps workflows** — every change is a Git commit or PR
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
  +-- UI (React fleet dashboard)
  +-- API (read + write endpoints)
  +-- Orchestrator (workflow engine)
  +-- ArgoCD client (account token auth)
  +-- Git client (GitHub, Azure DevOps)
  +-- Secrets provider (AWS SM, K8s Secrets)
  +-- AI assistant (OpenAI, Ollama, Claude)
```

The server holds all credentials. The CLI is a thin HTTP client — like `kubectl` to the Kubernetes API server. No credentials on developer laptops.

## Quickstart

### 1. Install Sharko

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/charts/sharko \
  --namespace sharko --create-namespace \
  --set argocd.token=<argocd-account-token> \
  --set git.token=<github-token> \
  --set secretsProvider.type=aws-sm \
  --set secretsProvider.region=eu-west-1
```

### 2. Connect the CLI

```bash
sharko login --server https://sharko.your-cluster.com
```

### 3. Initialize your addons repo

```bash
sharko init
```

### 4. Add addons and clusters

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
| GET | `/api/v1/addons/catalog` | Addon catalog with deployment stats |
| GET | `/api/v1/addons/version-matrix` | Version matrix: addon x cluster grid |
| GET | `/api/v1/fleet/status` | Fleet-wide health overview |
| GET | `/api/v1/observability/overview` | ArgoCD health groups + sync activity |

### Write Operations (management)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/clusters` | Register a cluster |
| DELETE | `/api/v1/clusters/{name}` | Deregister a cluster |
| PATCH | `/api/v1/clusters/{name}` | Update addon labels |
| POST | `/api/v1/clusters/{name}/refresh` | Refresh cluster credentials |
| POST | `/api/v1/addons` | Add addon to catalog |
| DELETE | `/api/v1/addons/{name}?confirm=true` | Remove addon (with safety gate) |

See [docs/api-contract.md](docs/api-contract.md) for full API reference with request/response shapes.

## CLI Commands

| Command | Description |
|---------|-------------|
| `sharko login --server <url>` | Authenticate with the server |
| `sharko version` | Show CLI + server version |
| `sharko init` | Initialize the addons repo |
| `sharko add-cluster <name>` | Register a cluster |
| `sharko remove-cluster <name>` | Deregister a cluster |
| `sharko update-cluster <name>` | Update addon assignments |
| `sharko list-clusters` | List all clusters |
| `sharko add-addon <name>` | Add addon to catalog |
| `sharko remove-addon <name>` | Remove addon (dry-run without `--confirm`) |
| `sharko status` | Fleet health overview |

## Secrets Providers

Sharko uses a pluggable provider interface to fetch cluster kubeconfigs:

| Provider | Description |
|----------|-------------|
| `aws-sm` | AWS Secrets Manager (IRSA for auth) |
| `k8s-secrets` | Kubernetes Secrets (no cloud dependency) |

Configure via Helm values:

```yaml
secretsProvider:
  type: aws-sm
  region: eu-west-1
```

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

| Config | Env Var | Default |
|--------|---------|---------|
| Provider type | `SHARKO_PROVIDER_TYPE` | (none) |
| Provider region | `SHARKO_PROVIDER_REGION` | (none) |
| GitOps mode | `SHARKO_GITOPS_DEFAULT_MODE` | `pr` |
| Branch prefix | `SHARKO_GITOPS_BRANCH_PREFIX` | `sharko/` |
| Commit prefix | `SHARKO_GITOPS_COMMIT_PREFIX` | `sharko:` |
| Base branch | `SHARKO_GITOPS_BASE_BRANCH` | `main` |
| Repo paths | `SHARKO_REPO_PATH_CLUSTER_VALUES` | `configuration/addons-clusters-values` |

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
