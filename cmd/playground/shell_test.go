package main

import (
	"testing"
	"time"
)

// TestIsAlreadyExistsOutput covers the pure decision logic behind
// execGiteaCmd's fail-fast path (walk finding: re-running `make
// playground-up` over a half-built playground burned all 10 retries on the
// gitea admin-user bootstrap step instead of recognizing "user already
// exists" immediately). No cluster/pod required — this only exercises the
// output-matching logic that decides whether a gitea CLI failure is the
// DB-not-ready race (retry) or a deterministic already-exists outcome
// (fail fast).
func TestIsAlreadyExistsOutput(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		stderr string
		want   bool
	}{
		{
			name:   "gitea admin user create's real error text, in stderr",
			stdout: "",
			stderr: "user already exists [name: sharko-admin]",
			want:   true,
		},
		{
			name:   "already-exists text landing in stdout instead of stderr",
			stdout: "Error: user already exists [name: sharko-admin]",
			stderr: "",
			want:   true,
		},
		{
			name:   "mixed case still matches (robust, not exact-sentence)",
			stdout: "",
			stderr: "User Already Exists [name: sharko-admin]",
			want:   true,
		},
		{
			name:   "generic already-exists phrasing without the word 'user'",
			stdout: "",
			stderr: "access token name already exists",
			want:   true,
		},
		{
			name:   "DB-not-ready race — empty output, should keep retrying",
			stdout: "",
			stderr: "",
			want:   false,
		},
		{
			name:   "unrelated failure — should keep retrying",
			stdout: "",
			stderr: "connection refused",
			want:   false,
		},
		{
			name:   "swallowed pod error with no output at all",
			stdout: "",
			stderr: "command terminated with exit code 1",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyExistsOutput(tt.stdout, tt.stderr)
			if got != tt.want {
				t.Errorf("isAlreadyExistsOutput(%q, %q) = %v, want %v", tt.stdout, tt.stderr, got, tt.want)
			}
		})
	}
}

// TestOuterCmdTimeout covers the pure duration math behind the kill-race
// fix (task #147): the outer exec.CommandContext deadline handed to runCmd
// must always be strictly more than the inner --timeout flag a wrapped
// command was also given, by at least outerCmdTimeoutMargin — otherwise the
// outer Go context can SIGKILL the process at (or before) the exact instant
// the inner command's own timeout would have fired and explained itself.
func TestOuterCmdTimeout(t *testing.T) {
	tests := []struct {
		name         string
		innerTimeout time.Duration
		want         time.Duration
	}{
		{
			name:         "kubectl wait's 3-minute deployment timeout",
			innerTimeout: 3 * time.Minute,
			want:         3*time.Minute + 30*time.Second,
		},
		{
			name:         "a short 15-second inner timeout still gets the full margin",
			innerTimeout: 15 * time.Second,
			want:         45 * time.Second,
		},
		{
			name:         "zero inner timeout still yields a positive outer deadline",
			innerTimeout: 0,
			want:         30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := outerCmdTimeout(tt.innerTimeout)
			if got != tt.want {
				t.Errorf("outerCmdTimeout(%s) = %s, want %s", tt.innerTimeout, got, tt.want)
			}
			if got <= tt.innerTimeout {
				t.Errorf("outerCmdTimeout(%s) = %s must be strictly greater than the inner timeout, or the outer context can kill the command before its own --timeout fires", tt.innerTimeout, got)
			}
		})
	}
}
