// Package bootstraprender holds a render-level regression test for the Sharko
// bootstrap Helm chart (templates/bootstrap).
//
// Why this exists (V2-cleanup-17):
//
// Sharko writes its addon catalog under the sharko.io/v1 envelope, so the addon
// list lives at spec.applicationsets. The bootstrap root Application loads that
// catalog as a Helm values file, so the list ends up at .Values.spec.applicationsets.
// The chart templates originally iterated .Values.applicationsets (top level),
// which is undefined under the envelope — so the chart rendered ZERO AppProjects
// and ZERO ApplicationSets, and NO addon ever deployed to any cluster.
//
// CI only ever rendered charts/sharko/, never templates/bootstrap/, so nothing
// caught it. This test renders templates/bootstrap against a realistic enveloped
// catalog (one addon: velero) plus the bootstrap-config and managed-clusters
// value files, and asserts the output contains a velero AppProject AND a velero
// ApplicationSet.
//
// Proof it guards the bug: against the OLD (unfixed) template this test FAILS
// (zero ApplicationSets rendered); against the fixed template it PASSES.
package bootstraprender

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRoot resolves the repository root from this test file's location so the
// test works regardless of the working directory `go test` is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = <root>/tests/bootstraprender/render_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func TestBootstrapChartRendersAddonFromEnvelopedCatalog(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping bootstrap render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "templates", "bootstrap")
	dataDir := filepath.Join(root, "tests", "bootstraprender", "testdata")

	cmd := exec.Command(helmBin, "template", "testbootstrap", chartDir,
		"--values", filepath.Join(dataDir, "bootstrap-config.yaml"),
		"--values", filepath.Join(dataDir, "addons-catalog.yaml"),
		"--values", filepath.Join(dataDir, "managed-clusters.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	// The enveloped fixture contains exactly one addon: velero. The chart must
	// emit a matching AppProject and ApplicationSet, both named "velero". A
	// substring check on "kind: ApplicationSet" alone is enough to catch the
	// wrong-path bug (it rendered zero), but we also assert the addon name to
	// guard against a future "renders an empty/unnamed set" regression.
	assertContains := func(needle string) {
		t.Helper()
		if !strings.Contains(rendered, needle) {
			t.Errorf("rendered bootstrap chart is missing %q.\n"+
				"This means the chart did not generate the addon resources — the V2-cleanup-17 bug "+
				"(template reading .Values.applicationsets instead of .Values.spec.applicationsets) "+
				"has regressed.\n--- rendered output ---\n%s", needle, rendered)
		}
	}

	assertContains("kind: AppProject")
	assertContains("kind: ApplicationSet")

	// Assert a velero AppProject and a velero ApplicationSet specifically.
	// metadata.name is indented under metadata: — match on "name: velero".
	veleroNames := regexp.MustCompile(`(?m)^\s+name: velero$`).FindAllString(rendered, -1)
	if len(veleroNames) < 2 {
		t.Errorf("expected at least 2 resources named 'velero' (AppProject + ApplicationSet), found %d.\n"+
			"--- rendered output ---\n%s", len(veleroNames), rendered)
	}
}

// TestBootstrapAppSetSelectorRequiresEnabledLabel pins the deploy-correctness
// contract for V2-cleanup-20: the ApplicationSet cluster selector matches ONLY
// the canonical "<addon>: enabled" label. This is the downstream half of the
// fix — register/enable now write "enabled" (not the legacy "true") precisely
// because this selector reads only "enabled". If a future template change ever
// loosened or mistyped the selector value, the register-side fix would silently
// stop driving deployment; this test fails first.
func TestBootstrapAppSetSelectorRequiresEnabledLabel(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping bootstrap render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "templates", "bootstrap")
	dataDir := filepath.Join(root, "tests", "bootstraprender", "testdata")

	cmd := exec.Command(helmBin, "template", "testbootstrap", chartDir,
		"--values", filepath.Join(dataDir, "bootstrap-config.yaml"),
		"--values", filepath.Join(dataDir, "addons-catalog.yaml"),
		"--values", filepath.Join(dataDir, "managed-clusters.yaml"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	// The velero ApplicationSet's cluster generator must select on
	// "velero: enabled" — the exact value the canonical AddonLabelValue(true)
	// emits and the reconciler propagates to the ArgoCD cluster Secret. A
	// cluster registered with velero on (label "velero: enabled") therefore
	// matches and the velero Application is generated for it.
	selectorRe := regexp.MustCompile(`(?m)^\s+velero:\s+enabled\s*$`)
	if !selectorRe.MatchString(rendered) {
		t.Errorf("ApplicationSet selector does not require \"velero: enabled\" — the canonical "+
			"label register/enable now write would not match, so velero would never deploy.\n"+
			"--- rendered output ---\n%s", rendered)
	}

	// Defensive: the legacy boolean vocabulary must NOT appear as a selector
	// value (it would silently re-introduce the V2-cleanup-20 mismatch).
	if regexp.MustCompile(`(?m)^\s+velero:\s+"?true"?\s*$`).MatchString(rendered) {
		t.Errorf("ApplicationSet selector uses legacy \"velero: true\" — the deploy-correctness "+
			"contract requires \"enabled\".\n--- rendered output ---\n%s", rendered)
	}
}
