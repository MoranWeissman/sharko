package api

import (
	"encoding/json"
	"net/http"

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

	addonName := r.PathValue("name")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
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

	var req struct {
		Version string `json:"version"`
		Cluster string `json:"cluster"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "version is required")
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)

	var result *orchestrator.GitResult
	if req.Cluster != "" {
		result, err = orch.UpgradeAddonCluster(r.Context(), addonName, req.Cluster, req.Version)
	} else {
		result, err = orch.UpgradeAddonGlobal(r.Context(), addonName, req.Version)
	}

	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

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

	var req struct {
		Upgrades map[string]string `json:"upgrades"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Upgrades) == 0 {
		writeError(w, http.StatusBadRequest, "at least one addon upgrade is required")
		return
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider, ac, git, s.gitopsCfg, s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)

	result, err := orch.UpgradeAddons(r.Context(), req.Upgrades)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}
