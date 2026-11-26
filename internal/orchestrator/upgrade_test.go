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

// TestUpgradeWriters_NeverWriteTheV3ShapeOnV4Repo keeps the half of the old
// VERIFY-story-4 pin that still matters. These three used to read and
// rewrite configuration/addons-catalog.yaml or
// configuration/addons-clusters-values/<cluster>.yaml (the v3 shape) even
// on an already-v4 repo — a write into files nothing v4-aware reads, and
// one that can bring a second cluster registry to life.
//
// For a while the answer was to REFUSE on a v4 repo ("operation not
// supported on a v4 repo"). Pre-release walk 4 found that this left the
// main Upgrade button, `sharko upgrade-addon` and the API with no working
// upgrade at all. They now do the v4 upgrade instead (upgrade_all_v4.go,
// upgrade_v4.go; proven in upgrade_all_v4_test.go). What must stay true is
// what this test checks: whatever they do on a v4 repo, the v3 files are
// never created and never changed.
func TestUpgradeWriters_NeverWriteTheV3ShapeOnV4Repo(t *testing.T) {
	const v3ClusterValues = "configuration/addons-clusters-values/prod-eu.yaml"
	for _, tc := range upgradeGuardCases() {
		t.Run(tc.name, func(t *testing.T) {
			git := newMockGitProvider()
			git.files[EnginePinPath] = []byte(v4EnginePinYAML) // makes isV4Repo true
			// Populate the v3 files too, so a bug that takes the old path
			// would find them and rewrite them.
			git.files[catalogPath] = sampleCatalog()
			v3Values := []byte("cert-manager:\n  enabled: true\n  version: 1.14.0\n")
			git.files[v3ClusterValues] = v3Values
			orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

			_ = tc.call(context.Background(), orch)

			if string(git.files[catalogPath]) != string(sampleCatalog()) {
				t.Errorf("%s rewrote %s on a v4 repo", tc.name, catalogPath)
			}
			if string(git.files[v3ClusterValues]) != string(v3Values) {
				t.Errorf("%s rewrote %s on a v4 repo", tc.name, v3ClusterValues)
			}
			for p := range git.files {
				if strings.HasPrefix(p, "configuration/") && p != catalogPath && p != v3ClusterValues {
					t.Errorf("%s created a v3 file on a v4 repo: %s", tc.name, p)
				}
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
