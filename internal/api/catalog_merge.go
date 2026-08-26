package api

import (
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// mergedCatalogEntries returns the effective catalog view used by the
// GET /catalog/addons* handlers: embedded entries plus third-party snapshot
// entries (via sources.Merge), with catalog.CatalogEntry.Source populated
// for every entry — "embedded", or the fixed word "redacted" for every
// entry that came from a configured third-party source.
//
// Behaviour:
//   - s.catalog == nil → returns nil (caller should 503).
//   - s.sourcesFetcher == nil → returns embedded-only; entries already carry
//     Source="embedded" from the loader.
//   - s.catalog set + fetcher set → merges via sources.Merge (embedded-wins
//     on name collision, alphabetical-URL tiebreak for third-party-vs-
//     third-party); flattens the result back into []catalog.CatalogEntry.
//
// The helper is deliberately tiny and side-effect-free — no logging, no
// metrics. It is a pure data-flow bridge between the merger and the
// handler layer, kept here (not in internal/catalog/sources) because it
// depends on *Server. The merger's Origin is the exact configured
// third-party address, and entries returned here reach API responses any
// signed-in account can read — so a third-party Origin is replaced with
// the fixed word from credsafe.PublicSourceLabel before it leaves. A
// catalog source address may carry an auth token in its own path, where
// no grammar can spot it, so the address is sensitive by type and never
// leaves the process in any form.
func (s *Server) mergedCatalogEntries() []catalog.CatalogEntry {
	if s.catalog == nil {
		return nil
	}
	embedded := s.catalog.Entries()
	if s.sourcesFetcher == nil {
		return embedded
	}

	snapMap := s.sourcesFetcher.Snapshots()
	snaps := make([]*sources.SourceSnapshot, 0, len(snapMap))
	for _, snap := range snapMap {
		snaps = append(snaps, snap)
	}

	merged := sources.Merge(embedded, snaps)
	out := make([]catalog.CatalogEntry, 0, len(merged.Entries))
	for _, me := range merged.Entries {
		// Copy the CatalogEntry value, then set Source to what may be said
		// outwards. Origin is either sources.OriginEmbedded ("embedded",
		// which keeps its name — it is a literal, not a configured
		// address) or the exact third-party URL, which must not leave the
		// process — every third-party entry carries the fixed word
		// instead. This also means upstream YAML cannot smuggle its own
		// Source value through (the overwrite is unconditional).
		e := me.CatalogEntry
		if me.Origin == sources.OriginEmbedded {
			e.Source = me.Origin
		} else {
			e.Source = credsafe.PublicSourceLabel()
		}
		out = append(out, e)
	}
	return out
}

// mergedCatalogGet is the single-name lookup analogue of mergedCatalogEntries.
// Handlers that previously called s.catalog.Get(name) — which only sees
// embedded entries — now use this helper to also resolve third-party snapshot
// entries served via sources.Fetcher.
//
// Behaviour:
//   - s.catalog == nil → returns (zero, false). Callers should already be
//     short-circuiting with 503 in that case; the contract here matches.
//   - s.sourcesFetcher == nil → falls back to s.catalog.Get(name) (the
//     embedded-only fast path) so handlers behave identically to the
//     pre-merge world when no third-party sources are configured.
//   - both wired → linear scan over mergedCatalogEntries(), embedded-wins
//     guarantee inherited from the underlying merger (sources.Merge).
//
// Linear scan is intentional: catalog sizes are tens-to-low-hundreds of
// entries, so building a map index per call would be premature optimization
// and a refresh-vs-read invalidation hazard. If catalogs grow into the
// thousands we revisit; today the simple loop is the right call.
//
// The returned CatalogEntry value is a copy (CatalogEntry is a value type),
// so callers may freely read or copy it — no aliasing back to the snapshot.
func (s *Server) mergedCatalogGet(name string) (catalog.CatalogEntry, bool) {
	if s.catalog == nil {
		return catalog.CatalogEntry{}, false
	}
	// Fast path: no third-party fetcher → embedded lookup is exact.
	if s.sourcesFetcher == nil {
		return s.catalog.Get(name)
	}
	for _, e := range s.mergedCatalogEntries() {
		if e.Name == name {
			return e, true
		}
	}
	return catalog.CatalogEntry{}, false
}
