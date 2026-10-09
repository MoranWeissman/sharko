package gitprovider

import (
	"context"
	"strings"
	"testing"
)

// countLines reports how many lines of msg exactly equal want.
func countLines(msg, want string) int {
	n := 0
	for _, l := range strings.Split(msg, "\n") {
		if strings.TrimRight(l, "\r") == want {
			n++
		}
	}
	return n
}

// TestAttributionApplyToMessage_DefaultSignsOffBot is the DCO-match invariant:
// a zero-value attribution (legacy/default path) MUST still sign off as the
// Sharko Bot identity, and that sign-off email MUST equal the author email
// commitAuthorFor produces. This equality is the whole point — DCO matches the
// commit author email against the Signed-off-by line.
func TestAttributionApplyToMessage_DefaultSignsOffBot(t *testing.T) {
	a := CommitAttribution{}
	got := a.ApplyToMessage("update foo")

	wantSignoff := "Signed-off-by: " + DefaultAuthorName + " <" + DefaultAuthorEmail + ">"
	if !strings.Contains(got, wantSignoff) {
		t.Errorf("default path missing bot sign-off:\n got: %q\nwant line: %q", got, wantSignoff)
	}
	if strings.Contains(got, "Co-authored-by") {
		t.Errorf("default path should have no co-author trailer, got %q", got)
	}

	// DCO-match invariant: the sign-off email == the author email the GitHub
	// provider stamps on the commit.
	author := commitAuthorFor(a)
	if got, want := author.GetEmail(), DefaultAuthorEmail; got != want {
		t.Errorf("commitAuthorFor email = %q, want %q", got, want)
	}
	signoffEmail := DefaultAuthorEmail
	if author.GetEmail() != signoffEmail {
		t.Errorf("sign-off email %q != author email %q (DCO would fail)", signoffEmail, author.GetEmail())
	}
}

// TestAttributionApplyToMessage_Tier1CoAuthor: no author override + a user
// co-author. Expect a Signed-off-by for BOTH Sharko Bot AND the user, plus the
// existing Co-authored-by for the user.
func TestAttributionApplyToMessage_Tier1CoAuthor(t *testing.T) {
	a := CommitAttribution{
		CoAuthorName:  "Alice Example",
		CoAuthorEmail: "alice@example.com",
	}
	got := a.ApplyToMessage("update foo")

	wantLines := []string{
		"Co-authored-by: Alice Example <alice@example.com>",
		"Signed-off-by: " + DefaultAuthorName + " <" + DefaultAuthorEmail + ">",
		"Signed-off-by: Alice Example <alice@example.com>",
	}
	for _, l := range wantLines {
		if !strings.Contains(got, l) {
			t.Errorf("Tier 1 message missing %q\nfull: %q", l, got)
		}
	}

	// commitAuthorFor falls back to the bot when there's no author override,
	// and the bot sign-off must match it (DCO-match for the author identity).
	if email := commitAuthorFor(a).GetEmail(); email != DefaultAuthorEmail {
		t.Errorf("Tier 1 author email = %q, want %q", email, DefaultAuthorEmail)
	}
}

// TestAttributionApplyToMessage_Tier2HappyPath: author override == user, no
// co-author. Expect a single Signed-off-by for the user and NO co-author
// trailer (existing suppression behavior preserved).
func TestAttributionApplyToMessage_Tier2HappyPath(t *testing.T) {
	a := CommitAttribution{
		AuthorName:  "Carol Dev",
		AuthorEmail: "carol@example.com",
	}
	got := a.ApplyToMessage("change widget")

	wantSignoff := "Signed-off-by: Carol Dev <carol@example.com>"
	if !strings.Contains(got, wantSignoff) {
		t.Errorf("Tier 2 missing user sign-off:\n got: %q\nwant: %q", got, wantSignoff)
	}
	if strings.Contains(got, "Co-authored-by") {
		t.Errorf("Tier 2 happy path should suppress co-author trailer, got %q", got)
	}
	if strings.Contains(got, DefaultAuthorEmail) {
		t.Errorf("Tier 2 should not sign off the bot when author is overridden, got %q", got)
	}

	// DCO-match: the override author is the commit author.
	if email := commitAuthorFor(a).GetEmail(); email != "carol@example.com" {
		t.Errorf("Tier 2 author email = %q, want carol@example.com", email)
	}
}

// TestAttributionApplyToMessage_Tier2WithCoAuthorFallback: a Tier 2 fallback
// where the service token authored but a different user triggered. Both the
// author and the co-author get signed off, and the co-author trailer is kept.
func TestAttributionApplyToMessage_Tier2WithCoAuthorFallback(t *testing.T) {
	a := CommitAttribution{
		AuthorName:    "Carol Dev",
		AuthorEmail:   "carol@example.com",
		CoAuthorName:  "Dave User",
		CoAuthorEmail: "dave@example.com",
	}
	got := a.ApplyToMessage("change widget")

	wantLines := []string{
		"Co-authored-by: Dave User <dave@example.com>",
		"Signed-off-by: Carol Dev <carol@example.com>",
		"Signed-off-by: Dave User <dave@example.com>",
	}
	for _, l := range wantLines {
		if !strings.Contains(got, l) {
			t.Errorf("Tier 2 fallback missing %q\nfull: %q", l, got)
		}
	}
}

// TestAttributionApplyToMessage_Idempotent: applying twice must not duplicate
// any trailer line.
func TestAttributionApplyToMessage_Idempotent(t *testing.T) {
	cases := []CommitAttribution{
		{}, // default/legacy
		{CoAuthorName: "Alice Example", CoAuthorEmail: "alice@example.com"}, // Tier 1
		{AuthorName: "Carol Dev", AuthorEmail: "carol@example.com"},         // Tier 2
		{
			AuthorName:    "Carol Dev",
			AuthorEmail:   "carol@example.com",
			CoAuthorName:  "Dave User",
			CoAuthorEmail: "dave@example.com",
		}, // Tier 2 fallback
	}
	for i, a := range cases {
		once := a.ApplyToMessage("do a thing")
		twice := a.ApplyToMessage(once)
		if once != twice {
			t.Errorf("case %d not idempotent:\n once:  %q\n twice: %q", i, once, twice)
		}
		// Spot-check: no trailer line appears more than once.
		for _, line := range strings.Split(twice, "\n") {
			l := strings.TrimRight(line, "\r")
			if !strings.HasPrefix(l, "Signed-off-by:") && !strings.HasPrefix(l, "Co-authored-by:") {
				continue
			}
			if n := countLines(twice, l); n != 1 {
				t.Errorf("case %d: trailer %q appears %d times, want 1\nfull: %q", i, l, n, twice)
			}
		}
	}
}

// TestAttributionApplyToMessage_CoAuthorEqualsAuthorEmail: when the co-author
// email equals the effective author email (case-insensitive), the co-author
// sign-off is deduped — only one Signed-off-by for that identity.
func TestAttributionApplyToMessage_CoAuthorEqualsAuthorEmail(t *testing.T) {
	a := CommitAttribution{
		AuthorName:    "Alice Example",
		AuthorEmail:   "alice@example.com",
		CoAuthorName:  "Alice Example",
		CoAuthorEmail: "ALICE@example.com", // same identity, different case
	}
	got := a.ApplyToMessage("change widget")
	if n := countLines(got, "Signed-off-by: Alice Example <alice@example.com>"); n != 1 {
		t.Errorf("expected exactly one sign-off for Alice, got %d\nfull: %q", n, got)
	}
	// Co-author trailer is also suppressed (same identity as author).
	if strings.Contains(got, "Co-authored-by") {
		t.Errorf("co-author trailer should be suppressed when == author, got %q", got)
	}
}

// TestAttributionApplyToMessage_TrailerBlockShape verifies the body / blank
// line / trailer-block layout for the Tier 1 case.
func TestAttributionApplyToMessage_TrailerBlockShape(t *testing.T) {
	a := CommitAttribution{
		CoAuthorName:  "Alice Example",
		CoAuthorEmail: "alice@example.com",
	}
	got := a.ApplyToMessage("update foo")
	want := "update foo\n\n" +
		"Co-authored-by: Alice Example <alice@example.com>\n" +
		"Signed-off-by: " + DefaultAuthorName + " <" + DefaultAuthorEmail + ">\n" +
		"Signed-off-by: Alice Example <alice@example.com>\n"
	if got != want {
		t.Errorf("trailer block mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// TestAttributionApplyToMessage_NormalisesTrailingNewlines: extra trailing
// blank lines collapse to a single blank separator before the trailer block.
func TestAttributionApplyToMessage_NormalisesTrailingNewlines(t *testing.T) {
	a := CommitAttribution{
		CoAuthorName:  "Bob",
		CoAuthorEmail: "bob@example.com",
	}
	got := a.ApplyToMessage("change\n\n\n")
	want := "change\n\n" +
		"Co-authored-by: Bob <bob@example.com>\n" +
		"Signed-off-by: " + DefaultAuthorName + " <" + DefaultAuthorEmail + ">\n" +
		"Signed-off-by: Bob <bob@example.com>\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEffectiveAuthor(t *testing.T) {
	// Default path → bot identity.
	name, email := CommitAttribution{}.EffectiveAuthor()
	if name != DefaultAuthorName || email != DefaultAuthorEmail {
		t.Errorf("default effective author = %q/%q, want %q/%q", name, email, DefaultAuthorName, DefaultAuthorEmail)
	}
	// Override path → the override identity.
	name, email = CommitAttribution{AuthorName: "Carol", AuthorEmail: "c@x.io"}.EffectiveAuthor()
	if name != "Carol" || email != "c@x.io" {
		t.Errorf("override effective author = %q/%q, want Carol/c@x.io", name, email)
	}
}

func TestWithAttributionRoundTrip(t *testing.T) {
	a := CommitAttribution{AuthorName: "Carol", AuthorEmail: "c@x.io"}
	ctx := WithAttribution(context.Background(), a)
	got := FromContext(ctx)
	if got.AuthorName != "Carol" || got.AuthorEmail != "c@x.io" {
		t.Errorf("round-trip failed: got %+v", got)
	}
}

func TestFromContext_NilSafe(t *testing.T) {
	if got := FromContext(nil); got.AuthorName != "" {
		t.Errorf("nil ctx should yield zero value, got %+v", got)
	}
	if got := FromContext(context.Background()); got.AuthorName != "" {
		t.Errorf("empty ctx should yield zero value, got %+v", got)
	}
}
