package orchestrator

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// EP3 dry run round R: R1 (a kept connection is marked before the PR opens,
// and a lost PR record fails the removal in plain words), R8 (the delete
// goes by the Secret's own name) and R13 (the after-merge step uses the
// credentials router, and un-adopt run again opens no empty PR).

// mockArgoSecretManagerR adds the round R optional capabilities.
type mockArgoSecretManagerR struct {
	*mockArgoSecretManagerWithStrip
	released   []string
	releasedPR []int
	releaseErr error
	deleted    []string
	setErrFor  map[string]error // annotation key -> error from SetAnnotation
}

func newMockArgoSecretManagerR() *mockArgoSecretManagerR {
	return &mockArgoSecretManagerR{mockArgoSecretManagerWithStrip: newMockArgoSecretManagerWithStrip(), setErrFor: map[string]error{}}
}

func (m *mockArgoSecretManagerR) SetAnnotation(ctx context.Context, name, key, value string) error {
	if err := m.setErrFor[key]; err != nil {
		return err
	}
	return m.mockArgoSecretManager.SetAnnotation(ctx, name, key, value)
}

func (m *mockArgoSecretManagerR) ReleaseKeptConnection(_ context.Context, name string, prID int) (bool, error) {
	if m.releaseErr != nil {
		return false, m.releaseErr
	}
	m.releasedPR = append(m.releasedPR, prID)
	if cur := m.annotations[name][AnnotationRemoveKeepPendingPR]; (prID > 0 && models.PendingPRMarkMatches(cur, strconv.Itoa(prID))) || (prID == 0 && models.PendingPRMarkMatches(cur, "pending")) {
		delete(m.annotations[name], AnnotationRemoveKeepPendingPR)
	}
	if m.managedByLabel[name] != "sharko" {
		return false, nil
	}
	delete(m.managedByLabel, name)
	m.released = append(m.released, name)
	return true, nil
}

func (m *mockArgoSecretManagerR) DeleteClusterSecret(_ context.Context, name string) error {
	m.deleted = append(m.deleted, name)
	return nil
}

func (m *mockArgoSecretManagerR) RemoveAnnotationIfValue(_ context.Context, name, key, value string) (bool, error) {
	if m.annotations[name] == nil {
		return false, nil
	}
	if cur, ok := m.annotations[name][key]; !ok || !models.PendingPRMarkMatches(cur, value) {
		return false, nil
	}
	delete(m.annotations[name], key)
	return true, nil
}

func (m *mockArgoSecretManagerR) owned(name string) {
	m.managedByLabel[name] = "sharko"
	if m.secretDetails == nil {
		m.secretDetails = map[string]ClusterSecretDetail{}
	}
	m.secretDetails[name] = ClusterSecretDetail{Found: true, Name: "cluster-" + name + "-k8s", ManagedBy: "sharko"}
}

type failingPRTracker struct{}

func (failingPRTracker) TrackPR(context.Context, TrackedPR) error {
	return errors.New("configmap write conflict")
}

func TestRemoveCluster_KeptConnection_IsMarkedWithThePRNumber(t *testing.T) {
	git := removeFixture("keep-me", "")
	mgr := newMockArgoSecretManagerR()
	mgr.owned("keep-me")
	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")
	orch.SetPRTracker(&stubPRTracker{})

	res, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "keep-me", Cleanup: "git", Yes: true})
	if err != nil || res.Status != "success" || res.Git == nil {
		t.Fatalf("remove: %+v, %v", res, err)
	}
	if got := mgr.annotations["keep-me"][AnnotationRemoveKeepPendingPR]; !models.PendingPRMarkNames(got, 1) {
		t.Fatalf("a kept connection must carry the remove-keep mark with the PR number while the PR waits; got %q", got)
	}
	if mgr.managedByLabel["keep-me"] != "sharko" {
		t.Fatal("nothing else may change before the merge")
	}

	// After the merge the finish step takes the label and the mark off.
	fin := orch.FinishRemoveAfterMerge(context.Background(), "keep-me", ClusterFollowUp{Cleanup: "git", KeepConnection: true, PRID: 1})
	if fin.Status != "success" || len(mgr.released) != 1 {
		t.Fatalf("finish: status %q released %v", fin.Status, mgr.released)
	}
	if _, still := mgr.annotations["keep-me"][AnnotationRemoveKeepPendingPR]; still {
		t.Fatal("the remove-keep mark must come off when the connection is handed back")
	}
}

func TestRemoveCluster_KeepMarkFails_RemovalStopsInPlainWords(t *testing.T) {
	git := removeFixture("keep-me", "")
	mgr := newMockArgoSecretManagerR()
	mgr.owned("keep-me")
	mgr.setErrFor[AnnotationRemoveKeepPendingPR] = errors.New("forbidden")
	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	res, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "keep-me", Cleanup: "none", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "failed" || res.FailedStep != "protect_kept_connection" {
		t.Fatalf("status %q step %q, want failed/protect_kept_connection", res.Status, res.FailedStep)
	}
	want := "Sharko did not remove cluster keep-me. Before it opens the pull request, it puts a mark on the cluster's ArgoCD connection so nothing deletes that connection after the merge, and that step failed. Nothing has changed. Try again."
	if res.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", res.Message, want)
	}
	if len(git.prs) != 0 {
		t.Fatalf("no pull request may open when the connection could not be protected; got %d", len(git.prs))
	}
}

func TestRemoveCluster_PROpenFails_TakesThePendingMarkOff(t *testing.T) {
	git := removeFixture("keep-me", "")
	git.prErr = errors.New("rate limited")
	mgr := newMockArgoSecretManagerR()
	mgr.owned("keep-me")
	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	res, _ := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "keep-me", Cleanup: "git", Yes: true})
	if res == nil || res.Status != "failed" {
		t.Fatalf("want failed, got %+v", res)
	}
	if v, still := mgr.annotations["keep-me"][AnnotationRemoveKeepPendingPR]; still {
		t.Fatalf("no pull request opened, so the pending mark must come off; it is %q", v)
	}
}

func TestRemoveCluster_PRNotRecorded_FailsInPlainWords(t *testing.T) {
	git := removeFixture("keep-me", "")
	mgr := newMockArgoSecretManagerR()
	mgr.owned("keep-me")
	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")
	orch.SetPRTracker(failingPRTracker{})

	res, err := orch.RemoveCluster(context.Background(), RemoveClusterRequest{Name: "keep-me", Cleanup: "git", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "partial" || res.FailedStep != "track_pr" {
		t.Fatalf("a lost PR record must fail the removal: status %q step %q", res.Status, res.FailedStep)
	}
	want := "Sharko opened pull request https://github.com/example/repo/pull/1 but could not record it, so it could not finish the removal after the merge. Nothing has changed on the cluster's ArgoCD connection. Close that pull request without merging it, then try again."
	if res.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", res.Message, want)
	}
	// The mark stays on, so the connection is safe whatever happens to the PR.
	if got := mgr.annotations["keep-me"][AnnotationRemoveKeepPendingPR]; !models.PendingPRMarkNames(got, 1) {
		t.Fatalf("the keep mark must stay on with the PR number, got %q", got)
	}
}

// R8: the delete goes by the Secret the lookup found, never by the server
// address through ArgoCD (two connections can share one address).
func TestFinishRemove_DeletesTheSecretByName_NotByServer(t *testing.T) {
	argocd := newMockArgocd()
	argocd.existingClusters = []models.ArgocdCluster{{Name: "prod-eu", Server: "https://shared.example.com"}}
	mgr := newMockArgoSecretManagerR()
	mgr.owned("prod-eu")
	orch := New(nil, nil, argocd, newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")

	res := orch.FinishRemoveAfterMerge(context.Background(), "prod-eu", ClusterFollowUp{Cleanup: "all"})
	if res.Status != "success" {
		t.Fatalf("status %q (%s)", res.Status, res.Error)
	}
	if len(argocd.deletedServers) != 0 {
		t.Fatalf("deleted through ArgoCD by server address: %v", argocd.deletedServers)
	}
	if len(mgr.deleted) != 1 || mgr.deleted[0] != "prod-eu" {
		t.Fatalf("want the Secret found for prod-eu deleted, got %v", mgr.deleted)
	}

	// A connection without Sharko's label is never deleted.
	mgr2 := newMockArgoSecretManagerR()
	mgr2.secretDetails = map[string]ClusterSecretDetail{"byo": {Found: true, Name: "byo", ManagedBy: ""}}
	orch2 := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch2.SetArgoSecretManager(mgr2, "")
	res2 := orch2.FinishRemoveAfterMerge(context.Background(), "byo", ClusterFollowUp{Cleanup: "all"})
	if len(mgr2.deleted) != 0 || res2.CompletedSteps[len(res2.CompletedSteps)-1] != "skip_argocd_secret_not_sharko_labeled" {
		t.Fatalf("an unlabelled Secret was deleted or not skipped: deleted %v steps %v", mgr2.deleted, res2.CompletedSteps)
	}
}

// R1: a failed hand-back makes the finish step fail, so it is tried again.
func TestFinishRemove_ReleaseFails_IsPartial(t *testing.T) {
	mgr := newMockArgoSecretManagerR()
	mgr.owned("keep-me")
	mgr.releaseErr = errors.New("conflict")
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(mgr, "")
	res := orch.FinishRemoveAfterMerge(context.Background(), "keep-me", ClusterFollowUp{Cleanup: "git", KeepConnection: true})
	if res.Status != "partial" || res.FailedStep != "strip_sharko_ownership_label" {
		t.Fatalf("status %q step %q, want partial/strip_sharko_ownership_label", res.Status, res.FailedStep)
	}
	want := "Cluster keep-me was removed from Git, but Sharko could not yet hand its ArgoCD connection back. The connection is kept and nothing deletes it. Sharko tries again by itself."
	if res.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", res.Message, want)
	}
}

// R13: the after-merge clean up reaches a cluster whose credentials live
// only in ArgoCD, through the credentials router, even with no secrets
// backend configured.
func TestFinishRemove_UsesTheCredentialsRouter(t *testing.T) {
	asked := false
	router := &providers.ClusterCredsRouter{
		ArgoCDReaderFn: func() (providers.ClusterCredentialsProvider, error) {
			asked = true
			return nil, errors.New("no ArgoCD reader in this test")
		},
	}
	orch := New(nil, nil, newMockArgocd(), newMockGitProvider(), defaultGitOps(), defaultPaths(), nil)
	orch.SetCredsRouter(router)
	orch.SetSecretManagement(map[string]AddonSecretDefinition{"dd": {AddonName: "datadog", SecretName: "dd-keys", Namespace: "datadog"}}, nil, roundSFakeClientFn)
	orch.SetArgoSecretManager(newMockArgoSecretManagerR(), "")
	orch.FinishRemoveAfterMerge(context.Background(), "inline", ClusterFollowUp{Cleanup: "all", CredentialsSource: models.CredsSourceInlineKubeconfig})
	if !asked {
		t.Fatal("the after-merge clean up did not ask the credentials router, so an inline-registered cluster keeps its addon secrets")
	}
}

// R13: un-adopt run again after its pull request merged (and its ArgoCD
// side failed) does the ArgoCD side and opens no empty pull request.
func TestUnadoptCluster_NothingLeftInGit_OpensNoPR(t *testing.T) {
	git := newMockGitProvider()
	git.files["managed-clusters.yaml"] = []byte("clusters: []\n")
	git.files["bootstrap/v4-engine.yaml"] = []byte("engine: v4\n")
	asm := newMockArgoSecretManager()
	asm.annotations["gone"] = map[string]string{AnnotationAdopted: "true"}
	orch := New(nil, nil, newMockArgocd(), git, defaultGitOps(), defaultPaths(), nil)
	orch.SetArgoSecretManager(asm, "")

	res, err := orch.UnadoptCluster(context.Background(), "gone", UnadoptClusterRequest{Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(git.prs) != 0 {
		t.Fatalf("un-adopt opened %d pull request(s) with nothing to change", len(git.prs))
	}
	if res.Status != "success" || len(asm.unadopted) != 1 {
		t.Fatalf("status %q unadopted %v", res.Status, asm.unadopted)
	}
	want := "Cluster gone was already gone from Git, so no pull request was needed. Sharko took its marks off the ArgoCD cluster Secret."
	if res.Message != want {
		t.Fatalf("message:\n got %q\nwant %q", res.Message, want)
	}
}
