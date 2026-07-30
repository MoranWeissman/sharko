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
// No ArgoCD client is needed anywhere here — the migration only ever
// touches Git. What runs in the clusters is unchanged by design, and the
// engine pin this PR adds is applied by the existing post-merge fan-out.

import (
	"encoding/json"
	"errors"
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
}

// migrationOrchestrator builds a Git-only orchestrator for the migration
// paths, with the shipped curated catalog wired in — the delta conversion
// cannot tell a user-added addon from a Sharko-shipped one without it, and
// would carry the whole catalog across as if every entry were the user's.
func (s *Server) migrationOrchestrator(git gitprovider.GitProvider) *orchestrator.Orchestrator {
	orch := orchestrator.New(&s.gitMu, nil, nil, git, s.gitopsConfig(), s.repoPaths, nil)
	// s.catalog is optional (router.go). A nil catalog makes every addon
	// merge as catalog.OriginInternal — which for the migration means the
	// whole v3 catalog is carried across verbatim rather than reduced to a
	// delta. Lossless, just less tidy, and exactly what a server with no
	// shipped catalog loaded should do.
	orch.SetCuratedCatalog(s.catalog)
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
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := s.migrationOrchestrator(git)
	writeJSON(w, http.StatusOK, orch.MigrationStatus(r.Context()))
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
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	plan, err := s.migrationOrchestrator(git).PreviewMigration(r.Context())
	if err != nil {
		// Everything PreviewMigration can fail on is a property of the
		// repo's own contents (an unparseable file, a cluster name the v4
		// layout cannot hold, a catalog entry that would not merge) — all
		// caller-fixable, so 400 rather than a 502 that blames upstream.
		writeError(w, http.StatusBadRequest, err.Error())
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

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "repo_migrated_v3_to_v4",
		Resource: "repo:migration",
	})

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

	// Tier 2: this changes the shape of every configuration file in the
	// repo — the same tier as the catalog and values writes.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := s.migrationOrchestrator(git)
	s.attachPRTracker(orch)

	result, err := orch.MigrateV3ToV4(ctx, orchestrator.MigrateRequest{
		DryRun:    req.DryRun,
		Yes:       req.Yes,
		AutoMerge: req.AutoMerge,
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	// The reconciler derives the v4 addon labels from clusters/*.yaml, so
	// nudge it — on an auto-merged migration the new files are already on
	// the base branch and the fleet converges without waiting for the
	// periodic tick.
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}
