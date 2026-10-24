package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

// EP3 dry run round S, S1: a cleanup=all finish that could not reach the
// cluster to remove the addon secrets is a failure that retries, not a
// success, and it does not delete the ArgoCD connection yet, so the next
// try can still reach the cluster.
func TestFinishRemove_CleanupAll_NoCredentials_RetriesAndKeepsTheConnection_RoundS(t *testing.T) {
	router := &providers.ClusterCredsRouter{
		ArgoCDReaderFn: func() (providers.ClusterCredentialsProvider, error) {
			return nil, errors.New("secrets backend is down")
		},
	}
	mgr := newMockArgoSecretManagerR()
	mgr.owned("going")
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{{Name: "going", Server: "https://going.example.com"}}
	orch := New(nil, nil, argocd, newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetCredsRouter(router)
	orch.SetSecretManagement(map[string]AddonSecretDefinition{"dd": {AddonName: "datadog", SecretName: "dd-keys", Namespace: "datadog"}}, nil, roundSFakeClientFn)
	orch.SetArgoSecretManager(mgr, "")

	fin := orch.FinishRemoveAfterMerge(context.Background(), "going", ClusterFollowUp{Cleanup: "all", CredentialsSource: models.CredsSourceInlineKubeconfig, PRID: 4})
	if fin.Status != "partial" || fin.FailedStep != "delete_remote_secrets" {
		t.Fatalf("status %q step %q, want partial/delete_remote_secrets", fin.Status, fin.FailedStep)
	}
	want := "Cluster going was removed from Git, but Sharko could not yet remove the addon secrets it put on the cluster. Its ArgoCD connection stays until that is done. Sharko tries again by itself."
	if fin.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", fin.Message, want)
	}
	if len(mgr.deleted) != 0 || len(argocd.deletedServers) != 0 {
		t.Fatalf("the connection was deleted before the addon secrets were removed: %v %v", mgr.deleted, argocd.deletedServers)
	}
}

func roundSFakeClientFn(_ []byte) (kubernetes.Interface, error) {
	return k8sfake.NewSimpleClientset(), nil
}

// S1 too: an addon secret that could not be deleted (not one Sharko left
// alone because it did not create it) makes the finish retry, and the
// connection stays.
func TestFinishRemove_CleanupAll_AddonSecretDeleteFails_Retries_RoundS(t *testing.T) {
	remote := k8sfake.NewSimpleClientset()
	remote.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("the cluster API is not answering")
	})
	mgr := newMockArgoSecretManagerR()
	mgr.owned("going")
	creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{"going": {Raw: []byte("kubeconfig")}}}
	orch := New(nil, creds, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetSecretManagement(map[string]AddonSecretDefinition{"dd": {AddonName: "datadog", SecretName: "dd-keys", Namespace: "datadog"}}, nil,
		func([]byte) (kubernetes.Interface, error) { return remote, nil })
	orch.SetArgoSecretManager(mgr, "")

	fin := orch.FinishRemoveAfterMerge(context.Background(), "going", ClusterFollowUp{Cleanup: "all", PRID: 4})
	if fin.Status != "partial" || fin.FailedStep != "delete_remote_secrets" {
		t.Fatalf("status %q step %q, want partial/delete_remote_secrets", fin.Status, fin.FailedStep)
	}
	if len(mgr.deleted) != 0 {
		t.Fatalf("the connection was deleted before the addon secrets were removed: %v", mgr.deleted)
	}
}
