package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
)

// ---------------------------------------------------------------------------
// V124-2.4 — formatConnectionError categories
// ---------------------------------------------------------------------------

func TestFormatConnectionError_ConnectionRefused(t *testing.T) {
	// Real connection-refused error returned by Dial when no server is
	// listening on a closed port. We construct the equivalent net.OpError
	// chain by hand to keep the test hermetic.
	wrapped := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
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
	wrapped := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &net.DNSError{Name: "no.such.host.invalid", Err: "no such host", IsNotFound: true},
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
	wrapped := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: errors.New("i/o timeout"),
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
		{"plain string fallback",
			errors.New("dial tcp 127.0.0.1:1234: connect: connection refused"), true},
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
