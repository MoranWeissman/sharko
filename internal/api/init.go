package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/operations"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

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
	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, gp, gitopsCfg, s.repoPaths, templateFS)

	s.opsStore.Start(sessionID)

	// Check for already-initialized repo before doing any work.
	if _, checkErr := gp.GetFileContent(ctx, "bootstrap/root-app.yaml", gitopsCfg.BaseBranch); checkErr == nil {
		s.opsStore.Fail(sessionID, "repo already initialized: bootstrap/root-app.yaml exists")
		return
	}

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

	// Step 4: Wait for PR merge.
	// If auto_merge is set (or global PRAutoMerge is enabled), merge immediately.
	shouldAutoMerge := req.AutoMerge || gitopsCfg.PRAutoMerge
	if shouldAutoMerge {
		if mergeErr := gp.MergePullRequest(ctx, gitResult.PRID); mergeErr != nil {
			s.opsStore.UpdateStep(sessionID, operations.StatusFailed, mergeErr.Error())
			s.opsStore.Fail(sessionID, "PR auto-merge failed: "+mergeErr.Error())
			return
		}
		s.opsStore.UpdateStep(sessionID, operations.StatusCompleted, "PR merged (auto)")
		// Clean up branch after merge (best-effort).
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

		// Step 6: Wait for sync.
		// V124-14 / BUG-031: poll the canonical bootstrap app name. The
		// constant is verified by templates_test.go to match the value of
		// metadata.name in templates/bootstrap/root-app.yaml — drift in
		// either direction breaks first-run init.
		syncStatus, syncErr := orch.WaitForSync(ctx, orchestrator.BootstrapRootAppName, 2*time.Minute)
		detail := syncStatus
		if syncErr != "" {
			detail = syncStatus + ": " + syncErr
		}
		if syncStatus != "synced" {
			// V124-14 / BUG-032: a sync timeout/failure must Fail the
			// operation, not Complete it. The wizard treats `completed` as
			// success and would otherwise show "Repository initialized
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

// pollPRMerge polls for the PR to be merged by checking whether the bootstrap
// file appears on the base branch. Returns true if merged, false if timed out
// or the session was abandoned/cancelled.
func (s *Server) pollPRMerge(ctx context.Context, sessionID string, gp gitprovider.GitProvider, baseBranch string) bool {
	// Allow up to 24 hours for a human to merge the PR.
	deadline := time.Now().Add(24 * time.Hour)
	ticker := time.NewTicker(10 * time.Second)
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

			// Check if PR is merged by seeing if bootstrap/root-app.yaml exists on the base branch.
			_, err := gp.GetFileContent(ctx, "bootstrap/root-app.yaml", baseBranch)
			if err == nil {
				return true // file exists on base branch — PR was merged
			}

		case <-ctx.Done():
			return false
		}
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
