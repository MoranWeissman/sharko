# Architecture Overview

Sharko is a server-first system. The server runs in-cluster alongside ArgoCD and holds all credentials. The CLI, UI, and integrations are thin clients that talk to the server's REST API.

## System Diagram

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
  +-- Swagger UI (/swagger/index.html)
```

## Why Server-First

- **Credentials stay on the cluster.** No ArgoCD tokens, Git tokens, or AWS credentials on developer laptops.
- **One `sharko login`** replaces configuring ArgoCD + Git + AWS locally.
- **Every consumer uses the same API** — the UI, CLI, Backstage, Terraform, and CI/CD pipelines all talk to the same REST endpoints.
- **Centralized audit trail** — all operations go through one server, making it easy to log and monitor.

## The Orchestrator Pattern

All write operations follow the same pattern. HTTP handlers are thin — they validate the request and delegate to the Orchestrator:

```
HTTP Handler
  |
  +-- Validates request
  +-- Gets ArgoCD client from ConnectionService
  +-- Gets Git provider from ConnectionService
  |
  v
Orchestrator
  |
  +-- Acquires gitMu (serializes all Git operations)
  +-- Step 1: Fetch credentials from provider
  +-- Step 2: Register in ArgoCD
  +-- Step 3: Create values file
  +-- Step 4: Create PR in Git
  +-- Step 5: Auto-merge if PRAutoMerge=true
  +-- Releases gitMu
```

A mutex (`gitMu`) serializes all Git operations, preventing concurrent PRs from conflicting on the same branch.

## Tech Stack

| Layer | Technology |
|-------|------------|
| Backend | Go 1.25, `net/http`, Cobra CLI framework |
| Frontend | React 18, TypeScript, Vite |
| Styling | Tailwind CSS v4, shadcn/ui components |
| GitOps | ArgoCD ApplicationSets, Helm charts |
| API docs | Swagger / OpenAPI (swag) |
| Secrets | AWS Secrets Manager, Kubernetes Secrets |
| AI | OpenAI, Claude, Gemini, Ollama, custom OpenAI-compatible |

## Secrets Providers

Sharko uses a pluggable provider interface to fetch cluster kubeconfigs:

| Provider | Description |
|----------|-------------|
| `aws-sm` | AWS Secrets Manager — uses IRSA for authentication, no static credentials needed |
| `k8s-secrets` | Kubernetes Secrets — no cloud dependency, simpler setup |

Implement the `ClusterCredentialsProvider` interface to add a custom provider:

```go
type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (*Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}
```

### AWS SM — Credential Flow

The `aws-sm` provider supports two secret formats and auto-detects which is in use:

```
AWS Secrets Manager
  |
  | GetSecretValue("{prefix}{cluster-name}")
  v
Format detection (internal/providers/aws_sm.go)
  |
  +-- Raw YAML? → parse as kubeconfig directly
  |
  +-- Structured JSON with "token" key?
  |     → build kubeconfig from server + ca + token fields
  |
  +-- Structured JSON with "role_arn" key?
        → call EKS STS token API (eks:DescribeCluster + pre-signed URL)
        → k8s-aws-v1.<base64url(presigned-url)> token (15-min TTL)
        → build kubeconfig from server + ca + fresh token
```

The STS path requires IRSA — the Sharko pod's service account is annotated with an IAM role ARN that has `secretsmanager:GetSecretValue`, `eks:DescribeCluster`, and optionally `sts:AssumeRole` for cross-account clusters. See [IRSA Setup](../operator/configuration.md#irsa-setup).

## Connection Management

ArgoCD and Git connections are stored encrypted in a Kubernetes Secret (`sharko-connections`), using AES-256-GCM. Connections are managed through the Settings UI — no restart required when updating connections.

## AI Assistant

The AI assistant runs as an agent loop with tool-calling capabilities. It has access to 24 read tools and 5 write tools (admin only, opt-in). Write tools are gated behind explicit user confirmation in the UI.

Multi-provider support means you can use any combination of cloud models or self-hosted Ollama, configured per-deployment.

## Secrets Reconciler Architecture

The secrets reconciler is a background subsystem that keeps addon secrets fresh on remote clusters without requiring External Secrets Operator:

```
Secrets Provider (AWS SM / K8s Secrets)
  |
  |  GetSecret(path) — fresh fetch, no cache
  v
secrets.Reconciler (Sharko Server)
  |
  +-- SHA-256 hash comparison (skip write if unchanged)
  |
  v
remoteclient → K8s Secret on remote cluster
  labeled: app.kubernetes.io/managed-by: sharko

Trigger sources:
  1. time.Ticker  — default every 5 minutes
  2. Webhook      — POST /api/v1/webhooks/git (HMAC-SHA256)
  3. Manual       — POST /api/v1/secrets/reconcile
```

ArgoCD is configured with a resource exclusion to ignore these labeled secrets. This prevents ArgoCD from deleting secrets that are not in Git — the secrets are managed exclusively by the Sharko reconciler.

## Operations Engine

Long-running workflows (init, large batch operations) use the async operations engine:

```
HTTP Handler
  |
  +-- Creates OperationSession (ID, status=pending)
  +-- Returns 202 Accepted + operation_id
  |
  Goroutine (async):
    +-- Runs workflow steps, appends to session.Log
    +-- Sets status = running → succeeded / failed
  |
Client:
  +-- GET /api/v1/operations/{id}    — polls for status
  +-- POST /api/v1/operations/{id}/heartbeat — keep-alive (required every 15s)
```

Sessions expire if the client stops sending heartbeats. This prevents orphaned sessions from accumulating when a client disconnects mid-operation.

## Managed vs Discovered Clusters

Sharko distinguishes clusters by whether they have a values file in the addons Git repo:

```
ArgoCD cluster registry
  |
  +-- Has a Sharko values file in Git?
        YES → "managed"   — full Sharko lifecycle
        NO  → "discovered" — read-only in Sharko, can be adopted
```

The `GET /api/v1/clusters` response includes a `"managed": bool` field on each entry. The UI renders the two groups separately. Discovered clusters show an **Adopt** action that creates the initial values file via PR.

## Notifications

Sharko includes a background notification checker (`internal/notifications/`) that fires on a configurable timer (default 24h):

```
notifications.Checker (Sharko Server)
  |
  +-- For each addon in catalog:
  |     fetch Helm repo index → compare semver
  |     major version bump? → security_advisory notification
  |
  +-- Version drift check (per-cluster vs catalog)
  |     diverged clusters → version_drift notification
  |
  v
notifications.Store (in-memory, read/unread state)
  |
  GET /api/v1/notifications          → list all
  POST /api/v1/notifications/{id}/read → mark read
```

Security advisory notifications are raised when an addon has a new **major** version available (e.g., v3 → v4). These are surfaced prominently in the UI notification bell with an amber shield icon.

## GitOps Flow

Every write operation that changes fleet state:

1. Sharko modifies files in the addons repo (values files, cluster directories)
2. Batch file changes are committed atomically via the Git tree API (`BatchCreateFiles`)
3. A PR is opened in Git
4. ArgoCD watches the repo and syncs the ApplicationSet after the PR is merged
5. The ApplicationSet generates ArgoCD Applications per-cluster, per-addon

This means Sharko's state is always reflected in Git — the addons repo is the source of truth, not the Sharko database.

When `SHARKO_GITOPS_PR_AUTO_MERGE=true`, Sharko also deletes the feature branch immediately after a successful merge (`DeleteBranch`). Branch cleanup is best-effort — a failure is logged but does not affect the operation result.
