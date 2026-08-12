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
