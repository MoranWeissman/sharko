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

Sharko is a server that runs in your Kubernetes cluster, next to ArgoCD, and manages the lifecycle of addons across your fleet. Install it with a single Helm command, and a guided wizard walks you through connecting your Git repo, ArgoCD instance, and optional secrets provider — no config files, no env vars to set by hand.

## Features

- **Wizard-based setup** — first run opens a step-by-step wizard: Git connection, ArgoCD connection, secrets provider, and repo initialization
- **Fleet dashboard** — cluster health cards with sync status, addon counts, and connection indicators; managed and discovered clusters in separate sections
- **Curated marketplace (v1.21)** — 45 vetted Helm addons with OpenSSF Scorecard signals, server-side ArtifactHub search, and smart values seeding (heuristic + optional LLM annotation) — every Add still goes through a Git PR
- **Addon catalog** — version matrix across every cluster, drift detection, and contextual help on all advanced config fields
- **GitOps-native** — every write operation creates a PR (auto-merge optional); branches cleaned up after merge
- **Managed vs discovered clusters** — Sharko surfaces all ArgoCD clusters; adopt discovered clusters into full management in one click
- **Secrets provider** — deliver addon credentials to remote clusters via AWS Secrets Manager or Kubernetes Secrets (no ESO required)
- **AI assistant** — context-aware troubleshooting panel with resizable panel and error-aware pre-filled prompts; supports OpenAI, Claude, Gemini, Ollama, and any OpenAI-compatible API
- **API keys** — long-lived tokens for Backstage, Terraform, and CI/CD integrations
- **Unified API** — CLI, UI, and external integrations all use the same REST API
- **Upgrade management** — security-aware upgrade recommendation cards (ArtifactHub advisories, security/breaking-change flags, scored best-path), analyze-before-upgrade enforcement, step-by-step progress, batch multi-addon upgrades
- **ArgoCD diagnostics** — ArgoCD connection state surfaced per cluster; bootstrap app health shown on dashboard and observability view
- **Auto-refresh** — dashboard, cluster detail, cluster overview, and addon detail pages refresh automatically (30s); addon catalog refreshes every 60s

- **Addon dependency ordering** — declare `dependsOn` in the catalog to enforce deployment order; cycle detection prevents invalid graphs
- **AI addon summaries** — AI-generated summaries of each addon's purpose and release notes, shown in the addon detail view
- **Audit log** — every write operation recorded with actor, action, result, and timestamp; queryable via `GET /api/v1/audit`
- **Multi-cloud provider stubs** — GCP and Azure provider stubs define the interface for community contributions
- **E2E test framework** — test against a real ArgoCD + Kind cluster (`make e2e-setup && make e2e`)

## Demo

No Kubernetes cluster required — mock backends simulate ArgoCD, Git, and secrets providers.

```bash
git clone https://github.com/MoranWeissman/sharko.git
cd sharko
make demo
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` / `admin` (admin role) or `qa` / `sharko` (viewer role).

## Quick Start (Production)

### 1. Install Sharko

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace
```

If using AWS Secrets Manager for cluster credentials, add the IRSA annotation:

```bash
helm install sharko oci://ghcr.io/moranweissman/sharko/sharko \
  --namespace sharko --create-namespace \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789012:role/sharko-role
```

### 2. Get the Admin Password

```bash
kubectl get secret sharko -n sharko \
  -o jsonpath='{.data.admin\.initialPassword}' | base64 -d
```

### 3. Open the UI

```bash
kubectl port-forward svc/sharko 8080:80 -n sharko
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` and the password from step 2.

### 4. Complete the First-Run Wizard

The wizard appears automatically on first access — no separate configuration step needed.

1. **Welcome** — overview of what Sharko will set up
2. **Git connection** — enter your repo URL and personal access token
3. **ArgoCD connection** — Sharko auto-discovers the ArgoCD service in-cluster; add optional secrets provider config
4. **Initialize repository** — Sharko creates the ApplicationSet, base values, and cluster directory structure in your repo; choose auto-merge or review the PR yourself

After the wizard completes, the dashboard loads with clusters pulled from ArgoCD.

## Architecture

```
Developer laptop / CI:
  sharko CLI ---------> Sharko Server API

Backstage / Port.io / Terraform:
  plugin / curl ------> Sharko Server API

Sharko Server (in-cluster):
  +-- UI (React dashboard with first-run wizard)
  +-- API (REST endpoints, JWT + API key auth)
  +-- Orchestrator (workflow engine, Git-serialized via mutex)
  +-- ArgoCD client (service-discovery + account token auth)
  +-- Git client (GitHub, Azure DevOps)
  +-- Secrets provider (AWS SM, K8s Secrets)
  +-- Remote client (deliver secrets to remote clusters)
  +-- AI assistant (multi-provider)
  +-- Swagger UI (/swagger/index.html)
```

The server holds all credentials. The CLI is a thin HTTP client — like `kubectl` to the Kubernetes API. No credentials on developer laptops.

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

## CLI Commands

| Command | Description |
|---------|-------------|
| `sharko login --server <url>` | Authenticate with the server |
| `sharko version` | Show CLI + server version |
| `sharko connect` | Configure the active Git connection |
| `sharko connect list` | Show current connection |
| `sharko connect test` | Test current connection |
| `sharko init` | Initialize the addons repo (async, streams progress) |
| `sharko validate [path]` | Validate catalog YAML against schema |
| `sharko add-cluster <name>` | Register a cluster |
| `sharko add-clusters <n1,n2,...>` | Batch register multiple clusters |
| `sharko remove-cluster <name>` | Deregister a cluster |
| `sharko update-cluster <name>` | Update addon assignments |
| `sharko list-clusters` | List all clusters |
| `sharko test-cluster <name>` | Test connectivity to a cluster |
| `sharko adopt-cluster <name>` | Adopt a discovered ArgoCD cluster |
| `sharko add-addon <name>` | Add addon to catalog |
| `sharko remove-addon <name>` | Remove addon (dry-run without `--confirm`) |
| `sharko upgrade-addon <name>` | Upgrade an addon version |
| `sharko upgrade-addons <addon=ver,...>` | Batch upgrade multiple addons |
| `sharko list-addons [--show-config]` | List addons |
| `sharko refresh-secrets [cluster]` | Trigger immediate secrets reconcile |
| `sharko secret-status` | Show reconciler status per cluster |
| `sharko token create` | Create an API key |
| `sharko token list` | List API keys |
| `sharko token revoke <name>` | Revoke an API key |
| `sharko status` | Cluster status overview |

## API

Sharko exposes a REST API that every consumer uses — the CLI, the UI, and external integrations.

### Read Operations

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/clusters` | List clusters with health stats |
| GET | `/api/v1/clusters/{name}` | Cluster detail + addon status |
| GET | `/api/v1/clusters/{name}/comparison` | Git vs ArgoCD comparison, including ArgoCD connection state |
| GET | `/api/v1/clusters/available` | Discover available clusters from the secrets provider |
| GET | `/api/v1/addons/catalog` | Addon catalog with deployment stats |
| GET | `/api/v1/addons/version-matrix` | Version matrix: addon × cluster grid |
| GET | `/api/v1/fleet/status` | Cluster status overview |
| GET | `/api/v1/dashboard/stats` | Aggregated stats including bootstrap app health |
| GET | `/api/v1/upgrade/{addonName}/recommendations` | Smart upgrade recommendations (next patch, next minor, latest stable) |
| GET | `/api/v1/tokens` | List API keys (admin only) |
| GET | `/api/v1/addon-secrets` | List addon secret definitions |
| GET | `/api/v1/clusters/{name}/secrets` | List managed secrets on a cluster |
| GET | `/api/v1/notifications` | List notifications (upgrades, drift, security advisories) |

### Write Operations

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/clusters` | Register a cluster |
| POST | `/api/v1/clusters/batch` | Batch register up to 10 clusters |
| DELETE | `/api/v1/clusters/{name}` | Deregister a cluster |
| PATCH | `/api/v1/clusters/{name}` | Update addon labels |
| POST | `/api/v1/clusters/{name}/refresh` | Refresh cluster credentials |
| POST | `/api/v1/clusters/{name}/secrets/refresh` | Refresh managed secrets on a cluster |
| POST | `/api/v1/clusters/{name}/test` | Test cluster connectivity |
| POST | `/api/v1/clusters/{name}/adopt` | Adopt a discovered ArgoCD cluster |
| POST | `/api/v1/addons` | Add addon to catalog |
| DELETE | `/api/v1/addons/{name}?confirm=true` | Remove addon (with safety gate) |
| POST | `/api/v1/addons/{name}/upgrade` | Upgrade an addon |
| POST | `/api/v1/addons/upgrade-batch` | Upgrade multiple addons in one PR |
| POST | `/api/v1/addon-secrets` | Define an addon secret template |
| DELETE | `/api/v1/addon-secrets/{addon}` | Remove an addon secret definition |
| POST | `/api/v1/tokens` | Create an API key |
| DELETE | `/api/v1/tokens/{name}` | Revoke an API key |
| POST | `/api/v1/init` | Initialize addons repo (async — returns `operation_id`) |
| GET | `/api/v1/operations/{id}` | Get async operation status and log lines |
| POST | `/api/v1/secrets/reconcile` | Trigger immediate secrets reconcile |
| GET | `/api/v1/secrets/status` | Reconciler status per cluster |

See [docs/api-contract.md](docs/api-contract.md) for full request/response shapes.

Interactive API docs at `/swagger/index.html` when the server is running.

## Settings

After the wizard, the **Settings** page has six sections:

| Section | What you configure |
|---------|-------------------|
| Connection | ArgoCD server URL + token, Git provider + repo + token |
| Secrets Provider | `aws-sm` or `k8s-secrets`, region or namespace |
| GitOps | Auto-merge PRs, branch prefix, commit prefix, base branch |
| Users | Change admin password |
| API Keys | Create and revoke long-lived tokens for CI/CD |
| AI | Provider (OpenAI, Claude, Gemini, Ollama, custom), model, API key |

## AI Assistant

Built-in assistant accessible from any page. Knows which cluster or addon you are viewing and can answer questions about health, configuration, and version drift.

**Supported providers:** OpenAI, Claude, Gemini, Ollama, or any OpenAI-compatible API.

**Capabilities:** 24 read tools and 5 write tools (admin-only, opt-in):
- List clusters, addons, and health status
- Inspect per-cluster addon configuration
- Query ArgoCD application health, resources, events, and pod logs
- Compare Helm chart versions and fetch release notes
- Enable/disable addons, update versions, sync and refresh ArgoCD apps

## Secrets Provider

Sharko uses a pluggable provider to fetch cluster kubeconfigs:

| Provider | Description |
|----------|-------------|
| `aws-sm` | AWS Secrets Manager (IRSA for auth — no static credentials) |
| `k8s-secrets` | Kubernetes Secrets (no cloud dependency) |

Configure in **Settings → Secrets Provider**. Supports structured JSON secrets in AWS SM (individual keys instead of raw kubeconfig YAML) and STS EKS token generation via IRSA.

## Development

### Demo mode

```bash
make demo
# Open http://localhost:8080 — login: admin/admin or qa/sharko
```

### Hot-reload development

```bash
make dev
# Frontend: http://localhost:5173
# Backend:  http://localhost:8080 (API only)
```

### Build and test

```bash
make build    # Build Go binary + UI
make test     # Run all tests (Go + UI)
make lint     # Go vet + UI build check
```

### Swagger regeneration

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

## Documentation

| Document | Description |
|----------|-------------|
| [API Contract](docs/api-contract.md) | Full API reference with request/response shapes and error codes |
| [Architecture](docs/architecture.md) | Server-first architecture, orchestrator pattern, provider interfaces |
| [User Guide](docs/user-guide.md) | End-to-end guide: install, configure, manage clusters and addons |
| [Developer Guide](docs/developer-guide.md) | Project structure, coding patterns, testing, adding new features |

## License

[MIT](LICENSE)
