package ai

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// V4EnginePinPath is the engine pin's fixed location in a v4 repo. Its
// presence with non-empty content is the v4-repo signal every other
// v4-aware read path uses (orchestrator.EnginePinPath / CheckEnginePin).
//
// It is a copy of orchestrator.BootstrapRootAppPath, not an import:
// internal/orchestrator imports internal/ai, so the arrow cannot point back
// without a cycle.
//
// WHY THIS COPY MATTERS MORE THAN IT LOOKS: isV4Repo below answers false on
// ANY read failure, including "there is no file at this path". So a copy
// that fell behind would not error and would not log — every v4 repo would
// quietly start looking like a v3 repo to the assistant, which would then
// confidently give v3 answers about a v4 repo. Silent and in the user's
// face is the worst combination.
//
// EXPORTED so the drift cannot happen unnoticed:
// orchestrator's lockstep_paths_test.go asserts this equals
// BootstrapRootAppPath, and that test fails the build the moment the two
// disagree. Do not un-export it without moving that assertion somewhere it
// can still run.
const V4EnginePinPath = "sharko-engine.yaml"

// v4ValuesUnsupportedMessage is what the values-reading tools return on a
// v4 repo. Same sentence the HTTP editors answer with (api's
// V4EditorUnsupportedMessage) so a person hears one story whichever door
// they came through.
//
// It is returned as an ordinary tool RESULT rather than an error so the
// model reads it and relays it, instead of retrying or inventing an answer.
const v4ValuesUnsupportedMessage = "this editor doesn't support the v4 layout yet — edit values/global or values/clusters files directly; sharko validate checks them; the v4 editor ships next wave"

// isV4Repo reports whether the connected repo uses the v4 data-file layout.
// A read failure of any kind answers false — the v3 behaviour — so a
// transient git hiccup never turns an ordinary answer into a refusal.
//
// It reads the CONFIGURED base branch (e.branch()), the same one every
// write gate and every other probe uses. It used to read "main" flat out,
// which on a repo whose default branch is anything else — master, develop,
// a per-team trunk — made every v4 repo look like a v3 one to the
// assistant, so it answered v3 questions about a v4 repo and never said
// why (review finding F5).
func (e *ToolExecutor) isV4Repo(ctx context.Context) bool {
	if e.gp == nil {
		return false
	}
	body, err := e.gp.GetFileContent(ctx, V4EnginePinPath, e.branch())
	return err == nil && len(body) > 0
}

// v4WriteUnsupportedMessage is what the addon write tools answer with on a
// v4 repo.
//
// Those tools write the v3 shape: an on/off label in managed-clusters.yaml,
// a version bump in configuration/addons-catalog.yaml. A v4 repo has
// neither, so the pull request they open cannot work. And enabling an addon
// there is two things the assistant does not write — the addon has to be in
// the org's catalog.yaml first (that is the approval, and it goes through a
// pull request somebody reviews), then switching it on writes
// cluster-addons/<name>.yaml. Skipping the catalog half would walk straight
// past the approval step, which is the one rule the whole model rests on.
//
// Returned as an ordinary tool RESULT, not an error, so the model reads it
// and relays it instead of retrying or inventing an answer.
const v4WriteUnsupportedMessage = "I can't make this change on this repo. Adding an addon to your catalog is an approval step — it opens a pull request somebody reviews — and switching one on writes the cluster's own file. Do it from the Addons screen, or POST /api/v1/catalog/addons. I can still read and explain anything that's already there."

// refuseV4Write returns the plain-words refusal (and true) when the repo
// reachable via gp uses the v4 layout. Any read failure answers false — a
// transient git hiccup must never turn an ordinary v3 write into a
// confusing refusal.
func refuseV4Write(ctx context.Context, gp gitprovider.GitProvider, branch string) (string, bool) {
	if gp == nil {
		return "", false
	}
	if branch == "" {
		branch = "main"
	}
	body, err := gp.GetFileContent(ctx, V4EnginePinPath, branch)
	if err != nil || len(body) == 0 {
		return "", false
	}
	return v4WriteUnsupportedMessage, true
}
