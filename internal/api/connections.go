package api

import (
	"encoding/json"
	"net/http"
	"os"

	"github.com/moran/argocd-addons-platform/internal/models"
)

func (s *Server) handleListConnections(w http.ResponseWriter, r *http.Request) {
	resp, err := s.connSvc.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

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

	writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "name": req.Name})
}

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

	// For edits: if token fields are empty, keep existing values
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
	}

	if err := s.connSvc.Create(req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "name": name})
}

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

func (s *Server) handleSetActiveConnection(w http.ResponseWriter, r *http.Request) {
	var req models.SetActiveConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.connSvc.SetActive(req.ConnectionName); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "active", "connection": req.ConnectionName})
}

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
