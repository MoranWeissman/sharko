//go:build e2e

package lifecycle

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"
)

// TestV4BootstrapSeedPassesRealDetector is the in-process proof (no kind
// required) that seedV4Bootstrap (cluster_helpers.go) plants a repo the
// PRODUCTION v3/v4 detectors — the same code the v3 migration gate and the
// register/adopt/patch write paths call — actually recognise as v4-format,
// not v3-format.
//
// This is the harness fix's load-bearing claim: TestClusterLifecycle and
// TestPerClusterAddonLifecycle both fail on kind CI with
// orchestrator.V3MigrationRequiredMessage because their seeded repo trips
// hasV3Markers (a real write landing on configuration/managed-clusters.yaml,
// which is also orchestrator.V3SecondaryMarkerPath). Seeding
// orchestrator.BuildV4SeedFiles first is supposed to make the SAME
// production isV4Repo/isV3Repo probes both engine.EnableAddon-style writers
// and the migration gate call see a v4 repo instead — this test calls those
// exact exported entry points against the exact seed function the two kind
// tests use, without needing docker/kind at all.
func TestV4BootstrapSeedPassesRealDetector(t *testing.T) {
	mock := harness.StartGitMock(t)
	seedV4Bootstrap(t, mock, "https://github.com/sharko-e2e/sharko-addons")

	orch := orchestrator.New(nil, nil, nil, mock,
		orchestrator.GitOpsConfig{BaseBranch: "main"},
		orchestrator.RepoPathsConfig{},
		nil,
	)

	// 1. The engine pin landed, with content — the ONE fact isV4Repo
	// (internal/orchestrator/v4paths.go) checks.
	if !mock.FileExists("main", orchestrator.BootstrapRootAppPath) {
		t.Fatalf("seedV4Bootstrap: %s was not written", orchestrator.BootstrapRootAppPath)
	}
	if body := mock.FileAt("main", orchestrator.BootstrapRootAppPath); body == "" {
		t.Fatalf("seedV4Bootstrap: %s exists but is empty — isV4Repo requires non-empty content", orchestrator.BootstrapRootAppPath)
	}

	// 2. Neither v3 marker landed — the v3 migration gate's hasV3Markers
	// check (internal/orchestrator/v3_markers.go) must find nothing.
	for _, marker := range []string{orchestrator.V3BootstrapMarkerPath, orchestrator.V3SecondaryMarkerPath} {
		if mock.FileExists("main", marker) {
			t.Errorf("seedV4Bootstrap: v3 marker %s unexpectedly present — this would still trip the migration gate", marker)
		}
	}

	// 3. The exported, production-used probe agrees: this is NOT a v3
	// repo, so RefuseOnV3Repo / the "uses the v3 format" 409 never fires
	// for it. This is the exact call the API layer's
	// refuseV3WriteOnActiveRepo makes before every mutating handler.
	if orch.IsV3Repo(t.Context()) {
		t.Errorf("orchestrator.IsV3Repo reports true against the v4 bootstrap seed — the migration gate would still 409 every write")
	}
}
