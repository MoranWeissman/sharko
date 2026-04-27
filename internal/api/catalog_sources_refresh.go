package api

// catalog_sources_refresh.go — V123-1.6: Tier-2 force-refresh of all
// configured catalog sources. Synchronously re-fetches every third-party
// catalog URL (from SHARKO_CATALOG_URLS) without waiting for the next
// cadence tick, then returns the same response shape as
// GET /catalog/sources.
//
// Used by an admin verifying that a newly added source is reachable
// without having to restart the process or wait for the ticker. The
// endpoint is admin-only (Tier 2) and audit-logged — the audit Detail
// carries the list of attempted URLs and their per-URL outcome, which
// is the authoritative record of the operation (the handler itself
// emits zero log lines to avoid leaking URLs into application logs).

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// refreshCtxTimeout bounds the handler's refresh window. ForceRefresh
// blocks until every configured source has been re-fetched; for the
// typical 3-5 source case this finishes in single-digit seconds, but
// the 60-second cap keeps a hung upstream from pinning the request
// forever. No per-source knob is needed in this story.
const refreshCtxTimeout = 60 * time.Second

// handleRefreshCatalogSources godoc
//
// @Summary Force-refresh all catalog sources (Tier 2, admin-only)
// @Description Synchronously re-fetches every configured third-party catalog source without waiting for the next cadence tick. Returns the refreshed list in the same shape as GET /catalog/sources. The embedded catalog is always included as a pseudo-source. Requires authentication AND admin role — classified Tier 2 and audit-logged; the audit Detail carries the list of attempted URLs and their per-URL status. The endpoint is a no-op in embedded-only mode (no fetcher wired) and returns just the embedded record.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {array} catalogSourceRecord "Refreshed catalog sources with per-source fetch status"
// @Failure 403 {object} map[string]interface{} "Caller role lacks the catalog.sources.refresh action"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded"
// @Router /catalog/sources/refresh [post]
func (s *Server) handleRefreshCatalogSources(w http.ResponseWriter, r *http.Request) {
	// V123-2.4 / B2 BLOCKER fix: catalog source refresh is Tier-2
	// (admin-only, audit-logged). The handler historically lacked the
	// authz gate, letting any authenticated operator/viewer drive a
	// force-refresh — out of line with the documented tier classification.
	// Gate first, before the catalog-loaded check, so callers without
	// the role get a clean 403 regardless of catalog state.
	if !authz.RequireWithResponse(w, r, "catalog.sources.refresh") {
		return
	}
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}

	// Attempted URLs + per-URL status are the audit payload regardless
	// of whether a fetcher is wired. The empty-slice / empty-map case
	// is a legitimate audit event (admin hit "refresh" on an
	// embedded-only deployment — worth recording as a human action).
	attempted := []string{}
	statusByURL := map[string]string{}

	if s.sourcesFetcher != nil {
		// 60s safety cap around ForceRefresh. The fetcher itself uses
		// a 30s per-URL HTTP timeout and parallel workers, so 3-5
		// sources wall-clock well under the cap. We still bound the
		// request so a hung upstream cannot tie up the handler
		// indefinitely.
		ctx, cancel := context.WithTimeout(r.Context(), refreshCtxTimeout)
		defer cancel()

		// Empty urls -> refresh every configured source. For
		// embedded-only deployments with a fetcher wired but no
		// SHARKO_CATALOG_URLS entries this is a no-op (fetcher
		// resolveTargets returns an empty target list).
		s.sourcesFetcher.ForceRefresh(ctx)

		snaps := s.sourcesFetcher.Snapshots()
		for u, snap := range snaps {
			attempted = append(attempted, u)
			statusByURL[u] = string(snap.Status)
		}
		// Deterministic order in the audit payload so two refreshes
		// of the same source set produce byte-identical Detail
		// strings (matters for log diffing / alerting rules).
		sort.Strings(attempted)
	}

	// audit.Fields.Detail is a string — marshal the structured payload
	// to JSON so audit log viewers can parse it back out. The
	// statusByURL map is intentionally left in its natural
	// (Go-randomised) JSON encoding; the urls slice above is the
	// authoritative ordered list of attempted sources.
	detailPayload := map[string]interface{}{
		"urls":   attempted,
		"status": statusByURL,
	}
	detailJSON, _ := json.Marshal(detailPayload)

	audit.Enrich(r.Context(), audit.Fields{
		Event:  "catalog_sources_refreshed",
		Detail: string(detailJSON),
	})

	// Build the response AFTER the refresh so the caller sees the
	// post-refresh snapshot state — matches the AC ("the response is
	// the same shape as GET /catalog/sources after the refresh
	// completes").
	writeJSON(w, http.StatusOK, s.buildCatalogSourcesResponse())
}
