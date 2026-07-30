package api

import (
	"errors"
	"strings"
	"testing"
)

func TestPlainConnectionError(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		err      error
		wantNone bool
	}{
		{name: "nil error returns empty", kind: "git", err: nil, wantNone: true},
		{name: "git network failure", kind: "git", err: errors.New("dial tcp 1.2.3.4:443: connect: connection refused")},
		{name: "git auth failure", kind: "git", err: errors.New("401 unauthorized")},
		{name: "argocd forbidden", kind: "argocd", err: errors.New("403 forbidden")},
		{name: "vault unknown failure", kind: "vault", err: errors.New("some AWS SDK internal error XYZ123")},
		{name: "unrecognized kind falls back to generic wording", kind: "mystery", err: errors.New("boom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := plainConnectionError(tt.kind, tt.err)
			if tt.wantNone {
				if got != "" {
					t.Fatalf("expected empty message for nil error, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected a non-empty plain message")
			}
			// The whole point: raw Go/SDK error text must never leak through.
			if strings.Contains(got, tt.err.Error()) {
				t.Fatalf("plain message leaked the raw error text: %q", got)
			}
			if !strings.HasPrefix(got, "Sharko can't reach") && !strings.HasPrefix(got, "Sharko can") {
				t.Fatalf("expected message to open in plain first-person Sharko voice, got %q", got)
			}
		})
	}
}
