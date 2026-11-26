package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/auth"
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
// @Failure 500 {object} map[string]interface{} "The user could not be saved"
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
		// A bad user name gets the one plain sentence, exactly.
		if errors.Is(err, auth.ErrInvalidUsername) {
			writeError(w, http.StatusBadRequest, auth.InvalidUsernameSentence)
			return
		}
		// What the caller asked for was wrong: 400. Anything else is a
		// storage failure on our side: 500. The text holds no password or
		// hash (see createUserK8s).
		writeError(w, userErrorStatus(err), err.Error())
		return
	}

	// The role the user really got. A request with no role makes a viewer,
	// and the answer used to show the empty role that was sent.
	role := req.Role
	if created := s.authStore.GetUser(req.Username); created != nil {
		role = created.Role
	}
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_created",
		Resource: fmt.Sprintf("user:%s", req.Username),
		Detail:   fmt.Sprintf("role=%s", role),
	})
	writeJSON(w, http.StatusCreated, map[string]string{
		"username":      req.Username,
		"role":          role,
		"temp_password": tempPassword,
		"message":       CreateUserMessage,
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
// @Failure 500 {object} map[string]interface{} "The user could not be saved"
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

	// A part that is left out is kept as it is saved. A request that only
	// changes the role must never turn a disabled account back on (it used
	// to: a missing "enabled" was read as true).
	before := s.authStore.GetUser(username)

	if err := s.authStore.UpdateUserFields(username, req.Enabled, req.Role); err != nil {
		writeError(w, userErrorStatus(err), err.Error())
		return
	}

	user := s.authStore.GetUser(username)

	// A changed role or a switched-off account ends every login of that
	// user right away. They log in again and get the new role.
	ended := 0
	if before != nil && user != nil && (before.Role != user.Role || (before.Enabled && !user.Enabled)) {
		ended = endSessionsForUser(username, "")
	}

	detail := ""
	if user != nil {
		detail = fmt.Sprintf("role=%s enabled=%t logins ended: %d", user.Role, user.Enabled, ended)
	}
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_updated",
		Resource: fmt.Sprintf("user:%s", username),
		Detail:   detail,
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
// @Failure 500 {object} map[string]interface{} "The user could not be removed"
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
		// The user may already be out of the user list (see DeleteUser), so
		// their logins end even on this path.
		if s.authStore.GetUser(username) == nil {
			endSessionsForUser(username, "")
		}
		writeError(w, userErrorStatus(err), err.Error())
		return
	}

	// A deleted user is cut off at once: every login ends, and every API
	// key they created is revoked.
	ended := endSessionsForUser(username, "")
	revoked, revokeErr := s.authStore.RevokeTokensCreatedBy(username)

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "user_deleted",
		Resource: fmt.Sprintf("user:%s", username),
		Detail:   fmt.Sprintf("logins ended: %d, API keys revoked: %d", ended, len(revoked)),
	})
	if revokeErr != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf(UserDeletedKeysNotRevokedSentence, username))
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":          "user deleted",
		"revoked_api_keys": revoked,
	})
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
// @Failure 500 {object} map[string]interface{} "The new password could not be saved"
// @Router /users/{username}/reset-password [post]
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "user.reset-password") {
		return
	}

	username := r.PathValue("username")
	if username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}

	tempPassword, err := s.authStore.ResetPassword(username)
	if err != nil {
		// The text holds no password or hash (see auth.ResetPassword).
		writeError(w, userErrorStatus(err), err.Error())
		return
	}

	// The old password is gone, so every login made with it ends now.
	ended := endSessionsForUser(username, "")

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "password_reset",
		Resource: fmt.Sprintf("user:%s", username),
		Detail:   fmt.Sprintf("logins ended: %d", ended),
	})
	writeJSON(w, http.StatusOK, map[string]string{
		"username":      username,
		"temp_password": tempPassword,
		"message":       "Password reset. Share the new temporary password.",
	})
}

// UserDeletedKeysNotRevokedSentence is the answer when a user was deleted but
// the API keys they created could not be revoked. The %q is the user name.
const UserDeletedKeysNotRevokedSentence = "User %q was deleted, but the API keys they created could not be revoked. Revoke them in Settings, API Keys."

// CreateUserMessage is the sentence the create-user answer carries. It used
// to say "they must change it on first login", which Sharko does not enforce.
const CreateUserMessage = "User created. Share the temporary password with them. They should change it after they log in, on the Account page."

// userErrorStatus picks the status for an error from CreateUser,
// UpdateUser, DeleteUser or ResetPassword: 400 when what the caller asked for
// was wrong, 500 when saving to storage failed.
func userErrorStatus(err error) int {
	if auth.IsInputError(err) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}
