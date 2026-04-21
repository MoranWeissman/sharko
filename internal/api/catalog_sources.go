package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// catalogSourceRecord is one row of the GET /catalog/sources response
// (design §6.8). URLs are intentionally returned verbatim — the endpoint
// is authenticated and the caller is the same admin who configured
// SHARKO_CATALOG_URLS, so the response does not disclose anything new.
// The embedded pseudo-source always uses the literal string "embedded"
// (never a file path).
type catalogSourceRecord struct {
	// URL is either the literal string "embedded" (for the binary-shipped
	// curated catalog) or the full third-party URL from SHARKO_CATALOG_URLS.
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
	// trusts its own bundled catalog") — this is not a cosign statement
	// (V123-2.5 owns signing of the embedded catalog). False for
	// third-party until V123-2.2 wires the verifier.
	Verified bool `json:"verified"`

	// Issuer is the human-readable OIDC subject of the signer, present
	// only when Verified is true. Omitted from the JSON when empty.
	Issuer string `json:"issuer,omitempty"`
}

// handleListCatalogSources godoc
//
// @Summary List catalog sources with fetch status
// @Description Returns one record per catalog source — the embedded binary catalog (url="embedded", always first) plus each configured third-party URL from SHARKO_CATALOG_URLS. Per-source fields: url, status (ok|stale|failed), last_fetched (RFC3339 or null), entry_count, verified (cosign-verified, currently always true for embedded and false for third-party until V123-2.2 lands), and optional issuer when verified. Read-only; requires authentication.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {array} catalogSourceRecord "Catalog sources with per-source fetch status"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/sources [get]
func (s *Server) handleListCatalogSources(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}

	writeJSON(w, http.StatusOK, s.buildCatalogSourcesResponse())
}

// buildCatalogSourcesResponse assembles the []catalogSourceRecord used by
// both GET /catalog/sources (V123-1.5) and POST /catalog/sources/refresh
// (V123-1.6). The embedded pseudo-source is always first and always "ok"
// — the binary trusts its own bundled catalog. Third-party rows appear
// only when a fetcher is wired in; in embedded-only deployments
// s.sourcesFetcher is nil and the result is a single-element slice.
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
		snaps := s.sourcesFetcher.Snapshots()
		third := make([]catalogSourceRecord, 0, len(snaps))
		for _, snap := range snaps {
			third = append(third, recordFromSnapshot(snap))
		}
		// Deterministic order so tests do not flake on Go map iteration
		// order and clients can diff responses across calls.
		sort.Slice(third, func(i, j int) bool { return third[i].URL < third[j].URL })
		records = append(records, third...)
	}
	return records
}

// recordFromSnapshot projects a fetcher SourceSnapshot onto the wire
// representation. The nil-pointer on LastFetched when LastSuccessAt is
// the zero time is what makes JSON emit `"last_fetched": null` cleanly
// (instead of `"0001-01-01T00:00:00Z"`).
func recordFromSnapshot(snap *sources.SourceSnapshot) catalogSourceRecord {
	rec := catalogSourceRecord{
		URL:        snap.URL,
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
