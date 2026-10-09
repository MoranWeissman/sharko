package models

import (
	"testing"
	"time"
)

func TestIsRegistrationPending(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 7, 8, 40, 53, 0, time.UTC)

	tests := []struct {
		name          string
		annotations   map[string]string
		wantPending   bool
		wantMalformed bool
	}{
		{
			name:        "nil annotations",
			annotations: nil,
			wantPending: false,
		},
		{
			name:        "no pending annotation",
			annotations: map[string]string{"other": "x"},
			wantPending: false,
		},
		{
			name:        "empty value",
			annotations: map[string]string{AnnotationRegistrationPending: ""},
			wantPending: false,
		},
		{
			name:        "within window",
			annotations: map[string]string{AnnotationRegistrationPending: RegistrationPendingTimestamp(now.Add(-1 * time.Second))},
			wantPending: true,
		},
		{
			name:        "just inside window",
			annotations: map[string]string{AnnotationRegistrationPending: RegistrationPendingTimestamp(now.Add(-RegistrationPendingGraceWindow + time.Second))},
			wantPending: true,
		},
		{
			name:        "expired (past window)",
			annotations: map[string]string{AnnotationRegistrationPending: RegistrationPendingTimestamp(now.Add(-RegistrationPendingGraceWindow - time.Second))},
			wantPending: false,
		},
		{
			name:          "malformed timestamp",
			annotations:   map[string]string{AnnotationRegistrationPending: "garbage"},
			wantPending:   false,
			wantMalformed: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pending, malformed := IsRegistrationPending(tc.annotations, now)
			if pending != tc.wantPending {
				t.Errorf("pending = %v, want %v", pending, tc.wantPending)
			}
			if malformed != tc.wantMalformed {
				t.Errorf("malformed = %v, want %v", malformed, tc.wantMalformed)
			}
		})
	}
}

func TestRegistrationPendingTimestamp_RFC3339UTC(t *testing.T) {
	t.Parallel()
	// A non-UTC input must be normalised to UTC RFC3339 so the value parses
	// back deterministically regardless of the writer's local zone.
	loc := time.FixedZone("UTC+5", 5*3600)
	in := time.Date(2026, 6, 7, 13, 40, 53, 0, loc)
	got := RegistrationPendingTimestamp(in)

	parsed, err := time.Parse(RegistrationPendingTimeFormat, got)
	if err != nil {
		t.Fatalf("emitted timestamp %q does not parse as RFC3339: %v", got, err)
	}
	if !parsed.Equal(in) {
		t.Fatalf("round-trip mismatch: emitted %q parsed to %v, want instant %v", got, parsed, in)
	}
}
