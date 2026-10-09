package gitops

// Story 8.6 (v4 Wave 2) — the all-or-nothing audit for
// UpdateEnginePinVersion / EnginePinVersion, the two readers of
// sharko-engine.yaml (design doc §2.5, "the engine pin"). This file
// is different from the schema-validated envelope readers elsewhere in
// the audit: sharko-engine.yaml is a real ArgoCD Application object,
// not a Sharko envelope, so there is no JSON Schema gate here — the
// locate-by-yaml.Node walk in locateEnginePinTargetRevision is the entire
// defense. Every case below must come back as a Go error, never a panic,
// and (for UpdateEnginePinVersion specifically) never a partial/garbled
// rewrite of the input bytes.

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/testutil/malformed"
)

func TestMalformedInput_EnginePinVersion(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":                    malformed.Empty(),
		"whitespace_only":          malformed.Whitespace(),
		"binary_junk":              malformed.BinaryJunk(),
		"null_bytes":               malformed.NullBytes(),
		"truncated_block_mapping":  malformed.TruncatedBlockMapping(),
		"truncated_flow_sequence":  malformed.TruncatedFlowSequence(),
		"wrong_top_level_type":     malformed.WrongTopLevelType(),
		"wrong_top_level_sequence": malformed.WrongTopLevelSequence(),
		"deep_nesting_200":         malformed.DeepNesting(200),
		"tab_indentation":          malformed.TabIndentation(),
		"no_spec":                  []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		"spec_not_a_mapping":       []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec: \"not-a-map\"\n"),
		"no_sources":               []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  project: default\n"),
		"sources_not_a_sequence":   []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources: \"not-a-list\"\n"),
		"sources_map_not_sequence": []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    chart: sharko-engine\n"),
		"source_items_are_scalars": []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    - \"not-a-mapping\"\n"),
		"no_matching_chart":        []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    - repoURL: https://example.com\n      chart: some-other-chart\n      targetRevision: 1.0.0\n"),
		"matching_chart_no_pin":    []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    - repoURL: https://example.com\n      chart: sharko-engine\n"),
		"chart_field_wrong_type":   []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    - chart: [not, a, scalar]\n      targetRevision: 1.0.0\n"),
	}

	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var err error
			malformed.AssertNoPanic(t, name, func() {
				_, err = EnginePinVersion(body, "sharko-engine")
			})
			if err == nil {
				t.Fatalf("EnginePinVersion(%s): expected an error, got success", name)
			}
		})
	}
}

func TestMalformedInput_UpdateEnginePinVersion(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":                   malformed.Empty(),
		"binary_junk":             malformed.BinaryJunk(),
		"truncated_block_mapping": malformed.TruncatedBlockMapping(),
		"wrong_top_level_type":    malformed.WrongTopLevelType(),
		"deep_nesting_200":        malformed.DeepNesting(200),
		"no_spec":                 []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		"sources_not_a_sequence":  []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources: \"not-a-list\"\n"),
		"no_matching_chart":       []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    - repoURL: https://example.com\n      chart: some-other-chart\n      targetRevision: 1.0.0\n"),
		"matching_chart_no_pin":   []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    - repoURL: https://example.com\n      chart: sharko-engine\n"),
		// A targetRevision value that IS located but whose source line does
		// not match the splice regex — the regex expects
		// `targetRevision: <value>` on its own line; a flow-style inline
		// mapping breaks that assumption on purpose.
		"targetRevision_inline_flow_style": []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\nspec:\n  sources:\n    - {chart: sharko-engine, targetRevision: 1.0.0}\n"),
	}

	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var err error
			malformed.AssertNoPanic(t, name, func() {
				_, err = UpdateEnginePinVersion(body, "sharko-engine", "9.9.9")
			})
			if err == nil {
				t.Fatalf("UpdateEnginePinVersion(%s): expected an error, got success", name)
			}
		})
	}
}
