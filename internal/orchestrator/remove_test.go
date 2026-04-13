package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestRemoveCluster_AllCleanup_Success(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://k8s.example.com:6443"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels:\n      cert-manager: true\n")
	git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte("# values")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
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
	// Should have created a PR.
	if len(git.prs) == 0 {
		t.Error("expected a PR to be created")
	}
	// Values file should be deleted.
	found := false
	for _, f := range git.deletedFiles {
		if strings.Contains(f, "prod-eu.yaml") {
			found = true
		}
	}
	if !found {
		t.Error("expected values file to be deleted")
	}
}

func TestRemoveCluster_GitCleanup_SkipsSecrets(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "git",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	// Should NOT have attempted ArgoCD deletion.
	if len(argocd.deletedServers) > 0 {
		t.Error("expected no ArgoCD cluster deletion with cleanup=git")
	}
}

func TestRemoveCluster_NoneCleanup_OnlyRemovesEntry(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eu\n    labels: {}\n")

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
		Cleanup: "none",
		Yes:     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	// Values file should NOT be deleted with cleanup=none.
	if len(git.deletedFiles) > 0 {
		t.Error("expected no file deletions with cleanup=none")
	}
}

func TestRemoveCluster_RequiresConfirmation(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
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

func TestRemoveCluster_DryRun(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
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

func TestRemoveCluster_EmptyName(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Yes: true})
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestRemoveCluster_InvalidCleanup(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)

	_, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{
		Name:    "prod-eu",
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
