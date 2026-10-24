package api

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// EP3 dry run round S, S8 (rest): a finish that runs twice stays harmless
// and writes one audit entry, not two. A try that failed writes none: the
// tracker tries again, and when it gives up it writes the one give-up
// entry. A JSON mark is read as the pull request it names.

func countFinishAuditEvents(srv *Server, event string) int {
	n := 0
	for _, e := range srv.auditLog.List(100) {
		if e.Event == event {
			n++
		}
	}
	return n
}

func joinWaitingSecret(key string, prID int) *corev1.Secret {
	sec := adoptWaitingSecret("")
	sec.Annotations[key] = models.PendingPRMark{PR: prID, Repo: "example.invalid/acme/fleet"}.String()
	return sec
}

func TestFinishAdopt_TwiceWritesOneAuditEntry_RoundS(t *testing.T) {
	cases := []struct {
		kind, key, event string
	}{
		{prtracker.FinishAdopt, argosecrets.AnnotationAdoptPendingPR, "cluster_adopted"},
		{prtracker.FinishTakeover, argosecrets.AnnotationTakeoverPendingPR, "cluster_taken_over"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			srv, client := adoptFinishServer(joinWaitingSecret(tc.key, 12))
			pr := adoptPR(12)
			pr.FollowUp = &prtracker.ClusterFollowUp{Finish: tc.kind, PreserveLegacyLabels: true}
			for i := 0; i < 2; i++ {
				if _, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged"); err != nil {
					t.Fatalf("run %d: %v", i+1, err)
				}
			}
			if n := countFinishAuditEvents(srv, tc.event); n != 1 {
				t.Fatalf("a finish run twice wrote %d %q audit entries, want 1", n, tc.event)
			}
			if _, still := liveAdoptSecret(t, client).Annotations[tc.key]; still {
				t.Fatal("the waiting mark is still on")
			}
		})
	}
}

// The first run takes ownership but cannot take the mark off; the second
// takes the mark off. One audit entry in all.
func TestFinishAdopt_MarkComesOffOnTheSecondRun_OneAuditEntry_RoundS(t *testing.T) {
	srv, client := adoptFinishServer(joinWaitingSecret(argosecrets.AnnotationAdoptPendingPR, 12))
	refuse := true
	client.PrependReactor("update", "secrets", func(a k8stesting.Action) (bool, runtime.Object, error) {
		obj := a.(k8stesting.UpdateAction).GetObject().(*corev1.Secret)
		if _, has := obj.Annotations[argosecrets.AnnotationAdoptPendingPR]; refuse && !has {
			return true, nil, errors.New("secret update refused")
		}
		return false, nil, nil
	})
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishAdopt}
	if _, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if _, still := liveAdoptSecret(t, client).Annotations[argosecrets.AnnotationAdoptPendingPR]; !still {
		t.Fatal("the test needs the mark to stay on after the first run")
	}
	refuse = false
	if _, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n := countFinishAuditEvents(srv, "cluster_adopted"); n != 1 {
		t.Fatalf("wrote %d cluster_adopted audit entries, want 1", n)
	}
}

func TestFinishAdopt_FailedTryWritesNoAuditEntry_RoundS(t *testing.T) {
	srv, client := adoptFinishServer(joinWaitingSecret(argosecrets.AnnotationTakeoverPendingPR, 12))
	client.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("secret update refused")
	})
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishTakeover}
	for i := 0; i < 3; i++ {
		if _, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged"); err == nil {
			t.Fatal("a finish that could not take ownership must be an error, so the tracker tries again")
		}
	}
	if n := len(srv.auditLog.List(100)); n != 0 {
		t.Fatalf("failed tries wrote %d audit entries; the tracker writes the one give-up entry", n)
	}
}

// A pull request tracked before the finish kind existed is finished when a
// JSON mark names it.
func TestFinishAdopt_OldTrackedPR_JSONMark_RoundS(t *testing.T) {
	srv, client := adoptFinishServer(joinWaitingSecret(argosecrets.AnnotationAdoptPendingPR, 12))
	if got, err := srv.FinishAdoptOnPRComplete(context.Background(), adoptPR(12), "merged"); err != nil || got != adoptFinishAdopted {
		t.Fatalf("outcome = %q (%v), want %q", got, err, adoptFinishAdopted)
	}
	if liveAdoptSecret(t, client).Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Fatal("ownership not taken")
	}
	srv2, _ := adoptFinishServer(joinWaitingSecret(argosecrets.AnnotationAdoptPendingPR, 12))
	if got, _ := srv2.FinishAdoptOnPRComplete(context.Background(), adoptPR(13), "merged"); got != adoptFinishIgnored {
		t.Fatalf("a mark naming another pull request must be ignored, got %q", got)
	}
}

// The first run takes ownership and then fails; the second run changes
// nothing on the ownership but takes the mark off. It must still write the
// one audit entry, or the adopt never shows in the audit log.
func TestFinishAdopt_FailedAfterTheSwap_SecondRunWritesTheAuditEntry_RoundS(t *testing.T) {
	srv, client := adoptFinishServer(joinWaitingSecret(argosecrets.AnnotationAdoptPendingPR, 12))
	updates := 0
	client.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 2 {
			return true, nil, errors.New("secret update refused")
		}
		return false, nil, nil
	})
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishAdopt}
	if _, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged"); err == nil {
		t.Fatal("the first run must fail after the swap")
	}
	if liveAdoptSecret(t, client).Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Fatal("the test needs the first run to have taken ownership")
	}
	if _, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if n := countFinishAuditEvents(srv, "cluster_adopted"); n != 1 {
		t.Fatalf("wrote %d cluster_adopted audit entries, want 1", n)
	}
}
