package api

// catalog_freshness.go — v4 wave 1 Story 3.4: read + trigger endpoints for
// the catalog version-freshness scheduler (internal/catalog.
// FreshnessScheduler). The scheduler itself keeps a durable "last checked"
// snapshot per curated addon and for the engine pin check, refreshed daily
// by default; this file exposes that state over HTTP and lets an operator
// or the UI's refresh button ask for an out-of-cycle pass.
//
// GET /api/v1/catalog/addons/{name}/versions (catalog_versions.go) is the
// per-addon detail the version picker consumes — it now prefers the
// scheduler's snapshot when one exists. This file's GET endpoint is the
// catalog-wide summary: when Sharko last checked ANYTHING, useful for a
// Marketplace list header before any single addon has been opened.

import (
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/catalog"
)

// SetFreshness wires the freshness scheduler onto the Server. Nil is
// tolerated everywhere it's read.
func (s *Server) SetFreshness(f *catalog.FreshnessScheduler) {
	s.freshness = f
}

// Freshness returns the wired freshness scheduler, or nil when unset.
func (s *Server) Freshness() *catalog.FreshnessScheduler {
	return s.freshness
}

// catalogFreshnessResponse is the catalog-wide summary envelope.
type catalogFreshnessResponse struct {
	Enabled       bool                    `json:"enabled"`
	IntervalSecs  int                     `json:"interval_seconds,omitempty"`
	LastRun       string                  `json:"last_run,omitempty"`
	NextRun       string                  `json:"next_run,omitempty"`
	AddonsChecked int                     `json:"addons_checked"`
	EnginePin     *enginePinFreshnessInfo `json:"engine_pin,omitempty"`
}

type enginePinFreshnessInfo struct {
	LastChecked      string `json:"last_checked,omitempty"`
	V4Repo           bool   `json:"v4_repo"`
	UpgradeAvailable bool   `json:"upgrade_available"`
	Message          string `json:"message,omitempty"`
	Err              string `json:"error,omitempty"`
}

// handleGetCatalogFreshness godoc
//
// @Summary Catalog version-freshness summary
// @Description Returns when Sharko's background freshness scheduler last checked chart versions across the curated catalog and last checked the v4 engine pin, plus the configured refresh interval. Read-only. Per-addon detail lives on GET /catalog/addons/{name}/versions, which also carries a real last-checked timestamp.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} catalogFreshnessResponse "Freshness summary"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /catalog/freshness [get]
func (s *Server) handleGetCatalogFreshness(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "catalog.freshness.read") {
		return
	}

	if s.freshness == nil {
		writeJSON(w, http.StatusOK, catalogFreshnessResponse{Enabled: false})
		return
	}

	resp := catalogFreshnessResponse{
		Enabled:      true,
		IntervalSecs: int(s.freshness.Interval().Seconds()),
	}
	if lastRun := s.freshness.LastRun(); !lastRun.IsZero() {
		resp.LastRun = lastRun.UTC().Format(time.RFC3339)
	}
	if nextRun := s.freshness.NextRun(); !nextRun.IsZero() {
		resp.NextRun = nextRun.UTC().Format(time.RFC3339)
	}
	if s.catalog != nil {
		resp.AddonsChecked = s.catalog.Len()
	}
	if snap, ok := s.freshness.EnginePinSnapshot(); ok {
		info := &enginePinFreshnessInfo{
			LastChecked:      snap.CheckedAt.UTC().Format(time.RFC3339),
			V4Repo:           snap.Status.V4Repo,
			UpgradeAvailable: snap.Status.UpgradeAvailable,
			Message:          snap.Status.Message,
			Err:              snap.Err,
		}
		resp.EnginePin = info
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleRefreshCatalogFreshness godoc
//
// @Summary Trigger an out-of-cycle catalog freshness refresh
// @Description Requests an immediate freshness pass (chart versions across the curated catalog, plus the engine pin check) instead of waiting for the next scheduled tick. Non-blocking — the refresh runs in the background; poll GET /catalog/freshness or GET /catalog/addons/{name}/versions afterward to see updated timestamps. A request while a refresh is already pending is coalesced into that pending run.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 202 {object} map[string]interface{} "Refresh requested"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "Freshness scheduler not enabled"
// @Router /catalog/freshness/refresh [post]
func (s *Server) handleRefreshCatalogFreshness(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "catalog.freshness.refresh") {
		return
	}

	if s.freshness == nil {
		writeError(w, http.StatusServiceUnavailable, "freshness scheduler not enabled")
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "catalog_freshness_refresh_triggered",
		Resource: "catalog:freshness",
	})

	s.freshness.Trigger()
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"message": "freshness refresh requested",
	})
}
