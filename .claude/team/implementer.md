# Implementer Agent

## Scope

**DO:** Feature code following plans, both Go and UI when simple
**DO NOT:** Architectural decisions, CI/CD changes, complex Go patterns (delegate to go-expert)

You write code for the Sharko project following a plan or task description.

## Rules
- Go module: `github.com/MoranWeissman/sharko`
- Go version: 1.25.8
- Never add Co-Authored-By trailers to commits
- Never use `--no-verify` or `-c commit.gpgsign=false`
- Git user: Moran Weissman <moran.weissman@gmail.com>
- Follow existing code patterns in the codebase
- `go build ./...` and `go vet ./...` must pass before committing
- Stay on your worktree branch. NO `git push`. NO retag. NO ref-mutation outside your branch.
- Self-review before returning to the orchestrator (which will cherry-pick + open PR + auto-merge
  per `feedback_auto_merge_when_green`).

### Dispatch protocol (worktree-isolated)

You run inside `Agent(isolation: "worktree")` on a `worktree-agent-<hash>` branch. Your contract:

1. Read your own role file + tech-lead.md + any other role file embedded in the dispatch prompt.
2. Implement the story; one focused commit per story unless instructed otherwise.
3. Run quality gates locally (see CHECK section in tech-lead.md).
4. Commit on your worktree branch. Return to the orchestrator — DO NOT push, DO NOT open a PR.

The orchestrator cherry-picks your commit onto the sprint branch (from a clean main checkout, not
your worktree) and opens / auto-merges the sprint PR.

### Edit-to-main-repo drift protocol (mandatory)

Edit/Write tools use the literal filesystem path. An absolute path under
`/Users/weissmmo/projects/github-moran/sharko/...` lands in MAIN, not your worktree. This bit 4
of 11 agents in a single recent session. **Mandatory:**

- Use `$(git rev-parse --show-toplevel)/<relative>` prefix OR relative paths from the worktree.
- After every batch of writes:
  `cd /Users/weissmmo/projects/github-moran/sharko && git status -s` — must be clean.
- If main got polluted: `cd <main> && git checkout -- <files>`, re-apply inside the worktree.

## Project Structure — Actual Files

```
cmd/sharko/
  main.go              Calls Execute()
  root.go              Cobra root command, --version, --insecure flag
  serve.go             'sharko serve' — full server startup logic
                       (wires prTracker.SetOnMergeFn → clusterreconciler.Trigger)
  login.go             'sharko login --server <url> [--username] [--password]'
  reset_admin.go       'sharko reset-admin' — local admin password reset
  version_cmd.go       'sharko version' — CLI + server version
  init_cmd.go          'sharko init' — POST /api/v1/init (async, returns operation ID)
  cluster.go           add-cluster, remove-cluster, update-cluster, list-clusters,
                       test-cluster (POST /api/v1/clusters/{name}/test)
  adopt.go             'sharko adopt-cluster' — POST /api/v1/clusters/{name}/adopt
  unadopt.go           'sharko unadopt-cluster' — POST /api/v1/clusters/{name}/unadopt
  discover.go          'sharko discover' — provider/ArgoCD discovery
  batch.go             'sharko add-clusters' — batch register (max 10)
  addon.go             add-addon, remove-addon (--confirm), list-addons, configure-addon
  upgrade.go           upgrade-addon, upgrade-addons (multi-addon batch)
  validate.go          'sharko validate' — LEGACY pre-envelope validator (kept during V125,
                       slated for V126 removal per yaml-schema-migration.md)
  validate_config.go   'sharko validate-config <file-or-dir>' — V125-1-9 envelope-aware
                       JSON-Schema validator used by the validate-sharko-config CI job.
                       Accepts ONE path arg (file or directory). Flags: --quiet/-q.
  connect.go           'sharko connect' — configure Git connection (--name, --git-provider,
                       --git-repo, --git-token); sub-commands: list, test
  secrets.go           'sharko refresh-secrets [cluster]' + 'sharko secret-status'
  token.go             'sharko token create|list|revoke' — API key management
  pr.go                'sharko pr list|wait' — PR tracker queries
  user.go              'sharko user' — user management
  status.go            'sharko status' — fleet overview
  client.go            Shared HTTP client, config loading (~/.sharko/config)

cmd/schema-gen/        V125-1-9 — introspects envelope-Go types via invopop/jsonschema and emits
                       committed schemas at BOTH docs/schemas/*.v1.json AND
                       internal/schema/*.v1.json (writeSchemaToBoth helper).
                       The schemas-up-to-date CI job re-runs this and fails on diff.
cmd/catalog-sign/      cosign-keyless catalog signer (v1.23 — workflow_run cert SAN, modern
                       Sigstore Bundle format).
cmd/gen-provider-types/  Provider type-mapping codegen — keeps providers.AddonSecretProviderConfig
                       / ClusterTestProviderConfig / ClusterRegSourceProviderConfig in sync.
                       provider-types-up-to-date CI job re-runs this and fails on diff.

internal/
  api/              HTTP handlers (28 files)
    router.go       Server struct, NewRouter (76 routes + swagger), middleware, auth
    init.go         POST /api/v1/init handler
    clusters.go     Cluster read handlers (includes managed/discovered split, ?sort=/?filter= params)
    clusters_write.go  POST/DELETE/PATCH cluster handlers
    clusters_batch.go  POST /api/v1/clusters/batch
    clusters_discover.go  GET /api/v1/clusters/available
    clusters_test.go  POST /api/v1/clusters/{name}/test — connectivity check
    clusters_adopt.go  POST /api/v1/clusters/{name}/adopt — adopt discovered clusters into Git
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
    notifications.go  GET /api/v1/notifications, POST /api/v1/notifications/{id}/read

  orchestrator/     Workflow engine (9 files)
    orchestrator.go ArgocdClient interface, Orchestrator struct, New()
    types.go        Request/result types, GitOpsConfig, RepoPathsConfig
    cluster.go      RegisterCluster, DeregisterCluster, UpdateClusterAddons, RefreshClusterCredentials
    cluster_adopt.go  AdoptCluster — adopts discovered ArgoCD clusters into Git
    addon.go        AddAddon, RemoveAddon
    init.go         InitRepo (with conflict detection + ArgoCD bootstrap)
    git_helpers.go  commitChanges, commitViaPR (+ branch cleanup after auto-merge)
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

  ai/               LLM agent
    client.go       Multi-provider (ollama, claude, openai, gemini, custom-openai)
    agent.go        Tool-calling loop, system prompt; SimplePrompt for one-shot LLM calls
    tools.go        Read tools (list_clusters, get_health, etc.)
    tools_write.go  Write tools (enable/disable addon, update version, sync, refresh)
    memory.go       JSON persistence for agent memory
    websearch.go    Web search tool

  schema/           V125-1-9 — Envelope[T] generic + IsEnveloped + DefaultValidator
                    (santhosh-tekuri/jsonschema v5) + generator.go (invopop/jsonschema)
                    + embedded JSON schemas (addon-catalog.v1.json, managed-clusters.v1.json)
  clusterreconciler/  V125-1-8 — Reconciler struct + Trigger() channel + ownership label
                    (labels.go: LabelManagedBy/LabelValueSharko + IsManagedBySharko +
                    ApplyManagedBySharkoLabel). 30s DefaultTickInterval + post-merge Trigger()
                    via prTracker.SetOnMergeFn. Uses models.LoadManagedClusters (envelope-aware).

  config/           Configuration (6 files)
    store.go        Store interface + FileStore (YAML, local dev)
    k8s_store.go    K8sStore (encrypted K8s Secret)
    ai_store.go     AI config persistence
    parser.go       Git repo config parser (addons-catalog, cluster-addons)

  auth/             store.go — User auth (K8s ConfigMap or env var)
  authz/            RBAC (Viewer/Operator/Admin) + action→role map
  crypto/           AES-256-GCM encryption
  gitops/           Envelope-aware YAML mutators:
                    yaml_mutator.go         — legacy line-level mutators (still in use for addons)
                    yaml_mutator_cluster.go — V125-1-9 parse-mutate-marshal replacement for the
                                              cluster mutators; defers to models.SaveManagedClusters
                                              (envelope-aware writer)
  helm/             fetcher.go + diff.go — Helm chart version fetching and diffing
  models/           addon/argocd/cluster/connection/dashboard/observability/upgrade
                    + V125-1-9 envelope-aware LoadManagedClusters / SaveManagedClusters readers
  platform/         detect.go — K8s vs local mode detection
  prtracker/        PR lifecycle tracker; SetOnMergeFn callback fans merge events to consumers
  service/          7 service files (addon, cluster, connection, dashboard, observability, upgrade)

docs/swagger/       Auto-generated Swagger/OpenAPI docs (NEVER edit manually)
  docs.go           Go init() registering the swagger spec
  swagger.json      OpenAPI 2.0 JSON spec
  swagger.yaml      OpenAPI 2.0 YAML spec

ui/                 React 18 + TypeScript + Vite + Tailwind + shadcn/ui
  src/views/        view components (Dashboard, Clusters, AddonCatalog, Settings, etc.)
  src/components/   custom + shadcn/ui components
  src/hooks/        custom hooks (useAuth, useTheme, useConnections, useDashboards, use-mobile)
  src/services/     api.ts + models.ts

tests/e2e/          V125-1-13 e2e harness
  harness/          apiclient_*.go (per-domain), argocd.go, auth.go, fixtures.go, ghmock.go,
                    gitfake* (in-cluster gitfake Pod for kind multi-cluster runs), kind.go,
                    sharko_helm* (Wave-D helm-mode harness)
  lifecycle/        addon, cluster, catalog, dashboard, init, pr, reconciler, ai, auth, values
                    domain tests. Run via `make test-e2e-fast` (~30s, no kind) or
                    `make test-e2e` (~10-15 min, kind-backed) / `make test-e2e-helm`.

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

**MANDATORY: After adding any new endpoint, regenerate swagger docs before committing. CI will fail otherwise.**

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

## Schema / Envelope Pattern (V125-1-9)

Every Sharko-owned YAML file (managed-clusters.yaml, addon-catalog.yaml) ships as:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: [...]
```

Read path: `models.LoadManagedClusters` / `catalog.LoadAddonCatalog` → IsEnveloped detect →
`schema.DefaultValidator.Validate(filename, body)` → unmarshal `Spec`.
Write path: `models.SaveManagedClusters(spec)` always emits the full envelope (the file header
comment is prepended on every save).
Legacy bare-YAML reads still work during V125 (validate-skip) and are removed in V126.

If you add a new envelope-shaped file or change the Spec type:
1. Update the Go envelope type (e.g. `internal/models/cluster.go`).
2. Run `go run ./cmd/schema-gen` — emits to BOTH `docs/schemas/*.v1.json` AND
   `internal/schema/*.v1.json` via `writeSchemaToBoth`.
3. Commit both schema files. The `schemas-up-to-date` CI job fails on diff.
4. Update `docs/site/operator/yaml-schema-migration.md` if user-visible.
5. Add a YAML sample under `docs/site/configuration/` so `sharko validate-config` exercises it.

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

## v1.8.0 Feature Additions

### AI SimplePrompt
`internal/ai/agent.go` — a lightweight `SimplePrompt(ctx, prompt string)` method that sends a single non-streaming request to the configured LLM without entering the tool-calling loop. Used for generating addon summaries from release notes. Returns `(string, error)`. No tool access, no memory.

### Addon dependsOn Field
`internal/models/addon.go` — `AddonCatalogEntry` now includes `DependsOn []string`. When set, the orchestrator generates ArgoCD sync wave annotations to enforce deployment order. The `addons_write.go` handler validates the dependency graph before accepting the request.

**Cycle detection**: `internal/orchestrator/addon.go` — `validateDependencyGraph(catalog []AddonCatalogEntry) error` performs a topological sort (DFS with visit marking). Returns a descriptive error identifying the cycle if one is detected. Called on every `AddAddon` and `PATCH /api/v1/addons/{name}` request.

### Audit Log
`internal/audit/` package:
- `logger.go` — `Logger` struct with `Log(entry AuditEntry)` method. Ring buffer (configurable size, default 1000). Thread-safe via `sync.RWMutex`.
- `entry.go` — `AuditEntry` struct: `ID`, `Timestamp`, `Actor`, `Action`, `Target`, `Result`, `Detail`.
- Injected into all write handlers in `internal/api/`. Handler calls `s.audit.Log(...)` after each write operation.
- `GET /api/v1/audit` returns the log filtered by query params (`cluster`, `addon`, `limit`, `before`).

### GCP / Azure Provider Stubs
`internal/providers/gcp.go` — `GCPProvider` struct implementing `ClusterCredentialsProvider`. Both `GetCredentials` and `ListClusters` return `ErrNotImplemented` with a descriptive message. Registered in `New()` factory under key `"gcp"`.

`internal/providers/azure.go` — same pattern for `"azure"` key.

These stubs define the interface boundary for community contributions. The provider selection error message names these stubs explicitly: `"supported providers: aws-sm, k8s-secrets, gcp (stub), azure (stub)"`.

### E2E Framework
`e2e/` directory:
- `e2e_test.go` — main test file, uses `testing.T`, requires `E2E_SHARKO_SERVER` env var
- `helpers.go` — shared helpers: `sharkoClient()`, `waitForOperation()`, `waitForArgoCD()`
- `Makefile` targets: `e2e-setup`, `e2e`, `e2e-teardown`
- Tests cover: cluster registration, addon deployment, init flow, secrets reconciliation

### Init Progress in Settings
`internal/api/init.go` — init handler now stores the `operation_id` in the response for all callers, not just the wizard. The Settings page calls the same `POST /api/v1/init` endpoint and polls `GET /api/v1/operations/{id}` to display progress. No new endpoint needed.

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
