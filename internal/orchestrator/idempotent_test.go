package orchestrator

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

func TestRegisterCluster_IdempotentRetry_ExistingPR(t *testing.T) {
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
			},
		},
	}

	// Simulate an existing open PR for this cluster registration.
	git.prs = []*gitprovider.PullRequest{
		{
			ID:           42,
			Title:        "sharko: register cluster prod-eu",
			SourceBranch: "sharko/register-cluster-prod-eu-abc123",
			URL:          "https://github.com/example/repo/pull/42",
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"cert-manager": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	if result.Git == nil {
		t.Fatal("expected git result")
	}
	if result.Git.PRID != 42 {
		t.Errorf("expected PR ID 42, got %d", result.Git.PRID)
	}
	// EP3 dry run round S, S23: plain words, pinned exactly.
	const wantReg = "Registering prod-eu is already waiting in pull request #42: https://github.com/example/repo/pull/42. Sharko did not open a second one. Merge it to finish."
	if result.Message != wantReg {
		t.Errorf("message = %q, want %q", result.Message, wantReg)
	}
	// Should NOT have created any new PRs (only the pre-existing one).
	if len(git.prs) != 1 {
		t.Errorf("expected exactly 1 PR (pre-existing), got %d", len(git.prs))
	}
}

func TestRegisterCluster_IdempotentRetry_NoPR(t *testing.T) {
	// When there's no existing PR, the registration should proceed normally.
	argocd := newMockArgocd()
	git := newMockGitProvider()
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://k8s.example.com:6443",
				CAData: []byte("fake-ca"),
				Token:  "fake-token",
			},
		},
	}

	orch := New(nil, creds, argocd, git, autoMergeGitOps(), defaultPaths(), nil)

	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{
		Name:   "prod-eu",
		Addons: map[string]bool{"cert-manager": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s (error: %s)", result.Status, result.Error)
	}
	// Should have created a new PR.
	if len(git.prs) == 0 {
		t.Error("expected a PR to be created")
	}
}

func TestAdoptClusters_IdempotentRetry_ExistingPR(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{
		{Name: "cluster-a", Server: "https://a.example.com"},
	}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")

	// Simulate an existing open PR for this cluster adoption.
	git.prs = []*gitprovider.PullRequest{
		{
			ID:           99,
			Title:        "sharko: adopt cluster cluster-a",
			SourceBranch: "sharko/adopt-cluster-cluster-a-def456",
			URL:          "https://github.com/example/repo/pull/99",
		},
	}

	asm := newMockArgoSecretManager()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	autoMerge := true
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		Clusters:  []string{"cluster-a"},
		AutoMerge: &autoMerge,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result.Results))
	}

	cr := result.Results[0]
	if cr.Status != "success" {
		t.Errorf("expected success, got %s", cr.Status)
	}
	if cr.Git == nil || cr.Git.PRID != 99 {
		t.Error("expected existing PR info in result")
	}
	const wantAdopt = "Adopting cluster-a is already waiting in pull request #99: https://github.com/example/repo/pull/99. Sharko did not open a second one. Merge it to finish."
	if cr.Message != wantAdopt {
		t.Errorf("message = %q, want %q", cr.Message, wantAdopt)
	}
	// Should NOT have created any new PRs.
	if len(git.prs) != 1 {
		t.Errorf("expected exactly 1 PR (pre-existing), got %d", len(git.prs))
	}
}
