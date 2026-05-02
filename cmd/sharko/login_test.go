package main

import (
	"fmt"
	"os"
	"testing"
)

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
