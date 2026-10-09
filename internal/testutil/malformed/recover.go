package malformed

import "testing"

// AssertNoPanic runs fn and turns any panic into a t.Fatalf — so a reader
// that panics on malformed input fails the specific test case with a clear
// message ("reader panicked on <case>: <recovered value>") instead of
// crashing the whole `go test` process (which would hide every other case
// in the same table). label identifies which corpus case was in play.
//
// Story 8.6 (v4 Wave 2): the whole point of the all-or-nothing audit is
// that malformed input is a normal, expected event a hostile or corrupted
// git repo can produce at any time — it must always come back as a Go
// error, never a runtime panic that takes the reconciler goroutine or an
// HTTP handler down with it.
func AssertNoPanic(t *testing.T, label string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked on %s: %v", label, r)
		}
	}()
	fn()
}
