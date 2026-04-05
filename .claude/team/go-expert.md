# Go Expert Agent

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
}
```

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

## Planned Packages

### `internal/notifications/`
Periodic notification system (PLANNED):
```
checker.go    — Periodic checker goroutine (runs every N minutes)
                Checks: upgrade available (semver comparison via Helm repo index),
                        version drift across clusters, security advisories
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

## Update This File When
- Interface signatures change (add/remove methods)
- Server struct fields change
- New dependencies are added to go.mod
- New testing patterns are established
- New packages are created (remoteclient, notifications, etc.)
- Swagger annotations are modified or new annotation patterns emerge
