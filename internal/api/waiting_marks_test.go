package api

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// EP3 dry run round R, R4: the waiting-marks pass finishes a mark whose
// pull request the PR tracker no longer has.

// statusGP answers a fixed pull request status and counts the calls.
type statusGP struct {
	*takeoverFakeGP
	mu     sync.Mutex
	status string
	err    error
	calls  int
}

func (g *statusGP) GetPullRequestStatus(context.Context, int) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	return g.status, g.err
}

func (g *statusGP) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func waitingMarksServer(t *testing.T, sec *corev1.Secret, status string, statusErr error) (*Server, *fake.Clientset, *statusGP, *prtracker.Tracker) {
	t.Helper()
	gp := &statusGP{takeoverFakeGP: newTakeoverFakeGP(), status: status, err: statusErr}
	srv, _, k8s := takeoverTestServer(t, gp, []*corev1.Secret{sec}, fakeAppSetReader{})
	tracker := prtracker.NewTracker(cmstore.NewStore(fake.NewSimpleClientset(), "default", "sharko-pending-prs"),
		func() prtracker.GitProvider { return nil }, func(audit.Entry) {})
	tracker.SetFinishFn(srv.FinishTrackedPR)
	srv.SetPRTracker(tracker)
	return srv, k8s, gp, tracker
}

func TestWaitingMarks_AdoptMergedWithoutTracker_Finishes(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, map[string]string{argosecrets.AnnotationAdoptPendingPR: "12"})
	srv, k8s, _, _ := waitingMarksServer(t, sec, "merged", nil)

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t), "")

	live := liveSecret(t, k8s, "prod-eu")
	if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue || live.Labels["env"] != "prod" {
		t.Fatalf("the merged adopt was not finished (or lost the old labels): %v", live.Labels)
	}
	if live.Annotations[orchestrator.AnnotationAdopted] != "true" {
		t.Fatalf("not marked adopted: %v", live.Annotations)
	}
	if _, still := live.Annotations[argosecrets.AnnotationAdoptPendingPR]; still {
		t.Fatalf("the waiting mark is still on: %v", live.Annotations)
	}
}

func TestWaitingMarks_TakeoverMergedWithoutTracker_KeepsTheOldLabels(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, map[string]string{argosecrets.AnnotationTakeoverPendingPR: "7"})
	srv, k8s, _, _ := waitingMarksServer(t, sec, "merged", nil)

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t), "")

	live := liveSecret(t, k8s, "prod-eu")
	if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Fatalf("the merged take over was not finished: %v", live.Labels)
	}
	if live.Labels["env"] != "prod" {
		t.Fatalf("the label choice was lost with the record, so the old labels must be kept: %v", live.Labels)
	}
	if _, still := live.Annotations[argosecrets.AnnotationTakeoverPendingPR]; still {
		t.Fatalf("the waiting mark is still on: %v", live.Annotations)
	}
}

func TestWaitingMarks_KeptConnectionClosedWithoutTracker_OnlyTheMarkComesOff(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "3"})
	srv, k8s, _, _ := waitingMarksServer(t, sec, "closed", nil)

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t), "")

	live := liveSecret(t, k8s, "prod-eu")
	if _, still := live.Annotations[argosecrets.AnnotationRemoveKeepPendingPR]; still {
		t.Fatalf("the keep mark is still on after its pull request closed: %v", live.Annotations)
	}
	if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Fatal("a closed removal must not change the label")
	}
}

func TestWaitingMarks_KeptConnectionMergedWithoutTracker_IsHandedBack(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "3"})
	srv, k8s, _, _ := waitingMarksServer(t, sec, "merged", nil)

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t), "")

	live := liveSecret(t, k8s, "prod-eu")
	if live.Labels[argosecrets.LabelManagedBy] != "" {
		t.Fatalf("the kept connection was not handed back: %v", live.Labels)
	}
	if _, still := live.Annotations[argosecrets.AnnotationRemoveKeepPendingPR]; still {
		t.Fatalf("the keep mark is still on: %v", live.Annotations)
	}
}

func TestWaitingMarks_LeavesAloneWhatItShould(t *testing.T) {
	for _, mark := range argosecrets.PendingPRMarks {
		t.Run("pull request still open/"+mark, func(t *testing.T) {
			sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue}, map[string]string{mark: "12"})
			srv, k8s, _, _ := waitingMarksServer(t, sec, "open", nil)
			srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t), "")
			if liveSecret(t, k8s, "prod-eu").Annotations[mark] != "12" {
				t.Fatal("an open pull request's mark was taken off")
			}
		})
	}
	t.Run("the tracker still has it", func(t *testing.T) {
		sec := brownfieldSecret("prod-eu", nil, map[string]string{argosecrets.AnnotationAdoptPendingPR: "12"})
		srv, k8s, gp, tracker := waitingMarksServer(t, sec, "merged", nil)
		if err := tracker.TrackPR(context.Background(), prtracker.PRInfo{PRID: 12, Cluster: "prod-eu", Operation: prtracker.OpAdoptCluster, LastStatus: "open"}); err != nil {
			t.Fatal(err)
		}
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
		if gp.callCount() != 0 {
			t.Fatal("asked Git about a pull request the tracker still finishes itself")
		}
		if liveSecret(t, k8s, "prod-eu").Labels[argosecrets.LabelManagedBy] != "" {
			t.Fatal("finished a pull request the tracker still has")
		}
	})
	t.Run("mark is still pending", func(t *testing.T) {
		sec := brownfieldSecret("prod-eu", nil, map[string]string{argosecrets.AnnotationTakeoverPendingPR: "pending"})
		srv, k8s, gp, _ := waitingMarksServer(t, sec, "merged", nil)
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
		if gp.callCount() != 0 {
			t.Fatal("asked Git about a mark that has no pull request number yet")
		}
	})
}

// EP3 dry run round S, S10: a mark's pull request is tracked again, so the
// tracker's one retry rule finishes it. An open one is asked about once,
// not on every pass.
func TestWaitingMarks_OpenPRIsTrackedOnce_RoundS(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "3"})
	srv, k8s, gp, tracker := waitingMarksServer(t, sec, "open", nil)
	for i := 0; i < 5; i++ {
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
	}
	if got := gp.callCount(); got != 1 {
		t.Fatalf("asked Git %d times about an open pull request, want 1: it must be tracked after the first pass", got)
	}
	pr, _ := tracker.GetPR(context.Background(), 3)
	if pr == nil || pr.LastStatus != "open" || pr.Operation != prtracker.OpRemoveCluster || pr.FollowUp == nil || !pr.FollowUp.KeepConnection {
		t.Fatalf("the mark's pull request is not tracked as a removal that keeps the connection: %+v", pr)
	}
}

// S10: a mark's pull request that Git says is gone is handled as closed.
// For an adopt that means the mark comes off and nothing is adopted.
func TestWaitingMarks_GonePRIsHandledAsClosed_RoundS(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, map[string]string{argosecrets.AnnotationAdoptPendingPR: "12"})
	srv, k8s, gp, _ := waitingMarksServer(t, sec, "", fmt.Errorf("get pull request #12: %w", gitprovider.ErrPullRequestNotFound))
	srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
	live := liveSecret(t, k8s, "prod-eu")
	if _, still := live.Annotations[argosecrets.AnnotationAdoptPendingPR]; still {
		t.Fatalf("the mark of a pull request Git says is gone is still on: %v", live.Annotations)
	}
	if live.Labels[argosecrets.LabelManagedBy] != "" {
		t.Fatalf("a gone adopt pull request adopted the connection: %v", live.Labels)
	}
}

// S10: a finish that keeps failing is given up once by the tracker; the
// pass does not start a second round of tries.
func TestWaitingMarks_GivenUpIsNotStartedAgain_RoundS(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "3"})
	srv, k8s, gp, tracker := waitingMarksServer(t, sec, "merged", nil)
	if err := tracker.TrackPR(context.Background(), prtracker.PRInfo{PRID: 3, Cluster: "prod-eu", Operation: prtracker.OpRemoveCluster,
		LastStatus: "merged", GaveUp: true, FollowUp: &prtracker.ClusterFollowUp{KeepConnection: true}}); err != nil {
		t.Fatal(err)
	}
	srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
	if gp.callCount() != 0 {
		t.Fatal("the pass asked Git about a pull request the tracker gave up on")
	}
	if !models.PendingPRMarkNames(liveSecret(t, k8s, "prod-eu").Annotations[argosecrets.AnnotationRemoveKeepPendingPR], 3) {
		t.Fatal("the protecting mark must stay on after the tracker gave up")
	}
}

// S8: a take over mark carries the label choice. "Do not keep the old
// labels" is honoured when the tracker no longer has the pull request.
func TestWaitingMarks_TakeoverUsesTheChoiceInTheMark_RoundS(t *testing.T) {
	no := false
	mark := models.PendingPRMark{PR: 7, KeepOldLabels: &no}.String()
	sec := brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, map[string]string{argosecrets.AnnotationTakeoverPendingPR: mark})
	srv, k8s, gp, tracker := waitingMarksServer(t, sec, "open", nil)
	srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
	pr, _ := tracker.GetPR(context.Background(), 7)
	if pr == nil || pr.FollowUp == nil || pr.FollowUp.Finish != prtracker.FinishTakeover || pr.FollowUp.PreserveLegacyLabels {
		t.Fatalf("the take over was rebuilt without the person's label choice: %+v", pr)
	}
}

// S11: a mark for another repository than the active Git connection is
// left alone: the same number there is another pull request.
func TestWaitingMarks_OtherRepoIsLeftAlone_RoundS(t *testing.T) {
	mark := models.PendingPRMark{PR: 12, Repo: "old-owner/old-repo"}.String()
	sec := brownfieldSecret("prod-eu", nil, map[string]string{argosecrets.AnnotationAdoptPendingPR: mark})
	srv, k8s, gp, tracker := waitingMarksServer(t, sec, "merged", nil)
	srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "o/r")
	if gp.callCount() != 0 {
		t.Fatal("asked the active repository about a pull request from another repository")
	}
	if pr, _ := tracker.GetPR(context.Background(), 12); pr != nil {
		t.Fatalf("tracked a pull request from another repository: %+v", pr)
	}
	if liveSecret(t, k8s, "prod-eu").Labels[argosecrets.LabelManagedBy] != "" {
		t.Fatal("finished a pull request from another repository")
	}
	// The same repository is acted on.
	same := models.PendingPRMark{PR: 12, Repo: "o/r"}.String()
	sec2 := brownfieldSecret("prod-eu", nil, map[string]string{argosecrets.AnnotationAdoptPendingPR: same})
	srv2, k8s2, gp2, _ := waitingMarksServer(t, sec2, "open", nil)
	srv2.finishWaitingMarks(context.Background(), k8s2, "argocd", gp2, "o/r")
	if gp2.callCount() != 1 {
		t.Fatal("a mark for the active repository was not looked at")
	}
}

// S20: a marked Secret that lost ArgoCD's cluster label is still found.
func TestWaitingMarks_FindsAMarkWithoutTheClusterLabel_RoundS(t *testing.T) {
	sec := brownfieldSecret("prod-eu", nil, map[string]string{argosecrets.AnnotationAdoptPendingPR: "12"})
	delete(sec.Labels, "argocd.argoproj.io/secret-type")
	srv, k8s, gp, _ := waitingMarksServer(t, sec, "open", nil)
	srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
	if gp.callCount() != 1 {
		t.Fatal("a marked Secret without the cluster label was never looked at")
	}
}

// S9: a keep mark that never got its pull request number is settled once
// it is old, and never by deleting the connection.
func TestWaitingMarks_OldPendingKeepMark_RoundS(t *testing.T) {
	old := models.PendingPRMark{Op: models.PendingMarkOpRemove, At: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}.String()
	fresh := models.PendingPRMark{Op: models.PendingMarkOpRemove, At: time.Now().UTC().Format(time.RFC3339)}.String()
	owned := map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue}
	key := argosecrets.AnnotationRemoveKeepPendingPR

	t.Run("a new one is left alone", func(t *testing.T) {
		sec := brownfieldSecret("prod-eu", owned, map[string]string{key: fresh})
		srv, k8s, gp, _ := waitingMarksServer(t, sec, "open", nil)
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
		live := liveSecret(t, k8s, "prod-eu")
		if live.Annotations[key] != fresh || live.Labels[argosecrets.LabelManagedBy] == "" {
			t.Fatalf("a keep mark still being written was changed: %v %v", live.Annotations, live.Labels)
		}
	})
	t.Run("the tracker has the removal: the mark gets its number", func(t *testing.T) {
		sec := brownfieldSecret("prod-eu", owned, map[string]string{key: old})
		srv, k8s, gp, tracker := waitingMarksServer(t, sec, "open", nil)
		if err := tracker.TrackPR(context.Background(), prtracker.PRInfo{PRID: 21, Cluster: "prod-eu", Operation: prtracker.OpRemoveCluster,
			LastStatus: "open", PRUrl: "https://github.example.invalid/o/r/pull/21",
			FollowUp: &prtracker.ClusterFollowUp{KeepConnection: true}}); err != nil {
			t.Fatal(err)
		}
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
		got, _ := models.ParsePendingPRMark(liveSecret(t, k8s, "prod-eu").Annotations[key])
		if got.PR != 21 || got.Repo != "o/r" || got.Op != models.PendingMarkOpRemove {
			t.Fatalf("keep mark = %+v, want it to name pull request 21 in o/r", got)
		}
	})
	t.Run("the cluster is still in Git: the mark comes off", func(t *testing.T) {
		sec := brownfieldSecret("other-cluster", owned, map[string]string{key: old})
		srv, k8s, gp, _ := waitingMarksServer(t, sec, "open", nil)
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
		live := liveSecret(t, k8s, "other-cluster")
		if _, still := live.Annotations[key]; still {
			t.Fatalf("an old keep mark with no removal is still on: %v", live.Annotations)
		}
		if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
			t.Fatal("a cluster still in Git lost Sharko's label")
		}
	})
	t.Run("the cluster is gone from Git: the connection is handed back", func(t *testing.T) {
		sec := brownfieldSecret("prod-eu", owned, map[string]string{key: "pending"})
		srv, k8s, gp, _ := waitingMarksServer(t, sec, "open", nil)
		// An older mark with no time: the pass waits from when it first saw it.
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
		if liveSecret(t, k8s, "prod-eu").Annotations[key] != "pending" {
			t.Fatal("an older pending mark was settled the first time it was seen")
		}
		srv.waitingMarks.seen["argocd/prod-eu"] = time.Now().Add(-time.Hour)
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
		live := liveSecret(t, k8s, "prod-eu")
		if _, still := live.Annotations[key]; still || live.Labels[argosecrets.LabelManagedBy] != "" {
			t.Fatalf("the kept connection was not handed back: %v %v", live.Annotations, live.Labels)
		}
	})
}

// mustGit returns the Git connection the test server uses.
func (s *Server) mustGit(t *testing.T) *statusGP {
	t.Helper()
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		t.Fatal(err)
	}
	return gp.(*statusGP)
}

// S10: a removal that keeps the connection, whose pull request Git says is
// gone, and whose cluster is gone from Git, did merge: the connection is
// handed back. Taking only the mark off would leave a Sharko-owned
// connection with nothing in Git, which the sweep deletes.
func TestWaitingMarks_GoneKeptRemovalWithClusterGone_IsHandedBack_RoundS(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "3"})
	srv, k8s, gp, _ := waitingMarksServer(t, sec, "", fmt.Errorf("get pull request #3: %w", gitprovider.ErrPullRequestNotFound))
	srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp, "")
	live := liveSecret(t, k8s, "prod-eu")
	if live.Labels[argosecrets.LabelManagedBy] != "" {
		t.Fatalf("the kept connection still carries Sharko's label, so the sweep would delete it: %v", live.Labels)
	}
	if _, still := live.Annotations[argosecrets.AnnotationRemoveKeepPendingPR]; still {
		t.Fatalf("the keep mark is still on: %v", live.Annotations)
	}
}
