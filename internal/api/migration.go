package api

// migration.go — the v3 -> v4 migration endpoints (v4 Wave 2, Stories 5.1
// and 5.2).
//
// Three doors, in the order a person uses them:
//
//	GET  /api/v1/migration/status   is there anything to migrate?
//	POST /api/v1/migration/preview  show me every file it would touch
//	POST /api/v1/migration/migrate  do it — one pull request, all or nothing
//
// The status probe is read-only and Viewer+, like every other repo-state
// probe. Preview and migrate are Admin: the migration rewrites the whole
// repository in one go, which is a bigger commitment than any single
// cluster or addon change, and the preview renders the content of every
// file including values.
//
// There is a fourth door a person normally never presses:
//
//	POST /api/v1/migration/complete  finish the ArgoCD side, if it stalled
//
// An ArgoCD client and ApplicationSet access ARE needed here, contrary to
// what this comment used to claim. The migration does not only touch Git.
// The ApplicationSets that keep a v3 fleet's addons running live in
// ArgoCD, and the migration pull request takes away everything they read —
// so they have to be made harmless before it is opened, and retired (with
// the engine started in their place) after it merges. See
// internal/orchestrator/migration_handoff.go for the whole sequence and
// the ArgoCD semantics it rests on.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// MigrationMigrateRequest is the request body for POST /api/v1/migration/migrate.
type MigrationMigrateRequest struct {
	// DryRun returns the plan with no side effects. Also settable via
	// ?dry_run=true.
	DryRun bool `json:"dry_run,omitempty"`
	// Yes is the confirmation every v4 write requires. Without it, a
	// non-dry-run call is refused with a 400 rather than silently
	// rewriting the repository.
	Yes bool `json:"yes"`
	// AutoMerge overrides the connection-level PRAutoMerge default for
	// this PR only. nil = fall back to the connection default.
	AutoMerge *bool `json:"auto_merge,omitempty"`
	// RuntimeHandoff controls the ArgoCD side of the migration. Leave it
	// empty and Sharko decides: it prepares the handoff whenever the repo
	// has clusters registered, and skips it when the repo has none. Send
	// "skip" to migrate the files only — the escape hatch for a repo with
	// nothing actually running.
	RuntimeHandoff string `json:"runtime_handoff,omitempty"`
}

// MigrationCompleteRequest is the request body for
// POST /api/v1/migration/complete. It has no fields today; the endpoint is
// idempotent and takes no options. Declared so the shape can grow without
// a breaking change.
type MigrationCompleteRequest struct{}

// writeMigrationError answers a migration failure with the RIGHT status
// code (review finding M-6).
//
// Everything the migration builder can refuse on is a property of the
// repo's own contents — an unparseable file, a cluster name the v4 layout
// cannot hold, a catalog entry that would not merge — and those are all
// caller-fixable, so 400 is right for them. But the SAME call also reads a
// dozen files from the git host, and when the git host is refusing
// connections, timing out, or rate-limiting us, answering 400 blames the
// person for their repo when the repo is fine. classifyUpstreamError knows
// the difference; anything it can positively identify as an upstream
// problem gets the upstream status code and plain words to match.
func writeMigrationError(w http.ResponseWriter, err error) {
	if code := classifyUpstreamError(err); code != http.StatusInternalServerError {
		writeError(w, code, "can't reach your git host right now: "+err.Error())
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// upstreamGitStatus is for errors that are ALREADY known to be git
// transport failures by construction — the layout probes only ever return
// an error for a read that failed with something other than "file not
// found". classifyUpstreamError sharpens that into 504 for a timeout or
// 429 for a rate limit where it can; where it cannot, 502 is the honest
// default, because "we could not reach the git host" is a gateway problem
// and never a 500 on our side.
func upstreamGitStatus(err error) int {
	if code := classifyUpstreamError(err); code != http.StatusInternalServerError {
		return code
	}
	return http.StatusBadGateway
}

// migrationOrchestrator builds a Git-only orchestrator for the migration
// paths, with the shipped curated catalog wired in — the delta conversion
// cannot tell a user-added addon from a Sharko-shipped one without it, and
// would carry the whole catalog across as if every entry were the user's.
// The ArgoCD client and the ApplicationSet manager are BOTH best-effort
// here, and each nil has a different, deliberate consequence:
//
//   - no ApplicationSet manager → prepareRuntimeHandoff refuses the
//     migration outright on any repo with clusters registered. That is the
//     safe direction: a repo-only migration on a live fleet is the exact
//     trap review finding B-1 describes.
//   - no ArgoCD client → the files migrate and the old ApplicationSets are
//     still made safe, but the last step (starting the engine) reports
//     itself as pending rather than complete.
func (s *Server) migrationOrchestrator(git gitprovider.GitProvider) *orchestrator.Orchestrator {
	// A missing ArgoCD connection is not fatal to a migration — see above.
	// The nil is assigned only on success: handing the interface a typed
	// nil pointer would make every `o.argocd == nil` check in the
	// orchestrator answer false and turn a clean "pending" report into a
	// nil-pointer panic.
	var ac orchestrator.ArgocdClient
	if client, acErr := s.connSvc.GetActiveArgocdClient(); acErr == nil && client != nil {
		ac = client
	}
	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsConfig(), s.repoPaths, nil)
	// s.catalog is optional (router.go). A nil catalog makes every addon
	// merge as catalog.OriginInternal — which for the migration means the
	// whole v3 catalog is carried across verbatim rather than reduced to a
	// delta. Lossless, just less tidy, and exactly what a server with no
	// shipped catalog loaded should do.
	orch.SetCuratedCatalog(s.catalog)
	if s.appSetManager != nil {
		orch.SetApplicationSetManager(s.appSetManager)
	}
	return orch
}

// handleMigrationStatus godoc
//
// @Summary Repo format and migration availability
// @Description Reports which data-file format the connected repo uses — "v3", "v4", or "empty" — and whether the one-pull-request migration is available. Read-only; never writes anything.
// @Tags migration
// @Produce json
// @Security BearerAuth
// @Success 200 {object} orchestrator.MigrationStatusResult "Repo format"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /migration/status [get]
func (s *Server) handleMigrationStatus(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "migration.status") {
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	status, err := s.migrationOrchestrator(git).MigrationStatus(r.Context())
	if err != nil {
		// The probe could not get an answer (review finding L-9). Saying so
		// is the whole point: the old code swallowed this and reported
		// format "empty", whose message tells the person to initialize the
		// repo — advice that seeds a v4 folder tree on top of a live v3
		// fleet.
		writeError(w, upstreamGitStatus(err), "can't reach your git host right now: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleMigrationPreview godoc
//
// @Summary Preview the v3 to v4 migration
// @Description Returns the complete dry-run plan: every file the migration pull request would add, convert (old path plus new content), or remove, with plain-words notes about anything that cannot be carried across. Values-file content is redacted the same way every other preview redacts it. Zero side effects — no branch, no commit, no pull request.
// @Tags migration
// @Produce json
// @Security BearerAuth
// @Success 200 {object} orchestrator.MigrationPlan "The migration plan"
// @Failure 400 {object} map[string]interface{} "The repo cannot be migrated as it stands"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /migration/preview [post]
func (s *Server) handleMigrationPreview(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "migration.preview") {
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	plan, err := s.migrationOrchestrator(git).PreviewMigration(r.Context())
	if err != nil {
		// Most of what PreviewMigration can fail on is a property of the
		// repo's own contents (an unparseable file, a cluster name the v4
		// layout cannot hold, a catalog entry that would not merge) — all
		// caller-fixable, so 400. But the same call reads a dozen files
		// from the git host, and blaming the person's repo for the git
		// host being down is a lie (review finding M-6).
		writeMigrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handleMigrateRepo godoc
//
// @Summary Migrate the repo from v3 to v4 (one pull request)
// @Description Converts the whole repository in exactly ONE pull request: the generated template files are removed, the engine pin is added, cluster assignments and values files are rewritten into the current layout, and the full-copy addon catalog becomes a delta of only your own changes. All or nothing — every generated file is validated before a branch exists, and any failure afterwards deletes the branch again, leaving the repo untouched. Requires yes=true (or dry_run=true to preview). Running it on an already-migrated repo is a clean no-op.
// @Tags migration
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body MigrationMigrateRequest false "Migration request"
// @Success 200 {object} orchestrator.MigrateResult "Migration pull request (or dry-run plan)"
// @Failure 400 {object} map[string]interface{} "Missing confirmation, or the repo cannot be migrated as it stands"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 409 {object} map[string]interface{} "A migration pull request from a previous attempt is already open"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /migration/migrate [post]
func (s *Server) handleMigrateRepo(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "migration.migrate") {
		return
	}

	var req MigrationMigrateRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	if r.URL.Query().Get("dry_run") == "true" {
		req.DryRun = true
	}

	// The audit event goes on AFTER the dry-run flag is known (review
	// finding L-10). Enriching unconditionally stamped
	// "repo_migrated_v3_to_v4" on every preview, so the audit log claimed
	// migrations that never happened — and a log that cries wolf is worse
	// than no log, because the one real entry is now indistinguishable
	// from the noise.
	if !req.DryRun {
		audit.Enrich(r.Context(), audit.Fields{
			Event:    "repo_migrated_v3_to_v4",
			Resource: "repo:migration",
		})
	}

	// Tier 2: this changes the shape of every configuration file in the
	// repo — the same tier as the catalog and values writes.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	orch := s.migrationOrchestrator(git)
	s.attachPRTracker(orch)

	result, err := orch.MigrateV3ToV4(ctx, orchestrator.MigrateRequest{
		DryRun:         req.DryRun,
		Yes:            req.Yes,
		AutoMerge:      req.AutoMerge,
		RuntimeHandoff: req.RuntimeHandoff,
	})
	if err != nil {
		// A previous attempt's migration PR is still open — refuse with a
		// plain-words 409 and the existing PR link rather than opening a
		// second PR for the same repo. Structured body so the banner can
		// render "migration PR open" from server truth even after a
		// remount (component state can't be trusted to remember it).
		var prOpenErr *orchestrator.ErrMigrationPROpen
		if errors.As(err, &prOpenErr) {
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error":               "a migration pull request is already open",
				"code":                "migration_pr_already_open",
				"migration_pr_url":    prOpenErr.PRURL,
				"migration_pr_number": prOpenErr.PRNumber,
			})
			return
		}
		writeMigrationError(w, err)
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	// The reconciler derives the v4 addon labels from cluster-addons/*.yaml, so
	// nudge it — on an auto-merged migration the new files are already on
	// the base branch and the fleet converges without waiting for the
	// periodic tick.
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	// An auto-merged migration is already on the base branch by the time
	// this line runs, so the second half can go now rather than waiting for
	// the PR tracker's next poll to notice the merge. On a PR left open for
	// a human to merge, this is a clean no-op (CompleteRuntimeHandoff sees
	// no engine pin yet) and the merge callback does the work later.
	if result.Git != nil && result.Git.Merged {
		if report, hErr := orch.CompleteRuntimeHandoff(ctx); hErr != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"The migration merged, but Sharko could not finish the ArgoCD side (%v). "+
					"Nothing was removed — your addons are still running. Press Finish migration to try again.", hErr))
		} else {
			result.Handoff = report
		}
	}

	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// handleMigrationComplete godoc
//
// @Summary Finish the ArgoCD side of a migration
// @Description Retires the ApplicationSets left over from the old layout and starts the engine in their place. Runs automatically when the migration pull request merges; this endpoint is for the case where that never happened — a PR merged outside Sharko, a restart at the wrong moment, or no ArgoCD connection at the time. Idempotent: running it twice, or on a repo that never needed it, does nothing.
// @Tags migration
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} orchestrator.RuntimeHandoffReport "What the handoff did"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /migration/complete [post]
func (s *Server) handleMigrationComplete(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "migration.migrate") {
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	report, err := s.migrationOrchestrator(git).CompleteRuntimeHandoff(r.Context())
	if err != nil {
		writeError(w, upstreamGitStatus(err), err.Error())
		return
	}
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "repo_migration_runtime_handoff",
		Resource: "repo:migration",
		Detail:   "state=" + report.State,
	})
	writeJSON(w, http.StatusOK, report)
}

// CompleteMigrationHandoffOnMerge is the pull-request-merge hook, wired in
// cmd/sharko/serve.go's prTracker.SetOnMergeFn. It is the ONLY thing that
// finishes a migration whose pull request a person merged themselves.
//
// operation is the tracked PR's operation code; anything other than the
// migration's own is ignored, so this can be called for every merged PR.
// Errors are logged, never returned: the merge already happened, and there
// is nothing this callback could do about a failure except say so. The
// migration banner keeps showing "not finished" from GET /migration/status
// until it succeeds, and POST /migration/complete retries it.
func (s *Server) CompleteMigrationHandoffOnMerge(ctx context.Context, operation string) {
	if operation != migrationPROperation {
		return
	}
	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		// B1: the error value used to go on this line. It is the same
		// connection-build failure the sixty-four response-body sites were
		// leaking, and a log line is one of the sinks the ban covers — on
		// the Git side the reason the client could not be built may BE the
		// repository token. The step is named instead.
		slog.Warn("migration handoff: no usable Git connection after merge")
		return
	}
	report, err := s.migrationOrchestrator(git).CompleteRuntimeHandoff(ctx)
	if err != nil {
		slog.Error("migration handoff failed after merge — the old ApplicationSets may still be in ArgoCD, "+
			"and the engine may not be running; press Finish migration to retry", "error", err)
		return
	}
	slog.Info("migration handoff", "state", report.State, "engine_applied", report.EngineApplied,
		"retired_applicationsets", report.ApplicationSets)
}

// migrationPROperation is the operation code commitMigrationPR stamps on
// the tracked migration pull request. Declared here because the merge hook
// is the only reader of it.
const migrationPROperation = "migrate-v3-v4"
