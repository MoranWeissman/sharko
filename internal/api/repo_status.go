package api

import (
	"net/http"
)

// handleRepoStatus godoc
//
// @Summary Get repo initialization status
// @Description Checks whether the GitOps repository has been bootstrapped (bootstrap/Chart.yaml exists on the base branch)
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Repo status"
// @Router /repo/status [get]
// handleRepoStatus handles GET /api/v1/repo/status — check if the repo is initialized.
func (s *Server) handleRepoStatus(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"initialized": false,
			"reason":      "no_connection",
		})
		return
	}

	// Check if bootstrap/Chart.yaml exists on the base branch.
	baseBranch := s.gitopsCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	_, err = gp.GetFileContent(r.Context(), "bootstrap/Chart.yaml", baseBranch)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"initialized": false,
			"reason":      "not_bootstrapped",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"initialized": true,
	})
}
