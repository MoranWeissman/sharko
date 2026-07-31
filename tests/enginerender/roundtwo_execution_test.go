// Wave 2 ride-along w2-q6 item 5 — engine render-test blind spots the
// review flagged in the round-one/round-two split every other test in
// this package already exercises:
//
//   - (a) the matrix generator's cluster arm must be FIRST — order is
//     precedence per the appset.yaml comment ("Arm 2 ... Must run second —
//     its path depends on `.name`, which only exists once arm 1 has run"),
//     but nothing before this file asserted the ORDER, only that both arms
//     existed somewhere in the output.
//   - (b) preserveResourcesOnDeletion must sit at the ApplicationSet's own
//     spec.syncPolicy — the existing test only substring-counted the
//     literal text twice, which would not catch a regression that moved it
//     to spec.template.spec.syncPolicy (the PER-APPLICATION sync policy, a
//     different field with the same name one level down).
//   - (c) templatePatch's full round-two Go-template text has only ever
//     been exercised in NARROW hand-picked fragments (nilsafety_test.go)
//     or asserted as SOURCE TEXT (createnamespace_test.go) — never actually
//     EXECUTED whole against a fixture that overrides every settings field
//     at once and had its rendered Application fields checked.
package enginerender

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestEngineChartMatrixGeneratorClusterArmFirst proves the clusters
// generator is index 0 and the git generator is index 1 in the matrix's
// generators list — a swap silently breaks every round-two `.name`/`.`
// (the whole merged data, per-cluster settings included) lookup, since the
// git arm's file path (clusters/{{ .name }}.yaml) depends on the cluster
// arm having already resolved `.name`, so the ORDER is load-bearing, not
// incidental.
func TestEngineChartMatrixGeneratorClusterArmFirst(t *testing.T) {
	rendered := renderEngineChart(t)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")

	var parsed struct {
		Spec struct {
			Generators []struct {
				Matrix struct {
					Generators []map[string]interface{} `yaml:"generators"`
				} `yaml:"matrix"`
			} `yaml:"generators"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("parsing rendered ApplicationSet: %v\n%s", err, doc)
	}
	if len(parsed.Spec.Generators) != 1 || len(parsed.Spec.Generators[0].Matrix.Generators) != 2 {
		t.Fatalf("expected exactly one matrix generator with two arms, got %+v", parsed.Spec.Generators)
	}
	arms := parsed.Spec.Generators[0].Matrix.Generators

	if _, ok := arms[0]["clusters"]; !ok {
		t.Errorf("matrix generator arm 0 = %v, want the \"clusters\" generator (arm 1 in appset.yaml, decides WHETHER a cluster runs this addon)", arms[0])
	}
	if _, ok := arms[1]["git"]; !ok {
		t.Errorf("matrix generator arm 1 = %v, want the \"git\" generator (arm 2, reads clusters/{{.name}}.yaml — depends on arm 0 having resolved .name first)", arms[1])
	}
}

// TestEngineChartPreserveResourcesOnDeletionAtAppSetSpecLevel is the
// structural twin of the existing substring-count test
// (TestEngineChartPreserveResourcesOnDeletionDefaultTrue): it parses the
// rendered YAML and asserts preserveResourcesOnDeletion lives at
// spec.syncPolicy (the ApplicationSet's own sync policy — the only level
// ArgoCD actually reads this field from, design doc section 3.2) and is
// ABSENT from spec.template.spec.syncPolicy (the per-Application sync
// policy nested one level down, which happens to share the parent
// "syncPolicy" key name but is a completely different field with no
// preserveResourcesOnDeletion semantics of its own).
func TestEngineChartPreserveResourcesOnDeletionAtAppSetSpecLevel(t *testing.T) {
	rendered := renderEngineChart(t)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")

	var parsed struct {
		Spec struct {
			SyncPolicy struct {
				PreserveResourcesOnDeletion *bool `yaml:"preserveResourcesOnDeletion"`
			} `yaml:"syncPolicy"`
			Template struct {
				Spec struct {
					SyncPolicy struct {
						PreserveResourcesOnDeletion *bool `yaml:"preserveResourcesOnDeletion"`
						Automated                   struct {
							Prune    bool `yaml:"prune"`
							SelfHeal bool `yaml:"selfHeal"`
						} `yaml:"automated"`
					} `yaml:"syncPolicy"`
				} `yaml:"spec"`
			} `yaml:"template"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("parsing rendered ApplicationSet: %v\n%s", err, doc)
	}

	if parsed.Spec.SyncPolicy.PreserveResourcesOnDeletion == nil {
		t.Fatal("spec.syncPolicy.preserveResourcesOnDeletion is absent — this is the field ArgoCD actually reads (design doc section 3.2)")
	}
	if !*parsed.Spec.SyncPolicy.PreserveResourcesOnDeletion {
		t.Errorf("spec.syncPolicy.preserveResourcesOnDeletion = false, want true (the documented default)")
	}
	if parsed.Spec.Template.Spec.SyncPolicy.PreserveResourcesOnDeletion != nil {
		t.Errorf("spec.template.spec.syncPolicy.preserveResourcesOnDeletion = %v, want absent — this is the PER-APPLICATION sync policy, a different field one level down; it must not carry a copy",
			*parsed.Spec.Template.Spec.SyncPolicy.PreserveResourcesOnDeletion)
	}
	// Belt-and-braces: the per-Application syncPolicy.automated block
	// (prune/selfHeal) DOES legitimately live at this nested level — proves
	// the parse path itself is exercising the right node, not silently
	// matching an empty/wrong struct.
	if !parsed.Spec.Template.Spec.SyncPolicy.Automated.Prune {
		t.Error("spec.template.spec.syncPolicy.automated.prune = false, want true (sanity check that this nested struct actually parsed)")
	}
}

// extractTemplatePatchBody pulls the raw text after "templatePatch: |" out
// of a rendered ApplicationSet document and de-indents it by the fixed
// 4-space block-scalar indent Helm emits — the Go-template SOURCE for
// round two, ready to feed to execRoundTwo.
func extractTemplatePatchBody(t *testing.T, doc string) string {
	t.Helper()
	const marker = "templatePatch: |\n"
	idx := strings.Index(doc, marker)
	if idx < 0 {
		t.Fatalf("templatePatch: | not found in rendered doc:\n%s", doc)
	}
	body := doc[idx+len(marker):]
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "    ")
	}
	return strings.Join(lines, "\n")
}

// TestEngineChartTemplatePatchExecution_SettingsBearingFixture is item 5c:
// EXECUTE the full templatePatch round-two template (not a hand-picked
// fragment) against a fixture that overrides every settings field at
// once, and assert the RENDERED Application fields — not just that the
// template text contains the right-looking substrings.
func TestEngineChartTemplatePatchExecution_SettingsBearingFixture(t *testing.T) {
	rendered := renderEngineChart(t)
	doc := extractApplicationSetDoc(t, rendered, "sharko-cert-manager")
	patchSrc := extractTemplatePatchBody(t, doc)

	// Flat shape (decision 9 of the 2026-07-31-catalog-approved-model design
	// doc): no `metadata:`/`spec:` wrapper — cluster/addons sit at the top
	// level, and round-two `.` is exactly this file's own parsed content
	// merged with the clusters arm's name/server (design doc section 4.1).
	const clusterFixture = `
apiVersion: sharko.dev/v1
kind: ClusterAddons
cluster: settings-full-us
addons:
  cert-manager:
    enabled: true
    settings:
      createNamespace: false
      syncOptions: ["Validate=false"]
      prune: false
      selfHeal: false
      ignoreDifferences:
        - group: apps
          kind: Deployment
          jsonPointers: ["/spec/replicas"]
`
	var fixtureDoc map[string]interface{}
	if err := yaml.Unmarshal([]byte(clusterFixture), &fixtureDoc); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	data := map[string]interface{}{}
	for k, v := range fixtureDoc {
		data[k] = v
	}
	data["name"] = "settings-full-us"
	data["server"] = "https://example.invalid"

	out, err := execRoundTwo(t, patchSrc, data)
	if err != nil {
		t.Fatalf("executing templatePatch round-two failed: %v\nsource:\n%s", err, patchSrc)
	}

	var result struct {
		Spec struct {
			SyncPolicy struct {
				Automated struct {
					Prune    bool `yaml:"prune"`
					SelfHeal bool `yaml:"selfHeal"`
				} `yaml:"automated"`
				SyncOptions []string `yaml:"syncOptions"`
			} `yaml:"syncPolicy"`
			IgnoreDifferences []map[string]interface{} `yaml:"ignoreDifferences"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("templatePatch round-two output is not valid YAML: %v\noutput:\n%s", err, out)
	}

	if result.Spec.SyncPolicy.Automated.Prune {
		t.Errorf("prune = true, want false (the per-cluster override)")
	}
	if result.Spec.SyncPolicy.Automated.SelfHeal {
		t.Errorf("selfHeal = true, want false (the per-cluster override)")
	}
	wantSyncOptions := []string{"Validate=false"}
	if len(result.Spec.SyncPolicy.SyncOptions) != 1 || result.Spec.SyncPolicy.SyncOptions[0] != wantSyncOptions[0] {
		t.Errorf("syncOptions = %v, want %v (the cluster's own list, with createNamespace=false meaning CreateNamespace=true is NOT added)", result.Spec.SyncPolicy.SyncOptions, wantSyncOptions)
	}
	if len(result.Spec.IgnoreDifferences) != 1 {
		t.Fatalf("ignoreDifferences = %v, want exactly one entry (the per-cluster override)", result.Spec.IgnoreDifferences)
	}
	if result.Spec.IgnoreDifferences[0]["kind"] != "Deployment" {
		t.Errorf("ignoreDifferences[0] = %v, want kind=Deployment", result.Spec.IgnoreDifferences[0])
	}
}
