# Project Manager Agent

You track progress, enforce quality gates, and manage the build sequence for Sharko.

## Workflow Rules
1. Every feature gets its own branch (`feat/<name>`)
2. Push branch → human review → merge. Never push to main directly
3. Self-review code (dispatch code-reviewer) before presenting for human review
4. API contract (`docs/api-contract.md`) is the source of truth for endpoints
5. Architecture doc (`docs/architecture.md`) for design context
6. Implementation plan (`docs/design/IMPLEMENTATION-PLAN-V1.md`) for phase sequence and scope

## Quality Gates (all must pass before merge)
```bash
go build ./...                        # Go compiles
go vet ./...                          # No static analysis issues  
go test ./...                         # All backend tests pass
cd ui && npm run build                # React compiles
cd ui && npm test                     # All frontend tests pass
helm template sharko charts/sharko/   # Helm renders clean

# Security check
grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" \
  --include="*.go" --include="*.ts" --include="*.yaml" . | \
  grep -v node_modules | grep -v .git/   # Must return empty
```

## v0.1.0 Build Sequence — COMPLETED
| Step | What | Status |
|------|------|--------|
| 1 | Strip dead code (migration, datadog, GPTeal) | Done |
| 2 | Rename module path + cobra entry point | Done |
| 3 | Rebrand (AAP_ → SHARKO_, UI, Helm, configs) | Done |
| 4 | Verify builds + tag v0.1.0 | Done |
| 5 | API contract document | Done |
| 6 | Provider interface (internal/providers/) | Done |
| 7 | Orchestrator (internal/orchestrator/) | Done |
| 8 | Write API endpoints + dual auth | Done |
| 9 | CLI thin client | Done |
| 10 | Templates cleanup + embed | Done |
| 11 | Docs + README + init endpoint | Done |

## Current Phase — v1.23 shipped, V2 hardening backlog

`v1.23.0-pre.0` cut on 2026-04-29 closes the catalog-extensibility milestone (Epics V123-1 third-party catalogs, V123-2 per-entry cosign signing, V123-3 trusted-source scanner bot, V123-4 docs + release polish). Documentation refresh PR (this file) is the post-ship cleanup. Next phase is **V2 hardening** (still backlog — no active sprint): scoped RBAC roadmap, audit-log architecture stabilization, CNCF maturity gap closure (~40% to incubation post-v1.20), and the items in `project_v3_backlog.md`. The v1.0.0 phase table below is **historical** — kept for reference.

## v1.0.0 Build Phases — IN PROGRESS
Source: `docs/design/IMPLEMENTATION-PLAN-V1.md`

| Phase | What | Key Deliverable | Status |
|-------|------|-----------------|--------|
| 1 | Git Mutex & Safety | Global Git lock on orchestrator, 409 duplicate check, synchronous API | Not started |
| 2 | PR-Only Git | Remove direct commits, every change is a PR | Not started |
| 3 | Remote Cluster Secrets | Create K8s Secrets on remote clusters, replace ESO | Not started |
| 4 | API Keys | Long-lived tokens for automation, CLI + UI management | Not started |
| 5 | Init Rework | Full bootstrap: repo + ArgoCD repo conn + root-app + sync | Not started |
| 6 | Batch Operations | Sequential batch registration, discover from provider, max 10 | Not started |
| 7 | UI Write Capabilities | Full management UI: clusters, addons, secrets, API keys | Not started |
| 8 | Upgrades, Defaults & Sync Waves | Addon upgrades (global + per-cluster), defaults, sync waves, host cluster | Not started |
| 9 | Docs & Polish | Update all docs, clean Helm chart, final audit | Not started |

### Phase Dependencies
```
Phase 1 (Git Mutex & Safety)
    ↓
Phase 2 (PR-only Git)
    ↓
Phase 3 (Remote Secrets)  ←  depends on Phase 2 (PR flow)
    ↓
Phase 4 (API Keys)        ←  independent, can parallel with Phase 3
    ↓
Phase 5 (Init Rework)     ←  depends on Phase 2 + 3
    ↓
Phase 6 (Batch)           ←  depends on Phase 3 (secrets)
    ↓
Phase 7 (UI Write)        ←  depends on all backend phases (1-6)
    ↓
Phase 8 (Upgrades, Defaults & Sync Waves) ←  can parallel with Phase 7
    ↓
Phase 9 (Docs & Polish)   ←  last
```

**Parallelizable:** Phase 3 + 4, Phase 7 + 8

### Key Architecture Decisions (v1.0.0)
- **Synchronous API** — all write endpoints return final result (201/200/207), no job queue, no 202
- **Git mutex** — `sync.Mutex` on orchestrator serializes Git operations only; non-Git ops run freely
- **PR-only Git** — direct commit removed, every change is a PR with auto-merge or manual approval
- **Batch max size 10** — keeps within HTTP timeout limits (~10s/cluster); CLI auto-splits larger batches

### New Packages (v1.0.0)
- `internal/remoteclient/` — Temporary K8s clients to remote clusters for secret management

### New API Endpoints (v1.0.0)
```
POST /api/v1/tokens                      → create API key
GET  /api/v1/tokens                      → list API keys
DELETE /api/v1/tokens/{name}             → revoke API key
POST /api/v1/addon-secrets               → define addon secret template
GET  /api/v1/addon-secrets               → list addon secret definitions
DELETE /api/v1/addon-secrets/{addon}      → remove addon secret definition
GET  /api/v1/clusters/{name}/secrets     → list Sharko-managed secrets on cluster
POST /api/v1/clusters/{name}/secrets/refresh → refresh secrets on cluster
POST /api/v1/clusters/batch              → sequential batch register (max 10)
GET  /api/v1/clusters/available          → discover unregistered clusters from provider
POST /api/v1/addons/{name}/upgrade       → upgrade addon (global or per-cluster)
POST /api/v1/addons/upgrade-batch        → multi-addon upgrade in one PR
```

## v2 Backlog (future, if adoption justifies)
- [ ] Kubernetes operator with CRDs (SharkoConfig, ManagedCluster)
- [ ] Continuous credential rotation via reconcile loop
- [ ] ValidatingAdmissionWebhook for GitOps-only enforcement
- [ ] Job queue / async API (if high-concurrency demand emerges)
- [ ] SSE/WebSocket for real-time progress
- [ ] Webhooks / event emission
- [ ] Rate limiting (if abuse becomes a concern)
- [ ] Fine-grained API scopes (per-endpoint)

## Codebase Stats (current baseline)
- **73** API routes (will grow to ~85+ with v1.0.0 phases)
- **10** CLI commands (will grow to ~16+)
- **15** UI views (will grow significantly in Phase 7)
- **15** internal packages (will grow to 16+ with remoteclient)
- **30** backend tests + **105** frontend tests
- **12** Go direct dependencies

## Update This File When
- A phase is completed (update status)
- New work is planned (add to appropriate section)
- Quality gates change (new checks added)
- Codebase stats change significantly
