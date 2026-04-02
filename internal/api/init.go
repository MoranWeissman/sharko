package api

import (
	"encoding/json"
	"net/http"
	"strings"

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

	orch := orchestrator.New(s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, s.templateFS)
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
