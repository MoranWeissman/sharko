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

// handleAdoptClusters godoc
//
// @Summary Adopt existing ArgoCD clusters
// @Description Adopts one or more existing ArgoCD clusters under Sharko management.
// @Description Phase 1 verifies connectivity per cluster, Phase 2 creates GitOps config via PR.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AdoptClustersRequest true "Adoption request"
// @Success 200 {object} orchestrator.AdoptClustersResult "Adoption results (may include dry_run)"
// @Success 207 {object} orchestrator.AdoptClustersResult "Partial success — some clusters failed"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/adopt [post]
func (s *Server) handleAdoptClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.adopt") {
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

	var req orchestrator.AdoptClustersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Clusters) == 0 {
		writeError(w, http.StatusBadRequest, "at least one cluster name is required")
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

	result, err := orch.AdoptClusters(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Trigger reconciler after adoption.
	if !req.DryRun && s.argoSecretReconciler != nil {
		s.argoSecretReconciler.Trigger()
	}

	// Emit audit events per cluster.
	for _, cr := range result.Results {
		if cr.Status == "success" || cr.Status == "partial" {
			s.auditLog.Add(audit.Entry{
				Level:    "info",
				Event:    "cluster_adopted",
				User:     "sharko",
				Action:   "adopt",
				Resource: fmt.Sprintf("cluster:%s", cr.Name),
				Source:   "api",
				Result:   cr.Status,
			})
		}
	}

	// Determine HTTP status.
	status := http.StatusOK
	hasFailure := false
	hasSuccess := false
	for _, cr := range result.Results {
		if cr.Status == "failed" {
			hasFailure = true
		} else {
			hasSuccess = true
		}
	}
	if hasFailure && hasSuccess {
		status = http.StatusMultiStatus
	}

	writeJSON(w, status, result)
}

// handleUnadoptCluster godoc
//
// @Summary Un-adopt a cluster
// @Description Reverses adoption of a cluster — removes Sharko management but keeps the ArgoCD secret.
// @Description The cluster must have been adopted (has sharko.sharko.io/adopted annotation).
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Param body body orchestrator.UnadoptClusterRequest true "Unadopt request (requires yes: true)"
// @Success 200 {object} orchestrator.UnadoptClusterResult "Cluster unadopted"
// @Success 207 {object} orchestrator.UnadoptClusterResult "Partial success"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 409 {object} map[string]interface{} "Cluster was not adopted"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/unadopt [post]
func (s *Server) handleUnadoptCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.unadopt") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	var req orchestrator.UnadoptClusterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Require confirmation unless dry-run.
	if !req.DryRun && !req.Yes {
		writeError(w, http.StatusBadRequest, "confirmation required: set yes: true in request body")
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.providerCfg != nil {
			roleARN = s.providerCfg.RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}

	result, err := orch.UnadoptCluster(r.Context(), name, req)
	if err != nil {
		// Check if this is a "not adopted" error.
		if contains(err.Error(), "was not adopted") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Trigger reconciler.
	if !req.DryRun && s.argoSecretReconciler != nil {
		s.argoSecretReconciler.Trigger()
	}

	// Audit event.
	if result.Status == "success" || result.Status == "partial" {
		s.auditLog.Add(audit.Entry{
			Level:    "info",
			Event:    "cluster_unadopted",
			User:     "sharko",
			Action:   "unadopt",
			Resource: fmt.Sprintf("cluster:%s", name),
			Source:   "api",
			Result:   result.Status,
		})
	}

	status := http.StatusOK
	if result.Status == "partial" {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, result)
}

// contains is a simple string contains helper to avoid importing strings in this file.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
