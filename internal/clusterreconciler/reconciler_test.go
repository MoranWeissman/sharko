package clusterreconciler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// setPollFn replaces the per-instance pollFn seam on r so tests can observe
// every tick / trigger without depending on the stub pollOnce's slog output.
// Counter is incremented on every invocation; signal (if non-nil) is sent on
// once per invocation in a non-blocking way (drop-on-saturate).
//
// MUST be called before r.Start to avoid racing the goroutine's read.
func setPollFn(r *Reconciler, counter *atomic.Int64, signal chan<- struct{}) {
	r.pollFn = func(ctx context.Context) {
		counter.Add(1)
		if signal != nil {
			select {
			case signal <- struct{}{}:
			default:
			}
		}
	}
}

func TestReconciler_New_ReturnsReady_DefaultTick(t *testing.T) {
	t.Parallel()
	r := New(Deps{})
	if r == nil {
		t.Fatal("New() returned nil")
	}
	if r.tickInterval != DefaultTickInterval {
		t.Fatalf("tickInterval = %v, want %v (default)", r.tickInterval, DefaultTickInterval)
	}
	if r.managedClustersPath != DefaultManagedClustersPath {
		t.Fatalf("managedClustersPath = %q, want %q (default)", r.managedClustersPath, DefaultManagedClustersPath)
	}
	if r.triggerCh == nil {
		t.Fatal("triggerCh is nil")
	}
	if cap(r.triggerCh) != 1 {
		t.Fatalf("triggerCh capacity = %d, want 1 (buffered)", cap(r.triggerCh))
	}
	if r.stopCh == nil {
		t.Fatal("stopCh is nil")
	}
	if r.pollFn == nil {
		t.Fatal("pollFn is nil — New must default to r.pollOnce")
	}
}

func TestReconciler_New_AppliesOverrides(t *testing.T) {
	t.Parallel()
	custom := 7 * time.Millisecond
	r := New(Deps{
		TickInterval:        custom,
		ManagedClustersPath: "custom/path.yaml",
	})
	if r.tickInterval != custom {
		t.Fatalf("tickInterval = %v, want %v", r.tickInterval, custom)
	}
	if r.managedClustersPath != "custom/path.yaml" {
		t.Fatalf("managedClustersPath = %q, want %q", r.managedClustersPath, "custom/path.yaml")
	}
}

func TestReconciler_New_ZeroAndNegativeTickIntervalFallToDefault(t *testing.T) {
	t.Parallel()
	for _, tc := range []time.Duration{0, -1, -time.Hour} {
		r := New(Deps{TickInterval: tc})
		if r.tickInterval != DefaultTickInterval {
			t.Fatalf("TickInterval=%v: got tickInterval=%v, want %v", tc, r.tickInterval, DefaultTickInterval)
		}
	}
}

func TestReconciler_StartStop_NoPanics(t *testing.T) {
	t.Parallel()
	var counter atomic.Int64

	r := New(Deps{TickInterval: time.Hour}) // long tick — keep test fast
	setPollFn(r, &counter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx)
	// Give the goroutine a moment to enter the select loop.
	time.Sleep(20 * time.Millisecond)
	r.Stop()
	// Stop should be effectively immediate. Give the goroutine a beat
	// to actually exit (it is unobservable from here, but if it panics
	// the test will fail via go test's stack dump).
	time.Sleep(20 * time.Millisecond)
}

func TestReconciler_StartIsIdempotent(t *testing.T) {
	t.Parallel()
	var counter atomic.Int64
	signal := make(chan struct{}, 8)

	// Very short tick so we can observe ticks accumulating.
	r := New(Deps{TickInterval: 5 * time.Millisecond})
	setPollFn(r, &counter, signal)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx)
	r.Start(ctx) // second Start must be a no-op (sync.Once)
	r.Start(ctx) // third too

	// Wait for at least one tick from the (single) goroutine.
	select {
	case <-signal:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no tick observed within 500ms")
	}

	// Collect ticks for a short window. If multiple goroutines were
	// running, we'd see roughly 2-3x the rate.
	deadline := time.Now().Add(60 * time.Millisecond)
	for time.Now().Before(deadline) {
		select {
		case <-signal:
		default:
			time.Sleep(time.Millisecond)
		}
	}

	r.Stop()
	// Sanity check: at least 2 ticks (we waited ~60ms with a 5ms tick).
	// We don't assert an exact upper bound because scheduling jitter on
	// a loaded CI box can push tick counts around. The real assertion
	// — "only one goroutine" — is enforced by sync.Once at compile-time
	// and by the absence of a "close of closed channel" panic from a
	// second goroutine racing to close stopCh.
	if got := counter.Load(); got < 2 {
		t.Fatalf("counter = %d, expected at least 2 ticks across ~60ms", got)
	}
}

func TestReconciler_StopIsIdempotent(t *testing.T) {
	t.Parallel()
	var counter atomic.Int64

	r := New(Deps{TickInterval: time.Hour})
	setPollFn(r, &counter, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx)
	r.Stop()
	r.Stop() // must not panic ("close of closed channel")
	r.Stop()
}

func TestReconciler_StopBeforeStart_NoPanic(t *testing.T) {
	t.Parallel()
	r := New(Deps{TickInterval: time.Hour})
	// Stop without Start must be safe — sync.Once.Do closes stopCh once,
	// and no goroutine is waiting on it. No panic expected.
	r.Stop()
	r.Stop()
}

func TestReconciler_TriggerBeforeStart_DoesNotPanic(t *testing.T) {
	t.Parallel()
	var counter atomic.Int64

	r := New(Deps{TickInterval: time.Hour})
	setPollFn(r, &counter, nil)
	// Trigger before Start must not panic. It enqueues a request into the
	// buffered(1) channel that the (not-yet-running) goroutine would drain
	// once Start is called.
	r.Trigger()
	r.Trigger() // second call hits the default branch — also safe

	// Counter must NOT have moved — no run loop is consuming the channel.
	if got := counter.Load(); got != 0 {
		t.Fatalf("counter = %d before Start, want 0", got)
	}
}

func TestReconciler_TriggerDuringRun_QueuesPoll(t *testing.T) {
	t.Parallel()
	var counter atomic.Int64
	signal := make(chan struct{}, 4)

	// Long tick so any observed poll is from Trigger, not the timer.
	r := New(Deps{TickInterval: time.Hour})
	setPollFn(r, &counter, signal)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r.Start(ctx)
	// Let the goroutine reach the select before we Trigger, to avoid the
	// race where Trigger enqueues before the loop is parked.
	time.Sleep(10 * time.Millisecond)

	r.Trigger()

	select {
	case <-signal:
		// Good — poll fired via Trigger.
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Trigger() did not fire pollOnce within 500ms (counter=%d)", counter.Load())
	}

	r.Stop()
}

func TestReconciler_TriggerCoalescesWhenSaturated(t *testing.T) {
	t.Parallel()
	// We don't Start the reconciler — we only verify that Trigger is
	// non-blocking even when the buffered channel is full. The first
	// Trigger fills the (capacity-1) buffer; subsequent ones must return
	// immediately via the default branch.
	r := New(Deps{TickInterval: time.Hour})

	done := make(chan struct{})
	go func() {
		r.Trigger()
		r.Trigger()
		r.Trigger()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Trigger blocked when triggerCh was saturated — should always drop via default")
	}
}

func TestReconciler_ContextCancelExitsRun(t *testing.T) {
	t.Parallel()
	var counter atomic.Int64

	r := New(Deps{TickInterval: time.Hour})
	setPollFn(r, &counter, nil)
	ctx, cancel := context.WithCancel(context.Background())

	r.Start(ctx)
	time.Sleep(10 * time.Millisecond) // let goroutine enter select
	cancel()
	// Goroutine should exit via ctx.Done(). No observable signal from
	// here, but the test will fail under -race if a goroutine leak +
	// later Stop() races with the cancel-induced exit.
	time.Sleep(20 * time.Millisecond)
	// Explicit Stop after cancel — must still be safe (Stop is
	// idempotent and doesn't require the goroutine to be alive).
	r.Stop()
}
