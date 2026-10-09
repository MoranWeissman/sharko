package schema

// Story 8.6 (v4 Wave 2) — the all-or-nothing audit's foundation layer.
// Every envelope reader across the whole codebase (internal/models,
// internal/config, cmd/sharko's validate-config) routes through
// IsEnveloped, Validator.Validate/ValidateAutoDetect, and
// LineForInstanceLocation before it ever gets to its own kind-specific
// logic. envelope_test.go, validator_test.go, and lineloc_test.go already
// cover the documented error-return contracts case by case; this file
// adds the corpus this audit introduced (binary junk, null bytes, deep
// nesting) as an explicit panic-safety proof at this shared choke point —
// if any of these three primitives ever panicked, EVERY reader built on
// top of them would inherit the crash.

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/testutil/malformed"
)

func TestMalformedInput_IsEnveloped_NeverPanics(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"binary_junk":              malformed.BinaryJunk(),
		"null_bytes":               malformed.NullBytes(),
		"truncated_block_mapping":  malformed.TruncatedBlockMapping(),
		"truncated_flow_sequence":  malformed.TruncatedFlowSequence(),
		"deep_nesting_200":         malformed.DeepNesting(200),
		"huge_flat_list_5000":      malformed.HugeFlatList(5000),
		"wrong_top_level_type":     malformed.WrongTopLevelType(),
		"tab_indentation":          malformed.TabIndentation(),
		"duplicate_top_level_keys": malformed.DuplicateTopLevelKeys(),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			malformed.AssertNoPanic(t, name, func() {
				_, _ = IsEnveloped(body)
			})
		})
	}
}

func TestMalformedInput_ValidateAutoDetect_NeverPanics(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	cases := map[string][]byte{
		"binary_junk":                malformed.BinaryJunk(),
		"null_bytes":                 malformed.NullBytes(),
		"truncated_block_mapping":    malformed.TruncatedBlockMapping(),
		"deep_nesting_200":           malformed.DeepNesting(200),
		"wrong_top_level_type":       malformed.WrongTopLevelType(),
		"unknown_sharko_api_version": malformed.UnknownSharkoAPIVersion(),
		"envelope_no_kind":           []byte("apiVersion: sharko.dev/v1\nspec: {}\n"),
		"envelope_empty_kind":        []byte("apiVersion: sharko.dev/v1\nkind: \"\"\nspec: {}\n"),
		"envelope_unknown_kind":      []byte("apiVersion: sharko.dev/v1\nkind: TotallyMadeUp\nspec: {}\n"),
	}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			malformed.AssertNoPanic(t, name, func() {
				_ = v.ValidateAutoDetect(body)
			})
		})
	}
}

func TestMalformedInput_Validate_WrongKindData_NeverPanics(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	// Feed EVERY known kind's validator the SAME malformed/wrong-shape
	// bodies — the validator must reject cleanly no matter which schema
	// is asked to validate against garbage, including another kind's
	// perfectly well-formed body (a ManagedClusters doc handed to the
	// AddonCatalog schema, etc.).
	kinds := []string{
		KindManagedClusters, KindAddonCatalog, KindDefaultAddons,
		KindMarketplaceSources, KindClusterAddons, KindAddonCatalog,
	}
	bodies := map[string][]byte{
		"binary_junk":      malformed.BinaryJunk(),
		"deep_nesting_200": malformed.DeepNesting(200),
		"empty_object":     []byte("{}\n"),
		"scalar_true":      malformed.WrongTopLevelType(),
	}
	for _, kind := range kinds {
		for name, body := range bodies {
			kind, name, body := kind, name, body
			t.Run(kind+"/"+name, func(t *testing.T) {
				t.Parallel()
				malformed.AssertNoPanic(t, kind+"/"+name, func() {
					_ = v.Validate(kind, body)
				})
			})
		}
	}
}

func TestMalformedInput_LineForInstanceLocation_NeverPanics(t *testing.T) {
	t.Parallel()
	cases := map[string][]byte{
		"binary_junk":             malformed.BinaryJunk(),
		"null_bytes":              malformed.NullBytes(),
		"truncated_block_mapping": malformed.TruncatedBlockMapping(),
		"deep_nesting_200":        malformed.DeepNesting(200),
		"wrong_top_level_type":    malformed.WrongTopLevelType(),
		"wrong_top_level_seq":     malformed.WrongTopLevelSequence(),
	}
	locations := []string{"", "/", "/spec", "/spec/clusters/0/name", "/../weird~1pointer~0here"}
	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, loc := range locations {
				loc := loc
				malformed.AssertNoPanic(t, name+" loc="+loc, func() {
					_, _ = LineForInstanceLocation(body, loc)
				})
			}
		})
	}
}
