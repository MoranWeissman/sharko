// v4 Wave 1 Story 2.6 — validate-config CLI coverage for the two new v4
// kinds (ClusterAddons, AddonCatalogDelta), the file-name/line
// reporting they add, and the v4-layout fixture-repo end-to-end path.
// Kept in a separate file from validate_config_test.go so the v3 and v4
// suites can evolve independently — none of the existing tests in that
// file are touched, which is itself part of proving "v3 layouts keep
// validating exactly as today".
package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validClusterAddons mirrors the design doc's §2.1 worked example
// (docs/design/2026-07-30-v4-data-file-format.md). File name must be
// prod-eu.yaml to match spec.cluster.
const validClusterAddons = `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/cluster-addons.v1.json
apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      version: "1.12.0"
      settings:
        ignoreDifferences:
          - group: admissionregistration.k8s.io
            kind: ValidatingWebhookConfiguration
            jsonPointers:
              - /webhooks/0/clientConfig/caBundle
    metrics-server:
      enabled: true
    external-dns:
      enabled: false
`

// validAddonCatalogDelta mirrors the design doc's §2.3 worked example.
const validAddonCatalogDelta = `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addon-catalog-delta.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons:
    cert-manager:
      version: "1.14.5"
    metrics-server:
      version: "3.12.1"
`

// TestValidateConfig_ClusterAddons_Valid_Exit0 — a well-formed
// clusters/<name>.yaml validates cleanly.
func TestValidateConfig_ClusterAddons_Valid_Exit0(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clustersDir := filepath.Join(dir, "clusters")
	if err := os.Mkdir(clustersDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := writeTempFile(t, clustersDir, "prod-eu.yaml", validClusterAddons)

	var buf bytes.Buffer
	if err := runValidateConfig(&buf, path, false); err != nil {
		t.Fatalf("runValidateConfig: unexpected error: %v\noutput: %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "✓ "+path) {
		t.Errorf("expected ✓ pass line for %q, got:\n%s", path, buf.String())
	}
}

// TestValidateConfig_ClusterAddons_MissingEnabled_FailsWithLine —
// a required-field schema violation must name the file, the reason, and
// a source line (Story 2.6's headline AC).
func TestValidateConfig_ClusterAddons_MissingEnabled_FailsWithLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clustersDir := filepath.Join(dir, "clusters")
	if err := os.Mkdir(clustersDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      version: "1.12.0"
`
	path := writeTempFile(t, clustersDir, "prod-eu.yaml", body)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✘ " + path + ":",
		"schema violations (kind: ClusterAddons)",
		"enabled",
		"line 9", // the cert-manager block's only present field ("version")
		"1 file(s) failed validation",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestValidateConfig_ClusterAddons_PreserveResourcesOnDeletion_Rejected
// pins the contract-specific redirect message (design doc §3.2 "Two
// tiers"): the file, the addon name, the line of the forbidden key, and
// where the field belongs.
func TestValidateConfig_ClusterAddons_PreserveResourcesOnDeletion_Rejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clustersDir := filepath.Join(dir, "clusters")
	if err := os.Mkdir(clustersDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      settings:
        preserveResourcesOnDeletion: false
`
	path := writeTempFile(t, clustersDir, "prod-eu.yaml", body)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✘ " + path + ":",
		"preserveResourcesOnDeletion",
		`addon "cert-manager"`,
		"line 11", // the preserveResourcesOnDeletion key itself
		"catalog/addons.yaml",
		"1 file(s) failed validation",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	// Negative: this must NOT be reported as a generic schema-violations
	// failure — the friendly redirect message takes priority.
	if strings.Contains(out, "schema violations") {
		t.Errorf("expected the friendly redirect message, not a generic schema-violations failure:\n%s", out)
	}
}

// TestValidateConfig_ClusterAddons_FilenameMismatch_Fails — the file
// name (minus .yaml) must equal spec.cluster (design doc §2.1).
func TestValidateConfig_ClusterAddons_FilenameMismatch_Fails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clustersDir := filepath.Join(dir, "clusters")
	if err := os.Mkdir(clustersDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: prod-eu
spec:
  cluster: staging-us
  addons:
    cert-manager:
      enabled: true
`
	// File is named prod-eu.yaml but spec.cluster says staging-us.
	path := writeTempFile(t, clustersDir, "prod-eu.yaml", body)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✘ " + path + ":",
		"file name and spec.cluster disagree",
		`"prod-eu"`,
		`"staging-us"`,
		"line 6", // the spec.cluster key
		"1 file(s) failed validation",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestValidateConfig_AddonCatalogDelta_Valid_Exit0 — a well-formed
// catalog/addons.yaml validates cleanly.
func TestValidateConfig_AddonCatalogDelta_Valid_Exit0(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := writeTempFile(t, dir, "addons.yaml", validAddonCatalogDelta)

	var buf bytes.Buffer
	if err := runValidateConfig(&buf, path, false); err != nil {
		t.Fatalf("runValidateConfig: unexpected error: %v\noutput: %s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "✓ "+path) {
		t.Errorf("expected ✓ pass line for %q, got:\n%s", path, buf.String())
	}
}

// TestValidateConfig_AddonCatalogDelta_MissingAddons_FailsWithLine —
// spec.addons is required even though it may be an empty map.
func TestValidateConfig_AddonCatalogDelta_MissingAddons_FailsWithLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := `apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec: {}
`
	path := writeTempFile(t, dir, "addons.yaml", body)

	var buf bytes.Buffer
	err := runValidateConfig(&buf, path, false)
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✘ " + path + ":",
		"schema violations (kind: AddonCatalogDelta)",
		"addons",
		"1 file(s) failed validation",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// buildV4FixtureRepo lays out the design doc's §6 worked end-to-end
// example on disk: two clusters, a catalog delta, connections, and a
// values override — plus engine/application.yaml, which is a real
// ArgoCD Application (not a Sharko envelope) and must be silently
// skipped, same as the plain Helm values files.
func buildV4FixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustMkdirAll(t, filepath.Join(dir, "clusters"))
	mustMkdirAll(t, filepath.Join(dir, "catalog"))
	mustMkdirAll(t, filepath.Join(dir, "fleet"))
	mustMkdirAll(t, filepath.Join(dir, "values", "global"))
	mustMkdirAll(t, filepath.Join(dir, "values", "clusters", "prod-eu"))
	mustMkdirAll(t, filepath.Join(dir, "engine"))

	writeTempFile(t, filepath.Join(dir, "catalog"), "addons.yaml", validAddonCatalogDelta)
	writeTempFile(t, filepath.Join(dir, "clusters"), "prod-eu.yaml", validClusterAddons)
	writeTempFile(t, filepath.Join(dir, "clusters"), "staging-us.yaml", `apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: staging-us
spec:
  cluster: staging-us
  addons:
    cert-manager:
      enabled: true
    metrics-server:
      enabled: true
`)
	writeTempFile(t, filepath.Join(dir, "fleet"), "connections.yaml", `apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: connections
spec:
  clusters:
    - name: prod-eu
      secretPath: k8s-prod-eu
      region: eu-central-1
    - name: staging-us
      secretPath: k8s-staging-us
      region: us-east-1
`)
	writeTempFile(t, filepath.Join(dir, "values", "global"), "cert-manager.yaml", `installCRDs: true
replicaCount: 2
resources:
  requests:
    cpu: 10m
    memory: 32Mi
`)
	writeTempFile(t, filepath.Join(dir, "values", "clusters", "prod-eu"), "cert-manager.yaml", `replicaCount: 3
`)
	writeTempFile(t, filepath.Join(dir, "engine"), "application.yaml", `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-engine
  namespace: argocd
spec:
  project: default
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
`)
	return dir
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// TestValidateConfig_V4FixtureRepo_EndToEnd_Exit0 is the Story 2.6 AC
// "a valid repo passes with exit code 0", exercised against the design
// doc's actual §6 worked example end to end via the CLI: two
// ClusterAddons files, an AddonCatalogDelta, a ManagedClusters
// connections file, plain Helm values (skipped — syntax-check only,
// no schema), and a real ArgoCD Application (skipped, not a Sharko
// envelope).
func TestValidateConfig_V4FixtureRepo_EndToEnd_Exit0(t *testing.T) {
	t.Parallel()
	dir := buildV4FixtureRepo(t)

	var buf bytes.Buffer
	if err := runValidateConfig(&buf, dir, false); err != nil {
		t.Fatalf("v4 fixture repo failed validation: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✓ " + filepath.Join(dir, "clusters", "prod-eu.yaml"),
		"✓ " + filepath.Join(dir, "clusters", "staging-us.yaml"),
		"✓ " + filepath.Join(dir, "catalog", "addons.yaml"),
		"✓ " + filepath.Join(dir, "fleet", "connections.yaml"),
		"skip: " + filepath.Join(dir, "values", "global", "cert-manager.yaml"),
		"skip: " + filepath.Join(dir, "values", "clusters", "prod-eu", "cert-manager.yaml"),
		"skip: " + filepath.Join(dir, "engine", "application.yaml"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestValidateConfig_V4FixtureRepo_BrokenFile_Exit1_NamesFileReasonLine
// is the Story 2.6 AC "given a repo with one broken data file, sharko
// validate in CI fails naming the file, the reason, and the line" —
// exercised against the same fixture repo with exactly one file
// corrupted (staging-us.yaml's spec.cluster renamed so it no longer
// matches the file name). Every OTHER file must still report pass or
// skip: a broken file fails in isolation, it doesn't take the rest of
// the repo down with it (part of "never half-applies" — validate-config
// is read-only and reports per file, so nothing downstream of a clean
// file is blocked by an unrelated broken one).
func TestValidateConfig_V4FixtureRepo_BrokenFile_Exit1_NamesFileReasonLine(t *testing.T) {
	t.Parallel()
	dir := buildV4FixtureRepo(t)

	brokenPath := filepath.Join(dir, "clusters", "staging-us.yaml")
	broken := `apiVersion: sharko.dev/v1
kind: ClusterAddons
metadata:
  name: staging-us
spec:
  cluster: staging-eu
  addons:
    cert-manager:
      enabled: true
`
	if err := os.WriteFile(brokenPath, []byte(broken), 0o644); err != nil {
		t.Fatalf("overwrite %s: %v", brokenPath, err)
	}

	var buf bytes.Buffer
	err := runValidateConfig(&buf, dir, false)
	if !errors.Is(err, errValidationFailed) {
		t.Fatalf("expected errValidationFailed, got %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{
		"✘ " + brokenPath + ":",
		"file name and spec.cluster disagree",
		"line 6",
		"1 file(s) failed validation",
		// every other file in the repo still gets its own clean verdict
		"✓ " + filepath.Join(dir, "clusters", "prod-eu.yaml"),
		"✓ " + filepath.Join(dir, "catalog", "addons.yaml"),
		"✓ " + filepath.Join(dir, "fleet", "connections.yaml"),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestValidateConfig_V3AndV4_Coexist proves the two formats validate
// side by side in the same directory walk: a v3 addons-catalog.yaml
// (kind: AddonCatalog) next to a v4 catalog/addons.yaml (kind:
// AddonCatalogDelta) — same base filename, different kind, different
// schema, both pass. This is the concrete regression test for "v3
// layouts must keep validating exactly as today" alongside the new v4
// kinds, matching design doc decision D5's whole point (same apiVersion,
// different kind, never cross-parsed).
func TestValidateConfig_V3AndV4_Coexist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	v3Path := writeTempFile(t, dir, "v3-addons-catalog.yaml", `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: cert-manager
      repoURL: https://charts.jetstack.io
      chart: cert-manager
      version: v1.16.1
`)
	v4Path := writeTempFile(t, dir, "v4-addons.yaml", validAddonCatalogDelta)

	var buf bytes.Buffer
	if err := runValidateConfig(&buf, dir, false); err != nil {
		t.Fatalf("v3+v4 coexistence: unexpected error: %v\noutput: %s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "✓ "+v3Path) {
		t.Errorf("expected ✓ for v3 file %q, got:\n%s", v3Path, out)
	}
	if !strings.Contains(out, "✓ "+v4Path) {
		t.Errorf("expected ✓ for v4 file %q, got:\n%s", v4Path, out)
	}
}
