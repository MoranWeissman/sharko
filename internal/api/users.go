package api

import (
	"encoding/json"
	"net/http"
)

// requireAdmin checks that the current user has the admin role.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	username := r.Header.Get("X-AAP-User")
	if username == "" {
		// Auth may be disabled (no users configured) — allow access
		if !s.authStore.HasUsers() {
			return true
		}
		writeError(w, http.StatusForbidden, "admin access required")
		return false
	}
	user := s.authStore.GetUser(username)
	if user == nil || user.Role != "admin" {
		writeError(w, http.StatusForbidden, "admin access required")
		return false
	}
	return true
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	users := s.authStore.ListUsers()
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	writeJSON(w, http.StatusCreated, map[string]string{
		"username":       req.Username,
		"role":           req.Role,
		"temp_password":  tempPassword,
		"message":        "User created. Share the temporary password — they must change it on first login.",
	})
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	writeJSON(w, http.StatusOK, map[string]string{"message": "user deleted"})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	writeJSON(w, http.StatusOK, map[string]string{
		"username":      username,
		"temp_password": tempPassword,
		"message":       "Password reset. Share the new temporary password.",
	})
}
