package main

import "testing"

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
