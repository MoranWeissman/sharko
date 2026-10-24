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

// EP3 dry run round S (S8, S11): a waiting mark carries its own pull
// request number and repository, and older values still read.
func TestParsePendingPRMark_RoundS(t *testing.T) {
	keep := true
	m := PendingPRMark{PR: 12, Repo: "acme/gitops", KeepOldLabels: &keep, At: "2026-10-24T10:00:00Z"}
	got, ok := ParsePendingPRMark(m.String())
	if !ok || got.PR != 12 || got.Repo != "acme/gitops" || got.KeepOldLabels == nil || !*got.KeepOldLabels {
		t.Fatalf("round trip lost data: %+v ok=%v", got, ok)
	}
	if at, ok := got.WrittenAt(); !ok || at.Year() != 2026 {
		t.Fatalf("WrittenAt = %v %v", at, ok)
	}
	if got, ok := ParsePendingPRMark("12"); !ok || got.PR != 12 || got.Repo != "" {
		t.Fatalf("old number form: %+v %v", got, ok)
	}
	if got, ok := ParsePendingPRMark("pending"); !ok || got.PR != 0 {
		t.Fatalf("old pending form: %+v %v", got, ok)
	}
	for _, bad := range []string{"", "0", "-3", "abc", "{bad"} {
		if _, ok := ParsePendingPRMark(bad); ok {
			t.Errorf("%q should not read as a mark", bad)
		}
	}
	if !PendingPRMarkNames(m.String(), 12) || PendingPRMarkNames(m.String(), 13) || PendingPRMarkNames("pending", 0) {
		t.Error("PendingPRMarkNames is wrong")
	}
}

func TestRepoKeyFromPRURL_RoundS(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Acme/GitOps/pull/12":                 "acme/gitops",
		"http://gitea.local:3000/acme/gitops/pulls/7":            "acme/gitops",
		"https://dev.azure.com/org/proj/_git/repo/pullrequest/4": "org/proj/repo",
		"https://git.example.com/sub/acme/gitops/pull/3":         "acme/gitops",
		"https://github.com/acme/gitops":                         "",
		"":                                                       "",
	}
	for in, want := range cases {
		if got := RepoKeyFromPRURL(in); got != want {
			t.Errorf("RepoKeyFromPRURL(%q) = %q, want %q", in, got, want)
		}
	}
	gh := GitRepoConfig{RepoURL: "https://github.com/Acme/GitOps.git"}
	if got := gh.RepoKey(); got != "acme/gitops" {
		t.Errorf("github RepoKey = %q", got)
	}
	az := GitRepoConfig{RepoURL: "https://dev.azure.com/org/proj/_git/repo"}
	if got := az.RepoKey(); got != "org/proj/repo" {
		t.Errorf("azure RepoKey = %q", got)
	}
	if !RepoKeysMatch("acme/gitops", "ACME/gitops") || RepoKeysMatch("acme/gitops", "acme/other") || RepoKeysMatch("", "") {
		t.Error("RepoKeysMatch is wrong")
	}
}
