// Package service — tiered Git token resolution (v1.20).
//
// Implements the attribution tier model documented in
// docs/design/2026-04-16-attribution-and-permissions-model.md:
//
//   - Tier 1 (operational): always use the service token from the active
//     connection. Add a Co-authored-by trailer for the calling user.
//
//   - Tier 2 (configuration): prefer the calling user's personal GitHub PAT
//     (encrypted, stored on the user profile). When unavailable, fall back to
//     the service token AND set AttributionFallback=true so the API layer can
//     surface a UX nudge ("set up a personal PAT for proper attribution").
//
// The returned TokenResolution captures both the token and the metadata the
// caller needs to (a) build the right gitprovider.CommitAttribution and
// (b) record the resulting AttributionMode in the audit log.
package service

import (
	"context"
	"fmt"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// UserIdentity is the minimum data needed to author commits as a user.
type UserIdentity struct {
	Username    string // Sharko username (always set when ctx has an authed user)
	Email       string // best-effort: GitHub-derived if known, else "<username>@users.noreply.sharko"
	DisplayName string // best-effort: GitHub display name if known, else Username
}

// HasIdentity reports whether the identity has enough data to attribute a commit.
func (u UserIdentity) HasIdentity() bool {
	return u.Username != "" && u.Email != ""
}

// UserTokenLookup resolves a per-user GitHub PAT for the given username.
// Returns "" with a nil error when the user has no personal PAT configured.
type UserTokenLookup func(username string) (string, error)

// TokenResolution is the outcome of PickGitTokenForTier.
type TokenResolution struct {
	// Token is the actual token value to authenticate Git API calls with.
	Token string

	// AttributionMode describes how the resulting commit will be attributed.
	// Used by the audit log layer.
	AttributionMode audit.AttributionMode

	// AttributionFallback is true when the caller asked for Tier 2 attribution
	// but no per-user PAT was available — the service token is being used and
	// the UI should surface a "set up your PAT" nudge.
	AttributionFallback bool

	// CommitAuthor / CoAuthor are pre-built for convenience. Apply via
	// gitprovider.WithAttribution before calling provider write methods.
	Attribution gitprovider.CommitAttribution
}

// PickGitTokenForTier returns the token + attribution metadata for an action
// of the given tier, performed by the given user identity. The serviceToken
// and lookup parameters decouple this function from the API-layer plumbing
// that supplies them.
//
// Behaviour by tier:
//   - Tier 1, Auth, Webhook, Personal: always service token; Co-authored-by
//     trailer when an identity is available.
//   - Tier 2: try userLookup(user.Username); if it returns a non-empty token,
//     use that as the commit author (per_user mode); otherwise fall back to
//     service token + co-author trailer + AttributionFallback=true.
func PickGitTokenForTier(
	tier audit.Tier,
	user UserIdentity,
	serviceToken string,
	userLookup UserTokenLookup,
) (TokenResolution, error) {
	res := TokenResolution{Token: serviceToken}

	switch tier {
	case audit.Tier2:
		// Try per-user PAT first.
		if user.HasIdentity() && userLookup != nil {
			tok, err := userLookup(user.Username)
			if err != nil {
				return res, fmt.Errorf("lookup per-user token: %w", err)
			}
			if tok != "" {
				res.Token = tok
				res.AttributionMode = audit.AttributionPerUser
				res.Attribution = gitprovider.CommitAttribution{
					AuthorName:  user.DisplayName,
					AuthorEmail: user.Email,
					// Suppressed automatically when author == co-author
					CoAuthorName:  user.DisplayName,
					CoAuthorEmail: user.Email,
				}
				return res, nil
			}
		}
		// Fall back: service token, co-author trailer, flag the warning.
		res.AttributionFallback = true
		res.AttributionMode = attributionForServiceToken(user)
		res.Attribution = coAuthorOnly(user)
		return res, nil

	case audit.Tier1, audit.TierPersonal, audit.TierAuth, audit.TierWebhook:
		fallthrough
	default:
		res.AttributionMode = attributionForServiceToken(user)
		res.Attribution = coAuthorOnly(user)
		return res, nil
	}
}

func attributionForServiceToken(user UserIdentity) audit.AttributionMode {
	if user.HasIdentity() {
		return audit.AttributionCoAuthor
	}
	return audit.AttributionService
}

func coAuthorOnly(user UserIdentity) gitprovider.CommitAttribution {
	if !user.HasIdentity() {
		return gitprovider.CommitAttribution{} // legacy bot author, no trailer
	}
	return gitprovider.CommitAttribution{
		CoAuthorName:  user.DisplayName,
		CoAuthorEmail: user.Email,
	}
}

// PickGitTokenForRequest is a convenience wrapper used by the API layer:
// it pulls the active connection's service token, applies tier-aware
// resolution, and records the resulting AttributionMode + tier in the
// in-flight audit entry (when one is present on ctx).
//
// userLookup is typically a closure over the auth.Store + SHARKO_ENCRYPTION_KEY.
//
// Returns the resolved token + a context augmented with
// gitprovider.CommitAttribution so any provider call made with this ctx will
// pick up the right author + trailer.
func (s *ConnectionService) PickGitTokenForRequest(
	ctx context.Context,
	tier audit.Tier,
	user UserIdentity,
	userLookup UserTokenLookup,
) (context.Context, TokenResolution, error) {
	conn, err := s.getActiveConn()
	if err != nil {
		return ctx, TokenResolution{}, err
	}

	serviceToken := serviceTokenFromConnection(conn)

	res, err := PickGitTokenForTier(tier, user, serviceToken, userLookup)
	if err != nil {
		return ctx, res, err
	}

	// Attach commit attribution to ctx for downstream gitprovider calls.
	ctx = gitprovider.WithAttribution(ctx, res.Attribution)

	// Record on the in-flight audit entry, if one exists.
	audit.Enrich(ctx, audit.Fields{
		Tier:            tier,
		AttributionMode: res.AttributionMode,
	})

	return ctx, res, nil
}

// serviceTokenFromConnection extracts the appropriate service token field
// from the active connection based on its Git provider.
func serviceTokenFromConnection(conn *models.Connection) string {
	switch conn.Git.Provider {
	case models.GitProviderGitHub:
		return conn.Git.Token
	case models.GitProviderAzureDevOps:
		return conn.Git.PAT
	}
	return ""
}
