package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog/sources"
	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// catalogSourceRecord is one row of the GET /catalog/sources response
// (design §6.8). The embedded pseudo-source always uses the literal string
// "embedded" (never a file path).
//
// # Why the url field never carries the configured address, not even a clean one
//
// An earlier version returned the address whenever the URL grammar could
// vouch it carried no credential. That allowance was withdrawn: the
// documented private-catalog shape hides the token in the address's own
// path — /private/<token>/catalog.yaml — and no grammar can tell that
// apart from an ordinary path. An address that LOOKS clean can still be
// the key to someone's private catalog. And the caller here is not the
// admin who configured SHARKO_CATALOG_URLS: any signed-in account can
// read this page, down to a viewer.
//
// So the configured address is treated as sensitive because of what it
// IS. Every third-party row's url is the fixed word "redacted" — never
// the address, and never anything computed from it (no hash, no length,
// no partial). The known cost: an operator with three sources configured
// sees three rows that all say the same word and tells them apart only
// by status, entry count and last-fetched time.
type catalogSourceRecord struct {
	// URL is either the literal string "embedded" (for the binary-shipped
	// curated catalog) or the fixed word "redacted" for every configured
	// third-party source. The configured address never appears here in
	// any form.
	URL string `json:"url"`

	// Status is "ok", "stale", or "failed" — mirrors
	// sources.SourceStatus. For the embedded row it is always "ok".
	Status string `json:"status"`

	// LastFetched is the RFC3339 timestamp of the most recent successful
	// fetch, or null when the source has never succeeded since process
	// start. Always null for the embedded row (never fetched).
	LastFetched *time.Time `json:"last_fetched"`

	// EntryCount is the number of catalog entries contributed by this
	// source — s.catalog.Len() for embedded, len(snap.Entries) for
	// third-party. A failed third-party source with no prior success
	// reports 0.
	EntryCount int `json:"entry_count"`

	// Verified reports whether the sidecar signature was validated
	// against the trust policy. True for the embedded row ("the binary
	// trusts its own bundled catalog") — not a cosign statement.
	Verified bool `json:"verified"`

	// Issuer is the human-readable OIDC subject of the signer, present
	// only when Verified is true. Omitted from the JSON when empty.
	Issuer string `json:"issuer,omitempty"`
}

// handleListCatalogSources godoc
//
// @Summary List catalog sources with fetch status
// @Description Returns one record per catalog source — the embedded binary catalog (url="embedded", always first) plus one row per configured third-party source from SHARKO_CATALOG_URLS. A third-party row's url is always the fixed word "redacted" — the configured address is never returned, because the documented private-catalog form carries an auth token inside the address itself. Per-source fields: url, status (ok|stale|failed), last_fetched (RFC3339 or null), entry_count, verified (cosign-verified), and optional issuer when verified. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {array} catalogSourceRecord "Catalog sources with per-source fetch status"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /marketplace/sources [get]
func (s *Server) handleListCatalogSources(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}

	writeJSON(w, http.StatusOK, s.buildCatalogSourcesResponse())
}

// buildCatalogSourcesResponse assembles the []catalogSourceRecord used
// by both GET /catalog/sources and POST /catalog/sources/refresh. The
// embedded pseudo-source is always first and always "ok" — the binary
// trusts its own bundled catalog. Third-party rows appear only when a
// fetcher is wired in; in embedded-only deployments s.sourcesFetcher is
// nil and the result is a single-element slice.
//
// Callers must have already verified s.catalog != nil — the helper does
// not 503 on its own because the two call sites differ on exactly which
// prerequisites constitute a 503 vs a 200 empty response.
func (s *Server) buildCatalogSourcesResponse() []catalogSourceRecord {
	records := []catalogSourceRecord{
		{
			URL:         "embedded",
			Status:      "ok",
			LastFetched: nil,
			EntryCount:  s.catalog.Len(),
			Verified:    true,
		},
	}
	if s.sourcesFetcher != nil {
		snapMap := s.sourcesFetcher.Snapshots()
		snaps := make([]*sources.SourceSnapshot, 0, len(snapMap))
		for _, snap := range snapMap {
			snaps = append(snaps, snap)
		}
		// Deterministic order, decided BEFORE the projection to the wire.
		// Every third-party row's url on the wire is the same fixed word,
		// so sorting the wire rows would compare equal keys and the order
		// would follow Go's randomised map iteration — two identical
		// requests could return different bodies. The configured address
		// is allowed to exist here, inside the process, so it is the sort
		// key; only the projected rows leave.
		sort.Slice(snaps, func(i, j int) bool { return snaps[i].URL < snaps[j].URL })
		for _, snap := range snaps {
			records = append(records, recordFromSnapshot(snap))
		}
	}
	return records
}

// recordFromSnapshot projects a fetcher SourceSnapshot onto the wire
// representation. The nil-pointer on LastFetched when LastSuccessAt is
// the zero time is what makes JSON emit `"last_fetched": null` cleanly
// (instead of `"0001-01-01T00:00:00Z"`).
func recordFromSnapshot(snap *sources.SourceSnapshot) catalogSourceRecord {
	rec := catalogSourceRecord{
		// snap.URL is the configured source address, and the env var that
		// sets it is documented as being for tokened/private URLs — the
		// token can sit in the address's own path, where no grammar can
		// spot it. So the address never reaches the wire in any form; the
		// row carries the fixed word instead.
		URL:        credsafe.PublicSourceLabel(),
		Status:     string(snap.Status),
		EntryCount: len(snap.Entries),
		Verified:   snap.Verified,
		Issuer:     snap.Issuer,
	}
	if !snap.LastSuccessAt.IsZero() {
		t := snap.LastSuccessAt
		rec.LastFetched = &t
	}
	return rec
}
