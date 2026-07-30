package ai_test

// v3_gate_pin_test.go lives in the EXTERNAL ai_test package (not ai) so it
// can import internal/orchestrator alongside internal/ai. internal/ai
// itself cannot do this — internal/orchestrator/ai_annotate.go already
// imports internal/ai, so the reverse import from an internal (package ai)
// file is a genuine cycle (verified: `go vet` reports "import cycle not
// allowed in test" for that shape). The external test package sidesteps it
// because internal/orchestrator only ever imports the plain internal/ai
// package, never this ai_test one.
//
// v3_gate.go duplicates the v3-write gate's marker paths and refusal
// sentence locally (same reason, same precedent as v4EnginePinPath in
// v4_gate.go) rather than importing orchestrator's originals. This test is
// the guardrail that duplication needs: it fails the moment
// internal/ai's copy and internal/orchestrator's source of truth drift.

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

func TestV3GateLiteralsMatchOrchestrator(t *testing.T) {
	got := ai.GetV3GateLiterals()

	if got.BootstrapMarkerPath != orchestrator.V3BootstrapMarkerPath {
		t.Errorf("ai's v3 bootstrap marker path = %q, orchestrator's = %q",
			got.BootstrapMarkerPath, orchestrator.V3BootstrapMarkerPath)
	}
	if got.SecondaryMarkerPath != orchestrator.V3SecondaryMarkerPath {
		t.Errorf("ai's v3 secondary marker path = %q, orchestrator's = %q",
			got.SecondaryMarkerPath, orchestrator.V3SecondaryMarkerPath)
	}
	if got.MigrationRequiredMessage != orchestrator.V3MigrationRequiredMessage {
		t.Errorf("ai's v3-migration-required message = %q, orchestrator's = %q",
			got.MigrationRequiredMessage, orchestrator.V3MigrationRequiredMessage)
	}
	if got.EnginePinPath != orchestrator.BootstrapRootAppPath {
		t.Errorf("ai's engine pin path = %q, orchestrator's BootstrapRootAppPath = %q",
			got.EnginePinPath, orchestrator.BootstrapRootAppPath)
	}
}
