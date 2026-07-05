package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// permissionDeniedDetail is the actionable message surfaced when ArgoCD answers
// the bootstrap-app probe with a 403. Phrased so a user with a scoped token
// understands the cause is RBAC on their token — NOT a broken bootstrap app.
const permissionDeniedDetail = "ArgoCD rejected Sharko's token (permission denied) — the token needs permission to read applications. Check your ArgoCD RBAC: the account needs role:admin (or at least applications:get)."

// handleInit godoc
//
// @Summary Initialize addons repository
// @Description Creates the GitOps repository structure and bootstraps ArgoCD with initial addon ApplicationSets.
// @Description Returns immediately with an operation_id; poll GET /api/v1/operations/{id} for progress.
// @Description If an existing "waiting" init session is found, returns that session (idempotent resume).
// @Tags init
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.InitRepoRequest false "Init request (defaults to bootstrap mode)"
// @Success 202 {object} map[string]interface{} "Operation accepted — poll operation_id for progress"
// @Success 200 {object} map[string]interface{} "Existing waiting session returned (already in progress)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /init [post]
func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "init") {
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	if s.templateFS == nil {
		writeError(w, http.StatusInternalServerError, "template filesystem not configured")
		return
	}

	var req orchestrator.InitRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default to bootstrap if no body provided.
		req = orchestrator.InitRepoRequest{BootstrapArgoCD: true}
	}

	// Resolve effective GitOps config — fall back to active connection's repo URL if not set via env.
	gitopsCfg := s.gitopsCfg
	conn, connErr := s.connSvc.GetActiveConnectionInfo()
	if connErr == nil {
		// Populate Git credentials for ArgoCD repository registration.
		if req.GitUsername == "" || req.GitToken == "" {
			username, token := extractGitCredentials(conn)
			if req.GitUsername == "" {
				req.GitUsername = username
			}
			if req.GitToken == "" {
				req.GitToken = token
			}
		}
		// Fall back to the connection's repo URL if env var was not set.
		if gitopsCfg.RepoURL == "" && conn.Git.RepoURL != "" {
			gitopsCfg.RepoURL = conn.Git.RepoURL
		}
	}

	// Check for an existing "waiting" init session — allow resume.
	// If one exists, return it so the client can continue polling.
	existing := s.opsStore.FindByTypeAndStatus("init", operations.StatusWaiting)
	if len(existing) > 0 {
		sess := existing[0]
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"operation_id": sess.ID,
			"status":       string(sess.Status),
			"wait_detail":  sess.WaitDetail,
			"wait_payload": sess.WaitPayload,
			"resumed":      true,
		})
		return
	}

	// Also check for a still-running init (avoid duplicate launches).
	running := s.opsStore.FindByTypeAndStatus("init", operations.StatusRunning)
	if len(running) > 0 {
		sess := running[0]
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"operation_id": sess.ID,
			"status":       string(sess.Status),
			"resumed":      true,
		})
		return
	}

	// Create a new operation session.
	steps := []string{
		"Creating bootstrap files",
		"Pushing to branch",
		"Creating pull request",
		"Waiting for PR merge",
		"Bootstrapping ArgoCD",
		"Waiting for sync",
	}
	session := s.opsStore.Create("init", steps)

	// Run init asynchronously — use a background context so the goroutine
	// outlives the HTTP request.
	bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	// NO defer cancel() here — the goroutine owns the context

	go func() {
		defer cancel()
		s.runInitOperation(bgCtx, session.ID, req, gitopsCfg, gp, ac, s.templateFS)
	}()

	audit.Enrich(r.Context(), audit.Fields{
		Event: "init_run",
	})
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"operation_id": session.ID,
		"status":       "pending",
	})
}

// runInitOperation is the background goroutine that performs the full init flow.
// It advances steps via opsStore and sets waiting/complete/fail states.
func (s *Server) runInitOperation(
	ctx context.Context,
	sessionID string,
	req orchestrator.InitRepoRequest,
	gitopsCfg orchestrator.GitOpsConfig,
	gp gitprovider.GitProvider,
	ac orchestrator.ArgocdClient,
	templateFS fs.FS,
) {
	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, gp, gitopsCfg, s.repoPaths, templateFS)
	s.attachPRTracker(orch)

	s.opsStore.Start(sessionID)

	// When the bootstrap root-app YAML is already present on the base
	// branch, the user is retrying an already-completed init. The probe
	// path comes from orchestrator.BootstrapRootAppPath — the same
	// constant CollectBootstrapFiles emits to.
	//
	// Probe ArgoCD to disambiguate:
	//   * Synced + Healthy   → idempotent success. Mark every step
	//     "already initialized" and Complete the session — the wizard's
	//     existing done-state UI does the right thing without changes.
	//   * Missing / Degraded → real partial state. Fail with a descriptive
	//     error so the user can decide (delete the orphaned repo files,
	//     manually re-create the ArgoCD app, etc.).
	//
	// We deliberately do NOT just blindly Complete on file-exists alone —
	// that would re-introduce a different false-success bug if the user
	// manually deleted the ArgoCD app and the wizard reported "all good"
	// while their cluster has nothing running.
	//
	// The classify-by-state decision is shared with the read-only
	// GET /api/v1/init/status probe via probeRepoState, so the two paths
	// can never disagree about what "already initialized" means.
	switch state, detail := probeRepoState(ctx, gp, ac, gitopsCfg.BaseBranch); state {
	case RepoStateInitialized:
		// Advance every step as already-completed so the wizard's
		// step-list UI shows a clean checkmarked sequence. We know the
		// step count from the Create() call above (6 steps); the
		// helper paginates by reading session state so it stays in
		// sync if the step list ever changes.
		markAllStepsAlreadyInitialized(s.opsStore, sessionID)
		s.opsStore.Complete(sessionID,
			"repo already initialized — ArgoCD bootstrap detected and healthy")
		return
	case RepoStateForbidden:
		// A 403 from ArgoCD is an RBAC problem with the token, not a broken
		// bootstrap — report the actionable permission message verbatim
		// instead of mislabeling it as "missing or unhealthy" (V2-cleanup-10).
		s.opsStore.Fail(sessionID, detail)
		return
	case RepoStatePartial, RepoStateUnreachable:
		// Both mean: repo files exist but the ArgoCD bootstrap is not healthy.
		// On the POST /init (repair) path we Fail identically — re-bootstrapping
		// an already-seeded repo is not the move, and a Sync=Unknown
		// (unreachable) repo can't be repaired by re-init either. The
		// unreachable/partial distinction is only surfaced on the read-only
		// GET /init/status path (which feeds the wizard); here we MUST NOT fall
		// through to the bootstrap flow, so we keep both in this Fail branch
		// (V2-cleanup-51).
		s.opsStore.Fail(sessionID,
			fmt.Sprintf("repo initialized but ArgoCD bootstrap is missing or unhealthy: %s",
				detail))
		return
	}
	// RepoStateEmpty falls through to the normal bootstrap flow below.

	// Step 1: Creating bootstrap files
	files, filesErr := orch.CollectBootstrapFiles(ctx)
	if filesErr != nil {
		s.opsStore.UpdateStep(sessionID, operations.StatusFailed, filesErr.Error())
		s.opsStore.Fail(sessionID, "failed to collect bootstrap files: "+filesErr.Error())
		return
	}
	s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, fmt.Sprintf("%d files prepared", len(files)))

	// Step 2: Pushing to branch — handled inside CommitBootstrapFiles (creates branch + commits).
	branch, pushErr := orch.CommitBootstrapFiles(ctx, files)
	if pushErr != nil {
		s.opsStore.UpdateStep(sessionID, operations.StatusFailed, pushErr.Error())
		s.opsStore.Fail(sessionID, "failed to push bootstrap files: "+pushErr.Error())
		return
	}
	s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "branch: "+branch)

	// Step 3: Creating pull request.
	gitResult, prErr := orch.CreateInitPR(ctx, branch)
	if prErr != nil {
		s.opsStore.UpdateStep(sessionID, operations.StatusFailed, prErr.Error())
		s.opsStore.Fail(sessionID, "failed to create pull request: "+prErr.Error())
		return
	}
	s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, gitResult.PRUrl)

	// Step 4: Wait for PR merge. Per-request auto_merge override wins
	// over the connection-level PRAutoMerge default; nil falls back.
	shouldAutoMerge := orchestrator.ResolveAutoMerge(req.AutoMerge, gitopsCfg.PRAutoMerge)
	if shouldAutoMerge {
		if mergeErr := gp.MergePullRequest(ctx, gitResult.PRID); mergeErr != nil {
			s.opsStore.UpdateStep(sessionID, operations.StatusFailed, mergeErr.Error())
			s.opsStore.Fail(sessionID, "PR auto-merge failed: "+mergeErr.Error())
			return
		}
		s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "PR merged (auto)")
		// Clean up branch after merge (best-effort). DeleteBranch
		// failures (e.g. AzureDevOps "not yet implemented", branch
		// already deleted) are logged but never fail the operation.
		if delErr := gp.DeleteBranch(ctx, branch); delErr != nil {
			slog.Warn("failed to delete branch after merge", "branch", branch, "error", delErr)
		}
	} else {
		// Set session to waiting — client polls for merge.
		s.opsStore.SetWaiting(sessionID, "Waiting for PR to be merged", gitResult.PRUrl)

		// Poll in background until merged or abandoned.
		merged := s.pollPRMerge(ctx, sessionID, gp, gitopsCfg.BaseBranch)
		if !merged {
			// Check if session was cancelled.
			sess, _ := s.opsStore.Get(sessionID)
			if sess != nil && sess.Status == operations.StatusCancelled {
				return
			}
			s.opsStore.Fail(sessionID, "PR merge timed out or session abandoned")
			return
		}
		s.opsStore.ResumeFromWaiting(sessionID)
		s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "PR merged")
	}

	// Step 5: Bootstrap ArgoCD.
	if req.BootstrapArgoCD && ac != nil {
		// Add repository to ArgoCD.
		if req.GitUsername != "" && req.GitToken != "" {
			if addRepoErr := ac.AddRepository(ctx, gitopsCfg.RepoURL, req.GitUsername, req.GitToken); addRepoErr != nil {
				slog.Warn("failed to add repository to ArgoCD", "error", addRepoErr)
				// Non-fatal — continue with bootstrap.
			}
		}

		rootAppContent, readErr := orch.ReadRootAppTemplate(ctx)
		if readErr != nil {
			s.opsStore.UpdateStep(sessionID, operations.StatusFailed, readErr.Error())
			s.opsStore.Fail(sessionID, "failed to read root-app template: "+readErr.Error())
			return
		}

		if bootstrapErr := orch.BootstrapArgoCD(ctx, rootAppContent); bootstrapErr != nil {
			s.opsStore.UpdateStep(sessionID, operations.StatusFailed, bootstrapErr.Error())
			s.opsStore.Fail(sessionID, "ArgoCD bootstrap failed: "+bootstrapErr.Error())
			return
		}
		s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "ArgoCD bootstrapped")

		// Step 6: Wait for sync. The canonical bootstrap app name is
		// verified by templates_test.go to match metadata.name in
		// templates/bootstrap/root-app.yaml — drift breaks first-run init.
		syncStatus, syncErr := orch.WaitForSync(ctx, orchestrator.BootstrapRootAppName, 2*time.Minute)
		detail := syncStatus
		if syncErr != "" {
			detail = syncStatus + ": " + syncErr
		}
		if syncStatus != "synced" {
			// A sync timeout/failure must Fail the operation, not
			// Complete it. The wizard treats `completed` as success
			// and would otherwise show "Repository initialized
			// successfully" while ArgoCD silently never reached Synced.
			s.opsStore.UpdateStep(sessionID, operations.StatusFailed, detail)
			s.opsStore.Fail(sessionID, fmt.Sprintf(
				"argocd application %q did not reach synced state: %s",
				orchestrator.BootstrapRootAppName, detail))
			return
		}
		s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "synced")
		s.opsStore.Complete(sessionID, "init complete")
	} else {
		// Skip steps 5 and 6 — advance them as skipped.
		s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "skipped")
		s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "skipped")
		s.opsStore.Complete(sessionID, "init complete (no ArgoCD bootstrap)")
	}

	s.auditLog.Add(audit.Entry{
		Level:    "info",
		Event:    "init",
		User:     "sharko",
		Action:   "init",
		Resource: "addons repository initialized and ArgoCD bootstrapped",
		Source:   "api",
		Result:   "success",
	})
}

// pollPRMergeInterval is the cadence at which pollPRMerge probes the
// base branch for the merged bootstrap file. The probe is a single
// GitHub file-read per cycle so there's no rate-limit risk; 5s keeps
// the manual-merge → wizard-advance gap snappy. Exposed as a package
// var (not const) so tests can inject a smaller value; production code
// never assigns to it.
var pollPRMergeInterval = 5 * time.Second

// isPRMerged returns true when the bootstrap root-app YAML is readable from
// `baseBranch`. We use file presence as the merge signal (rather than the
// PR-status API) because GitHub eventually-consistent state lags PR merges
// by 1–2s in practice, and the file-presence probe is what the next
// orchestrator step (BootstrapArgoCD) actually depends on.
//
// The probe path is orchestrator.BootstrapRootAppPath — the same
// constant CollectBootstrapFiles emits to. The helper exists so
// pollPRMerge can run an immediate first probe before entering the
// ticker loop; otherwise an already-merged PR would look like the
// wizard was hanging for the first ticker interval.
func isPRMerged(ctx context.Context, gp gitprovider.GitProvider, baseBranch string) bool {
	_, err := gp.GetFileContent(ctx, orchestrator.BootstrapRootAppPath, baseBranch)
	return err == nil
}

// pollPRMerge polls for the PR to be merged by checking whether the bootstrap
// file appears on the base branch. Returns true if merged, false if timed out
// or the session was abandoned/cancelled.
//
// We do an immediate file-presence check before entering the ticker
// loop. If the user merged the PR before pollPRMerge even started — or
// auto-merge raced ahead of the goroutine — we return true with no
// ticker wait. The ticker (5s, see pollPRMergeInterval) drives
// subsequent checks plus the heartbeat / cancellation / deadline
// guards.
func (s *Server) pollPRMerge(ctx context.Context, sessionID string, gp gitprovider.GitProvider, baseBranch string) bool {
	// Immediate first probe — skip the ticker wait if the file is already
	// on the base branch. Most-common paths this protects:
	//   - User merged the PR in their browser before the wizard's polling
	//     UI even rendered the "Waiting for PR merge…" panel.
	//   - A previous init crashed/restarted between PR-merge and the next
	//     step; on retry, the file is already there.
	if isPRMerged(ctx, gp, baseBranch) {
		return true
	}

	// Allow up to 24 hours for a human to merge the PR.
	deadline := time.Now().Add(24 * time.Hour)
	ticker := time.NewTicker(pollPRMergeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				return false
			}

			// Check if session is still alive (client must send heartbeats).
			sess, ok := s.opsStore.Get(sessionID)
			if !ok {
				return false
			}
			if sess.Status == operations.StatusCancelled {
				return false
			}
			if !sess.IsAlive(2 * time.Minute) {
				slog.Info("init operation abandoned — no heartbeat from client", "session_id", sessionID)
				return false
			}

			// Check if PR is merged by seeing if the bootstrap root-app YAML
			// (orchestrator.BootstrapRootAppPath) exists on the base branch.
			if isPRMerged(ctx, gp, baseBranch) {
				return true // file exists on base branch — PR was merged
			}

		case <-ctx.Done():
			return false
		}
	}
}

// Bootstrap-app probe statuses returned by ProbeBootstrapApp. These are the
// intermediate classifications probeRepoState maps onto the wire RepoState*
// values the wizard consumes.
const (
	// bootstrapHealthy — the app is present, Sync=Synced, Health=Healthy.
	bootstrapHealthy = "healthy"
	// bootstrapUnhealthy — the app is present but not Synced+Healthy, AND
	// ArgoCD WAS able to read the repo (e.g. Sync=OutOfSync, Health=Degraded).
	// This is a genuinely degraded bootstrap — ArgoCD compared the live tree
	// against the source and found a fixable problem, so re-running Initialize
	// (repair) may help.
	bootstrapUnhealthy = "unhealthy"
	// bootstrapUnreachable — the app is present but ArgoCD could NOT reach or
	// evaluate the repo: Sync=Unknown is ArgoCD's standard semantic for
	// "couldn't compare the working tree against the source" (repo-server can't
	// reach the Git host, e.g. a TLS-inspection proxy the repo-server doesn't
	// trust). This is a CONNECTION problem, not a degraded-deployment problem —
	// re-running Initialize cannot fix it, so the wizard must NOT auto-trap the
	// user in a re-bootstrap loop (V2-cleanup-51). Distinct from
	// bootstrapUnhealthy (OutOfSync/Degraded), which IS repair-able.
	bootstrapUnreachable = "unreachable"
	// bootstrapAbsent — the app is genuinely not created on this ArgoCD yet
	// (LIST succeeded, the app is not in the results). NOT a permission
	// problem and NOT an unhealthy app — the repo has bootstrap files but
	// ArgoCD has not been pointed at them yet, so the right move is to offer
	// init/repair, never an RBAC message (V2-cleanup-11.2).
	bootstrapAbsent = "absent"
	// bootstrapForbidden — ArgoCD rejected the LIST itself with a 403; the
	// token genuinely lacks permission to read applications (V2-cleanup-10).
	bootstrapForbidden = "forbidden"
)

// ProbeBootstrapApp checks whether the canonical ArgoCD root application
// (orchestrator.BootstrapRootAppName) exists and is Synced + Healthy.
//
// It LISTs applications and filters by name rather than GET-by-name. ArgoCD
// answers a GET on a non-existent application with HTTP 403 (not 404) for
// apiKey tokens — even a full-admin grant 403s on GET-of-a-missing-app while
// LIST returns 200 + empty. GET-by-name therefore could not distinguish "app
// absent" from "token forbidden", which made init abort with a bogus RBAC
// message whenever a populated repo was pointed at a fresh ArgoCD
// (V2-cleanup-11.2). LIST-and-filter removes that ambiguity:
//
//   - LIST ok, app present, Synced+Healthy         → ("healthy",     "")
//   - LIST ok, app present, Sync=Unknown            → ("unreachable", <detail>)
//   - LIST ok, app present, otherwise not S+H       → ("unhealthy",   <detail>)
//   - LIST ok, app absent (not in results)          → ("absent",      <detail>)
//   - LIST itself 403 (ErrPermissionDenied)         → ("forbidden",   <permMsg>)
//   - LIST fails for any other reason               → ("unhealthy",   <detail>)
//
// The unreachable vs unhealthy split (V2-cleanup-51) keys off the bootstrap
// app's Sync status: ArgoCD reports Sync=Unknown exactly when its repo-server
// could not compare the live tree against the Git source (repo unreachable /
// comparison error). That is a CONNECTION problem re-init cannot fix. Any other
// non-(Synced+Healthy) combination (OutOfSync/Degraded/Progressing) means
// ArgoCD DID read the repo and found a fixable problem → "unhealthy".
//
// Used to disambiguate "repo file exists" between idempotent-success and
// partial-state on first-run init retry. Exported so the /repo/status handler
// can reuse the same probe semantics — the wizard gate reads `bootstrap_synced`
// from /repo/status to auto-open the wizard when the bootstrap is
// absent/degraded. ("forbidden", "unhealthy", "unreachable", and "absent" are
// all non-healthy, so that gate keeps treating them as not-synced.)
func ProbeBootstrapApp(ctx context.Context, ac orchestrator.ArgocdClient) (status, detail string) {
	if ac == nil {
		return bootstrapUnhealthy, "no ArgoCD client configured"
	}
	apps, err := ac.ListApplications(ctx)
	if err != nil {
		// A 403 on the LIST is a genuine RBAC problem with the token — surface
		// it distinctly so the user fixes their ArgoCD permissions instead of
		// chasing a phantom bootstrap failure (V2-cleanup-10).
		if errors.Is(err, argocd.ErrPermissionDenied) {
			return bootstrapForbidden, permissionDeniedDetail
		}
		return bootstrapUnhealthy, fmt.Sprintf("listing argocd applications failed: %v", err)
	}

	var found *models.ArgocdApplication
	for i := range apps {
		if apps[i].Name == orchestrator.BootstrapRootAppName {
			found = &apps[i]
			break
		}
	}
	if found == nil {
		// LIST succeeded but the bootstrap app is not there — it simply has
		// not been created on this cluster yet. Offer init/repair; do NOT
		// report an RBAC or unhealthy condition.
		return bootstrapAbsent, fmt.Sprintf("argocd app %q is not created on this cluster yet",
			orchestrator.BootstrapRootAppName)
	}
	if found.SyncStatus != "Synced" || found.HealthStatus != "Healthy" {
		// Build the base detail with sync and health status (V2-cleanup-51.1
		// test asserts sync= and health= are present; do not reorder them).
		detail := fmt.Sprintf("argocd app %q sync=%s health=%s",
			orchestrator.BootstrapRootAppName, found.SyncStatus, found.HealthStatus)
		// Append repo URL when available so the bell alert "ArgoCD can't sync
		// the repo" names WHICH repo is failing (V2-cleanup-52). Empty URL
		// produces no trailing artifact.
		if found.SourceRepoURL != "" {
			detail += " repo=" + found.SourceRepoURL
		}
		// Predicate (locked, V2-cleanup-51.1): Sync=Unknown ⟺ ArgoCD's
		// repo-server could not reach/evaluate the repo (comparison error /
		// unreachable Git host). Classify that as unreachable — a connection
		// problem re-init cannot repair — distinct from a genuinely degraded
		// bootstrap (OutOfSync/Degraded), which stays "unhealthy".
		if found.SyncStatus == "Unknown" {
			return bootstrapUnreachable, detail
		}
		return bootstrapUnhealthy, detail
	}
	return bootstrapHealthy, ""
}

// markAllStepsAlreadyInitialized walks the session's steps and marks each as
// completed with the detail "already initialized". Used when the repo + ArgoCD
// bootstrap already exist on a healthy cluster and the user is retrying init.
//
// We can't just call s.opsStore.Complete() — the wizard's step UI expects to
// see each step transition through completed-with-detail, otherwise the
// "Steps:" panel renders blank/pending while the overall status is
// "completed", which is more confusing than helpful.
func markAllStepsAlreadyInitialized(store *operations.Store, sessionID string) {
	sess, ok := store.Get(sessionID)
	if !ok {
		return
	}
	// UpdateStep advances internally; one call per step is correct.
	for range sess.Steps {
		store.UpdateStep(sessionID, operations.StatusCompleted, "already initialized")
	}
}

// extractGitCredentials returns (username, token) from the active connection's Git config.
// Credentials come from the active connection only — no env var fallback.
func extractGitCredentials(conn *models.Connection) (string, string) {
	switch conn.Git.Provider {
	case models.GitProviderGitHub:
		if conn.Git.Token != "" {
			return "x-access-token", conn.Git.Token
		}
	case models.GitProviderAzureDevOps:
		if conn.Git.PAT != "" {
			return conn.Git.Organization, conn.Git.PAT
		}
	}
	return "", ""
}
