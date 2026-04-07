<p align="center">
  <img src="assets/logo/sharko-mascot.png" alt="Sharko" width="400">
</p>

<h1 align="center">Sharko</h1>

<p align="center">
  <strong>Addon management for Kubernetes clusters, built on ArgoCD</strong>
</p>

<p align="center">
  <a href="https://github.com/MoranWeissman/sharko/releases"><img src="https://img.shields.io/github/v/release/MoranWeissman/sharko" alt="Release"></a>
  <a href="https://github.com/MoranWeissman/sharko/blob/main/LICENSE"><img src="https://img.shields.io/github/license/MoranWeissman/sharko" alt="License"></a>
  <img src="https://img.shields.io/badge/go-1.25-blue" alt="Go">
  <img src="https://img.shields.io/badge/react-18-61dafb" alt="React">
  <img src="https://img.shields.io/badge/typescript-5-3178c6" alt="TypeScript">
</p>

---

Sharko is a server that runs in your Kubernetes cluster, next to ArgoCD, and manages the lifecycle of addons across your fleet. It provides a REST API, a dashboard UI with an ocean-inspired theme, a thin CLI client, and an AI assistant for troubleshooting.

## What Sharko Does

- **Register clusters** from secrets providers (AWS Secrets Manager, K8s Secrets), including remote cluster secrets (API keys delivered to remote clusters)
- **Manage addons** across your clusters (cert-manager, monitoring, logging, etc.)
- **Catalog-driven secrets** -- declare addon secrets in `addons-catalog.yaml`; Sharko reconciles them to remote clusters automatically (no ESO required)
- **Observe cluster health** with drift detection, version matrix, and sync status
- **Automate GitOps workflows** -- every change creates a PR (auto-merged if `SHARKO_GITOPS_PR_AUTO_MERGE=true`; branches cleaned up automatically after merge)
- **Upgrade addons** globally or per-cluster, with batch multi-addon upgrades and per-cluster drift detection
- **Batch register clusters** -- register up to 10 clusters in a single API call
- **Managed vs discovered clusters** -- Sharko surfaces all ArgoCD clusters, distinguishing managed (registered via Sharko) from discovered (pre-existing). Adopt discovered clusters into full management with one command.
- **Connectivity checks** -- verify cluster reachability via `POST /api/v1/clusters/{name}/test` without running a full registration
- **AWS SM structured JSON** -- store cluster credentials as individual JSON keys in AWS SM instead of raw kubeconfig YAML; supports STS EKS token generation via IRSA (no static tokens)
- **ArgoCD service discovery** -- Sharko probes all services in the ArgoCD namespace, tolerating service name changes without reconfiguration
- **Security advisory notifications** -- major Helm chart version bumps are flagged as security advisories in the notification bell
- **List filtering and sorting** -- filter clusters by env/health/addon and sort by name/health/addon count via query params
- **Addon help tooltips** -- all advanced configuration fields in the UI include contextual help text
- **Manage API keys** for non-interactive consumers (Backstage, Terraform, CI/CD)
- **Full UI write capabilities** -- register clusters, add addons, upgrade versions, manage secrets from the browser
- **First-run wizard** -- guided setup for new installations (connection config + repo init), dismissible at any step
- **Async operations** -- long-running workflows (init, batch) tracked via operation sessions with streaming logs
- **Integrate with IDPs** -- Backstage, Port.io, Terraform, CI/CD all use the same API
- **AI-powered troubleshooting** -- context-aware assistant with deep platform knowledge

## Demo

The fastest way to try Sharko. No Kubernetes cluster required -- mock backends simulate ArgoCD, Git, and secrets providers.

```bash
git clone https://github.com/MoranWeissman/sharko.git
cd sharko
make demo
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` / `admin` (admin role) or `qa` / `sharko` (viewer role).

Demo mode gives you a fully functional UI and API with realistic mock data -- clusters, addons, health status, drift detection, and the AI assistant.

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
  +-- AI assistant (multi-provider)
  +-- Swagger UI (/swagger/index.html)
```

The server holds all credentials. The CLI is a thin HTTP client -- like `kubectl` to the Kubernetes API server. No credentials on developer laptops.

## Tech Stack

| Layer | Technology |
|-------|------------|
| Backend | Go 1.25, net/http, Cobra CLI framework |
| Frontend | React 18, TypeScript, Vite |
| Styling | Tailwind CSS v4, shadcn/ui components |
| GitOps | ArgoCD ApplicationSets, Helm charts |
| API docs | Swagger / OpenAPI (swag) |
| Secrets | AWS Secrets Manager, Kubernetes Secrets |
| AI | OpenAI, Claude, Gemini, Ollama, custom OpenAI-compatible |

## UI Features

Sharko ships with a full-featured dashboard built on a sky-blue ocean theme.

- **Fleet overview** -- cluster health cards with sync status, addon counts, and connection indicators; separate sections for managed and discovered clusters
- **Addon catalog** -- version matrix showing every addon across every cluster, with drift detection highlights and contextual help tooltips on all advanced config fields
- **Cluster detail pages** -- left navigation panel with addon list, health status, per-cluster configuration, and a **Test Connectivity** button
- **Addon upgrade view** -- upgrade addons globally or per-cluster, with per-cluster drift detection showing which clusters are behind
- **Notification bell** -- alerts for available upgrades, configuration drift, and security advisories (major version bumps flagged with amber icon)
- **Filtering and sorting** -- filter clusters by environment/health/addon, sort by name/health/count; live filter UI above clusters list
- **AI assistant** -- floating panel accessible from any page, context-aware (knows what page you are on)
- **Settings** -- left navigation panel with connections (ArgoCD, Git), secrets providers, addon secrets, and AI configuration
- **First-run wizard** -- dismissible at any step with an X button; no forced walk-through
- **Full write support** -- register clusters, add addons, upgrade versions, manage API keys, configure secrets, all from the browser

> Screenshots are available in the `assets/` directory.

## Quickstart (Production)

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

Sharko exposes a REST API that every consumer uses -- the CLI, the UI, and external integrations.

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
| GET | `/api/v1/notifications` | List notifications (upgrades, drift, security advisories) |

### Write Operations (management)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/clusters` | Register a cluster |
| POST | `/api/v1/clusters/batch` | Batch register up to 10 clusters |
| DELETE | `/api/v1/clusters/{name}` | Deregister a cluster |
| PATCH | `/api/v1/clusters/{name}` | Update addon labels |
| POST | `/api/v1/clusters/{name}/refresh` | Refresh cluster credentials |
| POST | `/api/v1/clusters/{name}/secrets/refresh` | Refresh managed secrets on a cluster |
| POST | `/api/v1/clusters/{name}/test` | Test cluster connectivity |
| POST | `/api/v1/clusters/{name}/adopt` | Adopt a discovered ArgoCD cluster into Sharko |
| POST | `/api/v1/addons` | Add addon to catalog |
| DELETE | `/api/v1/addons/{name}?confirm=true` | Remove addon (with safety gate) |
| POST | `/api/v1/addons/{name}/upgrade` | Upgrade an addon (global or per-cluster) |
| POST | `/api/v1/addons/upgrade-batch` | Upgrade multiple addons in one PR |
| POST | `/api/v1/addon-secrets` | Define an addon secret template |
| DELETE | `/api/v1/addon-secrets/{addon}` | Remove an addon secret definition |
| POST | `/api/v1/tokens` | Create an API key |
| DELETE | `/api/v1/tokens/{name}` | Revoke an API key |
| POST | `/api/v1/init` | Initialize addons repo from templates (async — returns `operation_id`) |
| GET | `/api/v1/operations/{id}` | Get async operation status and log lines |
| POST | `/api/v1/operations/{id}/heartbeat` | Keep-alive for an active operation session |
| POST | `/api/v1/secrets/reconcile` | Trigger immediate secrets reconcile |
| GET | `/api/v1/secrets/status` | Reconciler status per cluster |
| POST | `/api/v1/webhooks/git` | Git push webhook (triggers secrets reconcile, HMAC-SHA256 verified) |

See [docs/api-contract.md](docs/api-contract.md) for full API reference with request/response shapes.

## Swagger

Interactive API documentation is available at `/swagger/index.html` when the server is running. In demo mode:

```
http://localhost:8080/swagger/index.html
```

The Swagger UI lets you explore all endpoints, view request/response schemas, and execute API calls directly from the browser.

## CLI Commands

| Command | Description |
|---------|-------------|
| `sharko login --server <url>` | Authenticate with the server |
| `sharko version` | Show CLI + server version |
| `sharko init` | Initialize the addons repo (async, streams progress) |
| `sharko validate [path]` | Validate catalog YAML against schema |
| `sharko connect` | Configure the active Git connection |
| `sharko connect list` | Show current connection |
| `sharko connect test` | Test current connection |
| `sharko add-cluster <name>` | Register a cluster |
| `sharko add-clusters <n1,n2,...>` | Batch register multiple clusters |
| `sharko remove-cluster <name>` | Deregister a cluster |
| `sharko update-cluster <name>` | Update addon assignments |
| `sharko list-clusters` | List all clusters |
| `sharko test-cluster <name>` | Test connectivity to a cluster |
| `sharko adopt-cluster <name>` | Adopt a discovered ArgoCD cluster into Sharko |
| `sharko add-addon <name>` | Add addon to catalog |
| `sharko remove-addon <name>` | Remove addon (dry-run without `--confirm`) |
| `sharko upgrade-addon <name>` | Upgrade an addon version (global or per-cluster) |
| `sharko upgrade-addons <addon=ver,...>` | Batch upgrade multiple addons |
| `sharko list-addons [--show-config]` | List addons (with catalog config) |
| `sharko refresh-secrets [cluster]` | Trigger immediate secrets reconcile |
| `sharko secret-status` | Show reconciler status per cluster |
| `sharko token create` | Create an API key |
| `sharko token list` | List API keys |
| `sharko token revoke <name>` | Revoke an API key |
| `sharko status` | Cluster status overview |

## AI Assistant

Sharko includes a built-in AI assistant that understands your platform. It runs as a floating panel in the UI and is context-aware -- it knows which page you are on and can answer questions about the clusters, addons, and configuration you are currently viewing.

**Supported providers:**

| Provider | Description |
|----------|-------------|
| OpenAI | GPT-4o and other OpenAI models |
| Claude | Anthropic Claude models |
| Gemini | Google Gemini models |
| Ollama | Local/self-hosted models (no API key needed) |
| Custom OpenAI-compatible | Any provider with an OpenAI-compatible API (custom base URL, auth header, auth prefix) |

**Capabilities:** 24 read tools and 5 write tools give the assistant deep platform access:

- List clusters, addons, and their health status
- Inspect per-cluster addon configuration and values
- Query ArgoCD application health, resources, events, and pod logs
- Compare Helm chart versions and fetch release notes
- Detect unhealthy addons and disconnected clusters
- Search the web for Kubernetes and Helm documentation
- Enable/disable addons, update versions, sync and refresh ArgoCD apps (write tools, admin only)
- Persistent memory across conversations

Configure the AI provider in the Settings page or via environment variables. Write tools require admin role and explicit opt-in.

## API Keys

API keys provide long-lived authentication for non-interactive consumers like Backstage, Terraform, and CI/CD pipelines.

- Keys use the `sharko_` prefix followed by 32 hex characters (e.g., `sharko_a1b2c3d4...`)
- The plaintext key is shown only once at creation time
- Keys are stored as bcrypt hashes -- the server never stores plaintext keys
- Each key has a name for identification and an associated role (`admin` or `viewer`)
- Manage keys via `sharko token create/list/revoke` or the UI Settings page

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
| `SHARKO_SECRET_RECONCILE_INTERVAL` | How often the secrets reconciler runs (Go duration) | `5m` |
| `SHARKO_WEBHOOK_SECRET` | HMAC-SHA256 secret for validating `/api/v1/webhooks/git` | (none) |
| `GITHUB_TOKEN` | GitHub PAT (set via `secrets.GITHUB_TOKEN` in Helm) | (none) |

## Development

### Demo mode (recommended for QA and exploration)

Builds the UI and starts the server with mock backends. No external dependencies required.

```bash
make demo
# Open http://localhost:8080
# Login: admin/admin (admin) or qa/sharko (viewer)
```

### Hot-reload development

Starts the Go backend in demo mode and the Vite dev server with hot reload. Use this when developing the UI.

```bash
make dev
# Frontend: http://localhost:5173 (open this)
# Backend:  http://localhost:8080 (API only)
```

### Build and test

```bash
make build          # Build Go binary + UI
make test           # Run all tests (Go + UI)
make lint           # Go vet + UI build check
```

### Swagger regeneration

After changing API annotations, regenerate the Swagger docs:

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

### Docker

```bash
docker build -t sharko:dev .
```

## Documentation

| Document | Description |
|----------|-------------|
| [API Contract](docs/api-contract.md) | Full API reference with request/response shapes, error codes, and orchestration behavior |
| [Architecture](docs/architecture.md) | Server-first architecture, orchestrator pattern, provider interfaces |
| [User Guide](docs/user-guide.md) | End-to-end guide for operators: install, configure, manage clusters and addons |
| [Developer Guide](docs/developer-guide.md) | Contributing guide: project structure, coding patterns, testing, adding new features |

## Contributing

Sharko development is coordinated through an agent team structure defined in [`.claude/team/`](.claude/team/). Each role (implementer, frontend expert, Go expert, test engineer, etc.) has a playbook that describes patterns, conventions, and responsibilities. If you are contributing, read the role file relevant to your change.

Key conventions:

- All write operations go through the orchestrator and create PRs (never direct commits)
- The API contract in `docs/api-contract.md` is the source of truth for endpoint behavior
- Tests live next to the code they test (`_test.go` suffix)
- UI components use shadcn/ui with the project's ocean theme

## License

[MIT](LICENSE)
