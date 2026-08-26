package clusterreconciler

// user_sentences_exact_test.go — exact pins on the sentences this package
// shows a person, written as LITERALS HERE.
//
// # Why this file exists
//
// B18 capitalised Git in four of this package's sentences, and not one of
// them had an exact pin. failure_sentence_test.go asserts only that the
// mapped sentence is non-empty and is not the raw error text; the two skip
// messages were inline literals in the middle of two reconcile loops with
// nothing referencing them at all. So all four could have been reworded, or
// quietly broken, with the whole Go suite staying green — the same shape as
// the repair refusals that had no pin (see api/connection_sentence_guards_test.go),
// and the same shape as the project lesson about a wrong sentence surviving
// four review rounds because its only test asserted != "".
//
// Every want below is TYPED OUT HERE. Comparing against the production
// constant would compare a constant with itself and pass no matter what the
// sentence said.

import (
	"strings"
	"testing"

	"k8s.io/client-go/tools/record"

	"github.com/MoranWeissman/sharko/internal/events"
)

// TestFailureSentence_GitFailuresExact pins the two whole-pass abort
// sentences by driving the REAL mapping function with the raw markers its
// call sites write, so this is a behaviour pin and not a string lookup.
func TestFailureSentence_GitFailuresExact(t *testing.T) {
	cases := []struct{ name, raw, want string }{
		{
			"git read failed (pollOnce spelling)",
			"reconciler pass aborted: git read failed: dial tcp 10.0.0.1:443: connection refused",
			"Sharko could not read Git, so this check did not finish. Check that Sharko can reach your Git host, then click Refresh.",
		},
		{
			"git_read failed (checkOnce spelling)",
			"reconciler pass aborted: git_read failed: context deadline exceeded",
			"Sharko could not read Git, so this check did not finish. Check that Sharko can reach your Git host, then click Refresh.",
		},
		{
			"schema validation failed",
			"reconciler pass aborted: schema_validation failed: spec.clusters.0.name is required",
			"The managed-clusters file in Git failed validation, so this check did not finish. Fix the file in Git, then click Refresh.",
		},
	}
	for _, tc := range cases {
		if got := FailureSentence(tc.raw); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestSkippedRecordSentencesExact pins the two skip messages. A skipped
// record's message reaches the browser EXACTLY AS RECORDED — it never goes
// through FailureSentence — so these are the words themselves.
func TestSkippedRecordSentencesExact(t *testing.T) {
	exact := []struct{ name, got, want string }{
		{"AssignmentFileUnreadableCheckMessage", AssignmentFileUnreadableCheckMessage,
			"Sharko couldn't read this cluster's addon assignment file in Git, so it can't say whether the cluster secret matches."},
		{"AssignmentFileUnreadableTickMessage", AssignmentFileUnreadableTickMessage,
			"Sharko couldn't read this cluster's addon assignment file in Git this tick, so it left the addon labels on your ArgoCD cluster secret exactly as they are."},
	}
	for _, tc := range exact {
		if tc.got != tc.want {
			t.Errorf("%s drifted:\n got %q\nwant %q", tc.name, tc.got, tc.want)
		}
	}

	// The two must stay different. Folded back together, the check pass would
	// claim it left labels alone (it never touches them) or the write tick
	// would stop saying that it did.
	if AssignmentFileUnreadableCheckMessage == AssignmentFileUnreadableTickMessage {
		t.Error("the check-pass and write-tick skip messages are identical — one of them is now claiming something it does not do")
	}
	if !strings.Contains(AssignmentFileUnreadableTickMessage, "left the addon labels") {
		t.Error("the write-tick skip message no longer says it left the labels alone, which is the one thing it is there to say")
	}
}

// TestRepairEventSentencesExact pins the two Kubernetes event messages an
// operator reads in `kubectl describe`. They carry a count, so the pin is on
// the rendered text with a known count rather than on a format string.
func TestRepairEventSentencesExact(t *testing.T) {
	full := renderRepairEvent(t, func(r *Reconciler) { r.EmitConnectionRepairEvent("prod-eu", 3) })
	labels := renderRepairEvent(t, func(r *Reconciler) { r.EmitAddonLabelsRepairEvent("prod-eu", 2) })

	wantFull := "Cluster prod-eu: Sharko rewrote this connection's sign-in details — the values Git references, resolved from its configured credentials source — and re-applied the labels Git declares (3 owned field(s) rewritten)."
	wantLabels := "Cluster prod-eu: Sharko re-applied the addon labels Git declares for this connection (2 label(s) rewritten). The connection's sign-in details were not read or changed."

	if !strings.Contains(full, wantFull) {
		t.Errorf("the full-repair event drifted:\n got %q\nwant it to contain %q", full, wantFull)
	}
	if !strings.Contains(labels, wantLabels) {
		t.Errorf("the labels-only repair event drifted:\n got %q\nwant it to contain %q", labels, wantLabels)
	}
}

// renderRepairEvent runs one emit against a fake recorder and returns the
// single event it produced.
func renderRepairEvent(t *testing.T, emit func(*Reconciler)) string {
	t.Helper()
	fake := record.NewFakeRecorder(10)
	emit(&Reconciler{eventRecorder: events.NewRecorderForTest(fake, "sharko")})
	select {
	case ev := <-fake.Events:
		return ev
	default:
		t.Fatal("no event was recorded — the pin below would assert against nothing")
		return ""
	}
}
