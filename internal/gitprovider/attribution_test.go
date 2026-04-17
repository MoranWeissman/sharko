package gitprovider

import (
	"context"
	"strings"
	"testing"
)

func TestAttributionApplyToMessage_NoCoAuthor(t *testing.T) {
	a := CommitAttribution{}
	got := a.ApplyToMessage("update foo")
	if got != "update foo" {
		t.Errorf("expected unchanged message, got %q", got)
	}
}

func TestAttributionApplyToMessage_AddsTrailer(t *testing.T) {
	a := CommitAttribution{
		CoAuthorName:  "Alice Example",
		CoAuthorEmail: "alice@example.com",
	}
	got := a.ApplyToMessage("update foo")
	want := "update foo\n\nCo-authored-by: Alice Example <alice@example.com>\n"
	if got != want {
		t.Errorf("trailer mismatch:\n got: %q\nwant: %q", got, want)
	}
	// GitHub parser requires exactly the form "Co-authored-by: Name <email>"
	// — verify we satisfy the literal pattern.
	if !strings.Contains(got, "Co-authored-by: Alice Example <alice@example.com>") {
		t.Error("trailer not in canonical form")
	}
}

func TestAttributionApplyToMessage_SuppressesWhenAuthorEqualsCoAuthor(t *testing.T) {
	a := CommitAttribution{
		AuthorName:    "Alice Example",
		AuthorEmail:   "alice@example.com",
		CoAuthorName:  "Alice Example",
		CoAuthorEmail: "alice@example.com",
	}
	got := a.ApplyToMessage("change widget")
	if strings.Contains(got, "Co-authored-by") {
		t.Errorf("expected trailer suppressed when author == co-author, got %q", got)
	}
}

func TestAttributionApplyToMessage_NormalisesTrailingNewlines(t *testing.T) {
	a := CommitAttribution{
		CoAuthorName:  "Bob",
		CoAuthorEmail: "bob@example.com",
	}
	got := a.ApplyToMessage("change\n\n\n")
	want := "change\n\nCo-authored-by: Bob <bob@example.com>\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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
