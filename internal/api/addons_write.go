package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// handleAddAddon godoc
//
// @Summary Add addon
// @Description Adds a new addon to the catalog by creating its ApplicationSet in the GitOps repo
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AddAddonRequest true "Add addon request"
// @Success 201 {object} map[string]interface{} "Addon created"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons [post]
// handleAddAddon handles POST /api/v1/addons — add a new addon to the catalog.
func (s *Server) handleAddAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.add-to-catalog") {
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

	var req orchestrator.AddAddonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	if req.Chart == "" {
		writeError(w, http.StatusBadRequest, "addon chart is required")
		return
	}
	if req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "addon repo_url is required")
		return
	}
	if req.Version == "" {
		writeError(w, http.StatusBadRequest, "addon version is required")
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.AddAddon(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_added",
		Resource: fmt.Sprintf("addon:%s", req.Name),
		Detail:   fmt.Sprintf("chart=%s version=%s", req.Chart, req.Version),
	})
	writeJSON(w, http.StatusCreated, result)
}

// handleRemoveAddon godoc
//
// @Summary Remove addon
// @Description Removes an addon from the catalog. Without ?confirm=true returns a dry-run impact report.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param confirm query string false "Set to 'true' to confirm destructive removal"
// @Success 200 {object} map[string]interface{} "Addon removed"
// @Failure 400 {object} map[string]interface{} "Confirmation required or bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons/{name} [delete]
// handleRemoveAddon handles DELETE /api/v1/addons/{name} — remove an addon.
// Requires ?confirm=true query parameter. Without it, returns a dry-run impact report.
func (s *Server) handleRemoveAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.remove-from-catalog") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
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

	// Without ?confirm=true, return a dry-run impact report.
	if r.URL.Query().Get("confirm") != "true" {
		catalog, err := s.addonSvc.GetCatalog(r.Context(), git, ac)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to fetch addon catalog: "+err.Error())
			return
		}

		affectedClusters := []string{}
		found := false
		for _, addon := range catalog.Addons {
			if addon.AddonName != name {
				continue
			}
			found = true
			for _, app := range addon.Applications {
				affectedClusters = append(affectedClusters, app.ClusterName)
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "addon not found in catalog: "+name)
			return
		}

		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": "destructive operation requires ?confirm=true",
			"impact": map[string]interface{}{
				"addon":                        name,
				"affected_clusters":            affectedClusters,
				"total_deployments_to_remove":  len(affectedClusters),
				"warning":                      "ArgoCD will cascade-delete " + name + " from all affected clusters when the ApplicationSet entry is removed.",
			},
		})
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.RemoveAddon(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_removed",
		Resource: fmt.Sprintf("addon:%s", name),
	})
	writeJSON(w, http.StatusOK, result)
}

// handleConfigureAddon godoc
//
// @Summary Configure addon
// @Description Updates an addon's catalog configuration. Only provided fields are modified (merge semantics).
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body orchestrator.ConfigureAddonRequest true "Configuration update"
// @Success 200 {object} map[string]interface{} "Addon configured"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /addons/{name} [patch]
func (s *Server) handleConfigureAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}
	name := r.PathValue("name")
	if name == "" {
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

	var req orchestrator.ConfigureAddonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Name = name

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.ConfigureAddon(r.Context(), req)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(r.Context(), audit.Fields{
		Event:    "addon_configured",
		Resource: fmt.Sprintf("addon:%s", name),
	})
	writeJSON(w, http.StatusOK, result)
}
