package api

// engine_pin.go — v4 Wave 1 Story 2.5: the engine chart pin-bump check and
// upgrade-PR endpoints.
//
// Mirrors the existing addon-write pattern (internal/api/addons_write.go):
// authz check -> Tier2 Git provider (configuration change) ->
// orchestrator call -> dry-run short-circuit -> audit.Enrich -> JSON
// response with the attribution warning attached. See
// docs/design/2026-07-30-v4-data-file-format.md section 2.5 for the
// engine pin's own shape and section 5 for the versioning rule.
//
// No ArgoCD client is required here (unlike most addon/cluster write
// paths): the engine pin PR only ever touches Git. The Application object
// itself is re-applied to ArgoCD by the existing PR-merge fan-out
// (prTracker.SetOnMergeFn) — that wiring belongs to the bootstrap/apply
// path, not this check-and-PR flow.

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// handleCheckEnginePin godoc
//
// @Summary Check engine pin
// @Description Compares the engine chart version pinned in the connected repo's engine/application.yaml against the version bundled with this Sharko build. Responds cleanly (v4_repo=false) for v3 repos or repos not yet bootstrapped — never errors on a missing pin.
// @Tags engine
// @Produce json
// @Security BearerAuth
// @Success 200 {object} orchestrator.EnginePinCheckResult "Check result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /engine/pin [get]
// handleCheckEnginePin handles GET /api/v1/engine/pin.
func (s *Server) handleCheckEnginePin(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "engine.pin-check") {
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, nil, git, s.gitopsConfig(), s.repoPaths, nil)
	result, err := orch.CheckEnginePin(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleUpgradeEnginePin godoc
//
// @Summary Upgrade engine pin
// @Description Opens a pull request that changes ONLY the engine pin's targetRevision to the version bundled with this Sharko build. Nothing in any cluster changes until the PR is merged. Without dry_run=true, returns a preview of the (single-line) diff with no side effects.
// @Tags engine
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body EngineUpgradePinRequest false "Upgrade request"
// @Success 200 {object} map[string]interface{} "Upgrade PR result (or dry-run preview)"
// @Failure 400 {object} map[string]interface{} "Bad request — no engine pin, or already up to date"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /engine/pin/upgrade [post]
// handleUpgradeEnginePin handles POST /api/v1/engine/pin/upgrade.
func (s *Server) handleUpgradeEnginePin(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "engine.pin-upgrade") {
		return
	}

	var req EngineUpgradePinRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	if r.URL.Query().Get("dry_run") == "true" {
		req.DryRun = true
	}

	// Tier 2: configuration change — prefer per-user PAT, fall back to
	// service token. Same tier addon-write and addon-configure use.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, nil, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	result, err := orch.UpgradeEnginePin(ctx, req.AutoMerge, req.DryRun)
	if err != nil {
		// "no engine pin" / "already up to date" are caller-fixable
		// conditions, not upstream failures — surface as 400.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "engine_pin_upgraded",
		Resource: "engine:sharko-engine",
		Detail:   fmt.Sprintf("pr_url=%s", result.PRUrl),
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// EngineUpgradePinRequest is the request body for POST /api/v1/engine/pin/upgrade.
type EngineUpgradePinRequest struct {
	// AutoMerge overrides the connection-level PRAutoMerge default for
	// this PR only. nil = fall back to the connection default.
	AutoMerge *bool `json:"auto_merge,omitempty"`
	// DryRun, when true, returns a preview of the diff with no side
	// effects. Also settable via ?dry_run=true.
	DryRun bool `json:"dry_run,omitempty"`
}
