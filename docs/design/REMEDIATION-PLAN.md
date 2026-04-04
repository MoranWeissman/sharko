# Sharko v1.0.0 — Remediation Plan

> Gaps found between design docs (sections 1-9) and actual implementation.
> Ordered by severity. Each item is an implementable task.

---

## Critical — Orchestration Ordering

### R1. Fix RegisterCluster step order

**Gap:** ArgoCD registration happens before PR and before secrets. Design requires: PR first (the gate), then secrets, then ArgoCD.

**Current order:** Validate → Duplicate check → Fetch creds → **ArgoCD register** → Create secrets → Git PR

**Correct order:** Validate → Duplicate check → Fetch creds → **Create secrets on remote cluster** → **Git PR (create + merge)** → **ArgoCD register** (cluster only sees merged values file + secrets already in place)

**Design intent:** ArgoCD must NOT see the cluster until: (a) secrets exist on the remote cluster, (b) the values file is merged to main. If ArgoCD registers before merge, it tries to deploy addons against a values file that only exists on a feature branch. The sequence is: secrets → merged values file → ArgoCD registration → ArgoCD deploys with everything in place.

**Why this order:**
- Secrets first: addon deployments need secrets to exist when they start
- PR merge second: the values file must be on main for the AppSet git generator to find it
- ArgoCD register last: the cluster + labels trigger the AppSet to create Applications — by this point secrets and values are both ready

**Files:** `internal/orchestrator/cluster.go` (RegisterCluster method)

**Partial success scenarios:**
- Secret creation fails → partial success: nothing committed to Git, nothing in ArgoCD, secrets partially created (report which succeeded)
- PR creation/merge fails → partial success: secrets created but no values file in Git, no ArgoCD registration. Caller gets PR URL for manual merge
- ArgoCD registration fails → partial success: secrets exist, values merged, but ArgoCD doesn't see the cluster. Caller can retry ArgoCD registration manually
- Each step is independently safe to retry

---

### R2. Fix UpdateClusterAddons — secrets before labels for enabling

**Gap:** When enabling addons, labels are applied to ArgoCD BEFORE secrets are created.

**Current order:** Update all labels → Create/delete secrets → Git PR

**Correct order for enabling:** Create secrets → Update values file via PR (merge) → Add ArgoCD labels
**Correct order for disabling:** Remove ArgoCD labels → Delete secrets → Update values file via PR (merge)

**Why it matters:** Same principle as R1 — secrets and values must exist before ArgoCD labels trigger deployment.

**Files:** `internal/orchestrator/cluster.go` (UpdateClusterAddons method)

**Note:** Need to split addon map into newly-enabled and newly-disabled, process them in different orders. For a mixed update (some enabling, some disabling), process disabling first (safe — removes deployments), then enabling (needs secrets + values before labels).

---

### R3. Fix DeregisterCluster — graceful addon removal before cluster delete

**Gap:** Code hard-deletes the ArgoCD cluster immediately. Design says: remove addon labels first (so ArgoCD prunes addon Applications gracefully), then delete secrets, then delete the cluster registration, then delete values via PR.

**Current:** `argocd.DeleteCluster()` → delete secrets → Git PR

**Correct:** Remove addon labels from ArgoCD → Poll until addon Applications are deleted (timeout 60s) → Delete secrets → `argocd.DeleteCluster()` → Git PR

**Prune strategy:** After removing labels, ArgoCD's ApplicationSet controller detects the label change and deletes the generated Applications. Poll `argocd.GetClusterApplications(clusterName)` every 5s until the count drops to 0 or timeout (60s). If timeout: log warning but continue — the Applications may still be pruning, but we proceed with secret deletion and cluster removal. This is best-effort graceful; the hard delete at the end is the safety net.

**Files:** `internal/orchestrator/cluster.go` (DeregisterCluster method)

**Needs:**
- Read cluster's current labels from ArgoCD to know which addons to unlabel
- `UpdateClusterLabels` to remove addon labels (set all to empty/remove)
- Polling helper similar to `waitForSync` in init.go
- `argocd.GetClusterApplications` or equivalent to check remaining apps

---

## High — Feature Gaps

### R4. Addon secret definitions — persist to K8s

**Gap:** Definitions created via `POST /api/v1/addon-secrets` are in-memory only. Lost on restart.

**Fix:** Persist addon secret definitions on every create/delete:
- **K8s mode:** Write to a K8s ConfigMap (`sharko-addon-secrets` in the sharko namespace). On startup, load from ConfigMap. Follow the existing `config.K8sStore` pattern.
- **Local dev mode:** Write to a JSON file (`~/.sharko/addon-secrets.json` or a path from `SHARKO_ADDON_SECRETS_FILE`). On startup, load from file. Fallback to `SHARKO_ADDON_SECRETS` env var if file doesn't exist.

**Files:** `internal/api/addon_secrets.go`, `internal/api/router.go`, new `internal/config/addon_secrets_store.go`

**Pattern:** Create an `AddonSecretStore` interface with `Save(defs)` / `Load() defs` methods. K8s implementation writes to ConfigMap, local implementation writes to JSON file. Same pattern as `config.Store` for connections.

---

### R5. Sync wave — wire into starter AppSet template

**Gap:** `syncWave` field is saved to catalog but the starter AppSet template never reads it. The annotation never appears on ArgoCD Applications.

**Fix:** Update `templates/starter/bootstrap/templates/addons-appset.yaml` to conditionally emit `argocd.argoproj.io/sync-wave` annotation when the catalog entry has a `syncWave` field.

**Files:** `templates/starter/bootstrap/templates/addons-appset.yaml`

**Note:** This only affects newly initialized repos. Existing repos need manual template update. A future `sharko upgrade-templates` command could automate this (post-v1).

---

### R6. Host cluster in-cluster routing — wire into starter template

**Gap:** `SHARKO_HOST_CLUSTER_NAME` is read in serve.go but never injected into templates or used by the AppSet.

**Fix:** 
1. Add `SHARKO_HOST_CLUSTER_NAME` to `replacePlaceholders()` in `init.go`
2. Update the starter AppSet template to conditionally route to `in-cluster` when the cluster name matches

**Files:** `internal/orchestrator/init.go`, `templates/starter/bootstrap/templates/addons-appset.yaml`

---

## Medium — UI & UX Gaps

### R7. Connections page — live status indicators

**Gap:** Connections page shows static config. Design calls for per-connection live status (Git ✓/✗, ArgoCD ✓/✗).

**Fix:** On mount, call the existing `POST /api/v1/connections/test` endpoint for the active connection. Show ✓/✗ badges next to each connection. Add Provider status (calls `GET /api/v1/providers` to check configured/available).

**Dependency:** The `POST /api/v1/connections/test` endpoint already exists and works — it calls `connSvc.TestConnection()` which tests both Git and ArgoCD. Returns `{git: {status: "ok"/"error"}, argocd: {status: "ok"/"error"}}`. Verified in `internal/api/connections.go` lines 166-182. The `GET /api/v1/providers` endpoint also exists and returns provider type and available providers.

**Files:** `ui/src/views/Connections.tsx`

---

### R8. Provider configuration section in UI

**Gap:** Provider type/region not shown anywhere in the Connections page.

**Fix:** Add a "Secrets Provider" section to Connections.tsx that shows provider type, region, and status (from `GET /api/v1/providers`). Read-only display (provider is configured via Helm values, not UI).

**Files:** `ui/src/views/Connections.tsx`

---

### R9. ArgoCD resource exclusion — document or auto-configure

**Gap:** Neither documented in the starter repo README nor auto-configured during init.

**Fix:** Add a note to `templates/starter/README.md` explaining that ArgoCD should be configured to exclude secrets with `app.kubernetes.io/managed-by: sharko`. Include the ArgoCD `resource.exclusions` config snippet.

**Files:** `templates/starter/README.md`

---

## Low — Polish & UX

### R10. Batch CLI `--from-provider` interactive selection

**Gap:** Registers all discovered clusters without prompting.

**Fix:** Add a `[Y/n]` confirmation prompt before registering. List discovered clusters, ask for confirmation. Interactive selection (pick individual clusters) is post-v1.

**Files:** `cmd/sharko/batch.go`

---

### R11. Auto-bootstrap on startup

**Gap:** `SHARKO_INIT_AUTO_BOOTSTRAP=true` not implemented.

**Fix:** In serve.go, after server setup, check if `autoBootstrap=true` AND provider configured AND connections configured. If so, start a goroutine that runs InitRepo after a short delay (5s). This enables fully automated deployment via Terraform/Helm.

**Files:** `cmd/sharko/serve.go`

---

### R12. Init API — non-blocking sync status

**Gap:** Init endpoint blocks HTTP response for up to 2 minutes waiting for sync. Design says API should return immediately with `sync_status: "pending"`.

**Decision:** Keep blocking for v1.0.0. The synchronous model is simpler and the CLI/UI both benefit from getting the final result. Document this behavior. Revisit if users report timeout issues.

**Status:** DEFER — not fixing, documenting as intentional.

---

## Not a Gap (confirmed)

| Item | Status |
|------|--------|
| commitDirect removed | ✓ Verified — no references in code |
| AddRepository exists | ✓ Verified — with `upsert: true` |
| Default addons | ✓ Working correctly |
| API key format (sharko_ + 32 hex) | ✓ Correct |
| Batch max 10, sequential | ✓ Correct |
| Synchronous API (no job queue) | ✓ Intentional design override from IMPL-PLAN |

---

## Implementation Order

```
R1 + R2 + R3 (Critical — orchestration ordering)
    ↓
R4 (High — addon secret persistence)
    ↓
R5 + R6 (High — template features)
    ↓
R7 + R8 (Medium — UI status indicators)
    ↓
R9 (Medium — documentation)
    ↓
R10 + R11 (Low — CLI/startup polish)
```

R1-R3 are tightly coupled (same file, same patterns). Do them together.
R5-R6 are both template changes. Do them together.
R7-R8 are both Connections.tsx changes. Do them together.
