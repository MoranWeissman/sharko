package orchestrator

import (
	"errors"
	"strings"
	"testing"
)

// TestScanForSecrets_HitsEachPattern asserts that each entry in the
// `secretPatterns` list matches at least one canonical example. This is
// the AC for Story 7.1 — the regex list must catch the documented
// pattern families. If you add a new pattern, add a case here.
func TestScanForSecrets_HitsEachPattern(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string // expected SecretMatch.Pattern
	}{
		{name: "AWS access key", input: "awsKey: AKIAIOSFODNN7EXAMPLE", want: "AWS access key"},
		{name: "AWS session key", input: "awsSession: ASIA0123456789ABCDEF", want: "AWS access key"},
		{name: "GitHub fine-grained PAT", input: "token: github_pat_11AAAAAA0aaaaaaaaaaaaa_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: "GitHub fine-grained PAT"},
		{name: "GitHub classic PAT", input: "token: ghp_1234567890abcdefABCDEF1234567890abcdef", want: "GitHub classic PAT"},
		{name: "JWT", input: "id_token: eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.aBcDeFgHiJkLmNoPqRsTuVwXyZ", want: "JWT token"},
		{name: "PEM private key", input: "-----BEGIN OPENSSH PRIVATE KEY-----", want: "PEM private key"},
		{name: "Slack token", input: "slack: xoxb-1234567890-abcdefghijklm", want: "Slack token"},
		{name: "Google API key", input: "google_key: AIzaSyA-1234567890abcdefghij_klmnopqrstu", want: "Google API key"},
		{name: "API key assignment", input: "apiKey: \"abc123def456ghi789jkl\"", want: "API key / token / password assignment"},
		{name: "Password assignment with quotes", input: "password = 'a-very-long-password-here-with-stuff'", want: "API key / token / password assignment"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := ScanForSecrets([]byte(tc.input))
			if len(matches) == 0 {
				t.Fatalf("expected a match for input %q, got none", tc.input)
			}
			found := false
			for _, m := range matches {
				if m.Pattern == tc.want {
					found = true
				}
				if strings.Contains(m.Field, "AKIAIOSFODNN7EXAMPLE") || strings.Contains(m.Field, "ghp_1234567890abcdefABCDEF1234567890abcdef") {
					t.Errorf("redacted field still contains the raw secret: %q", m.Field)
				}
			}
			if !found {
				t.Errorf("expected pattern %q in matches, got %+v", tc.want, matches)
			}
		})
	}
}

// TestScanForSecrets_NoFalsePositiveOnPlaceholder confirms the 16-char
// floor: short Helm placeholder values like `password: changeme` should
// NOT trip the guard. This is critical — the guard fires too readily
// otherwise and blocks every chart.
func TestScanForSecrets_NoFalsePositiveOnPlaceholder(t *testing.T) {
	yaml := `# typical Helm chart values.yaml stub
replicaCount: 2
service:
  type: ClusterIP
auth:
  password: changeme
  user: admin
`
	matches := ScanForSecrets([]byte(yaml))
	if len(matches) > 0 {
		t.Errorf("expected no matches on plain Helm placeholders, got %+v", matches)
	}
}

// TestScanForSecrets_DeduplicatesByPatternAndField makes sure the same
// (pattern, field) hit doesn't dominate the summary if it appears
// multiple times.
func TestScanForSecrets_DeduplicatesByPatternAndField(t *testing.T) {
	yaml := strings.Repeat("apiKey: abc123def456ghi789jkl\n", 5)
	matches := ScanForSecrets([]byte(yaml))
	if len(matches) != 1 {
		t.Errorf("expected 1 deduplicated match, got %d: %+v", len(matches), matches)
	}
}

// TestScanForSecrets_RedactsValueInField confirms the guard never echoes
// a real secret back through the field summary.
func TestScanForSecrets_RedactsValueInField(t *testing.T) {
	yaml := `awsKey: AKIAIOSFODNN7EXAMPLE`
	matches := ScanForSecrets([]byte(yaml))
	if len(matches) == 0 {
		t.Fatalf("expected a match")
	}
	if strings.Contains(matches[0].Field, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("redacted field still contains the raw secret: %q", matches[0].Field)
	}
	if !strings.Contains(matches[0].Field, "***") {
		t.Errorf("expected `***` mask in redacted field, got %q", matches[0].Field)
	}
}

// TestScanForSecrets_EmptyInput is a guard against nil panics.
func TestScanForSecrets_EmptyInput(t *testing.T) {
	if got := ScanForSecrets(nil); got != nil {
		t.Errorf("expected nil for nil input, got %+v", got)
	}
	if got := ScanForSecrets([]byte("")); got != nil {
		t.Errorf("expected nil for empty input, got %+v", got)
	}
}

// TestSecretLeakError_TypeAssertion ensures callers can errors.As against
// the typed error — the seed flow relies on this to render the banner.
func TestSecretLeakError_TypeAssertion(t *testing.T) {
	err := error(&SecretLeakError{
		Matches: []SecretMatch{{Pattern: "AWS access key", Field: "awsKey: ***", Line: 1}},
	})
	var leak *SecretLeakError
	if !errors.As(err, &leak) {
		t.Fatalf("errors.As did not extract SecretLeakError")
	}
	if leak.Code() != "secret_detected_blocked" {
		t.Errorf("unexpected code: %q", leak.Code())
	}
	msg := leak.Error()
	if !strings.Contains(msg, "secret_detected_blocked") {
		t.Errorf("expected error message to contain code, got %q", msg)
	}
}
