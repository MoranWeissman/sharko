package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/engineversion"
)

// enginePinTestFixture is a minimal-but-real engine/application.yaml, same
// two-source shape as docs/design/2026-07-30-v4-data-file-format.md
// section 2.5, pinned to a version older than engineversion.BundledVersion
// so the "upgrade available" path is exercised by default across these
// tests. Uses engineversion.BundledChartName so it never drifts from
// whatever charts/sharko-engine/Chart.yaml is actually named.
func enginePinTestFixture(pinnedVersion string) string {
	return `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-engine
  namespace: argocd
spec:
  project: default
  sources:
    - repoURL: ghcr.io/example-org/charts
      chart: ` + engineversion.BundledChartName + `
      targetRevision: ` + pinnedVersion + `
      helm:
        valueFiles:
          - $values/catalog/addons.yaml
    - repoURL: https://github.com/example-org/fleet-gitops.git
      targetRevision: main
      ref: values
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
`
}

func TestCheckEnginePin_NoFile_NotAV4Repo(t *testing.T) {
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.CheckEnginePin(context.Background())
	if err != nil {
		t.Fatalf("expected no error for a missing engine pin (v3 repo case), got: %v", err)
	}
	if result.V4Repo {
		t.Error("expected V4Repo=false when engine/application.yaml is absent")
	}
	if result.UpgradeAvailable {
		t.Error("expected UpgradeAvailable=false when there is no pin")
	}
	if result.Message == "" {
		t.Error("expected a non-empty explanatory message")
	}
}

func TestCheckEnginePin_UpgradeAvailable(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte(enginePinTestFixture("0.0.1"))
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.CheckEnginePin(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.V4Repo {
		t.Fatal("expected V4Repo=true when engine/application.yaml exists")
	}
	if result.PinnedVersion != "0.0.1" {
		t.Errorf("PinnedVersion = %q, want %q", result.PinnedVersion, "0.0.1")
	}
	if result.BundledVersion != engineversion.BundledVersion {
		t.Errorf("BundledVersion = %q, want %q", result.BundledVersion, engineversion.BundledVersion)
	}
	if !result.UpgradeAvailable {
		t.Error("expected UpgradeAvailable=true when pinned (0.0.1) is older than bundled")
	}
}

func TestCheckEnginePin_AlreadyUpToDate(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte(enginePinTestFixture(engineversion.BundledVersion))
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.CheckEnginePin(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.UpgradeAvailable {
		t.Error("expected UpgradeAvailable=false when pin already matches the bundled version")
	}
}

func TestCheckEnginePin_MalformedFile_Errors(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n") // no spec.sources
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.CheckEnginePin(context.Background())
	if err == nil {
		t.Fatal("expected an error for an existing-but-malformed engine pin file")
	}
}

func TestUpgradeEnginePin_OpensMinimalDiffPR(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte(enginePinTestFixture("0.0.1"))
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.UpgradeEnginePin(context.Background(), nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.PRUrl == "" {
		t.Fatal("expected a PR URL in the result")
	}

	updated := string(git.files[EnginePinPath])
	if !strings.Contains(updated, "targetRevision: "+engineversion.BundledVersion) {
		t.Errorf("expected pin updated to bundled version %s, got:\n%s", engineversion.BundledVersion, updated)
	}
	// The minimal-diff guarantee: every other line must be byte-identical
	// to the original fixture.
	oldLines := strings.Split(enginePinTestFixture("0.0.1"), "\n")
	newLines := strings.Split(updated, "\n")
	if len(oldLines) != len(newLines) {
		t.Fatalf("line count changed: %d -> %d", len(oldLines), len(newLines))
	}
	changed := 0
	for i := range oldLines {
		if oldLines[i] != newLines[i] {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("expected exactly 1 changed line in the PR diff, got %d", changed)
	}
	// The git-values source's own targetRevision ("main") must be untouched.
	if !strings.Contains(updated, "targetRevision: main") {
		t.Error("git-values source targetRevision (\"main\") was altered")
	}
}

func TestUpgradeEnginePin_DryRun_NoSideEffects(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte(enginePinTestFixture("0.0.1"))
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	result, err := orch.UpgradeEnginePin(context.Background(), nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected a DryRun preview")
	}
	if result.PRUrl != "" || len(git.branches) != 0 || len(git.prs) != 0 {
		t.Error("dry-run must not create a branch or PR")
	}
	if got := string(git.files[EnginePinPath]); got != enginePinTestFixture("0.0.1") {
		t.Error("dry-run must not modify the file in place")
	}
	if len(result.DryRun.FilesToWrite) != 1 || result.DryRun.FilesToWrite[0].Path != EnginePinPath {
		t.Errorf("unexpected FilesToWrite: %+v", result.DryRun.FilesToWrite)
	}
	if result.DryRun.FilesToWrite[0].Diff == "" {
		t.Error("expected a non-empty diff preview")
	}
}

func TestUpgradeEnginePin_NoFile_Errors(t *testing.T) {
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	if _, err := orch.UpgradeEnginePin(context.Background(), nil, false); err == nil {
		t.Fatal("expected an error when there is no engine pin to upgrade")
	}
}

func TestUpgradeEnginePin_AlreadyUpToDate_Errors(t *testing.T) {
	git := newMockGitProvider()
	git.files[EnginePinPath] = []byte(enginePinTestFixture(engineversion.BundledVersion))
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	if _, err := orch.UpgradeEnginePin(context.Background(), nil, false); err == nil {
		t.Fatal("expected an error when the pin already matches the bundled version")
	}
}

func TestSemverGreater(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.2.3", "1.2.2", true},
		{"1.2.2", "1.2.3", false},
		{"1.2.3", "1.2.3", false},
		{"2.0.0", "1.99.99", true},
		{"v1.2.3", "1.2.2", true},
		{"not-a-version", "1.2.3", true}, // fallback: differ -> report as "upgrade"
		{"same-weird", "same-weird", false},
	}
	for _, c := range cases {
		if got := semverGreater(c.a, c.b); got != c.want {
			t.Errorf("semverGreater(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
