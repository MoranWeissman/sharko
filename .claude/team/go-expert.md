# Go Expert Agent

## Scope

**DO:** Go code, interfaces, concurrency, stdlib, swagger annotations, go build/vet/test
**DO NOT:** Write UI code, modify Helm charts, change CI pipelines

You are a Go specialist for the Sharko project.

## Stack
- Go 1.25.8 (check `go.mod` for exact)
- HTTP router: Go 1.22+ `net/http.ServeMux` with method+pattern matching — NO third-party router
- K8s client: `k8s.io/client-go` v0.35.2
- AWS SDK: `github.com/aws/aws-sdk-go-v2` v1.41.5
- CLI: `github.com/spf13/cobra` v1.10.2
- YAML: `gopkg.in/yaml.v3`
- Auth: in-memory session map, bcrypt passwords, crypto/rand tokens, 24h expiry
- Swagger: `github.com/swaggo/swag` v1.16.6, `github.com/swaggo/http-swagger` v1.3.4

## Swagger / OpenAPI (swaggo/swag)

Sharko uses swaggo/swag for auto-generated OpenAPI 2.0 documentation. The generated files live at `docs/swagger/` and are imported in `internal/api/router.go` as a blank import:

```go
import (
    httpSwagger "github.com/swaggo/http-swagger"
    _ "github.com/MoranWeissman/sharko/docs/swagger" // swagger docs
)
```

The Swagger UI is served at `/swagger/index.html` via:

```go
mux.Handle("/swagger/", httpSwagger.Handler(
    httpSwagger.URL("/swagger/doc.json"),
))
```

### Annotation Pattern (Full Example)

Every HTTP handler must have swaggo annotations above it. Here is a complete example:

```go
// @Summary Register a new cluster
// @Description Registers a cluster by fetching credentials from the secrets provider,
// @Description verifying connectivity, registering in ArgoCD, generating a values file,
// @Description and committing to Git as a pull request.
// @Tags clusters
// @Accept json
// @Produce json
// @Param body body RegisterClusterRequest true "Cluster registration request"
// @Success 201 {object} RegisterClusterResult "Cluster registered successfully"
// @Failure 400 {object} ErrorResponse "Invalid input"
// @Failure 404 {object} ErrorResponse "Cluster not found in secrets provider"
// @Failure 409 {object} ErrorResponse "Cluster already registered"
// @Failure 502 {object} ErrorResponse "Upstream service unreachable"
// @Router /clusters [post]
func (s *Server) handleRegisterCluster(w http.ResponseWriter, r *http.Request) {
```

**Annotation fields:**
- `@Summary` — short one-line summary (appears in endpoint list)
- `@Description` — longer description (can be multi-line, each line prefixed with `// @Description`)
- `@Tags` — API group (clusters, addons, connections, system, auth, ai, dashboard, observability, upgrade, agent, docs)
- `@Accept` / `@Produce` — content types (usually `json`)
- `@Param` — `name location type required "description"`. Locations: `path`, `query`, `body`
- `@Success` / `@Failure` — `statusCode {type} GoType "description"`
- `@Router` — `path [method]` (path is relative to `/api/v1/`)

### Regeneration Command

After adding or modifying swagger annotations, regenerate the docs:

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

This reads the `@title`, `@version`, etc. from `cmd/sharko/serve.go` and all `@Router` annotations from handler files. Currently 71 annotated endpoints across 25 files.

**NEVER edit files in `docs/swagger/` manually.**

**MANDATORY: After adding any new endpoint, regenerate swagger docs before committing. CI will fail otherwise.**

## Interfaces (exact signatures from codebase)

### ClusterCredentialsProvider (`internal/providers/provider.go`)
```go
type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (*Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}
```

### GitProvider (`internal/gitprovider/provider.go`)
```go
type GitProvider interface {
    GetFileContent(ctx context.Context, path, ref string) ([]byte, error)
    ListDirectory(ctx context.Context, path, ref string) ([]string, error)
    ListPullRequests(ctx context.Context, state string) ([]PullRequest, error)
    TestConnection(ctx context.Context) error
    CreateBranch(ctx context.Context, branchName, fromRef string) error
    CreateOrUpdateFile(ctx context.Context, path string, content []byte, branch, commitMessage string) error
    DeleteFile(ctx context.Context, path, branch, commitMessage string) error
    CreatePullRequest(ctx context.Context, title, body, head, base string) (*PullRequest, error)
    MergePullRequest(ctx context.Context, prNumber int) error
    DeleteBranch(ctx context.Context, branchName string) error
    BatchCreateFiles(ctx context.Context, files map[string][]byte, branch, commitMessage string) error
    GetPullRequestStatus(ctx context.Context, prNumber int) (string, error)
}
```

### SecretProvider (`internal/providers/provider.go`)
```go
type SecretProvider interface {
    GetSecret(ctx context.Context, path string) (string, error)
}
```

Implemented by `KubernetesSecretProvider` and `AWSSecretsManagerProvider` — same types as `ClusterCredentialsProvider`, extended with `GetSecret`.

### ArgocdClient (`internal/orchestrator/orchestrator.go`)
```go
type ArgocdClient interface {
    RegisterCluster(ctx context.Context, name, server string, caData []byte, token string, labels map[string]string) error
    DeleteCluster(ctx context.Context, serverURL string) error
    UpdateClusterLabels(ctx context.Context, serverURL string, labels map[string]string) error
    SyncApplication(ctx context.Context, appName string) error
    CreateProject(ctx context.Context, projectJSON []byte) error
    CreateApplication(ctx context.Context, appJSON []byte) error
}
```
**v1.0.0 additions:** `AddRepository(ctx, repoURL, username, password string) error` (Phase 5)

### Server struct (`internal/api/router.go`)
```go
type Server struct {
    connSvc          *service.ConnectionService
    clusterSvc       *service.ClusterService
    addonSvc         *service.AddonService
    dashboardSvc     *service.DashboardService
    observabilitySvc *service.ObservabilityService
    upgradeSvc       *service.UpgradeService
    aiClient         *ai.Client
    agentMemory      *ai.MemoryStore
    authStore        *auth.Store
    aiConfigStore    *config.AIConfigStore
    credProvider     providers.ClusterCredentialsProvider
    providerCfg      *providers.Config
    repoPaths        orchestrator.RepoPathsConfig
    gitopsCfg        orchestrator.GitOpsConfig
    templateFS       fs.FS
}
```
**v1.0.0 additions:** `tokenStore` (Phase 4). No queue — API is synchronous.

## v1.0.0 New Go Patterns

### Git Mutex (Phase 1)
```go
type Orchestrator struct {
    gitMu        sync.Mutex  // serialize all Git operations
    // ... existing fields
}

func (o *Orchestrator) RegisterCluster(ctx, req) (*RegisterClusterResult, error) {
    // Non-Git ops run freely (no lock)
    creds, err := o.credProvider.GetCredentials(req.Name)
    err = o.argocd.RegisterCluster(ctx, ...)

    // Lock for Git only
    o.gitMu.Lock()
    defer o.gitMu.Unlock()
    gitResult, err := o.commitViaPR(ctx, ...)
    return result, nil
}
```

### Remote K8s Client (`internal/remoteclient/`)
- Build temporary `kubernetes.Interface` from kubeconfig bytes
- Connect -> create/update/delete secrets -> disconnect. No persistent connections.
- All Sharko-created secrets labeled: `app.kubernetes.io/managed-by: sharko`

### API Key Auth (`internal/auth/`)
- Token format: `sharko_` + 32 random hex = 39 chars
- Stored as bcrypt hash in K8s Secret
- Auth middleware priority: session cookie -> session token -> API key
- `last_used_at` updated on each API key auth

### PR-Only Git Flow (Phase 2)
- `commitDirect` removed entirely
- `commitChanges` always creates PR
- If `PRAutoMerge: true`: call `git.MergePullRequest()` after PR creation
- `GitResult` gains `Merged bool` and `PRID int`, loses `Mode` field

## v1.4.0 New Packages and Patterns

### `internal/secrets/` — Secrets Reconciler
```
reconciler.go    — Background goroutine (5-min timer + webhook trigger + manual trigger)
                   Reads AddonSecretRef from catalog, calls SecretProvider.GetSecret(),
                   compares hash (SHA-256 of value), pushes to remote cluster on change.
hash.go          — SHA-256-based change detection; reconciler skips write if hash unchanged.
```
**Key design:** push-based. Sharko holds secrets in memory only during reconcile; no cache. All
Sharko-managed secrets labeled `app.kubernetes.io/managed-by: sharko`. ArgoCD resource exclusion
must be configured so ArgoCD does not delete them.

### `internal/operations/` — Async Operations Engine
```
session.go       — OperationSession: ID, status (pending/running/succeeded/failed), log lines.
                   Heartbeat keep-alive: client must POST /heartbeat every N seconds or session expires.
store.go         — In-memory operation store, thread-safe.
```
**Pattern:** long-running handlers (init, batch register) create an Operation, return `202 Accepted`
with `operation_id`, continue in a goroutine. Client polls `GET /api/v1/operations/{id}`. When the
UI is the client, it uses `useEffect` + heartbeat interval.

```go
type OperationSession struct {
    ID        string
    Status    string   // "pending" | "running" | "succeeded" | "failed"
    Log       []string
    CreatedAt time.Time
    UpdatedAt time.Time
}
```

**Trigger sources for secrets reconciler:**
1. Timer — `time.NewTicker(interval)`, default 5 minutes
2. Webhook — `POST /api/v1/webhooks/git` with HMAC-SHA256 verification
3. Manual — `POST /api/v1/secrets/reconcile`

## v1.7.0 New Patterns

### Secret Path on Cluster Model

The `Cluster` model now has an optional `SecretPath` field:

```go
type Cluster struct {
    Name       string   `json:"name"`
    SecretPath string   `json:"secret_path,omitempty"` // override default path in secrets provider
    Region     string   `json:"region,omitempty"`
    Env        string   `json:"env,omitempty"`
    // ... existing fields
}
```

When `secret_path` is set, the provider uses it verbatim instead of deriving the path from `Name`. This supports non-standard naming conventions in AWS SM (e.g., `clusters/prod/my-cluster` instead of `my-cluster`).

The field is accepted on `POST /api/v1/clusters` and surfaced via `GET /api/v1/clusters/{name}`.
CLI flag: `--secret-path`.

### Smart Search Fallback in Providers

`ClusterCredentialsProvider.ListClusters()` now supports a smart search fallback when an exact path match fails:

1. Try exact lookup by `name` (or `secret_path` if set)
2. If not found, scan all secrets under the configured prefix and find the closest match (substring / suffix match on the last path segment)
3. Return ranked suggestions alongside a `not_found` error so the API can include them in the 404 response body

```json
{
  "error": "cluster not found",
  "suggestions": ["clusters/prod/my-cluster", "clusters/staging/my-cluster"]
}
```

This is implemented in `internal/providers/search.go` and used by both `KubernetesSecretProvider` and `AWSSecretsManagerProvider`.

### ConfigureAddon: Complex Fields (ignoreDifferences, additionalSources)

`PATCH /api/v1/addons/{name}` now accepts two complex fields:

**`ignore_differences`** — array of ArgoCD resource ignore rules:
```json
[
  {
    "group": "apps",
    "kind": "Deployment",
    "jsonPointers": ["/spec/replicas"]
  }
]
```

**`additional_sources`** — array of extra Helm chart sources to include alongside the main chart:
```json
[
  {
    "repoURL": "https://charts.example.com",
    "chart": "common-config",
    "targetRevision": "1.0.0",
    "helm": {"valueFiles": ["$values/base.yaml"]}
  }
]
```

Both fields are marshalled to YAML and written to the addon's catalog entry via `yaml_mutator.go`. They map directly to the ArgoCD ApplicationSet `ignoreDifferences` and `sources` fields.

## Phase 3-6 New Patterns

### AWS SM Structured JSON (Phase 3)

`AWSSecretsManagerProvider` supports two secret formats:

**Format 1 — Raw kubeconfig (original):**
```json
"apiVersion: v1\nkind: Config\n..."
```

**Format 2 — Structured JSON (auto-detected):**
```json
{
  "server": "https://abc123.gr7.us-east-1.eks.amazonaws.com",
  "ca": "<base64-ca>",
  "token": "<bearer-token>"
}
```
Or for EKS clusters that use STS token generation:
```json
{
  "server": "https://abc123.gr7.us-east-1.eks.amazonaws.com",
  "ca": "<base64-ca>",
  "cluster_name": "prod-eu",
  "role_arn": "arn:aws:iam::123456789012:role/EKSReadRole"
}
```

**Auto-detection logic (`internal/providers/aws_sm.go`):**
1. Attempt `json.Unmarshal` into `StructuredSecret` struct
2. If `server` key present → structured format
3. If `role_arn` key present → call STS EKS token API (`eks:GetToken`)
4. Otherwise → treat raw string as kubeconfig YAML

**STS EKS token generation:**
- Uses `github.com/aws/aws-sdk-go-v2/service/eks` — `GetToken` pre-signed URL
- Token format: `k8s-aws-v1.<base64url(presigned-url)>`
- Token TTL: 15 minutes (token is generated fresh on each `GetCredentials()` call)
- IRSA role assumed transitively — no static creds needed

### List Filtering and Sorting (Phase 4)

`GET /api/v1/clusters` and `GET /api/v1/addons/catalog` accept:

| Param | Values | Description |
|-------|--------|-------------|
| `?sort=name` | `name`, `env`, `health`, `addon_count` | Sort field |
| `?sort=-name` | prefix `-` | Descending sort |
| `?filter=env:prod` | `env:<val>`, `health:<val>`, `addon:<name>` | Filter predicate |

Filtering is applied server-side before pagination. Multiple `?filter=` params are AND-joined.

Implement as middleware helpers in `internal/api/` — no database, pure in-memory slice sort/filter.

### Security Advisory Notifications (Phase 6)

`internal/notifications/checker.go` — polls on a timer (default 24h):
1. Fetches Helm repo index for each addon in the catalog
2. Compares current version against latest **major** version
3. If major version bump detected → creates a `security_advisory` notification
4. Notifications stored in `internal/notifications/store.go`

Notification type:
```go
type Notification struct {
    ID          string    // uuid
    Type        string    // "upgrade_available" | "version_drift" | "security_advisory"
    Title       string
    Description string
    AddonName   string
    CreatedAt   time.Time
    Read        bool
}
```

Exposed via `GET /api/v1/notifications` (read all) and `POST /api/v1/notifications/{id}/read`.

## Planned Packages

### `internal/notifications/`
Periodic notification system (Phase 6):
```
checker.go    — Periodic checker goroutine (runs every 24h by default)
                Checks: upgrade available (semver comparison via Helm repo index),
                        version drift across clusters, security advisories (major bumps)
store.go      — In-memory notification store with read/unread state,
                exposes notifications via GET /api/v1/notifications
```

## Testing Patterns
- Fake K8s: `k8s.io/client-go/kubernetes/fake` -> `fake.NewSimpleClientset(...)`
- Mock interfaces: define in test file, implement only needed methods
- HTTP tests: `net/http/httptest.NewServer`
- Table-driven tests for validation
- Test files: `_test.go` co-located in same package
- Current: 30 backend tests passing

## Key Code Patterns
- `writeJSON(w, status, data)` / `writeError(w, status, msg)` — in router.go
- `requireAdmin(w, r) bool` — in users.go, checks `X-Sharko-User` header + role
- `getEnvDefault(key, defaultVal)` — in serve.go for env var reads
- `platform.Detect()` — returns ModeKubernetes or default
- Line-level YAML mutation in `internal/gitops/yaml_mutator.go` — preserves comments

## Write Rate Limiting

Write endpoints (admin, POST/DELETE/PATCH) are rate-limited to **30 requests/minute** per IP.
Rate limiter middleware is in `internal/api/router.go`. The same `SHARKO_TRUSTED_PROXIES` env var
governs IP extraction.

## Webhook HMAC Verification

`POST /api/v1/webhooks/git` validates the `X-Hub-Signature-256` header using HMAC-SHA256.
Secret configured via `SHARKO_WEBHOOK_SECRET` env var. Requests without a valid signature return 401.

## Update This File When
- Interface signatures change (add/remove methods)
- Server struct fields change
- New dependencies are added to go.mod
- New testing patterns are established
- New packages are created (remoteclient, notifications, etc.)
- Swagger annotations are modified or new annotation patterns emerge
- AWS SM format detection logic changes
- Filtering/sorting params are added or modified
- Cluster model fields are added (e.g., secret_path)
- Provider search/fallback logic changes
- Addon config fields are added or modified
