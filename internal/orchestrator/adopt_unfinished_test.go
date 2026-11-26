package orchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// Walk 9, finding 1 and finding 2. An adopt that was cut off (the person closed
// the tab while the page said "Adopting...") must not leave a cluster that
// adopt, un-adopt and the page all refuse to touch; and adopting a cluster that
// is already adopted must say so and open nothing.

// adoptedOnceOrchestrator runs one full adopt (auto-merge) so Git holds the
// two files an adopt writes, then returns a fresh orchestrator on the SAME Git
// with the connection in the state the test wants.
func adoptedOnceOrchestrator(t *testing.T, connection ClusterSecretDetail) (*Orchestrator, *mockGitProvider, *mockArgoSecretManager) {
	t.Helper()
	first, git, _ := adoptPendingSetup(t)
	first.gitops = autoMergeGitOps()
	if _, err := first.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := git.files[V4ManagedClustersPath]; !ok {
		t.Fatal("setup: the first adopt wrote nothing")
	}
	prsBefore := len(git.prs)
	_ = prsBefore

	asm := newMockArgoSecretManager()
	asm.secretDetails = map[string]ClusterSecretDetail{"prod-eu": connection}
	orch := New(nil, nil, first.argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")
	orch.SetApplicationSetManager(newFakeAppSetManager())
	return orch, git, asm
}

func TestAdoptV4_AlreadyAdopted_SaysSoAndOpensNothing(t *testing.T) {
	orch, git, asm := adoptedOnceOrchestrator(t, ClusterSecretDetail{
		Found: true, Server: "https://prod-eu.example.com", ManagedBy: "sharko",
	})
	prs := len(git.prs)

	for _, dry := range []bool{true, false} {
		result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
			AcknowledgedFindings: []string{"secret-owner"},
			Clusters:             []string{"prod-eu"},
			DryRun:               dry,
		})
		if err != nil {
			t.Fatal(err)
		}
		cr := result.Results[0]
		if cr.Status != "success" || !cr.AlreadyDone {
			t.Fatalf("dry=%v: status %q already_done %v, want success and already done (%+v)", dry, cr.Status, cr.AlreadyDone, cr)
		}
		want := "prod-eu is already adopted. Nothing to do, and no pull request was opened."
		if cr.Message != want {
			t.Fatalf("dry=%v: message:\n%s\nwant:\n%s", dry, cr.Message, want)
		}
		if cr.DryRun != nil {
			t.Fatalf("dry=%v: an already adopted cluster must not claim it would write anything: %+v", dry, cr.DryRun)
		}
	}
	if len(git.prs) != prs {
		t.Fatalf("a pull request was opened for a cluster that is already adopted (%d -> %d)", prs, len(git.prs))
	}
	if len(asm.takenOver) != 0 {
		t.Fatalf("the connection was touched: %v", asm.takenOver)
	}
}

// The earlier try merged its pull request but was cut off before Sharko took
// over the connection. Adopt must finish it, not refuse with "a different
// cluster with the same name".
func TestAdoptV4_CutOffAfterMerge_FinishesWithoutAPullRequest(t *testing.T) {
	orch, git, asm := adoptedOnceOrchestrator(t, ClusterSecretDetail{
		Found: true, Server: "https://prod-eu.example.com", // not owned by Sharko
	})
	prs := len(git.prs)

	dry, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
		DryRun:               true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantDry := "Sharko already has prod-eu in its list, from an earlier adopt that did not finish. Adopting it again takes over its ArgoCD connection. No pull request is opened."
	if d := dry.Results[0]; d.Status != "success" || d.Message != wantDry {
		t.Fatalf("dry run: status %q message %q (error %q), want success and:\n%s", d.Status, d.Message, d.Error, wantDry)
	}
	if len(asm.takenOver) != 0 {
		t.Fatalf("a dry run changed the connection: %v", asm.takenOver)
	}

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "success" || !cr.Merged {
		t.Fatalf("status %q merged %v error %q, want a finished adopt", cr.Status, cr.Merged, cr.Error)
	}
	want := "Sharko already had prod-eu in its list, from an earlier adopt that did not finish. It has now taken over the ArgoCD connection and marked the cluster adopted. No pull request was needed."
	if cr.Message != want {
		t.Fatalf("message:\n%s\nwant:\n%s", cr.Message, want)
	}
	if len(asm.takenOver) != 1 || asm.takenOver[0] != "prod-eu" {
		t.Fatalf("the connection was not taken over: %v", asm.takenOver)
	}
	if asm.annotations["prod-eu"][AnnotationAdopted] != "true" {
		t.Fatal("the cluster was not marked adopted")
	}
	if len(git.prs) != prs {
		t.Fatalf("a pull request was opened though Git already had everything (%d -> %d)", prs, len(git.prs))
	}
	for _, old := range []string{"name-collision", "different cluster", "Rename one of them"} {
		if strings.Contains(cr.Message+cr.Error, old) {
			t.Fatalf("the false name clash is back: %q", cr.Message+cr.Error)
		}
	}
}

// A cluster Sharko registered itself (credentials recorded) with the same name
// as a connection Sharko does not own IS a clash, and still stops.
func TestAdoptV4_RegisteredByShark_SameName_StillABlockedClash(t *testing.T) {
	orch, git, asm := adoptedOnceOrchestrator(t, ClusterSecretDetail{
		Found: true, Server: "https://prod-eu.example.com",
	})
	git.files[V4ManagedClustersPath] = []byte("clusters:\n  - name: prod-eu\n    secretPath: prod-eu\n    credsSource: secret-kubeconfig\n")

	result, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cr := result.Results[0]
	if cr.Status != "failed" || !strings.Contains(cr.Error, "not ready to take over yet") {
		t.Fatalf("status %q error %q, want the preflight to stop a real name clash", cr.Status, cr.Error)
	}
	if len(asm.takenOver) != 0 {
		t.Fatalf("a real name clash changed the connection: %v", asm.takenOver)
	}
}

// ---- the pull request that was opened but never recorded ----

type lookupTracker struct {
	tracked map[int]bool
	calls   []TrackedPR
}

func (l *lookupTracker) TrackPR(_ context.Context, p TrackedPR) error {
	l.calls = append(l.calls, p)
	return nil
}
func (l *lookupTracker) IsTracked(_ context.Context, id int) (bool, error) { return l.tracked[id], nil }

func TestAdoptV4_OpenPullRequestNotRecorded_IsRecordedAgain(t *testing.T) {
	orch, git, _ := adoptPendingSetup(t)
	git.prs = append(git.prs, &gitprovider.PullRequest{
		ID: 7, Title: "sharko: adopt cluster prod-eu", SourceBranch: "sharko/adopt-cluster-prod-eu-ab12",
		URL: "https://github.com/example/repo/pull/7",
	})
	tr := &lookupTracker{tracked: map[int]bool{}}
	orch.SetPRTracker(tr)

	if _, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(tr.calls) != 1 {
		t.Fatalf("TrackPR calls = %d, want 1: the open pull request was lost and must be recorded again", len(tr.calls))
	}
	got := tr.calls[0]
	if got.PRID != 7 || got.Cluster != "prod-eu" || got.Operation != "adopt-cluster" ||
		got.FollowUp == nil || got.FollowUp.Finish != FinishAdopt || got.LastStatus != "open" {
		t.Fatalf("recorded wrongly: %+v", got)
	}
}

func TestAdoptV4_OpenPullRequestAlreadyRecorded_IsLeftAlone(t *testing.T) {
	orch, git, _ := adoptPendingSetup(t)
	git.prs = append(git.prs, &gitprovider.PullRequest{
		ID: 7, Title: "sharko: adopt cluster prod-eu", URL: "https://github.com/example/repo/pull/7",
	})
	tr := &lookupTracker{tracked: map[int]bool{7: true}}
	orch.SetPRTracker(tr)

	if _, err := orch.AdoptClusters(context.Background(), AdoptClustersRequest{
		AcknowledgedFindings: []string{"secret-owner"},
		Clusters:             []string{"prod-eu"},
	}); err != nil {
		t.Fatal(err)
	}
	if len(tr.calls) != 0 {
		t.Fatalf("a pull request that is already recorded was written over: %+v", tr.calls)
	}
}

// ---- the stray branch ----

func TestCommitChanges_FailedTry_DeletesItsBranch(t *testing.T) {
	cases := map[string]func(g *mockGitProvider){
		"files could not be written":  func(g *mockGitProvider) { g.createErr = context.Canceled },
		"pull request could not open": func(g *mockGitProvider) { g.prErr = context.Canceled },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			git := adoptV4Git()
			breakIt(git)
			orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
			_, err := orch.commitChangesWithMeta(context.Background(),
				map[string][]byte{"a.yaml": []byte("a: 1\n")}, nil, "adopt cluster x", orch.prMeta(nil, "", "", "", ""))
			if err == nil {
				t.Fatal("expected the try to fail")
			}
			if len(git.branches) != 1 {
				t.Fatalf("branches made = %v", git.branches)
			}
			if len(git.deletedBranches) != 1 || git.deletedBranches[0] != git.branches[0] {
				t.Fatalf("the branch %v was left behind; deleted = %v", git.branches, git.deletedBranches)
			}
		})
	}
}
