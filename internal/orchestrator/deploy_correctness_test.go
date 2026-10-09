package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// TestRegisterCluster_AddonLabelIsEnabled_RecognizedDownstream is the
// deploy-blocker regression test (V2-cleanup-20). RegisterCluster with an
// addon switched on must write the canonical "enabled" label to BOTH the
// managed-clusters.yaml entry AND (for the kubeconfig direct-write path) the
// ArgoCD cluster Secret — because the live ApplicationSet selector and
// config.GetEnabledAddons only treat "enabled" as on. A register-time "true"
// label reads as NOT-enabled everywhere downstream → the addon never deploys.
func TestRegisterCluster_AddonLabelIsEnabled_RecognizedDownstream(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	orch := New(nil, defaultCreds(), argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"monitoring": true, "logging": false},
		Region: "eu-west-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected status success, got %q (error: %s)", result.Status, result.Error)
	}

	// The managed-clusters.yaml entry must carry "enabled"/"disabled", never
	// the legacy "true"/"false".
	mcData, ok := git.files["configuration/managed-clusters.yaml"]
	if !ok {
		t.Fatal("managed-clusters.yaml was not written")
	}
	spec, err := models.LoadManagedClusters(mcData)
	if err != nil {
		t.Fatalf("loading managed-clusters.yaml: %v", err)
	}
	var entry *models.ManagedClusterEntry
	for i := range spec.Clusters {
		if spec.Clusters[i].Name == "prod-eu" {
			entry = &spec.Clusters[i]
			break
		}
	}
	if entry == nil {
		t.Fatal("prod-eu entry not found in managed-clusters.yaml")
	}
	labels := normaliseLabelsForTest(entry.Labels)
	if labels["monitoring"] != models.LabelEnabled {
		t.Errorf("monitoring label = %q, want %q (the value the ArgoCD selector requires)", labels["monitoring"], models.LabelEnabled)
	}
	if labels["logging"] != models.LabelDisabled {
		t.Errorf("logging label = %q, want %q", labels["logging"], models.LabelDisabled)
	}
	// No legacy boolean vocabulary may leak through.
	if labels["monitoring"] == "true" || labels["logging"] == "false" {
		t.Errorf("legacy true/false vocabulary leaked into managed-clusters labels: %v", labels)
	}

	// End-to-end contract: the parser that the appset/reconciler rely on must
	// see the addon as enabled.
	cluster := models.Cluster{Name: "prod-eu", Labels: labels}
	catalog := []models.AddonCatalogEntry{
		{Name: "monitoring", Chart: "kube-prometheus-stack", RepoURL: "https://example.com", Version: "1.0.0"},
		{Name: "logging", Chart: "loki", RepoURL: "https://example.com", Version: "1.0.0"},
	}
	enabled := config.NewParser().GetEnabledAddons(cluster, catalog)
	enabledNames := map[string]bool{}
	for _, a := range enabled {
		enabledNames[a.AddonName] = true
	}
	if !enabledNames["monitoring"] {
		t.Error("GetEnabledAddons did NOT recognise monitoring as enabled — the addon would never deploy")
	}
	if enabledNames["logging"] {
		t.Error("GetEnabledAddons wrongly recognised logging (disabled) as enabled")
	}
}

// normaliseLabelsForTest coerces the interface{} Labels field into a string
// map for assertions, mirroring how config/reconciler read it (any scalar is
// stringified via fmt %v).
func normaliseLabelsForTest(raw interface{}) map[string]string {
	out := map[string]string{}
	switch v := raw.(type) {
	case models.ClusterLabels:
		// V2-cleanup-22: ManagedClusterEntry.Labels is now ClusterLabels.
		for k, val := range v {
			out[k] = val
		}
	case map[string]string:
		for k, val := range v {
			out[k] = val
		}
	case map[string]interface{}:
		for k, val := range v {
			out[k] = fmt.Sprintf("%v", val)
		}
	}
	return out
}

// TestEnableAddon_LabelWriteFails_NoSuccess proves the half-commit fix
// (V2-cleanup-20, decision #3). When the managed-clusters label cannot be
// written — here because the target cluster is not in managed-clusters.yaml,
// the same condition EnableAddonLabel errors on — EnableAddon must NOT report
// success with only the values file. It must fail honestly and open NO PR.
func TestEnableAddon_LabelWriteFails_NoSuccess(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	// managed-clusters.yaml exists but lists a DIFFERENT cluster, so the
	// EnableAddonLabel write for prod-eu will error ("cluster not found").
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: some-other-cluster\n    labels:\n      cert-manager: enabled\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if result.Status == "success" {
		t.Fatalf("EnableAddon reported success despite a failed label write (half-commit defect)")
	}
	if result.FailedStep != "update_addon_label" {
		t.Errorf("FailedStep = %q, want update_addon_label", result.FailedStep)
	}
	// No PR may have been opened — we must bail BEFORE committing.
	if len(git.prs) != 0 {
		t.Errorf("expected no PR on a failed label write, got %d", len(git.prs))
	}
}

// TestEnableAddon_NoManagedClusters_NoSuccess covers the sibling case where
// managed-clusters.yaml does not exist at all: there is no label to drive
// deployment, so EnableAddon must refuse rather than open a values-only PR.
func TestEnableAddon_NoManagedClusters_NoSuccess(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if result.Status == "success" {
		t.Fatalf("EnableAddon reported success with no managed-clusters.yaml (label that drives deploy is missing)")
	}
	if len(git.prs) != 0 {
		t.Errorf("expected no PR, got %d", len(git.prs))
	}
}

// TestEnableAddon_Success_WritesEnabledLabel is the happy path: when the
// cluster IS in managed-clusters.yaml, EnableAddon succeeds and the label is
// written in the canonical "enabled" vocabulary.
func TestEnableAddon_Success_WritesEnabledLabel(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q (error: %s)", result.Status, result.Error)
	}
	mcData := git.files["configuration/managed-clusters.yaml"]
	if !strings.Contains(string(mcData), "cert-manager: enabled") {
		t.Errorf("expected canonical 'cert-manager: enabled' label, got:\n%s", string(mcData))
	}
}

// TestEnableAddon_ProducesCleanValuesFile asserts the per-cluster values file
// EnableAddon writes contains NO live "<set per cluster>" placeholder
// (V2-cleanup-19) — every override hint lives in a trailing comment block, so
// the file `helm template`s cleanly. The global values file carries the
// per-cluster template block the seeder reads.
func TestEnableAddon_ProducesCleanValuesFile(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n")
	// Global values file with a per-cluster overrides template block — this is
	// what the seeder reads to know which fields to offer as hints.
	git.files["configuration/addons-global-values/cert-manager.yaml"] = []byte(`# Generated by Sharko
cert-manager:
  replicaCount: 1

# --- per-cluster overrides template ---
# Copy under the addon's stanza in configuration/addons-clusters-values/<cluster>.yaml.
# cert-manager:
#   "ingress.host": <set per cluster>
#   replicaCount: <set per cluster>
`)

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.EnableAddon(context.Background(), EnableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %q (error: %s)", result.Status, result.Error)
	}

	values := string(git.files["configuration/addons-clusters-values/prod-eu.yaml"])
	// The addon must be recorded as live enabled.
	if !strings.Contains(values, "cert-manager:") || !strings.Contains(values, "enabled: true") {
		t.Errorf("expected live 'cert-manager:\\n  enabled: true', got:\n%s", values)
	}
	// No LINE may carry a live "<set per cluster>" value — comments are fine.
	for _, line := range strings.Split(values, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "<set per cluster>") {
			t.Errorf("found LIVE '<set per cluster>' placeholder (must be commented only) on line: %q\nfull values:\n%s", line, values)
		}
	}
}
