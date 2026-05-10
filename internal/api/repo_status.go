package api

import (
	"net/http"
)

// RepoStatusResponse is the body returned by GET /api/v1/repo/status.
//
// `Initialized` reports whether bootstrap/Chart.yaml is readable on the
// configured base branch — i.e. the GitOps repo has been seeded.
//
// `BootstrapSynced` (V124-22 / BUG-046) reports whether the canonical
// ArgoCD application `cluster-addons-bootstrap` exists AND is
// Sync=Synced AND Health=Healthy. The wizard gate in App.tsx uses this
// to auto-open the wizard when the repo is initialized but the cluster-
// side bootstrap is missing or degraded — V124-15 made the operation
// framework treat that condition as a failure, but the wizard previously
// only checked `Initialized`, so the user landed on a dashboard splattered
// with errors instead of a recovery surface.
//
// `Reason` is a short machine-readable tag (only set when Initialized=false)
// that helps the UI distinguish e.g. "no_connection" from "not_bootstrapped".
type RepoStatusResponse struct {
	Initialized     bool   `json:"initialized"`
	BootstrapSynced bool   `json:"bootstrap_synced"`
	Reason          string `json:"reason,omitempty"`
}

// handleRepoStatus godoc
//
// @Summary Get repo initialization status
// @Description Checks whether the GitOps repository has been bootstrapped (bootstrap/Chart.yaml exists on the base branch) AND whether the ArgoCD bootstrap Application is Synced + Healthy. The wizard gate in the UI uses bootstrap_synced to auto-open the recovery wizard when the cluster-side bootstrap is missing or degraded even though the repo files are present.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.RepoStatusResponse "Repo status"
// @Router /repo/status [get]
// handleRepoStatus handles GET /api/v1/repo/status — check if the repo is initialized
// and whether the ArgoCD bootstrap application is Synced+Healthy.
func (s *Server) handleRepoStatus(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeJSON(w, http.StatusOK, RepoStatusResponse{
			Initialized:     false,
			BootstrapSynced: false,
			Reason:          "no_connection",
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
		writeJSON(w, http.StatusOK, RepoStatusResponse{
			Initialized:     false,
			BootstrapSynced: false,
			Reason:          "not_bootstrapped",
		})
		return
	}

	// Repo files are present. V124-22 / BUG-046: probe ArgoCD for the
	// canonical bootstrap root-app and report Synced+Healthy. Any error
	// path (no client, app missing, OutOfSync, Degraded) reports
	// bootstrap_synced=false — the UI then auto-opens the wizard so the
	// user has a recovery surface instead of a dashboard full of errors.
	//
	// We deliberately swallow the GetActiveOrchestratorArgocdClient error:
	// "the connection has Git but no usable ArgoCD" is exactly the
	// degraded state the wizard exists to repair.
	bootstrapSynced := false
	if ac, acErr := s.connSvc.GetActiveOrchestratorArgocdClient(); acErr == nil {
		status, _ := ProbeBootstrapApp(r.Context(), ac)
		bootstrapSynced = status == "healthy"
	}

	writeJSON(w, http.StatusOK, RepoStatusResponse{
		Initialized:     true,
		BootstrapSynced: bootstrapSynced,
	})
}
