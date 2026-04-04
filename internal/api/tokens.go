package api

import (
	"encoding/json"
	"net/http"
)

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	writeJSON(w, http.StatusCreated, map[string]string{
		"name":  req.Name,
		"token": plaintext,
		"role":  req.Role,
	})
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	tokens := s.authStore.ListTokens()
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
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

	writeJSON(w, http.StatusOK, map[string]string{"message": "token revoked"})
}
