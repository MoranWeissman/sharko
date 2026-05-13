//go:build e2e

package harness

// apiclient_auth.go — auth + tokens typed wrappers (V2 Epic 7-1.10).
//
// These wrappers are intentionally thin and decode into local typed
// shapes so the suite breaks at compile time when the API contract
// shifts. They do NOT import sharko's internal handler types because
// the handlers return inline structs / map[string]string today (see
// internal/api/router.go handleLogin / handleLogout / etc. and
// internal/api/tokens.go). The local mirror is the contract.
//
// All wrappers fail the test via t.Fatalf on transport / status
// mismatch, in line with the rest of the typed surface in apiclient.go.

import (
	"net/http"
	"testing"
	"time"
)

// LoginResponse mirrors the JSON returned by handleLogin.
type LoginResponse struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// LoginAs POSTs /api/v1/auth/login with the supplied credentials and
// returns the decoded response. The caller's session token is NOT
// updated by this helper — it is meant for tests that want to assert
// the raw login response (token shape, role echo, error mapping).
//
// Use Login (in apiclient.go) when you simply want the Client to be
// authenticated.
func (c *Client) LoginAs(t *testing.T, username, password string) LoginResponse {
	t.Helper()
	var out LoginResponse
	c.PostJSON(t, "/api/v1/auth/login",
		map[string]string{"username": username, "password": password},
		&out,
		WithExpectStatus(http.StatusOK),
		// Skip the bearer header on this call — the helper would
		// otherwise inject the existing session token, which is
		// fine but pointless on the login endpoint.
	)
	return out
}

// Logout POSTs /api/v1/auth/logout. The current bearer token is sent
// (the handler reads it directly off the Authorization header). After
// a successful call subsequent authenticated requests with the same
// token return 401 — the suite asserts this by issuing a follow-up
// request with WithNoRetry to bypass the auto-refresh.
func (c *Client) Logout(t *testing.T) {
	t.Helper()
	c.PostJSON(t, "/api/v1/auth/logout", map[string]string{}, nil)
}

// UpdatePassword POSTs /api/v1/auth/update-password. The current
// session must be active (the handler reads the username from the
// X-Sharko-User header that basicAuthMiddleware sets after token auth).
// The new password must be at least 12 characters — the handler
// rejects anything shorter with 400.
func (c *Client) UpdatePassword(t *testing.T, currentPassword, newPassword string, opts ...RequestOption) {
	t.Helper()
	c.PostJSON(t, "/api/v1/auth/update-password",
		map[string]string{
			"current_password": currentPassword,
			"new_password":     newPassword,
		},
		nil,
		opts...,
	)
}

// HashPasswordResponse mirrors the inline shape from handleHashPassword.
type HashPasswordResponse struct {
	Hash string `json:"hash"`
}

// HashPassword POSTs /api/v1/auth/hash. The handler is only reachable
// when the auth store has NO users (HasUsers()==false). Whenever the
// in-process boot path has seeded an admin (i.e. always, today) the
// endpoint returns 403 — callers asserting that code should pass
// WithExpectStatus(http.StatusForbidden).
func (c *Client) HashPassword(t *testing.T, plaintext string, opts ...RequestOption) HashPasswordResponse {
	t.Helper()
	var out HashPasswordResponse
	c.PostJSON(t, "/api/v1/auth/hash",
		map[string]string{"password": plaintext},
		&out,
		opts...,
	)
	return out
}

// APITokenView mirrors auth.APIToken without the secret hash. Matches
// the JSON ListTokens emits (`-` tag on Hash strips it).
type APITokenView struct {
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// CreateTokenResponse mirrors the JSON handleCreateToken returns.
//
// `Token` is the plaintext value — visible exactly once at creation
// time. The suite uses it immediately to drive an authenticated request,
// then discards it.
type CreateTokenResponse struct {
	Name  string `json:"name"`
	Token string `json:"token"`
	Role  string `json:"role"`
}

// CreateToken POSTs /api/v1/tokens with the requested name + role.
// Default role is empty → handler defaults to "viewer".
func (c *Client) CreateToken(t *testing.T, name, role string, opts ...RequestOption) CreateTokenResponse {
	t.Helper()
	var out CreateTokenResponse
	body := map[string]string{"name": name}
	if role != "" {
		body["role"] = role
	}
	if len(opts) == 0 {
		opts = []RequestOption{WithExpectStatus(http.StatusCreated)}
	}
	c.PostJSON(t, "/api/v1/tokens", body, &out, opts...)
	return out
}

// ListTokens fetches GET /api/v1/tokens. Returns the full token list
// (without secrets).
func (c *Client) ListTokens(t *testing.T, opts ...RequestOption) []APITokenView {
	t.Helper()
	var out []APITokenView
	c.GetJSON(t, "/api/v1/tokens", &out, opts...)
	return out
}

// RevokeToken issues DELETE /api/v1/tokens/{name}.
func (c *Client) RevokeToken(t *testing.T, name string, opts ...RequestOption) {
	t.Helper()
	c.Delete(t, "/api/v1/tokens/"+name, opts...)
}
