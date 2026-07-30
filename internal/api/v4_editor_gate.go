package api

import (
	"context"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// V4EditorUnsupportedMessage is the one sentence every v3-layout values
// surface returns on a v4 repo — the HTTP handlers as a 409 body, the AI
// tools as a tool error. One constant so the person reads the same words
// whichever door they came through.
//
// Exported because internal/ai's tool executor returns it too and the two
// must not drift.
const V4EditorUnsupportedMessage = "this editor doesn't support the v4 layout yet — edit values/global or values/clusters files directly; sharko validate checks them; the v4 editor ships next wave"

// isV4Repo reports whether the connected repo uses the v4 data-file layout,
// using the same probe every other v4-aware read path uses: the engine pin
// (orchestrator.BootstrapRootAppPath) resolving to non-empty content on the
// base branch. "Not found" is the ordinary v3 answer, never an error.
//
// A read failure of any kind answers false — the v3 behaviour — so a
// transient git hiccup can never turn an ordinary v3 edit into a confusing
// "this editor doesn't support v4" refusal.
//
// STANCE (v4 Wave 2 review, aligned with orchestrator.isV4Repo's
// fail-closed change). Leniency here is safe in a way it was NOT safe in
// the orchestrator, and the difference is worth spelling out because the
// two probes look identical:
//
//   - The orchestrator's probe stands in front of a write that PICKS A
//     PATH. Answering "not v4" wrongly there writes
//     configuration/managed-clusters.yaml onto a v4 repo, creating a second
//     cluster registry that the reconciler prefers — and every v4-registered
//     cluster loses its ArgoCD connection Secret. That is unrecoverable, so
//     it fails closed.
//   - This probe stands in front of a REFUSAL. Answering "not v4" wrongly
//     here lets a v3-shaped values edit through on a v4 repo. The worst
//     outcome is a commit at a v3 path the engine does not read: a
//     confusing no-op the person can see in git and revert, never a lost
//     fleet. And failing closed here would turn every git hiccup into
//     "this editor doesn't support the v4 layout yet", which is a lie
//     about the repo rather than an honest error.
//
// So the two stay deliberately different, and this comment is the record
// of why.
func (s *Server) isV4Repo(ctx context.Context, gp gitprovider.GitProvider) bool {
	if gp == nil {
		return false
	}
	baseBranch := s.gitopsConfig().BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}
	body, err := gp.GetFileContent(ctx, orchestrator.BootstrapRootAppPath, baseBranch)
	return err == nil && len(body) > 0
}

// refuseV3ValuesSurfaceOnV4Repo writes a 409 and reports true when the
// connected repo is v4-format.
//
// Every caller of this reads or writes a hard-coded v3 path
// (configuration/addons-global-values/<addon>.yaml,
// configuration/addons-clusters-values/<cluster>.yaml). On a v4 repo those
// paths hold nothing and the engine reads neither of them, so the surface
// has exactly two ways to behave, both bad: a read returns an empty editor
// that looks like "this addon has no values", and a write commits a file
// nothing will ever load while the user believes their change is live. A
// plain-English 409 that names where the values actually live is the honest
// third option until the v4 editor lands.
//
// 409 rather than 501/400: the request is well-formed and the endpoint
// exists — the repo is simply in a state this surface does not handle yet,
// the same shape as the v3-registry-writer refusals
// (orchestrator.ErrV4RepoUnsupported).
func (s *Server) refuseV3ValuesSurfaceOnV4Repo(ctx context.Context, w http.ResponseWriter, gp gitprovider.GitProvider) bool {
	if !s.isV4Repo(ctx, gp) {
		return false
	}
	writeError(w, http.StatusConflict, V4EditorUnsupportedMessage)
	return true
}

// refuseV3ValuesSurfaceOnActiveRepo is the form used by handlers that would
// otherwise reach for an ArgoCD client (or a per-user Git token) before
// they have a Git provider in hand. It resolves the active provider itself
// so the format check runs FIRST — a v4 repo gets the plain-words 409
// instead of a confusing "no active ArgoCD connection" 502, and no upstream
// call is spent on a request that was never going to be honoured.
//
// No active Git connection means no answer to "is this a v4 repo?", so the
// handler continues and hits its own connection error, exactly as before.
func (s *Server) refuseV3ValuesSurfaceOnActiveRepo(ctx context.Context, w http.ResponseWriter) bool {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		return false
	}
	return s.refuseV3ValuesSurfaceOnV4Repo(ctx, w, gp)
}
