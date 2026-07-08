package orchestrator

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestMergeAddonSection_AddNew — adds an addon section to an empty file.
func TestMergeAddonSection_AddNew(t *testing.T) {
	out, err := mergeAddonSection(nil, "cert-manager", "replicaCount: 2\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := map[string]interface{}{}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("merged output is not valid YAML: %v", err)
	}
	section, ok := got["cert-manager"].(map[string]interface{})
	if !ok {
		t.Fatalf("cert-manager section missing or wrong type: %T", got["cert-manager"])
	}
	if section["replicaCount"] != 2 {
		t.Errorf("expected replicaCount=2, got %v", section["replicaCount"])
	}
}

// TestMergeAddonSection_ReplaceExisting — overwrites an existing addon
// section while leaving other keys untouched. This is the per-cluster
// overrides happy path.
func TestMergeAddonSection_ReplaceExisting(t *testing.T) {
	existing := []byte(`clusterGlobalValues:
  region: us-east-1
cert-manager:
  replicaCount: 1
external-secrets:
  enabled: true
`)
	out, err := mergeAddonSection(existing, "cert-manager", "replicaCount: 3\nresources:\n  limits:\n    memory: 256Mi\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := map[string]interface{}{}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("merged output is not valid YAML: %v", err)
	}

	// cert-manager section was replaced.
	section := got["cert-manager"].(map[string]interface{})
	if section["replicaCount"] != 3 {
		t.Errorf("expected replicaCount=3, got %v", section["replicaCount"])
	}

	// Other top-level keys preserved.
	if _, ok := got["clusterGlobalValues"]; !ok {
		t.Error("clusterGlobalValues was unexpectedly removed")
	}
	if _, ok := got["external-secrets"]; !ok {
		t.Error("external-secrets section was unexpectedly removed")
	}
}

// TestMergeAddonSection_DeleteOnEmpty — passing an empty overrides string
// removes the addon's section. Used when the user clears all overrides for
// an addon and wants the cluster to use the global defaults.
func TestMergeAddonSection_DeleteOnEmpty(t *testing.T) {
	existing := []byte(`cert-manager:
  replicaCount: 1
external-secrets:
  enabled: true
`)
	out, err := mergeAddonSection(existing, "cert-manager", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := map[string]interface{}{}
	if err := yaml.Unmarshal(out, &got); err != nil {
		t.Fatalf("merged output is not valid YAML: %v", err)
	}

	if _, ok := got["cert-manager"]; ok {
		t.Errorf("cert-manager section should have been deleted; got %v", got["cert-manager"])
	}
	if _, ok := got["external-secrets"]; !ok {
		t.Error("external-secrets section was unexpectedly removed")
	}
}

// TestMergeAddonSection_BadOverrideYAML — invalid YAML in the new section
// surfaces as an error rather than silently corrupting the file.
func TestMergeAddonSection_BadOverrideYAML(t *testing.T) {
	_, err := mergeAddonSection(nil, "cert-manager", "replicaCount: : 3")
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing addon overrides") {
		t.Errorf("expected wrapped parse error, got %v", err)
	}
}

// TestValidateYAML — empty input is OK ("reset to chart defaults"); bad
// YAML is rejected.
func TestValidateYAML(t *testing.T) {
	if err := validateYAML(""); err != nil {
		t.Errorf("empty YAML should validate, got %v", err)
	}
	if err := validateYAML("foo: bar\n"); err != nil {
		t.Errorf("simple YAML should validate, got %v", err)
	}
	if err := validateYAML("foo: [bar"); err == nil {
		t.Error("malformed YAML should fail validation")
	}
}

// ── V2-cleanup-83.2: format-preserving edits ──────────────────────────────
//
// mergeAddonSection used to round-trip the whole file through
// map[string]interface{}, which stripped comments and blank lines,
// alphabetized keys, re-indented to 4 spaces, and rendered an empty
// clusterGlobalValues as `null`. These fixtures pin the fix: editing a
// generator-written file must produce a byte-identical shape everywhere
// except the addon section actually being edited.

// generatorShapedFixture is byte-for-byte what generateClusterValues writes
// for a cluster with a single enabled addon ("podinfo") and no region set.
const generatorShapedFixture = `# Cluster values for prod-eu
clusterGlobalValues:

podinfo:
  enabled: true
`

// TestMergeAddonSection_PreservesShape_FixtureA — editing podinfo's section
// on the generator-shaped fixture keeps the header comment, the blank line
// between clusterGlobalValues and podinfo, key order, 2-space indent, and
// renders clusterGlobalValues with no value (never `null`) — while still
// reflecting the new podinfo values.
func TestMergeAddonSection_PreservesShape_FixtureA(t *testing.T) {
	out, err := mergeAddonSection([]byte(generatorShapedFixture), "podinfo", "replicaCount: 2\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `# Cluster values for prod-eu
clusterGlobalValues:

podinfo:
  replicaCount: 2
`
	if string(out) != want {
		t.Errorf("shape not preserved.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestMergeAddonSection_PreservesShape_FixtureB — a second top-level addon
// with its own head comment stays intact (comment, blank line, order) when a
// *different* addon's section is the one being edited.
func TestMergeAddonSection_PreservesShape_FixtureB(t *testing.T) {
	existing := `# Cluster values for prod-eu
clusterGlobalValues:

podinfo:
  enabled: true

# cert-manager needs extra CPU
cert-manager:
  replicaCount: 1
`
	out, err := mergeAddonSection([]byte(existing), "podinfo", "replicaCount: 3\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `# Cluster values for prod-eu
clusterGlobalValues:

podinfo:
  replicaCount: 3

# cert-manager needs extra CPU
cert-manager:
  replicaCount: 1
`
	if string(out) != want {
		t.Errorf("second addon's comment/order not preserved.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestMergeAddonSection_PreservesShape_FixtureC — the delete path (empty
// overridesYAML) removes only the target addon's key, keeping the header
// comment and clusterGlobalValues (still rendered with no value) intact.
func TestMergeAddonSection_PreservesShape_FixtureC(t *testing.T) {
	out, err := mergeAddonSection([]byte(generatorShapedFixture), "podinfo", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `# Cluster values for prod-eu
clusterGlobalValues:
`
	if string(out) != want {
		t.Errorf("delete path did not preserve header/clusterGlobalValues.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
	if strings.Contains(string(out), "podinfo") {
		t.Errorf("podinfo key should have been removed, got:\n%s", out)
	}
}

// TestMergeAddonSection_NeverEmitsNullClusterGlobalValues — a direct
// regression test for the specific bug: an empty clusterGlobalValues block
// must never round-trip to the literal text "null".
func TestMergeAddonSection_NeverEmitsNullClusterGlobalValues(t *testing.T) {
	out, err := mergeAddonSection([]byte(generatorShapedFixture), "podinfo", "replicaCount: 2\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(string(out), "null") {
		t.Errorf("expected no literal 'null' in output, got:\n%s", out)
	}
}

// TestMergeAddonSection_PreservesGitAuthoredAnchors — V2-cleanup-83.4 pin.
//
// A GitOps user may hand-write a YAML anchor under clusterGlobalValues and
// reference it (as an alias) from one or more addon sections in the same
// file — that's the whole point of the clusterGlobalValues convention.
// Editing a completely unrelated addon through the per-addon values editor
// (mergeAddonSection, rewritten on yaml.Node in #498) must not expand,
// drop, or otherwise corrupt that anchor/alias pair: the file-level
// yaml.Node tree is only supposed to be touched at the edited addon's
// key/value pair.
func TestMergeAddonSection_PreservesGitAuthoredAnchors(t *testing.T) {
	existing := `# Cluster values for prod-eu
clusterGlobalValues:
  region: &region eu-west-1

someaddon:
  location: *region
`
	// Edit a DIFFERENT addon (podinfo) — someaddon and clusterGlobalValues
	// are untouched by this call.
	out, err := mergeAddonSection([]byte(existing), "podinfo", "replicaCount: 2\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "&region") {
		t.Errorf("anchor definition (&region) was dropped or expanded; got:\n%s", got)
	}
	if !strings.Contains(got, "*region") {
		t.Errorf("anchor alias (*region) was dropped or expanded to a literal value; got:\n%s", got)
	}
	if !strings.Contains(got, "# Cluster values for prod-eu") {
		t.Errorf("header comment was dropped; got:\n%s", got)
	}

	// The output must still be valid, and someaddon's aliased value must
	// still resolve to the anchor's value (i.e. the alias wasn't merely
	// left as dead text that no longer parses as a real YAML alias).
	var doc map[string]interface{}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("merged output with anchors is not valid YAML: %v", err)
	}
	section, ok := doc["someaddon"].(map[string]interface{})
	if !ok {
		t.Fatalf("someaddon section missing or wrong type: %T", doc["someaddon"])
	}
	if section["location"] != "eu-west-1" {
		t.Errorf("expected someaddon.location to resolve the *region alias to eu-west-1, got %v", section["location"])
	}
	podinfoSection, ok := doc["podinfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("podinfo section missing or wrong type: %T", doc["podinfo"])
	}
	if podinfoSection["replicaCount"] != 2 {
		t.Errorf("expected podinfo.replicaCount=2, got %v", podinfoSection["replicaCount"])
	}
}
