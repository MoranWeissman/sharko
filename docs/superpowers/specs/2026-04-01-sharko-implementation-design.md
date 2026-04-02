# Sharko — Implementation Design Spec

> Authoritative design document for the Sharko implementation.
> All other docs (vision, migration plan) should not contradict this spec.
> Written: 2026-04-01

---

## 1. What Sharko Is

Sharko is an addon management server for Kubernetes fleets, built on ArgoCD. It runs in-cluster, next to ArgoCD, and provides:

- **REST API** — the core product. Read operations (fleet observability, drift detection, version matrix) and write operations (register clusters, manage addons, bootstrap repos). Every consumer talks to this API.
- **Web UI** — a fleet dashboard. One client of the API among many.
- **CLI** — a thin client to the API. Like `kubectl` to the Kubernetes API server.
- **AI Assistant** — an in-app agent that can query cluster state, explain drift, troubleshoot addon failures.

Sharko is NOT a standalone CLI tool. It is a server that must be deployed to a cluster before use.

---

## 2. Architecture

```
Developer laptop:
  sharko CLI ---------> Sharko Server API

Backstage/Port.io:
  plugin -------------> Sharko Server API

Terraform/CI:
  curl/CLI -----------> Sharko Server API

Sharko Server (in-cluster pod):
  +-- UI (React SPA, fleet dashboard)
  +-- API (read + write endpoints)
  +-- Orchestrator (workflow engine)
  +-- ArgoCD client (account token auth)
  +-- Git client (configured token)
  +-- Secrets provider (AWS IRSA, K8s Secrets, Vault)
```

### Key Principle: Server Holds All Credentials

- **ArgoCD:** account token (Bearer auth), stored in K8s Secret. NOT ServiceAccount/in-cluster RBAC — uses ArgoCD's own account system.
- **Git:** token configured once in Helm values. Supports GitHub and Azure DevOps.
- **Secrets Provider:** AWS IRSA for Secrets Manager, or K8s service account for K8s Secrets.
- **Users' laptops have nothing** except a Sharko login token.

### Bootstrap (Chicken-and-Egg)

Sharko is a K8s addon that manages other addons. Like ArgoCD itself, the first install is manual:

```bash
helm install sharko oci://ghcr.io/your-org/sharko/charts/sharko \
  --namespace sharko \
  --set argocd.token=<argocd-account-token> \
  --set git.token=<github-token> \
  --set secretsProvider.type=aws-sm \
  --set secretsProvider.region=eu-west-1
```

After that, everything goes through the server — including `sharko init`.

---

## 3. Identity

| Property | Value |
|---|---|
| Go module path | `github.com/MoranWeissman/sharko` |
| Docker image (GHCR) | `ghcr.io/your-org/sharko` |
| Env var prefix | `SHARKO_*` (clean break from `AAP_*`) |
| Auth header | `X-Sharko-User` |
| Session token key | `sharko-auth-token` |
| Helm chart name | `sharko` |
| Binary name | `sharko` |
| Entrypoint | `sharko serve` |

---

## 4. Release Strategy

### v0.1.0 — Clean Foundation

Same functionality as the current `argocd-addons-platform`, minus stripped code, under the new identity.

**Step 1 — Strip dead code:**
- `internal/migration/` — entire package (models, steps, store, executor, configmap_store, secret_store)
- `internal/datadog/client.go` — vendor-specific metrics client
- `internal/api/datadog.go` — 3 handlers (`getDatadogStatus`, `getDatadogNamespaceMetrics`, `getClusterMetrics`) + route registrations
- `internal/api/migration.go` — 18 route registrations + handlers
- GPTeal references in `internal/ai/client.go` — `X-Merck-APIKey` header, hardcoded GPTeal endpoint URLs. Replace with standard `Authorization: Bearer` + configurable endpoint URL
- Migration-specific AI tools in `internal/ai/tools.go` and `tools_write.go` — list specific tool functions before deleting, preserve fleet management tools (query cluster health, explain drift, etc.)
- Datadog enrichment in `ui/src/views/Observability.tsx` — strip `ddEnabled`, `clusterMetricsCache`, `fetchClusterMetrics`, metric display in expanded rows. Keep everything from ArgoCD (health groups, sync activity, alerts)
- Datadog config section from `ui/src/views/Connections.tsx`
- Migration views from `ui/src/views/` and routes from `App.tsx`
- Corresponding API methods from `ui/src/services/api.ts`
- All test files for stripped code
- Keep `Dashboards.tsx` (generic iframe embedding, not Datadog-specific)
- Keep `templates/monitoring/` (Datadog CRDs/dashboards are addons pattern resources, not Sharko server code)

**Step 2 — Rename:**
- `go.mod` module path: `github.com/MoranWeissman/sharko`
- Global import path replacement across all `.go` files
- Create `cmd/sharko/root.go` — cobra root command with `Execute()`, `--version` flag via ldflags
- Create `cmd/sharko/serve.go` — move server startup logic from `main.go`
- Reduce `main.go` to `func main() { Execute() }`
- Dockerfile: build `./cmd/sharko`, binary name `sharko`, entrypoint `["sharko", "serve"]`
- Makefile: namespace, binary name, image name, build paths

**Step 3 — Rebrand:**
- All `AAP_*` env vars -> `SHARKO_*` in Go code, Helm chart, config files, test files, docs, comments
- `X-AAP-User` -> `X-Sharko-User`
- `aap-auth-token` -> `sharko-auth-token`
- Helm chart `Chart.yaml`: `name: sharko`, image `ghcr.io/your-org/sharko`
- Helm templates: `aap.*` helpers -> `sharko.*` (cascades to all template files)
- UI: sidebar branding, page title, login text, favicon
- `ui/package.json` name -> `sharko`
- Memory path: `/tmp/sharko-agent-memory.json`
- `config.yaml` — update any AAP references, default paths, branding
- `secrets.env.example` — rename `AAP_*` variable names
- Grep `AAP_` across entire codebase (`.go`, `.yaml`, `.json`, `.ts`, `.tsx`, `.md`, `.env`, comments) to catch stragglers

**Step 4 — Verify:**
- `go build ./cmd/sharko` succeeds
- `./sharko serve` starts the server
- `./sharko --help` shows cobra output with `serve` subcommand
- `./sharko --version` shows version
- `make test-go` passes (all remaining tests)
- `cd ui && npm run build` succeeds
- `cd ui && npm test` passes
- `docker build -t sharko:dev .` produces a working image
- UI renders with Sharko branding

**Tag v0.1.0.**

### API Contract Document (Gate)

Before any v1.0.0 code is written, produce `docs/api-contract.md`:

- Every endpoint: method, path, request body (JSON schema), response body, error codes
- Check current route paths in `internal/api/router.go` — map existing routes to versioned `/api/v1/` paths
- May need a versioned API layer wrapping existing handlers (UI keeps old routes during transition)
- Orchestration steps for each write endpoint
- Failure/rollback behavior for multi-step operations
- Partial success as a valid response state
- Preconditions for `POST /api/v1/init`
- CLI command -> API endpoint mapping table
- Sync vs async decision (sync for v1.0.0, noted for future)

This document is reviewed and approved by the user before implementation begins.

### v1.0.0 — The Product

**Step 5 — Provider Interface (`internal/providers/`):**

```go
type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}
```

Files:
- `provider.go` — interface, types (`Kubeconfig`, `ClusterInfo`, `Config`), factory function `New(cfg) -> Provider`
- `k8s_secrets.go` — `KubernetesSecretProvider` (reads kubeconfig from K8s Secrets, no cloud dependency)
- `aws_sm.go` — `AWSSecretsManagerProvider` (reads from AWS Secrets Manager, IRSA auth)
- `provider_test.go` — contract tests
- `k8s_secrets_test.go`, `aws_sm_test.go` — unit tests with mocks

New dependency: `github.com/aws/aws-sdk-go-v2` (config + secretsmanager modules).

Provider config comes from server config (Helm values / env vars).

**Step 6 — Orchestrator (`internal/orchestrator/`):**

The brain that executes multi-step workflows. API handlers delegate to the orchestrator, which coordinates the provider, ArgoCD client, and Git provider.

```go
type Orchestrator struct {
    credProvider  providers.ClusterCredentialsProvider
    argocdClient  *argocd.Client
    gitProvider   gitprovider.GitProvider
    config        *config.SharkoConfig
}

func (o *Orchestrator) InitRepo(ctx, req) (*InitRepoResult, error)
func (o *Orchestrator) RegisterCluster(ctx, req) (*RegisterClusterResult, error)
func (o *Orchestrator) DeregisterCluster(ctx, name) error
func (o *Orchestrator) UpdateClusterAddons(ctx, name, addons) error
func (o *Orchestrator) RefreshClusterCredentials(ctx, name) error
func (o *Orchestrator) AddAddon(ctx, req) error
func (o *Orchestrator) RemoveAddon(ctx, name) error
```

Rollback safety rules:
- **NEVER auto-rollback ArgoCD registration.** If a cluster is registered in ArgoCD and a later step fails (e.g., Git push), ArgoCD may have already started deploying addons. Auto-deregistering could trigger cascade deletion of those addons.
- Instead, return a **partial success** response: "Cluster registered in ArgoCD but Git commit failed. Run `sharko remove-cluster <name>` to clean up, or retry."
- Partial success is a first-class response type, not an error.

Template embedding: `//go:embed` directive lives in the orchestrator package (or a dedicated `internal/templates/` package). The server uses embedded `templates/starter/` content when handling `POST /api/v1/init`.

**Step 7 — Write API Endpoints:**

New handler files following existing patterns (methods on `*Server`, `writeJSON`/`writeError`):
- `internal/api/clusters_write.go` — POST, DELETE, PATCH cluster handlers
- `internal/api/addons_write.go` — POST, DELETE addon handlers
- `internal/api/fleet.go` — fleet status aggregation
- `internal/api/providers.go` — provider list, test connectivity
- `internal/api/init.go` — init workflow endpoint

Each handler: validate input -> call orchestrator -> return result. Thin glue code.

Route registration: new versioned routes (`/api/v1/...`) alongside existing routes.

**Dual auth:**
- UI uses session cookies (existing flow)
- CLI uses Bearer tokens (new `POST /api/v1/auth/token` endpoint: accepts username/password, returns Bearer token)
- Server middleware accepts BOTH cookies and `Authorization: Bearer` on protected endpoints
- CLI stores token in `~/.sharko/config`

Auth model for v1.0.0: session-based (username/password -> token). API keys (`sharko token create --name "backstage" --scope read,write`) are a v1.x feature.

**Step 8 — CLI (`cmd/sharko/`):**

Thin HTTP client commands. Each command:
1. Reads server URL + token from `~/.sharko/config`
2. Builds HTTP request
3. Sends to Sharko API
4. Prints result with progress output

New files:
- `cmd/sharko/login.go` — `sharko login --server <url>` (prompts for username/password, calls `/api/v1/auth/token`, saves to `~/.sharko/config`)
- `cmd/sharko/version.go` — `sharko version` (prints CLI version from ldflags + server version/health from `GET /api/v1/health`)
- `cmd/sharko/init_cmd.go` — `sharko init` (POST to `/api/v1/init`)
- `cmd/sharko/cluster.go` — `sharko add-cluster`, `sharko remove-cluster`, `sharko update-cluster`, `sharko list-clusters`
- `cmd/sharko/addon.go` — `sharko add-addon`, `sharko remove-addon`
- `cmd/sharko/status.go` — `sharko status` (GET `/api/v1/fleet/status`, formatted for terminal)

CLI command -> API mapping:

| CLI Command | API Endpoint |
|---|---|
| `sharko login` | `POST /api/v1/auth/token` |
| `sharko version` | `GET /api/v1/health` |
| `sharko init` | `POST /api/v1/init` |
| `sharko add-cluster <name>` | `POST /api/v1/clusters` |
| `sharko remove-cluster <name>` | `DELETE /api/v1/clusters/{name}` |
| `sharko update-cluster <name>` | `PATCH /api/v1/clusters/{name}` |
| `sharko list-clusters` | `GET /api/v1/clusters` |
| `sharko add-addon <name>` | `POST /api/v1/addons` |
| `sharko remove-addon <name>` | `DELETE /api/v1/addons/{name}` |
| `sharko status` | `GET /api/v1/fleet/status` |

**Step 9 — Templates Cleanup:**

- Create `templates/starter/` with clean scaffold (no production data)
- Strip or anonymize the 50+ real cluster names from `templates/configuration/addons-clusters-values/`
- Keep `templates/` as reference (full production example)
- Embed only `templates/starter/` in the binary
- `POST /api/v1/init` uses embedded starter templates

**Step 10 — Docs & Release:**

- `README.md` — logo, tagline, quickstart (`helm install` -> `sharko login` -> `sharko add-cluster`)
- Provider contribution guide
- API reference (from the contract document)
- Tag v1.0.0
- Docker image to GHCR
- Helm chart to OCI registry

---

## 5. Server-Side Configuration

All configuration is server-side — Helm values, env vars, K8s Secrets. There is no `sharko.yaml` in the addons repo. The server created the repo structure (via `sharko init`), so it already knows the layout. Customization happens via `helm upgrade --set`, same as every other config.

```yaml
# Helm values
argocd:
  token: <argocd-account-token>      # stored in K8s Secret
git:
  token: <github-token>              # stored in K8s Secret
  repo: https://github.com/my-org/my-addons
  branch: main
secretsProvider:
  type: aws-sm                       # or: k8s-secrets
  region: eu-west-1                  # IRSA handles auth
repo:
  paths:
    clusterValues: configuration/addons-clusters-values
    globalValues: configuration/addons-global-values
    charts: charts/
    bootstrap: bootstrap/
gitops:
  defaultMode: pr                    # "pr" or "direct"
  branchPrefix: sharko/
  commitPrefix: "sharko:"
ai:
  provider: openai
  endpoint: https://api.openai.com/v1
  model: gpt-4o
  apiKey: <key>                      # stored in K8s Secret
```

These render as env vars (`SHARKO_REPO_PATH_CLUSTER_VALUES`, `SHARKO_GITOPS_DEFAULT_MODE`, etc.) that the server reads on startup. Defaults are baked into the Helm chart. Users override only what they need.

**Why no sharko.yaml in the repo:** Reading config from Git on every operation is slow, fragile, and has no reconcile loop. The server already knows the repo structure because it created it. All config in one place (Helm values), one admin controls it, no security risk from repo-level config changes.

| Config | Where | Who controls |
|---|---|---|
| Repo paths (where files go) | Helm values / env vars | Admin |
| GitOps preferences (PR vs direct) | Helm values / env vars | Admin |
| ArgoCD connection (URL + token) | K8s Secret (via Helm) | Admin |
| Git connection (token) | K8s Secret (via Helm) | Admin |
| Secrets provider (type + creds) | Helm values + IRSA | Admin |
| AI provider config | K8s Secret (via Helm) | Admin |

---

## 6. What Was Stripped and Why

| Stripped | Reason |
|---|---|
| `internal/migration/` | Built for old two-repo migration (Azure DevOps -> GitHub). Sharko's onboarding is `sharko init` + `sharko add-cluster`. |
| `internal/datadog/client.go` | Vendor-specific metrics integration. Sharko's observability comes from ArgoCD, not Datadog API queries. |
| `internal/api/datadog.go` | Three Datadog-specific API handlers. |
| `internal/api/migration.go` | 18 migration-specific API routes. |
| GPTeal in `internal/ai/` | Company-specific API gateway (`X-Merck-APIKey`). Replaced with standard OpenAI-compatible, Ollama, and generic endpoint providers. |
| Migration AI tools | Tools that reference migration concepts. General fleet management tools preserved. |
| Datadog enrichment in UI | `ddEnabled`, metrics cache, metrics display. ArgoCD-native health data stays. |
| Migration views in UI | MigrationPage, MigrationDetail views and routes. |

---

## 7. AI Provider Configuration

Replaces GPTeal with generic LLM provider support:

- **OpenAI API** — standard, widely available
- **Ollama** — local/self-hosted (Helm chart has optional Ollama sidecar)
- **Generic OpenAI-compatible endpoint** — covers Azure OpenAI, LiteLLM, vLLM, etc.

```yaml
ai:
  provider: openai    # or: ollama, custom
  endpoint: https://api.openai.com/v1
  model: gpt-4o
  apiKey: ${SHARKO_AI_API_KEY}
```

The AI assistant, floating chat, and general-purpose tools (query cluster health, explain drift, troubleshoot) are preserved as Sharko features.

---

## 8. Settled Decisions

These are resolved. Do not re-litigate.

| Decision | Rationale |
|---|---|
| ArgoCD only, no Flux | ArgoCD won (~60% adoption). Abstracting Flux means building for a minority with no ability to test. |
| Server-first, CLI as thin client | One secure place for all credentials. Same API for all consumers. Like ArgoCD's own architecture. |
| CLI never generates ApplicationSets | AppSet templates contain deeply evolved production logic. CLI generates data files (values, config) only. |
| One repo | Solo maintainer. Multiple repos = multiple CI pipelines, releases, and READMEs that drift apart. |
| Provider interface worth it | `KubernetesSecretProvider` alone justifies it. Changes README from "requires AWS" to "supports AWS, Vault, GCP, Azure." |
| Coupling contract = cluster name matches values file name | The ONLY coupling point. Simple, predictable. |
| ArgoCD auth via account token | Not ServiceAccount/in-cluster RBAC. Uses ArgoCD's own account system with its own RBAC. |
| No auto-rollback of ArgoCD state | Cascade deletion risk. Partial success is safer than automatic cleanup. |
| Sync API for v1.0.0 | Async (202 + job polling) is a v1.x consideration for batch operations. |

---

## 9. Build Sequence

| Step | What | Depends On | Milestone |
|---|---|---|---|
| 1 | Strip dead code | — | |
| 2 | Rename module path + cobra + `--version` | Step 1 | |
| 3 | Rebrand (env vars, headers, UI, Helm, configs) | Step 2 | |
| 4 | Verify (Go build, UI build, tests, Docker) | Step 3 | **v0.1.0** |
| 5 | Write API contract document, user review | Step 4 | **Gate** |
| 6 | Provider interface (`internal/providers/`) | Step 5 | |
| 7 | Orchestrator (`internal/orchestrator/`) | Step 6 | |
| 8 | Write API endpoints + dual auth | Step 7 | |
| 9 | CLI thin client | Step 8 | |
| 10 | Templates cleanup + embed | Step 7 | |
| 11 | Docs + README + release | Steps 9+10 | **v1.0.0** |
