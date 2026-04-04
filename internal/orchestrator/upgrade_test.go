package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

func TestUpgradeAddonGlobal(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	// Pre-populate the catalog file.
	git.files["charts/cert-manager/addon.yaml"] = []byte(
		"# Addon catalog entry for cert-manager\n" +
			"name: cert-manager\n" +
			"chart: cert-manager\n" +
			"repoURL: https://charts.jetstack.io\n" +
			"version: 1.14.0\n" +
			"namespace: cert-manager\n")

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.UpgradeAddonGlobal(context.Background(), "cert-manager", "1.15.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.PRUrl == "" {
		t.Error("expected PR URL in result")
	}

	// Verify the catalog was updated.
	updatedCatalog := git.files["charts/cert-manager/addon.yaml"]
	if !strings.Contains(string(updatedCatalog), "version: 1.15.0") {
		t.Errorf("expected version 1.15.0 in catalog, got:\n%s", string(updatedCatalog))
	}
	if strings.Contains(string(updatedCatalog), "version: 1.14.0") {
		t.Error("old version 1.14.0 should not be present")
	}
}

func TestUpgradeAddonGlobal_MissingVersion(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	// Catalog without version field.
	git.files["charts/broken/addon.yaml"] = []byte("name: broken\nchart: broken\n")

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.UpgradeAddonGlobal(context.Background(), "broken", "1.0.0")
	if err == nil {
		t.Fatal("expected error for missing version field")
	}
	if !strings.Contains(err.Error(), "version field not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUpgradeAddonCluster(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	// Pre-populate the cluster values file.
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
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.PRUrl == "" {
		t.Error("expected PR URL in result")
	}

	// Verify the values file was updated.
	updatedValues := string(git.files["configuration/addons-clusters-values/prod-eu.yaml"])
	if !strings.Contains(updatedValues, "version: 1.15.0") {
		t.Errorf("expected version 1.15.0 in values, got:\n%s", updatedValues)
	}
	if strings.Contains(updatedValues, "version: 1.14.0") {
		t.Error("old version 1.14.0 should not be present")
	}
	// Monitoring section should be untouched.
	if !strings.Contains(updatedValues, "monitoring:") {
		t.Error("monitoring section should still be present")
	}
}

func TestUpgradeAddonCluster_NewVersionField(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	// Cluster values file without version field under the addon.
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
		t.Errorf("expected version 1.15.0 to be inserted, got:\n%s", updatedValues)
	}
}

func TestUpgradeAddons_Batch(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	// Pre-populate catalog files.
	git.files["charts/cert-manager/addon.yaml"] = []byte(
		"name: cert-manager\nversion: 1.14.0\n")
	git.files["charts/metrics-server/addon.yaml"] = []byte(
		"name: metrics-server\nversion: 0.6.0\n")

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	upgrades := map[string]string{
		"cert-manager":   "1.15.0",
		"metrics-server": "0.7.1",
	}

	result, err := orch.UpgradeAddons(context.Background(), upgrades)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.PRUrl == "" {
		t.Error("expected PR URL in result")
	}

	// Verify both catalogs were updated.
	cm := string(git.files["charts/cert-manager/addon.yaml"])
	if !strings.Contains(cm, "version: 1.15.0") {
		t.Errorf("cert-manager catalog not updated:\n%s", cm)
	}
	ms := string(git.files["charts/metrics-server/addon.yaml"])
	if !strings.Contains(ms, "version: 0.7.1") {
		t.Errorf("metrics-server catalog not updated:\n%s", ms)
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
		// No Addons specified — should get defaults.
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}

	// Verify the cluster values file includes default addons.
	valuesContent := string(git.files["configuration/addons-clusters-values/new-cluster.yaml"])
	for _, addon := range []string{"monitoring", "logging", "cert-manager"} {
		if !strings.Contains(valuesContent, addon+":") {
			t.Errorf("expected default addon %q in values file, got:\n%s", addon, valuesContent)
		}
	}

	// Verify ArgoCD labels include default addons.
	if argocd.registeredClusters["new-cluster"] == "" {
		t.Fatal("expected cluster to be registered in ArgoCD")
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
		Addons: map[string]bool{
			"monitoring": true,
			// Only monitoring — should NOT get logging or cert-manager defaults.
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected status 'success', got %q", result.Status)
	}

	// Verify only the explicitly specified addon is in the values file.
	valuesContent := string(git.files["configuration/addons-clusters-values/custom-cluster.yaml"])
	if !strings.Contains(valuesContent, "monitoring:") {
		t.Error("expected monitoring in values file")
	}
	if strings.Contains(valuesContent, "logging:") {
		t.Error("logging should NOT be present — explicit addons should override defaults")
	}
	if strings.Contains(valuesContent, "cert-manager:") {
		t.Error("cert-manager should NOT be present — explicit addons should override defaults")
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

	// Check the catalog entry includes syncWave.
	catalogContent := string(git.files["charts/external-dns/addon.yaml"])
	if !strings.Contains(catalogContent, "syncWave: -1") {
		t.Errorf("expected syncWave: -1 in catalog, got:\n%s", catalogContent)
	}
}

func TestSyncWave_AddAddonWithoutSyncWave(t *testing.T) {
	git := newMockGitProvider()
	argocd := newMockArgocd()

	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.AddAddon(context.Background(), AddAddonRequest{
		Name:      "prometheus",
		Chart:     "prometheus",
		RepoURL:   "https://prometheus-community.github.io/helm-charts",
		Version:   "25.0.0",
		Namespace: "monitoring",
		// SyncWave omitted (zero value).
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Check the catalog entry does NOT include syncWave when zero.
	catalogContent := string(git.files["charts/prometheus/addon.yaml"])
	if strings.Contains(catalogContent, "syncWave") {
		t.Errorf("syncWave should not appear when zero, got:\n%s", catalogContent)
	}
}
