//go:build e2e

// V2-1.1 — PhaseTimer + structured JSON emission for the 4 critical
// paths defined in phases.go.
//
// Design notes:
//   - Lives in the harness package so any lifecycle test can import it
//     alongside the existing apiclient_*.go helpers — no new package.
//   - Uses stdlib encoding/json + log/slog-style key-value emission;
//     no third-party deps.
//   - One PhaseTimer per measurement (Start / End pair). Re-using the
//     same value is forbidden (End panics if called twice) — this
//     catches bugs where a deferred End fires after an early-return
//     Start of the next phase.
//   - Emits to a configurable sink (default os.Stderr). The perf-tagged
//     test in tests/e2e/lifecycle/perf_test.go injects a buffer sink so
//     the stats helper can consume the emitted lines without scraping
//     the test process's stderr.
package harness

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// TimingSink is the destination for PhaseTimer's JSON lines.
//
// Defaults to os.Stderr. Override per-test via SetTimingSink to capture
// emissions into a *bytes.Buffer (the stats helper does this).
//
// A package-level var is intentional here despite the V125-1-8.0 lesson
// against package-level test seams: the perf-tagged tests run serially
// (the dispatch deliberately runs them one path at a time per harness),
// PhaseTimer emissions only happen from the test goroutine that booted
// the harness, and the global default sink mirrors how slog handles
// its default writer. If a future story needs parallel perf-tagged
// tests, promote the sink to a per-Sharko field.
var (
	timingSinkMu sync.Mutex
	timingSink   io.Writer = os.Stderr
)

// SetTimingSink replaces the destination PhaseTimer writes JSON lines
// to. Returns a restore function the caller MUST invoke (typically via
// defer) so the next test sees os.Stderr again.
//
// Example:
//
//	var buf bytes.Buffer
//	restore := harness.SetTimingSink(&buf)
//	defer restore()
//	// ... run perf measurements ...
//	// buf now holds one JSON line per Start/End pair.
func SetTimingSink(w io.Writer) (restore func()) {
	timingSinkMu.Lock()
	prev := timingSink
	timingSink = w
	timingSinkMu.Unlock()
	return func() {
		timingSinkMu.Lock()
		timingSink = prev
		timingSinkMu.Unlock()
	}
}

// timingLine is the JSON schema of a single emission. Field names are
// snake_case to match the rest of Sharko's structured logging surface
// (the V2-2 logging hardening epic will further consolidate this).
//
// duration_ms is a float64 (NOT int64) so sub-millisecond phases — and
// Sharko has plenty of them in the in-process harness — surface with
// meaningful precision rather than rounding to 0. The downstream
// quantile helper consumes float64 throughout so this is the natural
// shape end-to-end.
type timingLine struct {
	Path       string  `json:"path"`
	Phase      string  `json:"phase"`
	DurationMs float64 `json:"duration_ms"`
	// Iteration is set by the perf-tagged test runner so the stats
	// helper can distinguish "iteration N's enable_dry_run" from
	// "iteration N+1's enable_dry_run". Defaults to 0 when callers
	// don't bother — single-shot runs (manual smoke) don't need it.
	Iteration int `json:"iteration,omitempty"`
	// TimestampUnixMs is the wall-clock end time, useful when the
	// downstream stats helper needs to correlate emissions with other
	// log sources. Always populated.
	TimestampUnixMs int64 `json:"ts_ms"`
}

// PhaseTimer brackets a single phase of a critical path.
//
// Lifecycle:
//   - StartPhase returns a *PhaseTimer with the start wall-clock stamped.
//   - End() computes the duration, emits one JSON line to timingSink,
//     and marks the timer consumed.
//   - End() panics if called twice — catches the "deferred End fires
//     after the next phase Start" footgun.
//
// A nil *PhaseTimer is a no-op for End — convenient for tests that
// conditionally instrument (e.g. skip-graceful paths that bail before
// the measurement is meaningful):
//
//	pt := harness.StartPhase(harness.PathClusterRegistration, harness.PhaseUISubmit)
//	defer pt.End()
//	resp := admin.Do(...)
//	if resp.StatusCode == http.StatusServiceUnavailable {
//	    pt.Discard() // don't emit a 503 measurement
//	    t.Skip(...)
//	}
type PhaseTimer struct {
	path      string
	phase     string
	iteration int
	start     time.Time
	consumed  bool
	discarded bool
}

// StartPhase begins a new timing window. The returned *PhaseTimer's
// End() should be deferred immediately. Iteration defaults to 0; use
// StartPhaseN to set an explicit iteration number for the perf runner.
func StartPhase(path, phase string) *PhaseTimer {
	return StartPhaseN(path, phase, 0)
}

// StartPhaseN is StartPhase with an explicit iteration number — used
// by the perf-tagged runner so the stats helper can group emissions
// per iteration.
func StartPhaseN(path, phase string, iteration int) *PhaseTimer {
	return &PhaseTimer{
		path:      path,
		phase:     phase,
		iteration: iteration,
		start:     time.Now(),
	}
}

// End stamps the duration and emits one JSON line. Safe on a nil
// receiver (no-op). Panics if called twice on the same non-nil
// receiver.
func (pt *PhaseTimer) End() {
	if pt == nil {
		return
	}
	if pt.consumed {
		panic(fmt.Sprintf("PhaseTimer.End: double-End for path=%s phase=%s",
			pt.path, pt.phase))
	}
	pt.consumed = true
	if pt.discarded {
		return
	}

	end := time.Now()
	// nanoseconds → milliseconds as float64 preserves sub-ms precision
	// for the in-process harness's fast paths.
	durMs := float64(end.Sub(pt.start).Nanoseconds()) / 1e6
	line := timingLine{
		Path:            pt.path,
		Phase:           pt.phase,
		DurationMs:      durMs,
		Iteration:       pt.iteration,
		TimestampUnixMs: end.UnixMilli(),
	}

	// Marshal + write + newline. errors here are deliberately silent —
	// the perf measurement is best-effort observability; a flaky writer
	// must not fail the underlying test. Use a write-then-check pattern
	// if a future story needs to surface sink errors.
	timingSinkMu.Lock()
	sink := timingSink
	timingSinkMu.Unlock()
	if sink == nil {
		return
	}
	body, err := json.Marshal(line)
	if err != nil {
		// Should never happen — timingLine is plain primitives.
		return
	}
	_, _ = sink.Write(append(body, '\n'))
}

// Discard marks the timer as "do not emit". Useful for skip-graceful
// branches where the measurement is not meaningful (e.g. a 503 from a
// missing connection should not pollute the baseline).
//
// Must be called BEFORE End() — the End() call still fires (cleaning
// up the consumed flag) but skips the emission.
func (pt *PhaseTimer) Discard() {
	if pt == nil {
		return
	}
	pt.discarded = true
}
