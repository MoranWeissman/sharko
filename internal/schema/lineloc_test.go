package schema

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// lineLocFixture is a small ClusterAssignment-shaped body with a mix of
// mapping and sequence nesting so tests can exercise both step kinds.
// Line numbers below are pinned by literal position in this string —
// counting starts at 1, same as an editor's gutter. Note yaml.v3's
// convention: a block mapping/sequence VALUE node's own Line is its
// first child's line, not the line of the "key:" that introduces it
// (e.g. "cert-manager:" is line 8, but the cert-manager mapping node's
// Line is 9, the first line inside it).
//
//	1  apiVersion: sharko.dev/v1
//	2  kind: ClusterAssignment
//	3  metadata:
//	4    name: prod-eu
//	5  spec:
//	6    cluster: prod-eu
//	7    addons:
//	8      cert-manager:
//	9        enabled: true
//	10        version: "1.12.0"
//	11        settings:
//	12          syncOptions:
//	13            - CreateNamespace=true
//	14            - ServerSideApply=true
//	15      metrics-server:
//	16        enabled: true
const lineLocFixture = `apiVersion: sharko.dev/v1
kind: ClusterAssignment
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      version: "1.12.0"
      settings:
        syncOptions:
          - CreateNamespace=true
          - ServerSideApply=true
    metrics-server:
      enabled: true
`

func TestLineForInstanceLocation_ResolvesNestedPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		pointer  string
		wantLine int
	}{
		{"root", "", 1},
		{"root slash", "/", 1},
		{"top-level scalar", "/kind", 2},
		{"mapping key two levels deep", "/spec/cluster", 6},
		{"mapping key three levels deep", "/spec/addons/cert-manager", 9},
		{"mapping key four levels deep", "/spec/addons/cert-manager/version", 10},
		{"sequence index 0", "/spec/addons/cert-manager/settings/syncOptions/0", 13},
		{"sequence index 1", "/spec/addons/cert-manager/settings/syncOptions/1", 14},
		{"second sibling in addons map", "/spec/addons/metrics-server/enabled", 16},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line, ok := LineForInstanceLocation([]byte(lineLocFixture), tc.pointer)
			if !ok {
				t.Fatalf("LineForInstanceLocation(%q): ok = false, want true", tc.pointer)
			}
			if line != tc.wantLine {
				t.Errorf("LineForInstanceLocation(%q) = line %d, want %d", tc.pointer, line, tc.wantLine)
			}
		})
	}
}

// TestLineForInstanceLocation_MissingProperty_FallsBackToContainer
// exercises the "missing required property" case: jsonschema points
// InstanceLocation at the OBJECT missing the field (the field itself
// has no line — it doesn't exist), so the resolver should land on that
// containing object rather than failing outright.
func TestLineForInstanceLocation_MissingProperty_FallsBackToContainer(t *testing.T) {
	t.Parallel()

	// "/spec/addons/metrics-server/settings" doesn't exist in the
	// fixture (metrics-server has no settings block) — the walk should
	// stop at the last resolvable segment, metrics-server's VALUE node.
	// yaml.v3 assigns a block mapping's own Line to its first child's
	// line (the "metrics-server:" key sits at line 15, its lone child
	// "enabled: true" at line 16) — same convention pinned by the
	// "/spec/addons/cert-manager" case in
	// TestLineForInstanceLocation_ResolvesNestedPaths (line 9, one past
	// the "cert-manager:" key at line 8).
	line, ok := LineForInstanceLocation([]byte(lineLocFixture), "/spec/addons/metrics-server/settings")
	if !ok {
		t.Fatal("LineForInstanceLocation: ok = false, want true (fallback to container)")
	}
	if line != 16 {
		t.Errorf("LineForInstanceLocation fallback = line %d, want 16 (metrics-server's content)", line)
	}
}

func TestLineForInstanceLocation_MalformedYAML_ReturnsNotOK(t *testing.T) {
	t.Parallel()
	_, ok := LineForInstanceLocation([]byte("foo: [unclosed"), "/spec")
	if ok {
		t.Error("LineForInstanceLocation: ok = true for malformed YAML, want false")
	}
}

func TestLineForInstanceLocation_EmptyBody_ReturnsNotOK(t *testing.T) {
	t.Parallel()
	_, ok := LineForInstanceLocation([]byte(""), "/spec")
	if ok {
		t.Error("LineForInstanceLocation: ok = true for empty body, want false")
	}
}

func TestLineForInstanceLocation_OutOfRangeIndex_FallsBackToContainer(t *testing.T) {
	t.Parallel()
	// syncOptions has 2 entries (indices 0-1); index 5 doesn't exist.
	// The fallback lands on the syncOptions VALUE node (the sequence
	// itself), whose Line is its first item's line (13), not the
	// "syncOptions:" key's line (12) — same first-child convention as
	// the mapping case above.
	line, ok := LineForInstanceLocation([]byte(lineLocFixture), "/spec/addons/cert-manager/settings/syncOptions/5")
	if !ok {
		t.Fatal("LineForInstanceLocation: ok = false, want true (fallback to container)")
	}
	if line != 13 {
		t.Errorf("LineForInstanceLocation fallback = line %d, want 13 (syncOptions' first item)", line)
	}
}

// TestMappingValue_ResolvesKeyAndValueNodes pins the exported helper
// cmd/sharko's validate-config CLI uses to build its ClusterAssignment
// semantic checks (filename-vs-spec.cluster, the
// preserveResourcesOnDeletion redirect) — both need the KEY node's own
// line (to point at exactly where a forbidden field was written), not
// just the value's.
func TestMappingValue_ResolvesKeyAndValueNodes(t *testing.T) {
	t.Parallel()

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(lineLocFixture), &doc); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	root := doc.Content[0]
	_, spec, ok := MappingValue(root, "spec")
	if !ok {
		t.Fatal(`MappingValue(root, "spec"): ok = false`)
	}
	keyNode, valNode, ok := MappingValue(spec, "cluster")
	if !ok {
		t.Fatal(`MappingValue(spec, "cluster"): ok = false`)
	}
	if keyNode.Value != "cluster" {
		t.Errorf("keyNode.Value = %q, want \"cluster\"", keyNode.Value)
	}
	if keyNode.Line != 6 {
		t.Errorf("keyNode.Line = %d, want 6", keyNode.Line)
	}
	if valNode.Value != "prod-eu" {
		t.Errorf("valNode.Value = %q, want \"prod-eu\"", valNode.Value)
	}

	if _, _, ok := MappingValue(spec, "does-not-exist"); ok {
		t.Error(`MappingValue(spec, "does-not-exist"): ok = true, want false`)
	}
	if _, _, ok := MappingValue(nil, "cluster"); ok {
		t.Error("MappingValue(nil, ...): ok = true, want false")
	}
	// A scalar node is not a mapping — must report ok=false rather than
	// panicking.
	if _, _, ok := MappingValue(valNode, "cluster"); ok {
		t.Error("MappingValue(scalarNode, ...): ok = true, want false")
	}
}
