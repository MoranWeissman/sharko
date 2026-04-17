package api

// users_me.go — endpoints for the authenticated caller's own profile.
//
// In v1.20 these power the "My Account" section of the Settings UI:
//   GET    /api/v1/users/me                 — return profile + has_github_token
//   PUT    /api/v1/users/me/github-token    — set/replace per-user GitHub PAT
//   DELETE /api/v1/users/me/github-token    — clear per-user GitHub PAT
//
// The PAT is encrypted at rest (AES-256-GCM, SHARKO_ENCRYPTION_KEY) and is never
// returned to the client — only a `has_github_token` boolean is exposed on GET.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// meResponse is the shape returned from GET /api/v1/users/me.
type meResponse struct {
	Username        string `json:"username"`
	Role            string `json:"role"`
	HasGitHubToken  bool   `json:"has_github_token"`
}

// handleGetMe godoc
//
// @Summary Get current user profile
// @Description Returns the authenticated caller's profile. `has_github_token` indicates whether a per-user GitHub PAT is configured for tiered attribution.
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} api.meResponse "Current user"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /users/me [get]
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.me") {
		return
	}

	username := r.Header.Get("X-Sharko-User")
	if username == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	role := r.Header.Get("X-Sharko-Role")
	if role == "" {
		role = "viewer"
	}

	resp := meResponse{
		Username:       username,
		Role:           role,
		HasGitHubToken: s.authStore.HasUserGitHubToken(username),
	}
	writeJSON(w, http.StatusOK, resp)
}

// setMyGitHubTokenRequest is the request body for PUT /api/v1/users/me/github-token.
type setMyGitHubTokenRequest struct {
	Token string `json:"token"`
}

// handleSetMyGitHubToken godoc
//
// @Summary Set personal GitHub token
// @Description Stores an encrypted personal GitHub PAT for the authenticated user. The PAT is preferred over the service token when the user performs Tier 2 (configuration) actions, so the resulting Git commit is authored by the user rather than the Sharko service account.
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body api.setMyGitHubTokenRequest true "Token payload"
// @Success 200 {object} map[string]interface{} "Token saved"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /users/me/github-token [put]
func (s *Server) handleSetMyGitHubToken(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.me.set-token") {
		return
	}

	username := r.Header.Get("X-Sharko-User")
	if username == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req setMyGitHubTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	encKey := os.Getenv("SHARKO_ENCRYPTION_KEY")
	if encKey == "" {
		writeError(w, http.StatusInternalServerError, "encryption key not configured")
		return
	}

	if err := s.authStore.SetUserGitHubToken(username, req.Token, encKey); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_github_token_set",
		Resource: fmt.Sprintf("user:%s", username),
		Tier:     audit.TierPersonal,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":           "saved",
		"has_github_token": true,
	})
}

// handleClearMyGitHubToken godoc
//
// @Summary Clear personal GitHub token
// @Description Removes the authenticated user's personal GitHub PAT. After clearing, Tier 2 actions fall back to the service token with a co-author trailer for attribution.
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Token cleared"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /users/me/github-token [delete]
func (s *Server) handleClearMyGitHubToken(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.me.clear-token") {
		return
	}

	username := r.Header.Get("X-Sharko-User")
	if username == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	if err := s.authStore.ClearUserGitHubToken(username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_github_token_cleared",
		Resource: fmt.Sprintf("user:%s", username),
		Tier:     audit.TierPersonal,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":           "cleared",
		"has_github_token": false,
	})
}

// handleTestMyGitHubToken godoc
//
// @Summary Test personal GitHub token
// @Description Validates the authenticated user's stored personal GitHub PAT by calling GitHub's `/user` endpoint. Returns the GitHub login on success.
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Token valid"
// @Failure 400 {object} map[string]interface{} "Token invalid or not set"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Router /users/me/github-token/test [post]
func (s *Server) handleTestMyGitHubToken(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.me.set-token") {
		return
	}

	username := r.Header.Get("X-Sharko-User")
	if username == "" {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	encKey := os.Getenv("SHARKO_ENCRYPTION_KEY")
	if encKey == "" {
		writeError(w, http.StatusInternalServerError, "encryption key not configured")
		return
	}

	token, err := s.authStore.GetUserGitHubToken(username, encKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decryption failed: "+err.Error())
		return
	}
	if token == "" {
		writeError(w, http.StatusBadRequest, "no personal GitHub token configured")
		return
	}

	login, err := validateGitHubToken(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusBadRequest, "token validation failed: "+err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_github_token_tested",
		Resource: fmt.Sprintf("user:%s", username),
		Tier:     audit.TierPersonal,
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":       "ok",
		"github_login": login,
	})
}

// validateGitHubToken calls GET https://api.github.com/user to validate the
// PAT, returning the login on success or an error otherwise.
func validateGitHubToken(ctx context.Context, token string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("token rejected (401)")
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("token forbidden (403) — check scopes")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github returned status %d", resp.StatusCode)
	}

	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("parsing github response: %w", err)
	}
	return body.Login, nil
}

