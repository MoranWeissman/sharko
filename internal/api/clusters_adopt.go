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
// @Description On a v4 repo, phase 1 is replaced per cluster by the same takeover preflight the brownfield-takeover door runs: a cluster the preflight blocks fails with the preflight's summary as its error (the batch continues with the rest); a cluster it only warns about proceeds, and the warnings are listed on that cluster's result. The write is the same two v4 files a takeover writes (managed-clusters.yaml plus an empty cluster-addons/{name}.yaml), followed by the ArgoCD connection ownership swap and the adopted annotation once the pull request merges — no separate confirmation step beyond dry_run.
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
// @Failure 409 {object} map[string]interface{} "The repo carries both the old and the new layout (code repo_layout)"
// @Router /clusters/adopt [post]
func (s *Server) handleAdoptClusters(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.adopt") {
		return
	}
	// A v3 repo must migrate first (Story 5.1).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}

	// Validate body BEFORE any upstream call.
	var req orchestrator.AdoptClustersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Clusters) == 0 {
		writeError(w, http.StatusBadRequest, "at least one cluster name is required")
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

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if len(s.defaultAddons) > 0 {
		orch.SetDefaultAddons(s.defaultAddons)
	}
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}
	// On a v4 repo, AdoptClusters runs the takeover preflight per cluster,
	// which reads the ApplicationSets — same wiring migration.go uses for
	// the runtime handoff. nil is a valid, commonly-hit state (out-of-cluster
	// / no ApplicationSet access); the preflight then reports "could not
	// check" instead of claiming they are fine.
	orch.SetApplicationSetManager(s.appSetManager)

	result, err := orch.AdoptClusters(r.Context(), req)
	if err != nil {
		// A v4 repo is now a supported adopt door (v4-coherence-closure lane
		// D) — this stays only as a defensive mapping, matching the coded
		// shape the rest of the repo-layout family uses (CodeRepoLayout,
		// catalog_org.go).
		if orchestrator.IsV4RepoUnsupported(err) {
			writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Trigger canonical reconciler after adoption.
	if !req.DryRun && s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	// Enrich the audit entry. For batches we summarise all adopted clusters.
	successNames := []string{}
	for _, cr := range result.Results {
		if cr.Status == "success" || cr.Status == "partial" {
			successNames = append(successNames, cr.Name)
		}
	}
	resource := ""
	if len(successNames) == 1 {
		resource = fmt.Sprintf("cluster:%s", successNames[0])
	} else if len(successNames) > 1 {
		resource = fmt.Sprintf("clusters:%d", len(successNames))
	}
	hasFailure := false
	hasSuccess := false
	for _, cr := range result.Results {
		if cr.Status == "failed" {
			hasFailure = true
		} else {
			hasSuccess = true
		}
	}

	// RULING (f), C8: the audit result is set from what actually happened,
	// not derived from the HTTP status. 207 is only set when at least one
	// adoption succeeded, so an adoption where EVERY cluster failed returned
	// 200 and was recorded as "Cluster adopted · success" — a completed
	// adoption that adopted nothing.
	//
	// The HTTP status is deliberately NOT changed here. That an all-failed
	// adoption answers 200 is its own defect and belongs to the product
	// owner, not to a wording round; the audit log stops repeating it either
	// way.
	auditResult := "success"
	changes := audit.ChangesApplied
	switch {
	case hasFailure && !hasSuccess:
		auditResult = "failure"
		changes = audit.ChangesNone
	case hasFailure:
		auditResult = "partial"
	case !hasSuccess:
		// Nothing was asked for at all.
		changes = audit.ChangesNone
	}
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_adopted",
		Resource: resource,
		Result:   auditResult,
		Changes:  changes,
	})

	// Determine HTTP status.
	status := http.StatusOK
	if hasFailure && hasSuccess {
		status = http.StatusMultiStatus
	}

	writeJSON(w, status, result)
}

// handleUnadoptCluster godoc
//
// @Summary Un-adopt a cluster
// @Description Reverses adoption of a cluster — removes Sharko management but keeps the ArgoCD secret.
// @Description The cluster must have been adopted (has sharko.sharko.dev/adopted annotation).
// @Description On a v4 repo, the pull request removes the cluster from managed-clusters.yaml and deletes cluster-addons/{name}.yaml plus every Helm values file under values/clusters/{name}/ — everything this cluster owns in the repo.
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
// @Failure 409 {object} map[string]interface{} "Cluster was not adopted, or the repo carries both the old and the new layout (code repo_layout)"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/{name}/unadopt [post]
func (s *Server) handleUnadoptCluster(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.unadopt") {
		return
	}
	// A v3 repo must migrate first (Story 5.1).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	// Decode + validate body BEFORE upstream call.
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

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}

	result, err := orch.UnadoptCluster(r.Context(), name, req)
	if err != nil {
		// A v4 repo is now a supported unadopt door (v4-coherence-closure
		// lane D) — kept as a defensive mapping, coded shape like the rest
		// of the repo-layout family.
		if orchestrator.IsV4RepoUnsupported(err) {
			writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
			return
		}
		// Check if this is a "not adopted" error.
		if contains(err.Error(), "was not adopted") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Trigger canonical reconciler.
	if !req.DryRun && s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "cluster_unadopted",
		Resource: fmt.Sprintf("cluster:%s", name),
	})

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
