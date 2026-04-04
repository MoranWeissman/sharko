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
    status.go           Fleet status command
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

    service/            Read-only service layer
      connection.go     Connection management
      cluster.go        Cluster queries (via ArgoCD)
      addon.go          Addon catalog queries
      dashboard.go      Dashboard statistics
      observability.go  Observability overview

    platform/           Runtime detection
      detect.go         K8s vs local mode detection

    models/             Shared data models

  ui/                   React frontend (fleet dashboard)
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

### orchestrator/upgrade.go

Three upgrade methods:
- `UpgradeAddonGlobal(ctx, addonName, newVersion)` — updates `addons-catalog.yaml`
- `UpgradeAddonCluster(ctx, addonName, clusterName, newVersion)` — updates the cluster values file
- `UpgradeAddons(ctx, upgrades map[string]string)` — batch global upgrades in one PR

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

### api

HTTP handlers. Each handler is a method on the `Server` struct. Handlers are thin — they validate input, call the orchestrator or service layer, and write JSON responses.

**Handler files and their responsibilities:**

| File | Endpoints |
|------|-----------|
| `clusters_write.go` | POST, DELETE, PATCH `/clusters`, POST `/clusters/{name}/refresh` |
| `clusters_batch.go` | POST `/clusters/batch` |
| `clusters_discover.go` | GET `/clusters/available` |
| `cluster_secrets.go` | GET, POST `/clusters/{name}/secrets*` |
| `addons_write.go` | POST, DELETE `/addons` |
| `addons_upgrade.go` | POST `/addons/{name}/upgrade`, POST `/addons/upgrade-batch` |
| `addon_secrets.go` | GET, POST, DELETE `/addon-secrets*` |
| `tokens.go` | GET, POST, DELETE `/tokens*` |

### auth (API key support)

The `auth` store manages both session tokens (short-lived, from `POST /auth/login`) and API keys (long-lived, created via `POST /tokens`). The auth middleware in `router.go` checks for an API key first; if not found, it falls back to session token validation. API keys are stored hashed; the plaintext is only returned once at creation time.

### ai

Multi-provider AI client supporting Ollama, OpenAI, Claude, Gemini, and custom OpenAI-compatible endpoints. Includes a tool-calling agent loop for interactive troubleshooting.

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
