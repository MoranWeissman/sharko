package clusterreconciler

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/testutil/malformed"
)

// Story 8.6 (v4 Wave 2) — the all-or-nothing audit for readV4AddonLabels,
// the reader behind clusters/*.yaml on the reconciler's poll path.
//
// This reader's contract is deliberately DIFFERENT from every other
// reader in this audit: it is a per-tick CONVERGENCE loop, not a batch
// write, so "all-or-nothing" here does not mean "one bad file blocks
// everything" — it means the OPPOSITE: one malformed clusters/<name>.yaml
// must be skipped (logged, not applied) while every OTHER cluster in the
// same tick still converges normally. See the doc comment on
// readV4AddonLabels in v4_assignments.go for the reasoning ("the other
// clusters still converge, and the affected cluster keeps whatever labels
// it already has rather than having them all wiped by a 'successfully
// read zero addons' lie").
//
// So this suite proves two things per malformed case, mixed into a single
// tick alongside one known-good cluster:
//  1. readV4AddonLabels never panics on the malformed file.
//  2. The known-good cluster in the SAME tick still gets its labels — the
//     malformed sibling file does not take the whole tick down.
func TestMalformedInput_ReadV4AddonLabels_SkipsBadFilesKeepsGoodOnes(t *testing.T) {
	t.Parallel()

	goodClusterYAML := []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
`)

	cases := map[string][]byte{
		"empty":                      malformed.Empty(),
		"whitespace_only":            malformed.Whitespace(),
		"binary_junk":                malformed.BinaryJunk(),
		"null_bytes":                 malformed.NullBytes(),
		"truncated_block_mapping":    malformed.TruncatedBlockMapping(),
		"truncated_flow_sequence":    malformed.TruncatedFlowSequence(),
		"wrong_top_level_type":       malformed.WrongTopLevelType(),
		"deep_nesting_200":           malformed.DeepNesting(200),
		"tab_indentation":            malformed.TabIndentation(),
		"not_enveloped":              []byte("cluster: broken-cluster\naddons: {}\n"),
		"wrong_kind":                 []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: []\n"),
		"unknown_sharko_api_version": []byte("apiVersion: sharko.dev/v99\nkind: ClusterAddons\nspec:\n  cluster: broken-cluster\n  addons: {}\n"),
		"addons_wrong_type":          []byte("apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  cluster: broken-cluster\n  addons:\n    - cert-manager\n"),
		"missing_cluster_field":      []byte("apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  addons: {}\n"),
		"empty_cluster_field":        []byte("apiVersion: sharko.dev/v1\nkind: ClusterAddons\nspec:\n  cluster: \"\"\n  addons: {}\n"),
	}

	for name, badBody := range cases {
		badBody := badBody
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			gp := &fakeGit{files: map[string][]byte{
				"clusters/prod-eu.yaml":        goodClusterYAML,
				"clusters/broken-cluster.yaml": badBody,
			}}

			var labels map[string]map[string]string
			malformed.AssertNoPanic(t, name, func() {
				labels = readV4AddonLabels(context.Background(), gp, "main")
			})

			if _, ok := labels["broken-cluster"]; ok {
				t.Errorf("readV4AddonLabels(%s): expected broken-cluster to be skipped (no entry), got %v", name, labels["broken-cluster"])
			}
			goodLabels, ok := labels["prod-eu"]
			if !ok {
				t.Fatalf("readV4AddonLabels(%s): expected prod-eu to still converge despite the sibling malformed file, got no entry at all (labels=%v)", name, labels)
			}
			const wantKey = "addons.sharko.dev/cert-manager"
			if goodLabels[wantKey] != "enabled" {
				t.Errorf("readV4AddonLabels(%s): expected prod-eu[%q]=enabled, got %v", name, wantKey, goodLabels)
			}
		})
	}
}

// TestMalformedInput_ReadV4AddonLabels_AllFilesMalformed proves the
// degenerate case — every file in clusters/ is broken — comes back as an
// empty map, never a panic and never a partial/garbled result.
func TestMalformedInput_ReadV4AddonLabels_AllFilesMalformed(t *testing.T) {
	t.Parallel()
	gp := &fakeGit{files: map[string][]byte{
		"clusters/a.yaml": malformed.BinaryJunk(),
		"clusters/b.yaml": malformed.TruncatedBlockMapping(),
		"clusters/c.yaml": []byte("not: enveloped\n"),
	}}

	var labels map[string]map[string]string
	malformed.AssertNoPanic(t, "all_malformed", func() {
		labels = readV4AddonLabels(context.Background(), gp, "main")
	})
	if len(labels) != 0 {
		t.Errorf("readV4AddonLabels(all malformed): expected an empty map, got %v", labels)
	}
}
