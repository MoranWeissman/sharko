package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// EP3 dry run round S, S14: no answer ever says "PR #0". A provider that
// leaves the number out still gives the address, so the number comes from
// there; when neither has it the sentence says "a pull request".

// noIDGit is a Git provider that never fills in the pull request number.
// stripURL also takes the number off the end of the address.
type noIDGit struct {
	*mockGitProvider
	stripURL bool
}

func (g noIDGit) strip(pr gitprovider.PullRequest) gitprovider.PullRequest {
	pr.ID = 0
	if g.stripURL {
		pr.URL = "https://git.example.com/example/repo/pulls/open"
	}
	return pr
}

func (g noIDGit) CreatePullRequest(ctx context.Context, title, body, head, base string) (*gitprovider.PullRequest, error) {
	pr, err := g.mockGitProvider.CreatePullRequest(ctx, title, body, head, base)
	if err != nil {
		return nil, err
	}
	out := g.strip(*pr)
	return &out, nil
}

func (g noIDGit) ListPullRequests(ctx context.Context, state string) ([]gitprovider.PullRequest, error) {
	prs, err := g.mockGitProvider.ListPullRequests(ctx, state)
	for i := range prs {
		prs[i] = g.strip(prs[i])
	}
	return prs, err
}

func TestPRNumber_FromTheIDOrTheAddress(t *testing.T) {
	cases := []struct {
		id   int
		url  string
		want int
	}{
		{12, "https://github.com/o/r/pull/99", 12},
		{0, "https://github.com/o/r/pull/12", 12},
		{0, "https://gitea.example.com/o/r/pulls/32/", 32},
		{0, "https://github.com/o/r/pull/12/files", 0},
		{0, "", 0},
		{-3, "https://github.com/o/r/pull/abc", 0},
	}
	for _, c := range cases {
		if got := PRNumber(c.id, c.url); got != c.want {
			t.Errorf("PRNumber(%d, %q) = %d, want %d", c.id, c.url, got, c.want)
		}
	}
	if got := PullRequestPhrase(12); got != "pull request #12" {
		t.Errorf("PullRequestPhrase(12) = %q", got)
	}
	if got := PullRequestPhrase(0); got != "a pull request" {
		t.Errorf("PullRequestPhrase(0) = %q", got)
	}
}

func TestRegisterWaiting_NumberFromTheAddress(t *testing.T) {
	git := v4Git()
	openRegisterPR(git)
	orch := New(nil, defaultCreds(), newMockArgocd(), noIDGit{mockGitProvider: git}, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	var waiting *ClusterPRWaitingError
	if !errors.As(err, &waiting) || err.Error() != registerWaitingWant {
		t.Fatalf("err = %v, want %q", err, registerWaitingWant)
	}
}

func TestRegisterWaiting_NoNumberAtAll_SaysAPullRequest(t *testing.T) {
	git := v4Git()
	openRegisterPR(git)
	orch := New(nil, defaultCreds(), newMockArgocd(), noIDGit{mockGitProvider: git, stripURL: true}, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	want := "Cluster prod-eu is already waiting in a pull request: https://git.example.com/example/repo/pulls/open. That pull request registers it. Merge it or close it first, then try again."
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestAdoptV4_ProviderWithoutNumbers_NeverSaysPR0(t *testing.T) {
	orch, git, asm := adoptPendingSetup(t)
	orch.git = noIDGit{mockGitProvider: git}

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	want := "Pull request #1 is open: https://github.com/example/repo/pull/1. When it is merged, Sharko takes ownership of the ArgoCD connection and marks it adopted. If it is closed without merging, Sharko changes nothing on the connection."
	if cr.Message != want || cr.Git == nil || cr.Git.PRID != 1 {
		t.Fatalf("message %q git %+v, want %q and number 1", cr.Message, cr.Git, want)
	}
	if v := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; v != "1" {
		t.Fatalf("waiting mark = %q, want the number read from the address", v)
	}

}

func TestAdoptV4_OpenPRWithoutNumber_AlreadyWaitingUsesTheAddress(t *testing.T) {
	orch, git, asm := adoptPendingSetup(t)
	git.prs = append(git.prs, &gitprovider.PullRequest{
		Title: "sharko: adopt cluster prod-eu", URL: "https://github.com/example/repo/pull/7",
	})
	again, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantAgain := "Adopting prod-eu is already waiting in pull request #7: https://github.com/example/repo/pull/7. Sharko did not open a second one. Merge it to finish."
	if again.Results[0].Message != wantAgain || again.Results[0].Git == nil || again.Results[0].Git.PRID != 7 {
		t.Fatalf("result %+v, want %q", again.Results[0], wantAgain)
	}
	if v := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; v != "7" {
		t.Fatalf("waiting mark = %q, want 7", v)
	}
}

func TestAdoptV4_NoNumberAtAll_SaysAPullRequest(t *testing.T) {
	orch, git, _ := adoptPendingSetup(t)
	orch.git = noIDGit{mockGitProvider: git, stripURL: true}

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := result.Results[0].Message
	want := "A pull request is open: https://git.example.com/example/repo/pulls/open. When it is merged, Sharko takes ownership of the ArgoCD connection and marks it adopted. If it is closed without merging, Sharko changes nothing on the connection."
	if msg != want {
		t.Fatalf("message %q, want %q", msg, want)
	}
	if strings.Contains(msg, "#0") {
		t.Fatalf("message says #0: %q", msg)
	}
}
