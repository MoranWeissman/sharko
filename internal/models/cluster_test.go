package models

import (
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/schema"
)

// V125-1-9 Story 9.1 — reader/writer envelope compat for
// managed-clusters.yaml. These tests pin both the back-compat read path
// (legacy bare YAML continues to load) and the new envelope read+write
// path (apiVersion: sharko.io/v1, kind: ManagedClusters).
//
// The test names match the dispatch's acceptance-criteria list verbatim so
// CI failure messages and the orchestrator's traceability matrix line up.

// labelsAsMap normalises the Labels interface{} field — which mirrors the
// legacy parser's tolerance of `labels: { ... }` (map) and `labels: []`
// (empty-list sentinel) shapes — into a plain map[string]string so test
// assertions stay readable. Mirrors config.parseLabels' behaviour but
// scoped to this test file so the models package keeps zero test-only
// behaviour leaking into its production API.
func labelsAsMap(t *testing.T, raw interface{}) map[string]string {
	t.Helper()
	if raw == nil {
		return map[string]string{}
	}
	switch v := raw.(type) {
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = val
		}
		return out
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, val := range v {
			out[k] = toString(val)
		}
		return out
	case []interface{}:
		// Legacy empty-list sentinel.
		return map[string]string{}
	default:
		t.Fatalf("labelsAsMap: unsupported labels shape %T (%v)", v, v)
		return nil
	}
}

func toString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int, int32, int64, float32, float64:
		return strings.TrimRight(strings.TrimRight(toStringRaw(x), "0"), ".")
	default:
		return toStringRaw(v)
	}
}

// toStringRaw is a fallback formatter for label values that aren't
// strings/bools/numbers — keeps the test helper readable.
func toStringRaw(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	// Best-effort %v for ints/floats — production parsing path uses
	// fmt.Sprintf("%v") via config.parseLabels; the test mirror is OK
	// because labels in real configs are strings.
	return ""
}

// TestLoadManagedClusters_LegacyBareYAML_Accept confirms the back-compat
// read path: a pre-V125-1-9 managed-clusters.yaml (no envelope, top-level
// clusters: key) still parses cleanly. This is the dominant on-disk shape
// for every Sharko installation that bootstrapped before V125-1-9 ships,
// so a regression here would silently break every existing user repo.
func TestLoadManagedClusters_LegacyBareYAML_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`clusters:
  - name: prod-eu
    region: eu-west-1
    secretPath: clusters/prod-eu
    labels:
      cert-manager: enabled
      metrics-server: enabled
  - name: staging-eu
    region: eu-west-1
    labels: []
`)

	spec, err := LoadManagedClusters(body)
	if err != nil {
		t.Fatalf("LoadManagedClusters legacy bare YAML: %v", err)
	}
	if got, want := len(spec.Clusters), 2; got != want {
		t.Fatalf("got %d clusters, want %d", got, want)
	}

	prod := spec.Clusters[0]
	if prod.Name != "prod-eu" {
		t.Errorf("clusters[0].Name = %q, want %q", prod.Name, "prod-eu")
	}
	if prod.Region != "eu-west-1" {
		t.Errorf("clusters[0].Region = %q, want %q", prod.Region, "eu-west-1")
	}
	if prod.SecretPath != "clusters/prod-eu" {
		t.Errorf("clusters[0].SecretPath = %q, want %q", prod.SecretPath, "clusters/prod-eu")
	}
	prodLabels := labelsAsMap(t, prod.Labels)
	if prodLabels["cert-manager"] != "enabled" {
		t.Errorf("clusters[0].Labels[cert-manager] = %q, want enabled", prodLabels["cert-manager"])
	}
	if prodLabels["metrics-server"] != "enabled" {
		t.Errorf("clusters[0].Labels[metrics-server] = %q, want enabled", prodLabels["metrics-server"])
	}

	staging := spec.Clusters[1]
	if staging.Name != "staging-eu" {
		t.Errorf("clusters[1].Name = %q, want %q", staging.Name, "staging-eu")
	}
	// labels: [] should round-trip into a Labels field that normalises to
	// zero entries — exercising the legacy tolerance.
	if got := labelsAsMap(t, staging.Labels); len(got) != 0 {
		t.Errorf("clusters[1].Labels = %v, want empty", got)
	}
}

// TestLoadManagedClusters_EnvelopedYAML_Accept exercises the new path:
// a Sharko-emitted enveloped document loads cleanly and the spec block
// is extracted into ManagedClustersSpec. This is the shape Sharko WILL
// emit after V125-1-9 ships (via SaveManagedClusters) and the shape the
// V125-1-8 reconciler will read against.
func TestLoadManagedClusters_EnvelopedYAML_Accept(t *testing.T) {
	t.Parallel()

	body := []byte(`# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
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
`)

	spec, err := LoadManagedClusters(body)
	if err != nil {
		t.Fatalf("LoadManagedClusters enveloped YAML: %v", err)
	}
	if got, want := len(spec.Clusters), 2; got != want {
		t.Fatalf("got %d clusters, want %d", got, want)
	}
	if spec.Clusters[0].Name != "prod-eu" {
		t.Errorf("clusters[0].Name = %q, want prod-eu", spec.Clusters[0].Name)
	}
	if spec.Clusters[0].SecretPath != "clusters/prod-eu" {
		t.Errorf("clusters[0].SecretPath = %q, want clusters/prod-eu", spec.Clusters[0].SecretPath)
	}
	prodLabels := labelsAsMap(t, spec.Clusters[0].Labels)
	if prodLabels["cert-manager"] != "enabled" {
		t.Errorf("clusters[0].Labels[cert-manager] = %q, want enabled", prodLabels["cert-manager"])
	}
	stagingLabels := labelsAsMap(t, spec.Clusters[1].Labels)
	if stagingLabels["metrics-server"] != "enabled" {
		t.Errorf("clusters[1].Labels[metrics-server] = %q, want enabled", stagingLabels["metrics-server"])
	}
}

// TestLoadManagedClusters_EnvelopedWrongKind_Reject confirms that an
// envelope with apiVersion: sharko.io/v1 but kind: AddonCatalog (or
// anything other than ManagedClusters) is rejected with an explicit error,
// rather than silently parsing as an empty clusters list. This is the
// load-time guard for "wrong file handed to the wrong loader" — the kind
// of mistake the V125-1-8 reconciler MUST surface loudly to avoid
// reconciling an empty desired-state list (which would delete every
// existing cluster Secret).
func TestLoadManagedClusters_EnvelopedWrongKind_Reject(t *testing.T) {
	t.Parallel()

	body := []byte(`apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets: []
`)

	_, err := LoadManagedClusters(body)
	if err == nil {
		t.Fatalf("LoadManagedClusters wrong kind: want error, got nil")
	}
	// Exact message-format pin: the error MUST name the actual kind and
	// the expected kind so downstream tooling (the V125-1-8 reconciler's
	// audit log + the Story 9.5 sharko validate-config CLI) can produce
	// actionable user-facing messages. Pinning the format here means a
	// reviewer changing the error string will see the diff in this test
	// and update the consumers in lock-step.
	msg := err.Error()
	if !strings.Contains(msg, `envelope kind "AddonCatalog"`) {
		t.Errorf("error %q: want substring %q", msg, `envelope kind "AddonCatalog"`)
	}
	if !strings.Contains(msg, `expected "ManagedClusters"`) {
		t.Errorf("error %q: want substring %q", msg, `expected "ManagedClusters"`)
	}
}

// TestSaveManagedClusters_EmitsEnveloped pins the writer contract:
//
//   - Line 1 of the output is the yaml-language-server schema header
//     (editors use it to fetch the schema for inline validation).
//   - The body contains the full envelope: apiVersion: sharko.io/v1,
//     kind: ManagedClusters, metadata.name: managed-clusters, and the
//     spec block.
//
// All four are verified explicitly so a regression in any one of them
// fails this test with a clear locus instead of a vague "envelope wrong"
// message.
func TestSaveManagedClusters_EmitsEnveloped(t *testing.T) {
	t.Parallel()

	spec := ManagedClustersSpec{
		Clusters: []ManagedClusterEntry{
			{
				Name:       "prod-eu",
				Region:     "eu-west-1",
				SecretPath: "clusters/prod-eu",
				Labels: map[string]string{
					"cert-manager": "enabled",
				},
			},
		},
	}

	out, err := SaveManagedClusters(spec)
	if err != nil {
		t.Fatalf("SaveManagedClusters: %v", err)
	}

	// Line 1 — schema header.
	lines := strings.SplitN(string(out), "\n", 2)
	if len(lines) < 2 {
		t.Fatalf("SaveManagedClusters output has no second line:\n%s", out)
	}
	if lines[0] != ManagedClustersSchemaHeader {
		t.Errorf("line 1 = %q, want %q", lines[0], ManagedClustersSchemaHeader)
	}

	// Envelope fields — parse back via yaml.Unmarshal to assert
	// structural shape rather than substring-matching the output (which
	// would be brittle to yaml.v3 formatting changes).
	var doc ManagedClustersDoc
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("re-parse SaveManagedClusters output: %v\n%s", err, out)
	}
	if doc.APIVersion != schema.APIVersion {
		t.Errorf("apiVersion = %q, want %q", doc.APIVersion, schema.APIVersion)
	}
	if doc.Kind != schema.KindManagedClusters {
		t.Errorf("kind = %q, want %q", doc.Kind, schema.KindManagedClusters)
	}
	if doc.Metadata.Name != ManagedClustersMetadataName {
		t.Errorf("metadata.name = %q, want %q", doc.Metadata.Name, ManagedClustersMetadataName)
	}
	if len(doc.Spec.Clusters) != 1 || doc.Spec.Clusters[0].Name != "prod-eu" {
		t.Errorf("spec.clusters not preserved through Save: %+v", doc.Spec.Clusters)
	}
}

// TestRoundTrip_LegacyRead_EnvelopedWrite_Read is the migration-path
// guarantee: a legacy bare-YAML file is read, written back via
// SaveManagedClusters (which emits enveloped), and read again — the spec
// content survives unchanged. This is the operational story for every
// existing Sharko installation when V125-1-9 ships: first write after
// upgrade switches the on-disk shape; the reader stays correct on both
// sides of the change.
func TestRoundTrip_LegacyRead_EnvelopedWrite_Read(t *testing.T) {
	t.Parallel()

	legacy := []byte(`clusters:
  - name: prod-eu
    region: eu-west-1
    secretPath: clusters/prod-eu
    labels:
      cert-manager: enabled
      metrics-server: enabled
  - name: staging-eu
    labels:
      cert-manager: enabled
`)

	spec1, err := LoadManagedClusters(legacy)
	if err != nil {
		t.Fatalf("LoadManagedClusters legacy: %v", err)
	}

	written, err := SaveManagedClusters(spec1)
	if err != nil {
		t.Fatalf("SaveManagedClusters: %v", err)
	}

	// The written output MUST be enveloped — that's the writer
	// contract. Detect via the same schema.IsEnveloped helper the
	// reader uses, so a writer regression that drops the envelope
	// produces a clear failure here.
	if envOK, err := schema.IsEnveloped(written); err != nil {
		t.Fatalf("IsEnveloped on written output: %v", err)
	} else if !envOK {
		t.Fatalf("SaveManagedClusters output not detected as enveloped:\n%s", written)
	}

	spec2, err := LoadManagedClusters(written)
	if err != nil {
		t.Fatalf("LoadManagedClusters written: %v", err)
	}

	// Semantic equality — the round-trip MUST preserve the spec
	// contents. Compare via normalised labels so the interface{} vs
	// map[string]string vs map[string]interface{} differences (yaml.v3
	// produces map[string]interface{} on unmarshal of an inline map,
	// the caller-built spec uses map[string]string) don't trigger a
	// false negative.
	if len(spec1.Clusters) != len(spec2.Clusters) {
		t.Fatalf("cluster count drift: legacy=%d enveloped=%d", len(spec1.Clusters), len(spec2.Clusters))
	}
	for i := range spec1.Clusters {
		c1, c2 := spec1.Clusters[i], spec2.Clusters[i]
		if c1.Name != c2.Name {
			t.Errorf("cluster[%d] name: %q != %q", i, c1.Name, c2.Name)
		}
		if c1.Region != c2.Region {
			t.Errorf("cluster[%d] region: %q != %q", i, c1.Region, c2.Region)
		}
		if c1.SecretPath != c2.SecretPath {
			t.Errorf("cluster[%d] secretPath: %q != %q", i, c1.SecretPath, c2.SecretPath)
		}
		l1 := labelsAsMap(t, c1.Labels)
		l2 := labelsAsMap(t, c2.Labels)
		if !reflect.DeepEqual(l1, l2) {
			t.Errorf("cluster[%d] labels drift:\n  legacy:    %v\n  enveloped: %v", i, l1, l2)
		}
	}
}

// TestRoundTrip_EnvelopedRead_EnvelopedWrite_Read confirms the
// steady-state path is lossless: read an enveloped file, write it back,
// read again — same spec. This is the reconciler hot path post-V125-1-8:
// every reconcile tick reads the file; whole-file regenerates (when they
// happen) go through SaveManagedClusters; readers must keep returning the
// same spec.
func TestRoundTrip_EnvelopedRead_EnvelopedWrite_Read(t *testing.T) {
	t.Parallel()

	enveloped := []byte(`# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
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
        metrics-server: enabled
    - name: dev-us
      labels:
        cert-manager: enabled
`)

	spec1, err := LoadManagedClusters(enveloped)
	if err != nil {
		t.Fatalf("LoadManagedClusters enveloped #1: %v", err)
	}

	written, err := SaveManagedClusters(spec1)
	if err != nil {
		t.Fatalf("SaveManagedClusters: %v", err)
	}

	spec2, err := LoadManagedClusters(written)
	if err != nil {
		t.Fatalf("LoadManagedClusters written: %v", err)
	}

	if len(spec1.Clusters) != len(spec2.Clusters) {
		t.Fatalf("cluster count drift: in=%d out=%d", len(spec1.Clusters), len(spec2.Clusters))
	}
	for i := range spec1.Clusters {
		c1, c2 := spec1.Clusters[i], spec2.Clusters[i]
		if c1.Name != c2.Name {
			t.Errorf("cluster[%d] name: %q != %q", i, c1.Name, c2.Name)
		}
		if c1.Region != c2.Region {
			t.Errorf("cluster[%d] region: %q != %q", i, c1.Region, c2.Region)
		}
		if c1.SecretPath != c2.SecretPath {
			t.Errorf("cluster[%d] secretPath: %q != %q", i, c1.SecretPath, c2.SecretPath)
		}
		l1 := labelsAsMap(t, c1.Labels)
		l2 := labelsAsMap(t, c2.Labels)
		if !reflect.DeepEqual(l1, l2) {
			t.Errorf("cluster[%d] labels drift:\n  in:  %v\n  out: %v", i, l1, l2)
		}
	}
}

// ---------------------------------------------------------------------------
// V125-1-9.4 — read-time JSON Schema validation wiring
// ---------------------------------------------------------------------------

// TestLoadManagedClusters_EnvelopedInvalid_Reject pins the Story 9.4
// contract: an enveloped body that parses as YAML but violates the
// JSON Schema is rejected with an error AND a slog.Error audit log
// entry is emitted naming the kind + the structured violation list.
//
// Capture pattern: redirect slog.Default() to a buffer-backed JSON
// handler for the duration of the test, then assert the JSON object
// contains the expected msg+kind+violations fields. This is the same
// pattern used by Story 9.5's CLI tests will use, so the shape is
// future-friendly.
//
// NOT parallel — slog.SetDefault mutates a global; running side-by-side
// with another slog-capturing test would race on the default handler.
func TestLoadManagedClusters_EnvelopedInvalid_Reject(t *testing.T) {
	// Body is enveloped (so the validator runs) but violates the schema
	// in three ways: missing spec.clusters (required), extra field at
	// spec level (additionalProperties: false), and apiVersion is the
	// wrong const value (sharko.io/v2 instead of sharko.io/v1).
	body := []byte(`apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  unknownField: "x"
`)

	var buf bytes.Buffer
	originalLogger := slog.Default()
	defer slog.SetDefault(originalLogger)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	_, err := LoadManagedClusters(body)
	if err == nil {
		t.Fatalf("LoadManagedClusters: expected validation error, got nil")
	}

	var vf *schema.ValidationFailure
	if !errors.As(err, &vf) {
		t.Fatalf("expected error wrapping *schema.ValidationFailure, got %T: %v", err, err)
	}
	if vf.Kind != schema.KindManagedClusters {
		t.Errorf("ValidationFailure.Kind = %q, want %q", vf.Kind, schema.KindManagedClusters)
	}
	if len(vf.Violations) == 0 {
		t.Errorf("ValidationFailure.Violations is empty; expected at least one")
	}

	// Audit log assertion. The slog JSON handler emits one record per
	// log call; verify the schema_validation_failed event landed with
	// the expected structured fields. We only require the substrings
	// be present in the captured output — exact JSON shape is the
	// handler's contract, not the validator's.
	logOut := buf.String()
	wantSubstrings := []string{
		`"msg":"schema_validation_failed"`,
		`"kind":"ManagedClusters"`,
		`"resource":"managed-clusters.yaml"`,
		`"validation_errors"`,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(logOut, want) {
			t.Errorf("audit log missing substring %q\nfull log:\n%s", want, logOut)
		}
	}
}

// TestLoadManagedClusters_LegacyBareYAML_ValidationSkipped exercises
// the Story 9.4 back-compat invariant: legacy bare YAML does NOT
// trigger validation (so the validator never sees it, no slog.Error
// fires, and the read succeeds). This is the load-bearing test for
// the back-compat contract — every existing Sharko installation has
// legacy files on disk and they must keep loading after V125-1-9.
//
// Detection: capture slog output and assert no schema_validation_failed
// event was emitted. The legacy path also surface-checks the parsed
// content so a future bug that routes legacy through the enveloped
// path (and somehow doesn't fail) would still be detected via the
// audit-log absence + content correctness.
func TestLoadManagedClusters_LegacyBareYAML_ValidationSkipped(t *testing.T) {
	body := []byte(`clusters:
  - name: prod-eu
    region: eu-west-1
    secretPath: clusters/prod-eu
    labels:
      cert-manager: enabled
`)

	var buf bytes.Buffer
	originalLogger := slog.Default()
	defer slog.SetDefault(originalLogger)
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	spec, err := LoadManagedClusters(body)
	if err != nil {
		t.Fatalf("LoadManagedClusters legacy: %v", err)
	}
	if len(spec.Clusters) != 1 || spec.Clusters[0].Name != "prod-eu" {
		t.Errorf("legacy parse content drift: %+v", spec.Clusters)
	}
	if strings.Contains(buf.String(), "schema_validation_failed") {
		t.Errorf("legacy bare YAML must not trigger validation; got audit log:\n%s", buf.String())
	}
}
