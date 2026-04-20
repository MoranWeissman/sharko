package api

// catalog_reprobe.go — V121-3.4: force-clear the ArtifactHub circuit breaker.
//
//   POST /api/v1/catalog/reprobe
//
// Used by the UI when the user clicks "Retry" in the unreachable banner. The
// handler:
//   1. Resets the per-host backoff so the next call goes through immediately.
//   2. Optionally purges the search/package caches so the user sees a fresh
//      probe rather than the (possibly stale) cached value.
//   3. Probes ArtifactHub once to populate `reachable` in the response so the
//      user gets immediate feedback ("still down" vs "back online").
//
// Tier 1 (operational) — admin-only. Audited.

import (
	"context"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/catalog"
)

// catalogReprobeResponse is the body returned by POST /catalog/reprobe.
type catalogReprobeResponse struct {
	Reachable bool   `json:"reachable"`
	LastError string `json:"last_error,omitempty"`
	ProbedAt  string `json:"probed_at"`
}

// handleReprobeArtifactHub godoc
//
// @Summary Force ArtifactHub connectivity re-check
// @Description Resets the in-memory backoff/circuit-breaker for ArtifactHub, purges the search and package-detail caches, and issues a probe against the ArtifactHub API root. Returns whether ArtifactHub is currently reachable from this Sharko process. Used by the UI's "Retry" button on the search-unreachable banner. Tier 1 (operational); audit-logged.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} catalogReprobeResponse "Probe result"
// @Failure 401 {object} map[string]interface{} "Unauthenticated"
// @Router /catalog/reprobe [post]
func (s *Server) handleReprobeArtifactHub(w http.ResponseWriter, r *http.Request) {
	// Reset backoff so the probe is allowed to run.
	ahBackoff.Success()

	// Drop cached responses — the user expects "Retry" to mean "fresh fetch."
	searchCache.Purge()
	packageCache.Purge()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	resp := catalogReprobeResponse{
		ProbedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := ahClient.Probe(ctx); err != nil {
		ahBackoff.Failure()
		resp.Reachable = false
		resp.LastError = classifyAHError(err)
	} else {
		resp.Reachable = true
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "catalog_reprobe",
		Resource: "artifacthub",
		Detail:   classifyReprobeDetail(resp),
		Tier:     audit.Tier1,
	})

	writeJSON(w, http.StatusOK, resp)
}

// classifyReprobeDetail produces a short, log-friendly summary so audit
// readers see "reachable" or "rate_limited" without parsing the JSON body.
func classifyReprobeDetail(resp catalogReprobeResponse) string {
	if resp.Reachable {
		return "reachable"
	}
	if resp.LastError != "" {
		return "unreachable: " + resp.LastError
	}
	return "unreachable"
}

// classifyAHError maps an ArtifactHubError class to a short string the UI/audit
// can switch on. Returns "unknown" for non-classified errors so the field is
// always populated.
func classifyAHError(err error) string {
	if err == nil {
		return ""
	}
	for _, class := range []catalog.AHErrClass{
		catalog.AHErrRateLimited,
		catalog.AHErrServerError,
		catalog.AHErrTimeout,
		catalog.AHErrNotFound,
		catalog.AHErrMalformed,
		catalog.AHErrInvalidInput,
	} {
		if catalog.IsArtifactHubClass(err, class) {
			return string(class)
		}
	}
	return "unknown"
}
