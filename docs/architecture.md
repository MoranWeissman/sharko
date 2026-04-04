# Sharko Architecture

This document describes the architecture of Sharko, an addon management server for Kubernetes clusters built on ArgoCD.

---

## Server-First Architecture

Sharko is a server that runs in-cluster, next to ArgoCD. The CLI is a thin HTTP client, like `kubectl` to the Kubernetes API server or `argocd` CLI to the ArgoCD server.

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
```

**Why server-first:**
- The server holds all credentials (ArgoCD token, Git token, secrets provider access). No credentials on developer laptops.
- One `sharko login` replaces configuring ArgoCD + Git + AWS locally.
- Every consumer (UI, CLI, Backstage, Terraform, CI/CD) talks to the same API.
- A Kubernetes operator (CRDs, reconcile loop) is a potential v2 evolution if adoption justifies it.

---

## The Orchestrator Pattern

Write operations follow a consistent pattern: thin HTTP handlers delegate to the orchestrator, which coordinates across providers, ArgoCD, and Git.

```
HTTP Handler (api/)
  |
  +-- Validates request
  +-- Gets ArgoCD client from ConnectionService
  +-- Gets Git provider from ConnectionService
  +-- Constructs Orchestrator (with shared gitMu)
  |
  v
Orchestrator (orchestrator/)
  |
  +-- Acquires gitMu (serializes all Git operations)
  +-- Step 1: Fetch credentials from provider
  +-- Step 2: Register in ArgoCD
  +-- Step 3: Create values file
  +-- Step 4: Create PR in Git (always via PR)
  +-- Step 5: Auto-merge if PRAutoMerge=true
  +-- Releases gitMu
  |
  v
Response (partial success or full success)
```

The orchestrator is stateless between calls. All dependencies are injected via the constructor:

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

Handlers are thin glue code. They validate input, construct the orchestrator with the right dependencies, call the appropriate method, and write the response.

---

## Git Mutex Pattern

All Git operations are serialized by a single `sync.Mutex` (`gitMu`) that lives on the `Server` struct and is passed to every orchestrator instance. The mutex is held for the duration of each Git write operation (branch creation → file commit → PR creation → optional merge).

This prevents race conditions when multiple concurrent API requests attempt to create Git branches based on the same base commit:

```
Without mutex:                    With mutex:
  Request A reads HEAD            Request A reads HEAD
  Request B reads HEAD            Request A creates branch
  Request A creates branch A      Request A creates PR
  Request B creates branch B      Request A releases mutex
  Both branches point at          Request B reads HEAD
    the same parent commit        Request B creates branch
  May cause PR conflicts          (always from latest HEAD)
```

The mutex is shared across all orchestrator instances — not per-operation type. This means even concurrent `add-cluster` + `add-addon` requests are serialized. The tradeoff (reduced throughput) is acceptable because Git operations are fast and infrequent relative to read operations.

In tests where concurrency is not under test, pass `nil` for `gitMu`. The orchestrator checks for nil before locking.

---

## PR-Only Git Flow

Sharko always creates pull requests for Git changes. There is no direct commit mode.

**Configuration:**
- `SHARKO_GITOPS_PR_AUTO_MERGE=false` (default) — PR is created and left open for human review
- `SHARKO_GITOPS_PR_AUTO_MERGE=true` — PR is created and immediately merged (suitable for automated pipelines)

**GitResult shape:**
```go
type GitResult struct {
    PRUrl      string `json:"pr_url,omitempty"`
    PRID       int    `json:"pr_id,omitempty"`
    Branch     string `json:"branch,omitempty"`
    Merged     bool   `json:"merged"`
    CommitSHA  string `json:"commit_sha,omitempty"`
    ValuesFile string `json:"values_file,omitempty"`
}
```

`Merged: true` when `PRAutoMerge=true` and the merge succeeded. `Merged: false` when the PR is left open for manual review, or if auto-merge failed (PR still created).

Every write operation response includes a `git` field with this shape, giving consumers a direct link to the created PR.

---

## Partial Success Handling

Multi-step operations (like cluster registration) can fail partway through. Sharko never auto-rolls back ArgoCD state.

**Why no auto-rollback:** If step 3 fails after ArgoCD registration succeeds (step 2), auto-deregistering the cluster could trigger cascade deletion of addons that ArgoCD already started deploying. This is worse than a partial success.

Instead, Sharko returns a structured response indicating what succeeded and what failed:

```json
{
  "status": "partial",
  "completed_steps": ["credentials_fetched", "argocd_registered"],
  "failed_step": "git_commit",
  "error": "push failed: permission denied",
  "cluster": { "name": "prod-eu", "server": "https://..." }
}
```

The user decides whether to retry the failed step or clean up with `sharko remove-cluster`.

---

## Remote Cluster Secret Management

Sharko can deliver secrets from the secrets provider to remote clusters as Kubernetes Secrets. This is used for addons that need API keys or credentials at runtime (e.g., Datadog agent API keys).

```
Secrets Provider (AWS SM / K8s Secrets)
  |
  +-- GetSecretValue("secrets/datadog/api-key")
  |
  v
Orchestrator
  |
  +-- remoteclient.NewClientFromKubeconfig(kubeconfigBytes)
  +-- remoteclient.EnsureSecret(ctx, client, namespace, secretName, data)
  |
  v
Remote Cluster
  kubernetes.io/v1 Secret "datadog-keys" in namespace "datadog"
```

**AddonSecretDefinition** maps an addon to the K8s Secret it needs:
```go
type AddonSecretDefinition struct {
    AddonName  string            `json:"addon_name"`
    SecretName string            `json:"secret_name"`
    Namespace  string            `json:"namespace"`
    Keys       map[string]string `json:"keys"` // K8s data key → provider path
}
```

Definitions are loaded from the `SHARKO_ADDON_SECRETS` environment variable (JSON map) at startup and can be updated at runtime via `POST /api/v1/addon-secrets`.

Secret delivery happens:
1. During `RegisterCluster` — secrets are created on the new cluster for all enabled addons that have definitions
2. During `UpdateCluster` — when an addon is enabled, its secrets are created; when disabled, they are deleted (best-effort)
3. On-demand via `POST /api/v1/clusters/{name}/secrets/refresh` — refreshes all secrets on a cluster

---

## API Key Authentication Flow

Sharko supports two authentication mechanisms:

1. **Session tokens** — short-lived (24h), returned by `POST /api/v1/auth/login`. Used by the CLI and browser UI.
2. **API keys** — long-lived, created via `POST /api/v1/tokens`. Intended for non-interactive consumers.

The auth middleware checks in this order:
1. Extract the Bearer token from the `Authorization` header
2. Check if it matches a known API key (hashed comparison)
3. If not, validate it as a session token
4. If neither matches, return 401

API keys are stored in the same auth store as users, hashed with bcrypt. The plaintext is only returned once at creation time. Revocation (`DELETE /api/v1/tokens/{name}`) removes the entry from the store and immediately invalidates the key.

---

## Batch Operations Design

Batch cluster registration (`POST /api/v1/clusters/batch`) processes up to `MaxBatchSize = 10` clusters sequentially (not in parallel). Sequential processing is intentional:

1. **Git serialization** — each cluster registration creates a PR; parallel processing would require the Git mutex and would serialize anyway
2. **Failure isolation** — if cluster N fails, clusters N+1 through 10 are still attempted; per-cluster results are reported independently
3. **Predictable behavior** — no partial state from concurrent operations to reason about

The response includes `total`, `succeeded`, `failed`, and a `results` array with the per-cluster outcome. If any cluster fails, HTTP 207 is returned; HTTP 200 is returned only when all clusters succeed.

---

## ArgoCD Integration

Sharko communicates with ArgoCD via its REST API using account token authentication (Bearer header). It does NOT use Kubernetes ServiceAccount auth.

**Why account tokens:** ArgoCD has its own account system and RBAC (`argocd-rbac-cm` ConfigMap). This is how most tools integrate with ArgoCD. The token is stored in a Kubernetes Secret, injected via Helm values or configured through the Settings UI.

Operations:
- **Register cluster:** Creates an ArgoCD cluster secret with name, server URL, CA data, token, and addon labels
- **Update labels:** Patches cluster secret labels (addon enable/disable)
- **Delete cluster:** Removes the cluster secret from ArgoCD
- **Sync application:** Triggers a manual sync on an ArgoCD Application
- **List clusters/applications:** Read operations for cluster observability

---

## Git Integration

Sharko commits changes to the addons Git repository via the configured Git provider.

All Git operations go through pull requests. When `SHARKO_GITOPS_PR_AUTO_MERGE` is `true`, PRs are merged immediately after creation. When `false` (default), PRs require manual approval.

Git providers implement the `GitProvider` interface:

```go
type GitProvider interface {
    CreateOrUpdateFile(ctx context.Context, path string, content []byte, branch, message string) error
    CreateBranch(ctx context.Context, branchName, baseBranch string) error
    CreatePullRequest(ctx context.Context, title, body, head, base string) (string, error)
    GetFileContent(ctx context.Context, path, branch string) ([]byte, error)
    DeleteFile(ctx context.Context, path, branch, message string) error
}
```

---

## Secrets Provider Interface

The secrets provider abstracts how cluster kubeconfigs are fetched. This is the key abstraction that makes Sharko portable across cloud providers.

```go
type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (*Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}
```

Two implementations ship with Sharko:

| Provider | Description | Auth Mechanism |
|----------|-------------|----------------|
| `aws-sm` | AWS Secrets Manager | IRSA (IAM Roles for Service Accounts) |
| `k8s-secrets` | Kubernetes Secrets | K8s service account (no cloud dependency) |

The `k8s-secrets` provider is critical for adoption: anyone can try Sharko without a cloud account. Put kubeconfigs in Kubernetes Secrets and go.

The factory pattern in `providers.New()` creates the appropriate implementation based on the `SHARKO_PROVIDER_TYPE` configuration.

---

## The Coupling Contract

The only coupling point between Sharko and the GitOps repository is:

> **Cluster name must match the values file name.**

When you run `sharko add-cluster prod-eu`, the server creates `configuration/addons-clusters-values/prod-eu.yaml`. The ArgoCD ApplicationSet finds it via `{{.name}}`. This naming convention is the entire framework contract.

Directory paths are configurable via server-side environment variables:

| Variable | Default |
|----------|---------|
| `SHARKO_REPO_PATH_CLUSTER_VALUES` | `configuration/addons-clusters-values` |
| `SHARKO_REPO_PATH_GLOBAL_VALUES` | `configuration/addons-global-values` |
| `SHARKO_REPO_PATH_CHARTS` | `charts/` |
| `SHARKO_REPO_PATH_BOOTSTRAP` | `bootstrap/` |

---

## What Sharko Does NOT Do

- **Does not generate ApplicationSet templates.** AppSet templates contain deeply evolved production logic (sync waves, multi-source apps, conditional logic). They belong in Git, owned by the user. Sharko generates data files (values, config). It never touches template logic.
- **Does not replace ArgoCD.** ArgoCD handles delivery. Sharko handles what gets delivered and where. If Sharko goes away, the repo still works -- ArgoCD does not know or care about Sharko.
- **Does not store config in the addons repo.** All configuration is server-side (Helm values, env vars, K8s Secrets). No `sharko.yaml` in the repo.
- **Does not auto-rollback.** Partial failures return structured responses. The user decides the next step.
- **Does not commit directly to the base branch.** All Git changes go through pull requests.

---

## Configuration

All configuration is server-side. There are no Sharko-specific files in the addons repository.

Configuration sources (in priority order):
1. **Runtime settings** (configured via Settings UI, persisted in encrypted K8s Secret)
2. **Environment variables** (set via Helm values, deployment template, or `extraEnv`)
3. **Defaults** (hardcoded in the server binary)

Connections (ArgoCD + Git) are managed exclusively through the Settings UI and stored in an encrypted Kubernetes Secret. The encryption key is provided via `SHARKO_ENCRYPTION_KEY`.

---

## Future Directions

### V2: Kubernetes Operator

If adoption justifies it, Sharko could evolve into a Kubernetes operator:

- `SharkoConfig` CRD for global configuration
- `ManagedCluster` CRD for per-cluster lifecycle with status reporting
- Continuous credential rotation via reconcile loop
- `ValidatingAdmissionWebhook` for GitOps-only enforcement (block direct `kubectl` writes)

### Async Operations

Batch workflows beyond the current 10-cluster maximum could benefit from async processing: return `202 Accepted` with a job ID for polling, and process registrations in a background worker pool.

### Webhooks

Event-driven notifications for IDPs:

```
cluster.registered    -> new cluster added
cluster.degraded      -> health changed from healthy to degraded
addon.drift           -> version drift detected across clusters
addon.sync.failed     -> addon sync failure on a cluster
```
