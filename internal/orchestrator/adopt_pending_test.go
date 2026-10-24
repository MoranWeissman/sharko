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
	if got := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; !models.PendingPRMarkNames(got, 1) {
		t.Fatalf("waiting mark = %q, want one that names pull request 1", got)
	}
	want := "Pull request #1 is open: https://github.com/example/repo/pull/1. When it is merged, Sharko takes ownership of the ArgoCD connection and marks it adopted. If it is closed without merging, Sharko changes nothing on the connection."
	if cr.Message != want {
		t.Fatalf("message:\n%s\nwant:\n%s", cr.Message, want)
	}
	if strings.Contains(cr.Message, "will be set after the PR is merged") {
		t.Fatal("the old promise is back")
	}
}

// Round S, S7: a waiting mark that cannot go on is said in plain words.
// The pull request stays tracked, is not merged, and nothing else on the
// connection changes. (Round R made this a quiet log line; the old "run
// adopt again before you merge it" advice stays gone.)
func TestAdoptV4_OpenPR_MarkFails_IsPartialAndNotMerged_RoundS(t *testing.T) {
	orch, git, asm := adoptPendingSetup(t)
	tracker := &stubPRTracker{}
	orch.SetPRTracker(tracker)
	gitops := defaultGitOps()
	gitops.PRAutoMerge = true
	orch.gitops = gitops
	asm.annotationErr = errors.New("secret update refused")
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "partial" || cr.Merged || cr.Git == nil || cr.Git.Merged {
		t.Fatalf("status = %q merged = %v git = %+v, want partial and not merged", cr.Status, cr.Merged, cr.Git)
	}
	want := "Sharko opened pull request https://github.com/example/repo/pull/1, but could not mark prod-eu's ArgoCD connection as waiting for it. Nothing has changed on the connection. Run the adopt again to put the mark on, then merge the pull request."
	if cr.Message != want {
		t.Fatalf("message:\n%s\nwant:\n%s", cr.Message, want)
	}
	if git.mergeCalls != 0 {
		t.Fatalf("the pull request was merged %d times; it must wait for the mark", git.mergeCalls)
	}
	if got := tracker.snapshot(); len(got) != 1 || got[0].PRID != 1 {
		t.Fatalf("the pull request must still be tracked: %+v", got)
	}
	if len(asm.takenOver) != 0 {
		t.Fatalf("the connection changed: %v", asm.takenOver)
	}
	if strings.Contains(cr.Message, "Run adopt again before you merge it") || strings.Contains(cr.Message, "would not finish the adopt") {
		t.Fatalf("the old advice is back: %q", cr.Message)
	}
}

// Round S, S7: the waiting mark is on before the pull request is tracked,
// so a stop between the two leaves a mark that names the pull request.
func TestAdoptV4_MarkGoesOnBeforeTracking_RoundS(t *testing.T) {
	orch, _, asm := adoptPendingSetup(t)
	seen := ""
	orch.SetPRTracker(prTrackerFunc(func(_ context.Context, p TrackedPR) error {
		seen = asm.annotations["prod-eu"][AnnotationAdoptPendingPR]
		return nil
	}))
	if _, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	}); err != nil {
		t.Fatal(err)
	}
	m, ok := models.ParsePendingPRMark(seen)
	if !ok || m.PR != 1 || m.Repo != "example/repo" || m.At == "" || m.KeepOldLabels != nil {
		t.Fatalf("mark when the pull request was tracked = %q", seen)
	}
}

// prTrackerFunc is a PRTracker made of one function.
type prTrackerFunc func(ctx context.Context, p TrackedPR) error

func (f prTrackerFunc) TrackPR(ctx context.Context, p TrackedPR) error { return f(ctx, p) }

// Round S, S7: a re-run that finds its own open pull request and cannot
// put the mark back says so.
func TestAdoptV4_ExistingOpenPR_MarkFails_IsPartial_RoundS(t *testing.T) {
	orch, git, asm := adoptPendingSetup(t)
	git.prs = append(git.prs, &gitprovider.PullRequest{
		ID: 7, Title: "sharko: adopt cluster prod-eu", URL: "https://github.com/example/repo/pull/7",
	})
	asm.annotationErr = errors.New("secret update refused")
	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"}, Clusters: []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	want := "Sharko opened pull request https://github.com/example/repo/pull/7, but could not mark prod-eu's ArgoCD connection as waiting for it. Nothing has changed on the connection. Run the adopt again to put the mark on, then merge the pull request."
	if cr.Status != "partial" || cr.Message != want {
		t.Fatalf("status %q message %q", cr.Status, cr.Message)
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
	if got := asm.annotations["prod-eu"][AnnotationAdoptPendingPR]; !models.PendingPRMarkNames(got, 7) {
		t.Fatalf("waiting mark = %q, want one that names 7", got)
	}
	want := "Adopting prod-eu is already waiting in pull request #7: https://github.com/example/repo/pull/7. Sharko did not open a second one. Merge it to finish."
	if cr.Message != want {
		t.Fatalf("message:\n%s\nwant:\n%s", cr.Message, want)
	}
}
