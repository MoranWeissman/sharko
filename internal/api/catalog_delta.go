// v4 wave 1 Story 3.2 ("delta model") + Story 3.3 ("internal addons") API
// surface: the merged view of the shipped curated catalog overlaid with the
// caller's own git catalog/addons.yaml (kind AddonCatalogDelta), and the
// write path that lets a caller add a first-class in-house addon to that
// delta file. Distinct from GET /api/v1/catalog/addons (the pure curated
// Marketplace browse list, Story 3.1's home) and from GET
// /api/v1/addons/catalog (the v3 addons-catalog.yaml deployed-catalog view)
// — this endpoint is the v4-specific one, and is additive: it does not
// change either existing surface's response shape.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// loadCatalogDelta reads the caller's v4 catalog/addons.yaml from the given
// Git provider and returns its parsed spec. A file that does not exist is
// NOT an error — design doc D16 "missing means empty" — it returns a
// zero-value spec instead, the same isGitFileNotFound convention already
// used by the v3 read handlers (e.g. AddonService.GetVersionMatrix).
func (s *Server) loadCatalogDelta(ctx context.Context, gp gitprovider.GitProvider) (config.AddonCatalogDeltaSpec, error) {
	data, err := gp.GetFileContent(ctx, config.AddonCatalogDeltaPath, "main")
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return config.AddonCatalogDeltaSpec{}, nil
		}
		return config.AddonCatalogDeltaSpec{}, err
	}
	return config.LoadAddonCatalogDelta(data)
}

// mergedCatalogListResponse is the envelope the UI consumes for the merged
// v4 catalog view. Sorted by addon name for a stable render.
type mergedCatalogListResponse struct {
	Addons []catalog.MergedAddon `json:"addons"`
	Total  int                   `json:"total"`
}

// sortedMergedAddons flattens a MergeDelta result into a name-sorted slice.
func sortedMergedAddons(merged map[string]catalog.MergedAddon) []catalog.MergedAddon {
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]catalog.MergedAddon, 0, len(names))
	for _, name := range names {
		out = append(out, merged[name])
	}
	return out
}

// handleListMergedCatalogDelta godoc
//
// @Summary List the merged v4 catalog (curated + your delta)
// @Description Overlays the caller's own git catalog/addons.yaml (kind AddonCatalogDelta) onto the shipped curated catalog and returns the merged, per-addon view: chart location, version (and where it came from), settings, and — for curated addons — the extended knowledge fields (description, required values, secrets, quirks, docs link). Every addon carries an `origin` of "curated" or "internal" (v4 wave 1 Story 3.3's in-house-addon marker). A repo with no catalog/addons.yaml yet returns the curated set untouched (design doc D16, "missing means empty").
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} mergedCatalogListResponse "Merged catalog"
// @Failure 422 {object} map[string]interface{} "An internal addon in your delta is missing repoURL, chart, or version"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded or no active Git connection"
// @Router /catalog/delta/addons [get]
func (s *Server) handleListMergedCatalogDelta(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}
	delta, err := s.loadCatalogDelta(r.Context(), gp)
	if err != nil {
		writeUpstreamError(w, "load_catalog_delta", err)
		return
	}
	merged, err := catalog.MergeDelta(s.catalog, delta)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	addons := sortedMergedAddons(merged)
	writeJSON(w, http.StatusOK, mergedCatalogListResponse{Addons: addons, Total: len(addons)})
}

// handleGetMergedCatalogDeltaAddon godoc
//
// @Summary Get one addon's merged v4 catalog view
// @Description Single-addon form of GET /catalog/delta/addons — the curated entry (if any) overlaid with the caller's own catalog/addons.yaml override or, for an in-house addon, defined entirely by it.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} catalog.MergedAddon "Merged catalog entry"
// @Failure 404 {object} map[string]interface{} "Addon not found in the curated catalog or your delta"
// @Failure 422 {object} map[string]interface{} "An internal addon in your delta is missing repoURL, chart, or version"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "Catalog not loaded or no active Git connection"
// @Router /catalog/delta/addons/{name} [get]
func (s *Server) handleGetMergedCatalogDeltaAddon(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog not loaded")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}
	delta, err := s.loadCatalogDelta(r.Context(), gp)
	if err != nil {
		writeUpstreamError(w, "load_catalog_delta", err)
		return
	}
	merged, err := catalog.MergeDelta(s.catalog, delta)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	entry, ok := merged[name]
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// handleAddInternalAddon godoc
//
// @Summary Add an in-house addon to your v4 catalog delta
// @Description Adds (or updates) one first-class in-house addon entry in your catalog/addons.yaml (kind AddonCatalogDelta), committed via a pull request like every other Sharko write. repo_url, chart, and version are all required — nothing else can supply them for an addon with no shipped catalog entry (design doc §2.3). The addon becomes assignable to clusters and appears in the merged catalog view (origin=internal) once the PR merges.
// @Tags catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AddInternalAddonRequest true "Internal addon request"
// @Success 201 {object} map[string]interface{} "Internal addon added"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /catalog/delta/addons [post]
func (s *Server) handleAddInternalAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "catalog.add-internal-addon") {
		return
	}

	var req orchestrator.AddInternalAddonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "addon name is required")
		return
	}
	// The addon name becomes a values/global/<addon>.yaml path segment and
	// a Kubernetes label key the moment somebody enables this addon on a
	// cluster, so it goes through the same gate the v4 enable/disable
	// endpoints use.
	if !models.IsValidResourceName(req.Name) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid addon name %q: %s", req.Name, models.InvalidResourceNameMessage))
		return
	}
	if req.RepoURL == "" {
		writeError(w, http.StatusBadRequest, "repo_url is required")
		return
	}
	if req.Chart == "" {
		writeError(w, http.StatusBadRequest, "chart is required")
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

	// Tier 2: configuration change — prefer per-user PAT, fall back to
	// service token. Same tiering as every other catalog write.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsConfig(), s.repoPaths, nil)
	s.attachPRTracker(orch)
	result, err := orch.AddInternalAddon(ctx, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "internal_addon_added",
		Resource: "addon:" + req.Name,
		Detail:   "chart=" + req.Chart + " version=" + req.Version + " origin=internal",
	})
	writeJSON(w, http.StatusCreated, withAttributionWarning(result, tokRes))
}
