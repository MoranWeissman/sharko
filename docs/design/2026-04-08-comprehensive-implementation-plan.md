# Sharko — Comprehensive Implementation Plan

> Covers all open items from TODO-v2.md, organized into prioritized phases.
> Each task specifies the agent role, files to modify, and expected outcome.

---

## Phase 1: AWS SM Kubeconfig Builder + STS Auth (BLOCKER)

**Why first:** Can't test add-cluster or secrets without this. Currently Sharko expects raw kubeconfig YAML in AWS SM but real secrets have structured JSON keys.

### Task 1.1: Auto-detect SM secret format + build kubeconfig
**Agent:** Go Expert
**Files:** `internal/providers/aws_sm.go`
**What:**
- In `GetCredentials`, after fetching the secret value, try JSON parse first
- If JSON with `host` key → structured format → build kubeconfig from keys:
  - `host` → server URL
  - `caData` → base64-decoded CA certificate
  - `clusterName` → cluster name for context
  - `region` → for STS endpoint
  - `accountId` → for role ARN construction
- If not JSON → treat as raw kubeconfig YAML (current behavior)
- Return the same `*Kubeconfig` struct either way

### Task 1.2: STS-based token generation for EKS
**Agent:** Go Expert
**Files:** `internal/providers/aws_auth.go` (new)
**What:**
- Implement `GetEKSToken(ctx, clusterName, region string) (string, error)`
- Uses STS presigned GetCallerIdentity URL (same approach as argocd-k8s-auth)
- Encodes as `k8s-aws-v1.<base64url(presigned-url)>`
- Token valid for 15 minutes
- Called by `GetCredentials` when building kubeconfig from structured JSON
**Dependencies:** `aws-sdk-go-v2/service/sts`

### Task 1.3: Update ListClusters for structured format
**Agent:** Go Expert
**Files:** `internal/providers/aws_sm.go`
**What:**
- `ListClusters` currently just returns secret names
- Enhance: parse the JSON to extract region, environment, project from the secret
- Populate `ClusterInfo.Region` and `ClusterInfo.Tags` from the JSON fields

### Task 1.4: Tests
**Agent:** Test Engineer
**Files:** `internal/providers/aws_sm_test.go`
**What:**
- Test structured JSON → kubeconfig builder
- Test raw kubeconfig passthrough
- Test STS token generation (mock STS client)
- Test ListClusters with structured secrets

---

## Phase 2: Core UX Fixes (Unblocks smooth testing)

### Task 2.1: Wizard escape button
**Agent:** Frontend Expert
**Files:** `ui/src/components/FirstRunWizard.tsx`
**What:** Add an X button or "Skip to Dashboard" link at the top of the wizard card. Calls `refreshConnections()` + `navigate('/dashboard')`.

### Task 2.2: Bootstrap resume message
**Agent:** Frontend Expert
**Files:** `ui/src/components/FirstRunWizard.tsx`, `ui/src/App.tsx`
**What:** When wizard opens at Step 4 (resume), show a banner: "Resuming setup — your connection is configured, just need to initialize the repository."

### Task 2.3: Auto-merge branch cleanup
**Agent:** Go Expert
**Files:** `internal/orchestrator/git_helpers.go`
**What:** After `MergePullRequest` succeeds, call `DeleteBranch(ctx, branchName)`. Best-effort — log warning if delete fails, don't fail the operation.

### Task 2.4: Init from Settings — show sync progress in UI
**Agent:** Frontend Expert
**Files:** `ui/src/views/Settings.tsx` (or relevant settings component)
**What:** When initializing from the Settings page (not the wizard), the UI should poll sync status and display progress, matching the wizard's polling behavior.

### Task 2.5: Auto-detect GitHub token from Helm secret
**Agent:** Go Expert
**Files:** `internal/api/connections.go` (or settings handler)
**What:** Connection setup should check if `GITHUB_TOKEN` exists in the Sharko pod's environment variables and pre-fill the token field in the response. This removes the friction of entering the token twice when it was passed via `--set secrets.GITHUB_TOKEN` during install.

### Task 2.6: Mark completed design decision items
**Agent:** Docs Writer
**Files:** `docs/TODO-v2.md`
**What:** Mark all design decision items that are already implemented as `[x]` (Helm secrets removed, devMode removed, single connection, operations engine, heartbeat, PR polling, SecretProvider, reconciler).

---

## Phase 3: Cluster Management (Core product flow)

### Task 3.1: Managed vs discovered clusters
**Agent:** Go Expert + Frontend Expert (parallel)
**Go files:** `internal/service/cluster.go`, `internal/api/clusters.go`
**UI files:** `ui/src/views/ClustersOverview.tsx`, `ui/src/views/Dashboard.tsx`
**What:**
- Backend: `ListClusters` returns a `managed` boolean per cluster (true if in cluster-addons.yaml)
- Frontend: Dashboard shows two sections — "Managed Clusters" and "Discovered Clusters (ArgoCD)"
- Discovered clusters get a "Start Managing" button

### Task 3.2: Adopt existing ArgoCD clusters
**Agent:** Go Expert
**Files:** `internal/orchestrator/cluster.go`, `internal/api/clusters_write.go`
**What:**
- New endpoint or flag: `POST /api/v1/clusters` with `adopt: true`
- If cluster already exists in ArgoCD, skip registration, just add to cluster-addons.yaml + create values file
- Return success with `adopted: true` in response

### Task 3.3: Cluster connectivity check
**Agent:** Go Expert
**Files:** `internal/api/clusters.go` (new handler)
**What:**
- `POST /api/v1/clusters/{name}/test` — tests if Sharko can connect to the cluster
- Uses the credentials provider to get kubeconfig, tries a simple API call (server version)
- Returns connection status + cluster info

---

## Phase 4: Addon Detail Improvements

### Task 4.1: AppSet status on addon detail
**Agent:** Go Expert + Frontend Expert (parallel)
**Go files:** `internal/service/addon.go`, `internal/argocd/client.go`
**UI files:** `ui/src/views/AddonDetail.tsx`
**What:**
- Backend: fetch ApplicationSet status from ArgoCD for the addon (sync status, conditions, generated app count)
- Frontend: show AppSet health card in addon Overview section

### Task 4.2: Advanced config field help text
**Agent:** Frontend Expert
**Files:** `ui/src/views/AddonDetail.tsx`
**What:**
- Add tooltip/help icon next to each advanced config field
- syncWave: "Deploy order: negative = first, 0 = default, positive = last"
- selfHeal: "ArgoCD auto-reverts manual changes to match Git"
- syncOptions: "ArgoCD sync options, e.g. ServerSideApply=true, CreateNamespace=true"
- ignoreDifferences: example in placeholder
- additionalSources: example in placeholder

### Task 4.3: Make ignoreDifferences + additionalSources editable
**Agent:** Frontend Expert + Go Expert (parallel)
**UI files:** `ui/src/views/AddonDetail.tsx`
**Go files:** `internal/orchestrator/addon_configure.go`
**What:**
- Frontend: JSON/YAML editor with example template for both fields; both send as part of the PATCH configure request
- Backend: `ConfigureAddon` currently rejects these fields — update to handle them and write as YAML blocks in the catalog

### Task 4.4: Fix upgrade advisor
**Agent:** Go Expert
**Files:** `internal/service/upgrade.go`, `internal/helm/fetcher.go`
**What:**
- Verify the Helm repo fetch works for each addon's repoURL
- The version list should populate even when no clusters have the addon enabled (it's a catalog-level feature)
- Check if the changelog/compare endpoint works end-to-end

### Task 4.5: Clarify version comparison UI labels
**Agent:** Frontend Expert
**Files:** `ui/src/views/AddonDetail.tsx`
**What:**
- Rename "20+ latest versions" → "Available Versions" with subtitle "from Helm repository"
- Rename "Compare versions" → "Version Changelog" with subtitle "compare release notes between versions"

---

## Phase 5: Git Operations Polish

### Task 5.1: ArgoCD resource exclusion for Sharko-managed secrets
**Agent:** K8s Expert
**Files:** `docs/site/operator/configuration.md`, `templates/bootstrap/` (if needed)
**What:**
- Document how to configure ArgoCD to ignore Sharko-managed secrets (resource exclusion by label)
- Optionally: Sharko sets this up during init via ArgoCD API
- Config: add `resource.exclusions` to ArgoCD ConfigMap for `app.kubernetes.io/managed-by: sharko` labeled secrets

### Task 5.2: ArgoCD auto-discovery improvement
**Agent:** Go Expert
**Files:** `internal/argocd/client.go`
**What:**
- Instead of hardcoded common names, list all services in the ArgoCD namespace
- Find services with port 80/443 that respond to `/api/v1/version`
- Return the first one that works

---

## Phase 6: API & Backend Improvements

### Task 6.1: Filtering/sorting on list endpoints
**Agent:** Go Expert
**Files:** `internal/api/pagination.go`, `internal/api/clusters.go`, `internal/api/addons.go`
**What:**
- Add `?sort=name`, `?sort=status`, `?filter=name:prod*` query params
- Implement in the existing `parsePagination` helper
- Apply before pagination

### Task 6.2: Audit log for manual changes
**Agent:** Go Expert
**Files:** `internal/api/webhooks.go`, `internal/audit/` (new package)
**What:**
- On webhook push events, diff the changes and log which files changed and by whom
- Store audit entries (in-memory + optional file persistence like notifications)
- `GET /api/v1/audit` endpoint to retrieve audit log
- Show in UI as "External Changes" section

### Task 6.3: Security advisory detection
**Agent:** Go Expert
**Files:** `internal/notifications/checker.go`, `internal/helm/`
**What:**
- When fetching Helm chart versions, also check for CVE mentions in release notes
- Flag security-relevant upgrades differently (TypeSecurity notification)
- Use keyword matching: "CVE-", "security", "vulnerability", "patch"

---

## Phase 7: Documentation & Testing

### Task 7.1: Update TODO-v2 with completed items
**Agent:** Docs Writer
**Files:** `docs/TODO-v2.md`
**What:** Mark all completed items, add new items found during implementation

### Task 7.2: E2E test framework
**Agent:** Test Engineer + DevOps Agent
**Files:** `.github/workflows/`, `tests/e2e/` (new)
**What:**
- Kind cluster + ArgoCD setup in CI
- Basic flow: deploy Sharko → init → add addon → verify AppSet created
- Not full coverage — just the critical path

### Task 7.3: AI-parsed release notes (V1.x)
**Agent:** Go Expert
**Files:** `internal/ai/tools_write.go`, `internal/api/addons_changelog.go`
**What:**
- When AI provider is configured, pass changelog to AI for summary
- Show AI summary alongside raw changelog in addon detail

### Task 7.4: Addon dependency ordering (V1.x)
**Agent:** Go Expert + K8s Expert
**Files:** `internal/models/addon.go`, `templates/bootstrap/templates/addons-appset.yaml`
**What:**
- Add `dependsOn: []string` field to AddonCatalogEntry
- Validate dependency graph (no cycles)
- Map to ArgoCD sync waves automatically

---

## Phase Summary & Agent Assignments

| Phase | Items | Primary Agent | Support Agent |
|-------|-------|---------------|---------------|
| 1 | AWS SM format + STS auth | Go Expert | Test Engineer |
| 2 | Core UX fixes | Frontend Expert | Go Expert (branch cleanup, token detect) |
| 3 | Cluster management | Go Expert | Frontend Expert |
| 4 | Addon detail | Frontend Expert | Go Expert (upgrade advisor, configure) |
| 5 | Git operations polish | K8s Expert | Go Expert |
| 6 | API improvements | Go Expert | — |
| 7 | Docs & testing | Docs Writer | Test Engineer + DevOps |

## Execution Order

```
Phase 1 (BLOCKER — AWS SM)
    ↓
Phase 2 (UX fixes) — parallel with Phase 1
    ↓
Phase 3 (Cluster management) — needs Phase 1
    ↓
Phase 4 + 5 — parallel (addon detail + git polish)
    ↓
Phase 6 (API improvements)
    ↓
Phase 7 (docs + testing — after everything stabilizes)
```

## Items Explicitly Deferred

- Multi-cloud providers (GCP, Azure) — V1.x, after AWS is solid
- Full K8s operator with CRDs — V2
- Storybook — low ROI
- GitOps-only ESO reference template — separate deliverable
