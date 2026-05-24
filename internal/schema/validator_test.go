// V125-1-9 Story 9.4 — validator tests.
package schema

import (
	"errors"
	"strings"
	"testing"
)

// validManagedClustersBody is the canonical happy-path body — the same
// shape the docstring example in
// docs/design/2026-05-12-v125-architectural-todos.md lines 100-114
// uses. Reused across happy-path tests so the validator is exercised
// against the SAME bytes a human author would type from the design doc.
const validManagedClustersBody = `# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      region: eu-west-1
      secretPath: clusters/prod-eu
      labels:
        cert-manager: enabled
    - name: staging-eu
      labels:
        metrics-server: enabled
`

const validAddonCatalogBody = `# yaml-language-server: $schema=https://sharko.io/schemas/addons-catalog.v1.json
apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: cert-manager
      repoURL: https://charts.jetstack.io
      chart: cert-manager
      version: "1.16.3"
      namespace: cert-manager
`

// TestNewValidator_CompilesEmbeddedSchemas pins the build-time
// invariant: NewValidator never fails for the committed embedded
// schemas. If this regresses, the embedded JSON has drifted from
// what santhosh-tekuri can compile — almost always a `make
// generate-schemas` step the author forgot.
func TestNewValidator_CompilesEmbeddedSchemas(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	if v == nil {
		t.Fatal("NewValidator returned nil validator with no error")
	}
	if _, ok := v.schemas[KindManagedClusters]; !ok {
		t.Errorf("missing compiled schema for kind %q", KindManagedClusters)
	}
	if _, ok := v.schemas[KindAddonCatalog]; !ok {
		t.Errorf("missing compiled schema for kind %q", KindAddonCatalog)
	}
}

// TestDefaultValidator_SameInstanceAcrossCalls confirms the lazy-
// singleton contract. The plan's < 5ms-per-call performance target
// (§164) depends on this — if every caller re-compiled the schema set
// the target is unreachable.
func TestDefaultValidator_SameInstanceAcrossCalls(t *testing.T) {
	t.Parallel()
	a, err := DefaultValidator()
	if err != nil {
		t.Fatalf("DefaultValidator first call: %v", err)
	}
	b, err := DefaultValidator()
	if err != nil {
		t.Fatalf("DefaultValidator second call: %v", err)
	}
	if a != b {
		t.Errorf("DefaultValidator returned different pointers across calls (singleton invariant broken)")
	}
}

// TestValidator_ValidEnvelopedYAML_Pass — both kinds, valid bodies,
// returns nil. The happy path the design doc promises.
func TestValidator_ValidEnvelopedYAML_Pass(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	cases := []struct {
		name string
		kind string
		body string
	}{
		{"ManagedClusters happy path", KindManagedClusters, validManagedClustersBody},
		{"AddonCatalog happy path", KindAddonCatalog, validAddonCatalogBody},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := v.Validate(tc.kind, []byte(tc.body)); err != nil {
				t.Errorf("Validate(%s): unexpected error: %v", tc.kind, err)
			}
		})
	}
}

// TestValidator_InvalidEnvelopedYAML_Reject covers every failure mode
// the plan calls out: missing required, wrong type, extra forbidden
// field (additionalProperties: false), and wrong-kind-against-wrong-
// schema (validating an AddonCatalog body against the ManagedClusters
// schema).
func TestValidator_InvalidEnvelopedYAML_Reject(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	cases := []struct {
		name             string
		kind             string
		body             string
		wantSubstr       []string // each must appear in at least one violation OR in the formatted error
		wantValidationFn func(t *testing.T, vf *ValidationFailure)
	}{
		{
			name: "missing required field spec.clusters",
			kind: KindManagedClusters,
			body: `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec: {}
`,
			wantSubstr: []string{"clusters"},
		},
		{
			name: "wrong type — apiVersion is integer not string-const",
			kind: KindManagedClusters,
			body: `apiVersion: 42
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
`,
			wantSubstr: []string{"apiVersion"},
		},
		{
			name: "extra forbidden field at spec level (additionalProperties: false)",
			kind: KindManagedClusters,
			body: `apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
  unknownField: "x"
`,
			wantSubstr: []string{"unknownField"},
		},
		{
			name: "wrong kind in wrong schema — AddonCatalog body validated against ManagedClusters",
			kind: KindManagedClusters,
			body: validAddonCatalogBody,
			// kind const mismatch is the headline violation; presence
			// of "AddonCatalog" or "kind" in the error proves the
			// const constraint fired.
			wantSubstr: []string{"kind"},
		},
		{
			name: "AddonCatalog — missing required field spec.applicationsets",
			kind: KindAddonCatalog,
			body: `apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec: {}
`,
			wantSubstr: []string{"applicationsets"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := v.Validate(tc.kind, []byte(tc.body))
			if err == nil {
				t.Fatalf("Validate(%s): expected error, got nil", tc.kind)
			}

			var vf *ValidationFailure
			if !errors.As(err, &vf) {
				t.Fatalf("Validate(%s): error not *ValidationFailure (got %T): %v", tc.kind, err, err)
			}
			if vf.Kind != tc.kind {
				t.Errorf("ValidationFailure.Kind = %q, want %q", vf.Kind, tc.kind)
			}
			if len(vf.Violations) == 0 {
				t.Fatalf("ValidationFailure.Violations is empty; expected at least one")
			}

			// Each wantSubstr must appear in the error string (which
			// concatenates every violation). This is lenient on
			// purpose — santhosh-tekuri's exact wording can shift
			// between minor versions; the substring contract is
			// stable enough to be useful and loose enough to survive.
			errStr := err.Error()
			for _, want := range tc.wantSubstr {
				if !strings.Contains(errStr, want) {
					t.Errorf("error %q: want substring %q (full violations: %v)",
						errStr, want, vf.Violations)
				}
			}

			if tc.wantValidationFn != nil {
				tc.wantValidationFn(t, vf)
			}
		})
	}
}

// TestValidator_UnknownKind_Reject confirms an unknown kind is
// rejected cleanly (not a ValidationFailure — it's a programmer error,
// not a data error, so the caller should see a plain error rather
// than a structured violation list).
func TestValidator_UnknownKind_Reject(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	err = v.Validate("NotARealKind", []byte(validManagedClustersBody))
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
	var vf *ValidationFailure
	if errors.As(err, &vf) {
		t.Errorf("unknown-kind error should NOT be *ValidationFailure (it's a programmer error): %v", err)
	}
	if !strings.Contains(err.Error(), "NotARealKind") {
		t.Errorf("error should name the unknown kind: %v", err)
	}
}

// TestValidator_ValidateAutoDetect_RoutesByKind exercises the
// auto-detect convenience used by the Story 9.5 CLI. A single valid
// body should route correctly to its kind without the caller having to
// pre-extract the kind.
func TestValidator_ValidateAutoDetect_RoutesByKind(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}

	cases := []struct {
		name string
		body string
	}{
		{"ManagedClusters auto-detected", validManagedClustersBody},
		{"AddonCatalog auto-detected", validAddonCatalogBody},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := v.ValidateAutoDetect([]byte(tc.body)); err != nil {
				t.Errorf("ValidateAutoDetect: unexpected error: %v", err)
			}
		})
	}
}

// TestValidator_ValidateAutoDetect_LegacyBody_Reject — auto-detect on
// a non-enveloped body returns an error rather than silently passing
// (the CLI must tell the operator "this file isn't a Sharko envelope"
// rather than "valid").
func TestValidator_ValidateAutoDetect_LegacyBody_Reject(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	legacy := []byte(`clusters:
  - name: prod-eu
`)
	err = v.ValidateAutoDetect(legacy)
	if err == nil {
		t.Fatal("expected error for legacy bare YAML, got nil")
	}
	if !strings.Contains(err.Error(), "not an enveloped") {
		t.Errorf("error should mention non-envelope shape: %v", err)
	}
}

// TestValidator_ValidateAutoDetect_UnknownKind_Reject — an enveloped
// body with an unknown kind (a future Sharko kind, a typo) is rejected
// with a kind-listing error so the operator sees what kinds are valid.
func TestValidator_ValidateAutoDetect_UnknownKind_Reject(t *testing.T) {
	t.Parallel()
	v, err := NewValidator()
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	body := []byte(`apiVersion: sharko.io/v1
kind: ChartReleases
metadata:
  name: chart-releases
spec: {}
`)
	err = v.ValidateAutoDetect(body)
	if err == nil {
		t.Fatal("expected error for unknown envelope kind, got nil")
	}
	if !strings.Contains(err.Error(), "ChartReleases") {
		t.Errorf("error should name the unknown kind: %v", err)
	}
	if !strings.Contains(err.Error(), KindManagedClusters) {
		t.Errorf("error should list known kinds (expected %q in: %v)", KindManagedClusters, err)
	}
}

// TestValidationFailure_ErrorFormat pins the user-facing error format.
// The leading "schema validation failed" prefix is consumed by Story
// 9.5's CLI (string-prefix match to render a coloured banner); changing
// the prefix here is a coordination commit, not a freestanding refactor.
func TestValidationFailure_ErrorFormat(t *testing.T) {
	t.Parallel()
	vf := &ValidationFailure{
		Kind: "ManagedClusters",
		Violations: []string{
			"/spec/clusters: missing required property \"name\"",
			"/metadata: additional property \"extra\" not allowed",
		},
	}
	got := vf.Error()
	if !strings.HasPrefix(got, "schema validation failed for kind \"ManagedClusters\":") {
		t.Errorf("error should start with canonical prefix; got %q", got)
	}
	if !strings.Contains(got, "2 violation(s)") {
		t.Errorf("error should include violation count; got %q", got)
	}
	for _, v := range vf.Violations {
		if !strings.Contains(got, v) {
			t.Errorf("error missing violation %q; got %q", v, got)
		}
	}
}

// TestValidationFailure_NilSafe — defensive against a nil-pointer call
// path. Not expected in production, but the Error() method is called
// by every consumer; a nil-panic would be a worst-case regression.
func TestValidationFailure_NilSafe(t *testing.T) {
	t.Parallel()
	var vf *ValidationFailure
	_ = vf.Error() // must not panic
}

// BenchmarkValidate_ManagedClusters_Enveloped — non-gated baseline for
// the < 5ms-per-call performance target from the plan §164. Captured
// in the dispatch return report as a single ns/op number; future
// regressions can be detected by running this benchmark and comparing
// against the reported baseline.
//
// Uses DefaultValidator so the cached singleton is what's measured —
// per-call construction would be a different (and unrepresentative)
// number.
func BenchmarkValidate_ManagedClusters_Enveloped(b *testing.B) {
	v, err := DefaultValidator()
	if err != nil {
		b.Fatalf("DefaultValidator: %v", err)
	}
	body := []byte(validManagedClustersBody)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := v.Validate(KindManagedClusters, body); err != nil {
			b.Fatalf("Validate: %v", err)
		}
	}
}

// BenchmarkValidate_AddonCatalog_Enveloped — companion benchmark for
// the addon-catalog schema. Not required by the plan but kept for
// symmetry — operators tuning one path should be able to compare both.
func BenchmarkValidate_AddonCatalog_Enveloped(b *testing.B) {
	v, err := DefaultValidator()
	if err != nil {
		b.Fatalf("DefaultValidator: %v", err)
	}
	body := []byte(validAddonCatalogBody)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := v.Validate(KindAddonCatalog, body); err != nil {
			b.Fatalf("Validate: %v", err)
		}
	}
}
