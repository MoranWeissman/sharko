package api

// connection_check_schedule_test.go — B13 items 5 and 6.
//
// Item 5: the first pass must happen at boot, not one interval later — and
// it must survive its dependencies being a second late. run() already fired
// one pass before creating the ticker, but NOTHING TESTED IT: every existing
// test drives runOnce directly, so deleting that line broke nothing at all.
// And the pass it fired returned early and silently whenever a dependency
// was not up yet, with no retry, so the next attempt was a full interval
// away. At boot that is fifteen minutes of a fleet page saying nothing.
//
// Item 6: when checks cannot run, the page must be able to say so. Every
// connection row's headline comes from the last check, so a loop that cannot
// run leaves every row reading "Not checked yet" with the synced count at
// zero and no sentence anywhere explaining it. The worst case is an
// out-of-cluster server, where the loop is never started at all.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCheckStatus_SentencesExact pins every "why checks are not running"
// sentence character for character, as LITERALS IN THIS TEST — not as
// references to the production constants, which would let both sides drift
// together and still pass.
//
// It exists because the tests below only ever compared a constant with
// itself: TestCheckStatus_ReportsEachReasonPlainly asserts
// got.Reason == checkLoopNoReconciler, which stays green no matter what that
// constant says. (Project lesson: a wrong sentence survived four review
// rounds because its only test asserted != "".)
func TestCheckStatus_SentencesExact(t *testing.T) {
	exact := map[string]string{
		checkLoopNotScheduled: "This server is not running the background connection check, so a connection is only checked when someone opens its page or asks for a check.",
		checkLoopNotRunYet:    "The background connection check has not finished a pass yet on this server.",
		// Reworded on the product ruling of 2026-08-19: the old sentence
		// opened "Sharko's cluster reconciler is not running on this
		// server…", which names Sharko's own plumbing at a person who has
		// never read the source.
		checkLoopNoReconciler:      "This server is not set up to keep cluster connections in step with Git, so the background connection check has nothing to compare against.",
		checkLoopNoArgoCD:          "Sharko is not connected to ArgoCD right now, so the background connection check cannot list the clusters to check.",
		checkLoopClusterListFailed: "Sharko could not read the cluster list on the last background pass, so no connection was checked.",
	}
	for got, want := range exact {
		if got != want {
			t.Errorf("sentence drifted:\n got %q\nwant %q", got, want)
		}
	}
	// The reworded sentence may not name Sharko's machinery. Banning the
	// word as well as pinning the sentence means a rewrite cannot quietly
	// bring the old vocabulary back.
	for _, machinery := range []string{"reconciler", "next pass", "controller"} {
		if strings.Contains(strings.ToLower(checkLoopNoReconciler), machinery) {
			t.Errorf("the sentence names Sharko's machinery (%q) — banned:\n  %q", machinery, checkLoopNoReconciler)
		}
	}
	// It must still say why the check is not running: an explanation that
	// stops at "it is not running" tells the reader nothing.
	if !strings.Contains(checkLoopNoReconciler, ", so ") {
		t.Errorf("the sentence no longer explains what the absence costs the reader:\n  %q", checkLoopNoReconciler)
	}
}

// scriptedLoop wires a loop whose passes are scripted, so the schedule can
// be tested without any real dependency, clock or I/O.
type scriptedLoop struct {
	loop *ConnectionCredentialCheckLoop

	mu      sync.Mutex
	answers []checkLoopPass
	calls   int
	fired   chan struct{}
}

// newScriptedLoop returns a loop that answers with the given passes in
// order, repeating the last one forever.
func newScriptedLoop(t *testing.T, interval time.Duration, answers ...checkLoopPass) *scriptedLoop {
	t.Helper()
	srv := &Server{connCheckStatus: newConnectionCheckStatus()}
	sl := &scriptedLoop{
		answers: answers,
		fired:   make(chan struct{}, 64),
	}
	sl.loop = NewConnectionCredentialCheckLoop(srv, interval)
	// Retries fast enough that the test is not a wall-clock test.
	sl.loop.firstRetryDelay = time.Millisecond
	sl.loop.maxRetryDelay = 2 * time.Millisecond
	sl.loop.runOnceFn = func(context.Context) checkLoopPass {
		sl.mu.Lock()
		i := sl.calls
		sl.calls++
		var out checkLoopPass
		if len(sl.answers) > 0 {
			if i >= len(sl.answers) {
				i = len(sl.answers) - 1
			}
			out = sl.answers[i]
		}
		sl.mu.Unlock()
		select {
		case sl.fired <- struct{}{}:
		default:
		}
		return out
	}
	t.Cleanup(sl.loop.Stop)
	return sl
}

func (sl *scriptedLoop) callCount() int {
	sl.mu.Lock()
	defer sl.mu.Unlock()
	return sl.calls
}

// waitForCalls blocks until the loop has run at least n passes, or fails.
func (sl *scriptedLoop) waitForCalls(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for sl.callCount() < n {
		select {
		case <-sl.fired:
		case <-deadline:
			t.Fatalf("the loop ran %d passes in 3s, wanted at least %d", sl.callCount(), n)
		}
	}
}

// TestCheckLoop_RunsOnePassImmediately is the missing guard on the line that
// decides whether a fresh server says anything for its first interval.
// The interval here is an hour: if the immediate pass is deleted, nothing
// runs and this test times out.
func TestCheckLoop_RunsOnePassImmediately(t *testing.T) {
	sl := newScriptedLoop(t, time.Hour, checkLoopPass{Ready: true})
	sl.loop.Start(context.Background())
	sl.waitForCalls(t, 1)
}

// TestCheckLoop_RetriesWhileDependenciesAreNotUp: a pass that could not
// start is retried on a short backoff instead of waiting out a whole
// interval. The interval is an hour, so every pass after the first can only
// come from the retry.
func TestCheckLoop_RetriesWhileDependenciesAreNotUp(t *testing.T) {
	sl := newScriptedLoop(t, time.Hour,
		checkLoopPass{Reason: checkLoopNoReconciler},
		checkLoopPass{Reason: failNoGitConnection},
		checkLoopPass{Ready: true},
	)
	sl.loop.Start(context.Background())
	sl.waitForCalls(t, 3)

	// And it STOPS once a pass runs — it is a boot window, not a fast loop.
	time.Sleep(30 * time.Millisecond)
	if got := sl.callCount(); got != 3 {
		t.Errorf("the loop ran %d passes; it should have stopped retrying at 3, once a pass was ready", got)
	}
}

// TestCheckLoop_DoesNotRetryAGenuineFailure: a pass that RAN and failed is a
// real failure, not a missing dependency. Retrying a failing backend every
// few seconds turns a deliberately slow loop into a fast one.
func TestCheckLoop_DoesNotRetryAGenuineFailure(t *testing.T) {
	sl := newScriptedLoop(t, time.Hour, checkLoopPass{Ready: true, Reason: checkLoopClusterListFailed})
	sl.loop.Start(context.Background())
	sl.waitForCalls(t, 1)

	time.Sleep(30 * time.Millisecond)
	if got := sl.callCount(); got != 1 {
		t.Errorf("the loop ran %d passes on a genuine failure; it must wait for the next tick", got)
	}
}

// TestCheckLoop_GivesUpAfterTheBootWindow: the retry is bounded, so a server
// whose dependencies never arrive does not retry forever.
func TestCheckLoop_GivesUpAfterTheBootWindow(t *testing.T) {
	sl := newScriptedLoop(t, time.Hour, checkLoopPass{Reason: checkLoopNoArgoCD})
	sl.loop.maxBootRetries = 3
	sl.loop.Start(context.Background())
	sl.waitForCalls(t, 4) // the immediate pass plus three retries

	time.Sleep(30 * time.Millisecond)
	if got := sl.callCount(); got != 4 {
		t.Errorf("the loop ran %d passes; the boot window is 1 immediate + 3 retries = 4", got)
	}
}

// --- Item 6: the page can say why nothing was checked -----------------------

// TestCheckStatus_OutOfClusterServerSaysSo is the case the item is really
// about. cmd/sharko/serve.go starts the loop only inside its in-cluster
// branch, so an out-of-cluster server schedules no check at all — and every
// fleet row reads "Not checked yet" permanently.
func TestCheckStatus_OutOfClusterServerSaysSo(t *testing.T) {
	got := newConnectionCheckStatus().snapshot()
	if got.Running {
		t.Error("a server that never started the loop reports checks as running")
	}
	if got.Reason != checkLoopNotScheduled {
		t.Errorf("reason = %q, want %q", got.Reason, checkLoopNotScheduled)
	}
	if got.LastAttempt != "" {
		t.Errorf("last_attempt = %q, want absent — no pass was ever attempted", got.LastAttempt)
	}
}

// TestCheckStatus_NilIsTheSameAnswer: a Server built without the status
// object still answers honestly rather than claiming checks are running.
func TestCheckStatus_NilIsTheSameAnswer(t *testing.T) {
	var st *connectionCheckStatus
	got := st.snapshot()
	if got.Running || got.Reason != checkLoopNotScheduled {
		t.Errorf("a nil status reports %+v, want not running with the not-scheduled sentence", got)
	}
}

// TestCheckStatus_ReportsEachReasonPlainly walks the states a running loop
// can be in.
func TestCheckStatus_ReportsEachReasonPlainly(t *testing.T) {
	cases := []struct {
		name        string
		pass        checkLoopPass
		wantRunning bool
		wantReason  string
	}{
		{"a pass that checked the fleet", checkLoopPass{Ready: true}, true, ""},
		{"no cluster reconciler", checkLoopPass{Reason: checkLoopNoReconciler}, false, checkLoopNoReconciler},
		{"no git connection", checkLoopPass{Reason: failNoGitConnection}, false, failNoGitConnection},
		{"no hub client", checkLoopPass{Reason: failNoHubClient}, false, failNoHubClient},
		{"no ArgoCD", checkLoopPass{Reason: checkLoopNoArgoCD}, false, checkLoopNoArgoCD},
		{"the cluster list failed", checkLoopPass{Ready: true, Reason: checkLoopClusterListFailed}, false, checkLoopClusterListFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newConnectionCheckStatus()
			st.markScheduled(15 * time.Minute)

			// Scheduled but nothing attempted yet is its own honest answer.
			if before := st.snapshot(); before.Running || before.Reason != checkLoopNotRunYet {
				t.Errorf("before any pass: %+v, want not running with %q", before, checkLoopNotRunYet)
			}

			st.recordPass(time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC), tc.pass)
			got := st.snapshot()
			if got.Running != tc.wantRunning {
				t.Errorf("running = %v, want %v", got.Running, tc.wantRunning)
			}
			if got.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.IntervalSeconds != 900 {
				t.Errorf("interval_seconds = %d, want 900", got.IntervalSeconds)
			}
			if got.LastAttempt != "2026-08-18T10:00:00Z" {
				t.Errorf("last_attempt = %q, want the pass timestamp", got.LastAttempt)
			}
		})
	}
}

// TestCheckStatus_RunningNeverCarriesAReason: the two halves cannot
// contradict each other. A page that reads "checks are running" beside a
// sentence explaining why they are not is the same class of defect this
// whole round exists to remove.
func TestCheckStatus_RunningNeverCarriesAReason(t *testing.T) {
	for _, pass := range []checkLoopPass{
		{Ready: true},
		{Ready: true, Reason: checkLoopClusterListFailed},
		{Reason: checkLoopNoArgoCD},
		{},
	} {
		st := newConnectionCheckStatus()
		st.markScheduled(time.Minute)
		st.recordPass(time.Now(), pass)
		got := st.snapshot()
		if got.Running && got.Reason != "" {
			t.Errorf("pass %+v produced running=true with reason %q", pass, got.Reason)
		}
		if !got.Running && got.Reason == "" {
			t.Errorf("pass %+v produced running=false with no explanation at all", pass)
		}
	}
}

// TestCheckStatus_StartMarksScheduledBeforeTheFirstPass: a request arriving
// in the same millisecond as startup must read "scheduled, nothing finished
// yet", not "this server runs no background check" — two different and not
// interchangeable statements.
func TestCheckStatus_StartMarksScheduledBeforeTheFirstPass(t *testing.T) {
	sl := newScriptedLoop(t, time.Hour, checkLoopPass{Ready: true})
	sl.loop.Start(context.Background())
	sl.waitForCalls(t, 1)

	got := sl.loop.srv.connCheckStatus.snapshot()
	if !got.Running {
		t.Errorf("after a ready pass the status reads %+v, want running", got)
	}
	if got.IntervalSeconds != 3600 {
		t.Errorf("interval_seconds = %d, want the loop's own hour", got.IntervalSeconds)
	}
}
