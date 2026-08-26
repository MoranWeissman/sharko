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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// CheckEnginePinLive performs a live engine pin check against the currently
// active Git connection. Exported so the freshness scheduler (v4 wave 1
// Story 3.4, wired in cmd/sharko/serve.go) can run the identical check on
// its own daily cycle without duplicating the Git-provider lookup and
// orchestrator construction that handleCheckEnginePin below also needs.
func (s *Server) CheckEnginePinLive(ctx context.Context) (*orchestrator.EnginePinCheckResult, error) {
	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		// B1. This is the one site in the set that RETURNS the failure
		// instead of writing a response, and its return value did reach a
		// caller's screen: handleCheckEnginePin below passes it to
		// writeError as err.Error(). The %w wrap it used to carry was the
		// leak — the wrapped error is whatever building a Git provider out
		// of the saved connection produced, and on the Git side that can be
		// net/url quoting a repository URL with the token inside it.
		//
		// So the cause is dropped rather than wrapped: this returns the
		// fixed sentence and nothing under it, and the real reason is named
		// on the log line here, at the point of failure.
		slog.Warn("engine pin check: no usable Git connection for the active connection")
		return nil, credsafe.ErrNoActiveGitConnection
	}
	orch := orchestrator.New(&s.gitMu, nil, nil, git, s.gitopsConfig(), s.repoPaths, nil)
	return orch.CheckEnginePin(ctx)
}

// enginePinResultFromSnapshot adapts a catalog.EnginePinSnapshot (the
// freshness scheduler's stored result) into the same
// orchestrator.EnginePinCheckResult shape the live check returns, so
// GET /api/v1/engine/pin's response contract is identical whichever source
// answered it.
func enginePinResultFromSnapshot(snap catalog.EnginePinSnapshot) *orchestrator.EnginePinCheckResult {
	return &orchestrator.EnginePinCheckResult{
		V4Repo:           snap.Status.V4Repo,
		BundledVersion:   snap.Status.BundledVersion,
		PinnedVersion:    snap.Status.PinnedVersion,
		UpgradeAvailable: snap.Status.UpgradeAvailable,
		Message:          snap.Status.Message,
	}
}

// handleCheckEnginePin godoc
//
// @Summary Check engine pin
// @Description Compares the engine chart version pinned in the connected repo's sharko-engine.yaml against the version bundled with this Sharko build. Responds cleanly (v4_repo=false) for v3 repos or repos not yet bootstrapped — never errors on a missing pin.
// @Tags engine
// @Produce json
// @Security BearerAuth
// @Success 200 {object} orchestrator.EnginePinCheckResult "Check result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /engine/pin [get]
// handleCheckEnginePin handles GET /api/v1/engine/pin.
//
// v4 wave 1 Story 3.4: when the live check cannot run (most commonly no
// active Git connection), this falls back to the freshness scheduler's most
// recent background check rather than failing hard — "every fetch failure →
// stale-but-dated data shown, never an error page." The live path stays
// primary whenever a connection IS available: it is a single cheap file
// read, and an upgrade decision deserves the freshest answer Sharko can
// give, not a potentially day-old cached one.
func (s *Server) handleCheckEnginePin(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "engine.pin-check") {
		return
	}

	result, err := s.CheckEnginePinLive(r.Context())
	if err != nil {
		if s.freshness != nil {
			if snap, ok := s.freshness.EnginePinSnapshot(); ok && snap.Err == "" {
				writeJSON(w, http.StatusOK, enginePinResultFromSnapshot(snap))
				return
			}
		}
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
		writeNoActiveGitConnection(w, r)
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
