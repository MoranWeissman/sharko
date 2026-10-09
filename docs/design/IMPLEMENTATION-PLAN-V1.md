# Sharko v1.0.0 — Implementation Plan

> Derived from design sections 1-9. This is the authoritative build plan.
> Every feature, every endpoint, every component — nothing deferred except what's explicitly marked post-v1.
>
> **Source design docs:** `docs/design/section-*.md`
> **Existing codebase:** v0.1.0 (rebranded, stripped, working Go backend + React UI + CLI skeleton)

---

## What Exists (v0.1.0 baseline)

Already built and merged:

- Go backend with 60+ read API endpoints
- React UI (fleet dashboard, version matrix, drift detection, observability, connections page, AI assistant, embedded dashboards, user management)
- Helm chart for deployment
- Provider interface (`internal/providers/`) with AWS SM + K8s Secrets implementations
- Orchestrator (`internal/orchestrator/`) with RegisterCluster, DeregisterCluster, UpdateClusterAddons, RefreshCredentials, AddAddon, RemoveAddon, InitRepo
- CLI thin client (`cmd/sharko/`) with login, version, init, add-cluster, remove-cluster, update-cluster, list-clusters, add-addon, remove-addon, status
- Write API endpoints (`internal/api/`) for cluster CRUD, addon CRUD, fleet status, provider info
- Dual auth (session cookies for UI, Bearer tokens for CLI)
- Starter templates (`templates/starter/`) embedded in the binary
- Three docs (user-guide, developer-guide, architecture)
- Logo assets

---

## What Needs to Change or Be Built

### Architecture Changes

1. **Git operations are serialized via a global mutex** — prevents race conditions on branch creation and PR merges. All write API endpoints remain synchronous (return final result, not 202).
2. **Direct commit to main is removed** — every Git operation goes through a PR. Always. The only config is auto-merge vs manual approval.
3. **Sharko manages remote cluster secrets** — creates K8s Secrets directly on remote clusters via temporary K8s client connections. Replaces ESO dependency for addon secrets.
4. **The UI becomes a full management interface** — not read-only. Every CLI/API operation is available in the UI with role-based permissions.

---

## Build Phases

### Phase 1 — Git Mutex & Concurrency Safety

**Why first:** All write operations touch Git. Without a lock, concurrent operations can create conflicting branches or merge race conditions.

**Build:**

1. **Global Git mutex in the orchestrator**
   ```go
   // internal/orchestrator/orchestrator.go
   type Orchestrator struct {
       gitMu        sync.Mutex  // serialize all Git operations
       credProvider  providers.ClusterCredentialsProvider
       argocd       ArgocdClient
       git          gitprovider.GitProvider
       gitops       GitOpsConfig
       paths        RepoPathsConfig
   }
   ```

   Every orchestrator method that touches Git wraps the Git portion in the lock:
   ```go
   func (o *Orchestrator) RegisterCluster(ctx, req) (*RegisterClusterResult, error) {
       // No lock needed — talks to provider and remote cluster
       creds, err := o.credProvider.GetCredentials(req.Name)
       err = o.createAddonSecrets(ctx, req.Name, creds, req.Addons)
       err = o.argocd.RegisterCluster(ctx, ...)

       // Lock for Git operations only
       o.gitMu.Lock()
       defer o.gitMu.Unlock()
       gitResult, err := o.commitViaPR(ctx, files, nil, "register cluster "+req.Name)
       return result, nil
   }
   ```

   Non-Git operations (fetching credentials, creating secrets on remote clusters, ArgoCD API calls) run freely without the lock. Only the Git portion (branch from latest main, commit, open PR, merge) is serialized.

2. **409 duplicate cluster check**
   Before fetching credentials, check if the cluster already exists in ArgoCD. If yes, return error → handler returns 409 Conflict.

3. **API stays synchronous**
   - All write endpoints return the final result (201, 200, 207) not 202
   - No job IDs, no polling, no queue infrastructure
   - The CLI shows progress by waiting for the response
   - For longer operations (init with sync verification), the response streams or the endpoint takes up to 2 minutes

4. **Batch is a sequential loop**
   ```go
   func (o *Orchestrator) RegisterClusterBatch(ctx, clusters []RegisterClusterRequest) []RegisterClusterResult {
       var results []RegisterClusterResult
       for _, cluster := range clusters {
           result, err := o.RegisterCluster(ctx, cluster)
           results = append(results, result)
       }
       return results
   }
   ```
   Sequential, simple, predictable. The Git mutex ensures no race conditions. For 20 clusters at ~10 seconds each, the batch takes ~3 minutes. The caller waits. That's fine for the usage pattern.

**Tests:**
- Two concurrent RegisterCluster calls on different clusters: both succeed, no Git conflicts
- Two concurrent calls on same cluster: one gets 409
- Batch processes sequentially, all PRs succeed
- Git mutex prevents branch/merge race conditions

---

### Phase 2 — Remove Direct Commit, PR-Only Git Flow

**Why now:** Must be in place before any new features. Direct commit to main violates GitOps principles.

**Build:**

1. **Remove `commitDirect` from `internal/orchestrator/git_helpers.go`**
   - Delete the `commitDirect` method entirely
   - `commitChanges` always calls `commitViaPR`
   - After PR creation, if `PRAutoMerge` is true: call `git.MergePullRequest()`, wait for merge to complete
   - If merge fails: return partial success (PR created but not merged)

2. **Remove `DefaultMode` from `GitOpsConfig`**
   - Struct becomes: `PRAutoMerge bool`, `BranchPrefix string`, `CommitPrefix string`, `BaseBranch string`, `RepoURL string`
   - Remove `SHARKO_GITOPS_DEFAULT_MODE` env var
   - Remove any "direct" mode references from docs, help text, API contract

3. **Update `GitResult` response**
   - Remove `mode` field (it's always "pr")
   - Add `merged` boolean and `pr_id` int
   ```go
   type GitResult struct {
       PRUrl      string `json:"pr_url"`
       PRID       int    `json:"pr_id"`
       Branch     string `json:"branch"`
       Merged     bool   `json:"merged"`
       ValuesFile string `json:"values_file,omitempty"`
   }
   ```

4. **Configuration**
   ```yaml
   gitops:
     prAutoMerge: false    # default: false (manual approval)
     branchPrefix: sharko/
     commitPrefix: "sharko:"
     baseBranch: main
   ```

**Tests:**
- Every operation creates a PR (never direct commit)
- Auto-merge: PR created and merged, `merged: true` in response
- Manual: PR created, not merged, `merged: false` in response
- Merge failure: partial success response

---

### Phase 3 — Remote Cluster Secrets Management

**Why here:** This is the biggest differentiator. Must be in place before the full add-cluster flow works correctly with addon secrets.

**Build:**

1. **Remote K8s client builder** (`internal/remoteclient/`)
   - `client.go` — Given a kubeconfig (from provider), build a temporary `kubernetes.Interface` client
   - Connect → perform operations → disconnect. No persistent connections.
   - `secrets.go` — Create/update/delete K8s Secrets on a remote cluster
   - All Sharko-created secrets labeled: `app.kubernetes.io/managed-by: sharko`

2. **Addon secret definitions** (server config)
   - Stored in Helm values / ConfigMap
   - Maps addon name → secret spec (name, namespace, key→provider_path mappings)
   ```yaml
   addonSecrets:
     datadog:
       secretName: datadog-keys
       namespace: datadog
       keys:
         api-key: secrets/datadog/api-key
         app-key: secrets/datadog/app-key
     external-secrets:
       secretName: eso-credentials
       namespace: external-secrets
       keys:
         credentials: secrets/eso/aws-credentials
   ```

3. **API endpoints**
   ```
   POST   /api/v1/addon-secrets                     → define addon secret template
   GET    /api/v1/addon-secrets                      → list addon secret definitions
   DELETE /api/v1/addon-secrets/{addon}              → remove addon secret definition
   GET    /api/v1/clusters/{name}/secrets            → list Sharko-managed secrets on cluster
   POST   /api/v1/clusters/{name}/secrets/refresh    → re-fetch and update secrets on cluster
   ```

4. **Integrate into orchestrator flow**

   The `RegisterCluster` orchestration becomes (as designed in Section 3):
   ```
   Step 1 — Fetch kubeconfig from provider
   Step 2 — Open PR (branch + values file) — ArgoCD sees nothing yet
   Step 3 — Create addon secrets on remote cluster
     - Check which addons in the request have addon-secret definitions
     - For each: fetch secret values from provider, create K8s Secret on remote cluster
     - Verify each secret exists
     - If any fail: return partial success, PR stays open
   Step 4 — Create ArgoCD cluster secret with addon labels
   Step 5 — Merge PR (or wait for approval)
   Step 6 — ArgoCD deploys addons, secrets are already in place ✓
   ```

   The `DeregisterCluster` orchestration:
   ```
   Step 1 — Remove addon labels from ArgoCD (ArgoCD stops managing addons)
   Step 2 — Delete Sharko-managed secrets from remote cluster
   Step 3 — Delete ArgoCD cluster secret
   Step 4 — Delete values file via PR
   ```

   The `UpdateClusterAddons` orchestration:
   ```
   Enabling an addon:
     - Create addon secrets on remote cluster (if defined)
     - Add label to ArgoCD cluster secret
     - Update values file via PR

   Disabling an addon:
     - Remove label from ArgoCD cluster secret
     - Delete addon secrets from remote cluster
     - Update values file via PR
   ```

5. **CLI commands**
   ```bash
   sharko add-addon-secret datadog \
     --secret-name datadog-keys \
     --namespace datadog \
     --key api-key=secrets/datadog/api-key \
     --key app-key=secrets/datadog/app-key

   sharko list-secrets prod-eu
   sharko refresh-secrets prod-eu
   ```

6. **ArgoCD resource exclusion**
   - Document: add resource exclusion to ArgoCD config so it ignores secrets with `app.kubernetes.io/managed-by: sharko`
   - Or: Sharko sets this up during init via the ArgoCD API

**Tests:**
- Create secret on remote cluster (mock K8s client)
- Delete secret from remote cluster
- Full RegisterCluster flow: secrets created before PR merge
- Full DeregisterCluster flow: secrets deleted after addon removal
- Partial success: secret creation fails, PR stays open
- Addon without secret definition: no secret operations, just labels

---

### Phase 4 — API Keys for Automation

**Why here:** IDP and CI/CD integration needs long-lived tokens. Must be in place before the product is usable for automation.

**Build:**

1. **Token storage** (`internal/auth/`)
   - Extend existing auth system
   - `tokens.go` — Token struct (ID, name, hash, role, created_at, last_used_at)
   - Tokens stored as K8s Secret (hashed with bcrypt, same as passwords)
   - Token format: `sharko_` prefix + 32 random hex chars = 39 chars total

2. **Token CRUD**
   ```
   POST   /api/v1/tokens              → create token (returns plaintext ONCE)
   GET    /api/v1/tokens              → list tokens (name, role, created, last_used — no plaintext)
   DELETE /api/v1/tokens/{name}       → revoke token
   ```

3. **Auth middleware update**
   - Current: checks session cookie OR Bearer token (session-based)
   - New: also check if Bearer token matches a stored API key hash
   - Priority: session cookie → session token → API key
   - Update `last_used_at` on each API key authentication

4. **CLI commands**
   ```bash
   sharko token create --name backstage-prod --role admin
   # → Token: sharko_a8f2b4c6d8e0f2a4b6c8d0e2f4a6b8c0
   # → Store this securely. It won't be shown again.

   sharko token list
   sharko token revoke backstage-prod
   ```

5. **UI** (Settings → API Keys page)
   - List all tokens (name, role, created, last used)
   - "Create API Key" button → modal: name + role → shows token ONCE with copy button
   - "Revoke" button on each row → confirmation → delete

**Tests:**
- Create token, use it to authenticate, verify access
- Revoke token, verify it no longer works
- Token with viewer role can't call write endpoints (403)
- List tokens never shows plaintext
- Last used timestamp updates

---

### Phase 5 — Bootstrap Flow (Init Rework)

**Why here:** The init flow needs to use the PR-only flow and secret management. Rebuild it on the new foundation.

**Build:**

1. **Rework `InitRepo` orchestrator method**

   Full flow (as designed in Section 8):
   ```
   Step 1 — Check if repo already initialized (409 if bootstrap/root-app.yaml exists)
   Step 2 — Generate repo structure from embedded starter templates, replace placeholders
   Step 3 — Push to Git via PR (always PR, auto-merge or manual)
   Step 4 — Add repo connection to ArgoCD (POST /api/v1/repositories)
   Step 5 — Create AppProject in ArgoCD
   Step 6 — Create root Application in ArgoCD pointing at bootstrap/
   Step 7 — Block until sync completes or times out (up to 2 minutes), return final status
   ```

2. **Add `AddRepository` to ArgoCD client**
   ```go
   func (c *Client) AddRepository(ctx context.Context, repoURL, username, password string) error
   ```
   POST to ArgoCD `/api/v1/repositories` with Git credentials from server config.

3. **Init sync verification** (CLI and UI only)
   - After creating the Application, poll `GET /api/v1/applications/{name}` every 5 seconds
   - Timeout: 2 minutes
   - Success: root-app synced, ApplicationSets created
   - Failure: show ArgoCD error message (ComparisonError, repo unreachable, Helm rendering failed)
   - API response: the init endpoint blocks until sync completes or times out. Returns the final status (synced, failed, or timeout) in the response body. No polling needed — the caller gets the result when the request completes.

4. **Auto-bootstrap on first startup**
   ```yaml
   init:
     autoBootstrap: true  # default: false
   ```
   Server startup: if `autoBootstrap=true` AND all connections configured AND repo is empty → auto-run InitRepo. Enables fully automated deployment via Terraform/Helm without any manual step.

5. **Connections page as smart status dashboard**
   - Show status for each connection: Git ✓/✗, ArgoCD ✓/✗, Provider ✓/✗
   - "Initialize" button appears when all connections are green and repo isn't initialized
   - Provider configuration section (type, region)
   - Not a separate wizard — the existing connections page enhanced with status indicators

**Tests:**
- Full init flow: templates pushed → repo added to ArgoCD → project created → app created → sync verified
- Already initialized: 409 returned
- Sync timeout: partial success with ArgoCD error
- Auto-bootstrap: server auto-initializes on startup when conditions met
- Missing connection: clear error about what's not configured

---

### Phase 6 — Batch Operations

**Why here:** Depends on the Git mutex (Phase 1) and secrets management (Phase 3).

**Build:**

1. **Batch API endpoint**
   ```
   POST /api/v1/clusters/batch → process N clusters sequentially, return all results
   ```
   Synchronous — blocks until all clusters are processed. Max batch size: 10 (to stay within HTTP timeout limits; ~10s per cluster = ~100s max). For larger batches, the caller splits into multiple requests.

2. **Discover available clusters**
   ```
   GET /api/v1/clusters/available → list provider clusters with registered status
   ```
   Uses `provider.ListClusters()`, cross-references with ArgoCD cluster list, returns unregistered clusters.

3. **Batch execution**
   - `POST /api/v1/clusters/batch` processes clusters sequentially (Git mutex ensures no conflicts)
   - Each cluster gets its own PR
   - Response returns when all are done, with per-cluster success/failure status
   - One failed cluster doesn't block the rest — failures are collected and reported at the end

4. **CLI batch commands**
   ```bash
   sharko add-clusters cluster-1,cluster-2,cluster-3 --addons monitoring,logging
   sharko add-clusters --from-provider --addons monitoring,logging
   ```
   - `--from-provider`: discover available clusters, interactive selection
   - Shows sequential progress: each cluster status as it completes
   - If more than 10: CLI auto-splits into batches of 10
   - Final summary: N succeeded, M failed, with details

5. **UI batch add**
   - Clusters page → "Add Clusters" button
   - Multi-step form: select from provider → choose addons → review → submit
   - Progress table updating as each cluster completes sequentially

**Tests:**
- Batch processes clusters sequentially, returns all results
- Batch respects max size (10), rejects larger with 400
- One failed cluster doesn't block the rest
- Discover endpoint cross-references provider and ArgoCD
- CLI auto-splits large batches
- Partial batch: some succeed, some fail, report is clear

---

### Phase 7 — UI Write Capabilities

**Why here:** All backend infrastructure is in place (Git mutex, secrets, API keys, PR flow). Now surface it in the UI.

**Build:**

1. **Role-based UI rendering**
   - Fetch user role from session on login
   - Admin: all action buttons visible
   - Operator: limited actions (refresh, sync)
   - Viewer: read-only, no action buttons
   - Store role in React context, conditionally render action elements

2. **Add Cluster form** (Clusters page → "Add Cluster" button)
   - Fields: cluster name, region (optional), addon multi-select from catalog
   - Submit: POST /api/v1/clusters → show loading spinner → wait for synchronous response
   - Success: show PR URL, cluster appears in list
   - Partial: show what succeeded/failed with guidance
   - Error: show error message, form stays open for retry

3. **Remove Cluster** (Cluster detail page → "Remove Cluster" button)
   - Red destructive styling
   - Confirmation modal with warning about ArgoCD cascade + secret deletion
   - Submit → loading spinner → response → success or error

4. **Toggle addons on cluster** (Cluster detail page → addon list)
   - Toggle switches for each addon (admin only)
   - Changes accumulate, "Apply Changes" button appears
   - Review modal: what will be enabled/disabled, secrets impact
   - Submit → loading spinner → response

5. **Add Addon to catalog** (Addons page → "Add Addon" button)
   - Fields: name, chart, repo URL, version, namespace, sync wave
   - Submit → loading spinner → response with PR URL

6. **Remove Addon** (Addon detail page → "Remove Addon" button)
   - Shows impact preview first (dry-run): affected clusters, deployment count
   - Type-to-confirm (GitHub-style): type addon name to proceed
   - Submit → loading spinner → response

7. **Addon secrets configuration** (Addon detail page → "Secrets" tab)
   - Define secret template: name, namespace, key→provider_path mappings
   - Save → stored in server config
   - Shows which clusters have these secrets deployed

8. **API Keys management** (Settings → API Keys)
   - List all tokens (name, role, created, last used)
   - Create: modal → name + role → shows token ONCE → copy button → dismiss
   - Revoke: button per row → confirmation

9. **Initialize repo** (Connections page)
   - Status indicators for each connection (✓/✗)
   - Provider configuration section
   - "Initialize" button when all connections are green
   - Progress display for init steps including ArgoCD sync verification

10. **Batch cluster add** (Clusters page → "Add Clusters")
    - Multi-step: discover from provider → select → configure addons → review → submit
    - Progress table updating as each cluster completes

**Tests:**
- Role-based rendering: viewer sees no buttons, admin sees all
- Form validation matches API validation
- Loading states work correctly (spinner during synchronous API call)
- Destructive operations require confirmation
- API key modal shows token once, copy works

---

### Phase 8 — Addon Upgrades, Default Addons & Sync Wave Support

**Build:**

1. **Addon upgrade — global and per-cluster**

   Two upgrade paths matching how the AppSet template resolves versions:

   **Global upgrade:** Change version in the addon catalog. Affects every cluster using the global version.
   ```bash
   sharko upgrade-addon cert-manager --version 1.15.0
   # Updates catalog entry → opens PR → ArgoCD rolls out to all clusters using global version
   ```

   **Per-cluster override:** Change version in a specific cluster's values file. Only affects that cluster.
   ```bash
   sharko upgrade-addon cert-manager --version 1.15.0 --cluster prod-eu
   # Updates prod-eu.yaml → opens PR → only prod-eu gets the new version
   ```

   **Multi-addon upgrade in one PR:**
   ```bash
   sharko upgrade-addons cert-manager=1.15.0,metrics-server=0.7.1
   # Updates multiple catalog entries → one PR → ArgoCD handles the rest
   ```

   **Pre-upgrade version check:** The existing `GET /api/v1/upgrade/{addonName}/versions` endpoint queries the Helm repo for available versions. The CLI and UI show available versions before upgrading:
   ```bash
   sharko upgrade-addon cert-manager
   # No --version specified → shows available versions:
   # Current: 1.14.5 (global)
   # Available: 1.15.0, 1.14.6, 1.14.5
   # Clusters with override: prod-eu (1.13.2)
   # Select version: _
   ```

   **Orchestrator methods:**
   ```go
   func (o *Orchestrator) UpgradeAddonGlobal(ctx, addonName, newVersion) (*GitResult, error)
   func (o *Orchestrator) UpgradeAddonCluster(ctx, addonName, clusterName, newVersion) (*GitResult, error)
   func (o *Orchestrator) UpgradeAddons(ctx, upgrades map[string]string) (*GitResult, error)
   ```

   **API endpoints:**
   ```
   POST /api/v1/addons/{name}/upgrade
     { "version": "1.15.0" }                          → global upgrade
     { "version": "1.15.0", "cluster": "prod-eu" }    → per-cluster upgrade
   
   POST /api/v1/addons/upgrade-batch
     { "upgrades": {"cert-manager": "1.15.0", "metrics-server": "0.7.1"} }
   ```

   **UI:** Addon detail page → "Upgrade" button → shows current version, available versions from Helm repo, option to upgrade globally or per-cluster. Version matrix page → highlight clusters with outdated versions → bulk upgrade button.

   **Tests:**
   - Global upgrade changes catalog version, opens PR
   - Per-cluster upgrade changes cluster values file, opens PR
   - Multi-addon upgrade creates one PR with all changes
   - Version check queries Helm repo correctly
   - Cluster with override is not affected by global upgrade (unless explicitly targeted)

2. **Default addons** (server config)
   ```yaml
   defaults:
     clusterAddons:
       monitoring: true
       logging: true
       cert-manager: true
   ```
   When `add-cluster` is called without specifying addons, merge defaults. When addons are specified, use those instead (explicit overrides defaults).

3. **Sync wave support in add-addon**
   ```bash
   sharko add-addon istio-base --chart istio/base --version 1.20.0 --sync-wave -1
   sharko add-addon istiod --chart istio/istiod --version 1.20.0 --sync-wave 1
   ```
   The sync wave value is written into the AppSet template entry as an ArgoCD annotation: `argocd.argoproj.io/sync-wave: "-1"`

4. **Host cluster special-casing in starter template**
   - The starter AppSet template includes the conditional: if cluster name matches the host cluster, deploy to `in-cluster` instead of the registered cluster name
   - Host cluster name configured via server config: `SHARKO_HOST_CLUSTER_NAME`

**Tests:**
- Default addons merge correctly
- Explicit addons override defaults
- Sync wave annotation appears in generated AppSet entry
- Host cluster deploys to in-cluster

---

### Phase 9 — Documentation & Polish

**Build:**

1. **Update all docs to match v1.0.0 reality**
   - README: update quickstart, API reference, CLI commands (including upgrade-addon, batch, API keys)
   - API contract: update all endpoints — synchronous responses, new secret/upgrade/batch endpoints
   - User guide: add sections for secrets management, batch operations, API keys, UI write operations, addon upgrades
   - Developer guide: add sections for Git mutex, remote client, addon secrets, orchestrator flow
   - Architecture guide: update with secret management flow, PR-only git flow, remote K8s client pattern

2. **Update vision doc**
   - Remove outdated sections (standalone CLI mode, sharko.yaml, direct commit, job queue)
   - Add new sections (remote secrets, UI write capabilities, addon upgrades)

3. **Clean up Helm chart values.yaml**
   - Add all new configuration: default addons, addon secrets, auto-bootstrap, host cluster name, gitops PR settings
   - Document each value with comments
   - Ensure `helm template` renders correctly with defaults

4. **Remove all direct commit code remnants**
   - Grep codebase for `commitDirect`, `DefaultMode`, `SHARKO_GITOPS_DEFAULT_MODE`, `mode: "direct"`
   - Remove completely

5. **Final production data audit**
   - Grep entire codebase for company names, real AWS accounts, real emails, internal URLs
   - Verify starter templates are clean

---

## Phase Dependencies

```
Phase 1 (Git Mutex & Safety)
    ↓
Phase 2 (PR-only Git)
    ↓
Phase 3 (Remote Secrets) ← depends on Phase 2 (PR flow)
    ↓
Phase 4 (API Keys) ← independent, can parallel with Phase 3
    ↓
Phase 5 (Init Rework) ← depends on Phase 2 + Phase 3
    ↓
Phase 6 (Batch) ← depends on Phase 3 (secrets)
    ↓
Phase 7 (UI Write) ← depends on all backend phases (1-6)
    ↓
Phase 8 (Upgrades, Defaults & Sync Waves) ← can parallel with Phase 7
    ↓
Phase 9 (Docs & Polish) ← last, after everything is built
```

**Parallelizable:**
- Phase 3 + Phase 4 can run in parallel (independent backends)
- Phase 7 + Phase 8 can run in parallel (UI + backend config)

---

## Summary Table

| Phase | What | Key Deliverable |
|-------|------|-----------------|
| 1 | Git Mutex & Safety | Global Git lock, 409 duplicate check, synchronous API, batch as sequential loop |
| 2 | PR-Only Git | Remove direct commits, every change is a PR |
| 3 | Remote Cluster Secrets | Create K8s Secrets on remote clusters, replace ESO dependency |
| 4 | API Keys | Long-lived tokens for automation, CLI + UI management |
| 5 | Init Rework | Full bootstrap: repo + ArgoCD repo connection + root-app + sync verification |
| 6 | Batch Operations | Batch cluster registration, discover from provider, sequential execution (max 10) |
| 7 | UI Write Capabilities | Full management UI: add/remove clusters, addons, secrets, API keys |
| 8 | Upgrades, Defaults & Sync Waves | Addon upgrades (global + per-cluster), default addons, sync wave ordering, host cluster |
| 9 | Docs & Polish | Update all docs, clean Helm chart, final audit |

---

## Non-Functional Requirements

| Requirement | Implementation |
|-------------|---------------|
| Concurrency safety | Global Git mutex — serializes all Git operations, non-Git operations run freely |
| Git conflict prevention | PR-only flow, sequential merges per resource |
| Secret security | Never in ArgoCD pipeline, never in Git, managed-by label for ArgoCD exclusion |
| API backwards compatibility | v1 response shapes locked after release |
| Role-based access | Admin/operator/viewer enforced server-side, UI hides elements per role |
| Rate limiting | Post-v1 if needed — low-frequency usage doesn't require it |
| Audit trail | Every change is a PR in Git history |

---

## What Is Explicitly Post-v1

| Feature | Why Deferred |
|---------|-------------|
| Webhooks / event emission | API response is sufficient for v1. Build when async notification demand exists. |
| Credential auto-rotation | Manual `sharko refresh-cluster` works. Auto-rotation needs a reconcile loop (operator territory). |
| Platform-specific integrations | API is the integration surface. Community builds plugins. |
| Operator / CRDs | Server-first with Git mutex covers v1. Operator is v2 if adoption demands it. |
| Multi-source addon automation | Document as advanced customization. User edits AppSet template. |
| Node separation / tolerations | Document as advanced customization. User edits per-cluster values. |
| Per-addon ignoreDifferences automation | Document for now. |
| Fine-grained API scopes | Token roles (admin/operator/viewer) are sufficient. Per-endpoint scopes later. |
| Job queue / async API | Not needed for v1 usage patterns. Add if high-concurrency demand emerges. |
| SSE/WebSocket for progress | Polling or synchronous response is fine for v1. Real-time push if demand exists. |
| Rate limiting | Low-frequency usage. Add if abuse becomes a concern. |
