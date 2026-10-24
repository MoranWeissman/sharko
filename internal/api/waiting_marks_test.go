package api

import (
	"context"
	"errors"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
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
	srv.SetPRTracker(tracker)
	return srv, k8s, gp, tracker
}

func TestWaitingMarks_AdoptMergedWithoutTracker_Finishes(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, map[string]string{argosecrets.AnnotationAdoptPendingPR: "12"})
	srv, k8s, _, _ := waitingMarksServer(t, sec, "merged", nil)

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t))

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

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t))

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

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t))

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

	srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t))

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
			srv.finishWaitingMarks(context.Background(), k8s, "argocd", srv.mustGit(t))
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
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp)
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
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp)
		if gp.callCount() != 0 {
			t.Fatal("asked Git about a mark that has no pull request number yet")
		}
	})
}

// A pull request Git cannot find (or any read error) is tried up to the
// cap, then left alone with its mark on.
func TestWaitingMarks_GitCannotFindIt_StopsAfterTheCapAndKeepsTheMark(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "3"})
	srv, k8s, gp, _ := waitingMarksServer(t, sec, "", errors.New("pull request not found"))

	for i := 0; i < waitingMarkMaxTries+5; i++ {
		srv.finishWaitingMarks(context.Background(), k8s, "argocd", gp)
	}
	if got := gp.callCount(); got != waitingMarkMaxTries {
		t.Fatalf("asked Git %d times, want exactly %d", got, waitingMarkMaxTries)
	}
	if liveSecret(t, k8s, "prod-eu").Annotations[argosecrets.AnnotationRemoveKeepPendingPR] != "3" {
		t.Fatal("the protecting mark must stay on after giving up")
	}
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
