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

## Connection Management

ArgoCD and Git connections are stored encrypted in a Kubernetes Secret (`sharko-connections`), using AES-256-GCM. Connections are managed through the Settings UI — no restart required when updating connections.

## AI Assistant

The AI assistant runs as an agent loop with tool-calling capabilities. It has access to 24 read tools and 5 write tools (admin only, opt-in). Write tools are gated behind explicit user confirmation in the UI.

Multi-provider support means you can use any combination of cloud models or self-hosted Ollama, configured per-deployment.

## GitOps Flow

Every write operation that changes fleet state:

1. Sharko modifies files in the addons repo (values files, cluster directories)
2. A PR is opened in Git
3. ArgoCD watches the repo and syncs the ApplicationSet after the PR is merged
4. The ApplicationSet generates ArgoCD Applications per-cluster, per-addon

This means Sharko's state is always reflected in Git — the addons repo is the source of truth, not the Sharko database.
