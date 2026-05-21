package gitops

// V125-1-8.3 / closes #257: envelope round-trip tests for the
// cluster-side mutators. Pins the new contract introduced by the
// V125-1-9 envelope:
//
//   - Output ALWAYS carries the schema header on line 1 (so editors
//     using yaml-language-server fetch the schema for inline
//     validation regardless of which Sharko code path emitted the
//     file).
//   - Output ALWAYS carries the apiVersion/kind/metadata/spec
//     envelope shape (canonical SaveManagedClusters emission).
//   - Legacy bare-YAML inputs are accepted on read and silently
//     upgraded to the envelope on the next emit (V125-1-9 → V126
//     transition contract).
//   - Mutations round-trip cleanly: load → mutate → re-load returns
//     the expected struct state with no extra entries / missing
//     entries / mangled labels.
//   - AddClusterEntry stays idempotent on duplicate name to preserve
//     the orchestrator's retry-after-partial-failure semantics.
//   - Remove / Set / Update return an error when the cluster is not
//     found (matches the caller contracts in orchestrator/remove.go,
//     orchestrator/unadopt.go, api/clusters_write.go).

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// envelopedFixture is a V125-1-9-shaped managed-clusters.yaml body
// used by the round-trip tests. Schema header + envelope + two cluster
// entries with different label shapes (map and absent) exercise the
// reader/writer paths the mutators stitch together.
const envelopedFixture = `# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      region: eu-west-1
      labels:
        cert-manager: "true"
        keda: "false"
    - name: prod-us
      region: us-east-1
      secretPath: aws-sm/prod-us
      labels:
        cert-manager: "true"
`

// legacyBareFixture is a pre-V125-1-9 managed-clusters.yaml body
// (no envelope, top-level `clusters:` key). The reader accepts both
// during the back-compat window; the writer always emits the envelope.
const legacyBareFixture = `clusters:
  - name: prod-eu
    labels:
      cert-manager: "true"
  - name: prod-us
    labels:
      cert-manager: "true"
`

// schemaHeader is the canonical line-1 emission. Tests assert exact
// bytes (modulo trailing newline) — anything else would break editor
// inline validation.
const schemaHeader = "# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json"

// assertEnvelopedOutput is the shared post-condition every mutator
// must satisfy: line 1 is the schema header, the document re-parses
// successfully into a ManagedClustersSpec, and the envelope shape
// (apiVersion/kind/metadata) survives a marshal/unmarshal cycle.
func assertEnvelopedOutput(t *testing.T, out []byte, msg string) models.ManagedClustersSpec {
	t.Helper()
	if !strings.HasPrefix(string(out), schemaHeader+"\n") {
		t.Errorf("%s: schema header missing or not on line 1\n--- got ---\n%s\n--- end ---", msg, out)
	}
	if !strings.Contains(string(out), "apiVersion: sharko.io/v1") {
		t.Errorf("%s: envelope apiVersion missing", msg)
	}
	if !strings.Contains(string(out), "kind: ManagedClusters") {
		t.Errorf("%s: envelope kind missing", msg)
	}
	spec, err := models.LoadManagedClusters(out)
	if err != nil {
		t.Fatalf("%s: output failed to re-parse via LoadManagedClusters: %v\n--- bytes ---\n%s", msg, err, out)
	}
	return spec
}

// findCluster returns the entry with name n, or nil. Convenience for
// the round-trip assertions below.
func findCluster(spec models.ManagedClustersSpec, n string) *models.ManagedClusterEntry {
	for i := range spec.Clusters {
		if spec.Clusters[i].Name == n {
			return &spec.Clusters[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// AddClusterEntry
// ---------------------------------------------------------------------------

func TestAddClusterEntry_EnvelopedYAML_PreservesEnvelope(t *testing.T) {
	out, err := AddClusterEntry([]byte(envelopedFixture), ClusterEntryInput{
		Name:   "staging-01",
		Region: "eu-west-1",
		Labels: map[string]string{"cert-manager": "true", "keda": "true"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	spec := assertEnvelopedOutput(t, out, "AddClusterEntry")

	if got := len(spec.Clusters); got != 3 {
		t.Fatalf("expected 3 clusters after add, got %d", got)
	}
	added := findCluster(spec, "staging-01")
	if added == nil {
		t.Fatal("staging-01 missing from output")
	}
	if added.Region != "eu-west-1" {
		t.Errorf("expected region=eu-west-1, got %q", added.Region)
	}
	labels := normaliseLabels(added.Labels)
	if labels["cert-manager"] != "true" || labels["keda"] != "true" {
		t.Errorf("expected labels {cert-manager: true, keda: true}, got %v", labels)
	}

	// Pre-existing clusters survive untouched.
	if findCluster(spec, "prod-eu") == nil {
		t.Error("prod-eu was dropped by AddClusterEntry")
	}
	if findCluster(spec, "prod-us") == nil {
		t.Error("prod-us was dropped by AddClusterEntry")
	}
}

func TestAddClusterEntry_LegacyBareYAML_UpgradesToEnvelope(t *testing.T) {
	// Pre-V125-1-9 hand-authored file is read successfully; the new
	// mutator emits the canonical envelope on save. This is the
	// silent-upgrade contract documented in V125-1-9.
	out, err := AddClusterEntry([]byte(legacyBareFixture), ClusterEntryInput{
		Name:   "staging-01",
		Labels: map[string]string{"cert-manager": "true"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "AddClusterEntry (legacy bare input)")
	if findCluster(spec, "staging-01") == nil {
		t.Error("staging-01 missing from output")
	}
}

func TestAddClusterEntry_EmptyInput_BootstrapsEnvelope(t *testing.T) {
	out, err := AddClusterEntry([]byte(""), ClusterEntryInput{
		Name:   "first-cluster",
		Labels: map[string]string{"cert-manager": "true"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "AddClusterEntry (empty input)")
	if len(spec.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(spec.Clusters))
	}
	if spec.Clusters[0].Name != "first-cluster" {
		t.Errorf("expected first-cluster, got %q", spec.Clusters[0].Name)
	}
}

func TestAddClusterEntry_Idempotent_DuplicateSilentlySkipped(t *testing.T) {
	// Preserves the legacy contract — orchestrator.RegisterCluster
	// (and adoptSingleCluster) rely on a no-error duplicate add so a
	// retry after a partial failure does not surface a misleading
	// "cluster already exists" error. The output document is still
	// emitted (with the canonical envelope), but spec.Clusters does
	// NOT carry the duplicate twice.
	out, err := AddClusterEntry([]byte(envelopedFixture), ClusterEntryInput{
		Name:   "prod-eu", // already present
		Region: "different-region-should-be-ignored",
		Labels: map[string]string{"keda": "true"},
	})
	if err != nil {
		t.Fatalf("idempotent add must not error, got %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "AddClusterEntry (duplicate)")
	if len(spec.Clusters) != 2 {
		t.Errorf("expected 2 clusters (no duplicate), got %d", len(spec.Clusters))
	}
	// Existing entry's region/labels must NOT have been overwritten by
	// the duplicate-add — adoption-path semantics require it to be a
	// pure no-op on cluster state.
	existing := findCluster(spec, "prod-eu")
	if existing == nil {
		t.Fatal("prod-eu missing after duplicate add")
	}
	if existing.Region != "eu-west-1" {
		t.Errorf("duplicate add must not overwrite region, got %q", existing.Region)
	}
}

func TestAddClusterEntry_EmptyLabels_OmitsField(t *testing.T) {
	out, err := AddClusterEntry([]byte(envelopedFixture), ClusterEntryInput{
		Name: "labelless",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "AddClusterEntry (no labels)")
	added := findCluster(spec, "labelless")
	if added == nil {
		t.Fatal("labelless missing")
	}
	// Labels omitempty — the reader's normaliseLabels treats nil and
	// the legacy empty-array sentinel and {} all as empty.
	if got := normaliseLabels(added.Labels); len(got) != 0 {
		t.Errorf("expected empty labels, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// RemoveClusterEntry
// ---------------------------------------------------------------------------

func TestRemoveClusterEntry_EnvelopedYAML_RemovesByName(t *testing.T) {
	out, err := RemoveClusterEntry([]byte(envelopedFixture), "prod-us")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "RemoveClusterEntry")
	if len(spec.Clusters) != 1 {
		t.Fatalf("expected 1 cluster after remove, got %d", len(spec.Clusters))
	}
	if findCluster(spec, "prod-us") != nil {
		t.Error("prod-us was not removed")
	}
	if findCluster(spec, "prod-eu") == nil {
		t.Error("prod-eu was incorrectly removed")
	}
}

func TestRemoveClusterEntry_NotFound_ReturnsError(t *testing.T) {
	_, err := RemoveClusterEntry([]byte(envelopedFixture), "ghost")
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing cluster: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Enable / DisableAddonLabel (cluster mutator path)
// ---------------------------------------------------------------------------

func TestEnableAddonLabel_EnvelopedYAML_AddsLabel(t *testing.T) {
	out, err := EnableAddonLabel([]byte(envelopedFixture), "prod-eu", "monitoring")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "EnableAddonLabel (add)")
	c := findCluster(spec, "prod-eu")
	if c == nil {
		t.Fatal("prod-eu missing")
	}
	labels := normaliseLabels(c.Labels)
	if labels["monitoring"] != "enabled" {
		t.Errorf("expected monitoring=enabled, got %q", labels["monitoring"])
	}
	// Pre-existing labels untouched.
	if labels["cert-manager"] != "true" {
		t.Errorf("expected cert-manager=true preserved, got %q", labels["cert-manager"])
	}
}

func TestEnableAddonLabel_EnvelopedYAML_OverwritesExisting(t *testing.T) {
	out, err := EnableAddonLabel([]byte(envelopedFixture), "prod-eu", "keda")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "EnableAddonLabel (overwrite)")
	c := findCluster(spec, "prod-eu")
	labels := normaliseLabels(c.Labels)
	if labels["keda"] != "enabled" {
		t.Errorf("expected keda=enabled, got %q", labels["keda"])
	}
}

func TestDisableAddonLabel_EnvelopedYAML_SetsDisabled(t *testing.T) {
	out, err := DisableAddonLabel([]byte(envelopedFixture), "prod-us", "cert-manager")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "DisableAddonLabel")
	c := findCluster(spec, "prod-us")
	labels := normaliseLabels(c.Labels)
	if labels["cert-manager"] != "disabled" {
		t.Errorf("expected cert-manager=disabled, got %q", labels["cert-manager"])
	}
	// Other cluster (prod-eu) untouched.
	other := findCluster(spec, "prod-eu")
	otherLabels := normaliseLabels(other.Labels)
	if otherLabels["cert-manager"] != "true" {
		t.Errorf("prod-eu cert-manager label was mutated: got %q", otherLabels["cert-manager"])
	}
}

func TestEnableAddonLabel_ClusterNotFound(t *testing.T) {
	_, err := EnableAddonLabel([]byte(envelopedFixture), "ghost", "monitoring")
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the missing cluster: %v", err)
	}
}

func TestDisableAddonLabel_ClusterNotFound(t *testing.T) {
	_, err := DisableAddonLabel([]byte(envelopedFixture), "ghost", "monitoring")
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}
}

// ---------------------------------------------------------------------------
// UpdateClusterSecretPath
// ---------------------------------------------------------------------------

func TestUpdateClusterSecretPath_EnvelopedYAML_AddsField(t *testing.T) {
	out, err := UpdateClusterSecretPath([]byte(envelopedFixture), "prod-eu", "aws-sm/prod-eu")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "UpdateClusterSecretPath (add)")
	c := findCluster(spec, "prod-eu")
	if c.SecretPath != "aws-sm/prod-eu" {
		t.Errorf("expected secretPath=aws-sm/prod-eu, got %q", c.SecretPath)
	}
}

func TestUpdateClusterSecretPath_EnvelopedYAML_ReplacesField(t *testing.T) {
	out, err := UpdateClusterSecretPath([]byte(envelopedFixture), "prod-us", "vault/prod-us")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "UpdateClusterSecretPath (replace)")
	c := findCluster(spec, "prod-us")
	if c.SecretPath != "vault/prod-us" {
		t.Errorf("expected secretPath=vault/prod-us, got %q", c.SecretPath)
	}
}

func TestUpdateClusterSecretPath_EnvelopedYAML_ClearsField(t *testing.T) {
	out, err := UpdateClusterSecretPath([]byte(envelopedFixture), "prod-us", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec := assertEnvelopedOutput(t, out, "UpdateClusterSecretPath (clear)")
	c := findCluster(spec, "prod-us")
	if c.SecretPath != "" {
		t.Errorf("expected secretPath cleared, got %q", c.SecretPath)
	}
	// Yaml omitempty — the field should not appear in the bytes when cleared.
	if strings.Contains(string(out), "secretPath:") {
		t.Errorf("cleared field should be omitted from yaml output:\n%s", out)
	}
}

func TestUpdateClusterSecretPath_ClusterNotFound(t *testing.T) {
	_, err := UpdateClusterSecretPath([]byte(envelopedFixture), "ghost", "aws-sm/ghost")
	if err == nil {
		t.Fatal("expected error for missing cluster")
	}
}

// ---------------------------------------------------------------------------
// Cross-cutting: full round-trip (load → mutate twice → re-load) must
// converge on the expected struct state without drift.
// ---------------------------------------------------------------------------

func TestClusterMutators_FullRoundTrip_StructStateStable(t *testing.T) {
	body := []byte(envelopedFixture)

	// Add a cluster.
	body, err := AddClusterEntry(body, ClusterEntryInput{
		Name:   "staging-01",
		Region: "eu-west-1",
		Labels: map[string]string{"keda": "true"},
	})
	if err != nil {
		t.Fatalf("AddClusterEntry: %v", err)
	}

	// Enable a label on it.
	body, err = EnableAddonLabel(body, "staging-01", "monitoring")
	if err != nil {
		t.Fatalf("EnableAddonLabel: %v", err)
	}

	// Set secretPath on it.
	body, err = UpdateClusterSecretPath(body, "staging-01", "aws-sm/staging-01")
	if err != nil {
		t.Fatalf("UpdateClusterSecretPath: %v", err)
	}

	// Remove a different cluster.
	body, err = RemoveClusterEntry(body, "prod-us")
	if err != nil {
		t.Fatalf("RemoveClusterEntry: %v", err)
	}

	spec := assertEnvelopedOutput(t, body, "round-trip final")

	// staging-01 carries all three mutations.
	staging := findCluster(spec, "staging-01")
	if staging == nil {
		t.Fatal("staging-01 missing")
	}
	if staging.Region != "eu-west-1" {
		t.Errorf("staging-01 region: got %q", staging.Region)
	}
	if staging.SecretPath != "aws-sm/staging-01" {
		t.Errorf("staging-01 secretPath: got %q", staging.SecretPath)
	}
	labels := normaliseLabels(staging.Labels)
	if labels["keda"] != "true" || labels["monitoring"] != "enabled" {
		t.Errorf("staging-01 labels unexpected: %v", labels)
	}

	// prod-us is gone.
	if findCluster(spec, "prod-us") != nil {
		t.Error("prod-us still present after RemoveClusterEntry")
	}
	// prod-eu untouched.
	if findCluster(spec, "prod-eu") == nil {
		t.Error("prod-eu disappeared during round-trip")
	}
}
