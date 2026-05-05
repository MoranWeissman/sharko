package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/term"
)

// ---------------------------------------------------------------------------
// V124-2.4 — formatConnectionError categories
// ---------------------------------------------------------------------------

func TestFormatConnectionError_ConnectionRefused(t *testing.T) {
	// Real connection-refused error returned by http.Client.Post when no
	// server is listening on the target port. We construct the equivalent
	// chain by hand to keep the test hermetic. The outer *url.Error mirrors
	// what http.Client wraps the underlying *net.OpError in (review L5 —
	// the previous fixture used a bare *net.OpError, which short-circuited
	// the production unwrap chain and silently passed even if a regression
	// dropped errors.As-based unwrap from formatConnectionError).
	wrapped := &url.Error{
		Op:  "Post",
		URL: "http://wrong:1234/api/v1/auth/login",
		Err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
		},
	}

	out := formatConnectionError("http://wrong:1234", wrapped)
	msg := out.Error()

	if !strings.Contains(msg, "cannot reach Sharko server at http://wrong:1234") {
		t.Errorf("missing friendly server hint: %q", msg)
	}
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("missing connection-refused category: %q", msg)
	}
	if !strings.Contains(msg, "check that the --server URL is correct") {
		t.Errorf("missing actionable hint: %q", msg)
	}
	// Underlying error must be preserved for debugging.
	if !errors.Is(out, syscall.ECONNREFUSED) {
		t.Errorf("expected underlying ECONNREFUSED to be preserved via %%w; got: %v", out)
	}
}

func TestFormatConnectionError_DNSLookup(t *testing.T) {
	// Match production wrapping: http.Client → *url.Error → *net.OpError →
	// *net.DNSError. Without the outer *url.Error layer (review L5) the
	// fixture would not exercise the same unwrap depth as production.
	wrapped := &url.Error{
		Op:  "Post",
		URL: "http://no.such.host.invalid:8080/api/v1/auth/login",
		Err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: &net.DNSError{Name: "no.such.host.invalid", Err: "no such host", IsNotFound: true},
		},
	}

	out := formatConnectionError("http://no.such.host.invalid:8080", wrapped)
	msg := out.Error()

	if !strings.Contains(msg, "DNS lookup failed") {
		t.Errorf("missing DNS category: %q", msg)
	}
	if !strings.Contains(msg, "no.such.host.invalid") {
		t.Errorf("missing host in message: %q", msg)
	}
	var dns *net.DNSError
	if !errors.As(out, &dns) {
		t.Errorf("expected wrapped *net.DNSError; got: %v", out)
	}
}

func TestFormatConnectionError_GenericNetOpError(t *testing.T) {
	// Match production wrapping (review L5): the *net.OpError sits inside
	// a *url.Error returned by http.Client. errors.As must walk both layers
	// to reach *net.OpError, so the test fixture must reflect that depth.
	wrapped := &url.Error{
		Op:  "Post",
		URL: "http://slow:9999/api/v1/auth/login",
		Err: &net.OpError{
			Op:  "dial",
			Net: "tcp",
			Err: errors.New("i/o timeout"),
		},
	}

	out := formatConnectionError("http://slow:9999", wrapped)
	msg := out.Error()

	if !strings.Contains(msg, "cannot reach Sharko server") {
		t.Errorf("missing friendly hint: %q", msg)
	}
	if !strings.Contains(msg, "network error") {
		t.Errorf("missing network-error category: %q", msg)
	}
}

func TestFormatConnectionError_NonNetworkError(t *testing.T) {
	// Non-network errors fall through to the original wrap-and-pass-through
	// behavior — we should not erase context for unrelated errors.
	plain := errors.New("custom transport bug")
	out := formatConnectionError("http://x", plain)
	if !strings.Contains(out.Error(), "login request failed") {
		t.Errorf("expected fallback prefix; got: %q", out.Error())
	}
	if !errors.Is(out, plain) {
		t.Errorf("expected underlying plain err to be preserved; got: %v", out)
	}
}

func TestFormatConnectionError_NilPassthrough(t *testing.T) {
	if formatConnectionError("http://x", nil) != nil {
		t.Error("expected nil error to pass through unchanged")
	}
}

func TestIsConnectionRefused_Detection(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"errors.Is via OpError chain",
			&net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}}, true},
		// L5 — production-realistic wrapping: http.Client.Post returns
		// *url.Error{Err: *net.OpError{Err: *os.SyscallError{Err: ECONNREFUSED}}}.
		// errors.Is must walk all three layers — verify it does.
		{"errors.Is via url.Error -> OpError -> SyscallError",
			&url.Error{
				Op:  "Post",
				URL: "http://x:1/api/v1/auth/login",
				Err: &net.OpError{Err: &os.SyscallError{Err: syscall.ECONNREFUSED}},
			}, true},
		{"plain string fallback",
			errors.New("dial tcp 127.0.0.1:1234: connect: connection refused"), true},
		// L4 — case-insensitive fallback. Windows-style messages can capitalise
		// the substring; we should still detect it via the string-match
		// fallback even when the syscall errno chain isn't preserved.
		{"mixed-case Windows-style message",
			errors.New("No connection could be made because the target machine actively refused it. Connection Refused."), true},
		{"upper-case fallback",
			errors.New("CONNECTION REFUSED"), true},
		{"nil", nil, false},
		{"unrelated", errors.New("something else"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isConnectionRefused(tc.err); got != tc.want {
				t.Errorf("isConnectionRefused(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// V124-2.6 — TTY restore semantics
// ---------------------------------------------------------------------------

// readPasswordSafe is the public entry point for the TTY-safe password
// prompt. We can't easily exercise the full ReadPassword path without a
// real PTY, but we CAN exercise the non-terminal branch (piped stdin),
// which is the path most CI runs hit. The terminal-state restore behaviour
// is tested implicitly by the build (the function must compile against the
// term API) and explicitly by a behavioural smoke at the end.

func TestReadPasswordSafe_NonTerminalBranch(t *testing.T) {
	// Pipe a password to stdin so term.IsTerminal returns false and we
	// take the bufio.NewReader fallback. This exercises the
	// "non-interactive caller" path without needing a PTY.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })

	go func() {
		fmt.Fprintln(w, "hunter2")
		w.Close()
	}()

	pw, err := readPasswordSafe("Password: ")
	if err != nil {
		t.Fatalf("readPasswordSafe: %v", err)
	}
	if pw != "hunter2" {
		t.Errorf("got password %q, want %q", pw, "hunter2")
	}
}

// ---------------------------------------------------------------------------
// V124-2.13 + V124-2.14 — behavioural TTY-restore coverage via terminalIO.
//
// V124-2.6 spec required asserting term.Restore is called on every exit
// path. The original test only exercised the non-terminal branch, which
// returns BEFORE GetState/defer — leaving the actual TTY-restore code with
// zero behavioural coverage (review finding H3).
//
// readPasswordSafeWith now takes an injectable terminalIO. The recordingTIO
// double below records the order of method calls and lets each test inject
// its own GetState / ReadPassword outcomes. We can therefore prove:
//
//   - the success path calls IsTerminal → GetState → ReadPassword → Restore
//   - the ReadPassword-error path STILL calls Restore (defer fires)
//   - the GetState-failure path BAILS without calling ReadPassword and
//     without calling Restore (M3 — there is no state to restore to, and
//     continuing would defeat the safety net entirely)
//   - the non-terminal branch still works through the wrapper
// ---------------------------------------------------------------------------

// recordingTIO is a controllable terminalIO double. Each method appends its
// name to calls so tests can assert exact invocation order.
type recordingTIO struct {
	isTerminal      bool
	getStateErr     error
	readPasswordOut []byte
	readPasswordErr error
	calls           []string
}

func (r *recordingTIO) IsTerminal(fd int) bool {
	r.calls = append(r.calls, "IsTerminal")
	return r.isTerminal
}

func (r *recordingTIO) GetState(fd int) (*term.State, error) {
	r.calls = append(r.calls, "GetState")
	if r.getStateErr != nil {
		return nil, r.getStateErr
	}
	// Return a non-nil but empty State so the defer-Restore path engages.
	return &term.State{}, nil
}

func (r *recordingTIO) Restore(fd int, state *term.State) error {
	r.calls = append(r.calls, "Restore")
	return nil
}

func (r *recordingTIO) ReadPassword(fd int) ([]byte, error) {
	r.calls = append(r.calls, "ReadPassword")
	return r.readPasswordOut, r.readPasswordErr
}

func TestReadPasswordSafeWith_SuccessPath_RestoresAfterReadPassword(t *testing.T) {
	tio := &recordingTIO{
		isTerminal:      true,
		readPasswordOut: []byte("hunter2"),
	}
	pw, err := readPasswordSafeWith(tio, "Password: ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pw != "hunter2" {
		t.Errorf("got password %q, want %q", pw, "hunter2")
	}
	want := []string{"IsTerminal", "GetState", "ReadPassword", "Restore"}
	if !reflect.DeepEqual(tio.calls, want) {
		t.Errorf("call order = %v, want %v", tio.calls, want)
	}
}

func TestReadPasswordSafeWith_ReadPasswordError_StillRestores(t *testing.T) {
	// EOF mid-read (e.g. user hit Ctrl-D) — the defer must still fire so
	// the TTY is restored to cooked mode. This is the BUG-006 case the
	// original V124-2.6 fix was written for.
	tio := &recordingTIO{
		isTerminal:      true,
		readPasswordErr: io.EOF,
	}
	_, err := readPasswordSafeWith(tio, "Password: ")
	if err == nil {
		t.Fatal("expected error from ReadPassword EOF, got nil")
	}
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected wrapped io.EOF, got: %v", err)
	}
	want := []string{"IsTerminal", "GetState", "ReadPassword", "Restore"}
	if !reflect.DeepEqual(tio.calls, want) {
		t.Errorf("call order = %v, want %v (Restore must fire on error path via defer)", tio.calls, want)
	}
}

func TestReadPasswordSafeWith_GetStateError_BailsBeforeReadPassword(t *testing.T) {
	// M3 — if GetState fails we cannot install the safety-net defer, so
	// continuing into ReadPassword would leave the TTY at the mercy of
	// term.ReadPassword's own restore (the very thing BUG-006 said couldn't
	// be trusted). Bail loudly instead.
	getStateErr := errors.New("ioctl TIOCGETA: inappropriate ioctl for device")
	tio := &recordingTIO{
		isTerminal:  true,
		getStateErr: getStateErr,
	}
	_, err := readPasswordSafeWith(tio, "Password: ")
	if err == nil {
		t.Fatal("expected error when GetState fails, got nil")
	}
	if !errors.Is(err, getStateErr) {
		t.Errorf("expected wrapped GetState err, got: %v", err)
	}
	if !strings.Contains(err.Error(), "snapshot terminal state") {
		t.Errorf("error does not explain the bail reason: %v", err)
	}
	// CRITICAL: ReadPassword must NOT be called (no state captured → no
	// safety net → don't risk the TTY) and Restore must NOT be called
	// (nothing to restore to).
	want := []string{"IsTerminal", "GetState"}
	if !reflect.DeepEqual(tio.calls, want) {
		t.Errorf("call order = %v, want %v (must bail before ReadPassword/Restore)", tio.calls, want)
	}
}

func TestReadPasswordSafeWith_NonTerminalBranch_StillWorks(t *testing.T) {
	// The non-terminal path doesn't go through GetState/Restore — it uses
	// bufio against os.Stdin. We can't easily inject the bufio reader
	// without further refactoring, so we verify the call shape (only
	// IsTerminal is invoked on the terminalIO) and let
	// TestReadPasswordSafe_NonTerminalBranch above cover the actual stdin
	// read against real os.Pipe.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	origStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = origStdin })

	go func() {
		fmt.Fprintln(w, "piped-secret")
		w.Close()
	}()

	tio := &recordingTIO{isTerminal: false}
	pw, err := readPasswordSafeWith(tio, "Password: ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pw != "piped-secret" {
		t.Errorf("got password %q, want %q", pw, "piped-secret")
	}
	want := []string{"IsTerminal"}
	if !reflect.DeepEqual(tio.calls, want) {
		t.Errorf("call order = %v, want %v (non-terminal path must NOT touch GetState/ReadPassword/Restore)", tio.calls, want)
	}
}
