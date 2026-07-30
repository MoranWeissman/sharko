package ai

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// v3_gate.go — the chat-tool half of the v3 write block (v4 Wave 2 review
// finding H-3).
//
// Every REST v3 writer (internal/api's mutating cluster/addon handlers)
// refuses a write on a v3-format repo via
// (*api.Server).refuseV3WriteOnActiveRepo, which answers from the shared
// decision in internal/orchestrator/v3_migration_gate.go
// (orchestrator.RefuseOnV3Repo / V3MigrationRequiredMessage). Before this
// file, the AI chat's enable_addon / disable_addon / update_addon_version
// tools had NO such gate: a chat command against a v3 repo with a migration
// pending (or already migrated) would write configuration/managed-
// clusters.yaml or configuration/addons-catalog.yaml directly, undoing the
// point of the gate — on an already-migrated repo it can even RESURRECT the
// v3 file, which the cluster reconciler then prefers over the v4 path.
//
// internal/ai cannot import internal/orchestrator to call the real gate
// directly: internal/orchestrator/ai_annotate.go already imports
// internal/ai (for the smart-values annotation pass), so the reverse import
// would be a cycle. The marker paths and the refusal sentence are therefore
// declared here as the SAME literals, mirroring the exact pattern
// v4EnginePinPath already uses in v4_gate.go for the same reason. A
// cross-package test (v3_gate_pin_test.go, package ai_test) pins every
// literal in this file against its internal/orchestrator source of truth so
// the two can never silently drift.
const (
	// v3BootstrapMarkerPath mirrors orchestrator.V3BootstrapMarkerPath.
	v3BootstrapMarkerPath = "bootstrap/Chart.yaml"
	// v3SecondaryMarkerPath mirrors orchestrator.V3SecondaryMarkerPath.
	v3SecondaryMarkerPath = "configuration/managed-clusters.yaml"
)

// v3MigrationRequiredMessage mirrors orchestrator.V3MigrationRequiredMessage
// — the ONE sentence every blocked v3 write answers with, whichever door
// (REST or chat) the request came through.
const v3MigrationRequiredMessage = "this repo uses the v3 format — migrate to continue (one PR); reads keep working"

// isV3Repo reports whether the repo reachable via gp, at branch, is in the
// v3 data-file format: no v4 engine pin, but at least one v3 marker file.
// Mirrors (*api.Server).isV3Repo (internal/api/v3_migration_gate.go) and
// orchestrator.IsV3Repo — same two-state-then-empty logic, same
// fail-open-to-false on any read error (gp == nil included), so a
// transient git hiccup or an unresolved connection never turns an ordinary
// write into a confusing "migrate first" refusal.
func isV3Repo(ctx context.Context, gp gitprovider.GitProvider, branch string) bool {
	if gp == nil {
		return false
	}
	if branch == "" {
		branch = "main"
	}
	if body, err := gp.GetFileContent(ctx, v4EnginePinPath, branch); err == nil && len(body) > 0 {
		return false // already v4 — the v4 gate owns that case, this must not double-refuse
	}
	for _, marker := range []string{v3BootstrapMarkerPath, v3SecondaryMarkerPath} {
		if body, err := gp.GetFileContent(ctx, marker, branch); err == nil && len(body) > 0 {
			return true
		}
	}
	return false
}

// refuseV3Write returns the plain-words refusal (and true) when gp's repo,
// at branch, is still v3-format and has a migration available. Call this
// BEFORE any git read-modify-write so a refused write touches the provider
// zero times — the whole point of the gate.
//
// The refusal is returned as an ordinary tool RESULT (not an error), same
// convention as v4ValuesUnsupportedMessage, so the model relays the exact
// sentence to the user instead of retrying or inventing an answer.
func refuseV3Write(ctx context.Context, gp gitprovider.GitProvider, branch string) (string, bool) {
	if !isV3Repo(ctx, gp, branch) {
		return "", false
	}
	return v3MigrationRequiredMessage, true
}

// V3GateLiterals is the test-support snapshot of this file's local literals.
// Production code never calls this — it exists only so a test in package
// ai_test (which, unlike internal/ai itself, CAN import
// internal/orchestrator with no cycle: orchestrator only imports the plain
// internal/ai package, never the ai_test one) can pin every literal in this
// file against its internal/orchestrator source of truth
// (orchestrator.V3BootstrapMarkerPath, V3SecondaryMarkerPath,
// V3MigrationRequiredMessage, BootstrapRootAppPath) and fail loudly the
// moment the two drift.
type V3GateLiterals struct {
	BootstrapMarkerPath      string
	SecondaryMarkerPath      string
	MigrationRequiredMessage string
	EnginePinPath            string
}

// GetV3GateLiterals returns the current values of this file's local v3-gate
// literals. See V3GateLiterals for why this exists.
func GetV3GateLiterals() V3GateLiterals {
	return V3GateLiterals{
		BootstrapMarkerPath:      v3BootstrapMarkerPath,
		SecondaryMarkerPath:      v3SecondaryMarkerPath,
		MigrationRequiredMessage: v3MigrationRequiredMessage,
		EnginePinPath:            v4EnginePinPath,
	}
}
