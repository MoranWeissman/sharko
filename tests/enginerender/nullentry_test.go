// LANDMINE 6 (sprint-wave2.5 plan, .bmad/output/architecture/2026-07-31-
// catalog-approved-model.md decision 6): the null-entry crash guard used to
// live inside charts/sharko-engine/templates/_helpers.tpl's
// sharko-engine.mergedAddons (the curated+delta merge helper this story
// deletes along with the chart's baked curated block). It is relocated into
// sharko-engine.catalogAddons, the helper that replaced it — an org's own
// catalog.yaml can carry a bare `<addon>:` key with no value (a hand-edit
// mistake, or a "blank it out" habit carried over from other tools), which
// YAML parses as null. Left unfiltered, that nil would reach appset.yaml's
// range loop and `$addon.settings` on the very next line would panic the
// ENTIRE render ("nil pointer evaluating interface {}.settings") — one bad
// line in the org's catalog taking down every addon's ApplicationSet, not
// just the null one.
package enginerender

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEngineChartNullCatalogEntry_DoesNotCrashAndSkipsThatAddon is the full
// regression: renders successfully (proves the crash is fixed) AND the null
// entry produces NO ApplicationSet at all — there is no curated fallback to
// land on any more (decision 6), so a null catalog entry must behave
// exactly like an absent one (design doc D16, "missing means empty").
func TestEngineChartNullCatalogEntry_DoesNotCrashAndSkipsThatAddon(t *testing.T) {
	extra := `
addons:
  cert-manager: null
`
	rendered := renderEngineChartWithExtra(t, extra)
	if strings.Contains(rendered, "name: 'sharko-cert-manager'") {
		t.Errorf("expected no ApplicationSet for a null catalog entry — a null entry carries no chart/repoURL, so there is nothing to render, got:\n%s", rendered)
	}
}

// TestEngineChartNullCatalogEntry_OtherAddonsUnaffected proves the null
// entry's blast radius is scoped to its own addon — metrics-server (the
// OTHER fixture addon, no null override) must render exactly as it does
// with no null entry anywhere in the catalog.
func TestEngineChartNullCatalogEntry_OtherAddonsUnaffected(t *testing.T) {
	extra := `
addons:
  cert-manager: null
`
	rendered := renderEngineChartWithExtra(t, extra)
	doc := extractApplicationSetDoc(t, rendered, "sharko-metrics-server")
	if !strings.Contains(doc, "chart: metrics-server") {
		t.Errorf("expected metrics-server's ApplicationSet to render normally despite cert-manager's null catalog entry, got:\n%s", doc)
	}
}

// TestEngineChartEmptyCatalog_RendersZeroApplicationSets is the other half
// of landmine 6's acceptance criterion: an explicit empty catalog.yaml
// (addons: {}) — as opposed to
// TestEngineChartRendersZeroApplicationSetsWithEmptyCatalog in
// render_test.go, which proves the same thing via chart defaults alone —
// renders zero addon ApplicationSets, not a crash. Deliberately does NOT
// layer on top of the fixture's own 2-addon catalog.yaml (renderEngineChart/
// renderEngineChartWithExtra): Helm's values-file merge recursively merges
// maps, so an empty addons: {} override layered on top of an already
// 2-addon addons map would leave both addons untouched (there is nothing in
// an empty map to remove existing keys with) — only a real, standalone
// empty catalog proves this case, so this test renders the chart with
// ONLY engine-values.yaml plus its own isolated addons: {} file.
func TestEngineChartEmptyCatalog_RendersZeroApplicationSets(t *testing.T) {
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping engine render test (CI helm-validate job is the hard guard)")
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	emptyCatalogFile := filepath.Join(t.TempDir(), "empty-catalog.yaml")
	if err := os.WriteFile(emptyCatalogFile, []byte("apiVersion: sharko.dev/v1\nkind: AddonCatalog\naddons: {}\n"), 0o644); err != nil {
		t.Fatalf("writing temp empty catalog file: %v", err)
	}

	cmd := exec.Command(helmBin, "template", "testengine", chartDir,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", emptyCatalogFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	rendered := string(out)

	if got := strings.Count(rendered, "kind: ApplicationSet"); got != 0 {
		t.Errorf("expected zero ApplicationSets with an explicit empty catalog.yaml, got %d.\n--- rendered ---\n%s", got, rendered)
	}
}
