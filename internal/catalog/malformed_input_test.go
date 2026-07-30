package catalog

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/testutil/malformed"
)

// This file is Story 8.6's (v4 Wave 2) all-or-nothing audit for
// LoadBytes / LoadBytesWithSource — the curated-catalog reader. Unlike the
// v4 envelope kinds, the curated catalog is bare `addons: [...]` YAML with
// no apiVersion/kind wrapper, so its malformed-input surface is different:
// this file focuses on structural breakage (truncation, binary junk, wrong
// types, deep nesting) plus the loader's own semantic checks (duplicate
// entry names, unknown category/curated_by enum values) that are unique to
// this reader.
func TestMalformedInput_LoadBytes(t *testing.T) {
	t.Parallel()

	validAddon := `  - name: cert-manager
    description: X.509 certificate management for Kubernetes.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    license: Apache-2.0
    category: security
    maintainers: ["jetstack"]
    curated_by: ["cncf-graduated"]
`

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
		"no_addons_key":            []byte("notaddons:\n  - name: cert-manager\n"),
		"addons_wrong_type":        []byte("addons: \"not-a-list\"\n"),
		"addons_empty_list":        []byte("addons: []\n"),
		"entry_missing_required_fields": []byte(`addons:
  - name: cert-manager
`),
		"entry_bad_category": []byte(`addons:
` + replaceOnce(validAddon, "category: security", "category: not-a-real-category")),
		"entry_bad_curated_by": []byte(`addons:
` + replaceOnce(validAddon, `curated_by: ["cncf-graduated"]`, `curated_by: ["not-a-real-tier"]`)),
		"duplicate_entry_names": []byte(`addons:
` + validAddon + validAddon),
		"entry_name_wrong_type": []byte(`addons:
  - name: [not, a, string]
    description: X
    chart: x
    repo: https://example.com
    default_namespace: x
    license: Apache-2.0
    category: security
`),
	}

	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var err error
			malformed.AssertNoPanic(t, name, func() {
				_, err = LoadBytes(body)
			})
			if err == nil {
				t.Fatalf("LoadBytes(%s): expected an error, got success", name)
			}
		})
	}
}

// replaceOnce is a tiny wrapper over strings.Replace so the malformed
// fixtures above can mutate one known-good field in validAddon with an
// obvious, self-documenting call.
func replaceOnce(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}
