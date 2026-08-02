package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

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
	if !authz.RequireWithResponse(w, r, "connection.create") {
		return
	}
	var req models.CreateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.connSvc.Create(req); err != nil {
		// Validation errors (e.g. invalid git URL) are user-actionable
		// — surface as 400 with the underlying message. Genuine
		// internal failures still 500 with a sanitized body.
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
// @Description Updates an existing connection configuration; empty token fields retain their saved values. A request whose git or argocd block carries none of that block's identifying fields (e.g. a GitOps-settings-only or secrets-provider-only save) is treated as not touching that section — the stored git/argocd config is kept as-is instead of being overwritten with empty values.
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
	if !authz.RequireWithResponse(w, r, "connection.update") {
		return
	}
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
		// Settings pages other than the connection editor itself (GitOps
		// settings, secrets provider) call this same endpoint to save their
		// own section, and must not be forced to reconstruct the git/argocd
		// blocks from parts. GET /connections never exposes enough to
		// round-trip them faithfully — e.g. a self-hosted Gitea repo's host
		// isn't in the masked ConnectionResponse at all, and ArgoCD's
		// `insecure` flag isn't either. A request that carries none of a
		// block's identifying fields is read as "not touching this section"
		// and falls back to the stored value wholesale, the same way
		// req.Provider/req.AddonSecretProvider/req.GitOps already do below.
		// Without this, the client's only options were: invent a repo_url
		// (breaks Gitea, whose host it cannot know) or send real-looking
		// zero values that silently overwrite the stored config (e.g. an
		// ArgoCD connection verified over TLS quietly flipped to insecure).
		if req.Git.RepoURL == "" && req.Git.Owner == "" && req.Git.Repo == "" &&
			req.Git.Organization == "" && req.Git.Project == "" && req.Git.Repository == "" {
			tok, pat := req.Git.Token, req.Git.PAT
			req.Git = saved.Git
			if tok != "" {
				req.Git.Token = tok
			}
			if pat != "" {
				req.Git.PAT = pat
			}
		}
		if req.Git.Token == "" {
			req.Git.Token = saved.Git.Token
		}
		if req.Git.PAT == "" {
			req.Git.PAT = saved.Git.PAT
		}
		if req.Argocd.ServerURL == "" && req.Argocd.Token == "" {
			req.Argocd = saved.Argocd
		}
		if req.Argocd.Token == "" {
			req.Argocd.Token = saved.Argocd.Token
		}
		if req.Provider == nil {
			req.Provider = saved.Provider
		}
		if req.AddonSecretProvider == nil {
			req.AddonSecretProvider = saved.AddonSecretProvider
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
		// Validation errors → 400 (see handleCreateConnection).
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
	if !authz.RequireWithResponse(w, r, "connection.delete") {
		return
	}
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
	if !authz.RequireWithResponse(w, r, "connection.set-active") {
		return
	}
	var req models.SetActiveConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// An empty body would set ConnectionName="" and surface as a
	// confusing 500 "connection \"\" not found" via writeServerError;
	// treat empty as a 400 with a clear field-specific message.
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
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 422 {object} map[string]interface{} "A saved credential was needed for an address that is not the saved connection's own"
// @Router /connections/test-credentials [post]
func (s *Server) handleTestCredentials(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "connection.test") {
		return
	}
	var req models.CreateConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// The contract splits into two paths:
	//
	//   - request body credentials present → test as submitted, optionally
	//     back-filling empty token fields from the saved record by name
	//     (preserves "I changed only the URL but kept the saved token").
	//   - use_saved=true → load the named saved connection's full config
	//     server-side and test that. The user never re-types the saved
	//     credential, and a missing saved connection surfaces as 400.
	//
	// Both Git and ArgoCD share the same saved record, so use_saved is a
	// single boolean — the wizard's blank-keep contract applies
	// symmetrically across both steps.
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
		// Back-compat for partial-body submits.
		//
		// SECURITY (v4-wave2 review B1): the caller controls the ADDRESS in
		// this same body — git.repo_url, git.provider, argocd.server_url.
		// Back-filling a stored secret into a body that names a different
		// address would hand that secret to an address the caller picked
		// (a self-hosted Git host, or an ArgoCD server, of their choosing).
		// So the stored secret is pinned to the connection's own address:
		// it is filled in only when the submitted address IS the saved one.
		// Ask about a different address and the answer is a refusal, not a
		// silent credential loan.
		if saved, err := s.connSvc.GetConnection(conn.Name); err == nil && saved != nil {
			if !backfillSavedCredentials(w, conn, saved) {
				return
			}
		}
	}

	gitErr, argocdErr, authInfo := s.connSvc.TestCredentials(r.Context(), conn)

	result := map[string]interface{}{
		"git":    map[string]interface{}{"status": "ok"},
		"argocd": map[string]interface{}{"status": "ok"},
	}
	if gitErr != nil {
		result["git"] = connectionErrorFields("git", gitErr)
	} else if authInfo.GitSource != "" {
		result["git"] = map[string]interface{}{"status": "ok", "auth": authInfo.GitSource}
	}
	if argocdErr != nil {
		result["argocd"] = connectionErrorFields("argocd", argocdErr)
	} else if authInfo.ArgocdSource != "" {
		result["argocd"] = map[string]interface{}{"status": "ok", "auth": authInfo.ArgocdSource}
	}

	// Mark the audit entry when the saved-credential path ran so the
	// test event is traceable distinctly from a fresh-body test.
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

// backfillSavedCredentials fills the blank credential fields of a
// test-credentials request from the saved connection of the same name — but
// only for an address that belongs to that saved connection.
//
// It returns true when the request may go on to be tested, and false when it
// has already answered the caller with a 422 refusal.
//
// Why the pin exists (v4-wave2 review B1): the stored Git token and ArgoCD
// token are secrets the caller never typed and, for a viewer or an operator,
// may never have been allowed to see. The rest of the same request body says
// WHERE to send them. Filling in the secret without checking the address is
// what turns a connectivity test into a way to read the stored credentials
// out of the server. Nothing is pinned when there is no stored secret to
// lend, and nothing is pinned when the caller gave no address at all —
// there is nothing to leak in either case.
func backfillSavedCredentials(w http.ResponseWriter, conn, saved *models.Connection) bool {
	// --- Git ---
	if conn.Git.Token == "" && conn.Git.PAT == "" &&
		(saved.Git.Token != "" || saved.Git.PAT != "") &&
		submittedGitAddress(conn.Git) {

		if !sameGitAddress(conn.Git, saved.Git) {
			writeError(w, http.StatusUnprocessableEntity, savedCredentialRefusal("Git repository"))
			return false
		}
		conn.Git.Token = saved.Git.Token
		conn.Git.PAT = saved.Git.PAT
	}

	// --- ArgoCD ---
	if conn.Argocd.Token == "" && saved.Argocd.Token != "" && conn.Argocd.ServerURL != "" {
		if !sameEndpoint(conn.Argocd.ServerURL, saved.Argocd.ServerURL) {
			writeError(w, http.StatusUnprocessableEntity, savedCredentialRefusal("ArgoCD server"))
			return false
		}
		conn.Argocd.Token = saved.Argocd.Token
	}

	return true
}

// savedCredentialRefusal is the plain-words body of the 422 above. It says
// what happened and what to do instead, and it deliberately does NOT echo the
// saved address back — that would answer "what is this connection pointing
// at?" for a caller who only asked to test something else.
func savedCredentialRefusal(what string) string {
	return "the saved credential for this connection belongs to a different " + what +
		" address than the one you sent, so it was not used. To test a different address, submit its credentials explicitly."
}

// submittedGitAddress reports whether the caller named a Git address at all.
// A body with no address has nowhere to send a token, so there is nothing to
// pin — the test simply fails downstream on the missing configuration, the
// same as it always did.
func submittedGitAddress(g models.GitRepoConfig) bool {
	return g.RepoURL != "" || g.Owner != "" || g.Organization != ""
}

// sameGitAddress reports whether the submitted Git block points at the same
// place the saved connection does.
//
// The comparison runs over the SAME derivation the test path itself uses:
// ParseRepoURL fills in the provider from the URL's host when the caller left
// it out, and the provider is what decides whether the token goes to
// api.github.com or to a self-hosted address taken straight out of repo_url.
// Comparing the derived provider plus the URL therefore covers every way the
// token's destination host can be chosen. Differences BELOW the host —
// owner, repo, project — are not part of the check: those stay on the saved
// connection's own host, which the token already belongs to.
func sameGitAddress(submitted, saved models.GitRepoConfig) bool {
	probe := submitted // copy — ParseRepoURL mutates its receiver
	_ = probe.ParseRepoURL()
	if probe.Provider != saved.Provider {
		return false
	}
	return sameEndpoint(probe.RepoURL, saved.RepoURL)
}

// sameEndpoint compares two addresses the way a Git host or an ArgoCD server
// would: case-insensitively, ignoring a trailing slash and the optional .git
// suffix. Anything beyond that cosmetic difference counts as a different
// address.
func sameEndpoint(a, b string) bool {
	return strings.EqualFold(normalizeEndpoint(a), normalizeEndpoint(b))
}

func normalizeEndpoint(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	return strings.TrimSuffix(s, "/")
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
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /connections/test [post]
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "connection.test") {
		return
	}
	gitErr, argocdErr := s.connSvc.TestConnection(r.Context())

	result := map[string]interface{}{
		"git":    map[string]interface{}{"status": "ok"},
		"argocd": map[string]interface{}{"status": "ok"},
	}

	if gitErr != nil {
		result["git"] = connectionErrorFields("git", gitErr)
	}
	if argocdErr != nil {
		result["argocd"] = connectionErrorFields("argocd", argocdErr)
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event: "connection_tested",
	})
	writeJSON(w, http.StatusOK, result)
}
