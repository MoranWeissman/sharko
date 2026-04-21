---
story_key: V123-1-4-source-attribution-on-in-memory-entries
epic: V123-1 (Third-party private catalogs)
status: review
effort: S
dispatched: 2026-04-21
merged: TBD
---

# Story V123-1.4 — Source attribution on in-memory entries

## Brief (from epics-v1.23.md §V123-1.4)

As an **API consumer**, I want every catalog entry to carry its source, so that
the UI can render a source badge and the audit trail is clear.

## Acceptance criteria

**Given** the merged index
**When** an entry is read
**Then** it exposes a `source` field set to `embedded` or the full third-party URL.

**Given** the `GET /api/v1/catalog/addons/<name>` response
**When** rendered
**Then** the response JSON includes `source: "embedded"` or `source: "https://..."`.

**Given** the catalog schema struct
**When** examined
**Then** `source` is a computed field (not persisted in YAML — derived from
which snapshot contributed the entry).

## Implementation plan

### 1. Add `Source` field to `CatalogEntry`

File: `internal/catalog/loader.go` — `type CatalogEntry struct`.

```go
// Source is the origin of the entry — "embedded" for binaries-shipped,
// or the full third-party catalog URL (from SHARKO_CATALOG_URLS).
// Computed — NOT persisted in YAML or any on-disk artifact (NFR §2.7).
Source string `yaml:"-" json:"source,omitempty"`
```

- `yaml:"-"` → loader does not read it from YAML. A forged `source:` field in
  a third-party YAML MUST NOT poison the attribution.
- `json:"source,omitempty"` → appears in API responses; stays absent when
  zero (backwards compat for anyone consuming the struct pre-v1.23).
- Set to `"embedded"` sentinel inside `Load()` / `LoadBytes()` for every entry
  loaded from the embedded YAML.

### 2. Wire merge into server-side accessor

Add a thin method on `*Server` (or a pure helper taking `*Catalog` + `*Fetcher`)
that returns a merged `[]CatalogEntry` with `Source` populated:

```go
// mergedCatalogEntries returns the effective catalog view: embedded entries
// + third-party snapshot entries (via sources.Merge), with Source populated.
// Safe to call when s.sourcesFetcher is nil (embedded-only mode).
func (s *Server) mergedCatalogEntries() []catalog.CatalogEntry
```

Logic:
- If `s.catalog == nil` → return nil (let caller 503).
- embedded := `s.catalog.Entries()` (already has `Source="embedded"` from Load).
- If `s.sourcesFetcher == nil` → return embedded.
- snapshots := `s.sourcesFetcher.Snapshots()` → flatten to `[]*SourceSnapshot`.
- merged := `sources.Merge(embedded, snapshots)`.
- Flatten `merged.Entries` into `[]catalog.CatalogEntry`, copying the embedded
  `catalog.CatalogEntry` and setting `Source = merged.Entries[i].Origin`.
- Discard `merged.Conflicts` here (that's V123-1.5's `/catalog/sources` job).

### 3. Update catalog API handlers

File: `internal/api/catalog.go`.

Two handlers need the merged view:
- `handleListCatalogAddons` (GET `/catalog/addons`)
- `handleGetCatalogAddon` (GET `/catalog/addons/{name}`)

Find each call that reads `s.catalog.Entries()` or `s.catalog.Get(name)` and
replace with a lookup against the merged list returned by
`s.mergedCatalogEntries()`.

For `handleGetCatalogAddon`:
- Build the merged list, linear-scan by name (it's small — embedded 60 + any
  third-party). If profiling ever shows this matters, a helper that builds a
  `map[string]CatalogEntry` cache is trivial.
- Return 404 if not found (preserve current behavior).

### 4. Third-party fetcher must set Source on each entry at fetch time

When the fetcher parses a snapshot's YAML, the resulting `[]catalog.CatalogEntry`
has `Source = ""` (no YAML key exists for it, and `yaml:"-"` would drop it
anyway). The merger's `Origin` field is authoritative. No fetcher change needed
— the merged-entry's Origin replaces any stray Source at flatten time.

**Invariant:** after flatten, every returned entry has `Source` set to either
`"embedded"` or a non-empty third-party URL. No empty-string Sources in the
API response.

### 5. Swagger regen (MANDATORY)

After the handler changes are in, run:

```bash
swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal
```

Commit the resulting `docs/swagger/*` changes.

## Test plan

### Unit — loader
- `loader_test.go`: extend existing load-success test to assert every returned
  entry has `Source == "embedded"`.

### Unit — server helper
- New test: `catalog_merge_test.go` (in `internal/api/`) — or inline in an
  existing api test file. Cases:
  1. `catalog=nil` → returns nil.
  2. `catalog set, fetcher=nil` → returns embedded entries, all with Source=embedded.
  3. `catalog + fetcher with non-overlapping snapshot` → embedded entries keep
     Source=embedded, third-party entries have Source=<URL>.
  4. `catalog + fetcher with overlapping entry name` → the one in the result
     is the embedded one, with Source=embedded.

### Handler
- `TestHandleListCatalogAddons_SourceField` — JSON response has `source` key
  on every entry (embedded-only case).
- `TestHandleGetCatalogAddon_SourceFieldEmbedded` — 200 + `source: "embedded"`.
- (Third-party handler assertions land in V123-1.9 once integration test
  infra is in place; this story only validates the embedded case + the helper
  in isolation.)

### Quality gates
- `go build ./...`
- `go vet ./...`
- `go test ./internal/catalog/... ./internal/api/... -race -count=1`
- `golangci-lint run ./internal/catalog/... ./internal/api/...` (if available)
- `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` → diff must be committed.

## Explicit non-goals

- `GET /api/v1/catalog/sources` endpoint — that is V123-1.5.
- Force-refresh endpoint — V123-1.6.
- UI badge — V123-1.7.
- Settings view — V123-1.8.
- Integration tests with a real HTTP fixture server — V123-1.9.

## Dependencies

- V123-1.3 merger — done ✅.
- V123-1.2 fetcher `Snapshots()` — done ✅.

## Gotchas

1. **`yaml:"-"` is mandatory.** Without it, a malicious third-party YAML
   could set `source: embedded` and masquerade as curated. The `yaml:"-"` tag
   makes it impossible to deserialise from YAML at all.
2. **Do not log the Source URL** when writing diagnostic traces. Third-party
   URLs may carry auth tokens in the path. Use the existing fetcher fingerprint
   convention (`source_fp`) if any log line is needed.
3. **Nil-safety.** `s.catalog == nil` → 503 (existing behavior). `s.sourcesFetcher == nil`
   is a normal embedded-only deployment — do NOT 503 for that. Return embedded only.
4. **Handler test helpers.** If the existing api-test harness has a "set up a
   Server with a loaded catalog" helper, reuse it. Do not duplicate test setup.
5. **Swagger.** If `swag` is not installed locally, CI's "Swagger Up To Date"
   check will fail. The dev branch must regen + commit the swagger diff.
6. **Determinism in handler tests.** Assert that the `source` key is present
   on every entry (structural assertion), not that the list happens to be in a
   particular order — the merger sorts by Name, but better not to couple tests
   to ordering.

## Role files (MUST embed in dispatch)

- `.claude/team/go-expert.md` — handler wiring + pure-function helper.
- `.claude/team/docs-writer.md` — swagger regen pattern.
- `.claude/team/test-engineer.md` — unit + handler test patterns.

## PR plan

- Branch: `dev/v1.23-source-attribution` off current `main` (post-V123-1.3).
- Commits (suggested split):
  1. `feat(catalog): add Source field to CatalogEntry, set embedded sentinel at Load`
  2. `feat(api): merge third-party snapshots into catalog handlers (V123-1.4)`
  3. `docs(swagger): regen for source field on catalog entries`
- No tag (pre-release cadence).

## Next story after this

V123-1.5 — `GET /api/v1/catalog/sources` endpoint + swagger (reads
`fetcher.Snapshots()` + surfaces conflict diagnostics from V123-1.3 merger).

## Tasks completed

- [x] Added `Source string \`yaml:"-" json:"source,omitempty"\`` field on `catalog.CatalogEntry` + `SourceEmbedded = "embedded"` constant.
- [x] `Load` / `LoadBytes` set `e.Source = SourceEmbedded` after validation — on every loaded entry.
- [x] Extracted `catalog.ListFrom(entries []CatalogEntry, q Query) []CatalogEntry` as the pure version of `(*Catalog).List`; `List` is now a thin wrapper calling `ListFrom`.
- [x] New `internal/api/catalog_merge.go` — `(*Server).mergedCatalogEntries()` helper that returns embedded-only when no fetcher is wired, and embedded + third-party via `sources.Merge` when one is. Copies the embedded `CatalogEntry`, overwrites Source with the merger's Origin.
- [x] `handleListCatalogAddons` now calls `catalog.ListFrom(s.mergedCatalogEntries(), q)` instead of `s.catalog.List(q)`.
- [x] `handleGetCatalogAddon` now linear-scans the merged view for the requested name, preserving 404-on-unknown and 503-on-nil-catalog behaviour.
- [x] Added `(*Fetcher).SetSnapshotsForTest` in `internal/catalog/sources/fetcher.go` so the new api tests can inject fake snapshots without running an HTTP fetch loop.
- [x] Extended `TestLoad_Embedded` to assert every loaded entry has `Source == SourceEmbedded`.
- [x] Added `TestLoadBytes_SourceAlwaysEmbedded_IgnoresYAMLForgery` — verifies `yaml:"-"` on Source blocks a forged `source: <url>` in YAML.
- [x] Added `TestListFrom_PureFilter` in `search_test.go` — exercises filters, Source round-trip, nil-input, and deprecated flag on a synthetic slice.
- [x] Added `internal/api/catalog_merge_test.go` — 4 cases: nil catalog, embedded-only, disjoint third-party, overlapping-name embedded-wins, plus a 5th stale-snapshot case.
- [x] Added `TestHandleListCatalogAddons_SourceField` + `TestHandleGetCatalogAddon_SourceFieldEmbedded` — JSON structural assertions that `source` is present + equal to `"embedded"` for curated entries.
- [x] Swagger regen: `swag init -g cmd/sharko/serve.go -o docs/swagger --parseDependency --parseInternal` — `source` field added to `catalog.CatalogEntry` definition in both `swagger.json` and `swagger.yaml`.
- [x] `go build ./...` clean.
- [x] `go vet ./...` clean.
- [x] `go test ./internal/catalog/... ./internal/api/... -race -count=1` — all pass.
- [x] `golangci-lint` — not installed locally; skipped per brief (CI runs it).

## Files touched

- `internal/catalog/loader.go` — `Source` field + `SourceEmbedded` const + `Source` set in `LoadBytes`.
- `internal/catalog/loader_test.go` — extended `TestLoad_Embedded`; added forgery-resistance test.
- `internal/catalog/search.go` — extracted `ListFrom` from `(*Catalog).List`.
- `internal/catalog/search_test.go` — new `TestListFrom_PureFilter`.
- `internal/catalog/sources/fetcher.go` — test-only `SetSnapshotsForTest` helper.
- `internal/api/catalog.go` — both handlers routed through `s.mergedCatalogEntries()`.
- `internal/api/catalog_merge.go` (NEW) — `mergedCatalogEntries` helper.
- `internal/api/catalog_merge_test.go` (NEW) — 5 cases for the helper.
- `internal/api/catalog_test.go` — 2 new JSON-level source-field tests.
- `docs/swagger/docs.go`, `docs/swagger/swagger.json`, `docs/swagger/swagger.yaml` — regen with new `source` field.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — `V123-1-4` backlog → review, `last_updated` refreshed.

## Tests

Targeted test run (race-enabled):

```bash
go test ./internal/catalog/... ./internal/api/... -race -count=1
# ok   github.com/MoranWeissman/sharko/internal/catalog         3.4s
# ok   github.com/MoranWeissman/sharko/internal/catalog/sources 4.9s
# ok   github.com/MoranWeissman/sharko/internal/api             6.1s
```

New tests (8 total):

1. `TestLoad_Embedded` (extended) — every embedded entry carries `Source="embedded"`.
2. `TestLoadBytes_SourceAlwaysEmbedded_IgnoresYAMLForgery` — `yaml:"-"` blocks malicious `source: …` in a hostile feed.
3. `TestListFrom_PureFilter` — pure filter function on a hand-built slice with mixed Source values.
4. `TestMergedCatalogEntries_NilCatalog` — nil catalog → nil return.
5. `TestMergedCatalogEntries_EmbeddedOnly` — no fetcher → embedded entries with `Source="embedded"`.
6. `TestMergedCatalogEntries_DisjointThirdParty` — 2 embedded + 1 third-party; third-party carries snapshot URL, embedded keeps `"embedded"`, upstream's self-declared Source is ignored.
7. `TestMergedCatalogEntries_OverlappingNameEmbeddedWins` — collision on `cert-manager` name, embedded wins, hostile fields dropped.
8. `TestMergedCatalogEntries_StaleSnapshotIgnored` — `StatusStale` snapshot entries do not leak into the merged view.
9. `TestHandleListCatalogAddons_SourceField` — every addon in list JSON has `source: "embedded"`.
10. `TestHandleGetCatalogAddon_SourceFieldEmbedded` — detail response has `source: "embedded"`.

All existing tests in `internal/catalog/…` and `internal/api/…` continue to pass — no behaviour changes to embedded-only deployments.

## Decisions

- **`SourceEmbedded` const lives in the catalog package, not merged into the sources package** — the merger already has `sources.OriginEmbedded` with the same string value, but the loader path (which has no dependency on the merger) needs the constant available without pulling `internal/catalog/sources` into its import graph. The two constants share the same string literal `"embedded"` to guarantee wire-compatibility; the doc comment on `SourceEmbedded` calls this out.
- **`ListFrom` returns `nil` for empty input** (not `[]CatalogEntry{}`) — matches `(*Catalog).List` behaviour when the receiver is nil, and keeps callers that check `if got == nil` working without churn.
- **Helper overwrites whatever Source the upstream declared**, never trusts it — the test `TestMergedCatalogEntries_DisjointThirdParty` sets `Source: "malicious-upstream-declaration"` on the snapshot entry and asserts it gets replaced with the real URL. The merger's Origin is the authoritative source of truth; the helper's only job is to plumb Origin back onto CatalogEntry.Source.
- **Linear scan in `handleGetCatalogAddon`** rather than building a map — the embedded catalog is ~60 entries and third-party feeds are expected to stay small, so an O(n) scan per request is not a measurable cost. Adding a cache would add invalidation complexity (snapshots refresh every N minutes); if profiling ever shows this matters, the change is trivial.
- **`SetSnapshotsForTest` added to `sources.Fetcher`** rather than an interface extraction on the Server — the brief offered either path. A one-line test setter is much smaller than introducing an abstraction layer + retrofitting every caller; the setter is explicitly `_ForTest` to keep the test-only contract obvious.
- **No force-refresh, no `/catalog/sources` endpoint, no UI badge** — these are V123-1.5/1.6/1.7/1.9. This story is strictly the data plumbing for `source` on the existing handlers.
- **No cosign / signature surface** — V123-2 owns that. V123-1.4 is source-URL attribution only; the signed flag on snapshots stays unread here.

## Gotchas / constraints addressed

1. **`yaml:"-"` on Source** — covered by `TestLoadBytes_SourceAlwaysEmbedded_IgnoresYAMLForgery`. A hostile third-party YAML with `source: embedded` cannot forge attribution.
2. **No URL logging** — `catalog_merge.go` has no log calls; the helper is a pure data-flow bridge.
3. **Stateless** — Source is never written to disk. The loader sets it on every entry at parse time; the merger's Origin flows into it in memory only.
4. **Nil-safety** — `s.catalog == nil` returns nil (caller 503s); `s.sourcesFetcher == nil` is a normal embedded-only mode and returns embedded entries directly.
5. **Determinism in handler tests** — both new JSON tests iterate the addons slice and assert on the `source` key per entry, not on ordering.

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean |
| Vet | `go vet ./...` | clean |
| Targeted unit tests (race) | `go test ./internal/catalog/... ./internal/api/... -race -count=1` | 3 packages PASS |
| Lint | `golangci-lint run …` | skipped (not installed locally; CI will run) |
| Swagger regen | `swag init …` | `source` field surfaced on `catalog.CatalogEntry` definition (17 LOC diff across `docs/swagger/*`) |

## Deviations from the brief

None. The brief's test #2 recipe (inspect the fetcher for how tests construct a Fetcher) led to adding `SetSnapshotsForTest` on `*Fetcher` — the brief explicitly called this out as one of the acceptable paths ("expose the existing type with a test-only constructor").
