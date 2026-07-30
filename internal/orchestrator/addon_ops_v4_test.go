package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/schema"
)

// v4TestCuratedCatalogYAML seeds one addon ("cert-manager") with a
// required value and a needed secret — the AC 4.3 scenario verbatim
// ("an addon with required values and a needed secret").
const v4TestCuratedCatalogYAML = `
addons:
  - name: cert-manager
    description: X.509 certificate management for Kubernetes.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    license: Apache-2.0
    category: security
    maintainers: ["jetstack"]
    curated_by: ["cncf-graduated"]
    required_values:
      - key: installCRDs
        description: Whether to install cert-manager's CRDs.
    secrets:
      - name: acme-dns-token
        description: DNS provider API token for ACME DNS-01 challenges.
  - name: metrics-server
    description: Container resource metrics for Kubernetes.
    chart: metrics-server
    repo: https://kubernetes-sigs.github.io/metrics-server
    default_namespace: metrics-server
    license: Apache-2.0
    category: observability
    maintainers: ["sig-instrumentation"]
    curated_by: ["cncf-graduated"]
`

func v4TestCuratedCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadBytes([]byte(v4TestCuratedCatalogYAML))
	if err != nil {
		t.Fatalf("building test curated catalog: %v", err)
	}
	return cat
}

// assignPath / clusterValsPath are the error-checking test wrappers around
// the v4 path builders, which now refuse a name that could escape the v4
// data folders (the path-traversal fix). Every name these tests pass is a
// plain valid one, so an error here is a bug in the test itself.
func assignPath(t *testing.T, cluster string) string {
	t.Helper()
	p, err := v4ClusterAssignmentPath(cluster)
	if err != nil {
		t.Fatalf("v4ClusterAssignmentPath(%q): %v", cluster, err)
	}
	return p
}

func clusterValsPath(t *testing.T, cluster, addon string) string {
	t.Helper()
	p, err := v4ClusterValuesPath(cluster, addon)
	if err != nil {
		t.Fatalf("v4ClusterValuesPath(%q, %q): %v", cluster, addon, err)
	}
	return p
}

func newV4TestOrchestrator(t *testing.T, git *mockGitProvider) *Orchestrator {
	t.Helper()
	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetCuratedCatalog(v4TestCuratedCatalog(t))
	return orch
}

func TestEnableAddonV4_BlocksOnMissingRequiredValue_BeforeAnyGitWrite(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	// Secret IS declared so only the required-value problem should fire.
	orch.SetSecretManagement(map[string]AddonSecretDefinition{
		"cert-manager": {AddonName: "cert-manager", SecretName: "cert-manager-dns", Namespace: "cert-manager"},
	}, nil, nil)

	_, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
		// No Values supplied — installCRDs is never set anywhere.
	})
	if err == nil {
		t.Fatal("expected a semantic validation error, got nil")
	}
	var verr *V4SemanticValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *V4SemanticValidationError, got %T: %v", err, err)
	}
	if verr.Cluster != "prod-eu" || verr.Addon != "cert-manager" {
		t.Errorf("verr = %+v, want Cluster=prod-eu Addon=cert-manager", verr)
	}
	found := false
	for _, p := range verr.Problems {
		if strings.Contains(p, "installCRDs") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a problem naming installCRDs, got %v", verr.Problems)
	}

	// BEFORE any branch/PR — the whole point of "sharpened".
	if len(git.branches) != 0 {
		t.Errorf("expected no branch to be created, got %v", git.branches)
	}
	if len(git.prs) != 0 {
		t.Errorf("expected no PR to be created, got %d", len(git.prs))
	}
	if _, ok := git.files[assignPath(t, "prod-eu")]; ok {
		t.Error("expected no clusters/prod-eu.yaml write on validation failure")
	}
}

func TestEnableAddonV4_BlocksOnUndeclaredSecret(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	// No SetSecretManagement call at all — cert-manager's declared secret
	// (acme-dns-token) has nowhere to come from.

	_, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Values:  map[string]interface{}{"installCRDs": true},
		Yes:     true,
	})
	var verr *V4SemanticValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected *V4SemanticValidationError, got %T: %v", err, err)
	}
	found := false
	for _, p := range verr.Problems {
		if strings.Contains(p, "acme-dns-token") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a problem naming acme-dns-token, got %v", verr.Problems)
	}
	if len(git.branches) != 0 {
		t.Error("expected no branch to be created on validation failure")
	}
}

func TestEnableAddonV4_ValidInputs_WritesExpectedFiles(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	orch.SetSecretManagement(map[string]AddonSecretDefinition{
		"cert-manager": {AddonName: "cert-manager", SecretName: "cert-manager-dns", Namespace: "cert-manager"},
	}, nil, nil)

	result, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Version: strPtrTest("1.12.0"),
		Values:  map[string]interface{}{"installCRDs": true, "replicaCount": 2},
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.PRUrl == "" {
		t.Error("expected a PR to be opened")
	}

	clusterPath := assignPath(t, "prod-eu")
	clusterBytes, ok := git.files[clusterPath]
	if !ok {
		t.Fatalf("expected %s to be written", clusterPath)
	}
	spec, err := models.LoadClusterAssignment(clusterBytes)
	if err != nil {
		t.Fatalf("clusters/prod-eu.yaml failed to round-trip through LoadClusterAssignment: %v", err)
	}
	entry, ok := spec.Addons["cert-manager"]
	if !ok || !entry.Enabled || entry.Version != "1.12.0" {
		t.Errorf("cert-manager entry = %+v (ok=%v), want Enabled=true Version=1.12.0", entry, ok)
	}

	// Gate requirement: generated files pass sharko validate (the JSON
	// Schema validator). LoadClusterAssignment already ran it once inside
	// SaveClusterAssignment before this byte slice was ever committed;
	// this is the explicit round-trip proof the story's gate asks for.
	if validator, vErr := schema.DefaultValidator(); vErr == nil && validator != nil {
		if err := validator.Validate(schema.KindClusterAssignment, clusterBytes); err != nil {
			t.Errorf("clusters/prod-eu.yaml failed sharko validate: %v", err)
		}
	}

	valuesPath := clusterValsPath(t, "prod-eu", "cert-manager")
	valuesBytes, ok := git.files[valuesPath]
	if !ok {
		t.Fatalf("expected %s to be written", valuesPath)
	}
	valuesMap, err := parseYAMLMap(valuesBytes)
	if err != nil {
		t.Fatalf("parsing written values file: %v", err)
	}
	if valuesMap["installCRDs"] != true {
		t.Errorf("values file installCRDs = %v, want true", valuesMap["installCRDs"])
	}
}

func TestEnableAddonV4_DryRun_NoSideEffectsAndShowsExactFiles(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)
	orch.SetSecretManagement(map[string]AddonSecretDefinition{
		"cert-manager": {AddonName: "cert-manager", SecretName: "cert-manager-dns"},
	}, nil, nil)

	result, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Values:  map[string]interface{}{"installCRDs": true},
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected a DryRun result")
	}
	if len(git.branches) != 0 || len(git.prs) != 0 {
		t.Error("dry run must have zero git side effects")
	}
	paths := map[string]bool{}
	for _, f := range result.DryRun.FilesToWrite {
		paths[f.Path] = true
		if f.Action != "create" {
			t.Errorf("expected action=create for a brand-new file %s, got %s", f.Path, f.Action)
		}
	}
	if !paths[assignPath(t, "prod-eu")] {
		t.Errorf("expected preview to include %s, got %v", assignPath(t, "prod-eu"), paths)
	}
	if !paths[clusterValsPath(t, "prod-eu", "cert-manager")] {
		t.Errorf("expected preview to include %s, got %v", clusterValsPath(t, "prod-eu", "cert-manager"), paths)
	}
}

func TestEnableAddonV4_NoValues_SkipsClusterValuesWrite(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	_, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "metrics-server", // no required values, no secrets
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := git.files[clusterValsPath(t, "prod-eu", "metrics-server")]; ok {
		t.Error("expected no values file written when req.Values is empty")
	}
	if _, ok := git.files[assignPath(t, "prod-eu")]; !ok {
		t.Error("expected clusters/prod-eu.yaml to be written")
	}
}

func TestDisableAddonV4_PreservesVersionPin(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	// Seed an enabled entry that carries BOTH a version pin and a settings
	// block — the two things a plain disable must not throw away (design
	// doc §2.1: "false keeps the entry — and its settings"). Seeding only
	// the enabled flag would let a regression that drops Version/Settings
	// pass this test unnoticed.
	seedNamespace := "kube-system"
	seedPrune := true
	_, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu",
		Addon:   "metrics-server",
		Version: strPtrTest("3.12.1"),
		Settings: &models.ClusterAssignmentAddonSettings{
			Namespace: seedNamespace,
			Prune:     &seedPrune,
		},
		Yes: true,
	})
	if err != nil {
		t.Fatalf("seed enable: %v", err)
	}
	seeded, _ := models.LoadClusterAssignment(git.files[assignPath(t, "prod-eu")])
	seededEntry := seeded.Addons["metrics-server"]
	if !seededEntry.Enabled {
		t.Fatal("seed did not enable metrics-server")
	}
	if seededEntry.Version != "3.12.1" {
		t.Fatalf("seed Version = %q, want 3.12.1", seededEntry.Version)
	}
	if seededEntry.Settings == nil || seededEntry.Settings.Namespace != seedNamespace {
		t.Fatalf("seed Settings = %+v, want Namespace=%s", seededEntry.Settings, seedNamespace)
	}

	_, err = orch.DisableAddonV4(context.Background(), DisableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	spec, err := models.LoadClusterAssignment(git.files[assignPath(t, "prod-eu")])
	if err != nil {
		t.Fatalf("parse after disable: %v", err)
	}
	entry, ok := spec.Addons["metrics-server"]
	if !ok {
		t.Fatal("expected the entry to be kept (not removed) on a plain disable")
	}
	if entry.Enabled {
		t.Error("expected Enabled=false after disable")
	}
	if entry.Version != "3.12.1" {
		t.Errorf("Version after disable = %q, want the pin to survive (3.12.1)", entry.Version)
	}
	if entry.Settings == nil {
		t.Fatal("Settings after disable = nil, want the seeded settings block to survive")
	}
	if entry.Settings.Namespace != seedNamespace {
		t.Errorf("Settings.Namespace after disable = %q, want %q", entry.Settings.Namespace, seedNamespace)
	}
	if entry.Settings.Prune == nil || !*entry.Settings.Prune {
		t.Errorf("Settings.Prune after disable = %v, want the seeded true to survive", entry.Settings.Prune)
	}
}

func TestDisableAddonV4_Remove_DeletesEntry(t *testing.T) {
	git := newMockGitProvider()
	orch := newV4TestOrchestrator(t, git)

	_, err := orch.EnableAddonV4(context.Background(), EnableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Yes: true,
	})
	if err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	_, err = orch.DisableAddonV4(context.Background(), DisableAddonV4Request{
		Cluster: "prod-eu", Addon: "metrics-server", Remove: true, Yes: true,
	})
	if err != nil {
		t.Fatalf("disable+remove: %v", err)
	}
	spec, err := models.LoadClusterAssignment(git.files[assignPath(t, "prod-eu")])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := spec.Addons["metrics-server"]; ok {
		t.Error("expected the entry to be gone entirely with remove=true")
	}
}

func strPtrTest(s string) *string { return &s }
