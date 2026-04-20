# Sharko Developer Guide

This guide covers how to build, run, and contribute to Sharko.

---

## Prerequisites

- **Go 1.25+** (see `go.mod` for exact version)
- **Node.js 22+** and npm (for the UI)
- **Docker** (for container builds)
- **Helm 3.x** (for chart development)
- **kubectl** (for testing against a cluster)

---

## Building from Source

### Backend

```bash
# Build the sharko binary
go build -o sharko ./cmd/sharko

# Run tests
go test ./...

# Lint (requires golangci-lint)
golangci-lint run
```

### UI

```bash
cd ui
npm install
npm run build    # Production build (output to ui/dist)
npm run dev      # Development server with hot reload
```

### Combined Dev Mode

```bash
make dev
```

This runs the Go backend and Vite dev server concurrently. The backend serves the API and the Vite dev server proxies API calls to it.

### Docker

```bash
docker build -t sharko:dev .
```

---

## Project Structure

```
sharko/
  cmd/sharko/           CLI entry point (Cobra commands)
    main.go             Root command registration
    root.go             Cobra root command
    serve.go            Server startup and dependency wiring
    login.go            sharko login command
    init_cmd.go         sharko init command
    cluster.go          Cluster management commands (add-cluster, remove-cluster, update-cluster, list-clusters)
    batch.go            Batch cluster registration (add-clusters)
    addon.go            Addon management commands (add-addon, remove-addon)
    upgrade.go          Addon upgrade commands (upgrade-addon, upgrade-addons)
    token.go            API key management commands (token create/list/revoke)
    status.go           Cluster status overview command
    version_cmd.go      Version command
    client.go           HTTP client helpers for CLI commands

  internal/
    api/                HTTP handlers (thin glue code)
      router.go         Route registration, Server struct, middleware
      init.go           POST /api/v1/init handler
      clusters_write.go Cluster write operation handlers (register, deregister, update, refresh)
      clusters_batch.go POST /api/v1/clusters/batch handler
      clusters_discover.go GET /api/v1/clusters/available handler
      cluster_secrets.go Cluster secret list/refresh handlers
      addons_write.go   Addon write operation handlers (add, remove)
      addons_upgrade.go Addon upgrade handlers (upgrade, upgrade-batch)
      addon_secrets.go  Addon secret definition handlers
      tokens.go         API key management handlers (create, list, revoke)
      connections.go    Connection management handlers
      ai_config.go      AI configuration handlers
      users.go          User management handlers
      ...

    orchestrator/       Workflow engine (the brain)
      orchestrator.go   Orchestrator struct, constructor, shared Git mutex
      types.go          Request/response types (GitResult, RegisterClusterRequest, etc.)
      init.go           InitRepo workflow
      clusters.go       Cluster registration/deregistration workflows
      addons.go         Addon management workflows
      upgrade.go        Addon upgrade workflows (global, per-cluster, batch)
      batch.go          Batch cluster registration (MaxBatchSize = 10)
      secrets.go        Addon secret delivery to remote clusters (AddonSecretDefinition)
      git_helpers.go    Git operations (always via PR; auto-merge when configured)
      values_generator.go Cluster values file generation
      smart_values.go   v1.21 smart-values writer + chart-name unwrap
      unwrap_globals.go v1.21 Bundle 5 — legacy `<addon>:` wrap detector + unwrapper

    providers/          Secrets provider interface + implementations
      provider.go       ClusterCredentialsProvider interface
      aws_sm.go         AWS Secrets Manager implementation
      k8s_secrets.go    Kubernetes Secrets implementation

    argocd/             ArgoCD REST client
      client.go         HTTP client (read operations, connection test)
      client_write.go   Write operations (register/delete cluster, create app)
      service.go        Business logic layer (cluster matching)

    gitprovider/        Git provider interface
      provider.go       GitProvider interface
      github.go         GitHub implementation
      azuredevops.go    Azure DevOps implementation

    gitops/             GitOps configuration and env var parsing

    remoteclient/       Remote cluster Kubernetes client
      client.go         Build a kubernetes.Interface from raw kubeconfig bytes
      secrets.go        List and upsert Kubernetes Secrets on remote clusters

    verify/             Connectivity verification (Stage 1 secret CRUD, Stage 2 ArgoCD stub)
      config.go         Test namespace configuration (env var override)
      errors.go         ErrorCode type, ClassifyError() — 10 ERR_* codes
      result.go         Result struct (success, stage, error code, duration, details)
      stage1.go         Stage 1: K8s secret CRUD cycle
      stage2.go         Stage 2: ArgoCD round-trip (stub)

    observations/       Cluster observations store (ConfigMap-backed via cmstore)
      types.go          ClusterStatus (5-state), Observation, StatusResult
      store.go          Store backed by ConfigMap, RecordTestResult/GetObservation/ListObservations
      status.go         ComputeStatus pure function (derives status from observation + ArgoCD health)
      cache.go          CachedStatusProvider with configurable TTL (default 30s)

    diagnose/           IAM diagnostic tool for remote clusters
      diagnose.go       DiagnoseCluster() — runs permission checks, returns DiagnosticReport
      fixes.go          generateFix() — copy-paste-ready K8s RBAC YAML for failures

    metrics/            Prometheus metrics (20 metric definitions, auto-registered via promauto)
      metrics.go        All metric vars (cluster, addon, reconciler, PR, HTTP, auth)
      middleware.go     HTTP middleware for request counting/duration, NormalizePath()

    cmstore/            ConfigMap-based JSON state store helper
      store.go          Store struct, ReadModifyWrite pattern, Read, size warnings at 800KB

    authz/              RBAC authorization
      authz.go          Role type (Admin/Operator/Viewer), ActionRequirements table, Require/RequireWithResponse

    audit/              In-memory ring buffer audit log (1000 entries default)
      log.go            Log struct, Add/List/ListFiltered/Subscribe (SSE pattern)

    prtracker/          PR lifecycle tracking
      types.go          PRInfo struct (id, url, branch, cluster, operation, status)
      tracker.go        Background poller: polls Git for PR status, emits audit events on merge/close

    ai/                 LLM agent
      client.go         Multi-provider AI client
      agent.go          Tool-calling agent loop
      memory.go         Conversation memory store
      tools.go          Agent tool definitions

    auth/               Authentication
      store.go          User store + API key management (K8s ConfigMap or env var)

    config/             Configuration stores
      store.go          Connection config store (interface + file store)
      k8s_store.go      Encrypted K8s Secret store
      ai_store.go       AI config persistence
      parser.go         Git repo config parser (addons-catalog, cluster-addons)

    advisories/         Chart security & release advisory data (ArtifactHub primary, release-notes fallback)
    helm/               Helm chart fetching and diffing
      fetcher.go        Downloads chart tarballs, extracts values.yaml and Chart.yaml. Release-note
                        GitHub repo lookup now follows this precedence: (1) Chart.yaml `sources[]` —
                        first GitHub URL wins; (2) Chart.yaml `home` — if it is a GitHub URL; (3)
                        `guessGitHubRepo` heuristic. Results are cached per repoURL/chart/version.
      diff.go           YAML diff logic for values.yaml comparison

    service/            Read-only service layer
      connection.go     Connection management
      cluster.go        Cluster queries (via ArgoCD)
      addon.go          Addon catalog queries
      dashboard.go      Dashboard statistics
      observability.go  Observability overview
      upgrade.go        Upgrade recommendation service — scores versions, emits cards with security/breaking flags

    platform/           Runtime detection
      detect.go         K8s vs local mode detection

    models/             Shared data models

  ui/                   React frontend (dashboard)
  templates/            Embedded repo templates
    embed.go            Go embed directive for StarterFS
    starter/            Clean scaffold (what sharko init generates)
  charts/sharko/        Helm chart for deploying Sharko
  docs/                 Documentation
  assets/logo/          Logo and branding assets
```

---

## Key Packages

### orchestrator

The orchestrator is the workflow engine. It coordinates multi-step operations across the secrets provider, ArgoCD client, and Git provider. Each write operation (register cluster, add addon, init repo, upgrade addon) is an orchestrator method.

Key design decisions:

- **No auto-rollback.** If step 3 of 4 fails, the orchestrator returns a partial success response. The user decides whether to retry or clean up.
- **Each method receives all dependencies via the constructor.** The orchestrator is stateless between calls.
- **Git mutex.** A `sync.Mutex` (`gitMu`) is shared across all orchestrator instances and held for the duration of each Git operation. This prevents concurrent PR branches from colliding on the same base commit.

```go
func New(
    gitMu *sync.Mutex,
    credProvider providers.ClusterCredentialsProvider,
    argocd ArgocdClient,
    git gitprovider.GitProvider,
    gitops GitOpsConfig,
    paths RepoPathsConfig,
    templateFS fs.FS,
) *Orchestrator
```

The `gitMu` mutex is created once on the `Server` struct (`internal/api/router.go`) and passed to every orchestrator instance. In tests where concurrency is not under test, pass `nil`.

### Values file shape: when to wrap, when not to (v1.21 Bundle 5)

Sharko writes two kinds of values files. The wrap pattern differs between them:

| File | Path template | Shape | Rationale |
|------|---------------|-------|-----------|
| Global values | `configuration/addons-global-values/<addon>.yaml` | **Top-level chart values** (no `<addon>:` wrap) | Passed straight to Helm via `valueFiles:` in the ApplicationSet template. Must be the chart's own keys at document root. |
| Per-cluster overrides | `configuration/addons-clusters-values/<cluster>.yaml` | Wrapped under `<addon>:` (one file per cluster, many addons) | The ApplicationSet template extracts the matching `<addon>:` section per addon at runtime via `{{ $addonKey | toYaml }}`. |

**If you write to a global values file, do NOT add an `<addon>:` wrap.** The smart-values writer (`internal/orchestrator/smart_values.go`) and the migration helper (`internal/orchestrator/unwrap_globals.go`) both enforce this. Pre-Bundle-5 versions of Sharko got this wrong and the bug caused Helm to silently ignore every global value — see the migration endpoint `POST /api/v1/addons/unwrap-globals` for the fix path applied to user repos.

The per-cluster template block at the BOTTOM of a smart-values-generated global file IS still wrapped under `<addon>:` — that block is meant to be copy-pasted into the per-cluster file (which IS namespaced).

### orchestrator/upgrade.go

Three upgrade methods:
- `UpgradeAddonGlobal(ctx, addonName, newVersion)` — updates `addons-catalog.yaml`
- `UpgradeAddonCluster(ctx, addonName, clusterName, newVersion)` — updates the cluster values file
- `UpgradeAddons(ctx, upgrades map[string]string)` — batch global upgrades in one PR

### service/upgrade.go

The upgrade recommendation service scores versions using advisory data and emits smart recommendation cards.

```go
// GetRecommendations returns scored upgrade recommendations with advisory context.
func (s *UpgradeService) GetRecommendations(ctx context.Context, addonName string) (*models.UpgradeRecommendations, error)
```

`models.UpgradeRecommendations` includes:
- Legacy flat fields (`next_patch`, `next_minor`, `latest_stable`) for backwards compatibility.
- `Cards []RecommendationCard` — scored candidates with `has_security`, `has_breaking`, `cross_major`, and `advisory_summary` fields.
- `Recommended string` — version of the highest-scored card (`is_recommended: true`).

Advisory data is fetched from `internal/advisories/` (ArtifactHub primary, release-notes keyword fallback). Cards are returned even when advisory data is unavailable — flags default to false.

### orchestrator/adopt.go

`AdoptClusters` orchestrates the two-phase adoption of existing ArgoCD clusters:
1. Per-cluster Stage 1 connectivity verification.
2. Per-cluster atomic adoption: create values file, add to `managed-clusters.yaml`, commit as PR.

Rejects clusters managed by another tool (`managed-by` label check). After PR merge, sets the `sharko.sharko.io/adopted` annotation on the ArgoCD cluster secret. Supports dry-run mode.

### orchestrator/unadopt.go

`UnadoptCluster` reverses an adoption:
1. Checks the `adopted` annotation (errors if missing -- use `remove-cluster` instead).
2. Removes `managed-by` label and `adopted` annotation from the ArgoCD secret (keeps the secret).
3. Deletes Sharko-created addon secrets from the remote cluster (best-effort).
4. Creates PR to remove from `managed-clusters.yaml` and delete values file.

### orchestrator/remove.go

`RemoveCluster` orchestrates cluster removal with configurable cleanup scope (`all`, `git`, `none`). Scope controls which resources are cleaned up beyond the Git PR (addon secrets on remote, ArgoCD cluster secret).

### orchestrator/addon_ops.go

`DisableAddon` disables a specific addon on a cluster with configurable cleanup (`all`, `labels`, `none`). Updates the cluster values file, optionally updates `managed-clusters.yaml` labels, and optionally deletes addon secrets from the remote cluster.

### Idempotent Retry Pattern

Write operations that create PRs use `findOpenPRForCluster` to check for existing open PRs before creating new ones. If a previous attempt created a PR but failed in a later step, re-running the operation finds the existing PR instead of creating a duplicate.

```go
// In orchestrator/git_helpers.go
existingPR, err := o.findOpenPRForCluster(ctx, clusterName, "register")
if err == nil && existingPR != nil {
    // Return existing PR info instead of creating a new one
    return existingPR, nil
}
```

This pattern is used in `RegisterCluster`, `AdoptClusters`, and other operations.

### orchestrator/batch.go

`RegisterClusterBatch` registers clusters sequentially. `MaxBatchSize = 10` is enforced at the handler level. Returns a `BatchResult` with per-cluster results and aggregate counts.

### orchestrator/secrets.go

Defines `AddonSecretDefinition` and the `SecretValueFetcher` interface. `SetSecretManagement()` configures addon secret delivery on an orchestrator instance. `createAddonSecrets` fetches values from the provider and calls `remoteclient.EnsureSecret` on the remote cluster.

### remoteclient

The `internal/remoteclient` package builds a `kubernetes.Interface` from raw kubeconfig bytes (returned by the secrets provider). Used by the orchestrator to deliver secrets to remote clusters.

```go
// Build a remote cluster client from raw kubeconfig bytes
client, err := remoteclient.NewClientFromKubeconfig(kubeconfigBytes)

// List Sharko-managed secrets on a remote cluster
secrets, err := remoteclient.ListManagedSecrets(ctx, client, namespace)

// Create or update a K8s Secret on a remote cluster
err = remoteclient.EnsureSecret(ctx, client, namespace, secretName, data)
```

### providers

The `ClusterCredentialsProvider` interface abstracts how cluster kubeconfigs are fetched. Two implementations ship with Sharko:

```go
type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (*Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}
```

- `aws_sm.go` -- AWS Secrets Manager (uses IRSA for auth)
- `k8s_secrets.go` -- Kubernetes Secrets (no cloud dependency)

### argocd

REST client for ArgoCD. Uses account token authentication (Bearer header). Operations include cluster registration, label updates, application sync, and status queries.

### gitprovider

Git provider interface for creating/updating files and opening PRs. Implementations for GitHub and Azure DevOps. All write operations go through `commitChanges` in `orchestrator/git_helpers.go`, which always creates a PR. When `GitOpsConfig.PRAutoMerge` is true, the PR is merged immediately after creation.

**PR merge retry pattern:** Both the GitHub and Azure DevOps implementations retry PR merges when the remote returns HTTP 405 with a "Base branch was modified" message. This race condition occurs when two PRs target the same base branch and one merges between the time another was created and when auto-merge is attempted. The retry logic:

1. Attempt to merge the PR.
2. If HTTP 405 and "Base branch was modified" — wait briefly, then retry.
3. Up to 3 retries total before returning the error.

```go
// Pattern used in github.go and azuredevops.go
const mergePRMaxRetries = 3

for attempt := 0; attempt <= mergePRMaxRetries; attempt++ {
    err = provider.mergePR(ctx, prID)
    if err == nil {
        return nil
    }
    if !isBranchModifiedError(err) || attempt == mergePRMaxRetries {
        return err
    }
    // Brief wait before retry
    time.Sleep(retryDelay)
}
```

This prevents upgrade batch operations from failing when the Git provider rejects a merge due to a concurrent merge.

### api

HTTP handlers. Each handler is a method on the `Server` struct. Handlers are thin — they validate input, call the orchestrator or service layer, and write JSON responses.

**Handler files and their responsibilities:**

| File | Endpoints |
|------|-----------|
| `clusters_write.go` | POST, DELETE, PATCH `/clusters`, POST `/clusters/{name}/refresh`, POST `/clusters/{name}/test` |
| `clusters_batch.go` | POST `/clusters/batch` |
| `clusters_discover.go` | GET `/clusters/available`, POST `/clusters/discover` |
| `clusters_adopt.go` | POST `/clusters/adopt`, POST `/clusters/{name}/unadopt` |
| `cluster_secrets.go` | GET, POST `/clusters/{name}/secrets*` |
| `diagnose.go` | POST `/clusters/{name}/diagnose` |
| `addon_ops.go` | DELETE `/clusters/{name}/addons/{addon}` (disable addon on cluster) |
| `addons_write.go` | POST, DELETE `/addons`, PATCH `/addons/{name}` |
| `addons_upgrade.go` | POST `/addons/{name}/upgrade`, POST `/addons/upgrade-batch` |
| `addon_secrets.go` | GET, POST, DELETE `/addon-secrets*` |
| `tokens.go` | GET, POST, DELETE `/tokens*` |
| `audit.go` | GET `/audit`, GET `/audit/stream` (SSE) |
| `prs.go` | GET `/prs`, GET `/prs/{id}`, POST `/prs/{id}/refresh`, DELETE `/prs/{id}` |

### prtracker

The `internal/prtracker` package tracks pull requests created by Sharko operations. It polls the Git provider for status changes and emits audit events when PRs are merged or closed. State is persisted in a Kubernetes ConfigMap via `cmstore`, so pending PRs survive pod restarts.

```go
// Create a tracker
tracker := prtracker.NewTracker(cmStore, gitProviderFn, auditFn)
tracker.Start(ctx) // background poll loop (default 30s, configurable via SHARKO_PR_POLL_INTERVAL)

// Track a new PR
tracker.TrackPR(ctx, prtracker.PRInfo{
    PRID:      42,
    PRUrl:     "https://github.com/org/repo/pull/42",
    Cluster:   "prod-eu",
    Operation: "register",
    User:      "admin",
    Source:    "cli",
})

// List tracked PRs with filters
prs, _ := tracker.ListPRs(ctx, "open", "prod-eu", "", "")

// Wait for a specific PR
pr, _ := tracker.PollSinglePR(ctx, 42)

// Register a callback for merged PRs (e.g., trigger reconciler)
tracker.SetOnMergeFn(func(pr prtracker.PRInfo) {
    // trigger argosecrets reconciler
})
```

Key behaviors:
- Polls Git provider every 30 seconds (configurable via `SHARKO_PR_POLL_INTERVAL`)
- Emits `pr_merged` and `pr_closed_without_merge` audit events
- Removes merged/closed PRs from tracking automatically
- `ReconcileOnStartup` catches up on changes that occurred while the server was down

### auth (API key support)

The `auth` store manages both session tokens (short-lived, from `POST /auth/login`) and API keys (long-lived, created via `POST /tokens`). The auth middleware in `router.go` checks for an API key first; if not found, it falls back to session token validation. API keys are stored hashed; the plaintext is only returned once at creation time.

### ai

Multi-provider AI client supporting Ollama, OpenAI, Claude, Gemini, and custom OpenAI-compatible endpoints. Includes a tool-calling agent loop for interactive troubleshooting.

**Adding a new AI tool** (`internal/ai/tools.go`):

1. Define the tool schema as a JSON-serializable struct and register it in the tool list:

```go
// In tools.go, add to the toolList slice:
{
    Name:        "get_argocd_cluster_connection",
    Description: "Get the ArgoCD connection status for a specific cluster. Use this when diagnosing x509, auth, or connectivity errors between Sharko and ArgoCD for a cluster.",
    InputSchema: map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "cluster_name": map[string]interface{}{
                "type":        "string",
                "description": "The cluster name to check ArgoCD connection for",
            },
        },
        "required": []string{"cluster_name"},
    },
},
```

2. Add a case to the tool dispatch switch in `agent.go`:

```go
case "get_argocd_cluster_connection":
    clusterName, _ := input["cluster_name"].(string)
    result, err = s.getArgoCDClusterConnection(ctx, clusterName)
```

3. Implement the handler method on the agent struct. The method should call the relevant service or API and return a string result (or an error that the agent surfaces as a tool error).

The `get_argocd_cluster_connection` tool (added in v1.16.0) calls the cluster comparison endpoint and extracts `argocd_connection_status` and `argocd_connection_message`, giving the AI specific error context for diagnosing cluster-level ArgoCD connectivity failures rather than relying on the user to copy-paste error messages.

### verify

Connectivity verification for remote Kubernetes clusters. Runs a two-stage test:

- **Stage 1** — Full secret CRUD cycle: ensure namespace exists, create a test secret, read it back, delete it. Exercises the permissions Sharko needs for addon secret delivery.
- **Stage 2** — ArgoCD round-trip verification (stub, not yet implemented).

Each stage returns a `Result` struct:

```go
type Result struct {
    Success       bool                   `json:"success"`
    Stage         string                 `json:"stage"`
    ErrorCode     ErrorCode              `json:"error_code,omitempty"`
    ErrorMessage  string                 `json:"error_message,omitempty"`
    DurationMs    int64                  `json:"duration_ms"`
    ServerVersion string                 `json:"server_version,omitempty"`
    Details       map[string]interface{} `json:"details,omitempty"`
}
```

Failures are classified into one of 10 error codes via `ClassifyError()`:

| Code | Meaning |
|------|---------|
| `ERR_NETWORK` | Connection refused, DNS failure, dial error |
| `ERR_TLS` | x509 / certificate errors |
| `ERR_AUTH` | Unauthorized, expired token |
| `ERR_RBAC` | Forbidden (insufficient K8s RBAC) |
| `ERR_AWS_STS` | STS GetToken / identity provider failure |
| `ERR_AWS_ASSUME` | AssumeRole failure |
| `ERR_QUOTA` | API throttling / rate limiting |
| `ERR_NAMESPACE` | Admission webhook or namespace errors |
| `ERR_TIMEOUT` | Context deadline exceeded |
| `ERR_UNKNOWN` | Unclassified error |

Test namespace defaults to `sharko-test`, overridden via `SHARKO_TEST_NAMESPACE` env var.

### observations

Cluster observations store — persists per-cluster connectivity test results in a ConfigMap (via `cmstore`). The 5-state status model:

| Status | Meaning |
|--------|---------|
| `Unknown` | No observation recorded yet |
| `Connected` | Stage 1 passed (K8s API reachable, RBAC ok) |
| `Verified` | Stage 2 passed (ArgoCD round-trip confirmed) |
| `Operational` | Has at least one healthy addon in ArgoCD |
| `Unreachable` | Last test failed |

`ComputeStatus(obs, hasHealthyAddon)` is a pure function that derives the status from the last observation and ArgoCD health data.

`CachedStatusProvider` wraps the store with an in-memory TTL cache (default 30s, configurable via `SHARKO_CLUSTER_STATUS_CACHE_TTL`). The `GetStatus` method accepts a `refresh` bool to bypass the cache.

### diagnose

IAM diagnostic tool for remote clusters. `DiagnoseCluster()` runs a series of permission checks (list namespaces, get namespace, create/get/delete secret) and returns a `DiagnosticReport`:

```go
type DiagnosticReport struct {
    Identity        string      `json:"identity"`
    RoleAssumption  string      `json:"role_assumption"`
    NamespaceAccess []PermCheck `json:"namespace_access"`
    SuggestedFixes  []Fix       `json:"suggested_fixes"`
}
```

For each failed check, `generateFix()` produces copy-paste-ready K8s RBAC YAML (ClusterRole/ClusterRoleBinding for namespace access, Role/RoleBinding for secret CRUD). The generated YAML uses a `sharko-access` group as the subject.

### metrics

Prometheus metrics for the entire Sharko server. All metrics are auto-registered with the default registry via `promauto`. 20 metric definitions across 6 categories:

| Category | Metrics | Example |
|----------|---------|---------|
| Cluster | count, status, last_verified, test_duration, test_failures | `sharko_cluster_count{status="Connected"}` |
| Addon | sync_status, health, version, catalog_entries_count | `sharko_addon_sync_status{cluster,addon,status}` |
| Reconciler | runs, duration, last_run, items_checked, items_changed | `sharko_reconciler_runs_total{reconciler,result}` |
| PR | tracked, merge_duration | `sharko_pr_merge_duration_seconds` |
| HTTP | requests_total, request_duration | `sharko_api_requests_total{method,path,status}` |
| Auth | login_total, active_sessions | `sharko_auth_login_total{result}` |

`Middleware(next)` is an HTTP middleware that records request count and duration for every request (except `/metrics` itself). `NormalizePath()` replaces dynamic path segments (cluster names, addon names, etc.) with placeholders like `{name}` to prevent cardinality explosion.

### cmstore

Reusable ConfigMap-based JSON state store. Used by `observations` and other packages that need persistent state without a database.

```go
store := cmstore.NewStore(client, namespace, "my-configmap-name")

// Atomic read-modify-write (serialized with in-process mutex)
err := store.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
    data["key"] = "value"
    return nil
})

// Read-only
data, err := store.Read(ctx)
```

Key behaviors:
- ConfigMap is auto-created on first write with `version: 1`
- State stored as JSON in the `state` key of the ConfigMap's `.data`
- Size warning logged at 800KB (ConfigMap limit is 1MB)
- All read-modify-write operations serialized via `sync.Mutex`

### authz

RBAC authorization with three roles: `Admin` (2), `Operator` (1), `Viewer` (0). Roles are compared numerically — higher values include all lower permissions.

`ActionRequirements` is a declarative map from action strings to minimum role:

```go
// Examples from ActionRequirements:
"cluster.remove":    RoleAdmin,     // destructive — admin only
"cluster.register":  RoleOperator,  // write operation — operator+
"cluster.list":      RoleViewer,    // read — anyone
```

Actions not in the map default to `RoleAdmin` (fail-closed).

Two check functions:
- `Require(r, action) bool` — returns true/false
- `RequireWithResponse(w, r, action) bool` — writes a 403 JSON error if denied, returns false

Role is read from the `X-Sharko-Role` header (set by auth middleware). If no auth headers are present, the request is allowed through (auth not configured).

### audit (updated)

In-memory ring buffer for significant events. Default capacity is 1000 entries.

```go
type Entry struct {
    ID         string    `json:"id"`
    Timestamp  time.Time `json:"timestamp"`
    Level      string    `json:"level"`       // info, warn, error
    Event      string    `json:"event"`       // cluster_registered, pr_created, etc.
    User       string    `json:"user"`        // username or "system"
    Action     string    `json:"action"`      // register, remove, update, test
    Resource   string    `json:"resource"`    // cluster:prod-eu, addon:cert-manager
    Source     string    `json:"source"`      // ui, cli, api, reconciler, webhook
    Result     string    `json:"result"`      // success, failure, partial
    DurationMs int64     `json:"duration_ms"`
    Detail     string    `json:"detail,omitempty"`    // handler-supplied semantic context
    Error      string    `json:"error,omitempty"`
    RequestID  string    `json:"request_id,omitempty"`
}
```

Three retrieval methods:
- `List(limit)` — newest first, all entries up to limit
- `ListFiltered(filter)` — filter by user, action, source, result, since, cluster (default limit 50)
- `Subscribe() (<-chan Entry, unsub func())` — SSE pattern: returns a buffered channel (cap 64) that receives every new entry. Non-blocking fan-out (slow subscribers get drops). Call the returned `unsub` function to close the channel and remove the subscriber.

---

## How to Add a New API Endpoint

1. **Create a handler file** (or add to an existing one) in `internal/api/`:

```go
// internal/api/myfeature.go
package api

import "net/http"

func (s *Server) handleMyFeature(w http.ResponseWriter, r *http.Request) {
    if !s.requireAdmin(w, r) {
        return
    }
    // ... implementation
    writeJSON(w, http.StatusOK, result)
}
```

2. **Register the route** in `internal/api/router.go` inside `NewRouter()`:

```go
mux.HandleFunc("GET /api/v1/myfeature", srv.handleMyFeature)
```

3. **If it's a write operation**, add an orchestrator method in `internal/orchestrator/` and call it from the handler. The handler should get ArgoCD and Git clients from `s.connSvc` and construct the orchestrator with the shared `&s.gitMu` mutex.

4. **Verify**: `go build ./... && go vet ./...`

---

## Adding an Auditable Endpoint

When you add a new mutating handler in `internal/api/`, call `audit.Enrich(r.Context(), audit.Fields{...})` near the top of the handler. The `auditMiddleware` will read these fields and emit ONE audit entry per request after the handler returns.

```go
func (s *Server) handleDoSomething(w http.ResponseWriter, r *http.Request) {
    audit.Enrich(r.Context(), audit.Fields{
        Event:    "thing_done",
        Resource: "thing:" + name,
        Detail:   "extra context",
    })
    // ... handler body ...
}
```

The `audit_coverage_test.go` test fails CI if any new mutating handler lacks `audit.Enrich(`. To exempt a true read-only POST (e.g., a test/diagnose endpoint), add it to the allowlist in `audit_coverage_test.go` with a justification comment.

**Current allowlist** (endpoints that emit their own granular audit events or are non-state-changing): `handleLogin`, `handleLogout`, `handleLoginFailed`, `handleHashPassword`, `handleOperationHeartbeat`, `handleMarkAllNotificationsRead`, `handleAgentChat`, `handleGetAISummary`, `handleTestAIConfig`, `handleGitWebhook`.

---

## How to Add a New CLI Command

1. **Create a command file** in `cmd/sharko/`:

```go
// cmd/sharko/mycommand.go
package main

import "github.com/spf13/cobra"

func init() {
    rootCmd.AddCommand(myCmd)
}

var myCmd = &cobra.Command{
    Use:   "mycommand",
    Short: "Description of the command",
    RunE: func(cmd *cobra.Command, args []string) error {
        cfg, err := loadClientConfig()
        if err != nil {
            return err
        }
        // Make HTTP request to the Sharko API
        // Print result
        return nil
    },
}
```

2. The command file's `init()` registers it with the root command automatically.

3. Use `client.go` helpers for HTTP requests to the Sharko server.

---

## How to Write a Custom Secrets Provider

1. **Implement the interface** in a new file under `internal/providers/`:

```go
// internal/providers/vault.go
package providers

type VaultProvider struct {
    addr  string
    token string
}

func NewVaultProvider(addr, token string) *VaultProvider {
    return &VaultProvider{addr: addr, token: token}
}

func (v *VaultProvider) GetCredentials(clusterName string) (*Kubeconfig, error) {
    // Fetch kubeconfig from Vault
}

func (v *VaultProvider) ListClusters() ([]ClusterInfo, error) {
    // List available clusters from Vault
}
```

2. **Register in the factory** in `internal/providers/provider.go`:

```go
func New(cfg Config) (ClusterCredentialsProvider, error) {
    switch cfg.Type {
    case "aws-sm":
        return NewAWSSecretsManagerProvider(cfg.Region, cfg.Prefix)
    case "k8s-secrets":
        return NewK8sSecretsProvider(cfg.Namespace)
    case "vault":
        return NewVaultProvider(cfg.Addr, cfg.Token) // Add this
    default:
        return nil, fmt.Errorf("unknown provider type: %s", cfg.Type)
    }
}
```

---

## Testing Patterns

### Mock Interfaces

All external dependencies are behind interfaces. Write tests by providing mock implementations:

```go
type mockArgocdClient struct{}

func (m *mockArgocdClient) RegisterCluster(ctx context.Context, name, server string, caData []byte, token string, labels map[string]string) error {
    return nil
}
// ... implement other methods
```

### Fake Kubernetes Client

For providers that interact with Kubernetes, use the fake client:

```go
import "k8s.io/client-go/kubernetes/fake"

func TestK8sProvider(t *testing.T) {
    client := fake.NewSimpleClientset()
    // ... create fake secrets, test provider methods
}
```

### Running Tests

```bash
# All tests
go test ./...

# Specific package
go test ./internal/orchestrator/...

# With coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### UI accessibility (v1.21+)

New UI surfaces shipped from v1.21 onward target **WCAG 2.1 AA**: keyboard navigation, focus rings on every interactive element, semantic landmarks (`role="navigation"` / `role="main"`), and contrast ratios that pass `axe-core` with zero violations. Existing pages predate the target and are tracked for a v1.22 retrofit (per the v1.21 design's out-of-scope list).

When adding a new page or component:

1. Run the existing axe suite: `cd ui && npm test -- a11y`.
2. Add a fresh `describe(...)` block in `ui/src/__tests__/a11y.test.tsx` that mounts your page and asserts zero violations. The Marketplace block is the reference template.
3. Manual keyboard pass: tab through every interactive element; verify focus is visible; verify Esc closes any modal you opened.

The CI gate enforces zero a11y violations on the pages currently tested — extending the suite is part of the story for any new UI page.

---

## PR Workflow

1. Create a feature branch from `main`:
   ```bash
   git checkout -b feat/my-feature main
   ```

2. Make changes, commit with descriptive messages

3. Push to the remote:
   ```bash
   git push -u origin feat/my-feature
   ```

4. Open a pull request against `main`

5. Address review feedback, push additional commits

6. Merge after approval

Never push directly to `main`. All changes go through feature branches and pull request review.

Every PR push triggers a Docker build that publishes `ghcr.io/moranweissman/sharko:pr-<NUM>` (and a sha-suffixed variant for traceability) and comments the exact `helm upgrade` command on the PR — use it to test the change against a real cluster before merging. Tags are deleted when the PR is closed.

---

## Curated Catalog (v1.21)

The Sharko binary ships an embedded curated addon catalog used by the Marketplace UI's Browse tab.

### Files

- `catalog/addons.yaml` — single YAML file with the curated entry list (one stanza per addon under `addons:`).
- `catalog/schema.json` — JSON Schema reference for the entry shape (also used by the `catalog-validate` CI workflow once it lands in v1.21 Epic 8).
- `catalog/embed.go` — top-level `catalog` Go package that holds the `//go:embed` directives. Exposes `catalog.AddonsYAML()` and `catalog.SchemaJSON()` helpers that return copies of the embedded bytes.
- `internal/catalog/loader.go` — parses + validates the YAML at startup. Strict on required fields and on the closed enums for `category` / `curated_by`; tolerant of unknown fields so older binaries can parse newer catalogs (forward compatibility per design §4.2).
- `internal/catalog/search.go` — in-memory filter predicate: free-text on name/description/maintainers, plus filters for `category`, `curated_by` (AND match), `license`, `min_score`, `min_k8s_version`, `include_deprecated`.
- `internal/catalog/scorecard.go` — daily background goroutine that calls `https://api.scorecard.dev/projects/github.com/<owner>/<repo>` for every entry whose `source_url` is on GitHub and updates the in-memory `security_score`. Failures are non-fatal; entries keep their last-known score.

### REST API

All endpoints have full swaggo annotations (regenerate with `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal`):

Read-only (require authentication):

- `GET /api/v1/catalog/addons` — list with optional filters: `category`, `curated_by` (comma-separated AND), `license`, `q`, `min_score`, `min_k8s_version`, `include_deprecated`.
- `GET /api/v1/catalog/addons/{name}` — single entry with derived `security_tier` label (Strong / Moderate / Weak) and `security_score_updated` date.
- `GET /api/v1/catalog/addons/{name}/versions` — chart versions for the curated entry (15 min cached, capped at 200 entries).
- `GET /api/v1/catalog/search?q=<term>&limit=20` — blended search across the curated catalog and ArtifactHub. Returns `{curated, artifacthub, artifacthub_error?, stale?}`. Curated hits are returned even when ArtifactHub is unreachable.
- `GET /api/v1/catalog/remote/{repo}/{name}` — proxies one ArtifactHub package detail (used to pre-fill the Configure modal for external charts). 1 h cached, capped at 500 entries.

Tier 1 (admin, audit-tracked):

- `POST /api/v1/catalog/reprobe` — clears the search and package caches, resets the ArtifactHub backoff, and probes `/`. Returns `{reachable, last_error?, probed_at}`.

### ArtifactHub proxy + cache (V121-3)

Sharko proxies ArtifactHub server-side so the browser never calls a third party from the user's network. The plumbing lives in `internal/catalog/`:

- `internal/catalog/cache.go` — `TTLCache` (LRU + TTL with separate fresh/stale windows) and `Backoff` (per-host exponential backoff, 1 s → 60 s with ±25 % jitter).
- `internal/catalog/artifacthub.go` — minimal HTTP client for `/packages/search`, `/packages/helm/{repo}/{name}`, and a tiny `/` probe. Returns typed `ArtifactHubError{Class}` so handlers can switch on `not_found` / `rate_limited` / `server_error` / `timeout` / `malformed`.
- `internal/api/catalog_proxy_state.go` — process-global `searchCache`, `packageCache`, `ahBackoff`, `ahClient` shared by the search/remote/reprobe handlers.

Cache TTLs (per design §4.5):

| Tier | Fresh TTL | Stale TTL | Capacity |
|---|---|---|---|
| Search results | 10 min | 24 h | 200 entries (LRU) |
| Package detail | 1 h | 24 h | 500 entries (LRU) |
| Versions (curated) | 15 min | (no stale) | 200 entries (LRU) |

On upstream failure handlers serve from the stale window with `X-Cache-Stale: true` and `stale: true` in the JSON body. On 429 / 5xx the per-host backoff extends so a flapping ArtifactHub doesn't get hammered. The Marketplace UI's "Retry connectivity" button calls `POST /catalog/reprobe`, which resets backoff + caches and immediately probes ArtifactHub.

No API token is required — ArtifactHub's read endpoints are public. We never proxy chart tarballs (ArgoCD pulls those at deploy time from the upstream repo).

### Adding an entry

1. Edit `catalog/addons.yaml` and append a stanza under `addons:` matching the schema in `catalog/schema.json`. Required fields: `name`, `description`, `chart`, `repo`, `default_namespace`, `maintainers`, `license`, `category`, `curated_by`. The closed enums for `category` and `curated_by` are listed in the schema.
2. Run `go test ./internal/catalog/...` — `TestLoad_Embedded` will catch any structural error and name the offending entry.
3. Run `go test ./...` to confirm full backend health.
4. Open a PR. CODEOWNERS gates `catalog/**` to the maintainer.

The catalog is metadata-only — Sharko does not host or proxy Helm charts. ArgoCD pulls charts from the upstream `repo` URL at deploy time. Air-gap is the operator's infrastructure problem (typically solved by running an internal Helm mirror and pointing the user's `addons-catalog.yaml` at it).

### Security primitives

Cross-cutting hardening helpers live under `internal/security/`:

- `internal/security/url_guard.go` — `ValidateExternalURL(rawURL string) error` runs an SSRF check on user-supplied URLs (RFC1918, loopback, link-local, IPv6 ULA blocked by default; optional `SHARKO_URL_ALLOWLIST` env var pins to a fixed hostname set). Wired into every handler that fetches from a user-supplied URL — currently `GET /api/v1/catalog/validate`. Returns a typed `*SSRFError` so handlers can render a structured `ssrf_blocked` response without ambiguity.

The orchestrator-side secret-leak guard (`internal/orchestrator/ai_guard.go`) is the AI-pipeline analogue — pure scanner of `values.yaml` payloads with `ScanForSecrets`, returning redacted `SecretMatch` records. Handler call sites (`addons_write.go`, `ai_annotate.go`, `values_editor.go`) emit a dedicated `secret_leak_blocked` audit-log entry via the `(*Server).emitSecretLeakAuditBlock` helper in `internal/api/secret_leak_audit.go` so security review can grep one stable token across the audit log without parsing per-event detail strings.

### Release supply-chain (Story V121-8.1)

Every release artifact is cosign-signed via GitHub Actions OIDC (keyless). Verification commands are in `docs/site/operator/supply-chain.md`. The signing happens in:

- `.github/workflows/release.yml` — image (after `docker build-push`) and Helm OCI chart (after `helm push`). Both use `sigstore/cosign-installer` and `cosign sign --yes <ref>@<digest>` so each tag pointing at the same digest shares one Rekor entry.
- `.goreleaser.yaml` — per-archive `.sig` and `.pem` companions for every CLI binary tarball plus `checksums.txt`, generated via the goreleaser `signs:` block calling `cosign sign-blob`.

The per-PR Docker workflow (`pr-docker.yml`) does NOT sign — PR images are short-lived test artifacts and the cost of OIDC-signing every PR push is not justified. Production-bound deployments must use `vX.Y.Z` tags only.
