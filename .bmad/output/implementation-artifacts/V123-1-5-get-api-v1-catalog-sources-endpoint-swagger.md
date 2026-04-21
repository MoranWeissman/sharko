---
story_key: V123-1-5-get-api-v1-catalog-sources-endpoint-swagger
epic: V123-1 (Third-party private catalogs)
status: review
effort: S
dispatched: 2026-04-22
merged: TBD
---

# Story V123-1.5 — `GET /api/v1/catalog/sources` endpoint + swagger

## Brief (from epics-v1.23.md §V123-1.5 + design §6.8)

As an **API consumer**, I want a read endpoint listing every catalog source
with its fetch status, so that the UI (and monitoring) can surface health.

## Acceptance criteria

**Given** `GET /api/v1/catalog/sources`
**When** called (authenticated)
**Then** returns a JSON array:
```json
[
  {
    "url": "embedded",
    "status": "ok",
    "last_fetched": null,
    "entry_count": N,
    "verified": true
  },
  {
    "url": "https://private.example.com/catalog.yaml",
    "status": "ok" | "stale" | "failed",
    "last_fetched": "2026-04-22T10:00:00Z" | null,
    "entry_count": N,
    "verified": false,
    "issuer": "https://github.com/..."
  }
]
```

**Given** a third-party source has no successful fetch ever
**Then** its record has `entry_count: 0`, `status: "failed"`, `last_fetched: null`.

**Given** no `SHARKO_CATALOG_URLS` configured (embedded-only mode)
**Then** the response contains exactly one element (the embedded pseudo-source).

**Given** the fetcher has not yet populated its first snapshot round
**Then** third-party sources still appear, with `status` reflecting the current
per-source state (may be `"failed"` or `"stale"` during boot).

**Given** swaggo annotations land
**When** `swag init` regens `docs/swagger/swagger.json`
**Then** the file contains the new endpoint + response schema.

## Design constraints

### URL exposure — decision: return raw URL

Design §6.8 specifies `url: "<full URL>"` in the response. The trade-off is
already acknowledged in V123-1.1 / V123-1.2 gotchas — URLs *may* contain auth
tokens in the path, and those are logged nowhere. But **returning the URL in
an authenticated API response is intentional**: the caller is the admin who
already configured the env var, so no new information is disclosed.

- **Logs still never include URLs** — if the handler writes any diagnostic
  log line, it must use the fetcher's existing `source_fp` fingerprint
  convention, not the raw URL.
- **The embedded record uses the literal string `"embedded"`** as its `url`,
  never a file path.

### Authentication + tier

Design §6.8 and epic both note "Read-only — no audit / tier". The endpoint
requires existing auth (BearerAuth), but unlike V123-1.6's force-refresh it
does not need a specific tier check nor an audit trail.

### Embedded pseudo-source

Always appears in the response, always first (alphabetical / insertion order
is a detail — first is simpler for callers to render):

```go
{URL: "embedded", Status: "ok", LastFetched: nil, EntryCount: <len Catalog.Entries>, Verified: true}
```

`Verified: true` is semantically "the binary itself trusts its own bundled
catalog" — not a cosign statement. (Cosign signing of the embedded catalog is
V123-2.5, future story.)

## Implementation plan

### Files

- `internal/api/catalog_sources.go` (NEW) — handler + response types.
- `internal/api/catalog_sources_test.go` (NEW) — unit tests.
- `internal/api/router.go` — register the route.
- `docs/swagger/*` — regen after annotations land.

### Handler signature

```go
// handleListCatalogSources godoc
//
// @Summary List catalog sources with fetch status
// @Description Returns one record per configured catalog source (the embedded
//   binary catalog + every URL from SHARKO_CATALOG_URLS). Per-source fields:
//   url, status (ok|stale|failed), last_fetched (RFC3339 or null), entry_count,
//   verified (cosign-verified — currently always true for "embedded", false
//   for third-party until V123-2.2 lands), and optional issuer when verified.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {array} catalogSourceRecord
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/sources [get]
func (s *Server) handleListCatalogSources(w http.ResponseWriter, r *http.Request)
```

Logic:
1. If `s.catalog == nil` → 503 with `{"error": "catalog not loaded"}`.
2. Build embedded record: `{URL: "embedded", Status: "ok", LastFetched: nil, EntryCount: s.catalog.Len(), Verified: true}`.
3. If `s.sourcesFetcher == nil` → return `[embedded]`.
4. snapshots := `s.sourcesFetcher.Snapshots()`.
5. For each snapshot:
   - Map `SourceStatus` to response string:
     - `StatusOK` → `"ok"`
     - `StatusStale` → `"stale"`
     - `StatusFailed` → `"failed"`
   - `LastFetched` = `snap.LastSuccessAt` (pointer-to-time so JSON emits `null` when zero).
   - `EntryCount` = `len(snap.Entries)`.
   - `Verified` = `snap.Verified`.
   - `Issuer` = `snap.Issuer` (empty string → `json:",omitempty"` drops it).
6. Sort third-party records alphabetically by URL for deterministic output; embedded always first.
7. JSON-encode.

### Response type

```go
// catalogSourceRecord is one row of the GET /catalog/sources response.
type catalogSourceRecord struct {
    URL         string     `json:"url"`
    Status      string     `json:"status"`
    LastFetched *time.Time `json:"last_fetched"`
    EntryCount  int        `json:"entry_count"`
    Verified    bool       `json:"verified"`
    Issuer      string     `json:"issuer,omitempty"`
}
```

- `LastFetched *time.Time` (not `time.Time`) so an un-fetched source renders
  as JSON `null` cleanly (no `0001-01-01T00:00:00Z` zero-time garbage).
- **Do NOT** include fields like `allow_private` on per-record — that's a
  server-wide config, not a per-source property. If it's useful later,
  surface it at a different endpoint or a top-level wrapper. Skip for now.
- **Do NOT** include `conflicts` here — the merger's `MergedCatalog.Conflicts`
  are cross-source diagnostics; if the UI needs them, add a separate endpoint
  or wrap the response. Keep V123-1.5 scope tight.

### Router wiring

In `internal/api/router.go`, register the handler on the authenticated
subrouter alongside the existing `/catalog/addons` routes:

```go
r.Get("/catalog/sources", s.handleListCatalogSources)
```

Match the pattern used by sibling `/catalog/*` routes (same middleware chain —
auth required, no tier check).

### Swagger regen

After annotations land:
```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```
Commit `docs/swagger/*` diff.

## Test plan

`internal/api/catalog_sources_test.go`:

1. **Embedded-only (no fetcher)**: response is `[{url:"embedded", status:"ok", ...}]`, length 1.
2. **With fetcher, no snapshots**: response is `[embedded]` (no third-party rows — fetcher exists but has no sources configured).
3. **With fetcher + populated OK snapshot**: response has embedded + one third-party row with `status:"ok"`, `last_fetched` non-null, `entry_count > 0`.
4. **With fetcher + stale snapshot**: third-party row `status:"stale"`.
5. **With fetcher + failed snapshot (never succeeded)**: third-party row `status:"failed"`, `entry_count:0`, `last_fetched:null`.
6. **Multiple third-party sources**: response sorted alphabetically by URL; embedded first.
7. **503 on nil catalog**: response status 503, JSON body has `error` key.
8. **JSON shape**: decode into `[]catalogSourceRecord`, assert every field type matches.

Use the `SetSnapshotsForTest` helper added in V123-1.4 to inject snapshots.

## Quality gates

- `go build ./...`
- `go vet ./...`
- `go test ./internal/api/... -race -count=1`
- `golangci-lint run ./internal/api/...` (silent skip if not installed)
- `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` → diff committed.
- Swagger Up To Date CI check must pass.

## Explicit non-goals

- Force-refresh (V123-1.6).
- UI badge / view (V123-1.7 / V123-1.8).
- Conflict diagnostic surfacing (deferred — see note above).
- Cosign verification wiring (V123-2.2).
- Integration tests with real HTTP fixture server (V123-1.9).

## Dependencies

- V123-1.4 source attribution + `SetSnapshotsForTest` helper — done ✅.
- V123-1.2 fetcher `Snapshots()` — done ✅.

## Gotchas

1. **Never log catalog URLs.** If the handler emits any log line, use the
   fetcher's `source_fp` fingerprint convention. Handler body ideally emits
   no log lines at all (it's a read path).
2. **`url: "embedded"` literal.** Don't use a path, empty string, or `"*"`.
   Design §6.8 specifies the literal string.
3. **Determinism.** Sort third-party rows by URL before JSON-encoding so
   tests don't flake on Go map iteration order.
4. **`LastFetched` nil handling.** Use `*time.Time`; JSON encodes a nil pointer
   as `null`. Convert `SourceSnapshot.LastSuccessAt` (time.Time) → pointer only
   when non-zero.
5. **Nil-safety on fetcher.** `s.sourcesFetcher == nil` is the common embedded-only
   case; return just the embedded pseudo-source, do NOT 503.
6. **Swagger.** `@Success 200 {array} catalogSourceRecord` — swaggo needs
   the type to be in the same package or reachable. Put `catalogSourceRecord`
   in `catalog_sources.go` and it'll resolve.

## Role files (MUST embed in dispatch)

- `.claude/team/go-expert.md` — handler + response struct.
- `.claude/team/docs-writer.md` — swagger regen.
- `.claude/team/test-engineer.md` — table-driven handler tests.

## PR plan

- Branch: `dev/v1.23-sources-endpoint` off main.
- Commits:
  1. `feat(api): GET /catalog/sources endpoint (V123-1.5)`
  2. `docs(swagger): regen for catalog sources endpoint`
  3. `chore(bmad): mark V123-1.5 for review`
- No tag.

## Next story after this

V123-1.6 — `POST /api/v1/catalog/sources/refresh` (Tier-2 admin endpoint with
audit trail, calls `fetcher.ForceRefresh`).

## Tasks completed

- [x] New `internal/api/catalog_sources.go` — `catalogSourceRecord` response struct + `handleListCatalogSources` handler + `recordFromSnapshot` projection helper.
- [x] Swagger annotations on the handler: `@Tags catalog`, `@Produce json`, `@Security BearerAuth`, `@Success 200 {array} catalogSourceRecord`, `@Failure 503`, `@Router /catalog/sources [get]`.
- [x] Embedded pseudo-source always first in the response with `url:"embedded"`, `status:"ok"`, `last_fetched:null`, `entry_count:s.catalog.Len()`, `verified:true`.
- [x] Third-party rows projected from `s.sourcesFetcher.Snapshots()` — `status` mapped via `string(snap.Status)` (already `"ok"|"stale"|"failed"`), `last_fetched` as `*time.Time` so zero → JSON `null`, `entry_count` = `len(snap.Entries)`, `verified` + `issuer` plumbed through. `issuer` omitted via `,omitempty` when empty.
- [x] Deterministic output: third-party rows sorted alphabetically by URL before encoding; embedded stays first. Guards against Go map iteration order flake.
- [x] Nil-safety: `s.sourcesFetcher == nil` returns `[embedded]` only — NOT a 503 (embedded-only is the normal default). `s.catalog == nil` returns 503 with `{"error": "catalog not loaded"}` matching the sibling `/catalog/addons` contract.
- [x] Router wiring in `internal/api/router.go` — registered `GET /api/v1/catalog/sources` alongside `/catalog/addons` on the same authenticated subrouter, no tier check, no audit middleware coupling.
- [x] No URL logging — handler body has no log lines at all; the merger + fetcher already use the `source_fp` SHA-256 prefix convention if any log is ever needed in this path.
- [x] New `internal/api/catalog_sources_test.go` — 8 tests reusing `testCatalog`/`serverWithCatalog`/`makeFetcherWithSnapshots` helpers from the V123-1.4 test harness.
- [x] Swagger regen: `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` — new `/catalog/sources` path + `internal_api.catalogSourceRecord` definition landed in both `swagger.json` and `swagger.yaml` (+64 LOC each in JSON/YAML, +64 LOC in `docs.go`).
- [x] `go build ./...` clean.
- [x] `go vet ./...` clean.
- [x] `go test ./internal/api/... -race -count=1` — full api package green, including the 8 new tests.
- [x] `go test ./internal/api/... ./internal/catalog/...` full test sweep — all three packages PASS.
- [x] `golangci-lint` — not installed locally; skipped per brief (CI will run it).
- [x] BMAD tracking: sprint-status.yaml flipped `V123-1-5` → `review`, story frontmatter updated to `status: review` + `dispatched: 2026-04-22`.

## Files touched

- `internal/api/catalog_sources.go` (NEW) — handler + response struct + snapshot→record projection.
- `internal/api/catalog_sources_test.go` (NEW) — 8 test cases.
- `internal/api/router.go` — single `mux.HandleFunc` line added alongside `/catalog/addons`.
- `docs/swagger/docs.go`, `docs/swagger/swagger.json`, `docs/swagger/swagger.yaml` — regen; new endpoint + `catalogSourceRecord` schema.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — `V123-1-5` backlog → review; `last_updated` refreshed to 2026-04-22.
- `.bmad/output/implementation-artifacts/V123-1-5-get-api-v1-catalog-sources-endpoint-swagger.md` — frontmatter updated; retrospective sections appended.

## Tests

Targeted run (race-enabled):

```bash
go test ./internal/api/... -race -count=1
# ok   github.com/MoranWeissman/sharko/internal/api     ~6s
```

Full sweep of adjacent packages:

```bash
go test ./internal/api/... ./internal/catalog/... -race -count=1
# ok   github.com/MoranWeissman/sharko/internal/api              ~6s
# ok   github.com/MoranWeissman/sharko/internal/catalog          ~2s
# ok   github.com/MoranWeissman/sharko/internal/catalog/sources  ~4s
```

New tests (8):

1. `TestListCatalogSources_EmbeddedOnly_NoFetcher` — no fetcher wired at all; single-element response `[{url:"embedded", status:"ok", ...}]`. Asserts handler does NOT 503 when `s.sourcesFetcher == nil`.
2. `TestListCatalogSources_EmbeddedOnly_FetcherNoSnapshots` — fetcher exists but snapshots map is empty; same single-element result as case 1.
3. `TestListCatalogSources_WithOKSnapshot` — one healthy third-party snapshot; expects embedded + one `status:"ok"` row with `last_fetched` non-nil + equal to `LastSuccessAt`, `entry_count == len(Entries)`, `verified` + `issuer` plumbed through.
4. `TestListCatalogSources_WithStaleSnapshot` — `StatusStale` surfaces as `"stale"` on the wire; `last_fetched` reflects the prior success (not the most recent attempt); prior-good `entry_count` retained.
5. `TestListCatalogSources_WithFailedSnapshot` — fresh-start failure (no prior success): `status:"failed"`, `last_fetched:null` (zero-time → nil pointer), `entry_count:0`, `verified:false`.
6. `TestListCatalogSources_MultipleSourcesSortedByURL` — 3 snapshots injected out-of-order (`zeta`, `alpha`, `mid`); expect `[embedded, alpha, mid, zeta]` — embedded first, third-party alphabetical.
7. `TestListCatalogSources_503OnNilCatalog` — `s.catalog == nil` → 503 with JSON body containing an `error` key. Matches the sibling `/catalog/addons` handler contract.
8. `TestListCatalogSources_JSONShape` — round-trips the wire response through `[]catalogSourceRecord` AND a permissive `[]map[string]interface{}`, asserting: `last_fetched` is JSON `null` for embedded and a RFC3339 string for third-party (parse-check); `verified` round-trips as bool; `issuer` `omitempty` elides when empty on embedded; all typed fields survive decoding.

## Decisions

- **Response is a bare JSON array, not an envelope.** Design §6.8 specifies the array shape and the swagger is `@Success 200 {array} catalogSourceRecord`. Envelopes (`{sources:[...], total:N}`) were rejected — `total` is trivially derivable client-side and adds no diagnostic signal, whereas the bare array matches the design spec and is trivially streamable.
- **`recordFromSnapshot` is unexported and lives in the same file as the handler.** Swaggo's `{array} catalogSourceRecord` needs the type to be reachable from the handler's package; keeping the projection helper alongside means a future change to the record shape is a single-file diff.
- **Status string is derived via `string(snap.Status)` rather than a switch.** `sources.SourceStatus` is already `type SourceStatus string` with literal values `"ok"`, `"stale"`, `"failed"` — the design doc + the fetcher's status constants agree on the wire-level spelling, so a direct cast is safe and avoids a mapping that would rot if a new status were ever added.
- **`LastFetched *time.Time` with pointer-to-zero guard.** `snap.LastSuccessAt.IsZero()` → nil pointer → JSON `null`. Avoids `"0001-01-01T00:00:00Z"` zero-time garbage. Considered a `null` tag + `omitempty` but that would drop the field entirely for embedded, which the AC explicitly requires ("`last_fetched: null`" is a required key).
- **Embedded `verified: true`.** Per design §6.8 and the brief: the binary trusts its own bundled catalog. Not a cosign statement — V123-2.5 will add cryptographic signing of the embedded catalog, and at that point `verified` can be a function of "did our release pipeline sign this?". For now it is a sentinel meaning "not third-party".
- **No log lines in the handler body.** Gotcha #1 in the brief is "never log catalog URLs". The handler emits zero log lines — the read path doesn't need them, and avoiding them entirely means there is no risk of a future refactor accidentally introducing a `slog.Info("...", "url", snap.URL)` line. If any log is ever needed here, the `urlFingerprint(snap.URL)` helper in the fetcher is the right tool.
- **Sort by URL, not by status or entry_count.** Deterministic is the only hard requirement. URL sort was picked because it's stable across fetch cycles (status/entry_count can flip between calls) and matches the UI's likely display order (alphabetical list of configured sources).
- **No `total` / no conflicts / no `allow_private` / no force-refresh.** All explicitly out of scope per the brief. `conflicts` are cross-source diagnostics owned by a future endpoint (deferred in §"explicit non-goals"). `allow_private` is a server-wide config, not per-source metadata. Force-refresh is V123-1.6's job. UI surfaces are V123-1.7/1.8.
- **Test harness reused from V123-1.4.** `testCatalog`, `serverWithCatalog`, and `makeFetcherWithSnapshots` were introduced for the merge-helper tests in V123-1.4 and exactly fit this story's needs. No new helpers were added; the 8 test cases are all readable top-to-bottom without hidden setup.
- **`writeError(w, 503, ...)` rather than inline `json.NewEncoder`.** The brief left this open ("use pattern from sibling handlers"). `writeError` is the package-wide helper already used by `handleListCatalogAddons`' 503 branch — consistency with the rest of `internal/api/*.go` trumped adding a new local helper.

## Gotchas / constraints addressed

1. **Never log URLs** — handler emits zero log lines; the snapshot's URL is only ever serialised to JSON as a response field (intentional, authenticated).
2. **`url: "embedded"` literal** — hard-coded as a string constant in the record construction, not derived from a path or empty string.
3. **Determinism** — `sort.Slice` on the third-party slice before appending so Go's randomised map iteration never leaks into the response; `TestListCatalogSources_MultipleSourcesSortedByURL` enforces this.
4. **`LastFetched` nil handling** — `*time.Time`; conversion happens only when `!snap.LastSuccessAt.IsZero()`. JSON-level round-trip is asserted in `TestListCatalogSources_JSONShape` both with `null` (embedded row) and RFC3339 string (third-party row).
5. **Nil-safety on fetcher** — `s.sourcesFetcher == nil` returns `[embedded]`, not 503. `TestListCatalogSources_EmbeddedOnly_NoFetcher` asserts this is the normal embedded-only mode.
6. **Swagger resolves** — `catalogSourceRecord` lives in the same package as the handler; `swag init` picked it up as `internal_api.catalogSourceRecord` in the generated spec (verified in `docs/swagger/swagger.json`).

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean |
| Vet | `go vet ./...` | clean |
| Targeted unit tests (race) | `go test ./internal/api/... -race -count=1` | PASS (~6s) |
| Broader sweep (race) | `go test ./internal/api/... ./internal/catalog/... -race -count=1` | 3 packages PASS |
| Lint | `golangci-lint run ./internal/api/...` | skipped (not installed locally; CI will run) |
| Swagger regen | `swag init …` | new endpoint + `catalogSourceRecord` schema present in `docs/swagger/{docs.go,swagger.json,swagger.yaml}` |

## Deviations from the brief

None. The handler, response schema, router registration, and test list match the brief 1:1.
