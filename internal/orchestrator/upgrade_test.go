package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

const catalogPath = "configuration/addons-catalog.yaml"

// sampleCatalog returns a realistic addons-catalog.yaml with the given addons.
func sampleCatalog() []byte {
	return []byte(`applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
  - name: metrics-server
    chart: metrics-server
    repoURL: https://kubernetes-sigs.github.io/metrics-server
    version: 0.6.0
    namespace: kube-system
`)
}

func TestUpgradeAddonGlobal(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	git.files[catalogPath] = sampleCatalog()

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpgradeAddonGlobal(context.Background(), "cert-manager", "1.15.0", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.PRUrl == "" {
		t.Fatal("expected PR URL in result")
	}

	updated := string(git.files[catalogPath])
	if !strings.Contains(updated, "version: 1.15.0") {
		t.Errorf("expected version 1.15.0, got:\n%s", updated)
	}
	if strings.Contains(updated, "version: 1.14.0") {
		t.Error("old version 1.14.0 should not be present for cert-manager")
	}
	// metrics-server should be untouched.
	if !strings.Contains(updated, "version: 0.6.0") {
		t.Error("metrics-server version should be unchanged")
	}
}

func TestUpgradeAddonGlobal_AddonNotFound(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	git.files[catalogPath] = sampleCatalog()

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.UpgradeAddonGlobal(context.Background(), "nonexistent", "1.0.0", nil)
	if err == nil {
		t.Fatal("expected error for nonexistent addon")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpgradeAddonCluster(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(
		"# Cluster values for prod-eu\n" +
			"clusterGlobalValues:\n" +
			"  region: eu-west-1\n\n" +
			"cert-manager:\n" +
			"  enabled: true\n" +
			"  version: 1.14.0\n" +
			"monitoring:\n" +
			"  enabled: true\n")

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpgradeAddonCluster(context.Background(), "cert-manager", "prod-eu", "1.15.0", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.PRUrl == "" {
		t.Fatal("expected PR URL in result")
	}

	updatedValues := string(git.files["configuration/addons-clusters-values/prod-eu.yaml"])
	if !strings.Contains(updatedValues, "version: 1.15.0") {
		t.Errorf("expected version 1.15.0, got:\n%s", updatedValues)
	}
	if strings.Contains(updatedValues, "version: 1.14.0") {
		t.Error("old version should not be present")
	}
	if !strings.Contains(updatedValues, "monitoring:") {
		t.Error("monitoring section should be untouched")
	}
}

func TestUpgradeAddonCluster_NewVersionField(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(
		"cert-manager:\n" +
			"  enabled: true\n" +
			"monitoring:\n" +
			"  enabled: true\n")

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpgradeAddonCluster(context.Background(), "cert-manager", "prod-eu", "1.15.0", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	updatedValues := string(git.files["configuration/addons-clusters-values/prod-eu.yaml"])
	if !strings.Contains(updatedValues, "version: 1.15.0") {
		t.Errorf("expected version to be inserted, got:\n%s", updatedValues)
	}
}

func TestUpgradeAddons_Batch(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	git.files[catalogPath] = sampleCatalog()

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	upgrades := map[string]string{
		"cert-manager":   "1.15.0",
		"metrics-server": "0.7.1",
	}

	result, err := orch.UpgradeAddons(context.Background(), upgrades, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.PRUrl == "" {
		t.Fatal("expected PR URL in result")
	}

	updated := string(git.files[catalogPath])
	if !strings.Contains(updated, "version: 1.15.0") {
		t.Errorf("cert-manager not updated:\n%s", updated)
	}
	if !strings.Contains(updated, "version: 0.7.1") {
		t.Errorf("metrics-server not updated:\n%s", updated)
	}

	// Should have created exactly one PR (batch).
	if len(git.prs) != 1 {
		t.Errorf("expected 1 PR for batch, got %d", len(git.prs))
	}
}

// upgradeGuardCase is one v3-shaped upgrade writer under test.
type upgradeGuardCase struct {
	name string
	call func(ctx context.Context, o *Orchestrator) error
}

func upgradeGuardCases() []upgradeGuardCase {
	return []upgradeGuardCase{
		{"UpgradeAddonGlobal", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.UpgradeAddonGlobal(ctx, "cert-manager", "1.15.0", nil)
			return err
		}},
		{"UpgradeAddonCluster", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.UpgradeAddonCluster(ctx, "cert-manager", "prod-eu", "1.15.0", nil)
			return err
		}},
		{"UpgradeAddons", func(ctx context.Context, o *Orchestrator) error {
			_, err := o.UpgradeAddons(ctx, map[string]string{"cert-manager": "1.15.0"}, nil)
			return err
		}},
	}
}

// TestUpgradeWriters_RefuseOnV4Repo is the VERIFY-story-4 pin: "old v3
// upgrade endpoints not v4-gated" (flagged from the PR #642-era review).
// Before this fix, none of these three had a v4 check at all — they read
// and rewrote configuration/addons-catalog.yaml or
// configuration/addons-clusters-values/<cluster>.yaml (the v3 shape) even
// on an already-v4 repo, whose real version pin lives in
// cluster-addons/<cluster>.yaml. That write would land in a file nothing
// v4-aware reads, so the caller would see a merged "successful" upgrade
// that changed nothing the fleet runs — the same class of bug the sibling
// write paths (EnableAddon, AddAddon, ...) were already gated against.
func TestUpgradeWriters_RefuseOnV4Repo(t *testing.T) {
	for _, tc := range upgradeGuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files[EnginePinPath] = []byte(v4EnginePinYAML) // makes isV4Repo true
			// Populate the v3 files too, so a bug that skips the gate would
			// otherwise succeed and prove nothing.
			git.files[catalogPath] = sampleCatalog()
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(
				"cert-manager:\n  enabled: true\n  version: 1.14.0\n")
			orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

			beforeFiles := len(git.files)
			err := tc.call(context.Background(), orch)
			if err == nil {
				t.Fatalf("%s returned nil on a v4 repo — it would have written the v3 shape", tc.name)
			}
			if !IsV4RepoUnsupported(err) {
				t.Fatalf("%s error = %v, want an ErrV4RepoUnsupported refusal", tc.name, err)
			}
			if !strings.Contains(err.Error(), "writes files this repo does not use") {
				t.Errorf("%s message = %q, want the plain-words v4 refusal", tc.name, err.Error())
			}

			// Zero writes: no new/changed files, no branch, no PR. The two
			// v3 fixture files must be byte-identical to what was seeded.
			if len(git.files) != beforeFiles {
				t.Errorf("%s changed the file count despite refusing", tc.name)
			}
			if string(git.files[catalogPath]) != string(sampleCatalog()) {
				t.Errorf("%s mutated %s despite refusing", tc.name, catalogPath)
			}
			if len(git.branches) != 0 {
				t.Errorf("%s created branches %v despite refusing", tc.name, git.branches)
			}
			if len(git.prs) != 0 {
				t.Errorf("%s opened %d PR(s) despite refusing", tc.name, len(git.prs))
			}
		})
	}
}

// TestUpgradeWriters_UnchangedOnV3Repo is the other half: with no engine pin
// present the guard is inert, so all three upgrade writers behave exactly
// as they did before this fix (proceeding to their ordinary success path).
func TestUpgradeWriters_UnchangedOnV3Repo(t *testing.T) {
	for _, tc := range upgradeGuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider() // no EnginePinPath => v3 repo
			git.files[catalogPath] = sampleCatalog()
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(
				"cert-manager:\n  enabled: true\n  version: 1.14.0\n")
			orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

			err := tc.call(context.Background(), orch)
			if IsV4RepoUnsupported(err) {
				t.Fatalf("%s refused a v3 repo as if it were v4: %v", tc.name, err)
			}
		})
	}
}

func TestDefaultAddons_NilAddonsGetDefaults(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"new-cluster": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
			},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetDefaultAddons(map[string]bool{
		"monitoring":   true,
		"logging":      true,
		"cert-manager": true,
	})

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "new-cluster",
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected 'success', got %q", result.Status)
	}

	valuesContent := string(git.files["configuration/addons-clusters-values/new-cluster.yaml"])
	for _, addon := range []string{"monitoring", "logging", "cert-manager"} {
		if !strings.Contains(valuesContent, addon+":") {
			t.Errorf("expected default addon %q in values, got:\n%s", addon, valuesContent)
		}
	}
}

func TestDefaultAddons_ExplicitAddonsIgnoreDefaults(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"custom-cluster": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
			},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetDefaultAddons(map[string]bool{
		"monitoring":   true,
		"logging":      true,
		"cert-manager": true,
	})

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "custom-cluster",
		Region: "us-east-1",
		Addons: map[string]bool{"monitoring": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected 'success', got %q", result.Status)
	}

	valuesContent := string(git.files["configuration/addons-clusters-values/custom-cluster.yaml"])
	if !strings.Contains(valuesContent, "monitoring:") {
		t.Error("expected monitoring")
	}
	if strings.Contains(valuesContent, "logging:") {
		t.Error("logging should NOT be present — explicit overrides defaults")
	}
}
