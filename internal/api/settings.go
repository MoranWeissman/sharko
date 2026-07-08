package api

import (
	"encoding/json"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/settings"
)

// probeModeResponse is the response/request body shape for the probe-mode
// setting endpoints.
type probeModeResponse struct {
	// ProbeMode is one of "check-app" (default — Sharko auto-deploys a
	// transient connectivity-check application to new zero-addon clusters)
	// or "api-test" (no app is ever auto-deployed; reachability comes
	// purely from ArgoCD's own connection state).
	ProbeMode string `json:"probe_mode"`
}

// handleGetProbeMode godoc
//
// @Summary Get probe mode
// @Description Returns the server-wide connectivity probe mode (check-app | api-test, V2-cleanup-85.4)
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} probeModeResponse "Current probe mode"
// @Failure 503 {object} map[string]interface{} "Settings store not available"
// @Router /settings/probe-mode [get]
func (s *Server) handleGetProbeMode(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		// No in-cluster settings store wired (e.g. local/dev mode) — the
		// feature still behaves correctly at its default.
		writeJSON(w, http.StatusOK, probeModeResponse{ProbeMode: settings.ProbeModeCheckApp})
		return
	}
	mode, err := s.settingsStore.GetProbeMode(r.Context())
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "get_probe_mode", err)
		return
	}
	writeJSON(w, http.StatusOK, probeModeResponse{ProbeMode: mode})
}

// handleSetProbeMode godoc
//
// @Summary Set probe mode
// @Description Sets the server-wide connectivity probe mode (check-app | api-test, V2-cleanup-85.4). Admin only.
// @Tags system
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body probeModeResponse true "Desired probe mode"
// @Success 200 {object} probeModeResponse "Probe mode saved"
// @Failure 400 {object} map[string]interface{} "Invalid probe_mode value"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "Settings store not available"
// @Router /settings/probe-mode [put]
func (s *Server) handleSetProbeMode(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "settings.probe-mode") {
		return
	}
	if s.settingsStore == nil {
		writeError(w, http.StatusServiceUnavailable, "settings store is not available (no in-cluster ConfigMap access)")
		return
	}

	var req probeModeResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if err := s.settingsStore.SetProbeMode(r.Context(), req.ProbeMode); err != nil {
		if _, ok := err.(*settings.InvalidProbeModeError); ok {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeServerError(w, http.StatusInternalServerError, "set_probe_mode", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "probe_mode_updated",
		Resource: "settings:probe_mode",
		Detail:   req.ProbeMode,
	})

	writeJSON(w, http.StatusOK, probeModeResponse{ProbeMode: req.ProbeMode})
}
