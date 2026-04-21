---
story_key: V123-1-2-fetch-loop-yaml-validation-last-successful-snapshot
epic: V123-1 (Third-party private catalogs)
status: in-review
effort: L
dispatched: 2026-04-21
merged: (pending PR)
---

# Story V123-1.2 — Fetch loop + YAML validation + last-successful snapshot

## Brief (from epics-v1.23.md)

As the **catalog subsystem**, I want a resilient fetch loop that pulls configured URLs on startup + on a cadence, validates each against the schema, and keeps a last-successful snapshot on failure, so that a broken third-party URL never drops the catalog.

### Acceptance criteria

1. **Happy path** — Valid YAML matching `catalog/schema.json` v1.1 → entries parsed + stored as current snapshot, status `ok`.
2. **Upstream failure (5xx / timeout)** — Previous snapshot retained, status `stale`, non-fatal error log.
3. **Schema validation failure** — Prior snapshot retained, status `failed`, validation error stored for the future `GET /api/v1/catalog/sources` payload.
4. **HTTPS_PROXY respected** — HTTP client uses `net/http.ProxyFromEnvironment`.
5. **Fresh start, no successful fetches** — Source contributes 0 entries (status `failed`); embedded catalog still serves.
6. **Sidecar verifier delegation** — If `.sig` / `.bundle` sidecar present AND Subsystem B trust policy configured → invoke `SidecarVerifier` interface; else mark snapshot entries `verified: false`. Fetcher holds only the interface; MUST NOT import `internal/catalog/signing/`.

### Technical notes

- File: `internal/catalog/sources/fetcher.go`
- Uses `internal/catalog/loader.go` for schema validation (shared validator)
- In-memory only (NFR-V123-1). No disk cache.
- `SidecarVerifier` interface lives in fetcher package; concrete implementation comes in V123-2.2.

### Dependencies

- V123-1.1 (merged — provides `CatalogSourcesConfig` on `Server.catalogSources`)

## Implementation notes

Three atomic commits on `dev/v1.23-fetcher` (off `bf6186a`):

1. **Interface-first:** `SidecarVerifier` + `TrustPolicy` live in `internal/catalog/sources/verifier.go`. Fetcher package OWNS the contract; Subsystem B will implement it later. Dependency direction flows Subsystem B → Subsystem A (signing imports fetcher types), never the reverse — matches design §3.3.1. Return contract is explicit: legitimate "signature doesn't match" / "untrusted identity" must come back as `(false, "", nil)`, NOT as errors, so the fetcher records the result and keeps going. Only infrastructure failures (network, malformed bundle) return `err != nil`.

2. **Fetcher core** (`fetcher.go`, ~620 lines):
   - `Fetcher` struct holds `map[url]*SourceSnapshot` under `sync.RWMutex`. Snapshots are seeded with `StatusFailed` placeholders at construction so callers that read `Snapshots()` before the first fetch returns get a stable shape.
   - Lifecycle is supervisor + fanout: a single goroutine spawned by `Start` runs the initial fetch-all immediately, then loops on `ticker.C()`. Each fanout spawns N per-URL goroutines tracked via `fetchWG` so `Stop` drains cleanly. `stopOnce` guards the `stopCh` close so double-`Stop` is safe.
   - `ForceRefresh(ctx, urls...)` is the blocking variant used by the future V123-1.6 endpoint. Empty URL list = all configured sources; unknown URLs are silently dropped (configured-URL drift shouldn't turn an admin action into a cryptic error).
   - `Snapshots()` returns a deep copy (fresh map + fresh `*SourceSnapshot` + fresh `Entries` slice) so callers can mutate freely without racing. Verified by a dedicated test.
   - Runtime SSRF guard re-resolves the host on every fetch. Even though startup validated the URL's IPs aren't private, a public hostname can re-resolve to 10.x via DNS rebinding between boot and fetch. Skip when `cfg.AllowPrivate` is set (mirrors startup escape hatch). DNS failure is fail-open — transient resolver hiccups should not blackout the catalog.
   - Schema validation path: `catalog.LoadBytes(body)` is reused as-is. No refactor of `internal/catalog/loader.go` was needed. The existing `LoadBytes` parses YAML + runs `validateEntry` on each row + returns `*Catalog`. Fetcher grabs `cat.Entries()` for the snapshot.
   - Sidecar probe: HEAD `{URL}.bundle` first, then `{URL}.sig`, with a 5s per-probe timeout. Only probed when a verifier is wired — keeps the fetcher independent of signing when Subsystem B isn't compiled in. A probe error or non-2xx is silently treated as "no sidecar".
   - HTTP client: `Timeout: 30s`, `Transport` with `Proxy: http.ProxyFromEnvironment`, `TLSHandshakeTimeout: 10s`, `ResponseHeaderTimeout: 20s`, body size capped at 8 MiB (hostile-source defense). The proxy hook satisfies AC #4 without a mock proxy server.
   - Logging: never logs the URL. Every log line carries a 10-char SHA-256 fingerprint (`source_fp`). Addresses Gotcha #1 from V123-1.1 (catalog URL paths may encode auth tokens).

3. **Server wiring** (`cmd/sharko/serve.go` + `internal/api/{router,catalog}.go`):
   - New `sourcesFetcher *sources.Fetcher` field on `Server`.
   - `Server.SetSourcesFetcher(f)` / `Server.SourcesFetcher()` accessors on `internal/api/catalog.go` mirror the existing `SetCatalogSources` pattern. Future V123-1.3 / V123-1.5 / V123-1.6 will consume `Server.SourcesFetcher()`.
   - Boot-time wiring: the fetcher is only created when `len(catSources.Sources) > 0` — embedded-only mode keeps `Server.sourcesFetcher` nil. `Start(context.Background())` kicks off the loop; `defer sourcesFetcher.Stop()` registers the cleanup alongside the other background reconcilers.

### Tests

15 test functions in `fetcher_test.go`, all passing in 4.1s:

- AC #1 — `TestFetcher_HappyPath`: valid YAML → 2 entries parsed, `StatusOK`, `LastSuccessAt` non-zero, `LastErr` nil.
- AC #2 — `TestFetcher_5xxRetainsSnapshot`: first fetch healthy, flip server to 500, assert prior entries retained + `StatusStale` + `LastErr` populated.
- AC #3 — `TestFetcher_SchemaViolationRetainsSnapshot`: first fetch healthy, serve schema-invalid YAML, assert prior entries retained + `StatusFailed` + schema-flavoured error.
- AC #4 — `TestFetcher_HTTPProxyRespected`: asserts `Transport.Proxy` is `http.ProxyFromEnvironment` by function pointer identity AND end-to-end — sets `HTTPS_PROXY` env, builds a request, calls the Proxy hook, confirms it returned the expected proxy URL. Avoids needing a mock CONNECT proxy.
- AC #5 — `TestFetcher_FreshStartNoSuccess`: 500 on first fetch → `StatusFailed`, empty `Entries`, zero `LastSuccessAt`.
- AC #6 — `TestFetcher_SidecarDelegation`: mock verifier returns `(true, issuer, nil)`, server exposes `/catalog.yaml.bundle` on HEAD 200 → verifier.Verify called exactly once with `.bundle` URL, snapshot `Verified=true`, `Issuer` populated. Counterpart `TestFetcher_NilVerifierSidecarIgnored` proves nil verifier + sidecar present leaves `Verified=false` with no panic and no verifier call.
- Concurrency — `TestFetcher_ConcurrentFetchesInParallel`: 3 servers each delaying 200ms, fetched together finish in < 400ms (parallel, not 3x serial). Uses a pooled TLS client that trusts every httptest server's self-signed cert.
- Runtime SSRF — `TestFetcher_RuntimeSSRFGuardBlocksPrivateIP`: stubbed `lookupHostFn` resolves `public-looking.example.com` → `10.0.0.5`, assert snapshot flips to `StatusFailed` with a "private" error.
- Lifecycle — `TestFetcher_CtxCancelStopsSupervisor` + `TestFetcher_StartThenStopIsClean`: repeat Start/Stop cycles then assert `runtime.NumGoroutine()` returns to baseline (+3 headroom for scheduler jitter). Both close the httptest server BEFORE the count so they only measure fetcher goroutines, not httptest's accept loop.
- Contract — `TestFetcher_NilVerifierNilClockOK` (nil defaults don't panic), `TestFetcher_ForceRefreshEmptyURLsMeansAll` (empty list = all sources), `TestFetcher_ForceRefreshUnknownURLIgnored` (unknown URLs silently skipped), `TestFetcher_SnapshotsReturnsDeepCopy` (mutating returned map / slices does not corrupt fetcher state).

## Decisions

- **No refactor of `internal/catalog/loader.go`.** `catalog.LoadBytes([]byte) (*Catalog, error)` already existed as an exported helper (previously used by `loader_test.go`). It parses + validates in one shot, giving us ready-to-merge `[]CatalogEntry`. Exposing a fresh `ValidateBytes` would have been strictly worse — redundant with `LoadBytes` and forcing the fetcher to re-parse.
- **Real clock by default, clock abstraction for future tests.** Tests use `ForceRefresh` rather than the ticker path to avoid timing flakes — both driven by the same `fetchOne` internals so the ticker adds no untested behaviour. The `Clock` interface is still introduced so when a future story needs fake-time tests (e.g. stale-aging logic) the hook is already there.
- **HTTP timeout = 30s.** Third-party catalogs are tens of KB; a generous 30s outer deadline plus a 20s `ResponseHeaderTimeout` catches slow-TLS hosts without being hostile to legitimate hiccups. Matches common sense for server-to-server API calls.
- **Sidecar probe via HEAD, `.bundle` first.** HEAD minimizes traffic in the common "no sidecar" case. `.bundle` is the Sigstore format design §3.2 explicitly prefers; `.sig` is the fallback.
- **Sidecar probe only when verifier is wired.** Even though the test server could advertise a sidecar regardless, skipping the probe when verifier is nil saves a round-trip on every fetch in embedded-only + no-Subsystem-B mode. This also keeps the test matrix honest: "nil verifier, sidecar present" now strictly means "no verify call AND no wasted HEAD".
- **Body size cap at 8 MiB.** `io.LimitReader` with a +1 trip-wire. A hostile source could otherwise stream forever (response would still honour timeouts but would pin memory).
- **URL fingerprint in logs.** Addresses Gotcha #1 from V123-1.1. Using SHA-256 so the fingerprint is deterministic per URL but non-reversible — ops can tie a log line to a configured source without exposing the auth-token-bearing path.
- **Metrics live in `internal/metrics`, not on the fetcher.** Consistent with every other Sharko subsystem (reconcilers, HTTP layer, auth). `promauto.NewCounterVec` / `NewGaugeVec` self-register with the default registry so the existing `/metrics` endpoint picks them up with zero plumbing.
- **`SetHTTPClientForTest` + `HTTPClientForTest` escape hatches.** Testing with `httptest.NewTLSServer` requires injecting a client that trusts the self-signed cert. Cleaner than exposing a functional option in the production constructor — the public `NewFetcher` signature stays `(cfg, verifier, clock)` per the story brief.

## Commits

- `075bc7a` feat(catalog/sources): SidecarVerifier interface + TrustPolicy (V123-1.2)
- `29ab921` feat(catalog/sources): fetch loop + snapshots + runtime SSRF guard (V123-1.2)
- `90191fb` feat(catalog/sources): wire fetcher into server bootstrap (V123-1.2)

(Story-file update is folded into this PR as a separate commit — see Files touched.)

## PR

_(to be filled when PR is opened)_

## Files touched

| File | Change | Lines |
|------|--------|-------|
| `internal/catalog/sources/verifier.go` | new | +79 |
| `internal/catalog/sources/fetcher.go` | new | +779 |
| `internal/catalog/sources/fetcher_test.go` | new | +657 |
| `internal/metrics/metrics.go` | +19 | (3 new vars) |
| `internal/api/router.go` | +9 | (import + field) |
| `internal/api/catalog.go` | +16 | (accessors) |
| `cmd/sharko/serve.go` | +13 | (Start/Stop wiring) |
| **Total** | — | **+1572 / −1** |

No existing code was refactored. `internal/catalog/loader.go` is untouched — the story's proposed `ValidateBytes` extraction was not needed.

## Tests

Package `./internal/catalog/sources/...` — 15 test functions, all passing in 4.1s:

| # | Test | Proves |
|---|------|--------|
| 1 | `TestFetcher_HappyPath` | AC #1: valid YAML → `StatusOK` + entries |
| 2 | `TestFetcher_5xxRetainsSnapshot` | AC #2: 500 after success → `StatusStale` + entries retained |
| 3 | `TestFetcher_SchemaViolationRetainsSnapshot` | AC #3: bad YAML → `StatusFailed` + entries retained |
| 4 | `TestFetcher_HTTPProxyRespected` | AC #4: `Transport.Proxy == http.ProxyFromEnvironment` + end-to-end HTTPS_PROXY hook |
| 5 | `TestFetcher_FreshStartNoSuccess` | AC #5: fresh-start failure → `StatusFailed` + empty `Entries` |
| 6 | `TestFetcher_SidecarDelegation` | AC #6 positive: verifier called, `Verified=true`, `Issuer` populated |
| 7 | `TestFetcher_NilVerifierSidecarIgnored` | AC #6 negative: nil verifier → no call, `Verified=false` |
| 8 | `TestFetcher_ConcurrentFetchesInParallel` | Fetches fan out — 3× 200ms servers finish in < 400ms |
| 9 | `TestFetcher_RuntimeSSRFGuardBlocksPrivateIP` | Hostname→10.x at runtime blocked even after startup-ok |
| 10 | `TestFetcher_CtxCancelStopsSupervisor` | Repeated Start/Cancel/Stop leaves no goroutine leak |
| 11 | `TestFetcher_StartThenStopIsClean` | Immediate Stop (no tick) unblocks supervisor cleanly; double-Stop safe |
| 12 | `TestFetcher_NilVerifierNilClockOK` | `NewFetcher(cfg, nil, nil)` is supported |
| 13 | `TestFetcher_ForceRefreshEmptyURLsMeansAll` | `ForceRefresh()` fans out over all configured sources |
| 14 | `TestFetcher_ForceRefreshUnknownURLIgnored` | Unknown URLs are silently skipped (no error) |
| 15 | `TestFetcher_SnapshotsReturnsDeepCopy` | Callers mutating returned map / slices cannot corrupt fetcher state |

Full `go test ./...` is green. UI tests (167), UI build, and `helm template charts/sharko` all pass.

## Gotchas for V123-1.3 (next story — merge under embedded catalog)

1. **`Fetcher.Snapshots()` returns a DEEP copy every call.** The returned map, every `*SourceSnapshot`, and every `Entries` slice are fresh allocations. Safe to mutate. Call cost is O(total entries) — small in practice but don't call it in a tight loop; cache the result for the duration of one merge pass.

2. **Snapshot keys are canonical URLs** (as stored in `cfg.Sources[i].URL`). These are the canonical forms produced by V123-1.1's `canonicalize` — lowercase host, port 443 stripped, trailing slashes normalized, query/fragment dropped. The merge story should use them verbatim for the `source` attribution field in the merged index, e.g. `entry.Source = snapshot.URL`.

3. **`Entries` is sorted by name** already — `catalog.LoadBytes` (which the fetcher calls) sorts internally and returns a fresh slice via `Entries()`. The merge story can rely on this for deterministic output. If you want a different order (e.g. insertion order), you'll need to re-sort.

4. **A `StatusStale` snapshot still has valid `Entries`.** Don't filter by `Status == StatusOK` when merging — that would drop last-known-good data on a transient upstream hiccup, which is exactly what this story was built to prevent. The merge rule is: `len(Entries) > 0` means "serve these entries". `Status` is for the API payload / UI indicator.

5. **A `StatusFailed` snapshot may or may not have entries.** Fresh-start failure → empty `Entries`; schema violation after a prior success → `Entries` retained (AC #3). Again, filter on `len(Entries) > 0`, not on `Status`.

6. **Embedded-wins conflict rule (design §2.2):** when the same `name` appears in embedded + any third-party snapshot, embedded wins. Third-party version surfaces as "overridden by internal curation" in the UI. The merge story builds the final in-memory index by starting with embedded entries, then adding third-party entries whose `name` isn't already in the embedded set. Don't forget to carry `snap.Verified` + `snap.Issuer` onto the merged entry so the UI badge has the data.

7. **`Server.SourcesFetcher()` may be nil** — that's the embedded-only mode (no `SHARKO_CATALOG_URLS` configured). Merge code must tolerate nil and simply skip the third-party layer.

8. **Fetcher runs on its own schedule.** Merge doesn't need to trigger fetches; it reads whatever snapshot is current. A forced refresh (V123-1.6) calls `Fetcher.ForceRefresh` which blocks until complete, so the API endpoint can merge immediately after and return a coherent payload.

9. **`SourceSnapshot.Verified` / `Issuer`** are per-snapshot, not per-entry. Every entry from a given URL inherits the same verification status in v1.23. If future stories want per-entry verification (e.g. a catalog where only some entries are signed), the shape will need to change — but that's out of scope here.

10. **Metrics to alert on:** `sharko_catalog_source_last_success_timestamp{url}` gauges let ops graph freshness; `sharko_catalog_source_fetch_total{url,status="failed"}` rate shows chronically-broken sources. No alerts are shipped; that's a V123-1.7 concern.

11. **SidecarVerifier lives in the fetcher package (`internal/catalog/sources.SidecarVerifier`).** When V123-2.2 implements cosign verification, the signing package will import this interface — not the other way around. Any future changes to the interface shape go here, not in signing.

12. **SSRF guard state:** `cfg.AllowPrivate == true` disables BOTH startup and runtime checks. Operators using this for home-lab are on their own. Reasonable for pre-prod; revisit before V2.
