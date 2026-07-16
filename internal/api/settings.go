package api

import (
	"encoding/json"
	"fmt"
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

	// ManagedByGit (V3 C1) is true when the setting is Helm/git-declared
	// (authoritative, git wins). When true, a runtime PUT edit will be
	// reclaimed on the next reconcile. Omitted (false) when the key is NOT
	// declared → runtime ConfigMap value persists (API authoritative).
	ManagedByGit bool `json:"managed_by_git,omitempty"`
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
	writeJSON(w, http.StatusOK, probeModeResponse{
		ProbeMode:    mode,
		ManagedByGit: settings.IsManagedByGit("probe_mode"),
	})
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

// allowInlineCredentialsResponse is the response/request body shape for the
// allow-inline-credentials setting endpoints (V2-cleanup-89.6).
type allowInlineCredentialsResponse struct {
	// AllowInlineCredentials is true (the default) when the "Paste a
	// kubeconfig" registration path is available. An admin sets this to
	// false to forbid inline credential paste install-wide — registration
	// requests that actually supply inline kubeconfig bytes are then
	// rejected with a 403; connection-only registrations are unaffected.
	// Sharko has no user RBAC today (single admin login); when V2.x scoped
	// RBAC lands this is expected to become a per-role permission.
	AllowInlineCredentials bool `json:"allow_inline_credentials"`

	// ManagedByGit (V3 C1) is true when the setting is Helm/git-declared
	// (authoritative, git wins). When true, a runtime PUT edit will be
	// reclaimed on the next reconcile. Omitted (false) when the key is NOT
	// declared → runtime ConfigMap value persists (API authoritative).
	ManagedByGit bool `json:"managed_by_git,omitempty"`
}

// handleGetAllowInlineCredentials godoc
//
// @Summary Get allow-inline-credentials setting
// @Description Returns whether the "Paste a kubeconfig" registration path is enabled server-wide (V2-cleanup-89.6, default true)
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} allowInlineCredentialsResponse "Current setting"
// @Failure 503 {object} map[string]interface{} "Settings store not available"
// @Router /settings/allow-inline-credentials [get]
func (s *Server) handleGetAllowInlineCredentials(w http.ResponseWriter, r *http.Request) {
	if s.settingsStore == nil {
		// No in-cluster settings store wired (e.g. local/dev mode) — the
		// feature still behaves correctly at its default (allowed).
		writeJSON(w, http.StatusOK, allowInlineCredentialsResponse{AllowInlineCredentials: true})
		return
	}
	allow, err := s.settingsStore.GetAllowInlineCredentials(r.Context())
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "get_allow_inline_credentials", err)
		return
	}
	writeJSON(w, http.StatusOK, allowInlineCredentialsResponse{
		AllowInlineCredentials: allow,
		ManagedByGit:           settings.IsManagedByGit("allow_inline_credentials"),
	})
}

// handleSetAllowInlineCredentials godoc
//
// @Summary Set allow-inline-credentials setting
// @Description Sets whether the "Paste a kubeconfig" registration path is enabled server-wide (V2-cleanup-89.6). Admin only.
// @Tags system
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body allowInlineCredentialsResponse true "Desired setting"
// @Success 200 {object} allowInlineCredentialsResponse "Setting saved"
// @Failure 400 {object} map[string]interface{} "Invalid request body"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden — admin role required"
// @Failure 503 {object} map[string]interface{} "Settings store not available"
// @Router /settings/allow-inline-credentials [put]
func (s *Server) handleSetAllowInlineCredentials(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "settings.allow-inline-credentials") {
		return
	}
	if s.settingsStore == nil {
		writeError(w, http.StatusServiceUnavailable, "settings store is not available (no in-cluster ConfigMap access)")
		return
	}

	var req allowInlineCredentialsResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if err := s.settingsStore.SetAllowInlineCredentials(r.Context(), req.AllowInlineCredentials); err != nil {
		writeServerError(w, http.StatusInternalServerError, "set_allow_inline_credentials", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "allow_inline_credentials_updated",
		Resource: "settings:allow_inline_credentials",
		Detail:   fmt.Sprintf("%t", req.AllowInlineCredentials),
	})

	writeJSON(w, http.StatusOK, allowInlineCredentialsResponse{AllowInlineCredentials: req.AllowInlineCredentials})
}
