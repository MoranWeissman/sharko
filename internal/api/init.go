package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// handleInit handles POST /api/v1/init — initialize the addons repository.
func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	if s.templateFS == nil {
		writeError(w, http.StatusInternalServerError, "template filesystem not configured")
		return
	}

	var req orchestrator.InitRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Default to bootstrap if no body provided.
		req = orchestrator.InitRepoRequest{BootstrapArgoCD: true}
	}

	// Populate Git credentials for ArgoCD repository registration.
	if req.GitUsername == "" || req.GitToken == "" {
		conn, connErr := s.connSvc.GetActiveConnectionInfo()
		if connErr == nil {
			username, token := extractGitCredentials(conn)
			if req.GitUsername == "" {
				req.GitUsername = username
			}
			if req.GitToken == "" {
				req.GitToken = token
			}
		}
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, s.templateFS)
	result, err := orch.InitRepo(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "already") {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

// extractGitCredentials returns (username, token) from the active connection's Git config.
// It checks the connection config first, then falls back to environment variables.
func extractGitCredentials(conn *models.Connection) (string, string) {
	switch conn.Git.Provider {
	case models.GitProviderGitHub:
		token := conn.Git.Token
		if token == "" {
			token = os.Getenv("GITHUB_TOKEN")
		}
		if token != "" {
			return "x-access-token", token
		}
	case models.GitProviderAzureDevOps:
		pat := conn.Git.PAT
		if pat == "" {
			pat = os.Getenv("AZURE_DEVOPS_PAT")
		}
		if pat != "" {
			return conn.Git.Organization, pat
		}
	}
	return "", ""
}
