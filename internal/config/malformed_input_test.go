package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/testutil/malformed"
)

// deepNestingFlat builds a FLAT (apiVersion + kind at the top level, no
// spec: wrapper) Sharko document with `depth` levels of nesting under an
// unrecognised key instead of clusters:.
//
// malformed.DeepNesting wraps the same nesting under spec:, which is the
// legacy v3 shape ParseClusterAddons deliberately skips schema validation
// for, so it can no longer prove anything here. This body is always
// schema-validated, so the case keeps proving what it always proved:
// pathological nesting in a real Sharko file must error, never quietly
// parse as zero clusters. Mirrors the helper of the same name in
// internal/models.
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

// This file is Story 8.6's (v4 Wave 2) all-or-nothing audit for the four
// envelope/legacy readers this package owns:
//
//   - (*Parser).ParseClusterAddons — managed-clusters.yaml, both the legacy
//     bare shape and the v4 envelope.
//   - (*Parser).ParseAddonsCatalog — addons-catalog.yaml (v3), both shapes.
//   - LoadAddonCatalog — catalog/addons.yaml (v4 AddonCatalog),
//     enveloped only, no legacy shape.
//   - LoadMarketplaceSourcesFromFile — configuration/marketplace-sources.yaml,
//     enveloped only.
//
// Every case must come back as a Go error (or, for the legacy-empty cases,
// a documented empty success) — never a panic.

func TestMalformedInput_ParseClusterAddons(t *testing.T) {
	t.Parallel()
	p := NewParser()

	cases := map[string][]byte{
		"binary_junk":                malformed.BinaryJunk(),
		"null_bytes":                 malformed.NullBytes(),
		"truncated_flow_sequence":    malformed.TruncatedFlowSequence(),
		"truncated_block_mapping":    malformed.TruncatedBlockMapping(),
		"duplicate_top_level_keys":   malformed.DuplicateTopLevelKeys(),
		"wrong_top_level_type":       malformed.WrongTopLevelType(),
		"deep_nesting_200":           deepNestingFlat(200),
		"unknown_sharko_api_version": malformed.UnknownSharkoAPIVersion(),
		"tab_indentation":            malformed.TabIndentation(),
		"wrong_kind_envelope":        []byte("apiVersion: sharko.dev/v1\nkind: AddonCatalog\nspec:\n  applicationsets: []\n"),
		"legacy_bare_wrong_type":     []byte("clusters: \"not-a-list\"\n"),
		"legacy_bare_truncated":      []byte("clusters:\n  - name: \"unterminated"),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var err error
			malformed.AssertNoPanic(t, name, func() {
				_, err = p.ParseClusterAddons(body)
			})
			if err == nil {
				t.Fatalf("ParseClusterAddons(%s): expected an error, got success", name)
			}
		})
	}
}

// TestMalformedInput_ParseClusterAddons_EmptyIsLegacyEmptyFleet mirrors
// models.TestMalformedInput_LoadManagedClusters_EmptyIsLegacyEmptyFleet:
// an empty/blank body is documented legacy-empty-fleet behavior, not an
// error, because schema.IsEnveloped treats a blank body as "not enveloped"
// and the legacy branch happily unmarshals zero clusters.
func TestMalformedInput_ParseClusterAddons_EmptyIsLegacyEmptyFleet(t *testing.T) {
	t.Parallel()
	p := NewParser()
	for name, body := range map[string][]byte{
		"empty":           malformed.Empty(),
		"whitespace_only": malformed.Whitespace(),
	} {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := p.ParseClusterAddons(body)
			if err != nil {
				t.Fatalf("ParseClusterAddons(%s): expected success (empty fleet), got error: %v", name, err)
			}
			if len(out) != 0 {
				t.Fatalf("ParseClusterAddons(%s): expected 0 clusters, got %d", name, len(out))
			}
		})
	}
}

func TestMalformedInput_ParseAddonsCatalog(t *testing.T) {
	t.Parallel()
	p := NewParser()

	cases := map[string][]byte{
		"binary_junk":                malformed.BinaryJunk(),
		"null_bytes":                 malformed.NullBytes(),
		"truncated_flow_sequence":    malformed.TruncatedFlowSequence(),
		"truncated_block_mapping":    malformed.TruncatedBlockMapping(),
		"duplicate_top_level_keys":   malformed.DuplicateTopLevelKeys(),
		"wrong_top_level_type":       malformed.WrongTopLevelType(),
		"deep_nesting_200":           deepNestingFlat(200),
		"unknown_sharko_api_version": []byte("apiVersion: sharko.dev/v99\nkind: AddonCatalog\nspec:\n  applicationsets: []\n"),
		"wrong_kind_envelope":        []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: []\n"),
		"legacy_bare_wrong_type":     []byte("applicationsets: \"not-a-list\"\n"),
		"legacy_bare_truncated":      []byte("applicationsets:\n  - name: \"unterminated"),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var err error
			malformed.AssertNoPanic(t, name, func() {
				_, err = p.ParseAddonsCatalog(body)
			})
			if err == nil {
				t.Fatalf("ParseAddonsCatalog(%s): expected an error, got success", name)
			}
		})
	}
}

func TestMalformedInput_LoadAddonCatalog(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":                      malformed.Empty(),
		"whitespace_only":            malformed.Whitespace(),
		"binary_junk":                malformed.BinaryJunk(),
		"null_bytes":                 malformed.NullBytes(),
		"truncated_block_mapping":    malformed.TruncatedBlockMapping(),
		"wrong_top_level_type":       malformed.WrongTopLevelType(),
		"deep_nesting_200":           deepNestingFlat(200),
		"not_enveloped_at_all":       []byte("addons: {}\n"),
		"wrong_kind":                 []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: []\n"),
		"unknown_sharko_api_version": []byte("apiVersion: sharko.dev/v99\nkind: AddonCatalog\nspec:\n  addons: {}\n"),
		"addons_wrong_type_list":     []byte("apiVersion: sharko.dev/v1\nkind: AddonCatalog\nspec:\n  addons:\n    - cert-manager\n"),
		"addon_name_illegal_chars":   []byte("apiVersion: sharko.dev/v1\nkind: AddonCatalog\nspec:\n  addons:\n    \"../../engine\":\n      chart: x\n      repoURL: https://example.com\n      version: 1.0.0\n"),
		"settings_wrong_type":        []byte("apiVersion: sharko.dev/v1\nkind: AddonCatalog\nspec:\n  addons:\n    cert-manager:\n      settings: \"not-an-object\"\n"),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var err error
			malformed.AssertNoPanic(t, name, func() {
				_, err = LoadAddonCatalog(body)
			})
			if err == nil {
				t.Fatalf("LoadAddonCatalog(%s): expected an error, got success", name)
			}
		})
	}
}

func TestMalformedInput_LoadMarketplaceSourcesFromFile(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"empty":                      malformed.Empty(),
		"whitespace_only":            malformed.Whitespace(),
		"binary_junk":                malformed.BinaryJunk(),
		"null_bytes":                 malformed.NullBytes(),
		"truncated_block_mapping":    malformed.TruncatedBlockMapping(),
		"wrong_top_level_type":       malformed.WrongTopLevelType(),
		"deep_nesting_200":           deepNestingFlat(200),
		"not_enveloped_at_all":       []byte("sources: []\n"),
		"wrong_kind":                 []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: []\n"),
		"unknown_sharko_api_version": []byte("apiVersion: sharko.dev/v99\nkind: MarketplaceSources\nspec:\n  sources: []\n"),
		"sources_wrong_type":         []byte("apiVersion: sharko.dev/v1\nkind: MarketplaceSources\nspec:\n  sources: \"not-a-list\"\n"),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var err error
			malformed.AssertNoPanic(t, name, func() {
				_, err = LoadMarketplaceSourcesFromFile(body)
			})
			if err == nil {
				t.Fatalf("LoadMarketplaceSourcesFromFile(%s): expected an error, got success", name)
			}
		})
	}
}
