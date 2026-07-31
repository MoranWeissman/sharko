package models

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/testutil/malformed"
)

// deepNestingFlat builds a FLAT (apiVersion + kind at the top level, no
// spec: wrapper) Sharko document with `depth` levels of nesting under an
// unrecognised key instead of clusters:. Unlike malformed.DeepNesting
// (which wraps the same nesting under spec: — the legacy v3 shape that
// LoadManagedClusters deliberately skips schema validation for, see
// TestMalformedInput_LoadManagedClusters_WrappedHugeListAccepted below),
// this body is always schema-validated, so it keeps proving what the case
// has always proven: pathological nesting in a real Sharko file must
// error, never silently parse as zero clusters.
func deepNestingFlat(depth int) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: sharko.dev/v1\nkind: ManagedClusters\n")
	indent := "  "
	for i := 0; i < depth; i++ {
		b.WriteString(strings.Repeat(indent, i+1))
		b.WriteString(fmt.Sprintf("level%d:\n", i))
	}
	b.WriteString(strings.Repeat(indent, depth+1))
	b.WriteString("leaf: true\n")
	return []byte(b.String())
}

// TestMalformedInput_LoadManagedClusters and TestMalformedInput_LoadClusterAddons
// are Story 8.6's (v4 Wave 2) all-or-nothing audit for the two envelope
// readers this package owns:
//
//   - LoadManagedClusters — managed-clusters.yaml (v4) / managed-clusters.yaml
//     (v3 legacy, still read by the migration and by ParseClusterAddons'
//     enveloped branch).
//   - LoadClusterAddons — clusters/<name>.yaml (v4 only, no legacy shape).
//
// Both readers already go through schema.IsEnveloped + schema.DefaultValidator
// before unmarshalling into a typed struct, so the expectation going in is
// "every case returns a Go error, never a panic, never a zero-value success
// on garbage" — this suite is the proof, not new production code.

// TestMalformedInput_LoadManagedClusters_EmptyIsLegacyEmptyFleet pins the
// documented (LoadManagedClusters doc comment) behavior for an empty or
// blank body: it is NOT an error. schema.IsEnveloped treats a blank body
// as "not enveloped" and the legacy bare-YAML branch unmarshals it into a
// zero-value spec — an empty managed-clusters.yaml means an empty fleet,
// not a broken file. This is the one deliberate exception to "every
// malformed case returns an error" in this suite; every other case below
// must still error.
func TestMalformedInput_LoadManagedClusters_EmptyIsLegacyEmptyFleet(t *testing.T) {
	t.Parallel()
	for name, body := range map[string][]byte{
		"empty":           malformed.Empty(),
		"whitespace_only": malformed.Whitespace(),
	} {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var spec ManagedClustersSpec
			var err error
			malformed.AssertNoPanic(t, name, func() {
				spec, err = LoadManagedClusters(body)
			})
			if err != nil {
				t.Fatalf("LoadManagedClusters(%s): expected success (empty fleet), got error: %v", name, err)
			}
			if len(spec.Clusters) != 0 {
				t.Fatalf("LoadManagedClusters(%s): expected 0 clusters, got %d", name, len(spec.Clusters))
			}
		})
	}
}

func TestMalformedInput_LoadManagedClusters(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"truncated_flow_sequence":     malformed.TruncatedFlowSequence(),
		"truncated_mid_line":          malformed.TruncatedMidLine(),
		"truncated_block_mapping":     malformed.TruncatedBlockMapping(),
		"binary_junk":                 malformed.BinaryJunk(),
		"null_bytes":                  malformed.NullBytes(),
		"duplicate_top_level_keys":    malformed.DuplicateTopLevelKeys(),
		"wrong_top_level_type_scalar": malformed.WrongTopLevelType(),
		"wrong_top_level_type_seq":    malformed.WrongTopLevelSequence(),
		// deep_nesting_200 is a FLAT body rather than
		// malformed.DeepNesting(200), which wraps its nesting under
		// spec:. Both shapes are still schema-validated, but the flat one
		// is the shape Sharko writes, so this is the one worth pinning:
		// no clusters: key plus an unrecognised nested structure must
		// fail (missing required clusters:, additionalProperties: false
		// rejects level0). Same thing the case always tested —
		// pathological nesting must error, never silently parse as zero
		// clusters.
		"deep_nesting_200":           deepNestingFlat(200),
		"huge_flat_list_5000":        malformed.HugeFlatList(5000),
		"unknown_sharko_api_version": malformed.UnknownSharkoAPIVersion(),
		"go_template_directives":     malformed.GoTemplateDirectives(),
		"tab_indentation":            malformed.TabIndentation(),
		"wrong_kind_envelope":        []byte("apiVersion: sharko.dev/v1\nkind: AddonCatalog\nspec:\n  applicationsets: []\n"),
		"clusters_wrong_type_string": []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: \"not-a-list\"\n"),
		"clusters_wrong_type_map":    []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters:\n    name: prod-eu\n"),
		"cluster_entry_not_object":   []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters:\n    - \"just-a-string\"\n"),
		"legacy_bare_binary_junk":    malformed.BinaryJunk(),
		"legacy_bare_wrong_type":     []byte("clusters: \"not-a-list\"\n"),
		"legacy_bare_truncated":      []byte("clusters:\n  - name: \"unterminated"),
		"apiVersion_present_no_kind": []byte("apiVersion: sharko.dev/v1\nspec:\n  clusters: []\n"),
		"empty_kind_string":          []byte("apiVersion: sharko.dev/v1\nkind: \"\"\nspec:\n  clusters: []\n"),
	}

	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var spec ManagedClustersSpec
			var err error
			malformed.AssertNoPanic(t, name, func() {
				spec, err = LoadManagedClusters(body)
			})
			if err == nil {
				t.Fatalf("LoadManagedClusters(%s): expected an error, got success with %d clusters", name, len(spec.Clusters))
			}
		})
	}
}

func TestMalformedInput_LoadClusterAddons(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":                       malformed.Empty(),
		"whitespace_only":             malformed.Whitespace(),
		"binary_junk":                 malformed.BinaryJunk(),
		"null_bytes":                  malformed.NullBytes(),
		"truncated_block_mapping":     malformed.TruncatedBlockMapping(),
		"duplicate_nested_keys":       malformed.DuplicateNestedKeys(),
		"wrong_top_level_type":        malformed.WrongTopLevelType(),
		"deep_nesting_200":            malformed.DeepNesting(200),
		"unknown_sharko_api_version":  []byte("apiVersion: sharko.dev/v99\nkind: ClusterAddons\nspec:\n  cluster: prod-eu\n  addons: {}\n"),
		"not_enveloped_at_all":        []byte("cluster: prod-eu\naddons: {}\n"),
		"wrong_kind":                  []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: []\n"),
		"addons_wrong_type_list":      []byte("apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  cluster: prod-eu\n  addons:\n    - cert-manager\n"),
		"addon_entry_missing_enabled": []byte("apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  cluster: prod-eu\n  addons:\n    cert-manager: {}\n"),
		"addon_entry_enabled_wrong_type": []byte(
			"apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  cluster: prod-eu\n  addons:\n    cert-manager:\n      enabled: \"yes\"\n"),
		"missing_cluster_field": []byte("apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  addons: {}\n"),
		"settings_forbidden_field": []byte(
			"apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  cluster: prod-eu\n  addons:\n    cert-manager:\n      enabled: true\n      settings:\n        preserveResourcesOnDeletion: true\n"),
	}

	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var spec ClusterAddonsSpec
			var err error
			malformed.AssertNoPanic(t, name, func() {
				spec, err = LoadClusterAddons(body)
			})
			if err == nil {
				t.Fatalf("LoadClusterAddons(%s): expected an error, got success with cluster=%q", name, spec.Cluster)
			}
		})
	}
}
