// Package gitprovider — commit attribution context.
//
// Sharko's tiered attribution model (v1.20) requires every Git commit to
// surface the human who triggered the action — either as the commit author
// (Tier 2 with per-user PAT) or as a `Co-authored-by:` trailer (Tier 1, or
// Tier 2 fallback). The CommitAttribution struct travels via ctx so
// providers (GitHub, Azure DevOps) can render it consistently without
// every call site needing to construct provider-specific commit metadata.
package gitprovider

import (
	"context"
	"strings"
)

// DefaultAuthorName and DefaultAuthorEmail are the legacy "Sharko Bot"
// identity used for the commit author whenever an attribution carries no
// explicit author override. This is the single source of truth for the bot
// identity: both commitAuthorFor (which sets the Git commit author) and the
// Signed-off-by sign-off logic read these constants, so the commit author
// email and the DCO sign-off email can never drift apart. A DCO check
// requires the author's email to be matched exactly by a Signed-off-by line,
// so they MUST stay identical.
const (
	DefaultAuthorName  = "Sharko Bot"
	DefaultAuthorEmail = "sharko-bot@users.noreply.github.com"
)

// CommitAttribution describes how a Git commit should be authored and
// trailed. It is attached to a request context with WithAttribution and
// read by provider implementations when constructing commit objects.
//
// All fields are optional. When CommitAttribution is absent or empty the
// providers fall back to the legacy "Sharko Bot" identity. Every generated
// commit message carries a Signed-off-by trailer for the effective author
// (DCO compliance); a co-author, when present, is signed off too.
type CommitAttribution struct {
	// AuthorName / AuthorEmail set the Git commit author. Leave both empty
	// to keep the legacy "Sharko Bot <sharko-bot@users.noreply.github.com>"
	// author. Set to the user's GitHub identity when a per-user PAT is
	// being used (Tier 2 happy path).
	AuthorName  string
	AuthorEmail string

	// CoAuthorName / CoAuthorEmail control the `Co-authored-by:` trailer
	// appended to the commit message. Set these to the user's identity for
	// Tier 1 (operational) commits, or for Tier 2 fallback commits where
	// the service token authored the commit but the user triggered it.
	//
	// When AuthorName / AuthorEmail are set to the same identity (Tier 2
	// happy path), the trailer is suppressed to avoid attributing the same
	// person twice.
	CoAuthorName  string
	CoAuthorEmail string
}

// HasAuthor reports whether the attribution overrides the default author.
func (a CommitAttribution) HasAuthor() bool {
	return a.AuthorName != "" && a.AuthorEmail != ""
}

// EffectiveAuthor returns the (name, email) that will be used as the Git
// commit author: the override author when HasAuthor() is true, otherwise the
// default "Sharko Bot" identity. This is the identity the Signed-off-by
// trailer signs off, and it matches what commitAuthorFor sets as the commit
// author — keeping the DCO sign-off email aligned with the author email.
func (a CommitAttribution) EffectiveAuthor() (name, email string) {
	if a.HasAuthor() {
		return a.AuthorName, a.AuthorEmail
	}
	return DefaultAuthorName, DefaultAuthorEmail
}

// HasCoAuthor reports whether a Co-authored-by trailer should be added.
// Suppressed when the co-author equals the author (already-attributed).
func (a CommitAttribution) HasCoAuthor() bool {
	if a.CoAuthorName == "" || a.CoAuthorEmail == "" {
		return false
	}
	if a.HasAuthor() && strings.EqualFold(a.AuthorEmail, a.CoAuthorEmail) {
		return false
	}
	return true
}

// CoAuthorTrailer returns the canonical `Co-authored-by:` trailer line for
// this attribution, or "" if none should be added. The format matches
// GitHub's parser exactly: `Co-authored-by: Name <email>`.
func (a CommitAttribution) CoAuthorTrailer() string {
	if !a.HasCoAuthor() {
		return ""
	}
	return "Co-authored-by: " + a.CoAuthorName + " <" + a.CoAuthorEmail + ">"
}

// ApplyToMessage returns the commit message with a well-formed trailer block
// appended (body, one blank line, then trailer lines), per the Git trailer
// convention. The trailer block contains, in order:
//
//	Co-authored-by: <co-author>   (only when a co-author applies)
//	Signed-off-by:  <effective author>  (always — DCO compliance)
//	Signed-off-by:  <co-author>   (when a co-author applies and differs from author)
//
// The effective-author sign-off email matches the commit author the provider
// sets (see commitAuthorFor / EffectiveAuthor), so DCO-enforced repos accept
// the commit. The call is idempotent: a trailer line already present in msg is
// not duplicated, so applying ApplyToMessage twice yields the same result.
func (a CommitAttribution) ApplyToMessage(msg string) string {
	// Build the ordered list of desired trailer lines.
	var trailers []string
	if t := a.CoAuthorTrailer(); t != "" {
		trailers = append(trailers, t)
	}

	authorName, authorEmail := a.EffectiveAuthor()
	trailers = append(trailers, signoffTrailer(authorName, authorEmail))

	if a.HasCoAuthor() {
		// The co-author needs its own sign-off for DCO too — unless it is the
		// same identity as the effective author (case-insensitive on email).
		if !strings.EqualFold(a.CoAuthorEmail, authorEmail) {
			trailers = append(trailers, signoffTrailer(a.CoAuthorName, a.CoAuthorEmail))
		}
	}

	// Drop any trailer already present in the message (idempotency guard).
	var toAppend []string
	for _, t := range trailers {
		if !containsTrailerLine(msg, t) {
			toAppend = append(toAppend, t)
		}
	}
	if len(toAppend) == 0 {
		return msg
	}

	msg = strings.TrimRight(msg, "\n")
	// Collapse any existing trailing blank lines to a single blank separator,
	// then emit the new trailer lines.
	return msg + "\n\n" + strings.Join(toAppend, "\n") + "\n"
}

// signoffTrailer returns a canonical DCO `Signed-off-by:` trailer line.
func signoffTrailer(name, email string) string {
	return "Signed-off-by: " + name + " <" + email + ">"
}

// containsTrailerLine reports whether msg already contains the given trailer
// line as a standalone line (start-of-line, exact text). This keeps the
// idempotency guard from matching a substring buried inside the body.
func containsTrailerLine(msg, line string) bool {
	for _, l := range strings.Split(msg, "\n") {
		if strings.TrimRight(l, "\r") == line {
			return true
		}
	}
	return false
}

type ctxKey struct{}

// WithAttribution attaches a CommitAttribution to ctx. Providers read this
// via FromContext during write operations.
func WithAttribution(ctx context.Context, attr CommitAttribution) context.Context {
	return context.WithValue(ctx, ctxKey{}, attr)
}

// FromContext returns the CommitAttribution attached to ctx, or a zero value
// if none is present. Zero value preserves the legacy "Sharko Bot" identity.
func FromContext(ctx context.Context) CommitAttribution {
	if ctx == nil {
		return CommitAttribution{}
	}
	a, _ := ctx.Value(ctxKey{}).(CommitAttribution)
	return a
}
