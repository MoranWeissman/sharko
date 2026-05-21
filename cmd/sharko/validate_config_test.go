// V125-1-9 Story 9.5 — `sharko validate-config` CLI tests.
//
// These tests drive runValidateConfig directly rather than going through
// cobra.Command.Execute. Reasoning:
//
//   - The cobra command's RunE wrapper calls os.Exit(1) on a validation
//     failure to suppress the "Error: validation failed" trailer that
//     Execute() would otherwise add (see validate_config.go for the why).
//     os.Exit inside a t.Run terminates the test process, so the tests
//     must NOT go through the RunE path.
//
//   - runValidateConfig is the testable seam — it accepts an
//     io.Writer-shaped target so we can capture output to a bytes.Buffer
//     and assert against the exact text the operator (and the CI job)
//     would see.
//
//   - The cobra registration itself (flag binding, AddCommand, help
//     text) is covered implicitly by `sharko validate-config --help`
//     running successfully in the quality-gate smoke tests; doing it
//     in a unit test would add a second source of truth for the help
//     copy.
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validEnvelopeManagedClusters mirrors the canonical happy-path body
// from internal/schema/validator_test.go::validManagedClustersBody. We
// re-declare it here (rather than importing the test fixture) because
// validator_test.go's body is package-private to internal/schema and
// the unit-test fixture is small enough that duplicating it is cheaper
// than promoting a test helper. The bytes are deliberately the SAME
// shape so the two test layers stay in lockstep — a regression in
// either body shape catches in both places.
const validEnvelopeManagedClusters = `# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
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
`

// invalidEnvelopeManagedClusters declares spec.clusters as an object
// instead of the schema-required array. The validator should reject
// with a /spec/clusters: ... violation that includes the schema URL
// pointer line.
const invalidEnvelopeManagedClusters = `# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    name: not-an-array
`

// nonShakroYAML is the kind of YAML the CI hook would encounter on a
// PR that touches a non-Sharko file — a Kubernetes Pod manifest. The
// CLI must skip it (not fail it) so the CI job stays permissive.
const nonShakroYAML = `apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
    - name: app
      image: busybox
`

// writeTempFile creates a temp file with the given content + suffix
// (so the .yaml extension is real and the directory walker picks it
// up). Returns the absolute path. t.TempDir is auto-cleaned on test
// exit so we don't need to register an explicit cleanup.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestValidateConfig_SingleValidFile_Exit0 — a single, well-formed
// enveloped file should return nil from runValidateConfig (= exit 0 at
// the CLI boundary) and emit the ✓ pass line.
func TestValidateConfig_SingleValidFile_Exit0(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "managed-clusters.yaml", validEnvelopeManagedClusters)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if err != nil {
		t.Fatalf("runValidateConfig: unexpected error: %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "✓ "+path) {
		t.Errorf("expected ✓ pass line for %q in output, got:\n%s", path, out)
	}
}

// TestValidateConfig_SingleInvalidFile_Exit1 — a single enveloped file
// with a schema violation should return errValidationFailed and emit
// the ✘ summary, a ✘-prefixed violation, and the schema URL pointer.
func TestValidateConfig_SingleInvalidFile_Exit1(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "managed-clusters.yaml", invalidEnvelopeManagedClusters)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	for _, want := range []string{
		"✘ " + path + ":",                                  // file failure header
		"schema violations (kind: ManagedClusters)",        // kind callout
		"   ✘ ",                                            // at least one violation, indented + prefixed
		"→ for details: https://sharko.io/schemas/managed-clusters.v1.json", // remediation pointer
		"1 file(s) failed validation",                      // summary footer
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestValidateConfig_NonShakroYAML_Skip_Exit0 — a non-enveloped file
// (e.g. a K8s Pod manifest) should be skipped, not failed. This is the
// critical CI-hook property: PRs that touch unrelated YAML must not
// turn red just because they exist.
func TestValidateConfig_NonShakroYAML_Skip_Exit0(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "pod.yaml", nonShakroYAML)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if err != nil {
		t.Fatalf("runValidateConfig: unexpected error: %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "skip: "+path) {
		t.Errorf("expected skip line for %q in output, got:\n%s", path, out)
	}
	if !strings.Contains(out, "not a Sharko-enveloped file") {
		t.Errorf("expected skip reason 'not a Sharko-enveloped file' in output, got:\n%s", out)
	}
}

// TestValidateConfig_Directory_MixedFiles_AggregatesExit — directory
// containing one valid + one invalid + one non-Sharko file: exit 1,
// each file gets its own line in the appropriate verdict, and the
// summary says "1 file(s) failed validation" (not 2 — the skipped
// file doesn't count toward the failure tally).
func TestValidateConfig_Directory_MixedFiles_AggregatesExit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validPath := writeTempFile(t, dir, "a-valid.yaml", validEnvelopeManagedClusters)
	invalidPath := writeTempFile(t, dir, "b-invalid.yaml", invalidEnvelopeManagedClusters)
	skipPath := writeTempFile(t, dir, "c-pod.yaml", nonShakroYAML)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, dir, false)
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	for _, want := range []string{
		"✓ " + validPath,
		"✘ " + invalidPath + ":",
		"skip: " + skipPath,
		"1 file(s) failed validation",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Negative assertion: the summary must NOT count the skipped file
	// or the valid file as failures.
	if strings.Contains(out, "2 file(s) failed validation") || strings.Contains(out, "3 file(s) failed validation") {
		t.Errorf("summary incorrectly counted non-fail files toward failure total:\n%s", out)
	}
}

// TestValidateConfig_QuietFlag_SuppressesPassLines — --quiet should
// suppress ✓ lines for valid files but still show ✘ failures (none
// in this test) and skip lines.
func TestValidateConfig_QuietFlag_SuppressesPassLines(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	validPath := writeTempFile(t, dir, "valid.yaml", validEnvelopeManagedClusters)
	skipPath := writeTempFile(t, dir, "pod.yaml", nonShakroYAML)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, dir, true)
	if err != nil {
		t.Fatalf("runValidateConfig: unexpected error: %v\noutput: %s", err, buf.String())
	}

	out := buf.String()
	if strings.Contains(out, "✓ "+validPath) {
		t.Errorf("--quiet should suppress ✓ pass lines, but got:\n%s", out)
	}
	// Skip lines are signal, not noise — they should still appear so
	// the operator (and CI log scraper) knows the tool actually saw
	// the file and made a routing decision.
	if !strings.Contains(out, "skip: "+skipPath) {
		t.Errorf("--quiet should still show skip lines, but missing %q in:\n%s", skipPath, out)
	}
}

// TestValidateConfig_DesignDocExample_Validates is the load-bearing
// cross-layer assertion: the canonical example from the design doc
// (also pinned by internal/schema/validator_test.go) must validate
// through the CLI. If the schema generator output drifts from the
// design doc shape, this test (and the validator test) both fail.
func TestValidateConfig_DesignDocExample_Validates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "design-doc-example.yaml", validEnvelopeManagedClusters)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if err != nil {
		t.Fatalf("design-doc canonical example failed to validate: %v\noutput: %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "✓ "+path) {
		t.Errorf("design-doc canonical example missing ✓ pass line:\n%s", buf.String())
	}
}

// TestValidateConfig_EmptyDirectory_Exit0 — empty directory returns
// nil + "no YAML files found under ..." message. Mirrors the CI hook's
// "no YAML changes in this PR — skipping validation" branch so the
// shape is consistent.
func TestValidateConfig_EmptyDirectory_Exit0(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	var buf bytes.Buffer
	err := runValidateConfig(&buf, dir, false)
	if err != nil {
		t.Fatalf("runValidateConfig: unexpected error: %v\noutput: %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "no YAML files found under") {
		t.Errorf("expected 'no YAML files found' message, got:\n%s", buf.String())
	}
}

// TestValidateConfig_MissingPath_Errors — pointing at a non-existent
// file produces a wrapped stat error (NOT errValidationFailed —
// missing path is an internal/usage error, not a validation failure).
// The distinction matters because the CI hook treats validation
// failures and internal errors with the same exit code but should
// surface the actual cause in the message.
func TestValidateConfig_MissingPath_Errors(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := runValidateConfig(&buf, "/nonexistent/path/that/does/not/exist.yaml", false)
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
	if errors.Is(err, errValidationFailed) {
		t.Errorf("missing-path should NOT surface as errValidationFailed, got: %v", err)
	}
	if !strings.Contains(err.Error(), "cannot stat") {
		t.Errorf("expected wrapped stat error in message, got: %v", err)
	}
}

// TestValidateConfig_HiddenDirsSkipped — the walker should not descend
// into hidden directories (.git, .github). This is what keeps `sharko
// validate-config .` fast in the repo root.
func TestValidateConfig_HiddenDirsSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Visible file: should be picked up + validated.
	visible := writeTempFile(t, dir, "visible.yaml", validEnvelopeManagedClusters)
	// Hidden subdir with a YAML file: should NOT be picked up.
	hiddenDir := filepath.Join(dir, ".hidden")
	if err := os.Mkdir(hiddenDir, 0o755); err != nil {
		t.Fatalf("mkdir hidden: %v", err)
	}
	hiddenFile := writeTempFile(t, hiddenDir, "should-not-be-walked.yaml", invalidEnvelopeManagedClusters)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, dir, false)
	if err != nil {
		t.Fatalf("runValidateConfig: unexpected error: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "✓ "+visible) {
		t.Errorf("expected ✓ for visible file, got:\n%s", out)
	}
	if strings.Contains(out, hiddenFile) {
		t.Errorf("walker descended into .hidden directory; output mentions hidden file:\n%s", out)
	}
}

// TestValidateConfig_BootstrapTemplates_Validate is the load-bearing
// integration test that the real shipped bootstrap YAML in
// templates/bootstrap/configuration/ validates cleanly through the
// CLI. This is the same property the spec's smoke checks (§7 of the
// dispatch prompt) enforce manually; pinning it as a unit test means
// any future change to the bootstrap templates that breaks the
// envelope contract fails CI without needing a manual smoke step.
//
// The test is skipped when the bootstrap templates are unreadable
// from the test working directory (e.g. when running in an oddly-
// configured CI sandbox); the per-template smoke in the dispatch
// prompt is the backstop for those cases.
func TestValidateConfig_BootstrapTemplates_Validate(t *testing.T) {
	t.Parallel()
	// cmd/sharko tests run with the package directory as CWD; the
	// bootstrap templates live two levels up at the repo root. Build
	// the path relative to that fixed offset so the test is hermetic
	// (no env var, no walk-up search) and the failure mode if the
	// repo layout changes is obvious.
	repoRoot := filepath.Join("..", "..")
	bootstrapDir := filepath.Join(repoRoot, "templates", "bootstrap", "configuration")
	if _, err := os.Stat(bootstrapDir); err != nil {
		t.Skipf("bootstrap dir not reachable from test CWD (%v) — relying on dispatch smoke", err)
	}

	var buf bytes.Buffer
	if err := runValidateConfig(&buf, bootstrapDir, true); err != nil {
		t.Fatalf("bootstrap templates failed validation: %v\noutput: %s", err, buf.String())
	}
}
