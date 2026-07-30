// Wave 2 ride-along w2-q6 item 6, "curated:null nil guard in the delta
// merge": a user's own catalog/addons.yaml can carry a bare `<addon>:` key
// with no value (a hand-edit mistake) — YAML parses that as null. Before
// this fix, that null reached charts/sharko-engine/templates/_helpers.tpl's
// mergeOverwrite call unfiltered, which does NOT drop a nil value the way
// Helm's own --values-file layering drops a null override — it keeps the
// key and clobbers whatever the curated side had there, and appset.yaml's
// very next line (`$addon.settings`) then panics the ENTIRE render
// ("nil pointer evaluating interface {}.settings") — one bad line in one
// user's delta file taking down every addon's ApplicationSet, not just the
// null one.
package enginerender

import (
	"strings"
	"testing"
)

// TestEngineChartNullDeltaAddon_DoesNotCrashAndFallsBackToCurated is the
// full regression: renders successfully (proves the crash is fixed) AND
// the affected addon's ApplicationSet still carries the CURATED
// repoURL/chart (proves the fix filters the null entry out before the
// merge, rather than only guarding against the crash after the fact and
// leaving the addon silently broken with empty repoURL/chart).
func TestEngineChartNullDeltaAddon_DoesNotCrashAndFallsBackToCurated(t *testing.T) {
	extra := `
spec:
  addons:
    cert-manager: null
`
	rendered := renderEngineChartWithExtra(t, extra)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")

	if !strings.Contains(doc, "repoURL: https://charts.jetstack.io") {
		t.Errorf("expected the curated repoURL to survive a null delta entry, got:\n%s", doc)
	}
	if !strings.Contains(doc, "chart: cert-manager") {
		t.Errorf("expected the curated chart name to survive a null delta entry, got:\n%s", doc)
	}
}

// TestEngineChartNullDeltaAddon_OtherAddonsUnaffected proves the null
// entry's blast radius is scoped to its own addon — metrics-server (the
// OTHER fixture addon, no null override) must render exactly as it does
// with no null entry anywhere in the delta.
func TestEngineChartNullDeltaAddon_OtherAddonsUnaffected(t *testing.T) {
	extra := `
spec:
  addons:
    cert-manager: null
`
	rendered := renderEngineChartWithExtra(t, extra)
	doc := extractApplicationSetDoc(t, rendered, "sharko-metrics-server")
	if !strings.Contains(doc, "chart: metrics-server") {
		t.Errorf("expected metrics-server's ApplicationSet to render normally despite cert-manager's null delta entry, got:\n%s", doc)
	}
}

// TestEngineChartNullDeltaAddon_RegressionGuard proves the fix is actually
// meaningful: reverting _helpers.tpl's null-filtering (simulated here by
// exercising mergeOverwriteViaHelm directly, the unfiltered Sprig call)
// reproduces the exact clobber this fix prevents — a nil delta value
// overwrites the curated map wholesale rather than being ignored.
func TestEngineChartNullDeltaAddon_RegressionGuard(t *testing.T) {
	base := map[string]interface{}{
		"cert-manager": map[string]interface{}{"repoURL": "https://charts.jetstack.io", "chart": "cert-manager"},
	}
	delta := map[string]interface{}{
		"cert-manager": nil,
	}
	result := mergeOverwriteViaHelm(t, base, delta)
	if v, ok := result["cert-manager"]; !ok || v != nil {
		t.Fatalf("expected unfiltered mergeOverwrite to clobber cert-manager with a literal nil (proving the pre-fix crash was real), got %#v — if this assertion now fails, Sprig's mergeOverwrite semantics changed and the _helpers.tpl comment needs revisiting", result["cert-manager"])
	}
}
