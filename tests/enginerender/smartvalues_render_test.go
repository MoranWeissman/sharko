// smartvalues_render_test.go — v4 smartvalues wave.
//
// The other tests in this package prove the engine chart's OWN template
// (the ApplicationSet's valueFiles list, targetRevision dig calls, etc.)
// renders correctly; TestEngineChartFixtureValuesLayeringFiles proves the
// STATIC fixture at testdata/values/global/cert-manager.yaml is valid YAML
// matching the worked example. This file adds the missing piece for the
// v4 smartvalues wave: a REALISTIC smart-generated global values file —
// real chart values, inline comments, a commented-out cluster-specific
// field, and the trailing commented per-cluster template block — proven
// to (a) parse as a values file with EXACTLY the live keys it should have
// (no more, no less — the comments are true no-ops) and (b) flow through
// a real `helm template` invocation of charts/sharko-engine without
// breaking anything, the same way ArgoCD's ApplicationSet controller
// would pass it as one of the addon chart's --values sources at round two.
//
// What this does NOT prove (same limit as render_test.go's header
// comment): that a live ArgoCD ApplicationSet controller actually reads
// this exact file from git and layers it onto the target addon chart —
// that needs a running controller, out of reach for a Go unit test.
package enginerender

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// realisticSmartGlobalValues returns a smart-generated global values file
// for a fictitious chart with: a plain field, a nested map with one
// cluster-specific child and one plain child (controller.replicaCount vs
// controller.logLevel — the classifier only marks the matching leaf, not
// its siblings), and a nested map whose ONLY child is cluster-specific
// (ingress.host — exercising the hollow-key post-processor, which must
// comment the whole `ingress:` key out rather than leave `ingress: null`).
// Representative of what AddToCatalog now writes for a real chart, not a
// hand-picked minimal case.
func realisticSmartGlobalValues(t *testing.T) []byte {
	t.Helper()
	upstream := []byte(`installCRDs: true
image:
  repository: ghcr.io/example/cert-manager
  tag: v1.14.5
controller:
  replicaCount: 1
  logLevel: info
ingress:
  host: example.com
`)
	return orchestrator.GenerateGlobalValuesFileV4(
		"cert-manager", "cert-manager", "1.14.5", "https://charts.jetstack.io",
		upstream, false, false,
	)
}

// TestSmartGeneratedGlobalValues_CommentsAreNoOps parses the generated file
// and proves the comments — the header, the cluster-specific placeholder
// lines, and the entire trailing per-cluster template block — contribute
// NOTHING to the live values a chart would receive. Only the real,
// non-cluster-specific fields survive as live keys.
func TestSmartGeneratedGlobalValues_CommentsAreNoOps(t *testing.T) {
	generated := realisticSmartGlobalValues(t)

	var live map[string]interface{}
	if err := yaml.Unmarshal(generated, &live); err != nil {
		t.Fatalf("smart-generated global values file is not valid YAML: %v\n%s", err, generated)
	}

	// The plain, non-cluster-specific fields survive verbatim.
	if live["installCRDs"] != true {
		t.Errorf("installCRDs = %v, want true", live["installCRDs"])
	}
	img, ok := live["image"].(map[string]interface{})
	if !ok || img["repository"] != "ghcr.io/example/cert-manager" || img["tag"] != "v1.14.5" {
		t.Errorf("image section not preserved live, got %v", live["image"])
	}

	// controller.replicaCount IS cluster-specific (the *.replicacount
	// heuristic pattern) and must NOT appear as a live key — if it did, a
	// real Helm render would receive the literal string
	// "<cluster-specific>" as its value, exactly the bug class the
	// smart-values design exists to prevent (a typed chart field getting
	// a placeholder string instead of a real value or nothing at all).
	// controller.logLevel is NOT cluster-specific and must survive live —
	// proving the classifier marks only the matching leaf, not its
	// siblings.
	ctrl, ok := live["controller"].(map[string]interface{})
	if !ok {
		t.Fatalf("controller must survive live (it has a non-cluster-specific child), got %v", live["controller"])
	}
	if ctrl["logLevel"] != "info" {
		t.Errorf("controller.logLevel = %v, want info", ctrl["logLevel"])
	}
	if _, present := ctrl["replicaCount"]; present {
		t.Errorf("controller.replicaCount must not be a live key (cluster-specific), got %v", ctrl["replicaCount"])
	}

	// ingress.host is cluster-specific, and it is the ONLY child of
	// `ingress:` — so the hollow-key post-processor must comment out the
	// WHOLE `ingress:` key rather than leave `ingress: null` (which Helm
	// value coalescing would read as "delete the chart's own ingress
	// defaults" — see commentHollowMapKeys' doc comment). `ingress` must
	// therefore be entirely ABSENT from the live parse, not present-with-
	// nil.
	if _, present := live["ingress"]; present {
		t.Errorf("ingress must be fully commented out (hollow key), not present as a live key at all, got %v", live["ingress"])
	}

	// Nothing from the trailing per-cluster template block or the header
	// leaks in as an extra top-level key.
	wantKeys := map[string]bool{"installCRDs": true, "image": true, "controller": true}
	for k := range live {
		if !wantKeys[k] {
			t.Errorf("unexpected live top-level key %q leaked from a comment block, full parse: %v", k, live)
		}
	}
}

// TestSmartGeneratedGlobalValues_FlowsThroughHelmTemplate passes the
// smart-generated file as an extra --values source into a real `helm
// template` invocation of charts/sharko-engine — the same command shape
// ArgoCD's ApplicationSet controller uses when it layers
// values/global/<addon>.yaml onto an addon chart. Skips (not fails) if
// helm is not installed, matching every other render test's convention.
//
// The engine chart itself does not consume addon Helm values (it only
// EMITS the file paths for ArgoCD's later, separate Helm invocation of the
// target addon chart — see render_test.go's header comment on what is and
// isn't provable here) — so this proves the narrower, still load-bearing
// claim: the file is syntactically inert to Helm (an extra --values source
// with unrelated top-level keys and heavy commenting does not break the
// render), which is a real regression class (a stray tab, an unescaped
// character in a generated comment, a YAML anchor collision) that
// TestSmartGeneratedGlobalValues_CommentsAreNoOps's pure-Go yaml.Unmarshal
// would not catch on its own.
func TestSmartGeneratedGlobalValues_FlowsThroughHelmTemplate(t *testing.T) {
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm not installed; skipping (CI helm-validate job is the hard guard)")
	}

	generated := realisticSmartGlobalValues(t)
	extraFile := filepath.Join(t.TempDir(), "smart-global-values.yaml")
	if err := os.WriteFile(extraFile, generated, 0o644); err != nil {
		t.Fatalf("writing temp smart-values file: %v", err)
	}

	root := repoRoot(t)
	chartDir := filepath.Join(root, "charts", "sharko-engine")
	dataDir := filepath.Join(root, "tests", "enginerender", "testdata")

	cmd := exec.Command("helm", "template", "testengine", chartDir,
		"--values", filepath.Join(dataDir, "engine-values.yaml"),
		"--values", filepath.Join(dataDir, "catalog.yaml"),
		"--values", extraFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed with the smart-generated file layered in: %v\n%s", err, out)
	}

	rendered := string(out)
	// The engine chart's own render is unaffected by the addon-values
	// noise — the two catalog ApplicationSets plus the connectivity-check
	// one still come out, proving the extra source didn't corrupt
	// anything upstream of it in Helm's own merge.
	if got := strings.Count(rendered, "kind: ApplicationSet"); got != 3 {
		t.Errorf("expected 3 ApplicationSets unaffected by the extra smart-values source, got %d\n%s", got, rendered)
	}
}
