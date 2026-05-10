package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/service"
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
		writeServerError(w, http.StatusInternalServerError, "list_connections", err)
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
	if !authz.RequireWithResponse(w, r, "connection.create") { return }
	var req models.CreateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.connSvc.Create(req); err != nil {
		// V124-3.3 / M4: validation errors (e.g. invalid git URL) are
		// user-actionable — surface as 400 with the underlying message.
		// Genuine internal failures still 500 with a sanitized body.
		if errors.Is(err, service.ErrValidation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeServerError(w, http.StatusInternalServerError, "create_connection", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "connection_created",
		Resource: fmt.Sprintf("connection:%s", req.Name),
	})
	s.ReinitializeFromConnection()
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
	if !authz.RequireWithResponse(w, r, "connection.update") { return }
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
		// Preserve existing GitOps settings when not provided
		if req.GitOps == nil {
			req.GitOps = saved.GitOps
		} else if saved.GitOps != nil {
			// Merge: keep existing fields that aren't in the request
			if req.GitOps.BaseBranch == "" {
				req.GitOps.BaseBranch = saved.GitOps.BaseBranch
			}
			if req.GitOps.BranchPrefix == "" {
				req.GitOps.BranchPrefix = saved.GitOps.BranchPrefix
			}
			if req.GitOps.CommitPrefix == "" {
				req.GitOps.CommitPrefix = saved.GitOps.CommitPrefix
			}
			if req.GitOps.PRAutoMerge == nil {
				req.GitOps.PRAutoMerge = saved.GitOps.PRAutoMerge
			}
			if req.GitOps.HostClusterName == "" {
				req.GitOps.HostClusterName = saved.GitOps.HostClusterName
			}
			if req.GitOps.DefaultAddons == "" {
				req.GitOps.DefaultAddons = saved.GitOps.DefaultAddons
			}
		}
	}

	if err := s.connSvc.Create(req); err != nil {
		// V124-3.3 / M4: validation errors → 400 (see handleCreateConnection).
		if errors.Is(err, service.ErrValidation) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeServerError(w, http.StatusInternalServerError, "update_connection", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "connection_updated",
		Resource: fmt.Sprintf("connection:%s", name),
	})
	s.ReinitializeFromConnection()
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
	if !authz.RequireWithResponse(w, r, "connection.delete") { return }
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "connection name is required")
		return
	}

	if err := s.connSvc.Delete(name); err != nil {
		writeServerError(w, http.StatusInternalServerError, "delete_connection", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "connection_deleted",
		Resource: fmt.Sprintf("connection:%s", name),
	})
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
	if !authz.RequireWithResponse(w, r, "connection.set-active") { return }
	var req models.SetActiveConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// V124-4.5: empty body would set ConnectionName="" and surface as a
	// confusing 500 "connection \"\" not found" via writeServerError. Treat
	// empty connection name as a 400 with a clear field-specific message.
	if req.ConnectionName == "" {
		writeError(w, http.StatusBadRequest, "connection_name is required")
		return
	}

	if err := s.connSvc.SetActive(req.ConnectionName); err != nil {
		writeServerError(w, http.StatusInternalServerError, "set_active_connection", err)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "active_connection_changed",
		Resource: fmt.Sprintf("connection:%s", req.ConnectionName),
	})
	s.ReinitializeFromConnection()
	writeJSON(w, http.StatusOK, map[string]string{"status": "active", "connection": req.ConnectionName})
}

// handleTestCredentials godoc
//
// @Summary Test connection credentials
// @Description Tests Git and ArgoCD credentials. With use_saved=true, fetches the named saved connection's stored credentials and tests with those instead of the request body
// @Tags connections
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body models.CreateConnectionRequest true "Connection credentials to test (set use_saved=true with name to test the saved credentials of an existing connection)"
// @Success 200 {object} map[string]interface{} "Credential test result"
// @Failure 400 {object} map[string]interface{} "Bad request (e.g. use_saved=true but no matching saved connection)"
// @Router /connections/test-credentials [post]
func (s *Server) handleTestCredentials(w http.ResponseWriter, r *http.Request) {
	var req models.CreateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// V124-19 / BUG-044: when the wizard renders in resume mode it shows the
	// password fields empty by design (we never re-display saved secrets).
	// V124-17 added a placeholder telling the user "leave blank to keep, or
	// enter new value to replace", but the validation layer still rejected
	// blank submissions: TestCredentials → buildArgocdClient → "ArgoCD token
	// not configured", which kept the wizard's Next gate disabled.
	//
	// The fix splits the contract:
	//   - request body credentials present → existing behavior (test as
	//     submitted, optionally back-filling empty token fields from the
	//     saved record by name — preserves the original "I changed only
	//     the URL but kept the saved token" UX)
	//   - use_saved=true → load the named saved connection's full config
	//     server-side and test that. The user never re-types the saved
	//     credential, and a missing saved connection surfaces as 400 with
	//     a descriptive message (no silent "tested empty creds" failure).
	//
	// Both Git and ArgoCD share the same saved record, so use_saved is a
	// single boolean rather than separate per-service flags — the wizard's
	// blank-keep contract applies symmetrically (Step 2 Git + Step 3 ArgoCD).
	conn := &models.Connection{
		Name:   req.Name,
		Git:    req.Git,
		Argocd: req.Argocd,
	}

	usedSaved := false
	if req.UseSaved {
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "use_saved=true requires connection name in request body")
			return
		}
		saved, err := s.connSvc.GetConnection(req.Name)
		if err != nil || saved == nil {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("use_saved=true but no saved connection named %q found", req.Name))
			return
		}
		// Replace credential-bearing fields with the saved record's values.
		// We keep the request's Git/Argocd shape (provider, repo IDs, server
		// URL) so the test exercises any user-edited URL alongside the saved
		// token — but for the typical "blank keep" flow the wizard sends the
		// same URL it loaded, so there's no divergence.
		conn.Git = saved.Git
		conn.Argocd = saved.Argocd
		usedSaved = true
	} else if conn.Name != "" {
		// For edits: if token fields are empty, fill from saved connection.
		// Preserves pre-V124-19 behavior for partial-body submits.
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

	// V124-19: mark the audit entry when the saved-credential path ran so
	// the test event is traceable distinctly from a fresh-body test.
	auditEvent := "credentials_tested"
	if usedSaved {
		auditEvent = "credentials_tested_saved"
	}
	auditFields := audit.Fields{Event: auditEvent}
	if usedSaved {
		auditFields.Resource = fmt.Sprintf("connection:%s", req.Name)
	}
	audit.Enrich(r.Context(), auditFields)
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

	audit.Enrich(r.Context(), audit.Fields{
		Event: "connection_tested",
	})
	writeJSON(w, http.StatusOK, result)
}
