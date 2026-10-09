package main

// Story 8.6 (v4 Wave 2) — the all-or-nothing audit for `sharko
// validate-config`, the operator-facing front end over
// internal/schema.Validator. validate_config_test.go already covers the
// "genuinely broken YAML, no templating, must fail loudly" case
// (TestValidateConfig_MalformedNoTemplate_Fail_Exit1) and the directory
// aggregation path; this file rounds out the malformed-input corpus
// (binary junk, deep nesting, wrong types, duplicate keys, tab
// indentation) and is the panic-safety proof: every case here must come
// back as errValidationFailed from runValidateConfig — a fail-exit-1
// verdict — never a panic that would take the CLI process down mid-walk
// of a directory (which would also strand every OTHER file's verdict
// unprinted, the CLI's own version of "half-applied").

import (
	"bytes"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/testutil/malformed"
)

func TestMalformedInput_ValidateConfig_NeverPanicsAlwaysFails(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"binary_junk":               malformed.BinaryJunk(),
		"null_bytes":                malformed.NullBytes(),
		"truncated_block_mapping":   malformed.TruncatedBlockMapping(),
		"truncated_flow_sequence":   malformed.TruncatedFlowSequence(),
		"deep_nesting_200":          malformed.DeepNesting(200),
		"tab_indentation_no_apiver": []byte("clusters:\n\t- name: prod-eu\n"),
		"duplicate_top_level_keys": []byte(`apiVersion: sharko.dev/v1
apiVersion: sharko.dev/v1
kind: ManagedClusters
kind: ManagedClusters
spec:
  clusters:
    - name: [not, a, string]
`),
		"wrong_kind_for_apiversion": []byte("apiVersion: sharko.dev/v1\nkind: TotallyUnknownKind\nspec: {}\n"),
		"unknown_sharko_apiversion": []byte("apiVersion: sharko.dev/v99\nkind: ManagedClusters\nspec:\n  clusters: []\n"),
		"cluster_addons_wrong_types": []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
spec:
  cluster: 12345
  addons: "not-a-map"
`),
	}

	for name, body := range cases {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := writeTempFile(t, dir, "broken.yaml", string(body))

			var buf bytes.Buffer
			var err error
			malformed.AssertNoPanic(t, name, func() {
				err = runValidateConfig(&buf, path, false)
			})

			if !errors.Is(err, errValidationFailed) {
				t.Fatalf("runValidateConfig(%s): expected errValidationFailed, got %v\noutput: %s", name, err, buf.String())
			}
		})
	}
}

// TestMalformedInput_ValidateConfig_EmptyFile_SkipsCleanly pins the
// documented "not a Sharko-enveloped file" skip path for an empty or
// blank file — schema.IsEnveloped treats a blank body as not-enveloped,
// so it is a clean skip (exit 0), never a failure and never a panic.
func TestMalformedInput_ValidateConfig_EmptyFile_SkipsCleanly(t *testing.T) {
	t.Parallel()
	for name, body := range map[string][]byte{
		"empty":           malformed.Empty(),
		"whitespace_only": malformed.Whitespace(),
	} {
		body := body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := writeTempFile(t, dir, "blank.yaml", string(body))

			var buf bytes.Buffer
			var err error
			malformed.AssertNoPanic(t, name, func() {
				err = runValidateConfig(&buf, path, false)
			})
			if err != nil {
				t.Fatalf("runValidateConfig(%s): expected a clean skip, got error %v\noutput: %s", name, err, buf.String())
			}
			if !bytes.Contains(buf.Bytes(), []byte("skip:")) {
				t.Errorf("runValidateConfig(%s): expected a skip line, got:\n%s", name, buf.String())
			}
		})
	}
}
