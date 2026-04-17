// Package api — values editor endpoints (v1.20).
//
// Four endpoints power the in-app values editor:
//
//   PUT  /api/v1/addons/{name}/values                     — Tier 2: edit global values
//   PUT  /api/v1/clusters/{cluster}/addons/{name}/values  — Tier 2: edit per-cluster overrides
//   GET  /api/v1/addons/{name}/values-schema              — read: current values + optional JSON schema
//   GET  /api/v1/clusters/{cluster}/addons/{name}/values  — read: current overrides + optional schema
//
// The two PUTs both follow the same flow:
//   1. Resolve a Git provider via Server.GitProviderForTier(ctx, r, audit.Tier2),
//      which returns the right token (per-user PAT preferred, service token
//      fallback) and stamps audit attribution metadata.
//   2. Write the new YAML through the orchestrator's commit helpers, which
//      open a PR (and optionally auto-merge per the active connection's
//      gitops.PRAutoMerge setting).
//   3. Wrap the response with withAttributionWarning so the UI gets the
//      "no_per_user_pat" nudge signal when Tier 2 fell back to the service
//      token.
//
// The editor explicitly accepts the *full* file contents for global values
// (not a diff) so the resulting Git PR shows a clean before/after for human
// review. The per-cluster endpoint accepts only the addon's section — the
// orchestrator merges it into the existing per-cluster overrides file,
// preserving other addons and `clusterGlobalValues:`.

package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"

	"gopkg.in/yaml.v3"
)

// setAddonValuesRequest is the body of PUT /api/v1/addons/{name}/values.
type setAddonValuesRequest struct {
	// Values is the full YAML file content. Sharko commits this verbatim
	// as the new global default values for the addon.
	Values string `json:"values"`
}

// setClusterAddonValuesRequest is the body of
// PUT /api/v1/clusters/{cluster}/addons/{name}/values. Values here is the
// YAML for the addon's section in the cluster overrides file (NOT the
// whole file). Pass an empty string to remove the addon's overrides.
type setClusterAddonValuesRequest struct {
	Values string `json:"values"`
}

// handleSetAddonValues godoc
//
// @Summary Edit global addon values
// @Description Replaces the global default Helm values YAML for an addon and opens a PR. Tier 2 (configuration) — prefers the caller's personal GitHub PAT for proper Git authorship; otherwise falls back to the service token with a `Co-authored-by:` trailer and surfaces `attribution_warning: "no_per_user_pat"` in the response.
// @Tags addons
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body api.setAddonValuesRequest true "Full YAML payload"
// @Success 200 {object} map[string]interface{} "PR created (or merged if auto-merge is on)"
// @Failure 400 {object} map[string]interface{} "Invalid YAML or missing field"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 502 {object} map[string]interface{} "Git or ArgoCD failure"
// @Router /addons/{name}/values [put]
func (s *Server) handleSetAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	var req setAddonValuesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	// We intentionally accept an empty values payload — that resets the
	// addon to "use chart defaults". Validation is YAML well-formedness.
	if err := yaml.Unmarshal([]byte(req.Values), new(interface{})); err != nil {
		writeError(w, http.StatusBadRequest, "invalid YAML: "+err.Error())
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 2: configuration change — prefer per-user PAT, fall back to service token.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	// Existence check: surface a 404 instead of a confusing PR-against-an-unknown-addon.
	addon, gerr := s.addonSvc.GetAddonDetail(ctx, name, git, ac)
	if gerr != nil {
		writeError(w, http.StatusBadGateway, "looking up addon: "+gerr.Error())
		return
	}
	if addon == nil {
		writeError(w, http.StatusNotFound, "addon not found in catalog: "+name)
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.SetGlobalAddonValues(ctx, name, req.Values)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if s.prTracker != nil && result != nil && result.PRID > 0 {
		user := r.Header.Get("X-Sharko-User")
		if user == "" {
			user = "system"
		}
		_ = s.prTracker.TrackPR(ctx, prtracker.PRInfo{
			PRID:       result.PRID,
			PRUrl:      result.PRUrl,
			PRBranch:   result.Branch,
			PRTitle:    "Update global values for " + name,
			PRBase:     "main",
			Addon:      name,
			Operation:  "values-edit",
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "addon_values_edited",
		Resource: fmt.Sprintf("addon:%s", name),
		Detail:   fmt.Sprintf("file=%s bytes=%d", result.ValuesFile, len(req.Values)),
	})

	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// handleSetClusterAddonValues godoc
//
// @Summary Edit per-cluster addon overrides
// @Description Replaces the per-cluster overrides YAML for one addon on one cluster and opens a PR. Pass an empty `values` string to remove the addon's overrides entirely. Tier 2 (configuration) — same attribution semantics as the global values endpoint.
// @Tags clusters
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param cluster path string true "Cluster name"
// @Param name path string true "Addon name"
// @Param body body api.setClusterAddonValuesRequest true "Addon-section YAML payload"
// @Success 200 {object} map[string]interface{} "PR created (or merged if auto-merge is on)"
// @Failure 400 {object} map[string]interface{} "Invalid YAML or missing field"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Git or ArgoCD failure"
// @Router /clusters/{cluster}/addons/{name}/values [put]
func (s *Server) handleSetClusterAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.update-catalog") {
		return
	}

	cluster := r.PathValue("cluster")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	addonName := r.PathValue("name")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	var req setClusterAddonValuesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Values) != "" {
		if err := yaml.Unmarshal([]byte(req.Values), new(interface{})); err != nil {
			writeError(w, http.StatusBadRequest, "invalid YAML: "+err.Error())
			return
		}
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsCfg, s.repoPaths, nil)
	result, err := orch.SetClusterAddonValues(ctx, cluster, addonName, req.Values)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if s.prTracker != nil && result != nil && result.PRID > 0 {
		user := r.Header.Get("X-Sharko-User")
		if user == "" {
			user = "system"
		}
		_ = s.prTracker.TrackPR(ctx, prtracker.PRInfo{
			PRID:       result.PRID,
			PRUrl:      result.PRUrl,
			PRBranch:   result.Branch,
			PRTitle:    fmt.Sprintf("Update %s overrides on cluster %s", addonName, cluster),
			PRBase:     "main",
			Cluster:    cluster,
			Addon:      addonName,
			Operation:  "values-edit",
			User:       user,
			Source:     "api",
			CreatedAt:  time.Now(),
			LastStatus: "open",
		})
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "cluster_addon_values_edited",
		Resource: fmt.Sprintf("cluster:%s addon:%s", cluster, addonName),
		Detail:   fmt.Sprintf("file=%s bytes=%d", result.ValuesFile, len(req.Values)),
	})

	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// handleGetAddonValuesSchema godoc
//
// @Summary Get addon global values + schema
// @Description Returns the addon's current global values YAML and an optional JSON Schema (read from `<addon>.schema.json` next to the values file). The schema is omitted when none has been published; the UI then falls back to plain YAML mode.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Current values + optional schema"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 502 {object} map[string]interface{} "Git failure"
// @Router /addons/{name}/values-schema [get]
func (s *Server) handleGetAddonValuesSchema(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "addon.list") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.addonSvc.GetAddonValuesAndSchema(r.Context(), name, gp)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetClusterAddonValues godoc
//
// @Summary Get per-cluster addon overrides + schema
// @Description Returns the YAML for a single addon's section in a cluster's overrides file, plus an optional JSON Schema. `current_overrides` is empty when no overrides are configured for this addon yet.
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param cluster path string true "Cluster name"
// @Param name path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Current overrides + optional schema"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 502 {object} map[string]interface{} "Git failure"
// @Router /clusters/{cluster}/addons/{name}/values [get]
func (s *Server) handleGetClusterAddonValues(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "cluster.detail") {
		return
	}

	cluster := r.PathValue("cluster")
	if cluster == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}
	addonName := r.PathValue("name")
	if addonName == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.clusterSvc.GetClusterAddonValues(r.Context(), cluster, addonName, gp)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
