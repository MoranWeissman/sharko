package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// handleBatchRegisterClusters godoc
//
// @Summary Batch register clusters
// @Description Registers multiple clusters in a single operation
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Batch registration request with clusters array"
// @Success 200 {object} map[string]interface{} "All clusters registered"
// @Success 207 {object} map[string]interface{} "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/batch [post]
// handleBatchRegisterClusters handles POST /api/v1/clusters/batch — register multiple clusters.
func (s *Server) handleBatchRegisterClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.register") {
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
	if len(s.defaultAddons) > 0 {
		orch.SetDefaultAddons(s.defaultAddons)
	}
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.providerCfg != nil {
			roleARN = s.providerCfg.RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}

	result := orch.RegisterClusterBatch(r.Context(), req.Clusters)

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_registered",
		Resource: fmt.Sprintf("clusters:%d", len(req.Clusters)),
		Detail:   fmt.Sprintf("batch of %d", len(req.Clusters)),
	})

	status := http.StatusOK
	if result.Failed > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}
