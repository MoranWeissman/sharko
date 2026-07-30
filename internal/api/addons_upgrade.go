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

// handleUpgradeAddon godoc
//
// @Summary Upgrade addon
// @Description Upgrades an addon to a new version globally or for a specific cluster
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body map[string]interface{} true "Upgrade request with version and optional cluster"
// @Success 200 {object} map[string]interface{} "Upgrade result"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons/{name}/upgrade [post]
// handleUpgradeAddon handles POST /api/v1/addons/{name}/upgrade — upgrade an addon version.
// Body: {"version": "1.15.0"} for global, {"version": "1.15.0", "cluster": "prod-eu"} for per-cluster.
func (s *Server) handleUpgradeAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}
	// A v3 repo must migrate first (Story 5.1).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}

	addonName := r.PathValue("name")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	// Decode + validate body BEFORE upstream call.
	var req struct {
		Version   string `json:"version"`
		Cluster   string `json:"cluster"`
		AutoMerge *bool  `json:"auto_merge,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
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

	var result *orchestrator.GitResult
	if req.Cluster != "" {
		result, err = orch.UpgradeAddonCluster(r.Context(), addonName, req.Cluster, req.Version, req.AutoMerge)
	} else {
		result, err = orch.UpgradeAddonGlobal(r.Context(), addonName, req.Version, req.AutoMerge)
	}

	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	detail := fmt.Sprintf("to=%s", req.Version)
	if req.Cluster != "" {
		detail += fmt.Sprintf(" cluster=%s", req.Cluster)
	}
	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_upgraded",
		Resource: fmt.Sprintf("addon:%s", addonName),
		Detail:   detail,
	})
	writeJSON(w, http.StatusOK, result)
}

// handleUpgradeAddonsBatch godoc
//
// @Summary Batch upgrade addons
// @Description Upgrades multiple addons in a single atomic GitOps commit
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Batch upgrade request with upgrades map of addon->version"
// @Success 200 {object} map[string]interface{} "Batch upgrade result"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons/upgrade-batch [post]
// handleUpgradeAddonsBatch handles POST /api/v1/addons/upgrade-batch — upgrade multiple addons.
// Body: {"upgrades": {"cert-manager": "1.15.0", "metrics-server": "0.7.1"}}
func (s *Server) handleUpgradeAddonsBatch(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}
	// A v3 repo must migrate first (Story 5.1).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
		return
	}

	// Decode + validate body BEFORE upstream call.
	var req struct {
		Upgrades  map[string]string `json:"upgrades"`
		AutoMerge *bool             `json:"auto_merge,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Upgrades) == 0 {
		writeError(w, http.StatusBadRequest, "at least one addon upgrade is required")
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

	result, err := orch.UpgradeAddons(r.Context(), req.Upgrades, req.AutoMerge)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_upgraded",
		Resource: fmt.Sprintf("addons:%d", len(req.Upgrades)),
		Detail:   fmt.Sprintf("batch of %d upgrades", len(req.Upgrades)),
	})
	writeJSON(w, http.StatusOK, result)
}

// handleUpgradeAddonClustersV4 godoc
//
// @Summary Upgrade addon on a chosen subset of clusters (v4 format)
// @Description Bumps one addon's version pin to a new value on exactly the clusters listed in the request, in ONE pull request — the fleet-upgrade "one action, one PR" flow (v4 Wave 2 Epic 7 Story 7.2). Writes clusters/{cluster}.yaml (kind ClusterAddons) for every selected cluster only; clusters left out of the list are untouched, so the diff is one small block per cluster. Every selected cluster must already have the addon enabled — this endpoint never enables an addon as a side effect of upgrading it. Requires yes=true for confirmation (or dry_run=true to preview every file the PR would change before it opens).
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body orchestrator.UpgradeAddonClustersV4Request true "Subset upgrade request"
// @Success 200 {object} orchestrator.GitResult "Upgrade committed (or dry-run preview)"
// @Failure 400 {object} map[string]interface{} "Bad request or missing confirmation"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 502 {object} map[string]interface{} "Gateway error — including a cluster missing its assignment file, or the addon not enabled on a selected cluster"
// @Router /addons/{name}/upgrade-clusters [post]
// handleUpgradeAddonClustersV4 handles POST /api/v1/addons/{name}/upgrade-clusters.
// Body: {"clusters": ["prod-eu", "staging-us"], "version": "1.15.0", "yes": true}
func (s *Server) handleUpgradeAddonClustersV4(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.upgrade-clusters") {
		return
	}

	addonName := r.PathValue("name")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	var req orchestrator.UpgradeAddonClustersV4Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Addon = addonName

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

	result, err := orch.UpgradeAddonClustersV4(r.Context(), req)
	if err != nil {
		if err.Error() == "confirmation required: set yes: true in request body" {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_upgraded_clusters_v4",
		Resource: fmt.Sprintf("addon:%s", addonName),
		Detail:   fmt.Sprintf("to=%s clusters=%d", req.Version, len(req.Clusters)),
	})
	writeJSON(w, http.StatusOK, result)
}
