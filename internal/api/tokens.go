package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// handleCreateToken godoc
//
// @Summary Create API token
// @Description Creates a new long-lived API token for programmatic access (admin only)
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Token name and role"
// @Success 201 {object} map[string]interface{} "Created token (shown only once)"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /tokens [post]
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "token.create") {
		return
	}

	var req struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	plaintext, err := s.authStore.CreateToken(req.Name, req.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "token_created",
		Resource: fmt.Sprintf("token:%s", req.Name),
		Detail:   fmt.Sprintf("role=%s", req.Role),
	})
	writeJSON(w, http.StatusCreated, map[string]string{
		"name":  req.Name,
		"token": plaintext,
		"role":  req.Role,
	})
}

// handleListTokens godoc
//
// @Summary List API tokens
// @Description Returns all active API tokens without their secret values (admin only)
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Token list"
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

// handleRevokeToken godoc
//
// @Summary Revoke API token
// @Description Permanently revokes an API token by name (admin only)
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Param name path string true "Token name"
// @Success 200 {object} map[string]interface{} "Token revoked"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "token_revoked",
		Resource: fmt.Sprintf("token:%s", name),
	})
	writeJSON(w, http.StatusOK, map[string]string{"message": "token revoked"})
}
