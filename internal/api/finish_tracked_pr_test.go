package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// EP3 dry run round R: the tracker's finish step (Server.FinishTrackedPR).
// R1: a failed finish returns an error so the tracker tries again. R3: a
// cluster removed and registered again before the finish keeps its new
// connection. R4: an adopt finishes from the tracked pull request, not from
// the mark. R5: a take over swaps only after its merge.

func liveSecret(t *testing.T, k8s *fake.Clientset, name string) *corev1.Secret {
	t.Helper()
	sec, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the connection %s is gone: %v", name, err)
	}
	return sec
}

func removePR(id int, cluster, cleanup string, keep bool) prtracker.PRInfo {
	return prtracker.PRInfo{
		PRID: id, Cluster: cluster, Operation: prtracker.OpRemoveCluster,
		FollowUp: &prtracker.ClusterFollowUp{Cleanup: cleanup, KeepConnection: keep, CredentialKey: cluster},
	}
}

// R1: when the finish cannot run (here: Git cannot be read), it returns an
// error, so the tracker keeps the pull request, and the keep mark stays on.
func TestFinishTrackedPR_GitReadFails_ReturnsAnErrorAndKeepsTheMark(t *testing.T) {
	gp := newTakeoverFakeGP()
	gp.readErr[orchestrator.V4ManagedClustersPath] = errors.New("rate limited")
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "12"})
	srv, _, k8s := takeoverTestServer(t, gp, []*corev1.Secret{sec}, fakeAppSetReader{})

	if err := srv.FinishTrackedPR(context.Background(), removePR(12, "prod-eu", "git", true), "merged"); err == nil {
		t.Fatal("a finish that could not read Git returned nil, so the tracker would drop the pull request")
	}
	live := liveSecret(t, k8s, "prod-eu")
	if live.Annotations[argosecrets.AnnotationRemoveKeepPendingPR] != "12" {
		t.Fatalf("the keep mark must stay on while the finish waits: %v", live.Annotations)
	}
}

// R1: a kept connection's finish takes the label and the keep mark off.
func TestFinishTrackedPR_KeptConnection_LabelAndMarkComeOff(t *testing.T) {
	sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
		map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: "12"})
	srv, _, k8s := takeoverTestServer(t, newTakeoverFakeGP(), []*corev1.Secret{sec}, fakeAppSetReader{})

	if err := srv.FinishTrackedPR(context.Background(), removePR(12, "prod-eu", "git", true), "merged"); err != nil {
		t.Fatal(err)
	}
	live := liveSecret(t, k8s, "prod-eu")
	if _, still := live.Labels[argosecrets.LabelManagedBy]; still {
		t.Fatalf("Sharko's label is still on: %v", live.Labels)
	}
	if _, still := live.Annotations[argosecrets.AnnotationRemoveKeepPendingPR]; still {
		t.Fatalf("the keep mark is still on: %v", live.Annotations)
	}
}

// R1: a closed removal changed nothing, so only its own keep mark comes off.
func TestFinishTrackedPR_ClosedRemove_TakesOnlyItsOwnMarkOff(t *testing.T) {
	for _, tc := range []struct {
		mark      string
		wantStays bool
	}{{"12", false}, {"99", true}} {
		sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
			map[string]string{argosecrets.AnnotationRemoveKeepPendingPR: tc.mark})
		srv, _, k8s := takeoverTestServer(t, newTakeoverFakeGP(), []*corev1.Secret{sec}, fakeAppSetReader{})
		if err := srv.FinishTrackedPR(context.Background(), removePR(12, "prod-eu", "all", false), "closed"); err != nil {
			t.Fatal(err)
		}
		live := liveSecret(t, k8s, "prod-eu")
		_, stays := live.Annotations[argosecrets.AnnotationRemoveKeepPendingPR]
		if stays != tc.wantStays {
			t.Fatalf("mark %q: stays=%v, want %v", tc.mark, stays, tc.wantStays)
		}
		if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
			t.Fatal("a closed removal must not change the label")
		}
	}
}

// R3: the cluster was removed and registered again before the finish ran.
// The new registration's connection must not be deleted.
func TestFinishTrackedPR_RegisteredAgain_LeavesTheNewConnection(t *testing.T) {
	t.Run("registration-pending mark", func(t *testing.T) {
		sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue},
			map[string]string{models.AnnotationRegistrationPending: "2026-10-23T10:00:00Z"})
		srv, _, k8s := takeoverTestServer(t, newTakeoverFakeGP(), []*corev1.Secret{sec}, fakeAppSetReader{})
		if err := srv.FinishTrackedPR(context.Background(), removePR(12, "prod-eu", "all", false), "merged"); err != nil {
			t.Fatal(err)
		}
		live := liveSecret(t, k8s, "prod-eu")
		if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
			t.Fatalf("the new registration's connection was changed: %v", live.Labels)
		}
	})
	t.Run("open register pull request", func(t *testing.T) {
		sec := brownfieldSecret("prod-eu", map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue}, nil)
		srv, _, k8s := takeoverTestServer(t, newTakeoverFakeGP(), []*corev1.Secret{sec}, fakeAppSetReader{})
		tracker := prtracker.NewTracker(cmstore.NewStore(fake.NewSimpleClientset(), "default", "sharko-pending-prs"),
			func() prtracker.GitProvider { return nil }, func(audit.Entry) {})
		if err := tracker.TrackPR(context.Background(), prtracker.PRInfo{PRID: 20, Cluster: "prod-eu", Operation: prtracker.OpRegisterCluster, LastStatus: "open"}); err != nil {
			t.Fatal(err)
		}
		srv.SetPRTracker(tracker)
		if err := srv.FinishTrackedPR(context.Background(), removePR(12, "prod-eu", "all", false), "merged"); err != nil {
			t.Fatal(err)
		}
		liveSecret(t, k8s, "prod-eu")
	})
}

// R4: an adopt merged before its waiting mark was written still finishes,
// because the tracked pull request says it is an adopt.
func TestFinishTrackedPR_AdoptWithoutTheMark_StillFinishes(t *testing.T) {
	srv, client := adoptFinishServer(adoptWaitingSecret(""))
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishAdopt}
	if err := srv.FinishTrackedPR(context.Background(), pr, "merged"); err != nil {
		t.Fatal(err)
	}
	live := liveAdoptSecret(t, client)
	if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue || live.Annotations[orchestrator.AnnotationAdopted] != "true" {
		t.Fatalf("the adopt did not finish: labels %v annotations %v", live.Labels, live.Annotations)
	}
}

// R4: a failed ownership write returns an error, so the tracker tries again.
func TestFinishTrackedPR_AdoptWriteFails_ReturnsAnError(t *testing.T) {
	srv, client := adoptFinishServer(adoptWaitingSecret("12"))
	client.PrependReactor("update", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("conflict")
	})
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishAdopt}
	if err := srv.FinishTrackedPR(context.Background(), pr, "merged"); err == nil {
		t.Fatal("a failed adopt finish returned nil, so the tracker would drop it and the adopt would never finish")
	}
}

// R4: a connection that is gone has nothing left to finish.
func TestFinishTrackedPR_AdoptConnectionGone_IsDone(t *testing.T) {
	srv, _ := adoptFinishServer(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: adoptFinishNS}})
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishAdopt}
	got, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged")
	if err != nil || got != adoptFinishGone {
		t.Fatalf("outcome %q, %v; want %q, nil", got, err, adoptFinishGone)
	}
}

func takeoverWaitingSecret(mark string) *corev1.Secret {
	s := adoptWaitingSecret("")
	s.Annotations = map[string]string{argosecrets.AnnotationTakeoverPendingPR: mark}
	return s
}

// R5: a take over merged: Sharko takes ownership as the person asked (here:
// drop the old labels).
func TestFinishTrackedPR_TakeoverMerged_SwapsAsAsked(t *testing.T) {
	srv, client := adoptFinishServer(takeoverWaitingSecret("12"))
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishTakeover, PreserveLegacyLabels: false}
	got, err := srv.FinishAdoptOnPRComplete(context.Background(), pr, "merged")
	if err != nil || got != adoptFinishTakenOver {
		t.Fatalf("outcome %q, %v", got, err)
	}
	live := liveAdoptSecret(t, client)
	if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Fatalf("ownership not taken: %v", live.Labels)
	}
	if _, kept := live.Labels["env"]; kept {
		t.Fatalf("the person asked to drop the old labels: %v", live.Labels)
	}
	if _, still := live.Annotations[argosecrets.AnnotationTakeoverPendingPR]; still {
		t.Fatalf("the take over mark is still on: %v", live.Annotations)
	}
	if live.Annotations[argosecrets.AnnotationTakenOverAt] == "" {
		t.Fatalf("a finished take over carries the taken-over mark: %v", live.Annotations)
	}
}

// R5: a take over closed without merging: only the mark comes off.
func TestFinishTrackedPR_TakeoverClosed_TakesOnlyTheMarkOff(t *testing.T) {
	srv, client := adoptFinishServer(takeoverWaitingSecret("12"))
	pr := adoptPR(12)
	pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishTakeover, PreserveLegacyLabels: true}
	if err := srv.FinishTrackedPR(context.Background(), pr, "closed"); err != nil {
		t.Fatal(err)
	}
	live := liveAdoptSecret(t, client)
	if _, owned := live.Labels[argosecrets.LabelManagedBy]; owned {
		t.Fatal("a closed take over must not hand ownership to Sharko")
	}
	if len(live.Annotations) != 0 {
		t.Fatalf("annotations left: %v", live.Annotations)
	}
}

// R5: a take over whose pull request could not be recorded is a 207 partial
// in plain words, and the connection is left exactly as it was.
func TestTakeover_PRNotRecorded_Is207AndChangesNothing(t *testing.T) {
	gp := newTakeoverFakeGP()
	srv, router, k8s := takeoverTestServer(t, gp,
		[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)}, fakeAppSetReader{})
	cm := fake.NewSimpleClientset()
	cm.PrependReactor("*", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("configmap write refused")
	})
	srv.SetPRTracker(prtracker.NewTracker(cmstore.NewStore(cm, "default", "sharko-pending-prs"),
		func() prtracker.GitProvider { return nil }, func(audit.Entry) {}))

	w := httptest.NewRecorder()
	router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
		TakeoverRequest{Yes: true, AcknowledgedFindings: []string{"secret-owner"}}))
	if w.Code != http.StatusMultiStatus {
		t.Fatalf("got %d, want 207 (body=%s)", w.Code, w.Body.String())
	}
	var resp TakeoverResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := "Sharko opened pull request https://example.invalid/pr/1 but could not record it, so it could not finish the take over after the merge. Nothing has changed on the cluster's ArgoCD connection. Close that pull request without merging it, then try again."
	if resp.Status != "partial" || resp.Message != want || resp.SecretSwapped || resp.WaitingForMerge {
		t.Fatalf("got %s\nwant message %q", w.Body.String(), want)
	}
	// Only the waiting mark is on (it goes on before tracking, round S,
	// S7), so the waiting-marks pass can still find this pull request.
	live := liveSecret(t, k8s, "prod-eu")
	if live.Labels[argosecrets.LabelManagedBy] != "" || live.Labels["env"] != "prod" ||
		!models.PendingPRMarkNames(live.Annotations[argosecrets.AnnotationTakeoverPendingPR], 1) || len(live.Annotations) != 1 {
		t.Fatalf("the connection changed: labels %v annotations %v", live.Labels, live.Annotations)
	}
}

// R5: the tracked take over pull request remembers what the person asked
// for the old labels, so the merge does the same thing the call would have.
func TestTakeover_TrackedPRKeepsTheLabelChoice(t *testing.T) {
	for _, keep := range []bool{true, false} {
		gp := newTakeoverFakeGP()
		srv, router, _ := takeoverTestServer(t, gp,
			[]*corev1.Secret{brownfieldSecret("prod-eu", map[string]string{"env": "prod"}, nil)}, fakeAppSetReader{})
		tracker := prtracker.NewTracker(cmstore.NewStore(fake.NewSimpleClientset(), "default", "sharko-pending-prs"),
			func() prtracker.GitProvider { return nil }, func(audit.Entry) {})
		srv.SetPRTracker(tracker)
		k := keep
		w := httptest.NewRecorder()
		router.ServeHTTP(w, roleReq(http.MethodPost, "/api/v1/clusters/prod-eu/takeover", "admin",
			TakeoverRequest{Yes: true, PreserveLegacyLabels: &k, AcknowledgedFindings: []string{"secret-owner"}}))
		if w.Code != http.StatusOK {
			t.Fatalf("keep=%v: got %d (body=%s)", keep, w.Code, w.Body.String())
		}
		pr, err := tracker.GetPR(context.Background(), 1)
		if err != nil || pr == nil || pr.FollowUp == nil {
			t.Fatalf("keep=%v: the take over pull request was not tracked with its finish step: %+v %v", keep, pr, err)
		}
		if pr.FollowUp.Finish != orchestrator.FinishTakeover || pr.FollowUp.PreserveLegacyLabels != keep {
			t.Fatalf("keep=%v: follow-up = %+v", keep, *pr.FollowUp)
		}
	}
}
