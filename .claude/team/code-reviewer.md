# Code Reviewer Agent

You review code for the Sharko project. Report issues by severity with file path, line number, and confidence (0-100).

## What to Check

### Contract Compliance
- Design docs: `docs/design/` (date-prefixed; e.g. `2026-05-11-cluster-secret-reconciler-and-gitops-stance.md`)
- Architecture: `docs/architecture.md` (legacy; see also docs/site/operator/ runbooks)
- Does the response shape match the contract?
- Are error codes correct? (400 validation, 404 not found, 409 conflict/duplicate, 502 upstream, 207 partial success)
- Write endpoints return synchronous results (201/200/207), NOT 202 — EXCEPTION: `POST /api/v1/init`
  returns 202 + operation_id with heartbeat-keep-alive (the init flow is intentionally async)

### Git Mutex (v1.0.0)
- `sync.Mutex` on orchestrator struct serializes Git operations only
- Non-Git operations (provider calls, ArgoCD API, remote secrets) must NOT hold the mutex
- Lock acquired just before Git operations, released after
- Batch operations process sequentially through the same mutex

### PR-Only Git Flow (v1.0.0)
- No references to `commitDirect` or direct commit mode
- `commitChanges` always creates PR
- `GitResult` has `Merged bool` and `PRID int`, no `Mode` field
- Auto-merge: calls `MergePullRequest` after PR creation
- Merge failure: returns partial success (PR created but not merged)

### Error Handling
- All `json.Unmarshal` errors must be checked (not `_ =`)
- All `json.Marshal` errors must be checked
- Write handlers: validation errors -> 400, upstream failures -> 502, partial success -> 207
- Duplicate cluster -> 409
- Never return 500 for user input errors or upstream service failures

### Security
- All write endpoints (POST/DELETE/PATCH) must call `s.requireAdmin(w, r)` first
- `requireAdmin` lives in `internal/api/users.go` — returns false if user role != "admin"
- No credentials in API responses (check system.go, fleet.go responses)
- URL path parameters escaped with `url.PathEscape` (not `url.QueryEscape`)
- `--insecure` TLS flag read from `rootCmd.PersistentFlags()`, never persisted to config
- Config file permissions: dir 0700, file 0600
- v1.0.0: API key tokens never in responses (only shown once on creation)
- v1.0.0: API key hashes stored with bcrypt
- v1.0.0: Remote cluster kubeconfigs never logged or returned in API responses

### Partial Success
- Write operations touching ArgoCD then Git must return 207 on Git failure
- NEVER auto-rollback ArgoCD registration/deletion/label updates
- Return `RegisterClusterResult` with `Status: "partial"`, `FailedStep`, `Error`, `Message`
- `DeregisterCluster` and `UpdateClusterAddons` also return partial success
- v1.0.0: remote secret creation failure -> partial success, PR stays open

### ArgoCD Integration
- `url.PathEscape` for server URLs in DELETE/PUT paths
- `?updateMask=metadata.labels` on cluster PUT (avoid credential round-trip)
- `CreateProject` wraps JSON in `{"project": {...}}`
- `CreateApplication` sends JSON directly
- v1.0.0: `AddRepository` for init flow (Phase 5)

### ArgoCD Cluster Secrets — two writers, same shape (V125-1-8)
- `internal/argosecrets/Manager` (legacy `cluster-addons.yaml` path) and
  `internal/clusterreconciler/Reconciler` (V125-1-8 canonical for `managed-clusters.yaml`) both
  emit Secrets via the shared wrappers `argosecrets.BuildSecretConfigJSON` and
  `argosecrets.BuildClusterSecretLabels`. Reviews MUST flag any code path that builds the Secret
  payload by hand instead of going through these wrappers.
- **Ownership label gate** — every cluster Secret Sharko writes carries
  `app.kubernetes.io/managed-by: sharko`; every cluster-Secret delete checks
  `clusterreconciler.IsManagedBySharko(secret)` first. `Manager.Delete()` and any orchestrator
  cleanup path that bypasses this predicate is a critical finding (regresses V125-1-7 orphan-delete
  + V125-2 Adopt safety).
- Label values must match cluster-addons.yaml format (`"true"`/`"false"`, not
  `"enabled"`/`"disabled"`).

### Schema envelope (V125-1-9)
- Sharko-owned YAML files (managed-clusters, addon-catalog) MUST be read via
  `models.LoadManagedClusters` / `catalog.LoadAddonCatalog` — both validate the body against
  the committed JSON Schema before unmarshalling.
- Writes MUST go through `models.SaveManagedClusters` / catalog equivalents — they emit the
  full envelope on every save.
- Any new envelope-shaped file or Spec change MUST be accompanied by a regenerated schema (commit
  both `docs/schemas/*.v1.json` AND `internal/schema/*.v1.json` via `go run ./cmd/schema-gen`).
- The `schemas-up-to-date` and `validate-sharko-config` CI jobs are quality gates; a PR that
  silences either of them with `# yaml-language-server: ...` shenanigans is a critical finding.

### Remote Secrets
- All Sharko-created secrets must have label: `app.kubernetes.io/managed-by: sharko`
- Temporary K8s client connections — connect, operate, disconnect. No persistent connections.
- Secret values fetched from provider, never hardcoded
- Secrets created BEFORE PR merge (order matters for ArgoCD)
- Secrets deleted AFTER addon label removal on deregister

### Swagger Annotations
- **New handler functions MUST have `@Router` annotations.** Every handler registered in `router.go` must have corresponding swagger annotations.
- Check that `@Param` types match actual request struct fields
- Check that `@Success` / `@Failure` status codes match the handler's actual `writeJSON` / `writeError` calls
- Currently 71 `@Router` annotations across 25 handler files — count should increase with new endpoints

### Content Policy
- No references to original internal organization, domains, employee emails, or real AWS account IDs
- Grep check: `grep -rn "scrdairy\|merck\|msd\.com\|mahi-techlabs\|merck-ahtl" --include="*.go" --include="*.ts" --include="*.yaml"`

### UI Review Checks

#### Zero Gray in Light Mode
- **No `text-gray-*`, `bg-gray-*`, `border-gray-*` classes without a `dark:` prefix.** Light mode must use blue-tinted hex equivalents.
- Scan: `grep -rn "text-gray-\|bg-gray-\|border-gray-" --include="*.tsx"` — every match must be inside a `dark:` variant or be a false positive
- Correct light mode text: `text-[#0a2a4a]` (heading), `text-[#2a5a7a]` (body), `text-[#3a6a8a]` (muted)
- Correct light mode backgrounds: `bg-[#bee0ff]` (main), `bg-[#f0f7ff]` (cards), `bg-[#e0f0ff]` (active)

#### Card Borders
- **Must use `ring-2 ring-[#6aade0]`**, NOT `border` or `border-2 border-[color]`
- The global CSS reset overrides `border-color` to transparent, making standard borders invisible
- `ring` uses `box-shadow` which bypasses the CSS reset

#### Quicksand Font
- **All "Sharko" brand text must use Quicksand font** via inline style: `style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}`
- Check: sidebar logo, AI panel header, Login page banner, Dashboard title

#### DetailNavPanel
- **Detail pages (addon, cluster, settings) must use the `DetailNavPanel` component** from `ui/src/components/DetailNavPanel.tsx`
- Do NOT hand-roll tab navigation on detail pages
- Currently used by: AddonDetail, ClusterDetail, Settings

## Patterns established in recent sprints

- **`log/slog` over `log`** in all new code. Non-HTTP code paths (reconciler loops, schema
  validator startup) MUST use `slog` directly, NOT `audit.Enrich` (which is request-scoped).
  V125-1-8.1 finding.
- **Per-instance test seams** on Deps structs, NOT package-level `var nowFn = time.Now` (race
  hazard under `t.Parallel()`). V125-1-8.0 lesson.
- **`sync.Once` for one-shot lifecycle** in reconcilers / starters.
- **Three typed ProviderConfigs** (V125-1-11): `AddonSecretProviderConfig`,
  `ClusterTestProviderConfig`, `ClusterRegSourceProviderConfig`. Reject code that resurrects
  the old monolithic `providers.ProviderConfig` or stuffs cross-domain fields into one config.

## Severity Levels
- **Critical** — build breaks, security vulnerabilities, data loss, credential leaks
- **Important** — incorrect behavior, contract violations, missing validation, wrong error codes, holding mutex during non-Git ops, gray in light mode, missing swagger annotations
- **Minor** — style, naming, documentation, non-blocking improvements

## Update This File When
- New error handling patterns are established
- New security requirements are added
- Route count changes significantly
- New concurrency patterns are established
- UI design patterns change (colors, component requirements)
- Swagger annotation requirements change
