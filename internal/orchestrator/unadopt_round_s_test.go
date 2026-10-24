package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3 dry run round S, S6: un-adopt puts a keep mark on the connection
// before its pull request opens, so a lost tracker record still has
// something that finishes it; the finish takes the mark off; and on a v3
// repo a failed Git read is an error, never "nothing left in Git".

const unadoptV3Registry = "clusters:\n  - name: cluster-a\n    labels: {}\n"

func unadoptRoundSFixture() (*mockGitProvider, *mockArgoSecretManagerR) {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(unadoptV3Registry)
	mgr := newMockArgoSecretManagerR()
	mgr.owned("cluster-a")
	mgr.annotations["cluster-a"] = map[string]string{AnnotationAdopted: "true"}
	return git, mgr
}

func TestUnadoptCluster_V3_ReadFails_IsAnError_RoundS(t *testing.T) {
	for _, path := range []string{"configuration/managed-clusters.yaml", "configuration/addons-clusters-values/cluster-a.yaml"} {
		t.Run(path, func(t *testing.T) {
			base, mgr := unadoptRoundSFixture()
			git := &failReadGit{mockGitProvider: base, path: path, err: errGitDown}
			orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
			orch.SetArgoSecretManager(mgr, "")

			res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
			if err == nil || !errors.Is(err, errGitDown) {
				t.Fatalf("a failed Git read must be an error, got %+v, %v", res, err)
			}
			if len(mgr.unadopted) != 0 {
				t.Fatalf("the connection was changed after a failed Git read: %v", mgr.unadopted)
			}
			if len(base.prs) != 0 {
				t.Fatalf("a pull request was opened after a failed Git read: %d", len(base.prs))
			}
		})
	}
}

func TestUnadoptCluster_V3_UnreadableClusterList_IsAnError_RoundS(t *testing.T) {
	git, mgr := unadoptRoundSFixture()
	git.files["configuration/managed-clusters.yaml"] = []byte("clusters: [this is: not: yaml\n")
	orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")
	if res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true}); err == nil {
		t.Fatalf("a cluster list Sharko cannot read must be an error, got %+v", res)
	}
	if len(mgr.unadopted) != 0 || len(git.prs) != 0 {
		t.Fatalf("something changed: unadopted %v, prs %d", mgr.unadopted, len(git.prs))
	}
}

func TestUnadoptCluster_KeepMark_RoundS(t *testing.T) {
	t.Run("the mark goes on before tracking and then names the pull request", func(t *testing.T) {
		git, mgr := unadoptRoundSFixture()
		orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(mgr, "")
		seen := ""
		orch.SetPRTracker(prTrackerFunc(func(_ context.Context, _ TrackedPR) error {
			seen = mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR]
			return nil
		}))
		res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
		if err != nil || res.Status != "success" || res.Git == nil {
			t.Fatalf("unadopt: %+v, %v", res, err)
		}
		if m, ok := models.ParsePendingPRMark(seen); !ok || m.Op != models.PendingMarkOpUnadopt {
			t.Fatalf("the un-adopt keep mark must be on before the pull request is tracked, got %q", seen)
		}
		got := mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR]
		if m, _ := models.ParsePendingPRMark(got); !models.PendingPRMarkNames(got, res.Git.PRID) || m.Op != models.PendingMarkOpUnadopt {
			t.Fatalf("the keep mark must name the un-adopt pull request, got %q", got)
		}
		if len(mgr.unadopted) != 0 {
			t.Fatal("nothing else may change before the merge")
		}

		// After the merge the finish takes the mark off, so nothing
		// tracks this pull request again.
		if err := orch.FinishUnadoptAfterMerge(context.Background(), "cluster-a", ClusterFollowUp{KeepConnection: true, PRID: res.Git.PRID}); err != nil {
			t.Fatal(err)
		}
		if _, still := mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR]; still {
			t.Fatal("the finish must take the un-adopt keep mark off")
		}
	})

	t.Run("tracking fails: the mark still names the pull request", func(t *testing.T) {
		git, mgr := unadoptRoundSFixture()
		orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(mgr, "")
		orch.SetPRTracker(failingPRTracker{})
		res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
		if err != nil || res.Status != "partial" || res.Git == nil {
			t.Fatalf("unadopt: %+v, %v", res, err)
		}
		if got := mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR]; !models.PendingPRMarkNames(got, res.Git.PRID) {
			t.Fatalf("with tracking lost, the mark is what lets Sharko finish later; got %q", got)
		}
	})

	t.Run("merged at once: finished and the mark is off", func(t *testing.T) {
		git, mgr := unadoptRoundSFixture()
		orch := New(nil, nil, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(mgr, "")
		res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
		if err != nil || res.Status != "success" || len(mgr.unadopted) != 1 {
			t.Fatalf("unadopt: %+v, %v, unadopted %v", res, err, mgr.unadopted)
		}
		if got, still := mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR]; still {
			t.Fatalf("the keep mark must be off after the finish, got %q", got)
		}
	})

	t.Run("no pull request: the mark comes back off", func(t *testing.T) {
		git, mgr := unadoptRoundSFixture()
		git.prErr = errors.New("the git host said no")
		orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(mgr, "")
		res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
		if err != nil || res.Status != "failed" {
			t.Fatalf("unadopt: %+v, %v", res, err)
		}
		if got, still := mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR]; still {
			t.Fatalf("with no pull request the mark must come off, got %q", got)
		}
	})

	t.Run("the mark cannot go on: nothing happens, in plain words", func(t *testing.T) {
		git, mgr := unadoptRoundSFixture()
		mgr.setErrFor[AnnotationRemoveKeepPendingPR] = errors.New("secret update refused")
		orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(mgr, "")
		res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
		if err != nil {
			t.Fatal(err)
		}
		want := "Sharko did not un-adopt cluster cluster-a. Before it opens the pull request, it puts a mark on the cluster's ArgoCD connection so Sharko can finish the un-adopt after the merge, and that step failed. Nothing has changed. Try again."
		if res.Status != "failed" || res.Message != want || UnadoptKeepMarkFailedMessage("cluster-a") != want {
			t.Fatalf("got %q %q\nwant %q", res.Status, res.Message, want)
		}
		if len(git.prs) != 0 {
			t.Fatalf("a pull request was opened: %d", len(git.prs))
		}
	})

	t.Run("another removal waits: refused, its mark stays", func(t *testing.T) {
		git, mgr := unadoptRoundSFixture()
		other := keepMarkValue(models.PendingMarkOpRemove, 7, "https://example.invalid/pr/7")
		mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR] = other
		orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(mgr, "")
		res, err := orch.UnadoptCluster(context.Background(), "cluster-a", UnadoptClusterRequest{Yes: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "failed" || res.Message != OtherRemovalWaitingMessage("cluster-a", 7) || strings.Contains(res.Message, "#0") {
			t.Fatalf("got %q %q", res.Status, res.Message)
		}
		if len(git.prs) != 0 || mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR] != other {
			t.Fatalf("prs %d, mark %q", len(git.prs), mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR])
		}
	})

	t.Run("the finish leaves another pull request's mark alone", func(t *testing.T) {
		_, mgr := unadoptRoundSFixture()
		other := keepMarkValue(models.PendingMarkOpRemove, 9, "")
		mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR] = other
		orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
		orch.SetArgoSecretManager(mgr, "")
		if err := orch.FinishUnadoptAfterMerge(context.Background(), "cluster-a", ClusterFollowUp{KeepConnection: true, PRID: 3}); err != nil {
			t.Fatal(err)
		}
		if mgr.annotations["cluster-a"][AnnotationRemoveKeepPendingPR] != other {
			t.Fatal("the finish took off another pull request's mark")
		}
	})
}
