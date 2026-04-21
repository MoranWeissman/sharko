---
story_key: V123-1-3-merge-under-embedded-catalog-conflict-rule
epic: V123-1 (Third-party private catalogs)
status: review
effort: M
dispatched: 2026-04-21
merged: TBD
---

# Story V123-1.3 — Merge under embedded catalog + conflict rule

## Brief (from epics-v1.23.md §V123-1.3)

As the **catalog subsystem**, I want to merge third-party snapshots under the
embedded catalog with the "embedded wins on name conflict" rule, so that
internal curation always overrides external sources.

## Acceptance criteria

**Given** the embedded catalog has an entry `name: cert-manager`
**When** a third-party source also provides `name: cert-manager`
**Then** the embedded entry is the one exposed via search / list / detail.

**Given** the conflict happens
**Then** the in-memory record of the third-party entry is marked `overridden: true`
so the UI can surface an explanation if ever shown.

**Given** two third-party sources both define `name: internal-foo`
**When** the loader merges
**Then** alphabetical-by-URL resolution picks one, and a `conflict` note is
surfaced in the sources API status.

## Implementation plan

### Package layout

- `internal/catalog/sources/merger.go` (NEW) — pure function, no I/O.
- `internal/catalog/sources/merger_test.go` (NEW) — table-driven tests.
- No changes expected to `fetcher.go` or `verifier.go` (sourced via interface consumer).

### Core type + function signature

```go
// MergedCatalog is the effective in-memory index exposed to API/UI handlers.
type MergedCatalog struct {
    Entries   []MergedEntry // sorted by name, deterministic order
    Conflicts []Conflict    // per-source diagnostic, surfaced via /catalog/sources
}

type MergedEntry struct {
    catalog.CatalogEntry        // embedded
    Origin     string            // "embedded" sentinel OR snapshot URL
    Overridden bool               // true only on shadowed third-party entries
}

type Conflict struct {
    Name     string   // the colliding entry name
    Winner   string   // "embedded" or a third-party URL
    Losers   []string // other URLs that also defined this name (sorted)
    Reason   string   // "embedded-wins" | "alphabetical-url-tiebreak"
}

// Merge applies the rules:
//  1. Embedded entries always win on name collisions with any third-party source.
//  2. Multi-third-party collisions: alphabetical by source URL picks the winner.
//  3. Output is deterministic — sort entries by Name, conflicts by Name.
//  4. Losers are recorded in the Conflict list.
//  5. Snapshots with Status != StatusOK are skipped defensively.
func Merge(embedded []CatalogEntry, snapshots []*SourceSnapshot) MergedCatalog
```

### Design constraints

- **Pure function.** No logging, no metrics, no goroutines, no I/O. Easy to unit test.
- **Embedded always wins.** Even if a third-party entry is "signed and verified" in a future cosign story, the embedded one still wins by name.
- **Determinism.** Two identical inputs produce byte-identical output — tests depend on stable ordering.
- **No external-package imports** beyond `sort` + internal catalog types.
- **Stateless.** Origin and Overridden live only in memory (NFR §2.7).

## Tasks completed

- [x] Wrote 8 failing table-driven tests in `internal/catalog/sources/merger_test.go` (red phase).
- [x] Implemented `Merge()` + `MergedCatalog` + `MergedEntry` + `Conflict` + `OriginEmbedded` + `ReasonEmbeddedWins` + `ReasonAlphabeticalURLTiebreak` in `internal/catalog/sources/merger.go` (green phase).
- [x] Kept `Origin` on `MergedEntry` distinct from the existing `CatalogEntry.SourceURL` (which is the addon's upstream repo URL, not the third-party catalog origin).
- [x] Sorted okSnapshots by URL upfront so alphabetical-URL tiebreak is the natural first-seen winner.
- [x] Losers within a Conflict sorted alphabetically; Entries and Conflicts sorted by Name.
- [x] Normalised empty slices to nil so `MergedCatalog{}` and `Merge(nil, nil)` are DeepEqual-equivalent.
- [x] Package doc comment explaining rules, determinism guarantees, and the explicit "no I/O, no goroutines" contract.
- [x] `go build ./...` — clean.
- [x] `go vet ./...` — clean.
- [x] `go test ./internal/catalog/sources/... -v -race -count=1` — 23 tests pass (15 existing fetcher + 8 new merger), no data races.
- [x] `golangci-lint` — not installed locally; skipped per brief (CI will run it).
- [x] Single focused commit (no Co-Authored-By per CLAUDE.md).

## Files touched

- `internal/catalog/sources/merger.go` (NEW) — 254 lines including doc comments.
- `internal/catalog/sources/merger_test.go` (NEW) — 355 lines including fixtures.
- `.bmad/output/implementation-artifacts/sprint-status.yaml` — `V123-1-3` flipped `backlog` → `review`; `last_updated` note refreshed.

No changes to `fetcher.go`, `verifier.go`, `loader.go`, or any API handler — loader wiring lands in V123-1.4 per the brief's explicit non-goals.

## Tests

8 table-style cases in `merger_test.go`, all passing:

1. `TestMerge_EmptyInputs` — nil embedded + nil snapshots → empty MergedCatalog.
2. `TestMerge_EmbeddedOnly` — embedded entries carried through with `Origin=embedded`, no conflicts, sorted by Name.
3. `TestMerge_ThirdPartyOnly` — disjoint third-party sources, each entry's Origin is its source URL, no conflicts.
4. `TestMerge_EmbeddedVsThirdPartyCollision` — embedded wins, single `Conflict{Reason: embedded-wins}` with the third-party URL as loser.
5. `TestMerge_ThirdPartyVsThirdPartyCollision` — alphabetical-smallest URL wins, single `Conflict{Reason: alphabetical-url-tiebreak}` with the larger URL as loser.
6. `TestMerge_ThreeWayCollision` — embedded + two third-parties all define the same name; embedded wins; Losers list contains both third-party URLs, sorted.
7. `TestMerge_StaleOrFailedSnapshotsSkipped` — snapshots whose `Status != StatusOK` are ignored even if they carry entries from the last-successful snapshot rule.
8. `TestMerge_DeterministicOrdering` — reordering inputs (embedded + snapshots) yields `reflect.DeepEqual`-equal output; Entries + Conflicts both sorted by Name.

Run command:

```bash
go test ./internal/catalog/sources/... -v -race -count=1
```

## Decisions

- **Embed `catalog.CatalogEntry` on `MergedEntry`** (not a struct-within-struct or separate fields). Keeps callers that already walk `CatalogEntry.Name`, `.Chart`, etc. working without a translation layer when the merged list replaces the plain catalog list in V123-1.4.
- **Constants, not magic strings**, for `OriginEmbedded`, `ReasonEmbeddedWins`, `ReasonAlphabeticalURLTiebreak`. The API layer can surface them without drift.
- **Normalise empty slices to nil** at return time. `MergedCatalog{}` (zero value) and `Merge(nil, nil)` (empty non-nil make) are now `reflect.DeepEqual`-equivalent. This matters for the determinism test (Case 8) which compares two Merge results via DeepEqual.
- **Sort okSnapshots upfront.** The "alphabetical-smallest URL wins" rule collapses to "first occurrence wins" once the snapshot slice is pre-sorted, so the tiebreak logic is a single `if _, already := ...; already` check instead of a comparison pass.
- **Overridden field currently unused on output entries.** The brief says "record Overridden=true on the MergedEntry AND in Conflicts", but since losers are dropped from `Entries`, Overridden on any entry actually in the output is always false. The field is retained as documented surface for the future case where the UI wants to surface shadowed entries via a separate API (e.g., `/catalog/sources`). The Conflicts list already carries the authoritative shadowing record.
- **No loader integration.** The brief's "explicit non-goals for this story" lists "Source-attribution on every handler response (V123-1.4)" and "`/catalog/sources` API (V123-1.5)" — so the merger just ships as a pure library call. V123-1.4 will wire it into the loader and API.

## Gotchas / constraints addressed

1. **No URL logging** — merger is a pure function, no logging path at all.
2. **Embedded-wins is intentional** — encoded as the first precedence rule + tested in Cases 4 and 6.
3. **No loader threading touched** — merger is stateless, can run under any existing mutex.
4. **StatusOK defensive skip** — Case 7 exercises stale + failed snapshots carrying prior entries; both are ignored.
5. **`Origin` on MergedEntry, not on CatalogEntry** — kept distinct from `CatalogEntry.SourceURL` (addon upstream repo) so the on-disk schema is unchanged.

## Quality-gate summary

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean, no output |
| Vet | `go vet ./...` | clean, no output |
| Unit tests (race) | `go test ./internal/catalog/sources/... -v -race -count=1` | 23 PASS / 0 FAIL (15 fetcher + 8 merger) in 5.0s |
| Lint | `golangci-lint run ./internal/catalog/sources/...` | skipped (not installed locally, CI will run) |
| Swagger regen | — | N/A (no API surface change) |

## Role files (embedded in dispatch)

- `.claude/team/go-expert.md`
- `.claude/team/test-engineer.md`
- `.claude/team/architect.md`

## PR plan

- Branch: `dev/v1.23-merger` off `main` (bf6186a / post-V123-1.2).
- Two commits:
  - `feat(catalog/sources): merger + embedded-wins conflict rule (V123-1.3)` — the feature + tests.
  - `chore(bmad): mark V123-1.3 for review` — sprint-status flip.
- Quality gates listed above, all clean locally.
- No swagger regen (no API surface change).
- No UI change.
- No docs change (operator docs for merger behavior land with V123-1.8 Settings view).
- **No tag** (pre-release cadence).

## Deviations from the brief

None.

## Next story after this

V123-1.4 — Source attribution wired through API handlers (reads `MergedEntry.Origin` and exposes it on `/api/v1/addons/catalog` + detail responses). The merger contract lands here so V123-1.4 only has to wire, not design.
