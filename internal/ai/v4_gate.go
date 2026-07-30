package ai

import (
	"context"
)

// v4EnginePinPath is the engine pin's fixed location in a v4 repo. Its
// presence with non-empty content is the v4-repo signal every other v4-aware
// read path uses (orchestrator.EnginePinPath / CheckEnginePin). Declared
// here rather than imported so internal/ai keeps its narrow dependency set;
// keep the literal in lockstep with orchestrator.BootstrapRootAppPath.
const v4EnginePinPath = "engine/application.yaml"

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
	body, err := e.gp.GetFileContent(ctx, v4EnginePinPath, "main")
	return err == nil && len(body) > 0
}
