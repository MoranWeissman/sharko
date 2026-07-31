// Wave 2 ride-along w2-q6 item 5d: an additionalSources entry that sets
// `chart:` (an independent Helm chart source, its own repo) must not
// silently default repoURL/targetRevision to the engine's own gitops
// repo/revision — that combination only makes sense for a `path:` entry
// (another directory in the SAME repo). charts/sharko-engine/templates/
// appset.yaml now fails the render with a plain-English message naming the
// addon and the missing field when a chart-type entry omits repoURL or
// version. This file proves both failure cases and the still-working
// success cases (chart with both fields set; path entries keep defaulting).
package enginerender

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// renderEngineChartExpectingFailure is renderEngineChartWithExtra's
// failure-side twin: returns the combined output and the *exec.ExitError
// instead of calling t.Fatalf, for tests asserting `helm template` SHOULD
// fail (Helm's `fail` function) with a specific message.
func renderEngineChartExpectingFailure(t *testing.T, extraValuesYAML string) (string, error) {
	t.Helper()
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	extraFile := filepath.Join(t.TempDir(), "extra-values.yaml")
	if err := os.WriteFile(extraFile, []byte(extraValuesYAML), 0o644); err != nil {
		t.Fatalf("writing temp extra values file: %v", err)
	}

	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", filepath.Join(dataDir, "catalog.yaml"),
		"--values", extraFile,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestEngineChartAdditionalSourcesChartRequiresRepoURL proves a chart-type
// additionalSources entry with NO repoURL fails the render with a
// plain-English message naming the addon and the chart, instead of
// silently defaulting to the gitops repo (which would fail far later, at
// ArgoCD sync time, with a confusing "chart not found in this repo" error).
func TestEngineChartAdditionalSourcesChartRequiresRepoURL(t *testing.T) {
	extra := `
addons:
  cert-manager:
    additionalSources:
      - chart: common-config
        version: "1.0.0"
`
	out, err := renderEngineChartExpectingFailure(t, extra)
	if err == nil {
		t.Fatalf("expected helm template to fail for a chart entry with no repoURL, but it succeeded:\n%s", out)
	}
	for _, want := range []string{"cert-manager", "common-config", "repoURL"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure message missing %q — want a plain-English error naming the addon, the chart, and the missing field.\noutput:\n%s", want, out)
		}
	}
}

// TestEngineChartAdditionalSourcesChartRequiresVersion is the version-side
// twin of the repoURL test above.
func TestEngineChartAdditionalSourcesChartRequiresVersion(t *testing.T) {
	extra := `
addons:
  cert-manager:
    additionalSources:
      - chart: common-config
        repoURL: https://charts.example.com
`
	out, err := renderEngineChartExpectingFailure(t, extra)
	if err == nil {
		t.Fatalf("expected helm template to fail for a chart entry with no version, but it succeeded:\n%s", out)
	}
	for _, want := range []string{"cert-manager", "common-config", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure message missing %q — want a plain-English error naming the addon, the chart, and the missing field.\noutput:\n%s", want, out)
		}
	}
}

// TestEngineChartAdditionalSourcesChartWithRepoURLAndVersion_Succeeds is
// the success-path guard: a chart entry that DOES set both fields must
// still render correctly — the validation is a gate on missing fields, not
// a blanket rejection of chart-type entries.
func TestEngineChartAdditionalSourcesChartWithRepoURLAndVersion_Succeeds(t *testing.T) {
	extra := `
addons:
  cert-manager:
    additionalSources:
      - chart: common-config
        repoURL: https://charts.example.com
        version: "1.0.0"
`
	rendered := renderEngineChartWithExtra(t, extra)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")
	if !strings.Contains(doc, "repoURL: https://charts.example.com") {
		t.Errorf("expected the additionalSources entry's own repoURL in the rendered sources, got:\n%s", doc)
	}
	if !strings.Contains(doc, "chart: common-config") {
		t.Errorf("expected the additionalSources entry's chart name in the rendered sources, got:\n%s", doc)
	}
	if !strings.Contains(doc, "targetRevision: 1.0.0") {
		t.Errorf("expected the additionalSources entry's own version as targetRevision, got:\n%s", doc)
	}
}

// TestEngineChartAdditionalSourcesPath_StillDefaultsToGitopsRepo is the
// regression guard for the OTHER half of the fix: a path-type entry (same
// repo, different directory) must keep defaulting repoURL/targetRevision
// to the engine's own gitops repo/revision — the validation added for
// chart-type entries must not accidentally start requiring repoURL/version
// on path-type entries too.
func TestEngineChartAdditionalSourcesPath_StillDefaultsToGitopsRepo(t *testing.T) {
	extra := `
addons:
  cert-manager:
    additionalSources:
      - path: charts/common-config
`
	rendered := renderEngineChartWithExtra(t, extra)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")
	if !strings.Contains(doc, "path: charts/common-config") {
		t.Errorf("expected the additionalSources path entry in the rendered sources, got:\n%s", doc)
	}
	// The engine-values.yaml fixture's own repo.url/repo.revision — see
	// testdata/engine-values.yaml.
	if !strings.Contains(doc, "repoURL: https://github.com/example-org/fleet-gitops.git") {
		t.Errorf("expected the path entry's repoURL to default to the gitops repo, got:\n%s", doc)
	}
}
