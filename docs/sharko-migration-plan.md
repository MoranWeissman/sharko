# Sharko — Migration & Implementation Plan

> **SUPERSEDED:** This document is the original migration plan. The authoritative implementation
> design is now `docs/superpowers/specs/2026-04-01-sharko-implementation-design.md`.
>
> Key differences from this plan:
> - Server-first architecture (CLI is a thin HTTP client, not standalone)
> - `internal/orchestrator/` package for workflow logic
> - `internal/migration/` stripped entirely (dead weight from old two-repo world)
> - Datadog client and GPTeal stripped
> - Dual auth (cookies for UI, Bearer tokens for CLI)
> - API contract document as a hard gate before v1.0.0 implementation
>
> Original plan preserved below for historical context.

---

## Phase 0 — New Repo Setup (1 hour)

### 0.1 Create `sharko` repo on GitHub
- New repo under your GitHub org
- Apache 2.0 license (standard for k8s ecosystem — ArgoCD, Kubernetes, CNCF projects all use it. Requires attribution, includes patent grant, allows commercial use)
- Empty — no template, no gitignore (we'll bring our own)

### 0.2 Initialize repo structure
```
sharko/
  cmd/
    sharko/
      main.go           → entry point, cobra root command
      serve.go          → `sharko serve` — runs the UI + API server
      init.go           → `sharko init` — scaffolds addons repo (Phase 3)
      cluster.go        → `sharko add-cluster` (Phase 3)
      addon.go          → `sharko add-addon` (Phase 3)
  internal/
    api/                → current Go backend (router, handlers, middleware)
    argocd/             → ArgoCD client code
    git/                → Git provider integration (GitHub, AzureDevOps)
    providers/          → secrets provider interface + implementations
      provider.go       → interface definition
      aws_sm.go         → AWSSecretsManagerProvider
      k8s_secrets.go    → KubernetesSecretProvider
    models/             → shared data models
    config/             → configuration loading
  ui/                   → React frontend (current, rebranded)
  templates/            → reference addons repo structure
    bootstrap/
      root-app.yaml
      templates/
        addons-appset.yaml
    charts/             → starter addon charts
    configuration/
      addons-clusters-values/
      addons-global-values/
    sharko.yaml           → default CLI config
  charts/
    sharko/               → Helm chart for deploying Sharko itself
  docs/
    sharko-framework-vision.md
    sharko-migration-plan.md
  Dockerfile
  Makefile
  go.mod
  README.md
```

### 0.3 Set up CI
- GitHub Actions: lint, test, build, Docker image push
- Copy relevant workflow patterns from current repo
- GHCR image: `ghcr.io/<org>/sharko`

---

## Phase 1 — Migrate Existing Codebase (1-2 days)

The goal: everything that works today keeps working, just in the new repo with the new name.

### 1.1 Copy Go backend
- Copy `internal/api/` handlers, router, middleware from `argocd-addons-platform`
- Copy ArgoCD client code
- Copy Git provider code
- Copy data models
- Restructure into the new `internal/` layout
- Update import paths from old module name to `github.com/<org>/sharko`

### 1.2 Copy React frontend
- Copy entire `ui/` directory
- Update `package.json` name to `sharko`
- Keep all existing functionality intact

### 1.3 Copy Helm chart
- Copy `charts/argocd-addons-platform/` → `charts/sharko/`
- Rename release references, image names, labels
- Update `values.yaml` with new image repository

### 1.4 Copy Dockerfile
- Update image name, labels
- Same multi-stage build pattern

### 1.5 Wire up entry point
- Create `cmd/sharko/main.go` with cobra root command
- `sharko serve` runs the existing server (same behavior as current binary)
- Verify: `go build ./cmd/sharko && ./sharko serve` starts the UI + API

### 1.6 Verify everything works
- `make build` succeeds
- `make test` passes (all existing tests)
- `docker build` produces a working image
- `sharko serve` starts and serves the UI
- Helm chart deploys to cluster

**Milestone: the existing product works identically under the new name.**

---

## Phase 2 — Rebrand (1 day)

### 2.1 Frontend rebrand
- Replace "ArgoCD Addons Platform" → "Sharko" in all UI text
- Replace page title, browser tab, meta tags
- Replace logo/favicon with Sharko shark fin logo
- Update sidebar header, dashboard title
- Replace cyan accent color if desired (current is fine, matches logo)

### 2.2 Backend rebrand
- Update API response headers, health endpoint branding
- Update Helm chart `Chart.yaml` name, description
- Update `values.yaml` defaults

### 2.3 Documentation
- Write new `README.md` with Sharko branding, logo, description
- Include: what it is, screenshot, quickstart, architecture diagram
- Move vision doc into `docs/`

### 2.4 First release
- Tag `v0.1.0` — the rebranded product, functionally identical to current
- Push Docker image to GHCR
- Deploy to your cluster to verify

**Milestone: Sharko exists as a branded product. Same functionality, new identity.**

---

## Phase 3 — Provider Interface (2-3 days)

This is the foundation for everything that follows.

### 3.1 Define the interface

```go
// internal/providers/provider.go
package providers

type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (Kubeconfig, error)
    ListClusters() ([]ClusterInfo, error)
}

type ClusterInfo struct {
    Name   string
    Region string
    Labels map[string]string
}

type Kubeconfig struct {
    Raw []byte  // kubeconfig YAML bytes
}
```

### 3.2 Implement `KubernetesSecretProvider`
- Reads kubeconfig from Kubernetes Secrets in a configured namespace
- Secret name = cluster name (convention)
- No cloud dependency — works anywhere, great for testing
- This provider ships first because it's the easiest to test

### 3.3 Implement `AWSSecretsManagerProvider`
- Reads kubeconfig from AWS Secrets Manager
- Uses AWS SDK Go v2
- Supports IRSA for authentication (EKS native)
- Path pattern: `{prefix}/{cluster-name}` (configurable)

### 3.4 Provider registry
- Config-driven provider selection
- `sharko.yaml` or env vars: `MAKO_SECRETS_PROVIDER=aws-sm`
- Factory pattern: `NewProvider(config) → ClusterCredentialsProvider`

### 3.5 Wire into existing backend
- The existing ArgoCD client code that fetches cluster info can optionally use the provider
- Backwards compatible — existing env var config still works

### 3.6 Tests
- Unit tests for each provider (mock AWS SDK, mock K8s client)
- Integration test with KubernetesSecretProvider against a real cluster (kind)

**Milestone: the provider interface exists, two implementations work, tested.**

---

## Phase 4 — CLI Commands (3-5 days)

### 4.1 `sharko init <directory>`
- Copies `templates/` into the target directory
- Prompts for or accepts flags: `--secrets-provider`, `--region`, `--repo-url`
- Generates `sharko.yaml` in the new directory with provided config
- Initializes git repo if `--git` flag

What gets generated:
```
my-addons/
  sharko.yaml
  bootstrap/
    root-app.yaml
    templates/
      addons-appset.yaml    → starter AppSet template
  charts/                   → empty, ready for addon charts
  configuration/
    addons-clusters-values/ → empty, ready for per-cluster values
    addons-global-values/   → empty, ready for global addon values
```

### 4.2 `sharko add-addon <name>`
- Reads `sharko.yaml` for paths
- Flags: `--chart`, `--repo`, `--version`, `--namespace`
- Generates:
  - Entry in the bootstrap values (applicationsets list)
  - Global values file at `configuration/addons-global-values/<name>.yaml`
  - Outputs what was created

### 4.3 `sharko add-cluster <name>`
- Reads `sharko.yaml` for paths and secrets provider config
- Flags: `--secrets-provider`, `--region`, `--addons` (comma-separated)
- Actions:
  1. Calls `ClusterCredentialsProvider.GetCredentials(name)` to verify the cluster exists
  2. Creates per-cluster values file at `configuration/addons-clusters-values/<name>.yaml`
  3. Registers cluster in ArgoCD (creates cluster secret with addon labels)
  4. Outputs what was created and what ArgoCD will deploy

### 4.4 `sharko list-clusters`
- Calls `ClusterCredentialsProvider.ListClusters()`
- Shows which clusters are available in the secrets backend
- Shows which are already registered in ArgoCD
- Quick way to see what's available vs what's onboarded

### 4.5 `sharko status`
- Queries ArgoCD for all managed clusters
- Shows per-cluster addon health summary
- Like the Sharko UI dashboard but in the terminal

### 4.6 Tests
- Unit tests for each command (mock provider, mock ArgoCD client)
- Integration test: `sharko init` → `sharko add-addon` → `sharko add-cluster` → verify generated files

**Milestone: the CLI works end-to-end. `sharko init` + `sharko add-cluster` is the core loop.**

---

## Phase 5 — Write API Endpoints (2-3 days)

Extend the existing Go API server with write operations.

### 5.1 Cluster management endpoints
```
POST   /api/v1/clusters              → register (same as sharko add-cluster)
GET    /api/v1/clusters              → list all managed clusters
GET    /api/v1/clusters/:name        → cluster detail + addon status
PATCH  /api/v1/clusters/:name        → update addon labels
DELETE /api/v1/clusters/:name        → deregister from ArgoCD
```

### 5.2 Addon management endpoints
```
POST   /api/v1/addons                → add addon to catalog
GET    /api/v1/addons                → list available addons
DELETE /api/v1/addons/:name          → remove addon
```

### 5.3 Fleet overview endpoint
```
GET    /api/v1/fleet/status          → full fleet health, version matrix, drift summary
```

### 5.4 Wire CLI to API
- `sharko add-cluster` can work in two modes:
  - **Standalone:** directly calls provider + ArgoCD (no server needed)
  - **API mode:** `--server https://sharko.example.com` sends request to the API
- Same result either way

### 5.5 Authentication
- API key or Bearer token for API access
- The CLI stores credentials in `~/.sharko/config` (like `kubectl` or `gh`)

### 5.6 Tests
- API endpoint tests (existing test patterns from current codebase)
- CLI → API integration test

**Milestone: full API surface. IDPs can integrate. CLI can work standalone or via API.**

---

## Phase 6 — Templates & Reference Addons (2 days)

### 6.1 Build the starter template
- Clean, minimal AppSet template (simpler than the current production one)
- Supports the common case: one Helm chart per addon, per-cluster values
- Well-commented — explains what each section does

### 6.2 Include starter addons
- 2-3 common addons pre-configured in the template:
  - `cert-manager` (simple, universal)
  - `metrics-server` (simple, universal)
  - `external-secrets` (demonstrates multi-source pattern)
- These serve as examples for adding more

### 6.3 Documentation in template
- `README.md` inside the generated repo explaining the structure
- Comments in `sharko.yaml` explaining each field
- Comments in the AppSet template explaining customization points

**Milestone: `sharko init` generates a repo that actually works out of the box with ArgoCD.**

---

## Phase 7 — Documentation & Release (1-2 days)

### 7.1 README.md
- Logo + tagline
- What Sharko is (one paragraph)
- Screenshot of the UI
- Quickstart: `sharko init` → `sharko add-addon` → `sharko add-cluster`
- Architecture diagram (CLI / API / UI → ArgoCD + Provider)
- Provider documentation (AWS, K8s Secrets, how to write your own)

### 7.2 Provider contribution guide
- How to implement `ClusterCredentialsProvider`
- Where to submit (PR to main repo or separate repo)
- Testing requirements

### 7.3 Release
- Tag `v1.0.0`
- GitHub Release with changelog
- Docker image to GHCR
- Helm chart in repo or OCI registry

**Milestone: Sharko v1.0.0 is public, documented, and installable.**

---

## Timeline Summary

| Phase | What | Effort | Depends on |
|-------|------|--------|------------|
| 0 | Repo setup | 1 hour | — |
| 1 | Migrate codebase | 1-2 days | Phase 0 |
| 2 | Rebrand | 1 day | Phase 1 |
| 3 | Provider interface | 2-3 days | Phase 1 |
| 4 | CLI commands | 3-5 days | Phase 3 |
| 5 | Write API endpoints | 2-3 days | Phase 3 |
| 6 | Templates & starters | 2 days | Phase 4 |
| 7 | Docs & release | 1-2 days | Phase 6 |

**Phases 4 and 5 can run in parallel** (CLI and API are independent implementations of the same operations).

**Total: ~2-3 weeks** from start to v1.0.0.

---

## What NOT to Do

- Don't build the operator yet — CLI + API first, operator only if adoption demands it
- Don't abstract ArgoCD away — it's the only delivery mechanism, keep it
- Don't over-engineer the template — start minimal, evolve based on real usage
- Don't maintain the old `argocd-addons-platform` repo — archive it after migration
- Don't rename the work-specific `argocd-cluster-addons` repo — that stays as-is for work use. The `templates/` in Sharko is the clean, open-source version of the pattern
