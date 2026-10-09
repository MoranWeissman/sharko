package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// CreateTokenRequest is the body of POST /tokens.
type CreateTokenRequest struct {
	// Name identifies the token in the list and in audit entries.
	Name string `json:"name"`
	// Role is admin, operator, or viewer. Defaults to viewer.
	Role string `json:"role,omitempty"`
	// ExpiresInDays is how long the token stays usable, between 1 and 365.
	// Leave it out for the default of 90 days.
	ExpiresInDays int `json:"expires_in_days,omitempty"`
}

// CreateTokenResponse is returned once, at creation time. The Token field is
// the only time the plaintext is ever shown.
type CreateTokenResponse struct {
	Name      string `json:"name"`
	Token     string `json:"token"`
	Role      string `json:"role"`
	ExpiresAt string `json:"expires_at"`
}

// RenewTokenRequest is the (optional) body of POST /tokens/{name}/renew.
type RenewTokenRequest struct {
	// ExpiresInDays is the new window, counted from now, between 1 and 365.
	// Leave it out for the default of 90 days.
	ExpiresInDays int `json:"expires_in_days,omitempty"`
}

// handleCreateToken godoc
//
// @Summary Create API token
// @Description Creates a new long-lived API token for programmatic access. The token expires after 90 days unless expires_in_days (1-365) says otherwise. The plaintext value is shown once and never again. A token can carry at most the role of the caller who created it — asking for a higher role is refused with 403.
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body CreateTokenRequest true "Token name, role, and optional expiry window"
// @Success 201 {object} CreateTokenResponse "Created token (plaintext shown only once)"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /tokens [post]
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "token.create") {
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// SECURITY (v4-wave2 review B2): a token can carry at most the role of
	// whoever asked for it. Without this ceiling an operator could mint an
	// admin token and then use it to do everything their own account is not
	// allowed to do — a one-request escalation to full admin.
	//
	// authz.RoleFromString maps anything unrecognised to viewer, so a
	// nonsense role never sneaks past this check; it falls through to the
	// store, which rejects it with the plain "role must be ..." message.
	caller := callerRole(r)
	if req.Role != "" && !caller.AtLeast(authz.RoleFromString(req.Role)) {
		writeError(w, http.StatusForbidden, fmt.Sprintf(
			"you cannot create a %s token — a token can carry at most your own role, and yours is %s",
			req.Role, caller))
		return
	}

	plaintext, err := s.authStore.CreateTokenFor(r.Header.Get("X-Sharko-User"), req.Name, req.Role, req.ExpiresInDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Look the token back up so the response carries the expiry date the
	// store actually applied, rather than one recomputed here.
	expiresAt := ""
	role := req.Role
	if stored, ok := s.authStore.GetToken(req.Name); ok {
		role = stored.Role
		if stored.ExpiresAt != nil {
			expiresAt = stored.ExpiresAt.UTC().Format(time.RFC3339)
		}
	}
	if role == "" {
		role = "viewer"
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "token_created",
		Resource: fmt.Sprintf("token:%s", req.Name),
		Detail:   fmt.Sprintf("role=%s expires_at=%s", role, expiresAt),
	})
	writeJSON(w, http.StatusCreated, CreateTokenResponse{
		Name:      req.Name,
		Token:     plaintext,
		Role:      role,
		ExpiresAt: expiresAt,
	})
}

// handleListTokens godoc
//
// @Summary List API tokens
// @Description Returns all API tokens without their secret values, each with its expiry date and status (active, expired, or legacy-no-expiry)
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {array} auth.APIToken "Token list"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /tokens [get]
func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "token.list") {
		return
	}

	tokens := s.authStore.ListTokens()
	writeJSON(w, http.StatusOK, tokens)
}

// handleRenewToken godoc
//
// @Summary Renew API token
// @Description Pushes a token's expiry out by a fresh window (90 days by default, or expires_in_days between 1 and 365). The secret value does not change, so every client using the token keeps working. An operator may renew tokens they own; renewing anyone else's token requires an admin.
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Token name"
// @Param body body RenewTokenRequest false "Optional new expiry window"
// @Success 200 {object} auth.APIToken "Renewed token metadata (no secret)"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 404 {object} map[string]interface{} "Token not found"
// @Router /tokens/{name}/renew [post]
func (s *Server) handleRenewToken(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "token name is required")
		return
	}

	// SECURITY (v4-wave2 review H1): renewing a token keeps a live credential
	// alive, so it follows the same own/other split as revoking one. Renewing
	// your own token is an operator action; renewing anybody else's — which
	// includes every token nobody is recorded against — takes an admin.
	//
	// Resolving ownership BEFORE the role check also means an operator asking
	// about a token that does not exist gets the same 403 as one asking about
	// a token they do not own, so the refusal never doubles as a way to find
	// out which token names are real.
	//
	// Both branches name their action as a literal so the mechanical authz
	// coverage guard can see them (see authz_coverage_test.go).
	if s.authStore != nil && s.authStore.TokenOwnedBy(name, r.Header.Get("X-Sharko-User")) {
		if !authz.RequireWithResponse(w, r, "token.renew-own") {
			return
		}
	} else if !authz.RequireWithResponse(w, r, "token.renew-other") {
		return
	}

	// The body is optional — no body means "use the default window".
	var req RenewTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	token, err := s.authStore.RenewToken(name, req.ExpiresInDays)
	if err != nil {
		var notFound *auth.TokenNotFoundError
		if errors.As(err, &notFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	expiresAt := ""
	if token.ExpiresAt != nil {
		expiresAt = token.ExpiresAt.UTC().Format(time.RFC3339)
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "token_renewed",
		Resource: fmt.Sprintf("token:%s", name),
		Detail:   fmt.Sprintf("expires_at=%s", expiresAt),
	})
	writeJSON(w, http.StatusOK, token)
}

// handleRevokeToken godoc
//
// @Summary Revoke API token
// @Description Permanently revokes an API token by name. The token stops working immediately — there is no grace period.
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Param name path string true "Token name"
// @Success 200 {object} map[string]interface{} "Token revoked"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 404 {object} map[string]interface{} "Token not found"
// @Router /tokens/{name} [delete]
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "token.revoke-other") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "token name is required")
		return
	}

	if err := s.authStore.RevokeToken(name); err != nil {
		var notFound *auth.TokenNotFoundError
		if errors.As(err, &notFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "token_revoked",
		Resource: fmt.Sprintf("token:%s", name),
	})
	writeJSON(w, http.StatusOK, map[string]string{"message": "token revoked"})
}

// tokenRefusalMessage turns an auth-store refusal into the message the client
// sees on a 401.
//
// SECURITY: only a caller who presented a value that actually matched a stored
// token gets a specific message. Anything else — a wrong, made-up, or revoked
// token — gets the same generic "unauthorized" so an outsider cannot probe
// which token names exist.
func tokenRefusalMessage(err error) string {
	var expired *auth.TokenExpiredError
	if errors.As(err, &expired) {
		return expired.Error() + " — renew it or create a new one, then try again"
	}
	return ""
}
