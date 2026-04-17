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

// CommitAttribution describes how a Git commit should be authored and
// trailed. It is attached to a request context with WithAttribution and
// read by provider implementations when constructing commit objects.
//
// All fields are optional. When CommitAttribution is absent or empty the
// providers fall back to the legacy "Sharko Bot" identity with no trailer
// (existing behavior).
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

// ApplyToMessage returns the commit message with the Co-authored-by trailer
// appended (separated by a blank line per Git convention). When no co-author
// applies, the message is returned unchanged.
func (a CommitAttribution) ApplyToMessage(msg string) string {
	trailer := a.CoAuthorTrailer()
	if trailer == "" {
		return msg
	}
	msg = strings.TrimRight(msg, "\n")
	// Collapse any existing trailing blank lines to a single blank separator.
	return msg + "\n\n" + trailer + "\n"
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
