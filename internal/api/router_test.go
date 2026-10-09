package api

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestStatusRecorder_Flush asserts that a *statusRecorder wrapping a
// Flusher writer satisfies the http.Flusher interface and forwards the
// call to the underlying writer.
//
// This is the regression guard for the production SSE bug
// (BUG-SSE / V2 Epic 7-1.12): the loggingMiddleware wraps every
// response writer in *statusRecorder; if the wrapper omits Flush(),
// handlers like GET /api/v1/audit/stream — which do
// `w.(http.Flusher)` — return 500 "streaming not supported" because
// the type assertion fails on every request.
func TestStatusRecorder_Flush(t *testing.T) {
	inner := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	sr := &statusRecorder{ResponseWriter: inner, statusCode: http.StatusOK}

	f, ok := any(sr).(http.Flusher)
	if !ok {
		t.Fatal("statusRecorder does not satisfy http.Flusher — SSE handlers will return 500")
	}
	f.Flush()
	if inner.flushed != 1 {
		t.Fatalf("Flush() did not forward to underlying writer; flushed=%d want 1", inner.flushed)
	}

	// Flush() against a writer that does not implement Flusher must be
	// a no-op, not a panic — test handlers wrap a bare httptest.Recorder.
	plain := &statusRecorder{ResponseWriter: httptest.NewRecorder(), statusCode: http.StatusOK}
	if pf, ok := any(plain).(http.Flusher); !ok {
		t.Fatal("statusRecorder does not satisfy http.Flusher (bare wrapper)")
	} else {
		// Should not panic.
		pf.Flush()
	}
}

// TestStatusRecorder_Hijack asserts that *statusRecorder satisfies
// http.Hijacker and forwards to the underlying writer when it implements
// the interface, and returns a clean error otherwise.
func TestStatusRecorder_Hijack(t *testing.T) {
	wantConn := &fakeConn{}
	wantBuf := bufio.NewReadWriter(bufio.NewReader(bytes.NewReader(nil)), bufio.NewWriter(&bytes.Buffer{}))

	inner := &hijackRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		conn:             wantConn,
		buf:              wantBuf,
	}
	sr := &statusRecorder{ResponseWriter: inner, statusCode: http.StatusOK}

	h, ok := any(sr).(http.Hijacker)
	if !ok {
		t.Fatal("statusRecorder does not satisfy http.Hijacker — WebSocket upgrades will fail")
	}
	gotConn, gotBuf, err := h.Hijack()
	if err != nil {
		t.Fatalf("Hijack() unexpected error: %v", err)
	}
	if gotConn != wantConn {
		t.Errorf("Hijack() conn=%v want %v", gotConn, wantConn)
	}
	if gotBuf != wantBuf {
		t.Errorf("Hijack() buf=%v want %v", gotBuf, wantBuf)
	}
	if inner.calls != 1 {
		t.Errorf("Hijack() did not forward to underlying writer; calls=%d want 1", inner.calls)
	}

	// Wrapping a non-Hijacker writer must surface a clean error rather
	// than panicking.
	plain := &statusRecorder{ResponseWriter: httptest.NewRecorder(), statusCode: http.StatusOK}
	ph, ok := any(plain).(http.Hijacker)
	if !ok {
		t.Fatal("statusRecorder does not satisfy http.Hijacker (bare wrapper)")
	}
	if _, _, err := ph.Hijack(); err == nil {
		t.Error("Hijack() on non-Hijacker inner: want error, got nil")
	}
}

// TestStatusRecorder_CloseNotify asserts that *statusRecorder satisfies
// http.CloseNotifier and forwards to the underlying writer when it
// implements the interface, and returns a non-nil channel otherwise.
//
//nolint:staticcheck // CloseNotifier is deprecated but downstream code still uses it
func TestStatusRecorder_CloseNotify(t *testing.T) {
	want := make(chan bool, 1)
	inner := &closeNotifyRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		ch:               want,
	}
	sr := &statusRecorder{ResponseWriter: inner, statusCode: http.StatusOK}

	cn, ok := any(sr).(http.CloseNotifier)
	if !ok {
		t.Fatal("statusRecorder does not satisfy http.CloseNotifier")
	}
	if got := cn.CloseNotify(); got != (<-chan bool)(want) {
		t.Errorf("CloseNotify() returned a different channel than the underlying writer")
	}
	if inner.calls != 1 {
		t.Errorf("CloseNotify() did not forward; calls=%d want 1", inner.calls)
	}

	// Non-CloseNotifier inner must yield a non-nil channel that never fires.
	plain := &statusRecorder{ResponseWriter: httptest.NewRecorder(), statusCode: http.StatusOK}
	pcn, ok := any(plain).(http.CloseNotifier)
	if !ok {
		t.Fatal("statusRecorder does not satisfy http.CloseNotifier (bare wrapper)")
	}
	if pcn.CloseNotify() == nil {
		t.Error("CloseNotify() returned nil channel; callers will block on a nil receive forever")
	}
}

// TestStatusRecorder_PreservesStatusCode confirms the original
// behaviour of the wrapper (capturing WriteHeader) is unchanged after
// adding the passthrough methods.
func TestStatusRecorder_PreservesStatusCode(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, statusCode: http.StatusOK}
	sr.WriteHeader(http.StatusTeapot)
	if sr.statusCode != http.StatusTeapot {
		t.Errorf("statusCode=%d want %d", sr.statusCode, http.StatusTeapot)
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("underlying rec.Code=%d want %d", rec.Code, http.StatusTeapot)
	}
}

// ---------------------------------------------------------------------------
// fakes
// ---------------------------------------------------------------------------

// flushRecorder embeds httptest.ResponseRecorder and counts Flush calls.
// httptest.ResponseRecorder does not implement http.Flusher on its own
// (verified by go vet / type assertion), so we add the method here.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flushRecorder) Flush() {
	f.flushed++
}

// hijackRecorder embeds httptest.ResponseRecorder and reports a fixed
// (conn, buf) tuple from Hijack. Counts calls so the test can assert
// the wrapper actually forwarded.
type hijackRecorder struct {
	*httptest.ResponseRecorder
	conn  net.Conn
	buf   *bufio.ReadWriter
	calls int
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.calls++
	return h.conn, h.buf, nil
}

// closeNotifyRecorder embeds httptest.ResponseRecorder and returns a
// pre-built channel from CloseNotify.
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
	ch    chan bool
	calls int
}

func (c *closeNotifyRecorder) CloseNotify() <-chan bool {
	c.calls++
	return c.ch
}

// fakeConn is a minimal net.Conn used as the Hijack return value. None of
// the methods are exercised — the test only checks pointer equality.
type fakeConn struct{}

func (fakeConn) Read(_ []byte) (int, error)         { return 0, errors.New("fakeConn: not implemented") }
func (fakeConn) Write(_ []byte) (int, error)        { return 0, errors.New("fakeConn: not implemented") }
func (fakeConn) Close() error                       { return nil }
func (fakeConn) LocalAddr() net.Addr                { return nil }
func (fakeConn) RemoteAddr() net.Addr               { return nil }
func (fakeConn) SetDeadline(_ time.Time) error      { return nil }
func (fakeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (fakeConn) SetWriteDeadline(_ time.Time) error { return nil }
