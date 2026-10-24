package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3-DRYRUN-FIXES call 1: nothing that cuts a cluster off happens before
// its pull request is merged. These tests pin both halves: a PR that waits
// touches nothing on the ArgoCD connection and carries the after-merge
// choices on the tracked PR; FinishRemoveAfterMerge then reaches the same
// end state the merged-at-once path reaches.

func removeFixture(name string, extra string) *mockGitProvider {
	git := newMockGitProvider()
	git.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: " + name + "\n" + extra + "    labels:\n      monitoring: \"enabled\"\n")
	git.files["configuration/addons-clusters-values/"+name+".yaml"] = []byte("clusterGlobalValues: {}\n")
	return git
}

func TestRemoveCluster_PRWaits_DoesNotTouchTheConnection(t *testing.T) {
	for _, cleanup := range []string{"all", "git", "none"} {
		t.Run(cleanup, func(t *testing.T) {
			argocd := newMockArgocd()
			argocd.existingClusters = []models.ArgocdCluster{{Name: "prod-eu", Server: "https://prod-eu.example.com"}}
			git := removeFixture("prod-eu", "")
			mgr := newMockArgoSecretManagerWithStrip()
			mgr.managedByLabel["prod-eu"] = "sharko"
			tracker := &stubPRTracker{}

			orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
			orch.SetArgoSecretManager(mgr, "")
			orch.SetPRTracker(tracker)

			result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "prod-eu", Cleanup: cleanup, Yes: true})
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if len(argocd.deletedServers) != 0 {
				t.Fatalf("the ArgoCD connection was deleted before the PR merged: %v", argocd.deletedServers)
			}
			if len(mgr.stripped) != 0 || mgr.managedByLabel["prod-eu"] != "sharko" {
				t.Fatalf("the ArgoCD Secret was changed before the PR merged: stripped %v", mgr.stripped)
			}
			if result.Status != "success" || result.Git == nil || result.Git.Merged {
				t.Fatalf("want success with an open PR, got status %q git %+v", result.Status, result.Git)
			}
			want := "Pull request opened, waiting for merge: " + result.Git.PRUrl +
				". Nothing has changed on cluster prod-eu yet — its ArgoCD connection stays as it is until the pull request is merged. Sharko finishes the removal after the merge."
			if result.Message != want {
				t.Fatalf("message:\n got %q\nwant %q", result.Message, want)
			}
			if got := result.CompletedSteps[len(result.CompletedSteps)-1]; got != "waiting_for_merge" {
				t.Fatalf("last step %q, want waiting_for_merge (%v)", got, result.CompletedSteps)
			}

			calls := tracker.snapshot()
			if len(calls) != 1 || calls[0].FollowUp == nil {
				t.Fatalf("the tracked PR must carry the after-merge choices, got %+v", calls)
			}
			fu := calls[0].FollowUp
			if fu.Cleanup != cleanup || fu.KeepConnection != (cleanup != "all") || fu.CredentialKey != "prod-eu" {
				t.Fatalf("follow-up %+v does not match cleanup %q", fu, cleanup)
			}
		})
	}
}

func TestRemoveCluster_PRWaits_UserManagedConnection_KeepIsRecorded(t *testing.T) {
	argocd := newMockArgocd()
	git := removeFixture("byo", "    connectionManagedBy: user\n    secretPath: clusters/byo-kubeconfig\n")
	mgr := newMockArgoSecretManagerWithStrip()
	mgr.managedByLabel["byo"] = "sharko"
	tracker := &stubPRTracker{}
	orch := New(nil, nil, argocd, git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")
	orch.SetPRTracker(tracker)

	if _, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "byo", Cleanup: "all", Yes: true}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(mgr.stripped) != 0 {
		t.Fatalf("label stripped before the PR merged: %v", mgr.stripped)
	}
	fu := tracker.snapshot()[0].FollowUp
	if fu == nil || !fu.KeepConnection || fu.CredentialKey != "clusters/byo-kubeconfig" {
		t.Fatalf("a user-managed connection must be recorded as kept, with the stored secretPath; got %+v", fu)
	}
}

func TestFinishRemoveAfterMerge_SameEndStateAsMergedAtOnce(t *testing.T) {
	// cleanup=all on a Sharko-owned connection: the Secret is deleted.
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{{Name: "prod-eu", Server: "https://prod-eu.example.com"}}
	mgr := newMockArgoSecretManagerWithStrip()
	mgr.managedByLabel["prod-eu"] = "sharko"
	orch := New(nil, nil, argocd, newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	res := orch.FinishRemoveAfterMerge(context.Background(), "prod-eu", ClusterFollowUp{Cleanup: "all"})
	if res.Status != "success" || len(argocd.deletedServers) != 1 || argocd.deletedServers[0] != "https://prod-eu.example.com" {
		t.Fatalf("cleanup=all after merge must delete the Sharko-owned connection; status %q deleted %v", res.Status, argocd.deletedServers)
	}

	// cleanup=git: the connection is kept and loses Sharko's label.
	argocd2 := newMockArgocd()
	argocd2.existingClusters = []models.ArgocdCluster{{Name: "keep-me", Server: "https://keep.example.com"}}
	mgr2 := newMockArgoSecretManagerWithStrip()
	mgr2.managedByLabel["keep-me"] = "sharko"
	orch2 := New(nil, nil, argocd2, newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch2.SetArgoSecretManager(mgr2, "")
	res2 := orch2.FinishRemoveAfterMerge(context.Background(), "keep-me", ClusterFollowUp{Cleanup: "git", KeepConnection: true})
	if len(argocd2.deletedServers) != 0 {
		t.Fatalf("a kept connection was deleted: %v", argocd2.deletedServers)
	}
	if len(mgr2.stripped) != 1 || mgr2.stripped[0] != "keep-me" {
		t.Fatalf("a kept connection must lose Sharko's label; stripped %v (steps %v)", mgr2.stripped, res2.CompletedSteps)
	}

	// Running it twice is a no-op the second time.
	res3 := orch2.FinishRemoveAfterMerge(context.Background(), "keep-me", ClusterFollowUp{Cleanup: "git", KeepConnection: true})
	if res3.Status != "success" || len(mgr2.stripped) != 1 {
		t.Fatalf("second run must be a no-op; status %q stripped %v", res3.Status, mgr2.stripped)
	}
}

// EP3-DRYRUN-FIXES call 4, the root: "remove, keep the connection" must take
// Sharko's label off for every kept connection — adopted, taken over, and a
// Sharko-registered one removed with cleanup git or none — not only for a
// connectionManagedBy: user one. Before, cleanup=git left the label on and
// the orphan sweep deleted the "kept" Secret after the merge.
func TestRemoveCluster_KeepTheConnection_StripsTheLabelForEveryCluster(t *testing.T) {
	cases := []struct {
		name        string
		cleanup     string
		annotations map[string]string
	}{
		{"registered by Sharko, cleanup git", "git", nil},
		{"registered by Sharko, cleanup none", "none", nil},
		{"adopted, cleanup git", "git", map[string]string{AnnotationAdopted: "true"}},
		{"taken over, cleanup none", "none", map[string]string{AnnotationAdopted: "true", "sharko.dev/taken-over-at": "2026-10-23T10:00:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argocd := newMockArgocd()
			argocd.existingClusters = []models.ArgocdCluster{{Name: "c1", Server: "https://c1.example.com"}}
			git := removeFixture("c1", "")
			mgr := newMockArgoSecretManagerWithStrip()
			mgr.managedByLabel["c1"] = "sharko"
			if tc.annotations != nil {
				mgr.annotations["c1"] = tc.annotations
			}
			orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
			orch.SetArgoSecretManager(mgr, "")

			result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "c1", Cleanup: tc.cleanup, Yes: true})
			if err != nil {
				t.Fatalf("remove: %v", err)
			}
			if len(argocd.deletedServers) != 0 {
				t.Fatalf("a kept connection was deleted: %v", argocd.deletedServers)
			}
			if len(mgr.stripped) != 1 || mgr.stripped[0] != "c1" {
				t.Fatalf("the kept connection still carries Sharko's label, so the orphan sweep would delete it; steps %v", result.CompletedSteps)
			}
		})
	}
}

func TestRemoveCluster_MergeFails_NothingChanged(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{{Name: "prod-eu", Server: "https://prod-eu.example.com"}}
	git := removeFixture("prod-eu", "")
	git.mergeErr = errors.New("the git host refused the merge")
	mgr := newMockArgoSecretManagerWithStrip()
	mgr.managedByLabel["prod-eu"] = "sharko"
	orch := New(nil, nil, argocd, git, autoMergeGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	result, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "prod-eu", Cleanup: "all", Yes: true})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if result.Status != "partial" || len(argocd.deletedServers) != 0 || len(mgr.stripped) != 0 {
		t.Fatalf("a failed merge must change nothing on the connection; status %q deleted %v stripped %v", result.Status, argocd.deletedServers, mgr.stripped)
	}
	if result.Git == nil {
		t.Fatal("the reply must carry the pull request")
	}
	wantMsg := "PR created but merge failed: " + result.Git.PRUrl + ". Nothing on the cluster's ArgoCD connection was changed — Sharko finishes the removal after the PR is merged."
	if result.Message != wantMsg {
		t.Fatalf("message:\n got %q\nwant %q", result.Message, wantMsg)
	}
}
