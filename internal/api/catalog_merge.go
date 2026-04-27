package api

import (
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// mergedCatalogEntries returns the effective catalog view used by the
// GET /catalog/addons* handlers: embedded entries plus third-party snapshot
// entries (via sources.Merge), with catalog.CatalogEntry.Source populated
// for every entry ("embedded" or the full source URL).
//
// Behaviour:
//   - s.catalog == nil → returns nil (caller should 503).
//   - s.sourcesFetcher == nil → returns embedded-only; entries already carry
//     Source="embedded" from the loader.
//   - s.catalog set + fetcher set → merges via sources.Merge (embedded-wins
//     on name collision, alphabetical-URL tiebreak for third-party-vs-
//     third-party); flattens the result back into []catalog.CatalogEntry
//     with Source populated to the merged Origin.
//
// The helper is deliberately tiny and side-effect-free — no logging, no
// metrics. It is a pure data-flow bridge between the merger and the
// handler layer, kept here (not in internal/catalog/sources) because it
// depends on *Server. Never log the third-party Source URL — paths may
// encode auth tokens (see fetcher.go urlFingerprint rationale).
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
		// Copy the embedded CatalogEntry value, then overwrite Source with
		// the merger's Origin. Origin is either sources.OriginEmbedded
		// ("embedded") or the exact third-party URL.
		e := me.CatalogEntry
		e.Source = me.Origin
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
