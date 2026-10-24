package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// EP3 dry run round R: R5 (take over saves its finish kind and waits for the
// merge), R4 (adopt saves its finish kind), R7 (an open register pull
// request is refused; a list that cannot be read stops the change) and R11
// (merged only when the adopt really finished; warning ids are unique).

// listFailGit is a repo whose open pull requests cannot be read.
type listFailGit struct{ *mockGitProvider }

func (listFailGit) ListPullRequests(context.Context, string) ([]gitprovider.PullRequest, error) {
	return nil, errors.New("rate limited")
}

func openRegisterPR(git *mockGitProvider) {
	git.prs = append(git.prs, &gitprovider.PullRequest{
		ID: 12, Title: "sharko: register cluster prod-eu", SourceBranch: "sharko/register-prod-eu",
		URL: "https://github.com/example/repo/pull/12",
	})
}

const registerWaitingWant = "Cluster prod-eu is already waiting in PR #12: https://github.com/example/repo/pull/12. That pull request registers it. Merge it or close it first, then try again."

const openPRsUnreadableWant = "Sharko could not read the open pull requests, so it cannot tell whether prod-eu is already waiting in one. Nothing has changed. Try again."

func TestTakeoverClusterGit_SavesTheFinishKindOnTheTrackedPR(t *testing.T) {
	git := v4Git()
	tr := &stubPRTracker{}
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetPRTracker(tr)

	if _, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true, PreserveLegacyLabels: false}); err != nil {
		t.Fatal(err)
	}
	calls := tr.snapshot()
	if len(calls) != 1 || calls[0].FollowUp == nil {
		t.Fatalf("the take over pull request must carry its finish step; tracked %+v", calls)
	}
	if calls[0].FollowUp.Finish != FinishTakeover || calls[0].FollowUp.PreserveLegacyLabels {
		t.Fatalf("follow-up = %+v, want a take over that drops the old labels as asked", *calls[0].FollowUp)
	}
}

func TestTakeoverClusterGit_OpenRegisterPR_IsRefused(t *testing.T) {
	git := v4Git()
	openRegisterPR(git)
	orch := New(nil, defaultCreds(), newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	var waiting *ClusterPRWaitingError
	if !errors.As(err, &waiting) || err.Error() != registerWaitingWant {
		t.Fatalf("err = %v, want %q", err, registerWaitingWant)
	}
	if len(git.prs) != 1 {
		t.Fatalf("a second pull request was opened: %d", len(git.prs))
	}
}

func TestTakeoverClusterGit_OpenPRsUnreadable_Stops(t *testing.T) {
	git := v4Git()
	orch := New(nil, defaultCreds(), newMockArgocd(), listFailGit{git}, defaultGitOps(), defaultPaths(), nil)

	_, err := orch.TakeoverClusterGit(context.Background(), TakeoverClusterRequest{Name: "prod-eu", Yes: true})
	if err == nil || err.Error() != openPRsUnreadableWant {
		t.Fatalf("err = %v, want %q", err, openPRsUnreadableWant)
	}
	if len(git.prs) != 0 {
		t.Fatalf("a pull request was opened although the open ones could not be read: %d", len(git.prs))
	}
}

func TestAdoptV4_SavesTheFinishKindOnTheTrackedPR(t *testing.T) {
	orch, _, _ := adoptPendingSetup(t)
	tr := &stubPRTracker{}
	orch.SetPRTracker(tr)
	if _, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	}); err != nil {
		t.Fatal(err)
	}
	calls := tr.snapshot()
	if len(calls) != 1 || calls[0].FollowUp == nil || calls[0].FollowUp.Finish != FinishAdopt {
		t.Fatalf("the adopt pull request must carry its finish kind; tracked %+v", calls)
	}
}

func TestAdoptV4_OpenRegisterPR_IsRefused(t *testing.T) {
	orch, git, _ := adoptPendingSetup(t)
	openRegisterPR(git)
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "failed" || cr.Message != registerWaitingWant {
		t.Fatalf("status %q message %q, want failed and %q", cr.Status, cr.Message, registerWaitingWant)
	}
	if len(git.prs) != 1 {
		t.Fatalf("a second pull request was opened: %d", len(git.prs))
	}
}

func TestAdoptV4_OpenPRsUnreadable_Stops(t *testing.T) {
	orch, git, _ := adoptPendingSetup(t)
	orch.git = listFailGit{git}
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "failed" || cr.Message != openPRsUnreadableWant {
		t.Fatalf("status %q message %q", cr.Status, cr.Message)
	}
	if len(git.prs) != 0 {
		t.Fatalf("a pull request was opened: %d", len(git.prs))
	}
}

// Round S, S5: an open take over pull request would finish as a take
// over, so the adopt refuses, in plain words, and changes nothing.
// (Round R counted it as this adopt's own pull request.)
func TestAdoptV4_OpenTakeoverPR_IsRefused_RoundS(t *testing.T) {
	orch, git, asm := adoptPendingSetup(t)
	git.prs = append(git.prs, &gitprovider.PullRequest{
		ID: 9, Title: "sharko: take over cluster prod-eu", URL: "https://github.com/example/repo/pull/9",
	})
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	want := "A pull request to take over prod-eu is already open. Merge or close it first."
	if cr.Status != "failed" || cr.Merged || cr.Message != want || cr.Error != want {
		t.Fatalf("status %q merged %v message %q", cr.Status, cr.Merged, cr.Message)
	}
	if cr.OtherPR == nil || cr.OtherPR.PRID != 9 || cr.OtherPR.PRUrl != "https://github.com/example/repo/pull/9" || cr.OtherPR.Kind != "take over" {
		t.Fatalf("other pull request = %+v", cr.OtherPR)
	}
	if strings.Contains(cr.Message, "Adopting prod-eu is already waiting") {
		t.Fatal("the take over pull request is called this adopt's own again")
	}
	if len(git.prs) != 1 {
		t.Fatalf("a second pull request was opened: %d", len(git.prs))
	}
	if v := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; v != "" {
		t.Fatalf("an adopt mark was put on for a take over pull request: %q", v)
	}
}

func TestAdoptV4_PRNotRecorded_IsPartialInPlainWords(t *testing.T) {
	orch, _, _ := adoptPendingSetup(t)
	orch.SetPRTracker(failingPRTracker{})
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	want := "Sharko opened pull request https://github.com/example/repo/pull/1 but could not record it, so it could not finish the adopt after the merge. Nothing has changed on the cluster's ArgoCD connection. Close that pull request without merging it, then try again."
	if cr.Status != "partial" || cr.Merged || cr.Message != want {
		t.Fatalf("status %q merged %v message %q", cr.Status, cr.Merged, cr.Message)
	}
}

// merged is true only when the pull request merged and the adopt finished.
func TestAdoptV4_Merged_OnlyWhenTheAdoptFinished(t *testing.T) {
	orch, _, asm := adoptPendingSetup(t)
	open, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil || open.Results[0].Merged {
		t.Fatalf("an open pull request reported merged: %+v %v", open.Results[0], err)
	}

	orch2, _, _ := adoptPendingSetup(t)
	orch2.gitops = autoMergeGitOps()
	done, err := orch2.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil || done.Results[0].Status != "success" || !done.Results[0].Merged {
		t.Fatalf("a merged and finished adopt must say merged: %+v %v", done.Results[0], err)
	}

	orch3, _, asm3 := adoptPendingSetup(t)
	orch3.gitops = autoMergeGitOps()
	asm3.annotationErr = errors.New("update refused")
	half, err := orch3.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil || half.Results[0].Status != "partial" || half.Results[0].Merged {
		t.Fatalf("a merged pull request whose adopt did not finish must not say merged: %+v %v", half.Results[0], err)
	}
	_ = asm
}

func TestUniqueAdoptWarningIDs_NoTwoTheSame(t *testing.T) {
	got := foreignOwnerAdoptWarnings([]string{"one", "two", "three"})
	ids := []string{got[0].ID, got[1].ID, got[2].ID}
	want := []string{"foreign-owner", "foreign-owner-2", "foreign-owner-3"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
	// A later warning already named "-2" does not collide.
	mixed := uniqueAdoptWarningIDs([]AdoptWarning{{ID: "x"}, {ID: "x-2"}, {ID: "x"}})
	if mixed[0].ID != "x" || mixed[1].ID != "x-2" || mixed[2].ID != "x-3" {
		t.Fatalf("ids = %s %s %s", mixed[0].ID, mixed[1].ID, mixed[2].ID)
	}
}
