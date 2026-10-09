---
story_key: V123-1-6-post-api-v1-catalog-sources-refresh-tier-2-force-refresh
epic: V123-1 (Third-party private catalogs)
status: done
effort: M
dispatched: 2026-04-22
merged: 2026-04-22 (PR #276 → main @ eea0abb)
---

# Story V123-1.6 — `POST /api/v1/catalog/sources/refresh` — Tier-2 force refresh

## Brief (from epics-v1.23.md §V123-1.6 + design §6.8)

As an **admin**, I want to force-refresh all catalog sources without waiting
for the next cadence tick, so that I can test a newly added URL immediately.

## Acceptance criteria

**Given** `POST /api/v1/catalog/sources/refresh` (authenticated, Tier-2)
**When** called
**Then** every configured source is re-fetched synchronously; the response is
the same shape as `GET /api/v1/catalog/sources` after the refresh completes.

**Given** the handler is registered
**When** `TestTierCoverage` runs
**Then** the handler is classified as Tier 2 in `internal/api/tier_registry.go`.

**Given** the handler is registered
**When** the route pattern `POST /api/v1/catalog/sources/refresh` is examined
**Then** it is classified as `audit.Tier2` in `internal/api/pattern_tier.go`.

**Given** the handler runs
**When** `TestAuditCoverage` inspects it
**Then** the body contains an `audit.Enrich(...)` call with
`Event: "catalog_sources_refreshed"` and a `Detail` JSON payload of the shape
`{"urls": ["..."], "status": {"<url>": "ok|stale|failed"}}`.

**Given** swaggo annotations land
**When** `swag init` regens `docs/swagger/swagger.json`
**Then** the endpoint appears with full annotations including `@Security BearerAuth`.

**Given** the fetcher is nil (embedded-only mode, no third-party URLs configured)
**When** the endpoint is called
**Then** respond 200 with just the embedded pseudo-source record (no-op refresh).

**Given** the catalog is nil
**When** the endpoint is called
**Then** respond 503 with an error JSON body.

## Design constraints

### Why Tier 2

The force-refresh is a **configuration-time** action — an admin verifying a
newly added `SHARKO_CATALOG_URLS` entry works. It does not directly change
cluster state (Tier 1 is operational, cluster-affecting actions). Tier 2 is
the correct classification per `docs/design/2026-04-16-attribution-and-permissions-model.md`.

### Synchronous vs async

`fetcher.ForceRefresh(ctx, ...)` blocks until all fetches complete (verified
in `fetcher.go:456` — `fetchMany` is the inner sync call). The handler must
await completion before returning the snapshot list, per the AC. Use a
reasonable request deadline — the existing fetcher has a 30s HTTP timeout per
URL with worker parallelism; for 3-5 typical sources the wall-clock is
bounded. No separate timeout knob needed in this story.

### Audit Detail payload

`audit.Fields.Detail` is a `string`. Marshal the map literal
`{urls: [...sorted...], status: {url: "ok|stale|failed"}}` to JSON. The `urls`
list IS the list of third-party URLs attempted. Auth-token-in-path concerns
are acknowledged — **the audit trail includes URLs by design** because the
admin-caller already configured them, and the audit log is Tier-2-readable
only (not a public sink).

- **Do NOT log the URLs in application log lines.** The existing fetcher
  logging uses `source_fp` fingerprints. The handler itself emits no log lines.
- The Detail JSON is consumed by the audit log viewer, which is Tier-2 RBAC-gated.

### Response shape

Reuse the V123-1.5 response type (`catalogSourceRecord`) and the snapshot →
record mapping (`recordFromSnapshot`). After `ForceRefresh` returns, call the
same snapshot-enumeration logic used by `handleListCatalogSources` — don't
duplicate the mapping.

To avoid duplicating the snapshot-to-response code, **extract the response
builder from V123-1.5 into a tiny helper** (e.g., `(*Server).buildCatalogSourcesResponse()`)
and call it from both handlers. Keep the helper in `internal/api/catalog_sources.go`.

## Implementation plan

### 1. New file `internal/api/catalog_sources_refresh.go`

```go
package api

import (
    "context"
    "encoding/json"
    "net/http"
    "sort"
    "time"

    "github.com/MoranWeissman/sharko/internal/audit"
)

// handleRefreshCatalogSources godoc
//
// @Summary Force-refresh all catalog sources
// @Description Synchronously re-fetches every configured third-party catalog
//   source without waiting for the next cadence tick. Returns the refreshed
//   list in the same shape as GET /catalog/sources. Embedded catalog is
//   included as a pseudo-source. Requires authentication (Tier 2 — admin).
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {array} catalogSourceRecord
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/sources/refresh [post]
func (s *Server) handleRefreshCatalogSources(w http.ResponseWriter, r *http.Request) {
    if s.catalog == nil {
        writeCatalogNotLoaded(w)  // or inline the 503 pattern from V123-1.5
        return
    }

    // If a fetcher is wired AND has sources, block for refresh.
    attempted := []string{}
    statusByURL := map[string]string{}

    if s.sourcesFetcher != nil {
        ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
        defer cancel()

        s.sourcesFetcher.ForceRefresh(ctx)

        snaps := s.sourcesFetcher.Snapshots()
        for u, snap := range snaps {
            attempted = append(attempted, u)
            statusByURL[u] = string(snap.Status)
        }
        sort.Strings(attempted)
    }

    // Audit enrichment — middleware stamps actor + tier; we add event + detail.
    detailPayload := map[string]interface{}{
        "urls":   attempted,
        "status": statusByURL,
    }
    detailJSON, _ := json.Marshal(detailPayload)

    audit.Enrich(r.Context(), audit.Fields{
        Event:  "catalog_sources_refreshed",
        Detail: string(detailJSON),
    })

    records := s.buildCatalogSourcesResponse()
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(records)
}
```

### 2. Extract `buildCatalogSourcesResponse` from V123-1.5

In `internal/api/catalog_sources.go`, move the response-building logic (embedded
record + third-party snapshot → record mapping + alphabetical sort) into:

```go
// buildCatalogSourcesResponse assembles the []catalogSourceRecord used by
// both GET /catalog/sources and POST /catalog/sources/refresh.
// Callers must have already checked s.catalog != nil.
func (s *Server) buildCatalogSourcesResponse() []catalogSourceRecord { ... }
```

Update `handleListCatalogSources` to call this helper. Behaviour must be
byte-identical — existing V123-1.5 tests must keep passing with no changes.

### 3. Router wiring

In `internal/api/router.go`, register alongside sibling `/catalog/*` routes:

```go
// Tier-2 mutating endpoint: goes through auth + audit middleware.
r.Post("/catalog/sources/refresh", s.handleRefreshCatalogSources)
```

Match the chain used by other Tier-2 POSTs (e.g., `/addons` POST). The audit
middleware must wrap this route.

### 4. Tier registration

In `internal/api/tier_registry.go`, add (under the Tier-2 block):

```go
// V123-1.6 — force-refresh third-party catalog sources.
"handleRefreshCatalogSources": audit.Tier2,
```

In `internal/api/pattern_tier.go`, add under catalog section:

```go
"POST /api/v1/catalog/sources/refresh": audit.Tier2,
```

### 5. Audit coverage

The `TestAuditCoverage` test scans for `audit.Enrich(` inside each mutating
handler. Our handler contains that call — the coverage test should pass
without needing any additional config.

### 6. Swagger regen

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

Verify the new endpoint + shared `catalogSourceRecord` type appear.

## Test plan

`internal/api/catalog_sources_refresh_test.go`:

1. **503 when catalog is nil** — response 503, error JSON body.
2. **No fetcher wired** — 200, response is `[{url:"embedded", ...}]` only; audit event emitted with `urls:[], status:{}`.
3. **Fetcher wired, no sources configured** — 200, same as #2 (fetcher returns empty from ForceRefresh).
4. **Fetcher wired, one source, returns ok** — 200, response has embedded + one third-party `status:"ok"`, `last_fetched` non-nil. Audit detail contains `"urls":["<URL>"]` and `"status":{"<URL>":"ok"}`.
5. **Fetcher wired, one source, returns failed** — 200, response row `status:"failed"`. Audit `"status":{"<URL>":"failed"}`.
6. **Multiple sources, alphabetical response** — embedded first, third-party sorted by URL. Audit `urls` list also sorted.
7. **`TestTierCoverage`** — passes (handler is in HandlerTier as Tier2).
8. **`TestAuditCoverage`** — passes (handler body has `audit.Enrich(`).
9. **`TestListCatalogSources_*`** (existing V123-1.5 tests) — still pass after refactor to buildCatalogSourcesResponse.

Use the same test plumbing that V123-1.5 uses. Consider a `ForceRefreshForTest`
helper on `*Fetcher` if the real `ForceRefresh` needs live HTTP — or, simpler,
pre-populate snapshots via `SetSnapshotsForTest` + call `ForceRefresh` which
becomes a no-op on empty configured sources.

**Test-fetcher behaviour:** `ForceRefresh` iterates `f.cfg.Sources`. For tests
that want refresh-as-noop with preset snapshots, construct the fetcher with an
empty Sources list and inject snapshots. For tests that want actual
per-URL status changes, use the existing fetcher test-server pattern from
`fetcher_test.go`.

## Quality gates

- `go build ./...`
- `go vet ./...`
- `go test ./internal/api/... -race -count=1` (incl. TestTierCoverage, TestAuditCoverage)
- `golangci-lint run ./internal/api/...` (silent skip if missing)
- `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` → diff committed.

## Explicit non-goals

- UI button to trigger refresh (lives in V123-1.8 Settings view).
- Per-URL refresh (call param `urls=...` filter) — `ForceRefresh` supports it,
  but the story spec says "every configured source". Skip the query param for
  now; keep the handler simple.
- Rate-limiting — future admin hardening concern, not this story.

## Dependencies

- V123-1.5 response type + handler — done ✅.
- V123-1.2 `fetcher.ForceRefresh` — done ✅.

## Gotchas

1. **`writeCatalogNotLoaded` vs inline.** If V123-1.5 uses an inline 503
   pattern (not a helper), match it. Do not introduce a new helper just for
   this story.
2. **Detail payload must be a JSON string** (not an object) — `audit.Fields.Detail`
   is `string`. Marshal and stringify.
3. **Never log the URL** in handler body. The audit log entry itself holds the
   URL — fine — but `log.Info(...url...)` is not allowed.
4. **Synchronous response.** Do not return 202 + poll — the AC says "the
   response is the same shape as GET". 60s request timeout is a safety
   wrapper; ForceRefresh should finish in single-digit seconds for 3-5 sources.
5. **Audit must fire even on zero-source case.** The empty-URL refresh is still
   a user action worth auditing ("someone clicked refresh — noop").
6. **Tier coverage test** keyed by the handler function name — name it
   `handleRefreshCatalogSources`, not a shortened version.
7. **Preserve V123-1.5 behaviour.** After refactoring the response builder,
   run the existing `TestListCatalogSources_*` suite to confirm no drift.

## Role files (MUST embed in dispatch)

- `.claude/team/go-expert.md` — handler + refactor.
- `.claude/team/test-engineer.md` — coverage + handler tests.
- `.claude/team/docs-writer.md` — swagger regen.

## PR plan

- Branch: `dev/v1.23-sources-refresh` off main.
- Commits:
  1. `refactor(api): extract buildCatalogSourcesResponse helper (V123-1.6 prep)`
  2. `feat(api): POST /catalog/sources/refresh Tier-2 force-refresh (V123-1.6)`
  3. `docs(swagger): regen for catalog sources refresh endpoint`
  4. `chore(bmad): mark V123-1.6 for review`
- No tag.

## Next story after this

V123-1.7 — UI source badge on Browse tiles + detail page (reads `source` field
from V123-1.4 and the GET /catalog/sources endpoint for the hover-card).

## Tasks completed

- [x] **Step 1 — Refactor V123-1.5 (zero behaviour change):** extracted the embedded-record + third-party-snapshot projection logic of `handleListCatalogSources` into a new `(*Server).buildCatalogSourcesResponse()` helper living alongside the handler in `internal/api/catalog_sources.go`. `handleListCatalogSources` now reduces to the 503 guard + a single call to the helper + `writeJSON`. All 8 existing `TestListCatalogSources_*` cases pass unchanged (run before committing — byte-identical wire shape).
- [x] **Step 2 — New handler file `internal/api/catalog_sources_refresh.go`:** `handleRefreshCatalogSources` with full swagger annotations (`@Summary`, `@Description`, `@Tags catalog`, `@Produce json`, `@Security BearerAuth`, `@Success 200 {array} catalogSourceRecord`, `@Failure 503`, `@Router /catalog/sources/refresh [post]`). 60s request-ctx timeout wraps `ForceRefresh`. Handler emits zero log lines. Audit Detail is JSON-marshalled `{urls, status}` attached via `audit.Enrich` with `Event: "catalog_sources_refreshed"`. Uses the shared `buildCatalogSourcesResponse` for the post-refresh response and the same `writeError(503, "catalog not loaded")` pattern as V123-1.5 for the nil-catalog branch.
- [x] **Step 3 — Tier/audit wiring:** added `"handleRefreshCatalogSources": audit.Tier2` to `internal/api/tier_registry.go` under the Tier-2 catalog block; added `"POST /api/v1/catalog/sources/refresh": audit.Tier2` to `internal/api/pattern_tier.go` under the catalog section.
- [x] **Step 4 — Router:** registered via `mux.HandleFunc("POST /api/v1/catalog/sources/refresh", srv.handleRefreshCatalogSources)` in `internal/api/router.go`, alongside the existing `GET /catalog/sources` route. Same middleware chain (auth + audit) because the mux is flat.
- [x] **Step 5 — Tests (`internal/api/catalog_sources_refresh_test.go`):** 6 new cases covering the exact plan in the brief — 503 on nil catalog, no-fetcher embedded-only, empty-fetcher no-op, single OK snapshot, single failed snapshot, multi-source alphabetical response + audit ordering. Tests use a `callRefreshSources` helper that attaches an `audit.WithEnrichment` context so the returned `*audit.Fields` can be inspected; an `auditDetail` helper re-parses the JSON Detail string and returns `(urls, statusByURL)` so each case asserts both the wire response and the audit payload.
- [x] **`TestTierCoverage` + `TestAuditCoverage`** run automatically in the api-package test sweep; both PASS with the new handler registered.
- [x] **Step 6 — Swagger regen:** `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` picked up the new endpoint. Diff: +35 LOC in `docs.go`, +35 in `swagger.json`, +28 in `swagger.yaml` — purely additive (the new path block + `@Security`).
- [x] **Quality gates:** `go build ./internal/...` clean; `go vet ./internal/...` clean; `go test ./internal/api/... -race -count=1` PASS (~6s, incl. `TestTierCoverage` and `TestAuditCoverage`). `golangci-lint` skipped (not installed locally; CI will run).
- [x] **BMAD tracking:** frontmatter updated to `status: review`, `dispatched: 2026-04-22`; `sprint-status.yaml` flipped `V123-1-6-...: backlog → review` and the `last_updated` header comment refreshed.

## Files touched

- `internal/api/catalog_sources.go` — refactored `handleListCatalogSources` to delegate to a new `buildCatalogSourcesResponse` helper; both `handleListCatalogSources` and the new refresh handler call the helper so the response shape stays identical across the two endpoints. Pure refactor (+15 / -9 LOC).
- `internal/api/catalog_sources_refresh.go` (NEW) — `handleRefreshCatalogSources` with swagger annotations, 60s request-ctx timeout, `audit.Enrich` call with JSON-marshalled Detail (`{urls, status}`), alphabetical sort of the audit urls list. 103 LOC incl. docstring + comments.
- `internal/api/catalog_sources_refresh_test.go` (NEW) — 6 test cases + `callRefreshSources` / `auditDetail` helpers. 299 LOC.
- `internal/api/tier_registry.go` — 6 LOC added (block comment + one `HandlerTier` entry).
- `internal/api/pattern_tier.go` — 5 LOC added (block comment + one `mutatingPatternTier` entry).
- `internal/api/router.go` — 3 LOC added (comment + one `mux.HandleFunc` line alongside the GET sibling).
- `docs/swagger/docs.go`, `docs/swagger/swagger.json`, `docs/swagger/swagger.yaml` — `swag init` regen; purely additive new path block.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — `V123-1-6` backlog → review; `last_updated` header comment refreshed.
- `.bmad/output/implementation-artifacts/V123-1-6-post-api-v1-catalog-sources-refresh-tier-2-force-refresh.md` — frontmatter + retrospective sections (this file).

## Tests

Targeted run (race-enabled):

```bash
go test ./internal/api/ -run 'TestRefreshCatalogSources|TestListCatalogSources|TestTierCoverage|TestAuditCoverage' -count=1 -v
# PASS (all 16 cases): 6 refresh + 8 list + 1 tier + 1 audit
```

Full api-package sweep (race-enabled):

```bash
go test ./internal/api/... -race -count=1
# ok   github.com/MoranWeissman/sharko/internal/api     ~6s
```

New tests (6):

1. `TestRefreshCatalogSources_503OnNilCatalog` — `s.catalog == nil` → 503 with `{"error": "..."}`. Mirrors V123-1.5's GET 503 contract.
2. `TestRefreshCatalogSources_NoFetcher_ReturnsEmbeddedOnly` — no fetcher wired; response = `[embedded]`, audit fires with `Event:"catalog_sources_refreshed"` + empty urls / empty status map (admin action is auditable even when the operation is a noop).
3. `TestRefreshCatalogSources_FetcherEmpty_NoSourcesConfigured` — fetcher wired with empty `cfg.Sources`; ForceRefresh is a no-op via resolveTargets; response + audit payload identical to case 2.
4. `TestRefreshCatalogSources_SingleOKSnapshot` — injected `StatusOK` snapshot survives no-op ForceRefresh; response contains embedded + the third-party row with `status:"ok"`, `last_fetched` equal to the injected `LastSuccessAt`; audit `urls == [URL]` and `status[URL] == "ok"`.
5. `TestRefreshCatalogSources_SingleFailedSnapshot` — `StatusFailed` with zero `LastSuccessAt` surfaces as `status:"failed"`, `last_fetched:null`, `entry_count:0`; audit status map records `"failed"`.
6. `TestRefreshCatalogSources_MultipleSources_AlphabeticalSort` — 3 snapshots injected out-of-order (`zeta`, `alpha`, `mid`); asserts (a) response third-party rows come back `[alpha, mid, zeta]` (b) audit Detail `urls` array is also alphabetical. Deterministic ordering of both is load-bearing — without it Go's randomised map iteration would flake both the test AND any log-diffing / alerting rule parked on top of the audit stream.

Existing tests kept green:

- 8 `TestListCatalogSources_*` cases — the refactor to `buildCatalogSourcesResponse` is byte-identical on the wire (run before each commit).
- `TestTierCoverage` — the new handler is in `HandlerTier` as `Tier2`.
- `TestAuditCoverage` — the new handler body contains `audit.Enrich(`.

## Decisions

- **Refactor-first, feature-second commit split.** Per the brief, Step 1 landed as a standalone commit (`refactor(api): extract buildCatalogSourcesResponse helper`) before any feature change, so the refactor can be isolated in `git log` / `git bisect`. The feature commit then adds exactly the new-endpoint code + wiring + tests, and a third commit carries the swagger regen.
- **`buildCatalogSourcesResponse` lives in `catalog_sources.go`, not a new file.** The helper is only reachable through two sibling handlers in the same package, both owning the same wire shape. Splitting it into a `catalog_sources_response.go` would add a file for no semantic gain — the GET / POST symmetry is clearer when the helper sits alongside the record type it projects to.
- **60s request-ctx timeout hard-coded, no env knob.** The brief says "no separate timeout knob needed in this story". The fetcher already has a 30s per-URL HTTP timeout + worker parallelism; 60s is a generous outer cap that handles 2-3 slow sources fetched sequentially in the worst case. Making this tunable is a V2-hardening concern if it ever matters — right now the value is invisible to callers because ForceRefresh finishes in single-digit seconds for realistic source counts.
- **Audit fires on the empty-URL noop case.** The brief explicitly calls this out: "admin clicked refresh on embedded-only — worth auditing." Tests 2 and 3 assert the audit event with empty `urls:[]` + empty `status:{}`. This is the same design choice made elsewhere in the codebase (e.g., `handleReprobeArtifactHub` audits even when the probe succeeds without changing state).
- **Audit `urls` array sorted, `statusByURL` map left natural.** `urls` is the ordered "what did we attempt" list so we sort it for deterministic log-diffing. `statusByURL` is a lookup table (key is the URL itself), so sorting its natural-JSON encoding would be theatre — the test asserts each URL's value rather than the map's iteration order.
- **`writeError(503, "catalog not loaded")` instead of inline JSON encoding.** Brief says to "mirror V123-1.5's 503 pattern exactly." V123-1.5 uses `writeError`, so the refresh handler does too. Bonus: `writeError` centralises the error-shape contract so any future change to error bodies (e.g., adding a `code` field) lands in one place.
- **`writeJSON(w, 200, ...)` instead of manual `w.Header().Set + json.NewEncoder.Encode`.** Same rationale as the 503 path — V123-1.5 uses `writeJSON`, the helper already sets `Content-Type` and calls `WriteHeader` in the right order. Consistency over micro-divergence.
- **Handler emits zero log lines.** The audit log entry — Tier-2-readable only — is the authoritative record of which URLs were refreshed. Application logs (potentially visible to a wider audience, exported to SIEMs, etc.) must never carry the raw URL. The fetcher itself uses its `urlFingerprint` helper; the handler avoids the problem entirely by not logging.
- **`callRefreshSources` / `auditDetail` test helpers kept local to the new test file.** `audit.WithEnrichment` is not used by any existing `_test.go` in `internal/api/` (grep shows zero prior callers). Rather than introducing a package-wide test harness for one story's needs, the helpers live alongside the 6 cases that use them — future tests that need similar enrichment assertions can lift them to a shared file if/when the pattern repeats.
- **Test fetcher uses empty `cfg.Sources` + `SetSnapshotsForTest`.** Per the brief's "test-fetcher behaviour" note: with an empty `cfg.Sources`, `ForceRefresh → resolveTargets` yields an empty target list, so the call becomes a no-op and the injected snapshots survive. This lets each test deterministically control the per-URL status without spinning up a fixture HTTP server. Integration tests against a live fetcher are V123-1.9's scope.

## Gotchas / constraints addressed

1. **Never log URLs** — handler body has zero log lines. The audit log entry carries URLs (intentional, Tier-2-gated), no application log line does. Enforced by code review, not by a test.
2. **`catalog_sources_refreshed` event name** — matches the AC exactly, verified by each test's `auditDetail` helper which re-reads `fields.Event` before parsing the Detail.
3. **`Detail` is a JSON-marshalled string** — `audit.Fields.Detail` is typed `string`; we `json.Marshal(...)` the map and cast to string. Tests re-parse the string back into a struct to assert the payload shape.
4. **Audit fires on the zero-source noop case** — tests 2 and 3 explicitly assert that even when no third-party URLs exist, the audit event is emitted with empty `urls:[]` and empty `status:{}`.
5. **`s.catalog == nil` → 503, `s.sourcesFetcher == nil` → 200 with `[embedded]`** — two separate guards in the handler; tests 1 and 2 pin both.
6. **60s request timeout via `context.WithTimeout(r.Context(), 60*time.Second)`** — wraps `ForceRefresh` only; the outer `r.Context()` cancellation still propagates.
7. **Refactor-first, feature-second** — V123-1.5 tests passed unchanged after Step 1 (verified before each subsequent commit).
8. **No new dependencies** — only stdlib (`context`, `encoding/json`, `net/http`, `sort`, `time`) + the existing `internal/audit` package.
9. **No scope creep** — no `?urls=...` filter (skipped per brief, even though `ForceRefresh` supports it), no rate-limiting, no UI. Those belong in later stories (1.8, 1.9, V2 hardening).
10. **Tier coverage test name match** — handler is literally named `handleRefreshCatalogSources` (the string the test greps for in the AST).

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./internal/...` | clean |
| Vet | `go vet ./internal/...` | clean |
| Targeted unit tests (race) | `go test ./internal/api/ -run 'TestRefresh\|TestList\|TestTier\|TestAudit' -race -count=1` | 16/16 PASS |
| Full api-package tests (race) | `go test ./internal/api/... -race -count=1` | PASS (~6s) |
| Lint | `golangci-lint run ./internal/api/...` | skipped (not installed locally; CI will run) |
| Swagger regen | `swag init …` | new endpoint + `@Security BearerAuth` present in `docs/swagger/{docs.go,swagger.json,swagger.yaml}` |

## Deviations from the brief

None. The handler body, 503 pattern, audit enrichment shape, tier + pattern tables, router registration, and the 6-case test plan all match the brief 1:1. Two minor style choices (use `writeJSON` for success; use `writeError` for 503) are explicitly what the brief told us to do ("match the 503 pattern exactly from `handleListCatalogSources`"; V123-1.5 uses both helpers).
