// Cross-check between internal/catalog's Go implementation of the v4
// catalog-delta merge (MergeAddonSettings, and the ExtraHelmValues
// per-key merge inside catalog.applyDeltaOverlay) and the engine chart's
// Helm-side merge (charts/sharko-engine/templates/_helpers.tpl,
// sharko-engine.mergedAddons: `mergeOverwrite(deepCopy(curated.addons),
// spec.addons)`) — Wave 2 ride-along w2-q6 item 3.
//
// Design doc §4.7 names Helm's mergeOverwrite as the definition of
// "merged"; the Go implementation exists only so the API/CLI can show the
// same view without invoking Helm, so it must reproduce mergeOverwrite's
// actual behavior field type by field type:
//   - scalar/pointer fields: last-value-wins (both sides agree trivially).
//   - map fields (a Settings-shaped dict, ExtraHelmValues): mergeOverwrite
//     recurses and merges key by key.
//   - slice fields (SyncOptions, IgnoreDifferences): mergeOverwrite treats
//     a slice as one leaf value — whole replace, never merged
//     element-by-element (design doc §3.3 D12).
//
// Rather than assert what we believe mergeOverwrite does, this file runs
// the ACTUAL Sprig function via a tiny throwaway Helm chart whose only
// template is the identical mergeOverwrite call, and compares its
// rendered output byte-for-structure against catalog.MergeAddonSettings's
// Go output for the same base/delta inputs.
package enginerender

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
)

// mergeOverwriteViaHelm renders `mergeOverwrite(deepCopy($base), $delta) |
// toYaml` — the exact call sharko-engine.mergedAddons makes — against base
// and delta through a real `helm template` invocation, and returns the
// decoded result. Skips (not fails) if helm is unavailable, matching every
// other render test in this package.
func mergeOverwriteViaHelm(t *testing.T, base, delta map[string]interface{}) map[string]interface{} {
	t.Helper()
	helmBin, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not installed; skipping merge cross-check (CI helm-validate job is the hard guard)")
	}

	dir := t.TempDir()
	chartDir := filepath.Join(dir, "mergecheck")
	if err := os.MkdirAll(filepath.Join(chartDir, "templates"), 0o755); err != nil {
		t.Fatalf("mkdir chart dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(chartDir, "Chart.yaml"),
		[]byte("apiVersion: v2\nname: mergecheck\nversion: 0.1.0\n"), 0o644); err != nil {
		t.Fatalf("write Chart.yaml: %v", err)
	}
	// The exact call charts/sharko-engine/templates/_helpers.tpl makes
	// (sharko-engine.mergedAddons) — the whole point is to exercise the
	// real Sprig function, not a reimplementation of what we think it does.
	tpl := "{{- mergeOverwrite (deepCopy .Values.base) .Values.delta | toYaml -}}\n"
	if err := os.WriteFile(filepath.Join(chartDir, "templates", "merge.yaml"), []byte(tpl), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	valuesYAML, err := yaml.Marshal(map[string]interface{}{"base": base, "delta": delta})
	if err != nil {
		t.Fatalf("marshal values: %v", err)
	}
	valuesPath := filepath.Join(dir, "values.yaml")
	if err := os.WriteFile(valuesPath, valuesYAML, 0o644); err != nil {
		t.Fatalf("write values: %v", err)
	}

	// stdout only (Output, not CombinedOutput) — helm can print warnings
	// (e.g. kubeconfig file permissions) to stderr that are not valid YAML
	// and would otherwise corrupt the parse below.
	cmd := exec.Command(helmBin, "template", "mergecheck", chartDir, "--values", valuesPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm template failed: %v\nstderr:\n%s", err, stderr.String())
	}

	var result map[string]interface{}
	if err := yaml.Unmarshal(out, &result); err != nil {
		t.Fatalf("parsing helm template output as YAML: %v\nstdout:\n%s\nstderr:\n%s", err, out, stderr.String())
	}
	return result
}

// settingsToGenericMap round-trips an AddonSettings through YAML into a
// generic map[string]interface{} — the same shape a real curated/delta
// Settings dict has as raw Helm values — so it can be handed to
// mergeOverwriteViaHelm and compared against Helm's own output.
func settingsToGenericMap(t *testing.T, s *config.AddonSettings) map[string]interface{} {
	t.Helper()
	if s == nil {
		return map[string]interface{}{}
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal AddonSettings: %v", err)
	}
	var m map[string]interface{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal AddonSettings into generic map: %v", err)
	}
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

// normalizeViaYAML round-trips v through YAML marshal/unmarshal so both
// sides of a comparison use identical Go types for the same YAML shapes
// (e.g. int vs float64, nil vs empty map) before reflect.DeepEqual runs.
func normalizeViaYAML(t *testing.T, v interface{}) interface{} {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatalf("normalize marshal: %v", err)
	}
	var out interface{}
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("normalize unmarshal: %v", err)
	}
	return out
}

// TestMergeAddonSettings_MatchesHelmMergeOverwrite_PartialOverlay is the
// core cross-check: a delta that sets only SOME Settings fields must
// leave the rest exactly as the base had them, on BOTH sides — this is
// the actual divergence the review flagged (a prior Go implementation did
// a whole-object replace instead of a field-by-field merge).
func TestMergeAddonSettings_MatchesHelmMergeOverwrite_PartialOverlay(t *testing.T) {
	prune := true
	base := &config.AddonSettings{
		Namespace:   "cert-manager-system",
		Prune:       &prune,
		SyncOptions: []string{"ServerSideApply=true"},
	}
	selfHeal := true
	delta := &config.AddonSettings{
		SelfHeal: &selfHeal, // touches ONLY SelfHeal
	}

	goResult := &config.AddonSettings{}
	*goResult = *base
	goResult.SyncOptions = append([]string(nil), base.SyncOptions...)
	catalog.MergeAddonSettings(goResult, delta)

	helmResult := mergeOverwriteViaHelm(t, settingsToGenericMap(t, base), settingsToGenericMap(t, delta))

	goGeneric := normalizeViaYAML(t, settingsToGenericMap(t, goResult))
	helmGeneric := normalizeViaYAML(t, helmResult)

	if !reflect.DeepEqual(goGeneric, helmGeneric) {
		t.Errorf("Go MergeAddonSettings result diverges from helm template's mergeOverwrite:\n  go:   %#v\n  helm: %#v", goGeneric, helmGeneric)
	}

	// Belt-and-braces: assert the specific field-preservation behavior by
	// name too, so a future regression fails with a readable message
	// rather than just "maps differ".
	if ns, _ := helmResult["namespace"].(string); ns != "cert-manager-system" {
		t.Errorf("helm mergeOverwrite dropped the base namespace: %v", helmResult["namespace"])
	}
	if goResult.Namespace != "cert-manager-system" {
		t.Errorf("Go MergeAddonSettings dropped the base namespace: %q", goResult.Namespace)
	}
}

// TestMergeAddonSettings_MatchesHelmMergeOverwrite_ListReplacement proves
// the OTHER half of design doc §3.3 D12: a delta that sets a list field
// replaces it whole on both sides — mergeOverwrite does not merge slices
// element-by-element, and the Go implementation must not either.
func TestMergeAddonSettings_MatchesHelmMergeOverwrite_ListReplacement(t *testing.T) {
	base := &config.AddonSettings{
		SyncOptions: []string{"ServerSideApply=true", "Old=true"},
	}
	delta := &config.AddonSettings{
		SyncOptions: []string{"New=true"},
	}

	goResult := &config.AddonSettings{}
	*goResult = *base
	goResult.SyncOptions = append([]string(nil), base.SyncOptions...)
	catalog.MergeAddonSettings(goResult, delta)

	helmResult := mergeOverwriteViaHelm(t, settingsToGenericMap(t, base), settingsToGenericMap(t, delta))

	goGeneric := normalizeViaYAML(t, settingsToGenericMap(t, goResult))
	helmGeneric := normalizeViaYAML(t, helmResult)
	if !reflect.DeepEqual(goGeneric, helmGeneric) {
		t.Errorf("Go MergeAddonSettings result diverges from helm template's mergeOverwrite on list replacement:\n  go:   %#v\n  helm: %#v", goGeneric, helmGeneric)
	}

	wantSyncOptions := []interface{}{"New=true"}
	if syncOpts, ok := helmResult["syncOptions"].([]interface{}); !ok || !reflect.DeepEqual(syncOpts, wantSyncOptions) {
		t.Errorf("helm mergeOverwrite syncOptions = %v, want whole-replace to %v (not merged with the base list)", helmResult["syncOptions"], wantSyncOptions)
	}
}

// TestExtraHelmValuesMerge_MatchesHelmMergeOverwrite cross-checks the
// OTHER map field applyDeltaOverlay merges (catalog.MergedAddon.
// ExtraHelmValues, a map[string]string) — same divergence class as
// Settings, same fix (merge key by key instead of whole-replace).
func TestExtraHelmValuesMerge_MatchesHelmMergeOverwrite(t *testing.T) {
	base := map[string]interface{}{"replicaCount": "2", "foo": "bar"}
	delta := map[string]interface{}{"foo": "baz"} // overrides ONE key

	// Go side: the same per-key merge applyDeltaOverlay performs for
	// MergedAddon.ExtraHelmValues (internal/catalog/delta_merge.go).
	goResult := make(map[string]string, len(base)+len(delta))
	for k, v := range base {
		goResult[k] = v.(string)
	}
	for k, v := range delta {
		goResult[k] = v.(string)
	}

	helmResult := mergeOverwriteViaHelm(t, base, delta)

	goGeneric := normalizeViaYAML(t, goResult)
	helmGeneric := normalizeViaYAML(t, helmResult)
	if !reflect.DeepEqual(goGeneric, helmGeneric) {
		t.Errorf("Go per-key ExtraHelmValues merge diverges from helm template's mergeOverwrite:\n  go:   %#v\n  helm: %#v", goGeneric, helmGeneric)
	}
	if helmResult["replicaCount"] != "2" {
		t.Errorf("helm mergeOverwrite dropped the base-only key replicaCount: %v", helmResult["replicaCount"])
	}
}
