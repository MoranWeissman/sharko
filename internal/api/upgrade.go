package api

import (
	"encoding/json"
	"net/http"

	"github.com/moran/argocd-addons-platform/internal/models"
)

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

	writeJSON(w, http.StatusOK, resp)
}

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

func (s *Server) handleGetAIStatus(w http.ResponseWriter, r *http.Request) {
	enabled := s.upgradeSvc.IsAIEnabled()
	writeJSON(w, http.StatusOK, map[string]interface{}{"enabled": enabled})
}
