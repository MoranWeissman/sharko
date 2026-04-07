package api

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/MoranWeissman/sharko/internal/models"
)

// handleListConnections godoc
//
// @Summary List connections
// @Description Returns all configured Git and ArgoCD connections
// @Tags connections
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Connection list"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /connections/ [get]
func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	resp, err := s.connSvc.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleCreateConnection godoc
//
// @Summary Create connection
// @Description Creates a new Git and ArgoCD connection configuration
// @Tags connections
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.CreateConnectionRequest true "Connection request"
// @Success 201 {object} map[string]interface{} "Connection created"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /connections/ [post]
func (s *Server) handleCreateConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) { return }
	var req models.CreateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.connSvc.Create(req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.reinitializeProvider()
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "name": req.Name})
}

// handleUpdateConnection godoc
//
// @Summary Update connection
// @Description Updates an existing connection configuration; empty token fields retain their saved values
// @Tags connections
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Connection name"
// @Param body body models.CreateConnectionRequest true "Updated connection request"
// @Success 200 {object} map[string]interface{} "Connection updated"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /connections/{name} [put]
func (s *Server) handleUpdateConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) { return }
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "connection name is required")
		return
	}

	var req models.CreateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = name // ensure name matches URL

	// For edits: if token/provider fields are empty, keep existing values
	if saved, err := s.connSvc.GetConnection(name); err == nil && saved != nil {
		if req.Git.Token == "" {
			req.Git.Token = saved.Git.Token
		}
		if req.Git.PAT == "" {
			req.Git.PAT = saved.Git.PAT
		}
		if req.Argocd.Token == "" {
			req.Argocd.Token = saved.Argocd.Token
		}
		if req.Provider == nil {
			req.Provider = saved.Provider
		}
	}

	if err := s.connSvc.Create(req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.reinitializeProvider()
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "name": name})
}

// handleDeleteConnection godoc
//
// @Summary Delete connection
// @Description Permanently removes a connection configuration
// @Tags connections
// @Produce json
// @Security BearerAuth
// @Param name path string true "Connection name"
// @Success 200 {object} map[string]interface{} "Connection deleted"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /connections/{name} [delete]
func (s *Server) handleDeleteConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) { return }
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "connection name is required")
		return
	}

	if err := s.connSvc.Delete(name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "name": name})
}

// handleSetActiveConnection godoc
//
// @Summary Set active connection
// @Description Sets the specified connection as the active one used by all API operations
// @Tags connections
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.SetActiveConnectionRequest true "Set active connection request"
// @Success 200 {object} map[string]interface{} "Active connection set"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /connections/active [post]
func (s *Server) handleSetActiveConnection(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) { return }
	var req models.SetActiveConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.connSvc.SetActive(req.ConnectionName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.reinitializeProvider()
	writeJSON(w, http.StatusOK, map[string]string{"status": "active", "connection": req.ConnectionName})
}

// handleTestCredentials godoc
//
// @Summary Test connection credentials
// @Description Tests Git and ArgoCD credentials from a connection request without saving
// @Tags connections
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.CreateConnectionRequest true "Connection credentials to test"
// @Success 200 {object} map[string]interface{} "Credential test result"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Router /connections/test-credentials [post]
func (s *Server) handleTestCredentials(w http.ResponseWriter, r *http.Request) {
	var req models.CreateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	conn := &models.Connection{
		Name:   req.Name,
		Git:    req.Git,
		Argocd: req.Argocd,
	}

	// For edits: if token fields are empty, fill from saved connection
	if conn.Name != "" {
		if saved, err := s.connSvc.GetConnection(conn.Name); err == nil && saved != nil {
			if conn.Git.Token == "" {
				conn.Git.Token = saved.Git.Token
			}
			if conn.Git.PAT == "" {
				conn.Git.PAT = saved.Git.PAT
			}
			if conn.Argocd.Token == "" {
				conn.Argocd.Token = saved.Argocd.Token
			}
		}
	}

	gitErr, argocdErr, authInfo := s.connSvc.TestCredentials(r.Context(), conn)

	result := map[string]interface{}{
		"git":    map[string]interface{}{"status": "ok"},
		"argocd": map[string]interface{}{"status": "ok"},
	}
	if gitErr != nil {
		result["git"] = map[string]interface{}{"status": "error", "message": gitErr.Error()}
	} else if authInfo.GitSource != "" {
		result["git"] = map[string]interface{}{"status": "ok", "auth": authInfo.GitSource}
	}
	if argocdErr != nil {
		result["argocd"] = map[string]interface{}{"status": "error", "message": argocdErr.Error()}
	} else if authInfo.ArgocdSource != "" {
		result["argocd"] = map[string]interface{}{"status": "ok", "auth": authInfo.ArgocdSource}
	}

	writeJSON(w, http.StatusOK, result)
}

// handleDiscoverArgocd godoc
//
// @Summary Discover ArgoCD URL
// @Description Attempts to auto-discover the ArgoCD server URL from the Kubernetes cluster
// @Tags connections
// @Produce json
// @Security BearerAuth
// @Param namespace query string false "Kubernetes namespace to search (default: argocd)"
// @Success 200 {object} map[string]interface{} "Discovered ArgoCD URL"
// @Router /connections/discover-argocd [get]
func (s *Server) handleDiscoverArgocd(w http.ResponseWriter, r *http.Request) {
	ns := r.URL.Query().Get("namespace")
	if ns == "" {
		ns = "argocd"
	}
	url := s.connSvc.DiscoverArgocdURL(ns)
	hasEnvToken := os.Getenv("ARGOCD_TOKEN") != ""

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"server_url":    url,
		"has_env_token": hasEnvToken,
		"namespace":     ns,
	})
}

// handleTestConnection godoc
//
// @Summary Test active connection
// @Description Tests the currently active Git and ArgoCD connection
// @Tags connections
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Connection test result"
// @Router /connections/test [post]
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	gitErr, argocdErr := s.connSvc.TestConnection(r.Context())

	result := map[string]interface{}{
		"git":    map[string]interface{}{"status": "ok"},
		"argocd": map[string]interface{}{"status": "ok"},
	}

	if gitErr != nil {
		result["git"] = map[string]interface{}{"status": "error", "message": gitErr.Error()}
	}
	if argocdErr != nil {
		result["argocd"] = map[string]interface{}{"status": "error", "message": argocdErr.Error()}
	}

	writeJSON(w, http.StatusOK, result)
}
