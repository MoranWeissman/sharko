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

// TestUnadoptCluster_LegacyAnnotation_StillRecognised (V2-cleanup-59): a
// cluster adopted BEFORE the sharko.dev group rename carries only the old
// sharko.sharko.io/adopted annotation on its live ArgoCD Secret. Unadopt
// must keep recognising it — otherwise a pre-rename adopted cluster could
// never be unadopted.
func TestUnadoptCluster_LegacyAnnotation_StillRecognised(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: cluster-a\n    labels: {}\n")

	asm := newMockArgoSecretManager()
	// ONLY the pre-rename key present.
	asm.annotations["cluster-a"] = map[string]string{AnnotationAdoptedLegacy: "true"}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("unexpected error for legacy-annotated cluster: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(asm.unadopted) != 1 || asm.unadopted[0] != "cluster-a" {
		t.Errorf("expected unadopt call for cluster-a, got: %v", asm.unadopted)
	}
}

// TestUnadoptCluster_DoubledPrefixLegacyAnnotation_StillRecognised
// (V2-cleanup-60.5 L10): a cluster adopted during the short window when
// "sharko.sharko.dev/adopted" (the doubled-prefix V2-cleanup-59 spelling)
// was canonical carries only that key. Unadopt must keep recognising it —
// read-all-three, not read-both.
func TestUnadoptCluster_DoubledPrefixLegacyAnnotation_StillRecognised(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: cluster-a\n    labels: {}\n")

	asm := newMockArgoSecretManager()
	// ONLY the short-lived doubled-prefix key present.
	asm.annotations["cluster-a"] = map[string]string{AnnotationAdoptedDoubledPrefixLegacy: "true"}

	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	result, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatalf("unexpected error for doubled-prefix-annotated cluster: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if len(asm.unadopted) != 1 || asm.unadopted[0] != "cluster-a" {
		t.Errorf("expected unadopt call for cluster-a, got: %v", asm.unadopted)
	}
}

func TestUnadoptCluster_EmptyName(t *testing.T) {
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
	_, err := orch.UnadoptCluster(context.Background(), "", UnadoptClusterRequest{Yes: true})
	if err == nil {
		t.Fatal("expected error for empty cluster name")
	}
}
