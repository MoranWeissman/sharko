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
