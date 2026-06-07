package api

import (
	"context"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// Repo-state classifications returned by probeRepoState. These are the
// machine-readable values the first-run wizard switches on to decide
// what Step 4 should render.
const (
	// RepoStateEmpty — the bootstrap root-app YAML is not present on the
	// base branch, so the repo has never been initialized. The wizard
	// offers Initialize.
	RepoStateEmpty = "empty"
	// RepoStateInitialized — the bootstrap file is present AND the ArgoCD
	// bootstrap application is Synced + Healthy. The wizard tells the user
	// the repo is already set up and offers "Go to Dashboard".
	RepoStateInitialized = "initialized"
	// RepoStatePartial — the bootstrap file is present but the ArgoCD
	// bootstrap application is missing or unhealthy. The wizard surfaces
	// the detail string and offers a repair (re-run Initialize).
	RepoStatePartial = "partial"
	// RepoStateForbidden — the bootstrap file is present but ArgoCD rejected
	// the application read with a 403 (the token lacks RBAC permission). The
	// repo and bootstrap may be fine; the problem is the token's permissions.
	// Reported distinctly from "partial" so the user fixes ArgoCD RBAC rather
	// than chasing a phantom bootstrap failure (V2-cleanup-10). Detail carries
	// the actionable permission-denied message.
	RepoStateForbidden = "forbidden"
)

// InitStatusResponse is the body returned by GET /api/v1/init/status.
//
// State is one of "empty" | "initialized" | "partial". Detail carries a
// human-readable explanation — empty for the clean "empty"/"initialized"
// cases, and ArgoCD's diagnostic string for "partial".
type InitStatusResponse struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// probeRepoState is the single source of truth for classifying the GitOps
// repo's initialization state. It is read-only: one Git file-read plus, if
// the file exists, one ArgoCD application probe. No writes, no PR, no
// operation session.
//
// It is shared by BOTH the new read-only GET /api/v1/init/status handler
// AND the POST /init async runner (runInitOperation), so the "is this repo
// already initialized?" decision can never diverge between the two paths.
//
// The probe path is orchestrator.BootstrapRootAppPath — the same constant
// CollectBootstrapFiles emits to and isPRMerged keys off of.
//
//	file missing                                  -> ("empty",       "")
//	file present + ProbeBootstrapApp "healthy"     -> ("initialized", "")
//	file present + LIST 403 / permission-denied    -> ("forbidden",   <detail>)
//	file present + app absent (LIST ok, not found) -> ("partial",     <detail>)
//	file present + app present but unhealthy        -> ("partial",     <detail>)
//
// Note (V2-cleanup-11.2): the "app absent" case maps to "partial", NOT
// "forbidden". A populated repo pointed at a fresh ArgoCD (the bootstrap app
// not created yet) is a repair/init situation, not an RBAC problem. Only a
// genuine 403 on the LIST maps to "forbidden".
func probeRepoState(
	ctx context.Context,
	gp gitprovider.GitProvider,
	ac orchestrator.ArgocdClient,
	baseBranch string,
) (state, detail string) {
	if _, err := gp.GetFileContent(ctx, orchestrator.BootstrapRootAppPath, baseBranch); err != nil {
		return RepoStateEmpty, ""
	}
	status, argoDetail := ProbeBootstrapApp(ctx, ac)
	switch status {
	case bootstrapHealthy:
		return RepoStateInitialized, ""
	case bootstrapForbidden:
		// A 403 on the LIST is a genuine RBAC problem with the token, not a
		// broken bootstrap — surface it distinctly so neither the POST /init
		// partial path nor the GET /init/status probe mislabels it.
		return RepoStateForbidden, argoDetail
	default:
		// "absent" and "unhealthy" both mean: the repo has bootstrap files but
		// ArgoCD is not (yet) running a healthy bootstrap. The wizard offers
		// init/repair. Never an RBAC message here (V2-cleanup-11.2).
		return RepoStatePartial, argoDetail
	}
}

// handleInitStatus godoc
//
// @Summary Probe GitOps repo initialization state
// @Description Read-only probe used by the first-run wizard before it offers to initialize the repo. Returns "empty" when the bootstrap root-app YAML is not present on the base branch, "initialized" when it is present and the ArgoCD bootstrap application is Synced + Healthy, "forbidden" when the file is present but ArgoCD rejected the read with a 403 because the token lacks RBAC permission (detail carries an actionable permission message), and "partial" when the file is present but the ArgoCD bootstrap is missing or unhealthy (detail carries the ArgoCD diagnostic). Performs no writes and creates no operation session. Requires an active Git connection.
// @Tags init
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.InitStatusResponse "Repo state probe result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "No active Git/ArgoCD connection"
// @Router /init/status [get]
func (s *Server) handleInitStatus(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "init.status") {
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	ac, err := s.connSvc.GetActiveOrchestratorArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	baseBranch := s.gitopsCfg.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	state, detail := probeRepoState(r.Context(), gp, ac, baseBranch)
	writeJSON(w, http.StatusOK, InitStatusResponse{State: state, Detail: detail})
}
