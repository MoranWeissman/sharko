package clusterreconciler

import (
	"context"
	"strings"
	"testing"

	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// V3 E1 follow-up — DriftDetected Kubernetes event on a sustained label
// fight (reconcile_status.go's recordFightCheck). These tests pin:
//
//  1. No event below fightRevertThreshold.
//  2. Exactly one event the tick the threshold is first crossed, and no
//     further events while the same fight keeps going.
//  3. Once the fight resolves (reverts back to 0) and a NEW fight starts,
//     a fresh event fires.
//  4. A nil EventRecorder (out-of-cluster / dev mode) never panics.
//  5. The event message names the cluster and carries no secret material.

// newReconcilerForTestWithEventRecorder mirrors newReconcilerForTest
// (poll_test.go) but also wires an EventRecorder — the shared helper
// doesn't take one since none of its 86 existing call sites need it.
func newReconcilerForTestWithEventRecorder(t *testing.T, k8sClient *k8sfake.Clientset, audits *auditCollector, body []byte, recorder *events.EventRecorder) *Reconciler {
	t.Helper()

	fg := &fakeGit{files: map[string][]byte{}}
	if body != nil {
		fg.files[DefaultManagedClustersPath] = body
	}
	gitFn := func() gitprovider.GitProvider { return fg }

	return New(Deps{
		GitProvider:   gitFn,
		ArgoClient:    k8sClient,
		Vault:         staticVault(&fakeVault{}),
		AuditFn:       audits.Add,
		TickInterval:  0, // default; we never Start the loop in these tests
		EventRecorder: recorder,
	})
}

func TestDriftEvent_BelowThreshold_NoEvent(t *testing.T) {
	t.Parallel()

	fake := record.NewFakeRecorder(10)
	recorder := events.NewRecorderForTest(fake, "sharko")

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := k8sfake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTestWithEventRecorder(t, k8sClient, &auditCollector{}, body, recorder)

	ctx := context.Background()
	r.pollOnce(ctx) // tick 1: baseline

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 2: revert #1 — streak=1, below fightRevertThreshold (2)

	select {
	case ev := <-fake.Events:
		t.Fatalf("unexpected event below threshold: %q", ev)
	default:
		// expected — no event yet
	}
}

func TestDriftEvent_ThresholdCrossed_ExactlyOneEvent(t *testing.T) {
	t.Parallel()

	fake := record.NewFakeRecorder(10)
	recorder := events.NewRecorderForTest(fake, "sharko")

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := k8sfake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTestWithEventRecorder(t, k8sClient, &auditCollector{}, body, recorder)

	ctx := context.Background()
	r.pollOnce(ctx) // tick 1: baseline

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 2: revert #1 — still below threshold

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 3: revert #2 — threshold reached, event should fire

	var ev string
	select {
	case ev = <-fake.Events:
	default:
		t.Fatal("expected exactly one event once the threshold was crossed, got none")
	}
	if !strings.Contains(ev, "Warning") || !strings.Contains(ev, events.ReasonDriftDetected) {
		t.Fatalf("event = %q, want it to be a Warning with reason %q", ev, events.ReasonDriftDetected)
	}

	// Keep fighting for more ticks — the fight is ongoing, but only one
	// event should ever have been emitted for it.
	for i := 0; i < 3; i++ {
		revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
		r.pollOnce(ctx)
	}

	select {
	case ev := <-fake.Events:
		t.Fatalf("unexpected second event for the same ongoing fight: %q", ev)
	default:
		// expected — no further events while the same fight continues
	}
}

func TestDriftEvent_NewFightAfterResolution_FreshEvent(t *testing.T) {
	t.Parallel()

	fake := record.NewFakeRecorder(10)
	recorder := events.NewRecorderForTest(fake, "sharko")

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := k8sfake.NewSimpleClientset(userSecret("user-cluster", nil))
	r := newReconcilerForTestWithEventRecorder(t, k8sClient, &auditCollector{}, body, recorder)

	ctx := context.Background()
	r.pollOnce(ctx) // tick 1: baseline

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 2: revert #1
	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 3: revert #2 — threshold reached, event #1

	select {
	case <-fake.Events:
	default:
		t.Fatal("expected the first fight's event")
	}

	// The fight resolves: a clean tick where nothing reverts the label.
	r.pollOnce(ctx) // tick 4: clean — reverts resets to 0

	select {
	case ev := <-fake.Events:
		t.Fatalf("unexpected event on a clean/resolving tick: %q", ev)
	default:
	}

	// A brand new fight starts.
	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 5: revert #1 of the new fight
	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 6: revert #2 of the new fight — threshold reached again

	select {
	case ev := <-fake.Events:
		if !strings.Contains(ev, events.ReasonDriftDetected) {
			t.Fatalf("event = %q, want reason %q", ev, events.ReasonDriftDetected)
		}
	default:
		t.Fatal("expected a fresh event once a new fight crosses the threshold")
	}
}

func TestDriftEvent_NilRecorder_NoPanic(t *testing.T) {
	t.Parallel()

	body := envelopedWithModes(testClusterEntry{
		Name:   "user-cluster",
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := k8sfake.NewSimpleClientset(userSecret("user-cluster", nil))
	// nil EventRecorder — matches a server booted with no in-cluster K8s
	// client (local/dev mode). This must never panic.
	r := newReconcilerForTestWithEventRecorder(t, k8sClient, &auditCollector{}, body, nil)

	ctx := context.Background()
	r.pollOnce(ctx) // tick 1: baseline

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 2: revert #1

	revertLiveLabel(t, k8sClient, "user-cluster", "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 3: revert #2 — threshold reached; must not panic with a nil recorder

	rec, ok := r.LastReconcile("user-cluster")
	if !ok {
		t.Fatal("expected a LastReconcile record")
	}
	if rec.Message == "" {
		t.Fatal("expected the fight warning to still be recorded even with no event recorder")
	}
}

func TestDriftEvent_MessageNamesClusterNoSecretMaterial(t *testing.T) {
	t.Parallel()

	fake := record.NewFakeRecorder(10)
	recorder := events.NewRecorderForTest(fake, "sharko")

	const clusterName = "prod-eu-secret-fight"
	const secretToken = "user-owned-token" // matches userSecret's Data["config"] bearerToken
	body := envelopedWithModes(testClusterEntry{
		Name:   clusterName,
		Mode:   "user",
		Labels: map[string]string{"addon-foo": "enabled"},
	})
	k8sClient := k8sfake.NewSimpleClientset(userSecret(clusterName, nil))
	r := newReconcilerForTestWithEventRecorder(t, k8sClient, &auditCollector{}, body, recorder)

	ctx := context.Background()
	r.pollOnce(ctx) // tick 1: baseline

	revertLiveLabel(t, k8sClient, clusterName, "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 2: revert #1
	revertLiveLabel(t, k8sClient, clusterName, "addon-foo", "disabled")
	r.pollOnce(ctx) // tick 3: revert #2 — threshold reached, event fires

	var ev string
	select {
	case ev = <-fake.Events:
	default:
		t.Fatal("expected an event once the threshold was crossed")
	}

	if !strings.Contains(ev, clusterName) {
		t.Errorf("event = %q, want it to name the cluster %q", ev, clusterName)
	}
	if strings.Contains(ev, secretToken) {
		t.Errorf("event = %q, must never contain secret material (found the test token)", ev)
	}
	if strings.Contains(ev, "user-owned") || strings.Contains(ev, "bearerToken") || strings.Contains(ev, "caData") {
		t.Errorf("event = %q, must never echo secret/label values from the live Secret", ev)
	}
}
