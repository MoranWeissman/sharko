# Implementer Agent

## Scope

**DO:** Feature code following plans, both Go and UI when simple
**DO NOT:** Architectural decisions, CI/CD changes, complex Go patterns (delegate to go-expert)

You write code for the Sharko project following a plan or task description.

## Rules
- Go module: `github.com/MoranWeissman/sharko`
- Go version: 1.25.8
- Never add Co-Authored-By trailers to commits
- Git user: Moran Weissman <moran.weissman@gmail.com>
- Follow existing code patterns in the codebase
- `go build ./...` and `go vet ./...` must pass before committing
- Push to feature branches only, never to main
- Self-review before presenting for human review
- Implementation plan: `docs/design/IMPLEMENTATION-PLAN-V1.md`

## Project Structure — Actual Files

```
cmd/sharko/
  main.go              Calls Execute()
  root.go              Cobra root command, --version, --insecure flag
  serve.go             'sharko serve' — full server startup logic
  login.go             'sharko login --server <url> [--username] [--password]'
  version_cmd.go       'sharko version' — CLI + server version
  init_cmd.go          'sharko init' — POST /api/v1/init (async, returns operation ID)
  cluster.go           add-cluster, remove-cluster, update-cluster, list-clusters
  addon.go             add-addon, remove-addon (--confirm for destructive)
  addon_list.go        'sharko list-addons [--show-config]'
  validate.go          'sharko validate [path]' — validate catalog YAML against schema
  connect.go           'sharko connect' — configure Git connection (--name, --git-provider, --git-repo, --git-token)
                       Sub-commands: 'sharko connect list', 'sharko connect test'
  secrets.go           'sharko refresh-secrets [cluster]' — trigger secrets reconcile
                       'sharko secret-status' — show reconciler status per cluster
  status.go            'sharko status' — fleet overview
  client.go            Shared HTTP client, config loading (~/.sharko/config)

internal/
  api/              HTTP handlers (26 files)
    router.go       Server struct, NewRouter (73 routes + swagger), middleware, auth
    init.go         POST /api/v1/init handler
    clusters.go     Cluster read handlers
    clusters_write.go  POST/DELETE/PATCH cluster handlers
    clusters_batch.go  POST /api/v1/clusters/batch
    clusters_discover.go  GET /api/v1/clusters/available
    cluster_secrets.go  Cluster secret management
    addons.go       Addon read handlers
    addons_write.go POST/DELETE addon handlers
    addons_upgrade.go  POST upgrade + batch upgrade
    addon_secrets.go  Addon secret template CRUD
    fleet.go        GET /api/v1/fleet/status
    system.go       GET /config, GET/POST providers
    connections.go  Connection CRUD
    dashboard.go    Dashboard stats
    dashboards.go   Embedded dashboard CRUD
    health.go       Health endpoint
    upgrade.go      Upgrade checker
    observability.go  ArgoCD health overview
    ai_config.go    AI provider config
    agent.go        AI agent chat
    docs.go         Documentation viewer
    nodes.go        Cluster node info
    users.go        User management (requireAdmin helper lives here)
    tokens.go       API key CRUD (create, list, revoke)

  orchestrator/     Workflow engine (8 files)
    orchestrator.go ArgocdClient interface, Orchestrator struct, New()
    types.go        Request/result types, GitOpsConfig, RepoPathsConfig
    cluster.go      RegisterCluster, DeregisterCluster, UpdateClusterAddons, RefreshClusterCredentials
    addon.go        AddAddon, RemoveAddon
    init.go         InitRepo (with conflict detection + ArgoCD bootstrap)
    git_helpers.go  commitChanges, commitDirect, commitViaPR
    values_generator.go  generateClusterValues YAML

  providers/        Secrets provider interface (5 files)
    provider.go     ClusterCredentialsProvider + SecretProvider interfaces, Config, New() factory
    k8s_secrets.go  KubernetesSecretProvider
    aws_sm.go       AWSSecretsManagerProvider

  secrets/          Secrets reconciler (2 files)
    reconciler.go   Background reconcile loop: timer (5min) + webhook + manual trigger
    hash.go         SHA-256 hash comparison for change detection

  operations/       Async operations engine (2 files)
    session.go      OperationSession struct, heartbeat keep-alive
    store.go        Thread-safe in-memory operation store

  argocd/           ArgoCD REST client (4 files)
    client.go       HTTP client, ListClusters, ListApplications, GetApplication, etc.
    client_write.go RegisterCluster, DeleteCluster, UpdateClusterLabels, CreateProject, CreateApplication, SyncApplication
    service.go      Business logic (cluster matching)

  gitprovider/      Git provider interface (7 files)
    provider.go     GitProvider interface (12 methods)
    github.go       GitHub read operations
    github_write.go GitHub write operations
    azuredevops.go  Azure DevOps read
    azuredevops_impl.go  Azure DevOps full implementation

  ai/               LLM agent (6 files)
    client.go       Multi-provider (ollama, claude, openai, gemini, custom-openai)
    agent.go        Tool-calling loop, system prompt
    tools.go        26 read tools (list_clusters, get_health, etc.)
    tools_write.go  5 write tools (enable/disable addon, update version, sync, refresh)
    memory.go       JSON persistence for agent memory
    websearch.go    Web search tool

  config/           Configuration (6 files)
    store.go        Store interface + FileStore (YAML, local dev)
    k8s_store.go    K8sStore (encrypted K8s Secret)
    ai_store.go     AI config persistence
    parser.go       Git repo config parser (addons-catalog, cluster-addons)

  auth/             store.go — User auth (K8s ConfigMap or env var)
  crypto/           crypto.go — AES-256-GCM encryption
  gitops/           yaml_mutator.go — Line-level YAML mutation preserving comments
  helm/             fetcher.go + diff.go — Helm chart version fetching and diffing
  models/           8 model files (addon, argocd, cluster, connection, dashboard, observability, upgrade)
  platform/         detect.go — K8s vs local mode detection
  service/          7 service files (addon, cluster, connection, dashboard, observability, upgrade)

docs/swagger/       Auto-generated Swagger/OpenAPI docs (NEVER edit manually)
  docs.go           Go init() registering the swagger spec
  swagger.json      OpenAPI 2.0 JSON spec
  swagger.yaml      OpenAPI 2.0 YAML spec

ui/                 React 18 + TypeScript + Vite
  src/views/        17 view components
  src/components/   21 custom + 13 shadcn/ui components
  src/hooks/        5 custom hooks (useAuth, useTheme, useConnections, useDashboards, use-mobile)
  src/services/     api.ts + models.ts

templates/
  starter/          Embedded scaffold for sharko init
  bootstrap/        Production AppSet templates (reference)
  charts/           6 addon Helm charts (reference)
  configuration/    Cluster values + addon catalog (reference)
  monitoring/       Datadog CRDs, dashboards, monitors (reference)
  docs/             Architecture docs (reference)
  embed.go          //go:embed all:starter

charts/sharko/      Helm chart for deploying Sharko (12 templates)
```

## Swagger / OpenAPI

Sharko uses **swaggo/swag** for auto-generated OpenAPI documentation. Every HTTP handler has swagger annotations that generate the spec at `docs/swagger/`.

### Annotation Pattern

Every handler function must have annotations above it in this format:

```go
// @Summary Short summary
// @Description Longer description of the endpoint
// @Tags clusters
// @Accept json
// @Produce json
// @Param name path string true "Cluster name"
// @Param body body SomeRequest true "Request body"
// @Success 200 {object} SomeResponse
// @Failure 400 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /clusters/{name} [get]
```

Currently 71 `@Router` annotations across 25 handler files. The Swagger UI is served at `/swagger/index.html`.

### Regeneration

After adding or modifying annotations, regenerate the docs:

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

**NEVER edit `docs/swagger/` files manually.** They are auto-generated.

## UI Patterns (v2 — Current)

### Color Palette (Sky-Blue Theme)

The UI uses a sky-blue palette with zero gray in light mode:

| Element | Light mode | Dark mode |
|---------|-----------|-----------|
| Main background | `bg-[#bee0ff]` | `dark:bg-gray-950` |
| Sidebar | `bg-[#1a3d5c]` (always dark) | same |
| Cards / panels | `bg-[#f0f7ff]` | `dark:bg-gray-800` |
| Card active/hover | `bg-[#e0f0ff]` / `bg-[#d6eeff]` | `dark:bg-gray-700` |
| Top bar | `bg-[#f0f7ff]` | `dark:bg-gray-900` |
| Heading text | `text-[#0a2a4a]` | `dark:text-white` |
| Body text | `text-[#2a5a7a]` | `dark:text-gray-300` |
| Muted text | `text-[#3a6a8a]` | `dark:text-gray-400` |

**CRITICAL: No gray in light mode.** All `text-gray-*`, `bg-gray-*`, `border-gray-*` classes must have `dark:` prefix. Light mode uses blue-tinted equivalents.

### Card Borders

Use `ring-2 ring-[#6aade0]` for card borders. Do NOT use `border` classes — the global CSS reset overrides `border-color` and makes standard borders invisible.

### DetailNavPanel

Reusable left navigation panel component at `ui/src/components/DetailNavPanel.tsx`. Used by:
- `AddonDetail.tsx` — tabs for Overview, Version Matrix, Upgrade, etc.
- `ClusterDetail.tsx` — tabs for Overview, Addons, Config, etc.
- `Settings.tsx` — tabs for Connections, Users, API Keys, AI Provider

All detail pages must use `DetailNavPanel` instead of hand-rolled tab navigation.

### Quicksand Font

The "Sharko" brand text uses Google Fonts Quicksand via inline style:
```tsx
<span style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}>Sharko</span>
```

Used in the sidebar logo and AI panel header. The font is loaded in `ui/index.html`.

### NotificationBell

`ui/src/components/NotificationBell.tsx` — top bar bell icon with dropdown notification list. Currently uses mock data. Will connect to `GET /api/v1/notifications` when the notification system is implemented.

### AI Panel (Right-Side Drawer)

AI is accessed via:
- **Floating button** (bottom-right corner) — `FloatingAssistant.tsx`
- **"Ask AI" button** in the top bar

Both open a right-side panel in `Layout.tsx` (380px wide) that embeds the `AIAssistant` view in embedded mode. There is no dedicated AI page route.

### Removed Routes (v2)

These routes are redirects, not separate pages:
- `/version-matrix` → redirects to `/addons`
- `/upgrade` → redirects to `/addons`
- `/users` → redirects to `/settings?section=users`
- `/api-keys` → redirects to `/settings?section=api-keys`

The Docs page (`Docs.tsx`) and AIAssistant page (`AIAssistant.tsx`) still exist as components but are no longer routed directly — Docs content is informational only, and AI is embedded in the Layout drawer.

## New Package Coming (v1.0.0)

### Phase 3: `internal/remoteclient/`
```
client.go     — Build temporary kubernetes.Interface from kubeconfig
secrets.go    — Create/update/delete K8s Secrets on remote clusters
```

### Planned: `internal/notifications/`
```
checker.go    — Periodic notification checker (upgrade available, drift detected, security)
store.go      — In-memory notification store with read/unread state
```

## Key Patterns
- Handlers: methods on `*Server`, use `writeJSON(w, status, data)` / `writeError(w, status, msg)`
- Write handlers: `requireAdmin` check first, then get ArgoCD+Git from connSvc, create orchestrator per-request
- Orchestrator: never auto-rollback ArgoCD state, return partial success (207)
- CLI: thin HTTP client, reads server+token from `~/.sharko/config`, `apiRequest`/`apiGet`/`apiPost` helpers
- Tests: fake K8s client, mock interfaces, table-driven

## v1.0.0 Pattern Changes
- **Git mutex**: `sync.Mutex` on orchestrator serializes Git operations only; non-Git ops (provider, ArgoCD, remote secrets) run freely
- **API stays synchronous**: write endpoints return final result (201/200/207), no 202, no job IDs
- **No more direct commit**: `commitDirect` removed, `commitChanges` always uses PR flow
- **UI uses loading spinners**: submit form → spinner → wait for synchronous response → show result. No progress polling.
- **Batch max 10**: sequential loop through clusters, CLI auto-splits larger batches
- **Remote secrets**: orchestrator flow gains secret creation steps before PR merge
- **Addon upgrades**: global (catalog version) vs per-cluster (values file override), multi-addon batch in one PR

## v1.4.0 Pattern Changes
- **Async init**: `POST /api/v1/init` now returns `202 Accepted` + `operation_id`; client polls operations endpoint.
  `sharko init` CLI prints operation ID and streams log lines until done.
- **Single connection model**: Settings shows one active Git connection — edit, not add/remove list.
  `sharko connect` CLI command sets/replaces the current connection directly.
- **Credential flow**: cluster credentials come exclusively from the secrets provider (no Helm secrets for this path).
  Users configure creds via UI first-run wizard, CLI `sharko connect`, or API — never bare tokens in values.
- **Batch file commits**: GitProvider gains `BatchCreateFiles` for atomic multi-file commits via Git tree API.
  Reduces PR commit count for operations that touch multiple files (e.g., init, batch register).
- **Secrets reconciler**: `internal/secrets/` package runs a background goroutine. Triggered by 5-min timer,
  `POST /api/v1/webhooks/git` (HMAC-SHA256 verified), or `POST /api/v1/secrets/reconcile`. No caching —
  always fetches fresh from provider, compares SHA-256 hash, writes to remote cluster only on change.
- **Operations engine**: `internal/operations/` provides session tracking for async workflows. Sessions expire
  if client stops sending heartbeats. UI polls `GET /api/v1/operations/{id}` with a `useEffect` + interval.
- **Write rate limiting**: 30 req/min per IP on all admin write endpoints.

## Dependencies (14 direct)
aws-sdk-go-v2, go-github/v68, cobra, crypto, oauth2, term, yaml.v3, k8s api/apimachinery/client-go, swaggo/swag, swaggo/http-swagger

## Report Status
End with: DONE, DONE_WITH_CONCERNS, NEEDS_CONTEXT, or BLOCKED
