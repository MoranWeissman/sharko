package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/models"
)

// handleListUpgradeVersions godoc
//
// @Summary List upgrade versions
// @Description Returns available versions for an addon from the Helm repository
// @Tags upgrade
// @Produce json
// @Security BearerAuth
// @Param addonName path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Available versions"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /upgrade/{addonName}/versions [get]
func (s *Server) handleListUpgradeVersions(w http.ResponseWriter, r *http.Request) {
	addonName := r.PathValue("addonName")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.upgradeSvc.ListVersions(r.Context(), addonName, gp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleCheckUpgrade godoc
//
// @Summary Check upgrade impact
// @Description Analyzes the impact of upgrading an addon to a target version (changelog diff, values changes)
// @Tags upgrade
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.UpgradeCheckRequest true "Upgrade check request"
// @Success 200 {object} map[string]interface{} "Upgrade impact analysis"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /upgrade/check [post]
func (s *Server) handleCheckUpgrade(w http.ResponseWriter, r *http.Request) {
	var req models.UpgradeCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.AddonName == "" || req.TargetVersion == "" {
		writeError(w, http.StatusBadRequest, "addon_name and target_version are required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.upgradeSvc.CheckUpgrade(r.Context(), req.AddonName, req.TargetVersion, gp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "upgrade_analyzed",
		Resource: fmt.Sprintf("addon:%s", req.AddonName),
		Detail:   fmt.Sprintf("target=%s", req.TargetVersion),
	})
	writeJSON(w, http.StatusOK, resp)
}

// handleGetAISummary godoc
//
// @Summary Get AI upgrade summary
// @Description Generates an AI-written plain-language summary of the upgrade impact analysis
// @Tags upgrade
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.UpgradeCheckRequest true "Upgrade check request"
// @Success 200 {object} map[string]interface{} "AI summary"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /upgrade/ai-summary [post]
func (s *Server) handleGetAISummary(w http.ResponseWriter, r *http.Request) {
	var req models.UpgradeCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	// First get the upgrade check result
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	result, err := s.upgradeSvc.CheckUpgrade(r.Context(), req.AddonName, req.TargetVersion, gp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	summary, err := s.upgradeSvc.GetAISummary(r.Context(), result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"summary": summary})
}

// handleGetAIStatus godoc
//
// @Summary Get AI status
// @Description Returns whether the AI integration is currently enabled and configured
// @Tags upgrade
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "AI status"
// @Router /upgrade/ai-status [get]
func (s *Server) handleGetAIStatus(w http.ResponseWriter, r *http.Request) {
	enabled := s.upgradeSvc.IsAIEnabled()
	writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": enabled})
}

// handleGetRecommendations godoc
//
// @Summary Get upgrade recommendations
// @Description Returns smart upgrade recommendations for an addon: next patch, next minor, and latest stable version
// @Tags upgrade
// @Produce json
// @Security BearerAuth
// @Param addonName path string true "Addon name"
// @Success 200 {object} models.UpgradeRecommendations "Upgrade recommendations"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /upgrade/{addonName}/recommendations [get]
func (s *Server) handleGetRecommendations(w http.ResponseWriter, r *http.Request) {
	addonName := r.PathValue("addonName")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	rec, err := s.upgradeSvc.GetRecommendations(r.Context(), addonName, gp)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, rec)
}
