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
  - appName: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.14.0
    namespace: cert-manager
  - appName: metrics-server
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

	result, err := orch.UpgradeAddonGlobal(context.Background(), "cert-manager", "1.15.0")
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

	_, err := orch.UpgradeAddonGlobal(context.Background(), "nonexistent", "1.0.0")
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

	result, err := orch.UpgradeAddonCluster(context.Background(), "cert-manager", "prod-eu", "1.15.0")
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

	result, err := orch.UpgradeAddonCluster(context.Background(), "cert-manager", "prod-eu", "1.15.0")
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

	result, err := orch.UpgradeAddons(context.Background(), upgrades)
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

func TestSyncWave_AddAddonWithSyncWave(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.AddAddon(context.Background(), AddAddonRequest{
		Name:      "external-dns",
		Chart:     "external-dns",
		RepoURL:   "https://kubernetes-sigs.github.io/external-dns",
		Version:   "1.14.0",
		Namespace: "external-dns",
		SyncWave:  -1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	catalogContent := string(git.files["charts/external-dns/addon.yaml"])
	if !strings.Contains(catalogContent, "syncWave: -1") {
		t.Errorf("expected syncWave: -1, got:\n%s", catalogContent)
	}
}

func TestSyncWave_AddAddonWithoutSyncWave(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.AddAddon(context.Background(), AddAddonRequest{
		Name:      "prometheus",
		Chart:     "prometheus",
		RepoURL:   "https://prometheus-community.github.io/helm-charts",
		Version:   "25.0.0",
		Namespace: "monitoring",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	catalogContent := string(git.files["charts/prometheus/addon.yaml"])
	if strings.Contains(catalogContent, "syncWave") {
		t.Errorf("syncWave should not appear when zero, got:\n%s", catalogContent)
	}
}
