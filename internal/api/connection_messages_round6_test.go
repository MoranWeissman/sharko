package api

// connection_messages_round6_test.go — exact-text tests for Round 6 user-facing
// sentences and the swagger descriptions.
//
// These tests pin the exact wording the product owner signed off on 2026-08-13.
// They also assert that the old phrasing ("stored sign-in details",
// "independently stored copy", "cannot mint", etc.) is absent from every new
// sentence.
//
// Why these tests exist: every sentence in Round 6 drifted because it had no
// test pinning its text. A sentence nothing pins is free to keep saying what
// the code used to do. Pinning only a fragment let a break that dropped half
// the sentence sail through Round 5 until the whole thing was pinned.

import (
	"os"
	"strings"
	"testing"
)

// containsFold reports whether haystack contains needle, IGNORING CASE.
//
// Every "this phrase is banned" check in this file uses it, and none of them
// uses strings.Contains.
//
// Why: the bans below are written in the wording that was revoked, and several
// of them contain a lowercase "git" — "from git plus", "the configured git
// branch plus". This whole round capitalised Git everywhere. So the exact
// phrase these tests ban is a phrase the code can no longer be written in,
// while the capitalised form of the same revoked sentence — the form somebody
// would actually reintroduce today — slipped straight past. A ban nobody can
// trip is not a ban.
//
// The same hole covered "Sharko intends" versus "sharko intends". The
// TypeScript half of this ban (ui/src/__tests__/bannedWordingSweep.test.ts)
// has always lowercased both sides; this is the Go half catching up.
//
// The positive "the sentence must say this" assertions are deliberately left
// case-SENSITIVE: those pin exact signed-off wording, and capitalisation is
// part of what they pin.
func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

// TestFailBackendRead_ExactText pins the full text of failBackendRead, which is
// used in connection comparison and connection repair when the secrets backend
// cannot be read.
//
// Product owner signed off on this wording 2026-08-13.
func TestFailBackendRead_ExactText(t *testing.T) {
	want := "Sharko could not read this cluster's configured credentials source from the secrets backend, so it could not work out what the connection should look like. Check the secrets backend connection and try again."

	if failBackendRead != want {
		t.Errorf("failBackendRead text does not match signed-off wording.\nGot:  %q\nWant: %q", failBackendRead, want)
	}

	// Assert the old phrase is absent.
	if containsFold(failBackendRead, "stored sign-in details") {
		t.Errorf("failBackendRead must not say 'stored sign-in details', got %q", failBackendRead)
	}

	// Assert other banned phrasings are absent.
	bannedPhrases := []string{
		"independently stored copy",
		"cannot mint",
		"mints a fresh token on every fetch",
		"every fetch",
		"no query parameter is read",
	}
	for _, banned := range bannedPhrases {
		if containsFold(failBackendRead, banned) {
			t.Errorf("failBackendRead must not contain %q, got %q", banned, failBackendRead)
		}
	}
}

// TestFailCredsUnavailable_ExactText pins the full text of
// failCredsUnavailable, which is returned when the credentials router is nil.
//
// Product owner signed off on this wording 2026-08-13.
func TestFailCredsUnavailable_ExactText(t *testing.T) {
	want := "Sharko could not read this cluster's configured credentials source, so the check did not finish."

	if failCredsUnavailable != want {
		t.Errorf("failCredsUnavailable text does not match signed-off wording.\nGot:  %q\nWant: %q", failCredsUnavailable, want)
	}

	// Assert the old phrase is absent.
	if containsFold(failCredsUnavailable, "stored sign-in details") {
		t.Errorf("failCredsUnavailable must not say 'stored sign-in details', got %q", failCredsUnavailable)
	}

	// Assert other banned phrasings are absent.
	bannedPhrases := []string{
		"independently stored copy",
		"cannot mint",
		"mints a fresh token on every fetch",
		"every fetch",
		"no query parameter is read",
	}
	for _, banned := range bannedPhrases {
		if containsFold(failCredsUnavailable, banned) {
			t.Errorf("failCredsUnavailable must not contain %q, got %q", banned, failCredsUnavailable)
		}
	}
}

// TestAuditRepairCredentialFailure_ExactText pins the audit line text used
// when a repair fails to read the configured credentials source.
//
// Product owner signed off on this wording 2026-08-13.
func TestAuditRepairCredentialFailure_ExactText(t *testing.T) {
	// The audit line is hardcoded in auditRepairCredentialFailure at
	// internal/api/connection_repair.go:866. We verify by reading the source
	// file and checking the exact text is present.
	want := "failed: could not read the configured credentials source"

	// Read the source file to verify the text.
	sourceFile := "connection_repair.go"
	content, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", sourceFile, err)
	}

	sourceText := string(content)

	// The wanted text must be present.
	if !strings.Contains(sourceText, want) {
		t.Errorf("connection_repair.go must contain audit line %q", want)
	}

	// The old phrase must be absent.
	oldPhrase := "failed: could not read the stored sign-in details"
	if containsFold(sourceText, oldPhrase) {
		t.Errorf("connection_repair.go must not contain the old audit line %q", oldPhrase)
	}

	// Extract the audit line from auditRepairCredentialFailure to check for
	// banned phrases. Find the function first, then the audit line within it.
	funcStart := strings.Index(sourceText, "func (s *Server) auditRepairCredentialFailure(")
	if funcStart == -1 {
		t.Fatal("could not find auditRepairCredentialFailure function")
	}

	// Find the audit call within this function. (Ruling f renamed the
	// helper: a repair that could not read the credentials source FAILED, it
	// did not complete, so it files under the failure event.)
	funcText := sourceText[funcStart:]
	auditLineStart := strings.Index(funcText, `s.failedRepairAudit(r, cluster, "failed:`)
	if auditLineStart == -1 {
		t.Fatal("could not find the failedRepairAudit call in auditRepairCredentialFailure")
	}

	// Extract just the string literal.
	stringStart := strings.Index(funcText[auditLineStart:], `"failed:`)
	if stringStart == -1 {
		t.Fatal("could not find string start")
	}
	stringEnd := strings.Index(funcText[auditLineStart+stringStart+1:], `"`)
	if stringEnd == -1 {
		t.Fatal("could not find string end")
	}

	actualAuditLine := funcText[auditLineStart+stringStart : auditLineStart+stringStart+1+stringEnd+1]
	t.Logf("Extracted audit line: %s", actualAuditLine)

	// Check banned phrases in the actual audit line.
	bannedPhrases := []string{
		"independently stored copy",
		"cannot mint",
		"mints a fresh token on every fetch",
	}
	for _, banned := range bannedPhrases {
		if containsFold(actualAuditLine, banned) {
			t.Errorf("audit line must not contain %q, but found it in: %s", banned, actualAuditLine)
		}
	}
}

// TestConnectionRepairSwaggerDescription_ExactTextAndSource verifies the repair
// endpoint's @Description annotation contains the signed-off wording and that
// the source of truth is the Go annotation, not the generated swagger files.
//
// Product owner signed off on this wording 2026-08-13.
func TestConnectionRepairSwaggerDescription_ExactTextAndSource(t *testing.T) {
	// The authoritative source is the Go annotation in
	// internal/api/connection_repair.go at line 169 (@Description).
	// docs/swagger is generated from it.

	sourceFile := "connection_repair.go"
	content, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", sourceFile, err)
	}

	sourceText := string(content)

	// The opening must say "configured credentials source held outside the
	// connection" and explain the EKS case.
	wantOpening := "the cluster's configured credentials source held outside the connection. For an EKS cluster that source is the cluster's own details rather than a reusable sign-in credential, and the write creates exactly one short-lived sign-in token from it — the read-only check creates none."

	// The re-read sentence must say "configured credentials source".
	wantReReadSentence := "The configured credentials source is re-read from the secrets backend and never read back out of the connection being repaired."

	// Verify the wanted text is present.
	if !strings.Contains(sourceText, wantOpening) {
		t.Errorf("connection_repair.go @Description must contain: %q", wantOpening)
	}

	if !strings.Contains(sourceText, wantReReadSentence) {
		t.Errorf("connection_repair.go @Description must contain: %q", wantReReadSentence)
	}

	// Verify banned phrases are absent. The last five are the revoked
	// split-authority framing (ruling 8): the desired connection comes from
	// git alone — credential values are RESOLVED from the referenced
	// provider, which is never a second source of truth next to git.
	bannedPhrases := []string{
		"independently stored copy of the cluster's sign-in details",
		"Sign-in details are re-fetched",
		// Ruling (b), 2026-08-19. The FRAGMENT, not a whole sentence: it
		// survived in five files at once precisely because every existing
		// ban listed complete sentences. Git defines the connection.
		"Sharko intends",
		"the configured git branch plus",
		"from git plus",
		"to match git and this cluster's configured credentials source",
		"authoritative for connection details",
		"two authorities",
		"hybrid ownership",
	}

	for _, banned := range bannedPhrases {
		if containsFold(sourceText, banned) {
			t.Errorf("connection_repair.go must not contain banned phrase %q", banned)
		}
	}

	t.Logf("Source of truth: internal/api/connection_repair.go @Description annotation")
}

// TestConnectionComparisonSwaggerDescription_ExactTextAndSource verifies the
// comparison endpoint's @Description annotation contains the signed-off wording.
//
// This sentence exists on main today, so fixing it widened the diff deliberately.
//
// Product owner signed off on this wording 2026-08-13.
func TestConnectionComparisonSwaggerDescription_ExactTextAndSource(t *testing.T) {
	// The authoritative source is the Go annotation in
	// internal/api/connection_comparison.go at line 194 (@Description).
	// docs/swagger is generated from it.

	sourceFile := "connection_comparison.go"
	content, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("failed to read %s: %v", sourceFile, err)
	}

	sourceText := string(content)

	// The opening must say "configured credentials source held outside the
	// connection" and explain the EKS case.
	wantText := "the cluster's configured credentials source held outside the connection — and compares it with the connection that is actually there. For an EKS cluster that source is the cluster's own details rather than a reusable sign-in credential, and the check creates no sign-in tokens."

	// Verify the wanted text is present.
	if !strings.Contains(sourceText, wantText) {
		t.Errorf("connection_comparison.go @Description must contain: %q", wantText)
	}

	// Verify banned phrases are absent. Beyond the round-6 phrase, the rest
	// are the revoked split-authority framing (ruling 8): the desired
	// connection comes from git alone — credential values are RESOLVED from
	// the referenced provider, which is never a second source of truth next
	// to git.
	comparisonBannedPhrases := []string{
		"an independently stored copy of the cluster's sign-in details",
		// Ruling (b), 2026-08-19 — the fragment, same reasoning as the
		// repair test above.
		"Sharko intends",
		"from git plus",
		"authoritative for connection details",
		"two authorities",
		"hybrid ownership",
	}
	for _, banned := range comparisonBannedPhrases {
		if containsFold(sourceText, banned) {
			t.Errorf("connection_comparison.go must not contain banned phrase %q", banned)
		}
	}

	t.Logf("Source of truth: internal/api/connection_comparison.go @Description annotation")
	t.Logf("Note: This file was not in the original review list but was found to have")
	t.Logf("the identical false claim, so it was fixed to prevent a round seven.")
}
