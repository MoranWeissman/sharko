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
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// permissionDeniedDetail is the actionable message surfaced when ArgoCD answers
// the bootstrap-app probe with a 403. Phrased so a user with a scoped token
// understands the cause is RBAC on their token — NOT a broken bootstrap app.
const permissionDeniedDetail = "ArgoCD rejected Sharko's token (permission denied) — the token needs permission to read applications. Check your ArgoCD RBAC: the account needs role:admin (or at least applications:get)."

// The sentences below are the whole of what the init handlers and the
// bootstrap probe are allowed to say when something upstream of Sharko fails
// (B4).
//
// # What they replaced, and why it had to go
//
// Every one of these places used to hand the underlying error's own words
// straight to the caller — a short prefix of Sharko's own followed by
// err.Error(), or fmt.Sprintf("...: %v", err). That text is written by
// somebody else: the Git
// library, the ArgoCD server, the Go HTTP transport, the credentials store.
// It is not Sharko's to pass on, and on the Git side it can carry a secret
// outright. A repository URL is often written with the token inside it
// (https://x-access-token:<token>@host/org/repo.git), and net/url's own parse
// error quotes the URL it failed on in full — so one unparseable repo URL in
// the saved connection put the token into the body of a 502 that any signed-in
// viewer could ask for.
//
// # The rule these follow
//
// The sentence is a fixed constant, chosen by WHICH failure this is, and it
// never contains a fragment of the underlying error. That is unconditional: it
// does not depend on today's trace showing that a particular backend cannot
// reach a particular line. The classification is by type — errors.Is against a
// sentinel — never by reading an error's words, because a text match stops
// matching the day the other side rephrases itself.
//
// # They stay three different answers on purpose
//
// permissionDeniedDetail (403), tokenInvalidDetail (401) and
// bootstrapProbeFailedDetail ("Sharko could not check") are three separate
// sentences because they need three separate fixes: widen the token's ArgoCD
// permissions, replace a dead token, or go and look at whether ArgoCD is
// reachable. Collapsing them into one vague sentence would swap a leak for a
// different kind of dishonesty — telling an operator "we don't know" when
// Sharko does in fact know.
const (
	// The two sentences that used to be declared HERE — the ones for "no
	// usable Git connection" and "no usable ArgoCD connection" — moved to
	// internal/credsafe in B1 (credsafe.NoActiveGitConnectionMessage and
	// credsafe.NoActiveArgocdConnectionMessage). Sixty-four more call sites in
	// this package and four in internal/remediation say them now, and
	// internal/remediation cannot import internal/api. One owner, not two
	// copies: two copies of a fixed sentence is how two sentences drift apart.

	// tokenInvalidDetail is said when ArgoCD answers the bootstrap probe with
	// a 401. The token Sharko holds is not accepted at all, so nothing was
	// learned about the bootstrap application.
	tokenInvalidDetail = "ArgoCD rejected Sharko's token (the token is not valid, or it has expired). Create a new ArgoCD token and save it in Settings."

	// bootstrapProbeFailedDetail is said when the ArgoCD read failed for a
	// reason that is neither a permission problem nor a bad token. Sharko
	// genuinely does not know the bootstrap application's state, and the
	// sentence says exactly that and nothing more.
	bootstrapProbeFailedDetail = "Sharko could not reach ArgoCD to check the bootstrap application, so it does not know whether the bootstrap is healthy. Check that the ArgoCD server address in Settings is right and that Sharko can reach it."

	// noArgocdClientDetail is said when there is no ArgoCD connection to ask
	// in the first place. Kept apart from bootstrapProbeFailedDetail because
	// "there is nothing configured" and "the thing that is configured did not
	// answer" send an operator to two different screens.
	noArgocdClientDetail = "No ArgoCD connection is configured, so Sharko did not check the bootstrap application."
)

// argocdSyncStatuses and argocdHealthStatuses are the values ArgoCD is known
// to report, and the only ones Sharko will repeat back in the diagnostic
// detail string.
//
// The status fields are read off ArgoCD's API response, so they are somebody
// else's text arriving over the wire, and the detail string they are pasted
// into is shown to a person. They are a closed set in practice, which is what
// makes an allow-list the honest fix here: every real value still comes
// through unchanged, so the operator loses nothing, and a value nobody has
// seen before is named as unrecognised instead of being echoed.
var (
	argocdSyncStatuses = map[string]bool{
		"Synced": true, "OutOfSync": true, "Unknown": true,
	}
	argocdHealthStatuses = map[string]bool{
		"Healthy": true, "Progressing": true, "Degraded": true,
		"Suspended": true, "Missing": true, "Unknown": true, "Error": true,
	}
)

// unrecognisedArgocdStatus is what the detail string says in place of a status
// value that is not in the allow-list above. It is a fixed word, so it cannot
// carry anything ArgoCD sent.
const unrecognisedArgocdStatus = "unrecognised"

// knownArgocdStatus returns value when it is one ArgoCD is known to report,
// and the fixed unrecognisedArgocdStatus otherwise.
func knownArgocdStatus(known map[string]bool, value string) string {
	if known[value] {
		return value
	}
	return unrecognisedArgocdStatus
}

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
		// B4: the error's own words never go out. See the sentence block at
		// the top of this file for why, and for what is lost by not sending
		// them (nothing the operator can act on).
		// B1 routed this through the shared gate, which logs and writes the
		// same fixed sentence for all sixty-six sites.
		writeNoActiveArgocdConnection(w, r)
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
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
	gitopsCfg := s.gitopsConfig()
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
	//
	// The probe now recognises a v3 repo (no engine pin, but v3 markers
	// present) as initialized too, so the async runner takes the same
	// "already set up, do not re-bootstrap" branch a v4 repo does — rather
	// than falling through and seeding the v4 folder tree over a live v3
	// repo. InitRepo refuses that case as well; this is the earlier, nicer
	// stop.
	//
	// w2-q2: RepoStatePartial used to be a blanket Fail — the wizard's
	// banner said "Re-run initialize to repair it" but the backend refused
	// every time, a dead end. It now splits on the raw bootstrap status
	// (bootstrapStatus, from probeRepoStateWithBootstrapStatus):
	//   - bootstrapAbsent   → REAL repair: files are fine, only the ArgoCD
	//     bootstrap Application is missing. No git writes, no PR, no
	//     re-seeding — just (re-)create the Application and wait for sync.
	//   - bootstrapUnhealthy → the Application already exists but is
	//     degraded. Re-running Initialize cannot fix a live app, so this
	//     stays a refusal — just with a clearer message.
	// RepoStateUnreachable (Sync=Unknown, a connection problem) also stays a
	// refusal — re-init cannot repair a repo ArgoCD can't reach.
	switch state, detail, _, bootstrapStatus := probeRepoStateWithBootstrapStatus(ctx, gp, ac, gitopsCfg.BaseBranch, s.repoPaths.ManagedClusters); state {
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
	case RepoStatePartial:
		if bootstrapStatus == bootstrapAbsent {
			// REPAIR: the repo files are already in place — the only thing
			// missing is the ArgoCD bootstrap Application itself. Mark the
			// git-side steps (1-4: files/push/PR/wait-for-merge) as
			// already-done — no git writes, no PR, no re-seeding — then run
			// only the ArgoCD bootstrap + wait-for-sync steps (5-6).
			markGitStepsAlreadyDone(s.opsStore, sessionID,
				"already initialized — repairing ArgoCD bootstrap only")
			if ac == nil {
				s.opsStore.Fail(sessionID, "cannot repair: no ArgoCD client configured")
				return
			}
			if !s.runBootstrapSteps(ctx, sessionID, orch, req, gitopsCfg, ac) {
				return
			}
			s.auditLog.Add(audit.Entry{
				Level:    "info",
				Event:    "init_repair",
				User:     "sharko",
				Action:   "init_repair",
				Resource: "argocd bootstrap application created (repair, no git changes)",
				Source:   "api",
				Result:   "success",
			})
			s.opsStore.Complete(sessionID, "repaired — the ArgoCD bootstrap application was created")
			return
		}
		// bootstrapUnhealthy: the Application already exists but is
		// degraded (OutOfSync/Degraded, or the probe itself failed to
		// resolve one). Re-running Initialize cannot fix a live
		// application — refuse plainly instead of re-seeding anything.
		s.opsStore.Fail(sessionID, unhealthyRepairRefusalMessage(bootstrapStatus, detail))
		return
	case RepoStateUnreachable:
		// Sync=Unknown — ArgoCD's repo-server can't reach/evaluate the Git
		// repo. That is a connection problem; re-running Initialize cannot
		// fix it, so this stays a refusal (V2-cleanup-51).
		s.opsStore.Fail(sessionID, fmt.Sprintf(
			"repo initialized but ArgoCD cannot reach or evaluate the repository: %s", detail))
		return
	case RepoStateAuthFailed:
		// ArgoCD rejected Sharko's token outright (401) when probing the
		// bootstrap app. This is a credential problem, not a broken
		// bootstrap — refuse with the actionable token message rather than
		// asserting anything about the app's health, which Sharko never
		// got to check.
		s.opsStore.Fail(sessionID, detail)
		return
	case RepoStateUnknown:
		// Sharko could not determine the bootstrap app's health at all —
		// the LIST call itself failed for a reason that is neither a
		// permission problem nor a bad credential. Refuse honestly instead
		// of guessing "missing or unhealthy": we genuinely don't know.
		s.opsStore.Fail(sessionID, fmt.Sprintf(
			"repo initialized, but Sharko could not check whether the ArgoCD bootstrap application is healthy: %s", detail))
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

	// Step 5+6: Bootstrap ArgoCD and wait for sync.
	if req.BootstrapArgoCD && ac != nil {
		if !s.runBootstrapSteps(ctx, sessionID, orch, req, gitopsCfg, ac) {
			return
		}
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

// runBootstrapSteps performs the ArgoCD-bootstrap half of init (step 5:
// ReadRootAppTemplate + BootstrapArgoCD) and the wait-for-sync half (step 6),
// recording progress on the session's next two pending steps exactly as the
// original inline code did. Shared by two callers (w2-q2):
//   - the normal first-time-init flow, once the bootstrap PR has merged;
//   - the RepoStatePartial + bootstrapAbsent repair path in runInitOperation,
//     which skips straight to this without any git writes or PR.
//
// Returns true on success. On failure it has already recorded the failed
// step and called s.opsStore.Fail with a descriptive message — the caller
// must return immediately without calling Complete.
func (s *Server) runBootstrapSteps(
	ctx context.Context,
	sessionID string,
	orch *orchestrator.Orchestrator,
	req orchestrator.InitRepoRequest,
	gitopsCfg orchestrator.GitOpsConfig,
	ac orchestrator.ArgocdClient,
) bool {
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
		return false
	}

	if bootstrapErr := orch.BootstrapArgoCD(ctx, rootAppContent); bootstrapErr != nil {
		s.opsStore.UpdateStep(sessionID, operations.StatusFailed, bootstrapErr.Error())
		s.opsStore.Fail(sessionID, "ArgoCD bootstrap failed: "+bootstrapErr.Error())
		return false
	}
	s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "ArgoCD bootstrapped")

	// Wait for sync. The canonical bootstrap app name is verified by
	// templates_test.go to match metadata.name in
	// templates/bootstrap/root-app.yaml — drift breaks first-run init.
	syncStatus, syncErr := orch.WaitForSync(ctx, orchestrator.BootstrapRootAppName, 2*time.Minute)
	detail := syncStatus
	if syncErr != "" {
		detail = syncStatus + ": " + syncErr
	}
	if syncStatus != "synced" {
		// A sync timeout/failure must Fail the operation, not Complete it.
		// The wizard treats `completed` as success and would otherwise show
		// "Repository initialized successfully" while ArgoCD silently never
		// reached Synced.
		s.opsStore.UpdateStep(sessionID, operations.StatusFailed, detail)
		s.opsStore.Fail(sessionID, fmt.Sprintf(
			"argocd application %q did not reach synced state: %s",
			orchestrator.BootstrapRootAppName, detail))
		return false
	}
	s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "synced")
	return true
}

// unhealthyRepairRefusalMessage builds the POST /init failure message for
// the RepoStatePartial + bootstrapUnhealthy case (w2-q2). When the probe
// actually RESOLVED the bootstrap Application, the message says so explicitly:
// the app already exists, so re-running Initialize will not fix it, and it
// points at ArgoCD/diagnostics instead. When the probe never got far enough to
// resolve one (e.g. the ArgoCD LIST call itself failed for a non-permission
// reason), Sharko does not know whether the app exists — keep the original,
// more conservative wording rather than asserting something it cannot confirm.
//
// IT ASKS THE STATUS, NOT THE SENTENCE. This used to test the diagnostic
// string for the substring "sync=", which is the shape ProbeBootstrapApp
// happens to emit once it has found an Application. Reordering or rewording
// that diagnostic — its own comment already asks a reader not to reorder
// sync= and health= — would have flipped which claim this message makes about
// the operator's cluster, with nothing failing anywhere. bootstrapStatus is
// the fact the caller already holds; bootstrapAppResolved reads it.
func unhealthyRepairRefusalMessage(bootstrapStatus, argoDetail string) string {
	if !bootstrapAppResolved(bootstrapStatus) {
		return fmt.Sprintf("repo initialized but ArgoCD bootstrap is missing or unhealthy: %s", argoDetail)
	}
	return fmt.Sprintf(
		"%s — this ArgoCD application already exists, so re-running Initialize will not fix it. Check the application's sync and health status in ArgoCD directly, or use the diagnostics tools, before retrying.",
		argoDetail)
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
	// bootstrapAuthFailed — ArgoCD rejected the LIST itself with a 401; the
	// token Sharko is using is invalid or expired. This is the root cause of
	// the bug this const set was extended to fix: before this status existed,
	// a 401 fell through to bootstrapUnknown's predecessor (bootstrapUnhealthy)
	// and the wizard told the user their fully-working bootstrap app "already
	// exists but is not healthy" — a claim Sharko never actually verified,
	// because it never got past the token check. Distinct from
	// bootstrapForbidden (403 — the token IS valid but lacks RBAC permission):
	// here the credential itself is bad.
	bootstrapAuthFailed = "auth_failed"
	// bootstrapUnknown — Sharko could not determine the bootstrap app's health
	// at all: either no ArgoCD client is configured, or the LIST call failed
	// for a reason that is neither a permission problem (403) nor a bad
	// credential (401) — e.g. a network blip or a malformed response. This is
	// honestly "we don't know", NOT "it's broken" — callers must not treat it
	// as a repairable/degraded bootstrap (that would assert a fact Sharko
	// never observed).
	bootstrapUnknown = "unknown"
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
//   - LIST itself 401 (ErrTokenInvalid)             → ("auth_failed", <detail>)
//   - LIST fails for any other reason,
//     or no ArgoCD client is configured             → ("unknown",     <detail>)
//
// The "unknown" status matters as much as any other branch: it is the honest
// "Sharko could not check" answer, distinct from every other status which
// asserts something Sharko DID observe. Before this status existed, both the
// no-client case and "LIST failed for an unrecognized reason" (which is
// exactly what a 401 looked like before ErrTokenInvalid existed) fell into
// bootstrapUnhealthy — so an expired ArgoCD token made a fully-working
// install show the first-run wizard claiming the bootstrap app "already
// exists but is not healthy", a fact Sharko never actually verified.
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
// absent/degraded. ("forbidden", "unhealthy", "unreachable", "absent",
// "auth_failed", and "unknown" are all non-healthy, so that gate keeps
// treating them as not-synced.)
func ProbeBootstrapApp(ctx context.Context, ac orchestrator.ArgocdClient) (status, detail string) {
	if ac == nil {
		// No ArgoCD client at all — Sharko has nothing to ask, so this is
		// "couldn't check", not "broken" (see bootstrapUnknown doc).
		return bootstrapUnknown, noArgocdClientDetail
	}
	apps, err := ac.ListApplications(ctx)
	if err != nil {
		// A 403 on the LIST is a genuine RBAC problem with the token — surface
		// it distinctly so the user fixes their ArgoCD permissions instead of
		// chasing a phantom bootstrap failure (V2-cleanup-10).
		if errors.Is(err, argocd.ErrPermissionDenied) {
			return bootstrapForbidden, permissionDeniedDetail
		}
		// A 401 on the LIST means the token itself is invalid/expired — this
		// is the root-cause bug this status was added for: without it, a
		// dead token fell through to the catch-all below and got mislabeled
		// as a broken (but existing) bootstrap app.
		//
		// B4: this used to return err.Error(). The comment that stood here
		// claimed the error "already carries the full actionable message",
		// and by the time it reached this line that was not true — the real
		// client wraps the sentinel ("listing applications: %w"), and every
		// other implementation of orchestrator.ArgocdClient is free to wrap
		// it with whatever its transport produced. All this line ever knows
		// is that errors.Is found the sentinel, so the sentence is Sharko's,
		// fixed, and written here.
		if errors.Is(err, argocd.ErrTokenInvalid) {
			slog.Warn("bootstrap probe: argocd refused the token", "outcome", bootstrapAuthFailed)
			return bootstrapAuthFailed, tokenInvalidDetail
		}
		// Any other LIST failure (network blip, malformed response, etc.) —
		// Sharko genuinely does not know whether the bootstrap app is
		// healthy. Report that honestly instead of guessing "unhealthy".
		//
		// B4: this is the catch-all, so whatever the transport produced used
		// to ride out on it — the ArgoCD server address, a TLS chain, a DNS
		// answer, anything a future client wrapped in. The error value is not
		// logged either: this branch is reached by definition when Sharko
		// does not know what the error is, so it cannot know what is inside
		// it. The step name is enough to find the request in the log.
		slog.Warn("bootstrap probe: could not read applications from argocd", "outcome", bootstrapUnknown)
		return bootstrapUnknown, bootstrapProbeFailedDetail
	}

	// Look for the current bootstrap app name first, then the v3 one. A
	// cluster bootstrapped before the v4 rename still runs an Application
	// called "cluster-addons-bootstrap"; matching only the new name would
	// report that healthy v3 cluster as "bootstrap missing" and send the
	// user into the repair wizard for nothing.
	var found *models.ArgocdApplication
	var foundName string
	for _, want := range orchestrator.BootstrapAppNames() {
		for i := range apps {
			if apps[i].Name == want {
				found, foundName = &apps[i], want
				break
			}
		}
		if found != nil {
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
			foundName, knownArgocdStatus(argocdSyncStatuses, found.SyncStatus),
			knownArgocdStatus(argocdHealthStatuses, found.HealthStatus))
		// Append repo URL when available so the bell alert "ArgoCD can't sync
		// the repo" names WHICH repo is failing (V2-cleanup-52).
		//
		// B4: the URL is passed through credsafe.SafeRepoURL first, and this
		// is the leak that had nothing to do with an error at all. The value
		// is spec.source.repoURL copied verbatim out of ArgoCD's API answer,
		// and a repo URL is routinely written with the token inside it
		// (https://x-access-token:<token>@host/org/repo.git). So a degraded
		// bootstrap — an ordinary 200 response, no failure needed — put that
		// token in the detail string, which goes into the init-status body,
		// into the POST /init operation message, and into the bell alert.
		// SafeRepoURL keeps the host and path, which is all the alert needed
		// in order to name the repo, and returns "" for anything it cannot
		// take apart with confidence — in which case the repo is not named.
		if safeRepo := credsafe.SafeRepoURL(found.SourceRepoURL); safeRepo != "" {
			detail += " repo=" + safeRepo
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

// gitStepCount is the number of session steps that belong to the git side of
// init (see the `steps` slice built in handleInit): "Creating bootstrap
// files", "Pushing to branch", "Creating pull request", "Waiting for PR
// merge". The remaining two ("Bootstrapping ArgoCD", "Waiting for sync") are
// the ArgoCD side, driven by runBootstrapSteps.
const gitStepCount = 4

// markGitStepsAlreadyDone advances the git-side steps (the first
// gitStepCount of the session, or fewer if the step list is ever shorter) to
// completed with the given detail, without doing any real work. Used by the
// w2-q2 repair path: the repo files already exist (no git writes, no PR, no
// re-seeding needed), so only the ArgoCD-bootstrap steps that follow
// (runBootstrapSteps) do real work. Mirrors markAllStepsAlreadyInitialized's
// approach of walking UpdateStep one call per step so the wizard's step list
// renders a clean checkmarked sequence instead of blank/pending entries.
func markGitStepsAlreadyDone(store *operations.Store, sessionID, detail string) {
	sess, ok := store.Get(sessionID)
	if !ok {
		return
	}
	n := gitStepCount
	if n > len(sess.Steps) {
		n = len(sess.Steps)
	}
	for i := 0; i < n; i++ {
		store.UpdateStep(sessionID, operations.StatusCompleted, detail)
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
