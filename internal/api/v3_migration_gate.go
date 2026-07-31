package api

// v3_migration_gate.go — the request-edge half of the v3 write block
// (v4 Wave 2 Story 5.1).
//
// The decision itself lives in the orchestrator
// (orchestrator.V3MigrationRequiredMessage / ErrV3MigrationRequired), which
// is the shared layer both this gate and the migration endpoints answer
// from. This file is only the HTTP rendering of it, and the list of doors
// it sits behind.
//
// It is the exact mirror of v4_editor_gate.go: that one refuses a v3-shaped
// surface on a v4 repo, this one refuses a v3-shaped WRITE on a v3 repo
// that has a migration waiting. Reads are deliberately untouched — a person
// can still see every cluster, addon, values file, and pull request while
// they decide when to migrate.

import (
	"context"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// isV3Repo reports whether the connected repo is still in the v3 data-file
// format: NO engine pin, but at least one v3 marker
// (orchestrator.HasV3Markers).
//
// Both halves matter. "No engine pin" alone is also true of a repo nobody
// has set up yet, and blocking writes there would wedge the first-run flow.
// A marker alone is not enough either: mid-migration, a repo briefly has
// both, and the engine pin is the newer, more specific answer.
//
// A read failure of any kind answers false — the pre-Story-5.1 behaviour —
// so a transient git hiccup can never turn an ordinary write into a
// confusing "migrate first" refusal.
func (s *Server) isV3Repo(ctx context.Context, gp gitprovider.GitProvider) bool {
	if gp == nil {
		return false
	}
	baseBranch := s.gitopsConfig().BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	if body, err := gp.GetFileContent(ctx, orchestrator.BootstrapRootAppPath, baseBranch); err == nil && len(body) > 0 {
		return false // already v4
	}
	return orchestrator.HasV3Markers(ctx, gp, baseBranch, s.repoPaths.ManagedClusters)
}

// refuseV3WriteOnV3Repo writes a 409 and reports true when the connected
// repo is still v3-format.
//
// 409 rather than 400 or 501: the request is well-formed and the endpoint
// exists — the repo is simply in a state this operation no longer writes.
// Same shape, and the same status code, as the v4-side refusals.
func (s *Server) refuseV3WriteOnV3Repo(ctx context.Context, w http.ResponseWriter, gp gitprovider.GitProvider) bool {
	if !s.isV3Repo(ctx, gp) {
		return false
	}
	writeError(w, http.StatusConflict, orchestrator.V3MigrationRequiredMessage)
	return true
}

// refuseV3WriteOnActiveRepo is the form used by handlers that have not
// resolved a Git provider yet. It resolves the active one itself so the
// format check runs FIRST — a v3 repo gets the plain-words 409 instead of
// a confusing upstream error, and no ArgoCD or secrets-backend call is
// spent on a request that was never going to be honoured.
//
// No active Git connection means no answer to "is this a v3 repo?", so the
// handler continues and hits its own connection error, exactly as before.
func (s *Server) refuseV3WriteOnActiveRepo(ctx context.Context, w http.ResponseWriter) bool {
	if s.connSvc == nil {
		return false
	}
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		return false
	}
	return s.refuseV3WriteOnV3Repo(ctx, w, gp)
}
