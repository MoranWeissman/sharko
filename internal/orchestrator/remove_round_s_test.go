package orchestrator

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3 dry run round S, S4: a keep mark belongs to one pull request. A new
// removal never writes over another removal's mark; a hand-back only takes
// its own pull request's mark off; a cleanup=all finish never deletes a
// connection another pull request still has to finish with.

func TestRemoveCluster_AnotherRemovalsKeepMark_Refuses_RoundS(t *testing.T) {
	git := removeFixture("keep-me", "")
	mgr := newMockArgoSecretManagerR()
	mgr.owned("keep-me")
	other := models.PendingPRMark{PR: 9, Op: models.PendingMarkOpRemove}.String()
	mgr.annotations["keep-me"] = map[string]string{AnnotationRemoveKeepPendingPR: other}
	tracker := &stubPRTracker{}
	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")
	orch.SetPRTracker(tracker)

	res, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "keep-me", Cleanup: "git", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || res.FailedStep != "other_pr_open" || res.Git != nil {
		t.Fatalf("status %q step %q git %+v, want failed/other_pr_open and no PR", res.Status, res.FailedStep, res.Git)
	}
	want := "Sharko did not change cluster keep-me: another removal or un-adopt of it is still waiting in pull request #9. Merge or close that pull request first, then try again."
	if res.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", res.Message, want)
	}
	if got := mgr.annotations["keep-me"][AnnotationRemoveKeepPendingPR]; got != other {
		t.Fatalf("another removal's keep mark was written over: %q", got)
	}
	if got := OtherRemovalWaitingMessage("x", 0); got != "Sharko did not change cluster x: another removal or un-adopt of it is still waiting in a pull request. Merge or close that pull request first, then try again." {
		t.Fatalf("unknown number message: %q", got)
	}
}

func TestFinishRemove_HandBack_OnlyTakesItsOwnKeepMark_RoundS(t *testing.T) {
	mgr := newMockArgoSecretManagerR()
	mgr.owned("keep-me")
	mgr.annotations["keep-me"] = map[string]string{AnnotationRemoveKeepPendingPR: models.PendingPRMark{PR: 9}.String()}
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	fin := orch.FinishRemoveAfterMerge(context.Background(), "keep-me", ClusterFollowUp{Cleanup: "git", KeepConnection: true, PRID: 4})
	if fin.Status != "success" {
		t.Fatalf("finish: %+v", fin)
	}
	if len(mgr.releasedPR) != 1 || mgr.releasedPR[0] != 4 {
		t.Fatalf("the hand-back did not say which pull request it is for: %v", mgr.releasedPR)
	}
	if !models.PendingPRMarkNames(mgr.annotations["keep-me"][AnnotationRemoveKeepPendingPR], 9) {
		t.Fatalf("the hand-back for PR 4 took off PR 9's keep mark: %v", mgr.annotations["keep-me"])
	}
}

func TestFinishRemove_CleanupAll_LeavesAConnectionAnotherPRWaitsOn_RoundS(t *testing.T) {
	for _, mark := range waitingMarks {
		t.Run(mark, func(t *testing.T) {
			mgr := newMockArgoSecretManagerR()
			mgr.owned("busy")
			mgr.annotations["busy"] = map[string]string{mark: models.PendingPRMark{PR: 9}.String()}
			argocd := newMockArgocd()
			argocd.existingClusters = []models.ArgocdCluster{{Name: "busy", Server: "https://busy.example.com"}}
			orch := New(nil, nil, argocd, newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
			orch.SetArgoSecretManager(mgr, "")

			fin := orch.FinishRemoveAfterMerge(context.Background(), "busy", ClusterFollowUp{Cleanup: "all", PRID: 4})
			if len(mgr.deleted) != 0 || len(argocd.deletedServers) != 0 {
				t.Fatalf("a connection another pull request waits on was deleted: %v %v", mgr.deleted, argocd.deletedServers)
			}
			want := "Cluster busy was removed from Sharko, but its ArgoCD connection was left in place: another pull request still has to finish with it. Delete it yourself later if you no longer want ArgoCD connected to this cluster."
			if fin.Message != want {
				t.Fatalf("message:\n got %q\nwant %q", fin.Message, want)
			}
		})
	}
}
