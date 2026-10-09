// v4 wave 1 Story 3.5 — `sharko validate-catalog` CLI tests.
//
// Mirrors validate_config_test.go's approach: drive RunE directly (not
// cobra.Command.Execute) since Execute()'s os.Exit(1) on failure — see
// root.go — would kill the test process. RunE itself never calls os.Exit;
// only the top-level Execute() wrapper in main.go does.
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validCatalogEntryYAML = `
addons:
  - name: cert-manager
    description: Automated TLS certificate lifecycle management.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    maintainers: [jetstack]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated, aws-eks-blueprints]
`

func writeTestCatalogFile(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "addons.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing test fixture: %v", err)
	}
	return path
}

func runValidateCatalog(t *testing.T, args []string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	validateCatalogCmd.SetOut(&outBuf)
	validateCatalogCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		validateCatalogCmd.SetOut(nil)
		validateCatalogCmd.SetErr(nil)
	})
	err = validateCatalogCmd.RunE(validateCatalogCmd, args)
	return outBuf.String(), errBuf.String(), err
}

func TestValidateCatalog_ValidFile(t *testing.T) {
	path := writeTestCatalogFile(t, validCatalogEntryYAML)

	stdout, _, err := runValidateCatalog(t, []string{path})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if want := "OK   " + path; stdout == "" || !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

func TestValidateCatalog_MissingRequiredField(t *testing.T) {
	badYAML := `
addons:
  - name: broken-addon
    chart: broken-addon
    repo: https://charts.example.com
    default_namespace: broken-addon
    maintainers: [example]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`
	path := writeTestCatalogFile(t, badYAML)

	_, stderr, err := runValidateCatalog(t, []string{path})
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v", err)
	}
	if !strings.Contains(stderr, "missing required field: description") {
		t.Errorf("stderr = %q, want it to name the missing field", stderr)
	}
	if !strings.Contains(stderr, "broken-addon") {
		t.Errorf("stderr = %q, want it to name the offending entry", stderr)
	}
}

func TestValidateCatalog_InvalidCategory(t *testing.T) {
	badYAML := `
addons:
  - name: mystery-addon
    description: Does a thing.
    chart: mystery-addon
    repo: https://charts.example.com
    default_namespace: mystery-addon
    maintainers: [example]
    license: Apache-2.0
    category: not-a-real-category
    curated_by: [cncf-graduated]
`
	path := writeTestCatalogFile(t, badYAML)

	_, stderr, err := runValidateCatalog(t, []string{path})
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v", err)
	}
	if !strings.Contains(stderr, "not in the allowed set") {
		t.Errorf("stderr = %q, want the allow-list error message", stderr)
	}
}

func TestValidateCatalog_DuplicateName(t *testing.T) {
	badYAML := validCatalogEntryYAML + `  - name: cert-manager
    description: Duplicate entry.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    maintainers: [jetstack]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`
	path := writeTestCatalogFile(t, badYAML)

	_, stderr, err := runValidateCatalog(t, []string{path})
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v", err)
	}
	if !strings.Contains(stderr, "duplicate entry name") {
		t.Errorf("stderr = %q, want a duplicate-name error", stderr)
	}
}

func TestValidateCatalog_MissingFile(t *testing.T) {
	_, stderr, err := runValidateCatalog(t, []string{"/nonexistent/catalog/addons.yaml"})
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v", err)
	}
	if !strings.Contains(stderr, "FAIL") {
		t.Errorf("stderr = %q, want a FAIL line", stderr)
	}
}

// TestValidateCatalog_RealCatalogFile is the load-bearing regression test:
// the monorepo's own catalog/addons.yaml (the same file CI validates on
// every catalog-touching PR) must pass through this exact command cleanly.
// A failure here means either the catalog file or the loader's rules
// drifted — both are real problems this command exists to catch early.
func TestValidateCatalog_RealCatalogFile(t *testing.T) {
	// This test file lives at cmd/sharko/, so the repo root is two levels
	// up. Mirrors the repoRoot() helper pattern used by
	// tests/enginerender/render_test.go and tests/bootstraprender, just
	// inlined since this is the only test in this package that needs it.
	realPath := filepath.Join("..", "..", "catalog", "addons.yaml")
	if _, statErr := os.Stat(realPath); statErr != nil {
		t.Skipf("catalog/addons.yaml not found at %s (unexpected repo layout): %v", realPath, statErr)
	}

	stdout, stderr, err := runValidateCatalog(t, []string{realPath})
	if err != nil {
		t.Fatalf("expected the real catalog/addons.yaml to validate cleanly, got error: %v\nstderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, "OK") {
		t.Errorf("stdout = %q, want an OK line", stdout)
	}
}
