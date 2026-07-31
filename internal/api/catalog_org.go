// The org's catalog over HTTP — the addons this org approved, and the one
// write path that adds to them.
//
// Design doc .bmad/output/architecture/2026-07-31-catalog-approved-model.md.
// Two words, no third one:
//
//	Marketplace  GET /api/v1/marketplace/addons — what you COULD run.
//	             Read-only, the curated list Sharko ships, discovery only.
//	Catalog      GET /api/v1/catalog/addons — what your org ALLOWS.
//	             Read straight from catalog.yaml in your git repo.
//
// Your clusters run only what is enabled from the Catalog. Nothing crosses
// from the Marketplace into the Catalog except through the pull request
// POST /api/v1/catalog/addons opens — that is the approval, and it is the
// only door, for every source, forever.
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

// loadOrgCatalog reads catalog.yaml from the given Git provider. A file
// that does not exist is NOT an error — a fresh repo has approved nothing
// yet, which is the intended day-zero state, not a fault.
func (s *Server) loadOrgCatalog(ctx context.Context, gp gitprovider.GitProvider) (config.AddonCatalogSpec, error) {
	data, err := gp.GetFileContent(ctx, config.AddonCatalogPath, s.gitopsConfig().BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return config.AddonCatalogSpec{}, nil
		}
		return config.AddonCatalogSpec{}, err
	}
	return config.LoadAddonCatalog(data)
}

// ApprovedAddonsForFreshness lists the org's approved addons for the
// background freshness scheduler, so it watches the charts the fleet
// actually runs and not only the ones Sharko ships. Resolves the active Git
// connection freshly on every call, so it is safe to hand to the scheduler
// before any connection exists — it simply errors until one does, and the
// scheduler carries on.
func (s *Server) ApprovedAddonsForFreshness(ctx context.Context) ([]catalog.ApprovedAddon, error) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		return nil, err
	}
	spec, err := s.loadOrgCatalog(ctx, gp)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(spec.Addons))
	for name := range spec.Addons {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]catalog.ApprovedAddon, 0, len(names))
	for _, name := range names {
		e := spec.Addons[name]
		out = append(out, catalog.ApprovedAddon{Name: name, RepoURL: e.RepoURL, Chart: e.Chart})
	}
	return out, nil
}

// orgCatalogListResponse is what the Catalog tab renders. Sorted by addon
// name so the list never reshuffles between reloads.
type orgCatalogListResponse struct {
	Addons []catalog.CatalogAddon `json:"addons"`
	Total  int                    `json:"total"`
}

// sortedCatalogAddons flattens the view into a name-sorted slice.
func sortedCatalogAddons(view map[string]catalog.CatalogAddon) []catalog.CatalogAddon {
	names := make([]string, 0, len(view))
	for name := range view {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]catalog.CatalogAddon, 0, len(names))
	for _, name := range names {
		out = append(out, view[name])
	}
	return out
}

// handleListOrgCatalog godoc
//
// @Summary List the addons your org approved
// @Description Returns the contents of catalog.yaml in your git repo — the addons this org allows on its clusters, and nothing else. The list Sharko ships is NOT included: it lives on GET /marketplace/addons as discovery only. Each entry is self-contained (chart, chart repo, version, namespace, settings, needed secrets) and carries `origin`: "curated" when the Marketplace also knows this addon by name (so a description and docs link are filled in), "internal" when only your own entry describes it. An entry missing its chart location comes back with `deployable: false` and `missing_fields` naming what to fill in, rather than failing the whole list. A repo with no catalog.yaml returns an empty list — a fresh repo approves nothing on purpose.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Success 200 {object} orgCatalogListResponse "The org's approved addons"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "No active Git connection"
// @Router /catalog/addons [get]
func (s *Server) handleListOrgCatalog(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}
	spec, err := s.loadOrgCatalog(r.Context(), gp)
	if err != nil {
		writeUpstreamError(w, "load_org_catalog", err)
		return
	}
	addons := sortedCatalogAddons(catalog.BuildCatalogView(s.catalog, spec))
	writeJSON(w, http.StatusOK, orgCatalogListResponse{Addons: addons, Total: len(addons)})
}

// handleGetOrgCatalogAddon godoc
//
// @Summary Get one approved addon
// @Description Single-addon form of GET /catalog/addons. 404 means the addon is not in your catalog.yaml — your org has not approved it, so it cannot be enabled on a cluster until somebody adds it.
// @Tags catalog
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} catalog.CatalogAddon "The approved addon"
// @Failure 400 {object} map[string]interface{} "Addon name missing"
// @Failure 404 {object} map[string]interface{} "Addon is not in your catalog"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Failure 503 {object} map[string]interface{} "No active Git connection"
// @Router /catalog/addons/{name} [get]
func (s *Server) handleGetOrgCatalogAddon(w http.ResponseWriter, r *http.Request) {
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
	spec, err := s.loadOrgCatalog(r.Context(), gp)
	if err != nil {
		writeUpstreamError(w, "load_org_catalog", err)
		return
	}
	entry, ok := catalog.BuildCatalogView(s.catalog, spec)[name]
	if !ok {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("%s is not in your catalog — add it first", name))
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

// handleAddToCatalog godoc
//
// @Summary Add addons to your org's catalog
// @Description Writes one or more full addon entries into catalog.yaml and opens a pull request — the approval step, and the only way anything enters the org. Three shapes, one endpoint: ONE addon (a single-element `addons` list); MANY addons (several elements — still exactly ONE pull request, which is what the first-run wizard needs); and add-AND-enable (`enable_on_cluster` set — one pull request touching catalog.yaml and clusters/<name>.yaml together, so the reviewer sees both halves in one diff and one merge makes both true). Set `from_marketplace: true` on an entry to copy the chart location, default namespace and needed-secrets list out of the curated list; you still choose the version, because the curated list deliberately ships none. Nothing is written unless everything checks out: an unknown cluster, an entry with no chart location, or an addon whose required values are not set all fail before a branch exists.
// @Tags catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AddToCatalogRequest true "Addons to add"
// @Success 201 {object} orchestrator.AddToCatalogResult "Pull request opened"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Cluster not registered"
// @Failure 422 {object} map[string]interface{} "The addons cannot be enabled as asked"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /catalog/addons [post]
func (s *Server) handleAddToCatalog(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "catalog.add") {
		return
	}

	var req orchestrator.AddToCatalogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Addons) == 0 {
		writeError(w, http.StatusBadRequest, "name at least one addon to add to the catalog")
		return
	}
	for _, a := range req.Addons {
		if a.Name == "" {
			writeError(w, http.StatusBadRequest, "every addon in the request needs a name")
			return
		}
		// The addon name becomes a values/global/<addon>.yaml path segment
		// and a Kubernetes label key the moment it is enabled anywhere.
		if !models.IsValidResourceName(a.Name) {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("invalid addon name %q: %s", a.Name, models.InvalidResourceNameMessage))
			return
		}
	}
	if req.EnableOnCluster != "" && !models.IsValidResourceName(req.EnableOnCluster) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid cluster name %q: %s", req.EnableOnCluster, models.InvalidResourceNameMessage))
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 2: configuration change — prefer the per-user PAT, fall back to
	// the service token. Same tiering as every other catalog write.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := s.attachPRTracker(orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsConfig(), s.repoPaths, nil))
	result, err := orch.AddToCatalog(ctx, req)
	if err != nil {
		writeAddToCatalogError(w, err)
		return
	}

	names := make([]string, 0, len(result.Added))
	names = append(names, result.Added...)
	detail := "addons=" + joinNames(names)
	if result.Cluster != "" {
		detail += " enabled_on=" + result.Cluster
	}
	audit.Enrich(ctx, audit.Fields{
		Event:    "catalog_addons_added",
		Resource: "catalog:" + joinNames(names),
		Detail:   detail,
	})
	writeJSON(w, http.StatusCreated, withAttributionWarning(result, tokRes))
}

// writeAddToCatalogError maps the operation's errors onto the status codes
// the UI branches on: 404 for a cluster nobody registered, 422 for "the
// request names something that cannot work", 502 for anything upstream.
func writeAddToCatalogError(w http.ResponseWriter, err error) {
	var semantic *orchestrator.V4SemanticValidationError
	var missing *catalog.MissingRequiredFieldError
	switch {
	case errors.Is(err, orchestrator.ErrV4ClusterNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.As(err, &semantic):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.As(err, &missing):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, orchestrator.ErrAddonNotInMarketplace):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// joinNames renders a name list for an audit line.
func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}
