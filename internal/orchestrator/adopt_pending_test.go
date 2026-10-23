package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// An adopt whose pull request is not merged yet changes nothing on ArgoCD's
// connection except the waiting mark, and says truthfully what happens next
// (episode 3 dry run, design call 2).
func adoptPendingSetup(t *testing.T) (*Orchestrator, *mockGitProvider, *mockArgoSecretManager) {
	t.Helper()
	git := adoptV4Git()
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{{Name: "prod-eu", Server: "https://prod-eu.example.com"}}
	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{"prod-eu": {Found: true, Server: "https://prod-eu.example.com"}}
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())
	return orch, git, asm
}

func TestAdoptV4_OpenPR_OnlyPutsTheWaitingMark(t *testing.T) {
	orch, _, asm := adoptPendingSetup(t)
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "success" || cr.Git == nil || cr.Git.PRID == 0 {
		t.Fatalf("result = %+v", cr)
	}
	if len(asm.takenOver) != 0 {
		t.Fatalf("ownership must wait for the merge, but TakeOverClusterSecret ran for %v", asm.takenOver)
	}
	if asm.annotations["prod-eu"][AnnotationAdopted] != "" {
		t.Fatal("the adopted mark must wait for the merge")
	}
	if got := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; got != "1" {
		t.Fatalf("waiting mark = %q, want the pull request number 1", got)
	}
	want := "Pull request #1 is open: https://github.com/example/repo/pull/1. When it is merged, Sharko takes ownership of the ArgoCD connection and marks it adopted. If it is closed without merging, Sharko changes nothing on the connection."
	if cr.Message != want {
		t.Fatalf("message:\n%s\nwant:\n%s", cr.Message, want)
	}
	if strings.Contains(cr.Message, "will be set after the PR is merged") {
		t.Fatal("the old promise is back")
	}
}

// Round R, R4: the tracked pull request finishes the adopt, so a waiting
// mark that cannot be written no longer makes the adopt partial, and the
// old "run adopt again before you merge" advice is gone.
func TestAdoptV4_OpenPR_MarkFailsStillWaitsForTheMerge(t *testing.T) {
	orch, _, asm := adoptPendingSetup(t)
	asm.annotationErr = errors.New("secret update refused")
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "success" || cr.Merged {
		t.Fatalf("status = %q merged = %v, want success and not merged", cr.Status, cr.Merged)
	}
	want := "Pull request #1 is open: https://github.com/example/repo/pull/1. When it is merged, Sharko takes ownership of the ArgoCD connection and marks it adopted. If it is closed without merging, Sharko changes nothing on the connection."
	if cr.Message != want {
		t.Fatalf("message:\n%s\nwant:\n%s", cr.Message, want)
	}
	if strings.Contains(cr.Message, "Run adopt again before you merge it") || strings.Contains(cr.Message, "would not finish the adopt") {
		t.Fatalf("the old advice is back: %q", cr.Message)
	}
}

func TestAdoptV4_ExistingOpenPR_PutsTheMarkBackAndOpensNoSecond(t *testing.T) {
	orch, git, asm := adoptPendingSetup(t)
	git.prs = append(git.prs, &gitprovider.PullRequest{
		ID: 7, Title: "sharko: adopt cluster prod-eu", SourceBranch: "sharko/adopt-prod-eu",
		URL: "https://github.com/example/repo/pull/7",
	})
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if len(git.prs) != 1 {
		t.Fatalf("a second pull request was opened: %d", len(git.prs))
	}
	if got := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; got != "7" {
		t.Fatalf("waiting mark = %q, want 7", got)
	}
	want := "Adopting prod-eu is already waiting in pull request #7: https://github.com/example/repo/pull/7. Sharko did not open a second one. Merge it to finish."
	if cr.Message != want {
		t.Fatalf("message:\n%s\nwant:\n%s", cr.Message, want)
	}
}
