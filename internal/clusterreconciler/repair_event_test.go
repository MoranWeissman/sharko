package clusterreconciler

import (
	"strings"
	"testing"

	"k8s.io/client-go/tools/record"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/events"
)

// TestRepairAndDriftEventReasonsAreDifferent is the R3-11 criterion 3 test:
// the two event reasons must be different, and a successful repair must use
// the success reason (ReasonConnectionRepaired), not the fault reason
// (ReasonDriftDetected).
//
// Event reasons are documented as "suitable for switch statements" and operators
// and automation read them. An alert rule that fires on DriftDetected must not
// fire when someone successfully fixes a connection.
func TestRepairAndDriftEventReasonsAreDifferent(t *testing.T) {
	if events.ReasonConnectionRepaired == events.ReasonDriftDetected {
		t.Fatal("ReasonConnectionRepaired and ReasonDriftDetected must be different constants")
	}

	// Repair event must use the success reason.
	fake := record.NewFakeRecorder(10)
	recorder := events.NewRecorderForTest(fake, "sharko")
	r := &Reconciler{eventRecorder: recorder}

	r.EmitConnectionRepairEvent("test-cluster", 3)

	select {
	case ev := <-fake.Events:
		if !strings.Contains(ev, events.ReasonConnectionRepaired) {
			t.Errorf("repair event reason = %q, want it to contain %q", ev, events.ReasonConnectionRepaired)
		}
		if strings.Contains(ev, events.ReasonDriftDetected) {
			t.Errorf("repair event uses the drift-detected reason; it must use the success reason %q", events.ReasonConnectionRepaired)
		}
	default:
		t.Fatal("EmitConnectionRepairEvent did not emit an event")
	}
}

// TestDriftNoticeKeepsDriftDetectedReason verifies that the drift notice
// (noticeConnectionShapeDrift) still uses ReasonDriftDetected — that one is
// a real fault and its reason is correct.
func TestDriftNoticeKeepsDriftDetectedReason(t *testing.T) {
	fake := record.NewFakeRecorder(10)
	recorder := events.NewRecorderForTest(fake, "sharko")
	r := &Reconciler{
		eventRecorder: recorder,
		driftNotice:   make(map[string]connectionDriftState),
	}

	// Hand-fed broken Secret (no ownership label).
	broken := ownedConnSecret(nil,
		map[string]string{"name": repairReconCluster, "server": "https://x.invalid", "config": "{}"},
		nil)
	delete(broken.Labels, argosecrets.LabelManagedBy)

	r.noticeConnectionShapeDrift(nil, repairReconCluster, broken, false)

	// The drift notice should emit ReasonDriftDetected, not ReasonConnectionRepaired.
	select {
	case ev := <-fake.Events:
		if !strings.Contains(ev, events.ReasonDriftDetected) {
			t.Errorf("drift notice event reason = %q, want it to contain %q", ev, events.ReasonDriftDetected)
		}
		if strings.Contains(ev, events.ReasonConnectionRepaired) {
			t.Errorf("drift notice must use ReasonDriftDetected, not the repair success reason")
		}
	default:
		t.Fatal("noticeConnectionShapeDrift did not emit an event for a broken connection")
	}
}

// TestConnectionRepairEventMessageNamesTheFullRepair pins the FULL path's
// message text (R3-16). The full repair really does rewrite the sign-in details
// from the secrets backend, so its message may say so — and the assertion is
// here so the sentence cannot drift into something the labels-only path could
// also emit.
func TestConnectionRepairEventMessageNamesTheFullRepair(t *testing.T) {
	fake := record.NewFakeRecorder(10)
	r := &Reconciler{eventRecorder: events.NewRecorderForTest(fake, "sharko")}

	r.EmitConnectionRepairEvent("prod-eu", 3)

	select {
	case ev := <-fake.Events:
		if !strings.Contains(ev, events.ReasonConnectionRepaired) {
			t.Errorf("full-repair event reason = %q, want %q", ev, events.ReasonConnectionRepaired)
		}
		if !strings.Contains(ev, "stored sign-in details") {
			t.Errorf(`full-repair event message = %q, want it to say it rewrote the stored sign-in details — that is what a full repair does`, ev)
		}
		if !strings.Contains(ev, "3 owned field(s) rewritten") {
			t.Errorf("full-repair event message = %q, want the field count in it", ev)
		}
	default:
		t.Fatal("EmitConnectionRepairEvent did not emit an event")
	}
}

// TestAddonLabelsRepairEventClaimsOnlyWhatItDid is the R3-16 event criterion.
//
// The labels-only path used to call EmitConnectionRepairEvent, whose message
// says Sharko repaired the connection "to match git and the stored sign-in
// details". A labels-only repair never reads or writes sign-in details, so the
// event was claiming something that did not happen. An operator reads that text
// and acts on it.
func TestAddonLabelsRepairEventClaimsOnlyWhatItDid(t *testing.T) {
	fake := record.NewFakeRecorder(10)
	r := &Reconciler{eventRecorder: events.NewRecorderForTest(fake, "sharko")}

	r.EmitAddonLabelsRepairEvent("prod-eu", 2)

	select {
	case ev := <-fake.Events:
		if !strings.Contains(ev, events.ReasonAddonLabelsRepaired) {
			t.Errorf("labels-only repair event reason = %q, want %q", ev, events.ReasonAddonLabelsRepaired)
		}
		if strings.Contains(ev, events.ReasonConnectionRepaired) {
			t.Errorf(`labels-only repair event uses the full-repair reason: %q.

The two are different operations with different reach, and a reason is what an automation switches on. A label write must not look like a credential write.`, ev)
		}
		if strings.Contains(ev, "sign-in details were not read") {
			// The disclaimer sentence is allowed and expected; skip the
			// leak check below for it by asserting the claim form instead.
			if !strings.Contains(ev, "addon labels git declares") {
				t.Errorf("labels-only repair event message = %q, want it to say it re-applied the addon labels git declares", ev)
			}
		} else if strings.Contains(ev, "sign-in details") {
			t.Errorf(`labels-only repair event message claims something about sign-in details: %q.

A labels-only repair never reads or writes them.`, ev)
		}
		if strings.Contains(ev, "repaired its ArgoCD connection") {
			t.Errorf(`labels-only repair event claims it repaired the whole connection: %q — it re-applied labels`, ev)
		}
		if !strings.Contains(ev, "2 label(s) rewritten") {
			t.Errorf("labels-only repair event message = %q, want the label count in it", ev)
		}
	default:
		t.Fatal("EmitAddonLabelsRepairEvent did not emit an event")
	}
}

// TestRepairEventReasonsAreAllDistinct pins that the three reasons this feature
// touches are three different strings. If two ever collide, every switch on a
// reason silently starts treating two different things as one.
func TestRepairEventReasonsAreAllDistinct(t *testing.T) {
	reasons := map[string]string{
		"ReasonConnectionRepaired":  events.ReasonConnectionRepaired,
		"ReasonAddonLabelsRepaired": events.ReasonAddonLabelsRepaired,
		"ReasonDriftDetected":       events.ReasonDriftDetected,
	}
	seen := make(map[string]string, len(reasons))
	for name, value := range reasons {
		if other, clash := seen[value]; clash {
			t.Errorf("%s and %s are both %q — they must be distinct", name, other, value)
		}
		seen[value] = name
	}
}
