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

const registerWaitingWant = "Cluster prod-eu is already waiting in pull request #12: https://github.com/example/repo/pull/12. That pull request registers it. Merge it or close it first, then try again."

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

func TestAdoptV4_OpenTakeoverPR_CountsAsAlreadyWaiting(t *testing.T) {
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
	want := "Adopting prod-eu is already waiting in pull request #9: https://github.com/example/repo/pull/9. Sharko did not open a second one. Merge it to finish."
	if cr.Status != "success" || cr.Merged || cr.Message != want {
		t.Fatalf("status %q merged %v message %q", cr.Status, cr.Merged, cr.Message)
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
	seen := map[string]bool{}
	for _, w := range got {
		if !strings.HasPrefix(w.ID, "foreign-owner-") || seen[w.ID] {
			t.Fatalf("ids = %+v, want three different foreign-owner-<hash> ids", got)
		}
		seen[w.ID] = true
	}
}

// EP3 dry run round S, S17: an id comes from what the warning says, so the
// same warning keeps its id in any order and on every call, and a new one
// never takes the id of one already ticked.
func TestAdoptWarningIDs_FromTheirContent_NotTheirOrder(t *testing.T) {
	a := foreignOwnerAdoptWarnings([]string{"one", "two"})
	b := foreignOwnerAdoptWarnings([]string{"three", "two", "one"})
	ids := map[string]string{}
	for _, w := range b {
		ids[w.Message] = w.ID
	}
	for _, w := range a {
		if ids[w.Message] != w.ID {
			t.Fatalf("%q has id %q in one call and %q in another", w.Message, w.ID, ids[w.Message])
		}
	}
	if ids["three"] == a[0].ID || ids["three"] == a[1].ID {
		t.Fatal("a new warning took the id of one already ticked")
	}
	if got := foreignOwnerAdoptWarnings([]string{"one"})[0].ID; got != a[0].ID {
		t.Fatalf("one warning alone has id %q, with another %q", got, a[0].ID)
	}

	// Joining two lists: an exact copy is dropped, and two different
	// warnings with the same id each get one of their own, in any order.
	x, y := AdoptWarning{ID: "secret-owner", Message: "x"}, AdoptWarning{ID: "secret-owner", Message: "y"}
	m1 := uniqueAdoptWarningIDs([]AdoptWarning{x, y, x})
	m2 := uniqueAdoptWarningIDs([]AdoptWarning{y, x})
	if len(m1) != 2 || m1[0].ID == m1[1].ID || m1[0].ID == "secret-owner" {
		t.Fatalf("joined = %+v", m1)
	}
	for _, w := range m2 {
		for _, v := range m1 {
			if v.Message == w.Message && v.ID != w.ID {
				t.Fatalf("%q has id %q in one order and %q in the other", w.Message, v.ID, w.ID)
			}
		}
	}
	// One warning of a kind keeps the plain id the take over check gives it.
	if one := uniqueAdoptWarningIDs([]AdoptWarning{x}); one[0].ID != "secret-owner" {
		t.Fatalf("a single warning lost its id: %+v", one)
	}
}
