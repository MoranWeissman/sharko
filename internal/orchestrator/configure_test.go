package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// envelopedKedaCatalog is the V125-1-9.2 enveloped fixture used by the
// ZG1-A.264.1 round-trip tests below — schema header on line 1,
// apiVersion/kind/metadata/spec envelope, and one cert-manager entry the
// complex-fields path can mutate.
const envelopedKedaCatalog = `# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json
apiVersion: sharko.dev/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets:
    - name: cert-manager
      repoURL: https://charts.jetstack.io
      chart: cert-manager
      version: 1.14.0
      namespace: cert-manager
`

// catalogSchemaHeaderLine pins the canonical line-1 emission for the
// shape-pinning regression test. Anything else here would break editor
// inline validation against docs/schemas/addons-catalog.v1.json.
const catalogSchemaHeaderLine = "# yaml-language-server: $schema=https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/addons-catalog.v1.json"

// assertEnvelopedComplexUpdate is the shared post-condition every
// complex-fields update must satisfy after ZG1-A.264.1: schema header on
// line 1, apiVersion/kind survive in-place, and the document re-parses
// cleanly via ParseAddonsCatalog. Returns the re-parsed entries so the
// individual tests can assert their field-level expectations.
func assertEnvelopedComplexUpdate(t *testing.T, out []byte, msg string) []models.AddonCatalogEntry {
	t.Helper()
	if !strings.HasPrefix(string(out), catalogSchemaHeaderLine+"\n") {
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

func TestConfigureAddon_NoName(t *testing.T) {
	orch := New(nil, defaultCreds(), newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

func TestConfigureAddon_NoUpdatableFields(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
`)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{Name: "cert-manager"})
	if err == nil {
		t.Fatal("expected error for no updatable fields")
	}
	if !strings.Contains(err.Error(), "no updatable fields") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfigureAddon_UpdateSelfHeal(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
`)

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	selfHeal := true
	result, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:     "cert-manager",
		SelfHeal: &selfHeal,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	updated := string(git.files["configuration/addons-catalog.yaml"])
	if !strings.Contains(updated, "selfHeal: true") {
		t.Errorf("expected selfHeal: true in catalog, got:\n%s", updated)
	}
}

func TestConfigureAddon_AdditionalSources(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
`)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	result, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:              "cert-manager",
		AdditionalSources: []models.AddonSource{{RepoURL: "https://example.com", Chart: "extra-chart", Version: "1.0.0"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	updated := string(git.files["configuration/addons-catalog.yaml"])
	if !strings.Contains(updated, "https://example.com") {
		t.Errorf("expected additional source repoURL in catalog, got:\n%s", updated)
	}
}

// ---------------------------------------------------------------------------
// ZG1-A.264.1: envelope round-trip coverage for the complex-fields branch.
//
// Before A.264.1 the complex-fields path (SyncOptions / AdditionalSources /
// IgnoreDifferences / ExtraHelmValues) did its own bare yaml.Unmarshal +
// yaml.Marshal cycle that silently stripped the V125-1-9.2 envelope
// (apiVersion / kind / metadata / spec) from addons-catalog.yaml on every
// UPDATE — leaving the next reconciler read failing the IsEnveloped check.
// The fix routes the branch through config.NewParser().ParseAddonsCatalog
// and config.MarshalAddonCatalog so the envelope is preserved on read AND
// the canonical envelope is always emitted on write. These tests pin that
// contract.
// ---------------------------------------------------------------------------

// TestConfigureAddon_ComplexFields_EnvelopedYAML_PreservesEnvelope is the
// primary regression test: take an already-enveloped catalog, apply a
// complex-field update, and assert the output is still enveloped with the
// schema header on line 1 (so editor inline validation keeps working).
func TestConfigureAddon_ComplexFields_EnvelopedYAML_PreservesEnvelope(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(envelopedKedaCatalog)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:        "cert-manager",
		SyncOptions: []string{"CreateNamespace=true", "ServerSideApply=true"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := git.files["configuration/addons-catalog.yaml"]
	entries := assertEnvelopedComplexUpdate(t, out, "complex update on enveloped catalog")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if got, want := entries[0].SyncOptions, []string{"CreateNamespace=true", "ServerSideApply=true"}; !equalStringSlice(got, want) {
		t.Errorf("SyncOptions mismatch:\n  got:  %v\n  want: %v", got, want)
	}
}

// TestConfigureAddon_ComplexFields_LegacyBare_UpgradesToEnvelope checks
// the back-compat path: a pre-V125-1-9.2 bare-YAML catalog is read, the
// complex-field update applies, and the output is upgraded to the
// canonical envelope (the V125-1-9 → V126 transition path).
func TestConfigureAddon_ComplexFields_LegacyBare_UpgradesToEnvelope(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
`)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:            "cert-manager",
		ExtraHelmValues: map[string]string{"replicas": "3"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := git.files["configuration/addons-catalog.yaml"]
	entries := assertEnvelopedComplexUpdate(t, out, "complex update on legacy bare catalog")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if got := entries[0].ExtraHelmValues["replicas"]; got != "3" {
		t.Errorf("ExtraHelmValues[replicas] = %q, want %q", got, "3")
	}
}

// TestConfigureAddon_ComplexFields_MissingEntry_ReturnsError preserves the
// pre-fix error-on-not-found contract: a complex-field update against an
// addon name that doesn't exist surfaces a user-visible error rather than
// silently appending or no-op.
func TestConfigureAddon_ComplexFields_MissingEntry_ReturnsError(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(envelopedKedaCatalog)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:        "does-not-exist",
		SyncOptions: []string{"CreateNamespace=true"},
	})
	if err == nil {
		t.Fatal("expected error for missing addon, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// TestConfigureAddon_ComplexFields_MultipleFields_AllApplied asserts that
// a single ConfigureAddon call carrying multiple complex+simple fields
// applies every supplied field in one round-trip — the orchestrator's
// existing UI behaviour (the editor batches all dirty fields into one
// request) depends on this.
func TestConfigureAddon_ComplexFields_MultipleFields_AllApplied(t *testing.T) {
	git := newMockGitProvider()
	git.files["configuration/addons-catalog.yaml"] = []byte(envelopedKedaCatalog)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)

	selfHeal := true
	_, err := orch.ConfigureAddon(context.Background(), ConfigureAddonRequest{
		Name:              "cert-manager",
		Version:           "1.15.0",
		SelfHeal:          &selfHeal,
		SyncOptions:       []string{"CreateNamespace=true"},
		AdditionalSources: []models.AddonSource{{RepoURL: "https://example.com", Chart: "extra", Version: "0.1.0"}},
		IgnoreDifferences: []map[string]interface{}{{"group": "apps", "kind": "Deployment", "jsonPointers": []string{"/spec/replicas"}}},
		ExtraHelmValues:   map[string]string{"global.foo": "bar"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := git.files["configuration/addons-catalog.yaml"]
	entries := assertEnvelopedComplexUpdate(t, out, "multi-field complex update")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Version != "1.15.0" {
		t.Errorf("Version = %q, want %q", e.Version, "1.15.0")
	}
	if e.SelfHeal == nil || *e.SelfHeal != true {
		t.Errorf("SelfHeal = %v, want true", e.SelfHeal)
	}
	if got, want := e.SyncOptions, []string{"CreateNamespace=true"}; !equalStringSlice(got, want) {
		t.Errorf("SyncOptions = %v, want %v", got, want)
	}
	if len(e.AdditionalSources) != 1 || e.AdditionalSources[0].RepoURL != "https://example.com" {
		t.Errorf("AdditionalSources = %+v, want one entry with example.com", e.AdditionalSources)
	}
	if len(e.IgnoreDifferences) != 1 {
		t.Errorf("IgnoreDifferences = %v, want 1 entry", e.IgnoreDifferences)
	}
	if e.ExtraHelmValues["global.foo"] != "bar" {
		t.Errorf("ExtraHelmValues[global.foo] = %q, want %q", e.ExtraHelmValues["global.foo"], "bar")
	}
}

// equalStringSlice is a small helper to keep the round-trip tests
// readable. reflect.DeepEqual would work too but a typed compare lets
// callers see the slice diff in the error message without a cast.
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
