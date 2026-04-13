package orchestrator

import (
	"context"
	"strings"
	"testing"
)

func TestDisableAddon_Success(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels:\n      cert-manager: true\n      metrics-server: true\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n\ncert-manager:\n  enabled: true\nmetrics-server:\n  enabled: true\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Cleanup: "all",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Git == nil {
		t.Error("expected git result")
	}

	// Verify the values file was updated.
	updatedValues := git.files["configuration/addons-clusters-values/prod-eu.yaml"]
	if updatedValues == nil {
		t.Fatal("expected values file to be updated")
	}
	if !strings.Contains(string(updatedValues), "cert-manager:\n  enabled: false") {
		t.Errorf("expected cert-manager to be disabled in values file, got:\n%s", string(updatedValues))
	}
	// metrics-server should still be enabled.
	if !strings.Contains(string(updatedValues), "metrics-server:\n  enabled: true") {
		t.Errorf("expected metrics-server to remain enabled in values file, got:\n%s", string(updatedValues))
	}
}

func TestDisableAddon_NoneCleanup_OnlyUpdatesValues(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n\ncert-manager:\n  enabled: true\n")
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels:\n      cert-manager: true\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Cleanup: "none",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	// managed-clusters.yaml should NOT be in the written files (cleanup=none skips label update).
	if _, ok := git.files["configuration/managed-clusters.yaml"]; ok {
		// It's the original file, not updated. We need to check the PR content.
		// Since cleanup=none, the managed-clusters.yaml should not appear in the commit.
		// This is validated by checking completed steps.
		for _, step := range result.CompletedSteps {
			if step == "update_addon_label" {
				t.Error("expected no label update with cleanup=none")
			}
		}
	}
}

func TestDisableAddon_RequiresConfirmation(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Cleanup: "all",
		Yes:     false,
	})
	if err == nil {
		t.Fatal("expected error for missing confirmation")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDisableAddon_DryRun(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Cleanup: "all",
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected dry_run result")
	}
	if result.DryRun.PRTitle == "" {
		t.Error("expected PR title in dry run")
	}
	// No PRs should have been created.
	if len(git.prs) > 0 {
		t.Error("expected no PRs in dry-run mode")
	}
}

func TestDisableAddon_EmptyCluster(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Addon: "cert-manager",
		Yes:   true,
	})
	if err == nil {
		t.Fatal("expected error for empty cluster name")
	}
}

func TestDisableAddon_EmptyAddon(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Cluster: "prod-eu",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected error for empty addon name")
	}
}

func TestDisableAddon_InvalidCleanup(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.DisableAddon(context.Background(), DisableAddonRequest{
		Cluster: "prod-eu",
		Addon:   "cert-manager",
		Cleanup: "invalid",
		Yes:     true,
	})
	if err == nil {
		t.Fatal("expected error for invalid cleanup scope")
	}
	if !strings.Contains(err.Error(), "invalid cleanup scope") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExtractAddonsFromValues(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	values := []byte(`# Cluster values for prod-eu
clusterGlobalValues:
  region: eu-west-1

cert-manager:
  enabled: true
metrics-server:
  enabled: true
logging:
  enabled: false
`)

	addons := orch.extractAddonsFromValues(values, "cert-manager")

	if addons["cert-manager"] != false {
		t.Error("expected cert-manager to be disabled")
	}
	if addons["metrics-server"] != true {
		t.Error("expected metrics-server to remain enabled")
	}
	if addons["logging"] != false {
		t.Error("expected logging to remain disabled")
	}
}
