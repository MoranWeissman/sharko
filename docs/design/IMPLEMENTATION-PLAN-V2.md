# Sharko — Post-v1.0.0 Implementation Plan (V2)

> Derived from QA testing on a real Kubernetes cluster (2026-04-06).
> Covers every item in `docs/TODO.md` — Critical, High, Medium, Bugs, Tech Debt.
> Supersedes V1 plan (which covered the initial v1.0.0 build).
>
> **Source:** `docs/TODO.md` (20+ items from real-world testing)
> **Baseline:** v1.0.0 deployed to `devops-automation` cluster

---

## Phase Overview

| Phase | Name | Items | Depends On |
|-------|------|-------|------------|
| 1 | Catalog YAML — Single Source of Truth | 1 critical + 2 tech debt | — |
| 2 | Core Bug Fixes | 8 bugs | Phase 1 (some bugs touch catalog code) |
| 3 | Security Hardening + Notifications | 4 security + 3 notifications | — (parallel with Phase 2) |
| 4 | CLI & API Improvements | 4 CLI + 3 API | Phase 2 (needs working init/status) |
| 5 | UI Enhancements | 4 UI features + 1 bug | Phase 1 (edit config needs catalog) |
| 6 | GitOps Integration + DX | 4 GitOps + 4 DX | Phase 1 (schema validation needs catalog) |
| 7 | Documentation + Polish | 3 docs + 5 medium features + 2 tech debt | Phases 1-6 (docs reflect final state) |

**Parallelizable:**
- Phase 2 + Phase 3 (independent: bugs vs security/notifications)
- Phase 4 + Phase 5 (independent: CLI/API vs UI)
- Within phases: Go backend + UI work can run in parallel

**Total estimated scope:** ~45 items across 7 phases

---

## Phase 1 — Catalog YAML: Single Source of Truth

**Why first:** This is the #1 design decision from QA testing. Every subsequent bug fix and feature depends on Sharko reading/writing `addons-catalog.yaml` directly — the same file ArgoCD consumes. Without this, Sharko maintains a parallel data model that drifts from reality.

**Design decision:** Sharko = a fancy YAML editor for the files ArgoCD already uses. One source of truth. No individual `charts/<name>/addon.yaml` files.

### Tasks

#### 1.1 — Add catalog YAML mutation functions
**File:** `internal/gitops/yaml_mutator.go`
**What:**
- `AddCatalogEntry(catalog []byte, entry AddonCatalogEntry) ([]byte, error)` — append entry to applicationsets array
- `RemoveCatalogEntry(catalog []byte, addonName string) ([]byte, error)` — remove entry by name
- `UpdateCatalogEntry(catalog []byte, addonName string, updates func(*AddonCatalogEntry)) ([]byte, error)` — find and modify entry
- All functions preserve YAML comments and formatting (use `gopkg.in/yaml.v3` node manipulation)
- Unit tests for each function (add, remove, update, edge cases: empty catalog, duplicate names, missing entry)

#### 1.2 — Refactor orchestrator addon operations
**Files:** `internal/orchestrator/addon.go`, `internal/orchestrator/addon_configure.go`
**What:**
- `AddAddon` → reads `addons-catalog.yaml` from Git, calls `AddCatalogEntry`, commits via PR
- `ConfigureAddon` → reads catalog, calls `UpdateCatalogEntry` with merge semantics, commits via PR
- `RemoveAddon` → reads catalog, calls `RemoveCatalogEntry`, commits via PR
- Remove all references to individual `charts/<name>/addon.yaml` file paths
- The orchestrator never constructs addon file paths — it only knows about `addons-catalog.yaml`

#### 1.3 — Update init templates
**Files:** `templates/bootstrap/`, `templates/starter/`
**What:**
- Remove individual `charts/<name>/addon.yaml` template files
- Keep `addons-catalog.yaml` as the sole addon definition file
- Keep `addons-global-values/<name>.yaml` — per-addon values are correct for GitOps
- Update `configuration/clusters-addons.yaml` template if needed
- Ensure `sharko init` produces a clean repo structure that matches the new model

#### 1.4 — Reconcile `name` vs `appName`
**Tech debt item:** Individual addon files used `appName` but catalog uses `name`. Since we're eliminating individual files, standardize on `name` everywhere.
**Files:** `internal/models/addon.go`, any code referencing `appName`
**What:**
- Audit all `appName` references in Go code and templates
- Replace with `name` (or `Name` in Go structs)
- Update JSON/YAML tags
- Update API responses if `appName` was exposed

#### 1.5 — Clean up init repo structure
**Tech debt item:** Init creates a confusing hybrid of old structure + Sharko-specific files.
**What:**
- Document what each file is for and who consumes it (ArgoCD vs Sharko vs both)
- Add a `README.md` to the generated repo explaining the structure
- Remove any files that exist only for the old individual-addon model

### Quality Gates
```bash
go build ./...
go test ./internal/gitops/... ./internal/orchestrator/... -v
# Verify: no references to charts/<name>/addon.yaml remain
grep -rn "charts/.*addon\.yaml" --include="*.go" --include="*.yaml" . | grep -v node_modules | grep -v .git/
```

### Exit Criteria
- `AddAddon`, `ConfigureAddon`, `RemoveAddon` all read/write `addons-catalog.yaml`
- No individual addon YAML files in the codebase or templates
- `name` used consistently (no `appName` drift)
- Init produces a clean, documented repo structure

---

## Phase 2 — Core Bug Fixes

**Why now:** These bugs make the product unusable. Every CLI command found during QA testing has issues. Fix them before adding features.

**Depends on:** Phase 1 (some bugs touch catalog/addon code paths)

### Tasks

#### 2.1 — Fix gitopsCfg initialization bug
**File:** `cmd/sharko/serve.go` (around line 215)
**Bug:** `SetWriteAPIDeps` is inside `if providerType != ""` block. Without a secrets provider, ALL write operations fail because `gitopsCfg` is empty.
**Fix:** Initialize `gitopsCfg` independently of the provider type. The Git config (repo URL, branch, PR settings) has nothing to do with the secrets provider.

#### 2.2 — Fix `sharko init` on empty repos
**Bug:** GitHub API returns 409 when trying to create a branch from `main` that doesn't exist.
**Fix:** Detect empty repo (no default branch) and create an initial commit first (README.md), then proceed with init.

#### 2.3 — Fix `sharko init` PR creation failure
**Bug:** All files written to branch successfully (25+ API calls), then 502 on PR creation. Error is swallowed.
**Fix:**
- Add proper error handling and logging for PR creation
- Return clear error message to user with the branch name (so they can manually create PR)
- Log the full error response from GitHub API

#### 2.4 — Fix `sharko init` performance
**Bug:** Files written one-by-one via GitHub API (15+ seconds for 25 files).
**Fix:** Use Git tree/commit API to batch all files in a single commit. One API call instead of 25+.

#### 2.5 — Fix `sharko init` to read repo URL from connection
**Bug:** Requires `SHARKO_GITOPS_REPO_URL` env var separately. Should fall back to the Git repo configured in the active connection.
**Also:** Helm chart needs `config.repoURL` → `SHARKO_GITOPS_REPO_URL` mapping in deployment template.

#### 2.6 — Fix `sharko status` combined data
**Bug:** Currently only reads from Git and crashes on empty repo.
**Fix:** Return combined multi-source data:
- Sharko server info (version, mode, uptime) — always available
- ArgoCD API (clusters, apps, health) — always available when connected
- K8s API (pod health, secrets) — always available in-cluster
- Git repo (catalog, configs) — optional enrichment, graceful when empty
- Friendly error when repo is uninitialized: suggest `sharko init`

#### 2.7 — Add CLI init progress feedback
**Bug:** Just prints "Initializing..." then done/error.
**Fix:** Show step-by-step progress:
```
Creating bootstrap files...  ✓
Pushing to branch...         ✓
Creating pull request...     ✓
PR created: https://github.com/...
```

#### 2.8 — Fix login background flicker
**Bug:** Login screen background image flickers/resizes twice on load.
**Fix:** Set explicit dimensions on the image container, use CSS background with proper sizing, or preload the image.

### Quality Gates
```bash
go build ./...
go test ./...
# Manual: sharko init on empty repo, sharko status on empty repo
```

### Exit Criteria
- `sharko init` works on empty repos, batches file commits, creates PR, shows progress
- `sharko status` returns useful data even without Git repo
- `gitopsCfg` initialized independently of provider
- Login page loads without flicker

---

## Phase 3 — Security Hardening + Notifications

**Why now:** Quick wins that improve production readiness. Independent of bug fixes — can run in parallel with Phase 2.

### Security Tasks

#### 3.1 — Add Content-Security-Policy header
**File:** `internal/api/middleware.go` (or new `security_headers.go`)
**What:** Add CSP header to all responses. Start restrictive:
```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' https://fonts.gstatic.com
```

#### 3.2 — Add Strict-Transport-Security header
**What:** Add HSTS header: `Strict-Transport-Security: max-age=31536000; includeSubDomains`
Only when request is over HTTPS (check X-Forwarded-Proto or TLS).

#### 3.3 — Document trusted proxy requirement
**File:** `docs/` (operator manual or security section)
**What:** X-Forwarded-For is trusted for rate limiting. Document that Sharko must sit behind a reverse proxy (ingress controller, ALB, etc.) in production.

#### 3.4 — Document auth-disabled risk
**File:** `docs/` (operator manual or security section)
**What:** When no users are configured, all endpoints are public. Document this clearly with a warning.

### Notification Tasks

#### 3.5 — Wire VersionProvider to real Helm repo checks
**File:** `internal/notifications/checker.go`
**Bug:** The checker runs but `LatestVersion` is always empty — no actual Helm index fetch.
**Fix:** Implement Helm repo index fetching. For each addon in catalog:
1. Fetch `index.yaml` from the Helm repo URL
2. Parse available versions
3. Compare with current version
4. Generate notification for major/minor upgrades

#### 3.6 — Add security advisory detection
**What:** When fetching Helm repo versions, also check release notes or artifact hub for CVE mentions. Flag security-relevant upgrades differently from regular upgrades.

#### 3.7 — Add notification persistence
**Bug:** Currently in-memory, lost on restart.
**Fix:** Persist to K8s ConfigMap (fits the existing pattern — connections and users are already in ConfigMaps/Secrets). Load on startup, write on change.

### Quality Gates
```bash
go build ./...
go test ./internal/api/... ./internal/notifications/... -v
# Verify: curl -I http://localhost:8080/api/v1/health shows security headers
```

### Exit Criteria
- Security headers present on all responses
- Notifications survive pod restarts
- Version checker actually fetches from Helm repos
- Security docs written

---

## Phase 4 — CLI & API Improvements

**Why now:** The CLI is the primary interface for power users and CI/CD. Fix gaps found during QA.

**Depends on:** Phase 2 (needs working init/status)

### CLI Tasks

#### 4.1 — Add `sharko connect` command
**What:** Create/manage connections from CLI without needing the UI.
**Flags:** `--name`, `--git-provider`, `--git-repo`, `--git-token`, `--argocd-url`, `--argocd-token`, `--argocd-namespace`
**Enables:** Full CLI-first workflow: `login → connect → init → add addons`

#### 4.2 — Add CLI login flags
**What:** Add `--username` and `--password` flags to `sharko login` for non-interactive use (CI/CD, scripting).

#### 4.3 — Add `sharko list-addons --show-config`
**What:** Quick way to see which features are enabled across all addons. Table output showing syncWave/selfHeal/syncOptions per addon.

#### 4.4 — Auto-detect GitHub token from Helm secret
**What:** Connection setup should pre-fill the Git token if it was passed via `--set secrets.GITHUB_TOKEN` during Helm install, instead of asking the user to enter it again.

### API Tasks

#### 4.5 — Add pagination to list endpoints
**What:** Large cluster/addon lists need pagination. Add `?page=1&per_page=20` query params to:
- `GET /api/v1/clusters`
- `GET /api/v1/addons`
- `GET /api/v1/notifications`
**Response:** Add `X-Total-Count` header and `Link` header for next/prev.

#### 4.6 — Add filtering/sorting to list endpoints
**What:** Query params for list endpoints:
- `?sort=name`, `?sort=status`, `?sort=updated`
- `?filter=name:prod*`, `?filter=status:healthy`

#### 4.7 — Add rate limiting to all write endpoints
**Bug:** Currently only login is rate-limited.
**Fix:** Apply rate limiting to all `POST/PUT/PATCH/DELETE` endpoints. Use the existing rate limiter infrastructure.

### Quality Gates
```bash
go build ./...
go test ./...
# Manual: sharko connect + sharko init + sharko add-addon full flow
```

### Exit Criteria
- Full CLI-first workflow works without UI
- List endpoints paginated and filterable
- All write endpoints rate-limited

---

## Phase 5 — UI Enhancements

**Why now:** The UI needs write capabilities for addon config and a guided first-run experience.

**Depends on:** Phase 1 (edit config reads/writes catalog YAML)

### Tasks

#### 5.1 — UI edit addon advanced config
**File:** `ui/src/views/AddonDetail.tsx`
**What:** Toggle syncWave, selfHeal, syncOptions, ignoreDifferences, extraHelmValues from the UI with save button. Currently read-only display (accordion in Overview section).
**Implementation:**
- Convert accordion `<details>` sections to editable form fields
- "Edit" button toggles read/write mode
- "Save" sends PATCH to addon configure endpoint
- Show PR URL on success

#### 5.2 — First-run wizard
**What:** When no connection exists, guide the user through setup instead of showing an error.
**Implementation:**
- Detect "no connections" state on app load
- Show a step-by-step wizard: Connect Git → Connect ArgoCD → Initialize Repo
- Each step validates before proceeding
- Replaces the current blank/error state

#### 5.3 — Init button in UI
**What:** After saving connection, detect empty repo and offer to bootstrap.
**Implementation:**
- Connection save → check repo status → if empty, show "Initialize Repository" button
- Clicking triggers the init flow with progress display
- Currently init is CLI-only (`sharko init`), no UI path exists

#### 5.4 — Auto-bootstrap option
**What:** Connection save could auto-init if the repo is empty (opt-in toggle in connection form).
**Implementation:** Add checkbox "Automatically initialize if repo is empty" in connection form.

#### 5.5 — Fix login page mascot gap
**Tech debt:** PNG has transparent padding, needs manual cropping for pixel-perfect alignment.

### Quality Gates
```bash
cd ui && npm run build && npm test
# Manual: create connection → first-run wizard → init from UI
```

### Exit Criteria
- Addon config editable from UI with save
- First-run wizard guides new users
- Init available from UI
- Login mascot properly aligned

---

## Phase 6 — GitOps Integration + Developer Experience

**Why now:** These features make Sharko production-grade (GitOps) and development-sustainable (DX).

**Depends on:** Phase 1 (schema validation needs to know the catalog structure)

### GitOps Tasks

#### 6.1 — Webhook listener
**What:** GitHub/GitLab webhook endpoint so Sharko gets notified when someone pushes to the addons repo directly.
**Endpoint:** `POST /api/v1/webhooks/git`
**Implementation:**
- Verify webhook signature (GitHub: HMAC-SHA256, GitLab: secret token)
- On push to main: refresh internal cache of catalog/values
- On PR merge: trigger notification if relevant changes detected

#### 6.2 — Conflict detection
**What:** Detect and handle merge conflicts when Sharko creates a PR while someone else edited the same file.
**Implementation:**
- Before creating PR: fetch latest main, check if target files changed since our read
- If conflict: return error with details, don't create broken PR
- Option: auto-rebase and retry once

#### 6.3 — Schema validation CI
**What:** GitHub Action for the addons repo that validates YAML schema on every push/PR.
**Deliverable:** A `sharko validate` CLI command + a reusable GitHub Action YAML file that repos can adopt.

#### 6.4 — Audit log for manual changes
**What:** Track who changed what in the repo outside of Sharko.
**Implementation:** On webhook push events, diff the changes and log which files changed and by whom (from commit author). Surface in UI as "External Changes" notification.

### Developer Experience Tasks

#### 6.5 — E2E tests
**What:** Test against real ArgoCD (Kind + ArgoCD in CI).
**Implementation:**
- Kind cluster setup in CI (GitHub Actions)
- Install ArgoCD via Helm
- Run Sharko against it
- Test: init → add addon → add cluster → verify ArgoCD apps created

#### 6.6 — Helm chart validation in CI
**What:** Test `charts/sharko/` renders correctly with `helm template`.
**Implementation:** Add to CI: `helm template sharko charts/sharko/ --values charts/sharko/values.yaml`

#### 6.7 — Code splitting for UI
**What:** UI JS bundle is ~1MB. Split with dynamic imports.
**Implementation:** React.lazy + Suspense for route-level code splitting. Views loaded on demand.

#### 6.8 — ArgoCD auto-discovery improvement
**Bug:** Hardcodes `argocd-server` service name.
**Fix:** Discover the actual service name by listing services in the ArgoCD namespace, or try common names (`argocd-server`, `argo-cd-argocd-server`, etc.).

### Quality Gates
```bash
go build ./...
go test ./...
cd ui && npm run build && npm test
# E2E: make e2e (new target)
```

### Exit Criteria
- Webhook endpoint receives and processes Git push events
- Conflict detection prevents broken PRs
- `sharko validate` CLI command works
- E2E tests pass in CI
- UI bundle under 500KB initial load

---

## Phase 7 — Documentation + Polish

**Why last:** Documentation reflects the final state. Polish items are nice-to-have.

**Depends on:** Phases 1-6 (features must be stable before documenting)

### Documentation Tasks

#### 7.1 — ReadTheDocs website
**What:** MkDocs + Material theme, hosted docs site (ArgoCD-style).
**Structure:**
- Getting Started (quickstart, installation, first-run)
- User Guide (managing clusters, managing addons, upgrades, GitOps workflow)
- Operator Manual (installation, configuration, upgrading, troubleshooting, security)
- API Reference (all endpoints, examples)
- CLI Reference (all commands, examples)
- Architecture (design decisions, data flow, repo structure)

#### 7.2 — Operator manual
**What:** Installation, configuration, upgrading, troubleshooting guide.
**Covers:** Helm values, env vars, RBAC, ArgoCD setup, secrets provider, production checklist.

#### 7.3 — User guide restructure
**What:** Split current monolithic guide into per-topic pages.

### Polish Tasks

#### 7.4 — AI-parsed release notes
**What:** Use configured AI provider to parse Helm chart changelogs for upgrade comparison.
**Depends on:** Existing AI provider infrastructure.

#### 7.5 — arm64 Docker image
**What:** Re-enable multi-platform build (amd64 + arm64) for Graviton clusters.
**When:** After testing phase is complete and fast iteration is less critical.

#### 7.6 — CLI binary distribution
**What:** goreleaser for macOS/Linux/Windows binaries.

#### 7.7 — Dark mode refinements
**What:** Test dark mode with the sky-blue palette. Fix any contrast issues.

#### 7.8 — Addon dependency ordering
**What:** Declare that addon B depends on addon A (beyond sync waves).

#### 7.9 — Improve test coverage
**Tech debt:** Go coverage is ~40%, UI coverage not measured. Target 70%+.
**Approach:** Focus on critical paths — orchestrator, gitops, API handlers.

### Quality Gates
```bash
# Docs: mkdocs build (no errors)
# Coverage: go test -cover ./... | grep -v "no test files"
# All CI passes
```

### Exit Criteria
- Docs website builds and serves
- Operator manual covers all production setup
- Coverage > 60% (stretch: 70%)
- All TODO.md items addressed

---

## Phase Dependencies (Visual)

```
Phase 1 (Catalog YAML — Single Source of Truth)
    ├──→ Phase 2 (Core Bug Fixes)
    │        ↓
    │    Phase 4 (CLI & API)
    │
    ├──→ Phase 5 (UI Enhancements)
    │
    └──→ Phase 6 (GitOps + DX)

Phase 3 (Security + Notifications) ← independent, parallel with Phase 2

Phase 7 (Docs + Polish) ← after all other phases
```

**Recommended execution order:**
1. Phase 1 (foundation — everything depends on it)
2. Phase 2 + Phase 3 in parallel
3. Phase 4 + Phase 5 in parallel
4. Phase 6
5. Phase 7

---

## TODO.md Coverage Matrix

Every item in `docs/TODO.md` mapped to a phase:

| TODO Item | Phase | Task |
|-----------|-------|------|
| **Critical** | | |
| Refactor: catalog YAML single source of truth | 1 | 1.1-1.3 |
| **High Priority** | | |
| Webhook listener | 6 | 6.1 |
| Conflict detection | 6 | 6.2 |
| Schema validation CI | 6 | 6.3 |
| Audit log for manual changes | 6 | 6.4 |
| Content-Security-Policy header | 3 | 3.1 |
| Strict-Transport-Security header | 3 | 3.2 |
| Document trusted proxy requirement | 3 | 3.3 |
| Document auth-disabled risk | 3 | 3.4 |
| Wire VersionProvider to Helm repo | 3 | 3.5 |
| Security advisory detection | 3 | 3.6 |
| Notification persistence | 3 | 3.7 |
| ReadTheDocs website | 7 | 7.1 |
| Operator manual | 7 | 7.2 |
| User guide restructure | 7 | 7.3 |
| **Medium Priority** | | |
| First-run wizard | 5 | 5.2 |
| Init button in UI | 5 | 5.3 |
| Auto-bootstrap option | 5 | 5.4 |
| CLI login flags | 4 | 4.2 |
| Auto-detect GitHub token | 4 | 4.4 |
| ArgoCD auto-discovery | 6 | 6.8 |
| AI-parsed release notes | 7 | 7.4 |
| arm64 Docker image | 7 | 7.5 |
| CLI binary distribution | 7 | 7.6 |
| Dark mode refinements | 7 | 7.7 |
| Addon dependency ordering | 7 | 7.8 |
| E2E tests | 6 | 6.5 |
| Helm chart validation | 6 | 6.6 |
| Code splitting | 6 | 6.7 |
| Storybook | — | Deferred (low ROI for current team size) |
| Pagination | 4 | 4.5 |
| Filtering/sorting | 4 | 4.6 |
| Rate limiting on writes | 4 | 4.7 |
| **Bugs** | | |
| `sharko status` combined data | 2 | 2.6 |
| `sharko init` read repo URL from connection | 2 | 2.5 |
| `sharko init` PR creation fails silently | 2 | 2.3 |
| `sharko init` perf (batch files) | 2 | 2.4 |
| `sharko init` fails on empty repos | 2 | 2.2 |
| gitopsCfg initialization bug | 2 | 2.1 |
| Friendly error for uninitialized repo | 2 | 2.6 |
| CLI init progress feedback | 2 | 2.7 |
| UI edit addon advanced config | 5 | 5.1 |
| CLI `sharko connect` command | 4 | 4.1 |
| CLI list enabled features | 4 | 4.3 |
| Login background flicker | 2 | 2.8 |
| **Tech Debt** | | |
| `name` vs `appName` reconciliation | 1 | 1.4 |
| Single source of truth design | 1 | 1.1-1.3 |
| Init repo structure cleanup | 1 | 1.5 |
| Test coverage | 7 | 7.9 |
| Login page mascot gap | 5 | 5.5 |

**Deferred (not in this plan):**
- Storybook — low ROI for current team size, revisit when team grows

---

## Branch Strategy

One feature branch per phase:
```
feat/phase-1-catalog-single-source
feat/phase-2-core-bug-fixes
feat/phase-3-security-notifications
feat/phase-4-cli-api
feat/phase-5-ui-enhancements
feat/phase-6-gitops-dx
feat/phase-7-docs-polish
```

Each phase: branch → implement → code review → security audit → merge to main.
QA testing between phases to validate fixes on the real cluster.
