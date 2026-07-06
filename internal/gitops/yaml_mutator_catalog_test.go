package gitops

// ZG1-A.264 / closes #264: envelope round-trip tests for the catalog-side
// mutators (AddCatalogEntry, RemoveCatalogEntry, UpdateCatalogEntry,
// UpdateCatalogVersion). Mirrors the cluster-side
// yaml_mutator_envelope_test.go shape introduced in V125-1-8.3.
//
// Pins the V125-1-9.2 contract:
//
//   - Output ALWAYS carries the schema header on line 1 (so editors
//     using yaml-language-server fetch the schema for inline validation).
//   - Output ALWAYS carries the apiVersion/kind/metadata/spec envelope
//     (canonical MarshalAddonCatalog emission).
//   - Legacy bare-YAML inputs are accepted on read and silently upgraded
//     to the envelope on the next emit (V125-1-9 → V126 transition).
//   - Mutations round-trip cleanly: load → mutate → re-load returns the
//     expected struct state with no extra entries / missing entries /
//     mangled fields.
//   - AddCatalogEntry returns an error on duplicate name (no silent-skip
//     retry path on the catalog side — unlike the cluster-side mutator
//     where adoption depends on it).
//   - Remove / Update return an error when the addon is not found.
//   - UpdateCatalogEntry rejects updates to the "name" key and to
//     unknown field keys.

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// envelopedCatalogFixture is a V125-1-9.2-shaped addons-catalog.yaml body
// used by the round-trip tests. Schema header + envelope + two entries
// with different field shapes (with and without optional fields) exercise
// the reader/writer paths the mutators stitch together.
const envelopedCatalogFixture = `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: datadog
      repoURL: https://helm.datadoghq.com
      chart: datadog
      version: 3.160.1
    - name: keda
      repoURL: https://kedacore.github.io/charts
      chart: keda
      version: 2.14.2
      namespace: keda-system
`

// legacyBareCatalogFixture is a pre-V125-1-9.2 addons-catalog body (no
// envelope, top-level `applicationsets:` key). The reader accepts both
// during the back-compat window; the writer always emits the envelope.
const legacyBareCatalogFixture = `applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: 3.160.1
  - name: keda
    repoURL: https://kedacore.github.io/charts
    chart: keda
    version: 2.14.2
`

// catalogSchemaHeader is the canonical line-1 emission. Tests assert
// exact bytes (modulo trailing newline) — anything else would break
// editor inline validation.
const catalogSchemaHeader = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json"

// assertEnvelopedCatalogOutput is the shared post-condition every
// catalog mutator must satisfy: line 1 is the schema header, the
// document re-parses successfully into a slice of AddonCatalogEntry,
// and the envelope shape (apiVersion/kind/metadata) survives a
// marshal/unmarshal cycle.
func assertEnvelopedCatalogOutput(t *testing.T, out []byte, msg string) []models.AddonCatalogEntry {
	t.Helper()
	if !strings.HasPrefix(string(out), catalogSchemaHeader+"\n") {
		t.Errorf("%s: schema header missing or not on line 1\n--- got ---\n%s\n--- end ---", msg, out)
	}
	if !strings.Contains(string(out), "apiVersion: sharko.dev/v1") {
		t.Errorf("%s: envelope apiVersion missing", msg)
	}
	if !strings.Contains(string(out), "kind: AddonCatalog") {
		t.Errorf("%s: envelope kind missing", msg)
	}
	entries, err := config.NewParser().ParseAddonsCatalog(out)
	if err != nil {
		t.Fatalf("%s: output failed to re-parse via ParseAddonsCatalog: %v\n--- bytes ---\n%s", msg, err, out)
	}
	return entries
}

// findEntry returns the entry with name n, or nil. Convenience for the
// round-trip assertions below.
func findEntry(entries []models.AddonCatalogEntry, n string) *models.AddonCatalogEntry {
	for i := range entries {
		if entries[i].Name == n {
			return &entries[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// AddCatalogEntry
// ---------------------------------------------------------------------------

func TestAddCatalogEntry_EnvelopedYAML_PreservesEnvelope(t *testing.T) {
	out, err := AddCatalogEntry([]byte(envelopedCatalogFixture), CatalogEntryInput{
		Name:    "prometheus",
		RepoURL: "https://prometheus-community.github.io/helm-charts",
		Chart:   "prometheus",
		Version: "25.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	entries := assertEnvelopedCatalogOutput(t, out, "AddCatalogEntry")
	if got := len(entries); got != 3 {
		t.Fatalf("expected 3 entries after add, got %d", got)
	}
	added := findEntry(entries, "prometheus")
	if added == nil {
		t.Fatal("prometheus missing from output")
	}
	if added.RepoURL != "https://prometheus-community.github.io/helm-charts" {
		t.Errorf("repoURL: got %q", added.RepoURL)
	}
	if added.Chart != "prometheus" {
		t.Errorf("chart: got %q", added.Chart)
	}
	if added.Version != "25.0.0" {
		t.Errorf("version: got %q", added.Version)
	}
	if added.Namespace != "" {
		t.Errorf("namespace: expected empty, got %q", added.Namespace)
	}

	// Pre-existing entries survive untouched.
	if findEntry(entries, "datadog") == nil {
		t.Error("datadog was dropped by AddCatalogEntry")
	}
	if findEntry(entries, "keda") == nil {
		t.Error("keda was dropped by AddCatalogEntry")
	}
}

func TestAddCatalogEntry_LegacyBareYAML_UpgradesToEnvelope(t *testing.T) {
	// Pre-V125-1-9.2 hand-authored file is read successfully; the new
	// mutator emits the canonical envelope on save.
	out, err := AddCatalogEntry([]byte(legacyBareCatalogFixture), CatalogEntryInput{
		Name:    "prometheus",
		RepoURL: "https://prometheus-community.github.io/helm-charts",
		Chart:   "prometheus",
		Version: "25.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "AddCatalogEntry (legacy bare input)")
	if findEntry(entries, "prometheus") == nil {
		t.Error("prometheus missing from output")
	}
}

func TestAddCatalogEntry_EmptyInput_BootstrapsEnvelope(t *testing.T) {
	out, err := AddCatalogEntry([]byte(""), CatalogEntryInput{
		Name:    "first-addon",
		RepoURL: "https://example.com/charts",
		Chart:   "first-addon",
		Version: "1.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "AddCatalogEntry (empty input)")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Name != "first-addon" {
		t.Errorf("expected first-addon, got %q", entries[0].Name)
	}
}

func TestAddCatalogEntry_WithNamespace(t *testing.T) {
	out, err := AddCatalogEntry([]byte(envelopedCatalogFixture), CatalogEntryInput{
		Name:      "metrics-server",
		RepoURL:   "https://kubernetes-sigs.github.io/metrics-server",
		Chart:     "metrics-server",
		Version:   "3.12.0",
		Namespace: "kube-system",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "AddCatalogEntry (with namespace)")
	added := findEntry(entries, "metrics-server")
	if added == nil {
		t.Fatal("metrics-server missing")
	}
	if added.Namespace != "kube-system" {
		t.Errorf("namespace: got %q", added.Namespace)
	}
}

func TestAddCatalogEntry_DuplicateName_ReturnsError(t *testing.T) {
	// Unlike the cluster-side mutator (which silent-skips for retry
	// semantics), the catalog mutator returns an error on duplicate —
	// internal/orchestrator/addon.go surfaces this as a user-visible
	// "addon already exists" error.
	_, err := AddCatalogEntry([]byte(envelopedCatalogFixture), CatalogEntryInput{
		Name:    "datadog",
		RepoURL: "https://helm.datadoghq.com",
		Chart:   "datadog",
		Version: "3.200.0",
	})
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !strings.Contains(err.Error(), "datadog") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RemoveCatalogEntry
// ---------------------------------------------------------------------------

func TestRemoveCatalogEntry_First(t *testing.T) {
	out, err := RemoveCatalogEntry([]byte(envelopedCatalogFixture), "datadog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "RemoveCatalogEntry (first)")
	if findEntry(entries, "datadog") != nil {
		t.Error("datadog still present")
	}
	if findEntry(entries, "keda") == nil {
		t.Error("keda removed unexpectedly")
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestRemoveCatalogEntry_Last(t *testing.T) {
	out, err := RemoveCatalogEntry([]byte(envelopedCatalogFixture), "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "RemoveCatalogEntry (last)")
	if findEntry(entries, "keda") != nil {
		t.Error("keda still present")
	}
	if findEntry(entries, "datadog") == nil {
		t.Error("datadog removed unexpectedly")
	}
}

func TestRemoveCatalogEntry_Middle(t *testing.T) {
	threeEntry := `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: datadog
      repoURL: https://helm.datadoghq.com
      chart: datadog
      version: 3.160.1
    - name: keda
      repoURL: https://kedacore.github.io/charts
      chart: keda
      version: 2.14.2
    - name: prometheus
      repoURL: https://prometheus-community.github.io/helm-charts
      chart: prometheus
      version: 25.0.0
`
	out, err := RemoveCatalogEntry([]byte(threeEntry), "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "RemoveCatalogEntry (middle)")
	if findEntry(entries, "keda") != nil {
		t.Error("keda still present")
	}
	if findEntry(entries, "datadog") == nil {
		t.Error("datadog missing")
	}
	if findEntry(entries, "prometheus") == nil {
		t.Error("prometheus missing")
	}
}

func TestRemoveCatalogEntry_NotFound(t *testing.T) {
	_, err := RemoveCatalogEntry([]byte(envelopedCatalogFixture), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

func TestRemoveCatalogEntry_OnlyEntry(t *testing.T) {
	input := `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: keda
      repoURL: https://kedacore.github.io/charts
      chart: keda
      version: 2.14.2
`
	out, err := RemoveCatalogEntry([]byte(input), "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "RemoveCatalogEntry (only entry)")
	if len(entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(entries))
	}
	// Envelope shape (applicationsets: []) must survive even when empty.
	if !strings.Contains(string(out), "applicationsets:") {
		t.Errorf("applicationsets key dropped from envelope:\n%s", out)
	}
}

func TestRemoveCatalogEntry_LegacyBareYAML_UpgradesToEnvelope(t *testing.T) {
	out, err := RemoveCatalogEntry([]byte(legacyBareCatalogFixture), "datadog")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "RemoveCatalogEntry (legacy bare input)")
	if findEntry(entries, "datadog") != nil {
		t.Error("datadog still present")
	}
	if findEntry(entries, "keda") == nil {
		t.Error("keda removed unexpectedly")
	}
}

// ---------------------------------------------------------------------------
// UpdateCatalogEntry
// ---------------------------------------------------------------------------

func TestUpdateCatalogEntry_Version(t *testing.T) {
	out, err := UpdateCatalogEntry([]byte(envelopedCatalogFixture), "datadog", map[string]string{"version": "3.200.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "UpdateCatalogEntry (version)")
	updated := findEntry(entries, "datadog")
	if updated == nil {
		t.Fatal("datadog missing")
	}
	if updated.Version != "3.200.0" {
		t.Errorf("version: got %q", updated.Version)
	}
	// keda untouched.
	other := findEntry(entries, "keda")
	if other == nil || other.Version != "2.14.2" {
		t.Errorf("keda version modified: %v", other)
	}
}

func TestUpdateCatalogEntry_MultipleFields(t *testing.T) {
	out, err := UpdateCatalogEntry([]byte(envelopedCatalogFixture), "keda", map[string]string{
		"version": "2.15.0",
		"chart":   "keda-patched",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "UpdateCatalogEntry (multi)")
	updated := findEntry(entries, "keda")
	if updated == nil {
		t.Fatal("keda missing")
	}
	if updated.Version != "2.15.0" {
		t.Errorf("version: got %q", updated.Version)
	}
	if updated.Chart != "keda-patched" {
		t.Errorf("chart: got %q", updated.Chart)
	}
}

func TestUpdateCatalogEntry_AddNewField(t *testing.T) {
	// datadog in the fixture has no namespace set; this update adds it.
	out, err := UpdateCatalogEntry([]byte(envelopedCatalogFixture), "datadog", map[string]string{
		"namespace": "monitoring",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "UpdateCatalogEntry (add new field)")
	updated := findEntry(entries, "datadog")
	if updated == nil {
		t.Fatal("datadog missing")
	}
	if updated.Namespace != "monitoring" {
		t.Errorf("namespace: got %q", updated.Namespace)
	}
}

func TestUpdateCatalogEntry_NameRejected(t *testing.T) {
	_, err := UpdateCatalogEntry([]byte(envelopedCatalogFixture), "datadog", map[string]string{"name": "renamed"})
	if err == nil {
		t.Fatal("expected error when attempting to update name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should mention name: %v", err)
	}
}

func TestUpdateCatalogEntry_NotFound(t *testing.T) {
	_, err := UpdateCatalogEntry([]byte(envelopedCatalogFixture), "nonexistent", map[string]string{"version": "1.0.0"})
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

func TestUpdateCatalogEntry_SelfHealParsed(t *testing.T) {
	out, err := UpdateCatalogEntry([]byte(envelopedCatalogFixture), "datadog", map[string]string{"selfHeal": "true"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "UpdateCatalogEntry (selfHeal)")
	updated := findEntry(entries, "datadog")
	if updated == nil {
		t.Fatal("datadog missing")
	}
	if updated.SelfHeal == nil || *updated.SelfHeal != true {
		t.Errorf("selfHeal: got %v", updated.SelfHeal)
	}
}

func TestUpdateCatalogEntry_UnknownField_Rejected(t *testing.T) {
	// The typed AddonCatalogEntry exposes a fixed field set; silently
	// dropping unknown keys would mask caller bugs. Reject explicitly.
	_, err := UpdateCatalogEntry([]byte(envelopedCatalogFixture), "datadog", map[string]string{"nonexistent": "value"})
	if err == nil {
		t.Fatal("expected error for unknown field key")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention field key: %v", err)
	}
}

// ---------------------------------------------------------------------------
// UpdateCatalogVersion (thin wrapper around UpdateCatalogEntry)
// ---------------------------------------------------------------------------

func TestUpdateCatalogVersion_Existing(t *testing.T) {
	out, err := UpdateCatalogVersion([]byte(envelopedCatalogFixture), "datadog", "3.170.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "UpdateCatalogVersion (existing)")
	updated := findEntry(entries, "datadog")
	if updated == nil {
		t.Fatal("datadog missing")
	}
	if updated.Version != "3.170.0" {
		t.Errorf("version: got %q", updated.Version)
	}
	// keda untouched.
	other := findEntry(entries, "keda")
	if other == nil || other.Version != "2.14.2" {
		t.Errorf("keda version modified: %v", other)
	}
}

func TestUpdateCatalogVersion_NotFound(t *testing.T) {
	_, err := UpdateCatalogVersion([]byte(envelopedCatalogFixture), "nonexistent", "1.0.0")
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention addon name: %v", err)
	}
}

func TestUpdateCatalogVersion_LegacyBareYAML_UpgradesToEnvelope(t *testing.T) {
	out, err := UpdateCatalogVersion([]byte(legacyBareCatalogFixture), "keda", "2.15.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	entries := assertEnvelopedCatalogOutput(t, out, "UpdateCatalogVersion (legacy bare input)")
	updated := findEntry(entries, "keda")
	if updated == nil {
		t.Fatal("keda missing")
	}
	if updated.Version != "2.15.0" {
		t.Errorf("version: got %q", updated.Version)
	}
}

// ---------------------------------------------------------------------------
// Cross-cutting: full round-trip (load → mutate × N → re-load) must
// converge on the expected entries without drift.
// ---------------------------------------------------------------------------

func TestCatalogMutators_FullRoundTrip_EntriesStable(t *testing.T) {
	body := []byte(envelopedCatalogFixture)

	// Add an entry.
	body, err := AddCatalogEntry(body, CatalogEntryInput{
		Name:    "cert-manager",
		RepoURL: "https://charts.jetstack.io",
		Chart:   "cert-manager",
		Version: "1.14.0",
	})
	if err != nil {
		t.Fatalf("AddCatalogEntry: %v", err)
	}

	// Update its namespace (was unset).
	body, err = UpdateCatalogEntry(body, "cert-manager", map[string]string{"namespace": "cert-manager"})
	if err != nil {
		t.Fatalf("UpdateCatalogEntry: %v", err)
	}

	// Bump datadog version.
	body, err = UpdateCatalogVersion(body, "datadog", "3.200.0")
	if err != nil {
		t.Fatalf("UpdateCatalogVersion: %v", err)
	}

	// Remove keda.
	body, err = RemoveCatalogEntry(body, "keda")
	if err != nil {
		t.Fatalf("RemoveCatalogEntry: %v", err)
	}

	entries := assertEnvelopedCatalogOutput(t, body, "round-trip final")

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	cm := findEntry(entries, "cert-manager")
	if cm == nil {
		t.Fatal("cert-manager missing")
	}
	if cm.Namespace != "cert-manager" {
		t.Errorf("cert-manager namespace: got %q", cm.Namespace)
	}

	dd := findEntry(entries, "datadog")
	if dd == nil {
		t.Fatal("datadog missing")
	}
	if dd.Version != "3.200.0" {
		t.Errorf("datadog version: got %q", dd.Version)
	}

	if findEntry(entries, "keda") != nil {
		t.Error("keda still present after RemoveCatalogEntry")
	}
}

// TestParseAddonsCatalog_ToleratesRemovedSyncWaveAndDependsOnKeys —
// V2-cleanup-67.1 removed the syncWave and dependsOn fields from
// AddonCatalogEntry (they were dead: Sharko builds one ApplicationSet per
// addon, so a sync-wave annotation on one addon's Application can never
// order it against another addon's Application, and dependsOn only fed a
// log-only warning). No migration was written — existing catalog files in
// git may still carry these keys. This test pins that reading such a file
// stays safe: yaml.v3 (via config.NewParser().ParseAddonsCatalog) ignores
// unknown keys by default, so the stale keys are silently dropped and the
// rest of the entry parses intact.
func TestParseAddonsCatalog_ToleratesRemovedSyncWaveAndDependsOnKeys(t *testing.T) {
	const body = `apiVersion: sharko.dev/v1
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
      syncWave: 5
      dependsOn: [datadog]
`
	entries, err := config.NewParser().ParseAddonsCatalog([]byte(body))
	if err != nil {
		t.Fatalf("ParseAddonsCatalog: unexpected error on a catalog body still carrying "+
			"removed syncWave/dependsOn keys: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	e := entries[0]
	if e.Name != "cert-manager" {
		t.Errorf("Name = %q, want %q", e.Name, "cert-manager")
	}
	if e.RepoURL != "https://charts.jetstack.io" {
		t.Errorf("RepoURL = %q, want %q", e.RepoURL, "https://charts.jetstack.io")
	}
	if e.Chart != "cert-manager" {
		t.Errorf("Chart = %q, want %q", e.Chart, "cert-manager")
	}
	if e.Version != "1.16.3" {
		t.Errorf("Version = %q, want %q", e.Version, "1.16.3")
	}
	if e.Namespace != "cert-manager" {
		t.Errorf("Namespace = %q, want %q", e.Namespace, "cert-manager")
	}
}
