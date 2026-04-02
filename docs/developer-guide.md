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
    cluster.go          Cluster management commands
    addon.go            Addon management commands
    status.go           Fleet status command
    version_cmd.go      Version command
    client.go           HTTP client helpers for CLI commands

  internal/
    api/                HTTP handlers (thin glue code)
      router.go         Route registration, Server struct, middleware
      init.go           POST /api/v1/init handler
      clusters_write.go Cluster write operation handlers
      addons_write.go   Addon write operation handlers
      connections.go    Connection management handlers
      ai_config.go      AI configuration handlers
      users.go          User management handlers
      ...

    orchestrator/       Workflow engine (the brain)
      orchestrator.go   Orchestrator struct and constructor
      types.go          Request/response types
      init.go           InitRepo workflow
      clusters.go       Cluster registration/deregistration workflows
      addons.go         Addon management workflows

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

    ai/                 LLM agent
      client.go         Multi-provider AI client
      agent.go          Tool-calling agent loop
      memory.go         Conversation memory store
      tools.go          Agent tool definitions

    auth/               Authentication
      store.go          User store (K8s ConfigMap or env var)

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

The orchestrator is the workflow engine. It coordinates multi-step operations across the secrets provider, ArgoCD client, and Git provider. Each write operation (register cluster, add addon, init repo) is an orchestrator method.

Key design decisions:
- **No auto-rollback.** If step 3 of 4 fails, the orchestrator returns a partial success response. The user decides whether to retry or clean up.
- **Each method receives all dependencies via the constructor.** The orchestrator is stateless between calls.

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

Git provider interface for creating/updating files and opening PRs. Implementations for GitHub and Azure DevOps.

### api

HTTP handlers. Each handler is a method on the `Server` struct. Handlers are thin -- they validate input, call the orchestrator or service layer, and write JSON responses.

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

3. **If it's a write operation**, add an orchestrator method in `internal/orchestrator/` and call it from the handler. The handler should get ArgoCD and Git clients from `s.connSvc` and construct the orchestrator.

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
