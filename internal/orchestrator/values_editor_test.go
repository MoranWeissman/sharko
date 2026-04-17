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
