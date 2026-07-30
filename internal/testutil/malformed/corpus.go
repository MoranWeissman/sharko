// Package malformed is the shared malformed-input corpus for Story 8.6's
// all-or-nothing audit (v4 Wave 2): a small set of generic, reusable byte
// fixtures that every repo-file reader across Sharko is expected to survive
// without panicking and without applying a partial result.
//
// These primitives are deliberately generic (not tied to any one Sharko
// kind) — each reader package wraps them into its own kind-specific
// envelopes (e.g. a ClusterAddons body, an AddonCatalogDelta body) in its
// own test file, alongside kind-specific malformed cases (wrong types, wrong
// kind, schema violations) that only make sense for that reader.
//
// See internal/testutil/malformed/README (this doc comment) for the
// inventory of readers this corpus is exercised against — the full list
// lives in each package's own malformed_input_test.go per the "documented
// inline" convention the story asked for; this file is the shared byte
// generator only.
package malformed

import (
	"fmt"
	"strings"
)

// Empty returns a zero-length body — the "file exists but has nothing in
// it" case. Distinct from Whitespace, which some readers treat differently
// (both should be handled as "no content", never panic).
func Empty() []byte { return []byte{} }

// Whitespace returns a body that is nothing but blank lines and spaces —
// technically non-empty bytes, semantically empty content. Deliberately
// space-only (no tabs): YAML forbids literal tabs for indentation even on
// an otherwise-blank line, so a tab here would make this case indistinguishable
// from TabIndentation's dedicated "tabs are illegal" case.
func Whitespace() []byte { return []byte("   \n\n   \n") }

// TruncatedFlowSequence returns YAML with an unterminated flow collection
// (an opened `[` never closed) — a common "the write got cut off mid-file"
// shape (e.g. a truncated git blob, a killed process mid-write).
func TruncatedFlowSequence() []byte {
	return []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: [{name: a, labels: {")
}

// TruncatedMidLine returns YAML that stops mid key, with a trailing
// unterminated quote — simulates a file cut off by a disk-full write or a
// network interruption during a git blob fetch.
func TruncatedMidLine() []byte {
	return []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters:\n    - name: \"prod-eu")
}

// TruncatedBlockMapping returns YAML with an indentation error partway
// through a block mapping — the shape a hand-edit gone wrong produces (a
// dedent that doesn't line up with any enclosing block).
func TruncatedBlockMapping() []byte {
	return []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters:\n    - name: prod-eu\n   labels:\n")
}

// BinaryJunk returns non-UTF-8, non-YAML bytes — the "someone committed a
// binary file with a .yaml extension" case (a stray image, a core dump, an
// accidental `cat /dev/urandom > file.yaml`).
func BinaryJunk() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	// Force a few bytes that are structurally invalid UTF-8 continuation
	// bytes with no lead byte, so this can never accidentally decode as
	// valid (if unlikely) UTF-8 text.
	b[0] = 0xFF
	b[1] = 0xFE
	b[2] = 0x00
	b[3] = 0x80
	return b
}

// NullBytes returns a body that is nothing but NUL bytes — a distinct
// binary-junk shape some YAML/JSON decoders choke on differently than
// BinaryJunk's varied byte range.
func NullBytes() []byte {
	return make([]byte, 64)
}

// DuplicateTopLevelKeys returns YAML with the same top-level key declared
// twice with different values. YAML spec says this is technically
// disallowed, but yaml.v3's default decoder is lenient and takes the last
// value — readers must not panic on it, whichever value wins.
func DuplicateTopLevelKeys() []byte {
	return []byte(`apiVersion: sharko.dev/v1
apiVersion: sharko.dev/v1
kind: ManagedClusters
kind: ManagedClusters
spec:
  clusters: []
`)
}

// DuplicateNestedKeys returns YAML with a duplicated key inside a nested
// mapping (an addon entry naming "enabled" twice with different values).
func DuplicateNestedKeys() []byte {
	return []byte(`apiVersion: sharko.dev/v1
kind: ClusterAddons
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      enabled: false
`)
}

// WrongTopLevelType returns YAML whose document root is a scalar, not a
// mapping — e.g. a file that is just the word "true" or a bare number.
// Every envelope reader expects a mapping at the root.
func WrongTopLevelType() []byte {
	return []byte("true")
}

// WrongTopLevelSequence returns YAML whose document root is a sequence
// (a bare list), not a mapping — the shape you'd get from accidentally
// pasting the INSIDE of a list into the file instead of the whole document.
func WrongTopLevelSequence() []byte {
	return []byte("- one\n- two\n- three\n")
}

// DeepNesting returns a YAML document with `depth` levels of nested
// mappings under a `spec:` key — the "someone fat-fingered a huge
// pathological structure" case. Exercises decoder recursion limits without
// necessarily being large in byte count.
func DeepNesting(depth int) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n")
	indent := "  "
	for i := 0; i < depth; i++ {
		b.WriteString(strings.Repeat(indent, i+1))
		b.WriteString(fmt.Sprintf("level%d:\n", i))
	}
	b.WriteString(strings.Repeat(indent, depth+1))
	b.WriteString("leaf: true\n")
	return []byte(b.String())
}

// HugeFlatList returns a YAML document with `n` entries in a flat
// sequence under `clusters:` — the "someone scripted ten thousand cluster
// entries into one file" case. Exercises allocation/perf paths without
// pathological nesting.
func HugeFlatList(n int) []byte {
	var b strings.Builder
	b.WriteString("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "    - name: cluster-%d\n      labels: {}\n", i)
	}
	return []byte(b.String())
}

// UnknownSharkoAPIVersion returns an enveloped body from a future/unknown
// Sharko apiVersion in the sharko.* family — the H2 forward-guard case
// (internal/schema.UnknownSharkoAPIVersionError): must be a loud error,
// never a silent "treat as legacy, zero entries" fallthrough.
func UnknownSharkoAPIVersion() []byte {
	return []byte("apiVersion: sharko.dev/v99\nkind: ManagedClusters\nspec:\n  clusters: []\n")
}

// GoTemplateDirectives returns a body containing raw Helm/Go template
// delimiters — not valid YAML until rendered. Some readers (the
// validate-config CLI) special-case this; others should just fail cleanly
// as a YAML parse error.
func GoTemplateDirectives() []byte {
	return []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n  clusters: {{ .Values.clusters | toYaml }}\n")
}

// TabIndentation returns YAML that uses a literal tab character for
// indentation — the YAML spec forbids tabs for indentation, and this is a
// common hand-edit mistake (an editor set to insert tabs instead of
// spaces).
func TabIndentation() []byte {
	return []byte("apiVersion: sharko.dev/v1\nkind: ManagedClusters\nspec:\n\tclusters: []\n")
}
