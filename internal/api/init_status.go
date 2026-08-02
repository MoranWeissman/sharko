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
	// bootstrap application is missing or genuinely degraded (OutOfSync/
	// Degraded). The wizard surfaces the detail string and offers a repair
	// (re-run Initialize).
	RepoStatePartial = "partial"
	// RepoStateUnreachable — the bootstrap file is present but the ArgoCD
	// bootstrap application reports Sync=Unknown, i.e. ArgoCD's repo-server
	// cannot reach/evaluate the Git repo (a connection problem, often a
	// TLS-inspection proxy the repo-server doesn't trust). Reported distinctly
	// from "partial" because re-running Initialize CANNOT fix a connection
	// problem — Story 2 uses this to keep the wizard manually reachable instead
	// of auto-trapping the user in a re-bootstrap loop (V2-cleanup-51).
	RepoStateUnreachable = "unreachable"
	// RepoStateForbidden — the bootstrap file is present but ArgoCD rejected
	// the application read with a 403 (the token lacks RBAC permission). The
	// repo and bootstrap may be fine; the problem is the token's permissions.
	// Reported distinctly from "partial" so the user fixes ArgoCD RBAC rather
	// than chasing a phantom bootstrap failure (V2-cleanup-10). Detail carries
	// the actionable permission-denied message.
	RepoStateForbidden = "forbidden"
	// RepoStateAuthFailed — the bootstrap file is present but ArgoCD rejected
	// the application read with a 401: the token itself is invalid or
	// expired. Distinct from "forbidden" (403 — a valid token without RBAC
	// permission): here the credential is bad outright. This is the state
	// that closes the bug where an expired token made a fully set-up install
	// show the wizard claiming the bootstrap app "already exists but is not
	// healthy" — a fact Sharko never actually verified, because it never got
	// past the token check. Detail carries the actionable token message.
	RepoStateAuthFailed = "auth_failed"
	// RepoStateUnknown — the bootstrap file is present but Sharko could not
	// determine the ArgoCD bootstrap application's health at all (the LIST
	// call failed for a reason that is neither a permission problem nor a
	// bad credential — e.g. a network blip). This is the honest "couldn't
	// check" state: unlike "partial", it does NOT assert the bootstrap is
	// missing or degraded, because Sharko never got a usable answer from
	// ArgoCD to make that call.
	RepoStateUnknown = "unknown"
)

// InitStatusResponse is the body returned by GET /api/v1/init/status.
//
// State is one of "empty" | "initialized" | "partial" | "unreachable" |
// "forbidden" | "auth_failed" | "unknown". Detail carries a human-readable
// explanation — empty for the clean "empty"/"initialized" cases, and
// ArgoCD's diagnostic string for every other state. "auth_failed" carries
// the actionable invalid-token message; "unknown" carries whatever detail
// the failed LIST call produced — Sharko genuinely could not determine the
// bootstrap app's health, so the UI must not claim it is missing or
// degraded for this state.
// Format (additive) names the repo layout the probe recognised — "v4" when
// the engine pin is present, "v3" when it is not but a v3 marker is, empty
// when the repo really is un-bootstrapped.
// Repairable (additive, w2-q2) is only meaningful when State is "partial":
// true means the bootstrap Application simply has not been created yet
// (bootstrapAbsent) and POST /init's repair path can fix it with no PR — the
// wizard's "Re-run initialize to repair it" copy is honest here. False means
// the Application already exists but is degraded (bootstrapUnhealthy) —
// re-running Initialize cannot fix a live app, so the wizard must not
// promise a repair.
type InitStatusResponse struct {
	State      string `json:"state"`
	Detail     string `json:"detail"`
	Format     string `json:"format,omitempty"`
	Repairable bool   `json:"repairable,omitempty"`
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
//	file missing                                   -> ("empty",       "")
//	file present + ProbeBootstrapApp "healthy"      -> ("initialized", "")
//	file present + LIST 403 / permission-denied     -> ("forbidden",   <detail>)
//	file present + LIST 401 / invalid token         -> ("auth_failed", <detail>)
//	file present + app Sync=Unknown (unreachable)   -> ("unreachable", <detail>)
//	file present + app absent (LIST ok, not found)  -> ("partial",     <detail>)
//	file present + app present but degraded         -> ("partial",     <detail>)
//	file present + LIST failed, uncategorized       -> ("unknown",     <detail>)
//	file present + no ArgoCD client configured      -> ("unknown",     <detail>)
//
// Note (V2-cleanup-11.2): the "app absent" case maps to "partial", NOT
// "forbidden". A populated repo pointed at a fresh ArgoCD (the bootstrap app
// not created yet) is a repair/init situation, not an RBAC problem. Only a
// genuine 403 on the LIST maps to "forbidden".
//
// Note (V2-cleanup-51): the "unreachable" case (bootstrap app Sync=Unknown,
// i.e. ArgoCD's repo-server can't reach the Git repo) maps to "unreachable",
// NOT "partial". Re-running Initialize cannot fix a connection problem, so the
// wizard must treat it differently (Story 2). A genuinely degraded bootstrap
// (OutOfSync/Degraded) and an absent app stay "partial".
//
// Note (error review package 1): "auth_failed" and "unknown" must NEVER be
// folded into "partial". "partial" asserts something Sharko actually
// observed about the bootstrap app (it's absent, or it's degraded);
// "auth_failed" and "unknown" mean Sharko never got a usable answer from
// ArgoCD in the first place. Before these states existed, a dead/expired
// token (which surfaced as an uncategorized LIST failure) fell into the
// same "unhealthy" bucket as a genuinely degraded app, so the wizard told
// the user their fully-working bootstrap "already exists but is not
// healthy" — a claim Sharko had no basis for.
//
// Note (v4 review R1, H3): "no engine pin" is NOT the same as "empty". A v3
// repo has no engine pin either, so an engine-pin-only probe reports a
// live, fully-populated v3 repo as "empty" and the wizard then offers
// Initialize — which would write the v4 folder tree on top of it. When the
// pin is absent but a v3 marker is present (orchestrator.HasV3Markers) the
// probe classifies the repo exactly as it would a v4 one, and reports
// format "v3" so the caller knows which layout it is looking at.
func probeRepoState(
	ctx context.Context,
	gp gitprovider.GitProvider,
	ac orchestrator.ArgocdClient,
	baseBranch string,
	extraMarkers ...string,
) (state, detail, format string) {
	state, detail, format, _ = probeRepoStateWithBootstrapStatus(ctx, gp, ac, baseBranch, extraMarkers...)
	return state, detail, format
}

// probeRepoStateWithBootstrapStatus is probeRepoState plus the raw
// ProbeBootstrapApp status ("absent" | "unhealthy" | "unreachable" |
// "forbidden" | "healthy" | "") that produced the wire-level state. Added in
// w2-q2 so POST /init's repair path (runInitOperation) can tell "the app was
// simply never created" (bootstrapAbsent — repairable, no PR needed) apart
// from "the app exists but is degraded" (bootstrapUnhealthy — re-init cannot
// fix a live app), both of which classifyBootstrapApp collapses into the
// single wire-level RepoStatePartial. probeRepoState remains the function
// most callers use — this variant exists only for callers that need the
// finer-grained split.
func probeRepoStateWithBootstrapStatus(
	ctx context.Context,
	gp gitprovider.GitProvider,
	ac orchestrator.ArgocdClient,
	baseBranch string,
	extraMarkers ...string,
) (state, detail, format, bootstrapStatus string) {
	if _, err := gp.GetFileContent(ctx, orchestrator.BootstrapRootAppPath, baseBranch); err != nil {
		if orchestrator.HasV3Markers(ctx, gp, baseBranch, extraMarkers...) {
			s, d, bs := classifyBootstrapApp(ctx, ac)
			return s, d, orchestrator.RepoFormatV3, bs
		}
		return RepoStateEmpty, "", "", ""
	}
	s, d, bs := classifyBootstrapApp(ctx, ac)
	return s, d, orchestrator.RepoFormatV4, bs
}

// classifyBootstrapApp maps the ArgoCD bootstrap-app probe onto the
// RepoState* vocabulary. Shared by the v3 and v4 arms of probeRepoState so
// both formats get the identical classification. bootstrapStatus is the raw
// ProbeBootstrapApp status this classification was derived from (see
// probeRepoStateWithBootstrapStatus).
func classifyBootstrapApp(ctx context.Context, ac orchestrator.ArgocdClient) (state, detail, bootstrapStatus string) {
	status, argoDetail := ProbeBootstrapApp(ctx, ac)
	switch status {
	case bootstrapHealthy:
		return RepoStateInitialized, "", status
	case bootstrapForbidden:
		// A 403 on the LIST is a genuine RBAC problem with the token, not a
		// broken bootstrap — surface it distinctly so neither the POST /init
		// partial path nor the GET /init/status probe mislabels it.
		return RepoStateForbidden, argoDetail, status
	case bootstrapUnreachable:
		// Sync=Unknown means ArgoCD's repo-server can't reach/evaluate the Git
		// repo — a connection problem re-running Initialize cannot fix. Report
		// it distinctly so Story 2 can keep the wizard out of the auto-trap
		// loop on the GET /init/status read path (V2-cleanup-51). The POST
		// /init runner treats this the same as "partial" (a Fail), so this
		// distinction only affects the read path that feeds the wizard.
		return RepoStateUnreachable, argoDetail, status
	case bootstrapAuthFailed:
		// A 401 on the LIST means the token itself is invalid/expired — a
		// credential problem, not a broken bootstrap. Report it distinctly
		// so the wizard never claims to know anything about the app's
		// health, which it never got to check (error review package 1).
		return RepoStateAuthFailed, argoDetail, status
	case bootstrapUnknown:
		// Sharko could not determine the bootstrap app's health at all —
		// the LIST call failed for a reason that is neither a permission
		// problem nor a bad credential (or no ArgoCD client is configured).
		// This must NOT fold into "partial": "partial" asserts the app is
		// absent or degraded, a fact Sharko never established here.
		return RepoStateUnknown, argoDetail, status
	default:
		// "absent" and "unhealthy" both mean: the repo has bootstrap files but
		// ArgoCD is not (yet) running a healthy bootstrap. The wizard offers
		// init/repair. Never an RBAC message here (V2-cleanup-11.2). w2-q2:
		// POST /init tells these two apart via bootstrapStatus — "absent" is
		// a real repair (no PR), "unhealthy" stays a refusal.
		return RepoStatePartial, argoDetail, status
	}
}

// handleInitStatus godoc
//
// @Summary Probe GitOps repo initialization state
// @Description Read-only probe used by the first-run wizard before it offers to initialize the repo. Returns "empty" when the bootstrap root-app YAML is not present on the base branch, "initialized" when it is present and the ArgoCD bootstrap application is Synced + Healthy, "forbidden" when the file is present but ArgoCD rejected the read with a 403 because the token lacks RBAC permission (detail carries an actionable permission message), "auth_failed" when the file is present but ArgoCD rejected the read with a 401 because the token is invalid or expired (detail carries an actionable token message), "unreachable" when the file is present but the ArgoCD bootstrap reports Sync=Unknown because ArgoCD cannot reach/evaluate the Git repo (a connection problem re-init cannot fix), "partial" when the file is present but the ArgoCD bootstrap is missing or genuinely degraded (detail carries the ArgoCD diagnostic), and "unknown" when the file is present but Sharko could not determine the bootstrap application's health at all (the ArgoCD read failed for a reason that is neither a permission problem nor an invalid token, or no ArgoCD connection is configured) — this state never claims the bootstrap is missing or degraded, only that Sharko could not check. When state is "partial", repairable is true only if the bootstrap application was simply never created (POST /init can repair it with no PR) and false if it already exists but is degraded (re-init cannot fix a live app). Performs no writes and creates no operation session. Requires an active Git connection.
// @Tags init
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.InitStatusResponse "Repo state probe result"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "No active Git connection. A missing or broken ArgoCD connection does NOT 502 here — it surfaces as state \"unknown\" in a 200 response, since the Git-side probe can still run."
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

	// A missing/broken ArgoCD connection is NOT the same failure as a missing
	// Git connection: this probe can still answer "is the repo bootstrapped?"
	// from Git alone, it just can't classify the bootstrap app's health. The
	// documented "unknown" state exists exactly for this case (see
	// RepoStateUnknown and ProbeBootstrapApp's nil-client branch) — 502-ing
	// here instead of probing meant that documented state was unreachable in
	// practice, and the wizard's catch handler had to invent an "empty"
	// fallback that wrongly offered to set up an unchecked repo (review
	// findings r1, M11). Explicitly nil out ac on error rather than passing
	// the failed call's return value through: GetActiveOrchestratorArgocdClient
	// can return a non-nil interface wrapping a nil concrete client on error,
	// which would fail ProbeBootstrapApp's `ac == nil` check and panic on use.
	ac, acErr := s.connSvc.GetActiveOrchestratorArgocdClient()
	if acErr != nil {
		ac = nil
	}

	baseBranch := s.gitopsConfig().BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	state, detail, format, bootstrapStatus := probeRepoStateWithBootstrapStatus(r.Context(), gp, ac, baseBranch, s.repoPaths.ManagedClusters)
	repairable := state == RepoStatePartial && bootstrapStatus == bootstrapAbsent
	writeJSON(w, http.StatusOK, InitStatusResponse{
		State: state, Detail: detail, Format: format, Repairable: repairable,
	})
}
