package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// RepoStatusResponse is the body returned by GET /api/v1/repo/status.
//
// `Initialized` reports whether the engine pin (orchestrator.BootstrapRootAppPath
// — "engine/application.yaml", v4 Wave 1 Story 4.2) is readable on the
// configured base branch — i.e. the GitOps repo has been seeded. Before
// Story 4.2 this checked the v3 "bootstrap/Chart.yaml" marker; the engine
// pin is the one file every v4 bootstrap PR is guaranteed to contain, so
// it is the readiness signal for both fresh v4 installs and (once merged)
// any repo Sharko has bootstrapped.
//
// `BootstrapSynced` reports whether the canonical ArgoCD application
// (orchestrator.BootstrapRootAppName, "sharko-engine") exists AND is
// Sync=Synced AND Health=Healthy.
// The wizard gate in App.tsx uses this to auto-open the wizard when the
// repo is initialized but the cluster-side bootstrap is missing or
// degraded — without this signal, the user would land on a dashboard
// full of errors instead of a recovery surface.
//
// `Reason` is a short machine-readable tag that helps the UI distinguish the
// not-ready cases from one another. When Initialized=false (Git side):
//   - "no_connection"    — no active Git connection is configured.
//   - "not_bootstrapped" — the repo IS reachable but the engine pin is
//     genuinely absent (a real "never set up" state — the wizard should fire).
//   - "connection_error" — the repo could NOT be reached or verified (e.g. a
//     TLS/transport/auth failure). This is an environment problem, NOT a setup
//     problem, so the UI must KEEP the user in their working app and surface
//     the broken connection rather than forcing a re-bootstrap.
//
// When Initialized=true but BootstrapSynced=false (repo files present, but the
// ArgoCD bootstrap app is not Synced+Healthy), Reason distinguishes (V2-cleanup-51):
//   - "bootstrap_unreachable" — the bootstrap app reports Sync=Unknown, i.e.
//     ArgoCD's repo-server cannot reach/evaluate the Git repo (a connection
//     problem, often a TLS-inspection proxy the repo-server doesn't trust).
//     Re-running Initialize CANNOT fix this, so the UI must NOT auto-trap the
//     user in the re-bootstrap wizard.
//   - "bootstrap_degraded"    — the bootstrap app is genuinely degraded
//     (OutOfSync/Degraded/absent); ArgoCD read the repo and found a fixable
//     problem, so offering a repair (re-run Initialize) is appropriate.
//
// If no ArgoCD client is available, Reason is left empty (we can't classify).
// `Format` (additive) names the repo layout the probe recognised: "v4" when
// the engine pin is present, "v3" when it is not but a v3 marker is
// (orchestrator.HasV3Markers). Empty when neither is present, i.e. the repo
// really is un-bootstrapped. A v3 repo has no engine pin, so before this
// field existed the probe reported a perfectly healthy v3 repo as
// Initialized=false / not_bootstrapped — and the wizard gate in App.tsx
// then invited the user to Initialize, which would have written the v4
// folder tree on top of their live v3 repo.
type RepoStatusResponse struct {
	Initialized     bool   `json:"initialized"`
	BootstrapSynced bool   `json:"bootstrap_synced"`
	Reason          string `json:"reason,omitempty"`
	Format          string `json:"format,omitempty"`
}

// handleRepoStatus godoc
//
// @Summary Get repo initialization status
// @Description Checks whether the GitOps repository has been bootstrapped (the engine pin at engine/application.yaml exists on the base branch) AND whether the ArgoCD bootstrap Application is Synced + Healthy. The wizard gate in the UI uses bootstrap_synced to auto-open the recovery wizard when the cluster-side bootstrap is missing or degraded even though the repo files are present.
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

	// Check if the engine pin exists on the base branch — the one file
	// every v4 bootstrap PR is guaranteed to contain (design doc §1).
	baseBranch := s.gitopsConfig().BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	_, err = gp.GetFileContent(r.Context(), orchestrator.BootstrapRootAppPath, baseBranch)
	if err != nil {
		// The gitprovider layer already distinguishes the two cases:
		// a genuinely-missing file is wrapped with gitprovider.ErrFileNotFound,
		// while a transport/TLS/auth failure stays a plain wrapped error.
		// Detect via errors.Is (provider-agnostic — GitHub and Azure DevOps
		// both wrap the sentinel) so we DON'T mistake a broken connection for
		// "the repo was never set up".
		reason := "connection_error"
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			// The repo is reachable and the engine pin is genuinely absent.
			// That is NOT the same as "never bootstrapped" — a v3 repo has
			// no engine pin either. Check the v3 markers before telling the
			// wizard to offer Initialize, which on a live v3 repo would seed
			// the v4 folder tree on top of it.
			if orchestrator.HasV3Markers(r.Context(), gp, baseBranch) {
				synced, v3Reason := s.probeBootstrapSynced(r.Context())
				writeJSON(w, http.StatusOK, RepoStatusResponse{
					Initialized:     true,
					BootstrapSynced: synced,
					Reason:          v3Reason,
					Format:          orchestrator.RepoFormatV3,
				})
				return
			}
			reason = "not_bootstrapped"
		}
		writeJSON(w, http.StatusOK, RepoStatusResponse{
			Initialized:     false,
			BootstrapSynced: false,
			Reason:          reason,
		})
		return
	}

	bootstrapSynced, reason := s.probeBootstrapSynced(r.Context())
	writeJSON(w, http.StatusOK, RepoStatusResponse{
		Initialized:     true,
		BootstrapSynced: bootstrapSynced,
		Reason:          reason,
		Format:          orchestrator.RepoFormatV4,
	})
}

// probeBootstrapSynced asks ArgoCD whether the bootstrap application is
// Synced + Healthy, and classifies the not-ready cases. Extracted so the v3
// and v4 branches of handleRepoStatus give the same answer to the same
// question — a v3 repo's bootstrap health is just as real as a v4 repo's,
// and reporting it as "unknown" would send the wizard the wrong signal.
//
// Any error path (no ArgoCD client, app missing, OutOfSync, Degraded)
// reports false — the UI then opens the wizard so the user has a recovery
// surface instead of a dashboard full of errors. The
// GetActiveOrchestratorArgocdClient error is deliberately swallowed: "the
// connection has Git but no usable ArgoCD" is exactly the degraded state
// the wizard exists to repair.
func (s *Server) probeBootstrapSynced(ctx context.Context) (synced bool, reason string) {
	ac, acErr := s.connSvc.GetActiveOrchestratorArgocdClient()
	if acErr != nil {
		return false, ""
	}
	status, _ := ProbeBootstrapApp(ctx, ac)
	if status == bootstrapHealthy {
		return true, ""
	}
	// Distinguish "ArgoCD can't reach the repo" (re-init won't help) from a
	// genuinely degraded bootstrap (re-init may repair it), so the wizard
	// gate in App.tsx can avoid trapping the user when the problem is a
	// connection one (V2-cleanup-51).
	if status == bootstrapUnreachable {
		return false, "bootstrap_unreachable"
	}
	return false, "bootstrap_degraded"
}
