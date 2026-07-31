package ai

import (
	"context"
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
const V4EnginePinPath = "engine.yaml"

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
func (e *ToolExecutor) isV4Repo(ctx context.Context) bool {
	if e.gp == nil {
		return false
	}
	body, err := e.gp.GetFileContent(ctx, V4EnginePinPath, "main")
	return err == nil && len(body) > 0
}
