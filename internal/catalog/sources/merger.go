// Merger — pure function that overlays third-party source snapshots
// under the embedded catalog using the "embedded wins on name
// conflict" rule (design §2.7 + §3.2 of docs/design/2026-04-20-v1.23-
// catalog-extensibility.md).
//
// The merger is deliberately tiny and side-effect-free:
//
//   - No I/O, no logging, no metrics, no goroutines — a pure function.
//   - No dependency beyond `sort` + `internal/catalog` + the local
//     SourceSnapshot / SourceStatus types defined in fetcher.go.
//   - No state persisted on disk (stateless NFR §2.7). The Origin and
//     Overridden fields on MergedEntry live only in memory.
//
// Conflict rules, in order of precedence:
//
//  1. Embedded always wins on a name collision with any third-party
//     source. Reason recorded as ReasonEmbeddedWins.
//  2. Between two or more third-party sources that define the same
//     name, the alphabetically-smallest source URL wins. Reason
//     recorded as ReasonAlphabeticalURLTiebreak.
//  3. Defensive skip: snapshots whose Status is not StatusOK are
//     ignored — even if they carry retained prior entries (see
//     V123-1.2 §AC #3). The merger exposes only fresh data.
//
// Determinism: input order never affects output. Entries and
// Conflicts are both sorted by Name; Losers within a Conflict are
// sorted alphabetically by URL. Two calls with identical semantic
// inputs return reflect.DeepEqual-equal values.
package sources

import (
	"sort"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// OriginEmbedded is the sentinel Origin value for entries that came
// from the embedded catalog. Third-party entries carry the source
// URL (exact string from CatalogSource.URL) as their Origin.
const OriginEmbedded = "embedded"

// Conflict reason constants. Kept as typed constants so the API layer
// can surface them without magic-string drift.
const (
	// ReasonEmbeddedWins — an embedded entry shadowed one or more
	// third-party entries with the same Name.
	ReasonEmbeddedWins = "embedded-wins"

	// ReasonAlphabeticalURLTiebreak — two or more third-party sources
	// defined the same Name; the alphabetically-smallest source URL
	// won.
	ReasonAlphabeticalURLTiebreak = "alphabetical-url-tiebreak"
)

// MergedCatalog is the effective in-memory index the API/UI handlers
// read from. Entries is the sorted, de-duplicated list exposed to
// callers; Conflicts is a diagnostic list surfaced on the future
// /catalog/sources endpoint (V123-1.5).
type MergedCatalog struct {
	// Entries are sorted by Name, deterministic.
	Entries []MergedEntry

	// Conflicts describe names where two or more sources collided.
	// Sorted by Name.
	Conflicts []Conflict
}

// MergedEntry wraps a CatalogEntry with provenance fields that live
// only in memory. Keeping Origin / Overridden on a separate struct
// preserves the on-disk schema — CatalogEntry is what's parsed from
// YAML, and nothing here ever round-trips back to disk (stateless
// §2.7).
//
// Note: CatalogEntry already has a SourceURL field which is the
// addon's upstream repo URL (e.g. GitHub). It is unrelated to the
// third-party catalog origin URL. That's exactly why Origin lives on
// MergedEntry instead.
type MergedEntry struct {
	// Entry is the underlying catalog entry, unchanged.
	catalog.CatalogEntry

	// Origin is either the OriginEmbedded sentinel or the exact
	// third-party source URL this entry came from.
	Origin string

	// Overridden is true only on third-party entries that lost a
	// collision. Since losers are dropped from MergedEntry.Entries,
	// this field is currently always false on entries that appear in
	// MergedCatalog.Entries — it exists for future use when / if the
	// UI wants to surface shadowed entries.
	Overridden bool
}

// Conflict records a name collision. Winner is either OriginEmbedded
// or a third-party URL; Losers is the alphabetically-sorted list of
// the other source URLs that tried to claim the same Name.
type Conflict struct {
	// Name is the colliding entry name.
	Name string

	// Winner is OriginEmbedded or the winning third-party URL.
	Winner string

	// Losers is the list of source URLs (always third-party — the
	// embedded catalog can only win, never lose) that also defined
	// this name. Sorted alphabetically.
	Losers []string

	// Reason is ReasonEmbeddedWins or ReasonAlphabeticalURLTiebreak.
	Reason string
}

// Merge overlays third-party snapshots under the embedded catalog per
// the rules documented on this package. Safe for concurrent callers
// as long as the inputs are not mutated during the call — the merger
// does not retain references to input slices in its output.
//
// The returned MergedCatalog is always non-nil; empty inputs yield a
// MergedCatalog with nil slices for Entries and Conflicts (len == 0
// on both).
func Merge(embedded []catalog.CatalogEntry, snapshots []*SourceSnapshot) MergedCatalog {
	// Step 1: index embedded entries by Name. The embedded catalog is
	// the final authority on any name collision — we record them
	// first so the third-party pass can detect shadowing cheaply.
	embeddedByName := make(map[string]catalog.CatalogEntry, len(embedded))
	for _, e := range embedded {
		embeddedByName[e.Name] = e
	}

	// Step 2: build a sorted list of usable (StatusOK) snapshots.
	// Sorting upfront means every downstream loop iterates in
	// alphabetical-URL order, which is the tiebreak rule for
	// third-party-vs-third-party collisions.
	okSnapshots := make([]*SourceSnapshot, 0, len(snapshots))
	for _, s := range snapshots {
		if s == nil {
			continue
		}
		if s.Status != StatusOK {
			continue
		}
		okSnapshots = append(okSnapshots, s)
	}
	sort.Slice(okSnapshots, func(i, j int) bool {
		return okSnapshots[i].URL < okSnapshots[j].URL
	})

	// Step 3: first pass over third-party snapshots — pick the
	// alphabetical-URL winner for each Name, record losers. Later
	// sources cannot displace an earlier winner (okSnapshots is
	// alphabetical), so the FIRST occurrence of a Name is the winner.
	//
	// thirdPartyWinners maps Name → MergedEntry (Origin set to the
	// winning URL). thirdPartyLosers maps Name → []URL.
	thirdPartyWinners := make(map[string]MergedEntry)
	thirdPartyLosers := make(map[string][]string)
	for _, s := range okSnapshots {
		for _, e := range s.Entries {
			if _, already := thirdPartyWinners[e.Name]; already {
				// A previously-seen (alphabetically-smaller) URL
				// already claimed this name → this occurrence is a
				// loser. Preserve chronological-within-sorted order
				// for determinism — okSnapshots is sorted, so
				// appending here builds the loser list in URL order.
				thirdPartyLosers[e.Name] = append(thirdPartyLosers[e.Name], s.URL)
				continue
			}
			thirdPartyWinners[e.Name] = MergedEntry{
				CatalogEntry: e,
				Origin:       s.URL,
				Overridden:   false,
			}
		}
	}

	// Step 4: build the merged entry list + conflicts. Start with
	// every embedded entry (they always win), then add third-party
	// winners that don't collide with embedded.
	entries := make([]MergedEntry, 0, len(embeddedByName)+len(thirdPartyWinners))
	conflicts := make([]Conflict, 0) // len==0 slice preferred over nil for JSON-friendly output downstream

	// Embedded entries → always in the output with OriginEmbedded.
	for _, e := range embedded {
		entries = append(entries, MergedEntry{
			CatalogEntry: e,
			Origin:       OriginEmbedded,
			Overridden:   false,
		})
	}

	// Third-party winners that are NOT shadowed by an embedded entry
	// → in the output with their source URL as Origin. Shadowed ones
	// → dropped from Entries, recorded as a Conflict with
	// ReasonEmbeddedWins.
	//
	// Also record alphabetical-tiebreak conflicts for third-party
	// names that had losers AND are not shadowed by embedded (when
	// embedded shadows the name, all third-party copies are losers
	// under the embedded-wins rule, not under the tiebreak rule).
	for _, tp := range thirdPartyWinners {
		if _, shadowed := embeddedByName[tp.Name]; shadowed {
			// Embedded-wins conflict. Losers = this third-party URL
			// + every other third-party URL that defined the same
			// name. Sort the combined list.
			losers := append([]string{tp.Origin}, thirdPartyLosers[tp.Name]...)
			sort.Strings(losers)
			conflicts = append(conflicts, Conflict{
				Name:   tp.Name,
				Winner: OriginEmbedded,
				Losers: losers,
				Reason: ReasonEmbeddedWins,
			})
			continue
		}
		// Not shadowed by embedded — third-party entry makes it into
		// the merged list.
		entries = append(entries, tp)

		if loserURLs, had := thirdPartyLosers[tp.Name]; had && len(loserURLs) > 0 {
			losers := append([]string(nil), loserURLs...)
			sort.Strings(losers)
			conflicts = append(conflicts, Conflict{
				Name:   tp.Name,
				Winner: tp.Origin,
				Losers: losers,
				Reason: ReasonAlphabeticalURLTiebreak,
			})
		}
	}

	// Step 5: deterministic sort of both output slices by Name. This
	// is the final guarantee of the merger — identical semantic inputs
	// give byte-identical output.
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	sort.Slice(conflicts, func(i, j int) bool { return conflicts[i].Name < conflicts[j].Name })

	// Normalise empty slices to nil so the zero/empty comparison
	// tests (Case 1) and reflect.DeepEqual determinism checks (Case
	// 8) both pass cleanly. An empty make() slice has a non-nil
	// underlying pointer which DeepEqual treats as NOT equal to a
	// nil slice; normalising here keeps MergedCatalog{} and
	// MergedCatalog{Entries: []MergedEntry{}} indistinguishable.
	if len(entries) == 0 {
		entries = nil
	}
	if len(conflicts) == 0 {
		conflicts = nil
	}

	return MergedCatalog{
		Entries:   entries,
		Conflicts: conflicts,
	}
}
