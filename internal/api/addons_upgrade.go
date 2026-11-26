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
// @Description Upgrades an addon to a new version, for all clusters or for one cluster, in one pull request. For all clusters (no "cluster" in the body): sets the addon's version in the catalog, removes the addon's own version pin from every cluster that has it switched on (a cluster already on a newer version is left as it is and named in warnings), and moves the settings in the addon's generated global values file that are still the old chart's defaults to the new chart's defaults (a setting somebody changed is never touched). For one cluster: sets that cluster's own version pin. The version must exist in the addon's chart repository; a version that is not there is refused before any pull request (400, code version_not_found). Send dry_run=true to get the exact files and diffs without opening a pull request.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body map[string]interface{} true "Upgrade request: version (required), cluster (optional, one cluster only), dry_run (optional), auto_merge (optional)"
// @Success 200 {object} orchestrator.GitResult "Upgrade pull request opened (or the dry-run preview). warnings lists anything the upgrade left on the old chart's values."
// @Failure 400 {object} map[string]interface{} "Bad request: no version, a version the chart repository does not have (code version_not_found), nothing to change (code nothing_to_change), or the addon is not switched on on that cluster"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "The addon is not in the catalog"
// @Failure 409 {object} map[string]interface{} "The repo carries two layouts at once and has to be cleaned up first"
// @Failure 502 {object} map[string]interface{} "Sharko could not reach the Git host or the chart repository (code chart_repo_unreadable)"
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
		DryRun    bool   `json:"dry_run,omitempty"`
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
		writeNoActiveArgocdConnection(w, r)
		return
	}

	// Tiered resolver, so the pull request carries the caller's attribution
	// — the same as the some-clusters upgrade below.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier1)
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	s.wireChartChecks(orch)

	result, err := orch.UpgradeAddon(ctx, orchestrator.UpgradeAddonRequest{
		Addon: addonName, Version: req.Version, Cluster: req.Cluster,
		DryRun: req.DryRun, AutoMerge: req.AutoMerge,
	})
	if err != nil {
		writeUpgradeError(w, err)
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
		return
	}

	detail := fmt.Sprintf("to=%s", req.Version)
	if req.Cluster != "" {
		detail += fmt.Sprintf(" cluster=%s", req.Cluster)
	} else {
		detail += " clusters=all"
	}
	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_upgraded",
		Resource: fmt.Sprintf("addon:%s", addonName),
		Detail:   detail,
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// handleUpgradeAddonsBatch godoc
//
// @Summary Batch upgrade addons
// @Description Upgrades several addons for all clusters in one pull request. Each addon is upgraded the same way POST /addons/{name}/upgrade does it for all clusters. Every version must exist in its chart repository; one that is not there refuses the whole request before any pull request.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body map[string]interface{} true "Batch upgrade request with upgrades map of addon->version"
// @Success 200 {object} orchestrator.GitResult "Batch upgrade pull request opened"
// @Failure 400 {object} map[string]interface{} "Bad request: an empty list, a version the chart repository does not have (code version_not_found), or nothing to change"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "An addon is not in the catalog"
// @Failure 409 {object} map[string]interface{} "The repo carries two layouts at once and has to be cleaned up first"
// @Failure 502 {object} map[string]interface{} "Sharko could not reach the Git host or the chart repository"
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
		writeNoActiveArgocdConnection(w, r)
		return
	}

	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier1)
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	s.wireChartChecks(orch)

	result, err := orch.UpgradeAddons(ctx, req.Upgrades, req.AutoMerge)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_upgraded",
		Resource: fmt.Sprintf("addons:%d", len(req.Upgrades)),
		Detail:   fmt.Sprintf("batch of %d upgrades", len(req.Upgrades)),
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
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
// @Failure 400 {object} map[string]interface{} "Bad request: missing confirmation, a version the chart repository does not have (code version_not_found), nothing to change (code nothing_to_change), a cluster with no assignment file, or the addon not switched on on a chosen cluster"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Failure 409 {object} map[string]interface{} "The connected repo is still in the v3 format and has to be migrated first"
// @Failure 502 {object} map[string]interface{} "Sharko could not reach the Git host or the chart repository (code chart_repo_unreadable)"
// @Router /addons/{name}/upgrade-clusters [post]
// handleUpgradeAddonClustersV4 handles POST /api/v1/addons/{name}/upgrade-clusters.
// Body: {"clusters": ["prod-eu", "staging-us"], "version": "1.15.0", "yes": true}
func (s *Server) handleUpgradeAddonClustersV4(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.upgrade-clusters") {
		return
	}
	// A v3 repo must migrate first (Story 5.1) — this handler writes the v4
	// clusters/{cluster}.yaml files, which a v3 repo does not have. Missing
	// here while every sibling write had it (v4-wave2 review L2).
	if s.refuseV3WriteOnActiveRepo(r.Context(), w) {
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
		writeNoActiveArgocdConnection(w, r)
		return
	}
	// Tiered resolver rather than the legacy provider, so the pull request
	// this opens carries the caller's attribution and the audit entry gets
	// its tier stamped (v4-wave2 review L2).
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier1)
	if err != nil {
		writeNoActiveGitConnection(w, r)
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	s.wireChartChecks(orch)

	result, err := orch.UpgradeAddonClustersV4(ctx, req)
	if err != nil {
		writeUpgradeError(w, err)
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_upgraded_clusters_v4",
		Resource: fmt.Sprintf("addon:%s", addonName),
		Detail:   fmt.Sprintf("to=%s clusters=%d", req.Version, len(req.Clusters)),
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}
