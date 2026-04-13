package orchestrator

import (
	"context"
	"strings"
	"testing"
)

func TestUnadoptCluster_Success(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: cluster-a\n    labels: {}\n")

	asm := newMockArgoSecretManager()
	asm.annotations["cluster-a"] = map[string]string{AnnotationAdopted: "true"}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Git == nil {
		t.Error("expected git result")
	}

	// Check that unadopt was called on the ArgoCD secret manager.
	if len(asm.unadopted) != 1 || asm.unadopted[0] != "cluster-a" {
		t.Errorf("expected unadopt call for cluster-a, got: %v", asm.unadopted)
	}
}

func TestUnadoptCluster_NotAdopted(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()

	asm := newMockArgoSecretManager()
	// No adopted annotation set.

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	_, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
	if err == nil {
		t.Fatal("expected error for non-adopted cluster")
	}
	if !strings.Contains(err.Error(), "was not adopted") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnadoptCluster_DryRun(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()

	asm := newMockArgoSecretManager()
	asm.annotations["cluster-a"] = map[string]string{AnnotationAdopted: "true"}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{DryRun: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DryRun == nil {
		t.Fatal("expected dry_run result")
	}
	if result.DryRun.PRTitle == "" {
		t.Error("expected PR title in dry run")
	}
	// No PRs or unadopt calls should have happened.
	if len(git.prs) > 0 {
		t.Error("expected no PRs in dry-run mode")
	}
	if len(asm.unadopted) > 0 {
		t.Error("expected no unadopt calls in dry-run mode")
	}
}

func TestUnadoptCluster_EmptyName(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	_, err := orch.UnadoptCluster(context.Background(), "", UnadoptClusterRequest{Yes: true})
	if err == nil {
		t.Fatal("expected error for empty cluster name")
	}
}
