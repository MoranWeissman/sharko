package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
)

// handleListUsers godoc
//
// @Summary List users
// @Description Returns all configured users (admin only)
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "User list"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /users [get]
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.list") {
		return
	}
	users := s.authStore.ListUsers()
	writeJSON(w, http.StatusOK, users)
}

// handleCreateUser godoc
//
// @Summary Create user
// @Description Creates a new user with a generated temporary password (admin only)
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Create user request with username and role"
// @Success 201 {object} map[string]interface{} "User created with temp password"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /users [post]
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.create") {
		return
	}

	var req struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	tempPassword, err := s.authStore.CreateUser(req.Username, req.Role)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_created",
		Resource: fmt.Sprintf("user:%s", req.Username),
		Detail:   fmt.Sprintf("role=%s", req.Role),
	})
	writeJSON(w, http.StatusCreated, map[string]string{
		"username":      req.Username,
		"role":          req.Role,
		"temp_password": tempPassword,
		"message":       "User created. Share the temporary password — they must change it on first login.",
	})
}

// handleUpdateUser godoc
//
// @Summary Update user
// @Description Updates a user's role and enabled status (admin only)
// @Tags auth
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param username path string true "Username"
// @Param body body map[string]interface{} true "Update user request with enabled flag and role"
// @Success 200 {object} map[string]interface{} "Updated user"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /users/{username} [put]
func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.change-role") {
		return
	}

	username := r.PathValue("username")
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	var req struct {
		Enabled *bool  `json:"enabled"`
		Role    string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Default enabled to true if not specified
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if err := s.authStore.UpdateUser(username, enabled, req.Role); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	user := s.authStore.GetUser(username)
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_updated",
		Resource: fmt.Sprintf("user:%s", username),
	})
	writeJSON(w, http.StatusOK, user)
}

// handleDeleteUser godoc
//
// @Summary Delete user
// @Description Permanently deletes a user account (admin only)
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Param username path string true "Username"
// @Success 200 {object} map[string]interface{} "User deleted"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /users/{username} [delete]
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.delete") {
		return
	}

	username := r.PathValue("username")
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	if err := s.authStore.DeleteUser(username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_deleted",
		Resource: fmt.Sprintf("user:%s", username),
	})
	writeJSON(w, http.StatusOK, map[string]string{"message": "user deleted"})
}

// handleResetPassword godoc
//
// @Summary Reset user password
// @Description Resets a user's password and returns a new temporary password (admin only)
// @Tags auth
// @Produce json
// @Security BearerAuth
// @Param username path string true "Username"
// @Success 200 {object} map[string]interface{} "Temp password"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /users/{username}/reset-password [post]
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.change-role") {
		return
	}

	username := r.PathValue("username")
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	tempPassword, err := s.authStore.ResetPassword(username)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "password_reset",
		Resource: fmt.Sprintf("user:%s", username),
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"username":      username,
		"temp_password": tempPassword,
		"message":       "Password reset. Share the new temporary password.",
	})
}
