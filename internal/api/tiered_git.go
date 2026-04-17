package api

// tiered_git.go — wires the tier-aware Git token resolver into the API layer.
//
// Handlers that perform a Git write should obtain their gitprovider.GitProvider
// via Server.GitProviderForTier(ctx, r, tier) instead of the legacy
// connSvc.GetActiveGitProvider(). The returned ctx carries a CommitAttribution
// so all downstream provider calls render the right author + Co-authored-by
// trailer, and the in-flight audit entry is stamped with the resolved tier and
// AttributionMode.
//
// When the user is unknown (webhook, internal callers without an authed user)
// the resolver falls back to the service token with no trailer, exactly the
// pre-v1.20 behaviour.

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
)

// noreplyEmailDomain is used when we don't know the user's GitHub-verified
// email — keeps the trailer well-formed without leaking org-specific guesses.
const noreplyEmailDomain = "users.noreply.sharko"

// userIdentityFromRequest builds a UserIdentity from the auth headers set by
// basicAuthMiddleware. If no user is authenticated, returns a zero value.
func (s *Server) userIdentityFromRequest(r *http.Request) service.UserIdentity {
	username := r.Header.Get("X-Sharko-User")
	if username == "" || username == "anonymous" {
		return service.UserIdentity{}
	}
	return service.UserIdentity{
		Username:    username,
		DisplayName: username,
		Email:       username + "@" + noreplyEmailDomain,
	}
}

// userTokenLookup returns a closure that resolves a per-user PAT via the
// auth store using the configured encryption key. The closure is safe to
// call concurrently — the auth store handles its own locking.
func (s *Server) userTokenLookup() service.UserTokenLookup {
	encKey := os.Getenv("SHARKO_ENCRYPTION_KEY")
	store := s.authStore
	return func(username string) (string, error) {
		if encKey == "" {
			// No encryption key — local dev mode without K8s; per-user PATs
			// aren't usable. Treat as "no token" rather than erroring.
			return "", nil
		}
		return store.GetUserGitHubToken(username, encKey)
	}
}

// GitProviderForTier returns a GitProvider authenticated with the right token
// for the given tier, plus a context carrying the matching CommitAttribution
// (so downstream provider write calls render the correct author + trailer).
//
// The resulting context also has the audit entry stamped with the resolved
// AttributionMode and Tier, so the audit log surfaces who really authored
// the commit downstream.
//
// Returns the original ctx and a normal error if no active connection exists
// or the connection cannot supply a usable token.
func (s *Server) GitProviderForTier(
	ctx context.Context,
	r *http.Request,
	tier audit.Tier,
) (context.Context, gitprovider.GitProvider, service.TokenResolution, error) {
	conn, err := s.connSvc.GetActiveConnectionInfo()
	if err != nil {
		return ctx, nil, service.TokenResolution{}, err
	}

	user := s.userIdentityFromRequest(r)

	serviceToken := s.serviceTokenForConnection(conn)

	res, err := service.PickGitTokenForTier(tier, user, serviceToken, s.userTokenLookup())
	if err != nil {
		return ctx, nil, res, err
	}

	gp, err := s.providerFromConnectionWithToken(conn, res.Token)
	if err != nil {
		return ctx, nil, res, err
	}

	ctx = gitprovider.WithAttribution(ctx, res.Attribution)
	audit.Enrich(ctx, audit.Fields{
		Tier:            tier,
		AttributionMode: res.AttributionMode,
	})

	return ctx, gp, res, nil
}

// serviceTokenForConnection extracts the service token field from a connection
// based on its configured Git provider. Falls back to env vars in dev mode,
// matching the existing behaviour in connection.buildGitProvider so that no
// previously-working setup regresses.
func (s *Server) serviceTokenForConnection(conn *models.Connection) string {
	switch conn.Git.Provider {
	case models.GitProviderGitHub:
		if t := conn.Git.Token; t != "" {
			return t
		}
		if os.Getenv("SHARKO_DEV_MODE") == "true" {
			return os.Getenv("GITHUB_TOKEN")
		}
	case models.GitProviderAzureDevOps:
		if t := conn.Git.PAT; t != "" {
			return t
		}
		if os.Getenv("SHARKO_DEV_MODE") == "true" {
			return os.Getenv("AZURE_DEVOPS_PAT")
		}
	}
	return ""
}

// providerFromConnectionWithToken constructs a GitProvider against the same
// connection but with an explicit token. Used by GitProviderForTier when the
// resolved token differs from the connection's service token (Tier 2 happy
// path with per-user PAT).
func (s *Server) providerFromConnectionWithToken(conn *models.Connection, token string) (gitprovider.GitProvider, error) {
	switch conn.Git.Provider {
	case models.GitProviderGitHub:
		if token == "" {
			// Reuse the original error so callers get a stable message.
			return s.connSvc.GetActiveGitProvider()
		}
		return gitprovider.NewGitHubProvider(conn.Git.Owner, conn.Git.Repo, token), nil
	case models.GitProviderAzureDevOps:
		if token == "" {
			return s.connSvc.GetActiveGitProvider()
		}
		return gitprovider.NewAzureDevOpsProvider(conn.Git.Organization, conn.Git.Project, conn.Git.Repository, token), nil
	default:
		// Unknown provider — defer to the connection service which produces
		// the canonical error message.
		return s.connSvc.GetActiveGitProvider()
	}
}

// AttributionWarning is the value placed into a Tier 2 mutation response
// body when the caller had no per-user PAT and the service token was used
// as a fallback. The UI watches for this string and renders the
// "set up your personal token" nudge.
const AttributionWarning = "no_per_user_pat"

// addAttributionWarning is a small helper for handlers that build a JSON
// response map themselves: it stamps the response with the standard
// `attribution_warning: "no_per_user_pat"` key when the resolution
// flagged a fallback. No-op otherwise.
func addAttributionWarning(resp map[string]interface{}, res service.TokenResolution) map[string]interface{} {
	if !res.AttributionFallback {
		return resp
	}
	if resp == nil {
		resp = map[string]interface{}{}
	}
	resp["attribution_warning"] = AttributionWarning
	return resp
}

// withAttributionWarning wraps an arbitrary response payload, returning either
// the original payload unchanged (no fallback) or a generic JSON object that
// embeds the original under "result" alongside the attribution_warning. This
// keeps the no-fallback wire format identical to the pre-v1.20 shape so
// existing UI code continues to work, while giving the new UI a stable
// signal to render the nudge banner.
func withAttributionWarning(payload interface{}, res service.TokenResolution) interface{} {
	if !res.AttributionFallback {
		return payload
	}
	return map[string]interface{}{
		"result":               payload,
		"attribution_warning":  AttributionWarning,
	}
}

// stripCoAuthorEmailHint normalises the email part of an attribution to a
// `.noreply.sharko` value when the input looks like a free-text address. Used
// to keep the trailer well-formed even if a user has spaces or unusual
// characters in their stored profile.
//
// (Currently unused — kept here as a cheap utility for the values-editor
// agent that lands on this foundation.)
func stripCoAuthorEmailHint(email string) string {
	email = strings.TrimSpace(email)
	if email == "" || strings.Contains(email, " ") || !strings.Contains(email, "@") {
		return ""
	}
	return email
}
