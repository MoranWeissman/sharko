package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// handleBatchRegisterClusters handles POST /api/v1/clusters/batch — register multiple clusters.
func (s *Server) handleBatchRegisterClusters(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
		return
	}

	var req struct {
		Clusters []orchestrator.RegisterClusterRequest `json:"clusters"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if len(req.Clusters) == 0 {
		writeError(w, http.StatusBadRequest, "at least one cluster is required")
		return
	}
	if len(req.Clusters) > orchestrator.MaxBatchSize {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch size exceeds maximum of %d clusters", orchestrator.MaxBatchSize))
		return
	}

	// Validate all cluster names before processing.
	for _, c := range req.Clusters {
		if c.Name == "" {
			writeError(w, http.StatusBadRequest, "cluster name is required for all entries")
			return
		}
		if !validClusterNameRe.MatchString(c.Name) {
			writeError(w, http.StatusBadRequest, "invalid cluster name "+c.Name+": must be alphanumeric with hyphens, starting with alphanumeric")
			return
		}
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

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)

	result := orch.RegisterClusterBatch(r.Context(), req.Clusters)

	status := http.StatusOK
	if result.Failed > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}
