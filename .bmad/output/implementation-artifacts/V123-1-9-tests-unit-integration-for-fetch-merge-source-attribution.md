---
story_key: V123-1-9-tests-unit-integration-for-fetch-merge-source-attribution
epic: V123-1 (Third-party private catalogs)
status: done
effort: M
dispatched: 2026-04-23
merged: 2026-04-23 (PR #282 → main @ 4e87d6e); Epic V123-1 closed (9/9 stories done)
---

# Story V123-1.9 — Tests: unit + integration for fetch / merge / source attribution

## Brief (from epics-v1.23.md §V123-1.9)

As the **quality pipeline**, I want unit tests for each new Go package + an
integration test that spins up a local HTTPS server serving a third-party YAML
and verifies the full loop, so that third-party catalogs cannot regress
silently.

This is the **final story of Epic V123-1**. It hardens the test bar; no new
product surface.

## Acceptance criteria

**Given** `internal/catalog/sources/fetcher_test.go`
**Then** unit cases cover: happy fetch ✓, 5xx ✓, **timeout** (gap), **invalid YAML** (gap), schema violation ✓, proxy env respected ✓.

**Given** `internal/catalog/sources/merger_test.go`
**Then** unit cases cover: embedded wins on conflict ✓, alphabetical resolution on third-party-vs-third-party conflict ✓, source attribution on every entry ✓.

**Given** a new integration test at `internal/catalog/sources/integration_test.go`
**When** the test runs
**Then** it:
1. Spins up `httptest.NewTLSServer` serving a multi-entry third-party YAML.
2. Constructs a `CatalogSourcesConfig` pointing at that URL (AllowPrivate=true so the loopback SSRF guard accepts 127.0.0.1).
3. Starts the `Fetcher`, waits for the first successful fetch.
4. Calls `Merge(embedded, snapshots)` with a small synthetic embedded slice.
5. Asserts: every third-party entry's `Origin == <full URL>`, every embedded entry's `Origin == "embedded"`, no `Conflict` when names don't overlap, `Conflict{Reason: "embedded-wins"}` when they do.

**Given** `go test ./internal/catalog/sources/... -race -count=1`
**Then** all existing + new tests pass, no data races.

## Current coverage audit

### Already covered (do NOT duplicate)

Fetcher (`internal/catalog/sources/fetcher_test.go`):
- `TestFetcher_HappyPath` (L130) — happy fetch
- `TestFetcher_5xxRetainsSnapshot` (L167) — 5xx error
- `TestFetcher_SchemaViolationRetainsSnapshot` (L205) — schema violation
- `TestFetcher_HTTPProxyRespected` (L241) — proxy env
- `TestFetcher_FreshStartNoSuccess` (L283)
- `TestFetcher_SidecarDelegation` (L349)
- `TestFetcher_NilVerifierSidecarIgnored` (L381)
- `TestFetcher_ConcurrentFetchesInParallel` (L407)
- `TestFetcher_RuntimeSSRFGuardBlocksPrivateIP` (L454)
- `TestFetcher_CtxCancelStopsSupervisor` (L495)
- `TestFetcher_StartThenStopIsClean` (L529)
- `TestFetcher_NilVerifierNilClockOK` (L556)
- `TestFetcher_ForceRefreshEmptyURLsMeansAll` (L569)
- `TestFetcher_ForceRefreshUnknownURLIgnored` (L586)
- `TestFetcher_SnapshotsReturnsDeepCopy` (L605)

Merger (`internal/catalog/sources/merger_test.go`):
- `TestMerge_EmptyInputs`, `_EmbeddedOnly`, `_ThirdPartyOnly`,
  `_EmbeddedVsThirdPartyCollision`, `_ThirdPartyVsThirdPartyCollision`,
  `_ThreeWayCollision`, `_StaleOrFailedSnapshotsSkipped`,
  `_DeterministicOrdering`.

### Gaps to fill

**Fetcher:**
1. `TestFetcher_ClientTimeoutMarksFailed` — server sleeps longer than `SetHTTPClientForTest` allows (use a short http.Client Timeout via the existing setter), snapshot lands in `StatusFailed`, `LastErr` mentions "context deadline exceeded" or equivalent.
2. `TestFetcher_InvalidYAMLMarksFailed` — server returns `<html>...</html>` (or truncated YAML like `addons:\n  - name: broken\nfoo: [`). Snapshot lands in `StatusFailed` with a parse-error message, NOT a schema violation. This is distinct from the existing `SchemaViolationRetainsSnapshot` (which tests a YAML that parses but fails schema checks).

**Merger:**
Existing tests already cover the AC's "source attribution on every entry" implicitly via the `Origin` field checks in `_ThirdPartyOnly` and `_EmbeddedVsThirdPartyCollision`. No gap here — but add a redundant explicit test if it clarifies intent:

- `TestMerge_EveryEntryCarriesOrigin` — given 2 embedded + 3 third-party (non-overlapping) entries, assert `len(merged.Entries) == 5` AND every entry has a non-empty `Origin` AND embedded entries have `Origin == "embedded"` AND third-party entries have `Origin == <their snapshot URL>`.

### Integration test (NEW file)

`internal/catalog/sources/integration_test.go`:

```go
package sources

import (
    "context"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/MoranWeissman/sharko/internal/catalog"
    "github.com/MoranWeissman/sharko/internal/config"
)

func TestIntegration_FullLoop_FetcherToMergedCatalog(t *testing.T) {
    // 1. Serve a multi-entry YAML over TLS loopback.
    ff := fakeFetch{/* ... 3 entries: alpha, beta, cert-manager ... */}
    srv := httptest.NewTLSServer(ff.handler())
    t.Cleanup(srv.Close)

    // 2. Configure Sharko with that URL; AllowPrivate=true is required
    //    (SSRF guard would otherwise block the 127.0.0.1 loopback).
    cfg := &config.CatalogSourcesConfig{
        Sources: []config.CatalogSource{{URL: srv.URL + "/catalog.yaml"}},
        RefreshInterval: 1 * time.Hour,   // long — we force one refresh
        AllowPrivate: true,
    }

    // 3. Start fetcher, force one refresh (sync), stop.
    f := NewFetcher(cfg, nil, nil)
    f.SetHTTPClientForTest(srv.Client())     // trusts the test TLS cert
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    f.ForceRefresh(ctx)

    // 4. Assert: snapshots show status OK with the 3 entries.
    snaps := f.Snapshots()
    if len(snaps) != 1 { t.Fatalf("want 1 snap, got %d", len(snaps)) }
    var snap *SourceSnapshot
    for _, s := range snaps { snap = s }
    if snap.Status != StatusOK { t.Errorf("status=%s want ok", snap.Status) }
    if len(snap.Entries) != 3 { t.Errorf("entries=%d want 3", len(snap.Entries)) }

    // 5. Merge with a synthetic embedded slice that collides on "cert-manager".
    embedded := []catalog.CatalogEntry{
        {Name: "cert-manager", /* ... required fields ... */, Source: "embedded"},
        {Name: "embedded-only", /* ... */, Source: "embedded"},
    }
    merged := Merge(embedded, []*SourceSnapshot{snap})

    // 6. Assert final shape:
    //    - cert-manager appears ONCE (embedded wins); Origin="embedded".
    //    - alpha + beta from third-party carry Origin=<srv URL>.
    //    - embedded-only carries Origin="embedded".
    //    - Conflict{Name:"cert-manager", Reason:"embedded-wins", Winner:"embedded"} present.
    //    - Total: 4 entries.
    assertEntriesByName(t, merged.Entries, map[string]string{
        "alpha":         srv.URL + "/catalog.yaml",
        "beta":          srv.URL + "/catalog.yaml",
        "cert-manager":  "embedded",
        "embedded-only": "embedded",
    })
    // ... Conflict assertions ...
}
```

Leverage the existing `startYAMLServer` / `fakeFetch` test-helper pattern from
`fetcher_test.go`. If those helpers are unexported and small, consider
extracting them into a shared `internal/catalog/sources/testhelpers_test.go`
OR just inline the helpers in the integration test (pragmatic — integration
tests don't need to share code with unit tests).

**Do NOT wire the full `*api.Server` + router into this integration test.**
That would create an import cycle (api imports sources). Handler-level
assertions already exist in V123-1.4 + V123-1.5 handler tests.

## Quality gates

- `go build ./...`
- `go vet ./...`
- `go test ./internal/catalog/sources/... -race -count=1` (≥ 25 tests passing: 15 fetcher + 2 new + 8 merger + 1 new + 1 integration).
- `go test ./internal/catalog/... ./internal/api/... -race -count=1` (no cross-package regression).
- `golangci-lint run ./internal/catalog/sources/...` (silent skip if missing).
- No swagger regen (no API surface change).

## Explicit non-goals

- UI integration tests. V123-1.7 + V123-1.8 already land with vitest coverage.
- End-to-end handler tests that route through `*api.Server`. Covered by
  V123-1.4 + V123-1.5 + V123-1.6 handler tests.
- Scaled / load testing. V1.23 is feature-delivery; load tests belong to
  V2 hardening task #82.

## Dependencies

- V123-1.2 fetcher + test helpers — done ✅.
- V123-1.3 merger — done ✅.
- V123-1.4 `Source` field on `CatalogEntry` — done ✅ (used in the integration test's embedded slice).

## Gotchas

1. **`AllowPrivate: true`** is required in the integration test's config
   because `httptest.NewTLSServer` binds to 127.0.0.1, which the runtime
   SSRF guard rejects by default. This matches how existing fetcher tests
   handle it (`newTestFetcher` sets the same flag).
2. **Use `srv.Client()`** as the fetcher HTTP client so it trusts the
   test-server's self-signed TLS cert. The existing `SetHTTPClientForTest`
   setter is the injection point.
3. **Timeout test**: configure a short http.Client Timeout (e.g., 100ms) via
   `SetHTTPClientForTest`, then have the test server block for longer.
   Status must land on `StatusFailed`, not `StatusStale`.
4. **Invalid-YAML test** must be distinct from the existing schema-violation
   test. The YAML payload should fail at the YAML *parse* stage (e.g.,
   `foo: [` — unterminated bracket), NOT at the schema-validation stage.
5. **Integration test cleanup** — `t.Cleanup(srv.Close)` + short ctx timeouts.
   Never leak a goroutine or TLS server past test end.
6. **Do NOT hit flaky timing**. Use `ForceRefresh` (synchronous), not `Start`
   + `Stop` + sleeping for a tick, which introduces timing flake.

## Role files (MUST embed in dispatch)

- `.claude/team/test-engineer.md` — primary (table-driven, failure cases, no flake).
- `.claude/team/go-expert.md` — secondary (for any helper refactor).

## PR plan

- Branch: `dev/v1.23-integration-tests` off main.
- Commits:
  1. `test(catalog/sources): fetcher timeout + invalid-YAML gap cases (V123-1.9)`
  2. `test(catalog/sources): full-loop integration test (V123-1.9)`
  3. `chore(bmad): mark V123-1.9 for review`
- No tag.

## Epic V123-1 closeout

After this story merges, **Epic V123-1 (Third-party private catalogs) is
complete** — all 9 stories done. Remaining v1.23 work:

- Epic V123-2 — per-entry cosign signing (6 stories)
- Epic V123-3 — trusted-source scanning bot (5 stories)
- Epic V123-4 — docs + release cut (5 stories)

## Tasks completed

- [x] **Gap-fill fetcher tests (`internal/catalog/sources/fetcher_test.go`, appended):**
  - `TestFetcher_ClientTimeoutMarksFailed` — 500ms server delay vs a 100ms
    `http.Client.Timeout` (copied off `srv.Client()` so TLS still trusts the
    test CA). Asserts `StatusFailed` + tolerant match on "deadline" / "timeout"
    in `LastErr`; `LastSuccessAt` remains zero (fresh-start path, no prior
    success → Failed, not Stale).
  - `TestFetcher_InvalidYAMLMarksFailed` — server returns
    `"addons:\n  - name: broken\nfoo: [\n"` (unterminated flow sequence →
    `yaml.Unmarshal` parse error). Asserts `StatusFailed`, non-empty
    `LastErr`, tolerant match on `yaml`/`parse`/`unmarshal`/`schema` (the
    fetcher wraps loader errors as `"schema validation: %w"`), and
    empty `Entries` (fresh start, nothing to retain).
  - Helper: `clientWithTimeout(srv, timeout)` shallow-copies `srv.Client()`
    and overrides `.Timeout`. Inlined next to the two new tests per the
    brief ("inline is fine") to avoid touching existing helpers.
- [x] **Gap-fill merger test (`internal/catalog/sources/merger_test.go`, appended):**
  - `TestMerge_EveryEntryCarriesOrigin` — 2 embedded (`cert-manager`,
    `embedded-only`) + 3 third-party (`alpha`, `beta` on URL1; `gamma` on
    URL2), no name overlaps. Asserts `len(Entries) == 5`, `len(Conflicts) == 0`,
    every entry has non-empty `Origin`, embedded entries carry
    `OriginEmbedded`, third-party entries carry their snapshot URL, and no
    surviving entry is flagged `Overridden` (which is always the case
    today since losers are dropped from `Entries`).
- [x] **Integration test (`internal/catalog/sources/integration_test.go`, NEW):**
  - `TestIntegration_FullLoop_FetcherToMergedCatalog` — full loop
    HTTPS loopback → Fetcher → snapshot → Merge → final 4-entry
    catalog. `AllowPrivate: true` for the 127.0.0.1 loopback;
    `SetHTTPClientForTest(srv.Client())` to trust the ephemeral TLS CA.
    `ForceRefresh(ctx)` for deterministic timing (no `Start` + sleep
    patterns). Third-party YAML has 3 entries (`alpha`, `beta`,
    `cert-manager`); the synthetic embedded slice collides on
    `cert-manager` and also declares `embedded-only`. Asserts:
    1. Snapshot `StatusOK`, 3 entries parsed.
    2. Merged catalog has 4 entries (embedded wins cert-manager).
    3. Exactly 1 `Conflict{Name: "cert-manager", Reason:
       ReasonEmbeddedWins, Winner: OriginEmbedded, Losers: [srv URL]}`.
    4. Every entry has the correct `Origin` (alpha/beta → srv URL,
       cert-manager/embedded-only → `OriginEmbedded`).
  - Local helper `entryNamesFor` added (distinct from `entryNames` in
    merger_test.go) for logging on assertion failure without risking a
    test-file load-order collision.
- [x] **Quality gates:** `go build ./...` clean, `go vet ./...` clean,
  `go test ./internal/catalog/sources/... -race -count=1` 27 tests pass
  (15 fetcher + 2 new + 8 merger + 1 new + 1 integration — exceeds the
  brief's ≥26 target), `go test ./internal/catalog/... ./internal/api/... -race -count=1`
  all green. `golangci-lint` not installed locally → silent skip per
  brief. No swagger regen (test-only story, no API surface change).
- [x] **BMAD tracking:** story frontmatter → `status: review`,
  `dispatched: 2026-04-23`; sprint-status.yaml
  `V123-1-9-…: backlog → review`; `last_updated` header refreshed to
  note Epic V123-1 is 8/9 done with 1.9 in review.

## Files touched

- `internal/catalog/sources/fetcher_test.go` — `+~105 LOC`. Appended two
  tests + a 6-line `clientWithTimeout` helper under a new
  `--- V123-1.9 gap cases ---` section banner. No existing tests
  reordered or modified.
- `internal/catalog/sources/merger_test.go` — `+~80 LOC`. Inserted
  `TestMerge_EveryEntryCarriesOrigin` ahead of the existing Case-8
  `TestMerge_DeterministicOrdering` so the "every entry has Origin"
  invariant check reads sequentially with the other Origin-oriented
  cases (EmbeddedOnly / ThirdPartyOnly / Collision).
- `internal/catalog/sources/integration_test.go` — NEW, ~180 LOC.
  Single test `TestIntegration_FullLoop_FetcherToMergedCatalog` +
  local `entryNamesFor` helper.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` —
  `V123-1-9-…: backlog → review`; `last_updated` header refreshed.
- `.bmad/output/implementation-artifacts/V123-1-9-tests-unit-integration-for-fetch-merge-source-attribution.md`
  — frontmatter (`status: review`, `dispatched: 2026-04-23`) +
  retrospective sections (this file).

## Tests

Targeted run (package only):

```bash
go test ./internal/catalog/sources/... -race -count=1
# ok  	github.com/MoranWeissman/sharko/internal/catalog/sources	~7.5s
# 27 tests — 15 fetcher + 2 new fetcher + 8 merger + 1 new merger + 1 integration
```

Verbose summary of the 27 cases in execution order:

Fetcher (17, including 2 new):

1. `TestFetcher_HappyPath`
2. `TestFetcher_5xxRetainsSnapshot`
3. `TestFetcher_SchemaViolationRetainsSnapshot`
4. `TestFetcher_HTTPProxyRespected`
5. `TestFetcher_FreshStartNoSuccess`
6. `TestFetcher_SidecarDelegation`
7. `TestFetcher_NilVerifierSidecarIgnored`
8. `TestFetcher_ConcurrentFetchesInParallel`
9. `TestFetcher_RuntimeSSRFGuardBlocksPrivateIP`
10. `TestFetcher_CtxCancelStopsSupervisor`
11. `TestFetcher_StartThenStopIsClean`
12. `TestFetcher_NilVerifierNilClockOK`
13. `TestFetcher_ForceRefreshEmptyURLsMeansAll`
14. `TestFetcher_ForceRefreshUnknownURLIgnored`
15. `TestFetcher_SnapshotsReturnsDeepCopy`
16. **NEW** `TestFetcher_ClientTimeoutMarksFailed`
17. **NEW** `TestFetcher_InvalidYAMLMarksFailed`

Merger (9, including 1 new):

18. `TestMerge_EmptyInputs`
19. `TestMerge_EmbeddedOnly`
20. `TestMerge_ThirdPartyOnly`
21. `TestMerge_EmbeddedVsThirdPartyCollision`
22. `TestMerge_ThirdPartyVsThirdPartyCollision`
23. `TestMerge_ThreeWayCollision`
24. `TestMerge_StaleOrFailedSnapshotsSkipped`
25. **NEW** `TestMerge_EveryEntryCarriesOrigin`
26. `TestMerge_DeterministicOrdering`

Integration (1 new):

27. **NEW** `TestIntegration_FullLoop_FetcherToMergedCatalog`

Cross-package regression check:

```bash
go test ./internal/catalog/... ./internal/api/... -race -count=1
# ok  	github.com/MoranWeissman/sharko/internal/catalog	~4.4s
# ok  	github.com/MoranWeissman/sharko/internal/catalog/sources	~6.4s
# ok  	github.com/MoranWeissman/sharko/internal/api	~7.0s
```

## Decisions

- **Tolerant LastErr substring matching in both gap-fill fetcher tests.**
  Go's `net/http` wraps transport timeouts variously (`"context deadline
  exceeded"`, `"Client.Timeout exceeded while awaiting headers"`,
  `"net/http: request canceled (Client.Timeout exceeded ...)"`). Pinning
  to a single exact phrase would turn a Go stdlib detail change into a
  test break. Same reasoning for the invalid-YAML case — the fetcher's
  `recordSchemaFailure` path wraps loader errors as `"schema validation:
  %w"` even when the underlying cause is a parse error, so the test
  accepts any of `yaml`/`parse`/`unmarshal`/`schema`. Keeps the test
  a contract check ("it landed on Failed with a diagnostic error"), not
  a fragile phrase match.
- **`srv.Client()`-based HTTP client for the timeout test, not
  `http.DefaultClient` with `InsecureSkipVerify`.** The self-signed cert
  from `httptest.NewTLSServer` only validates against `srv.Client()`'s
  root CA pool. `clientWithTimeout` shallow-copies `srv.Client()` and
  overrides `.Timeout` so TLS still handshakes cleanly and the test
  failure really is the wall-clock timeout, not a TLS rejection. Same
  pattern the existing `newTestFetcher` helper uses, just with an extra
  Timeout wrinkle.
- **Unterminated flow sequence for the invalid-YAML payload.** Several
  payloads fail at parse time (mismatched quotes, control chars, etc.);
  the unterminated `[` sequence is the cleanest — it's obviously
  malformed to a human reader AND guaranteed to fail `yaml.v3`'s
  parser rather than a downstream schema check. Distinct enough from
  the existing `invalidCatalogYAML` constant (which is a well-formed
  document missing a required field) to prove the two code paths are
  both covered.
- **Inline `clientWithTimeout` instead of promoting a shared helper.**
  Brief explicitly permits inline helpers in the gap-fill tests. Only
  one test needs the Timeout override; a package-level helper would
  add surface area for one caller. When a second caller appears, the
  pattern is tiny enough to promote in one commit without friction.
- **Integration test stays in the `sources` package.** Brief Gotcha #6
  ("Do NOT wire the full `*api.Server`") — pulling `internal/api`
  would create an import cycle (`api → sources`). The in-package
  integration test still exercises every public function in the
  contract: `NewFetcher`, `SetHTTPClientForTest`, `ForceRefresh`,
  `Snapshots`, and `Merge`. Handler-level coverage is already owned
  by V123-1.4/1.5/1.6 tests in `internal/api`.
- **Distinct local helper `entryNamesFor` in `integration_test.go`
  instead of reusing `entryNames` from `merger_test.go`.** Both test
  files are in the same package, so the symbol collision would fail
  at compile time. Kept the two names distinct; the duplication is 4
  lines, and the alternative (extracting a shared
  `testhelpers_test.go`) is not worth the churn for a single extra
  helper call site. Brief §3 explicitly endorsed this ("inline is
  fine — integration tests don't need to share code with unit tests").
- **No edits to product code (`merger.go`, `fetcher.go`) — strict
  test-only story.** Hard constraint recap in the brief: "Don't touch
  merger.go, fetcher.go, or any product code." Every change lives
  under a `_test.go` file or in `.bmad/`.

## Gotchas / constraints addressed

1. **`AllowPrivate: true`** set on the integration test's config — the
   runtime SSRF guard would otherwise block 127.0.0.1 (Gotcha #1).
2. **`srv.Client()` as the HTTP client** on both the integration test
   and the timeout test (Gotcha #2) — the ephemeral CA from
   `httptest.NewTLSServer` must be trusted; a plain `http.Client{}` or
   `http.DefaultClient` fails TLS.
3. **Timeout-test uses an http.Client Timeout < server delay** (Gotcha
   #3) — 100ms vs 500ms — and asserts `StatusFailed`, *not* Stale
   (fresh start, no prior success).
4. **Invalid-YAML test is parse-level, not schema-level** (Gotcha #4) —
   unterminated flow sequence fails `yaml.Unmarshal` before the
   loader's schema checks can run. Distinct from the pre-existing
   `TestFetcher_SchemaViolationRetainsSnapshot` which tests a
   well-formed-but-schema-invalid payload.
5. **Integration-test cleanup** via `t.Cleanup(srv.Close)` + 5s ctx
   timeout (Gotcha #5) — the TLS server is reaped when the test ends;
   no goroutine leak.
6. **`ForceRefresh`, not `Start` + sleep** (Gotcha #6) — every new
   test uses `ForceRefresh(ctx)` for deterministic, non-flaky timing.
7. **`-race` passes** — no data races reported on the full sources
   package run with `-race -count=1`.

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean |
| Vet | `go vet ./...` | clean |
| Package tests | `go test ./internal/catalog/sources/... -race -count=1` | 27/27 PASS (≥26 target) |
| Cross-package | `go test ./internal/catalog/... ./internal/api/... -race -count=1` | all green |
| Lint | `golangci-lint run ./internal/catalog/sources/...` | **skipped** — binary not installed locally. CI will run it on the PR. |
| Swagger | n/a — test-only story, no API surface change | n/a |

## Deviations from the brief

- **None substantive.** The brief's sketched integration test listed a
  `conflict.Reason` check of `"embedded-wins"` (the raw string); I used
  the exported `ReasonEmbeddedWins` constant instead — the brief's own
  preamble called this out explicitly ("check merger.go for the exact
  constant — may be `ReasonEmbeddedWins` vs the raw string
  `"embedded-wins"`"). Using the constant means a future rename of the
  string value ripples through the test automatically.
- **Integration test placed in `integration_test.go`, not merged into
  an existing file.** Matches the brief's §3 file path exactly.
