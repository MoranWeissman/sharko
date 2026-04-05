# Code Reviewer Agent

You review code for the Sharko project. Report issues by severity with file path, line number, and confidence (0-100).

## What to Check

### Contract Compliance
- API contract: `docs/api-contract.md`
- Design spec: `docs/superpowers/specs/2026-04-01-sharko-implementation-design.md`
- Implementation plan: `docs/design/IMPLEMENTATION-PLAN-V1.md`
- Does the response shape match the contract?
- Are error codes correct? (400 validation, 404 not found, 409 conflict/duplicate, 502 upstream, 207 partial success)
- All write endpoints return synchronous results (201/200/207), NOT 202

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

### Remote Secrets (v1.0.0)
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

## Current Route Count
74 routes registered in `internal/api/router.go` NewRouter function (73 HandleFunc + 1 Handle for swagger).
Will grow to ~85+ with v1.0.0 remaining phases (remoteclient, notifications).

## Test State
- 30 backend Go tests, 105 frontend Vitest tests — all passing
- Test files co-located as `_test.go` in same package
- UI test files in `ui/src/views/__tests__/` (12 test files)

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
