package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// EP3 dry run round S, S23: the adopt answers use plain words, not "adopted
// annotation" or "idempotent retry".

const wantAdoptMarkFailed = "The adoption pull request is merged, but Sharko could not mark the cluster's ArgoCD connection as adopted. Adopt the cluster again to finish. It is safe to repeat."

func TestAdoptMarkFailedMessage_PlainWords(t *testing.T) {
	if AdoptMarkFailedMessage != wantAdoptMarkFailed {
		t.Fatalf("AdoptMarkFailedMessage = %q", AdoptMarkFailedMessage)
	}
}

func legacyAdoptSetup() (*Orchestrator, *mockArgoSecretManager) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{{Name: "cluster-a", Server: "https://a.example.com"}}
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters:\n")
	asm := newMockArgoSecretManager()
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	return orch, asm
}

func TestAdopt_MergedButMarkFails_SaysAdoptAgain(t *testing.T) {
	orch, asm := legacyAdoptSetup()
	asm.annotationErr = errors.New("update refused")
	yes := true
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{Clusters: []string{"cluster-a"}, AutoMerge: &yes})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "partial" || cr.Message != wantAdoptMarkFailed {
		t.Fatalf("status %q message %q, want partial and %q", cr.Status, cr.Message, wantAdoptMarkFailed)
	}
}

func TestAdopt_PullRequestOpen_PlainWords(t *testing.T) {
	orch, _ := legacyAdoptSetup()
	no := false
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{Clusters: []string{"cluster-a"}, AutoMerge: &no})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	want := "Pull request #1 is open: https://github.com/example/repo/pull/1. When it is merged, Sharko marks cluster cluster-a as adopted."
	if cr.Message != want {
		t.Fatalf("message %q, want %q", cr.Message, want)
	}
}

func TestAdoptV4_MergedButMarkFails_SaysAdoptAgain(t *testing.T) {
	orch, _, asm := adoptPendingSetup(t)
	orch.gitops = autoMergeGitOps()
	asm.annotationErr = errors.New("update refused")
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "partial" || cr.Message != wantAdoptMarkFailed {
		t.Fatalf("status %q message %q, want partial and %q", cr.Status, cr.Message, wantAdoptMarkFailed)
	}
}

// A provider that leaves the number out: it comes from the address.
func TestRegister_AlreadyWaiting_NumberFromTheAddress(t *testing.T) {
	git := newMockGitProvider()
	git.prs = []*gitprovider.PullRequest{{
		Title: "sharko: register cluster prod-eu", SourceBranch: "sharko/register-cluster-prod-eu-abc123",
		URL: "https://github.com/example/repo/pull/42",
	}}
	creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
		"prod-eu": {Server: "https://k8s.example.com:6443", CAData: []byte("fake-ca"), Token: "fake-token"},
	}}
	orch := New(nil, creds, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	result, err := orch.RegisterCluster(context.Background(), RegisterClusterRequest{Name: "prod-eu", Addons: map[string]bool{"cert-manager": true}})
	if err != nil {
		t.Fatal(err)
	}
	want := "Registering prod-eu is already waiting in pull request #42: https://github.com/example/repo/pull/42. Sharko did not open a second one. Merge it to finish."
	if result.Message != want || result.Git == nil || result.Git.PRID != 42 {
		t.Fatalf("message %q git %+v, want %q and number 42", result.Message, result.Git, want)
	}
}
