package api

import (
	"errors"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// RepoStatusResponse is the body returned by GET /api/v1/repo/status.
//
// `Initialized` reports whether bootstrap/Chart.yaml is readable on the
// configured base branch — i.e. the GitOps repo has been seeded.
//
// `BootstrapSynced` reports whether the canonical ArgoCD application
// `cluster-addons-bootstrap` exists AND is Sync=Synced AND Health=Healthy.
// The wizard gate in App.tsx uses this to auto-open the wizard when the
// repo is initialized but the cluster-side bootstrap is missing or
// degraded — without this signal, the user would land on a dashboard
// full of errors instead of a recovery surface.
//
// `Reason` is a short machine-readable tag (only set when Initialized=false)
// that helps the UI distinguish the not-initialized cases from one another:
//   - "no_connection"    — no active Git connection is configured.
//   - "not_bootstrapped" — the repo IS reachable but bootstrap/Chart.yaml is
//     genuinely absent (a real "never set up" state — the wizard should fire).
//   - "connection_error" — the repo could NOT be reached or verified (e.g. a
//     TLS/transport/auth failure). This is an environment problem, NOT a setup
//     problem, so the UI must KEEP the user in their working app and surface
//     the broken connection rather than forcing a re-bootstrap.
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
		// The gitprovider layer already distinguishes the two cases:
		// a genuinely-missing file is wrapped with gitprovider.ErrFileNotFound,
		// while a transport/TLS/auth failure stays a plain wrapped error.
		// Detect via errors.Is (provider-agnostic — GitHub and Azure DevOps
		// both wrap the sentinel) so we DON'T mistake a broken connection for
		// "the repo was never set up".
		reason := "connection_error"
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			// The repo is reachable, the file is genuinely absent — this is a
			// real "never bootstrapped" state, so the wizard SHOULD fire.
			reason = "not_bootstrapped"
		}
		writeJSON(w, http.StatusOK, RepoStatusResponse{
			Initialized:     false,
			BootstrapSynced: false,
			Reason:          reason,
		})
		return
	}

	// Repo files are present. Probe ArgoCD for the canonical bootstrap
	// root-app and report Synced+Healthy. Any error path (no client,
	// app missing, OutOfSync, Degraded) reports bootstrap_synced=false
	// — the UI then auto-opens the wizard so the user has a recovery
	// surface instead of a dashboard full of errors.
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
