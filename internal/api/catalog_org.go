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
	"bytes"
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
//
// A file that DOES exist but is blank is a different thing: somebody made
// it and left it there without the two header lines. That gets
// orchestrator.ErrCatalogFileEmpty, which the handlers turn into a 422 with
// plain words about what to put in it — it used to come back as a 502
// "gateway error", which points at the git host for a problem that is in
// the repo (review finding L).
func (s *Server) loadOrgCatalog(ctx context.Context, gp gitprovider.GitProvider) (config.AddonCatalogSpec, error) {
	data, err := gp.GetFileContent(ctx, config.AddonCatalogPath, s.gitopsConfig().BaseBranch)
	if err != nil {
		if errors.Is(err, gitprovider.ErrFileNotFound) {
			return config.AddonCatalogSpec{}, nil
		}
		return config.AddonCatalogSpec{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return config.AddonCatalogSpec{}, fmt.Errorf("%w: %s",
			orchestrator.ErrCatalogFileEmpty, orchestrator.CatalogFileEmptyMessage)
	}
	return config.LoadAddonCatalog(data)
}

// writeOrgCatalogReadError answers a catalog READ failure. A blank
// catalog.yaml is the caller's repo to fix, not an upstream fault, so it
// gets a 422 with a code instead of the blanket 502.
func writeOrgCatalogReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, orchestrator.ErrCatalogFileEmpty) {
		writeCodedError(w, http.StatusUnprocessableEntity, CodeEmptyCatalogFile, err.Error(), nil)
		return
	}
	writeUpstreamError(w, "load_org_catalog", err)
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
		writeOrgCatalogReadError(w, err)
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
		writeOrgCatalogReadError(w, err)
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
// @Description Writes one or more full addon entries into catalog.yaml and opens a pull request — the approval step, and the only way anything enters the org. Three shapes, one endpoint: ONE addon (a single-element `addons` list); MANY addons (several elements — still exactly ONE pull request, which is what the first-run wizard needs); and add-AND-enable (`enable_on_cluster` set — one pull request touching catalog.yaml and cluster-addons/<name>.yaml together, so the reviewer sees both halves in one diff and one merge makes both true). The add-and-enable shape REQUIRES `yes: true`, the same confirmation the v4 enable endpoint asks for, because that half changes what runs on a real cluster; a catalog-only add needs no confirmation. Set `from_marketplace: true` on an entry to copy the chart location, default namespace and needed-secrets list out of the curated list; leave `version` empty there and the server fills in the newest version it knows for the chart (the same freshness data the version picker shows), so the resolved pin is visible in the pull-request diff — if Sharko has no version data for that chart you get a 422 with code `version_required` asking you to pick one. Nothing is written unless everything checks out: an unknown cluster, an entry with no chart location, or an addon whose required values are not set all fail before a branch exists.
// @Description Every 4xx body carries a machine-readable `code` next to the plain-English `error`, so a client branches on the code and never on the message text. Codes: `invalid_request` (400); `cluster_not_found` (404); `repo_layout` (409 — the repo is still v3, or carries both layouts at once); and on 422 one of `confirmation_required`, `empty_catalog_file`, `not_in_marketplace`, `version_required`, `incomplete_entry` (with a `problems` array naming each missing piece), `not_in_catalog`, or `validation_failed` (also with `problems`). A 502 means a genuine upstream/git failure and carries no code.
// @Tags catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param body body orchestrator.AddToCatalogRequest true "Addons to add"
// @Success 201 {object} orchestrator.AddToCatalogResult "Pull request opened"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Cluster not registered (code cluster_not_found)"
// @Failure 409 {object} map[string]interface{} "The repo is in a layout this endpoint does not write (code repo_layout)"
// @Failure 422 {object} map[string]interface{} "The addons cannot be added or enabled as asked — see the code field"
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
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest,
			"name at least one addon to add to the catalog", nil)
		return
	}
	for _, a := range req.Addons {
		if a.Name == "" {
			writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest,
				"every addon in the request needs a name", nil)
			return
		}
		// The addon name becomes a values/global/<addon>.yaml path segment
		// and a Kubernetes label key the moment it is enabled anywhere.
		if !models.IsValidResourceName(a.Name) {
			writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest,
				fmt.Sprintf("invalid addon name %q: %s", a.Name, models.InvalidResourceNameMessage), nil)
			return
		}
	}
	if req.EnableOnCluster != "" && !models.IsValidResourceName(req.EnableOnCluster) {
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("invalid cluster name %q: %s", req.EnableOnCluster, models.InvalidResourceNameMessage), nil)
		return
	}
	// The confirmation check is at the request edge as well as in the
	// orchestrator, so a caller who forgot `yes` gets told immediately
	// rather than after Sharko has resolved two upstream connections for a
	// request that was never going to be honoured. The orchestrator keeps
	// its own copy — it is the layer the CLI and any future caller go
	// through too.
	if req.EnableOnCluster != "" && !req.Yes && !req.DryRun {
		writeCodedError(w, http.StatusUnprocessableEntity, CodeConfirmationRequired,
			fmt.Sprintf("this also switches the addon on for %s, so send yes: true to confirm (or dry_run: true to see the change first)",
				req.EnableOnCluster), nil)
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
	// A Marketplace pick arrives with no version — the curated list ships
	// none on purpose — so the server fills in the newest version it knows
	// and that resolved pin is what lands in the pull request diff.
	orch.SetLatestVersionResolver(s.resolveLatestChartVersion)
	// Smart-values generation (v4 smartvalues wave): AddToCatalog fetches
	// each entry's official chart values.yaml itself, once chart+version
	// are final (which for a from_marketplace entry only happens after
	// the resolver above runs) — see ChartValuesFetcherFn's doc comment
	// for why this can't be a pre-fetch at the request edge the way v3's
	// AddAddon does it. Skip on dry-run for the same reason the v3 door
	// skips its pre-fetch: the preview shows only paths + create/update
	// actions, so the fetch (and the AI annotate pass below) can't change
	// what it shows, and skipping avoids burning registry/LLM quota on a
	// request that writes nothing.
	if !req.DryRun {
		orch.SetChartValuesFetcher(s.fetchChartValuesForV4Catalog)
	}
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

// The `code` values POST /catalog/addons returns alongside a 4xx. They are
// the machine-readable half of the error body: the UI branches on the code
// and shows the message, instead of matching on English text that changes
// (review finding B-2 — the combo's catalog gate was reading the message
// and firing on the wrong errors).
const (
	// CodeNotInCatalog — the addon is not in catalog.yaml, so it cannot be
	// switched on. This is the one the add-to-catalog-then-enable flow
	// watches for.
	CodeNotInCatalog = "not_in_catalog"
	// CodeIncompleteEntry — the catalog entry does not carry enough to
	// deploy. The body's `problems` array names each missing piece.
	CodeIncompleteEntry = "incomplete_entry"
	// CodeValidationFailed — the addon IS in the catalog and complete, but
	// this cluster is missing a required value or a secret definition.
	// `problems` names each one.
	CodeValidationFailed = "validation_failed"
	// CodeNotInMarketplace — a from_marketplace add naming something the
	// curated list does not have.
	CodeNotInMarketplace = "not_in_marketplace"
	// CodeVersionRequired — no version was sent and Sharko has no version
	// data for the chart to fill one in from.
	CodeVersionRequired = "version_required"
	// CodeEmptyCatalogFile — catalog.yaml exists in the repo but is blank.
	CodeEmptyCatalogFile = "empty_catalog_file"
	// CodeConfirmationRequired — an add-and-enable without yes: true.
	CodeConfirmationRequired = "confirmation_required"
	// CodeInvalidRequest — the request itself cannot be read (400).
	CodeInvalidRequest = "invalid_request"
	// CodeClusterNotFound — the named cluster is not registered (404).
	CodeClusterNotFound = "cluster_not_found"
	// CodeRepoLayout — the repo is in a shape this operation does not
	// write (409): still v3, already v4, or carrying both at once.
	CodeRepoLayout = "repo_layout"
	// CodeAddonEnabledOnClusters — a DELETE /catalog/addons/{name} request
	// for an addon that is still switched on in at least one cluster
	// (409). The body's `clusters` array names each one.
	CodeAddonEnabledOnClusters = "addon_enabled_on_clusters"
)

// writeCodedError writes {"error": msg, "code": code} plus any extra
// fields. One shape for every coded refusal so the UI has one thing to read.
func writeCodedError(w http.ResponseWriter, status int, code, msg string, extra map[string]interface{}) {
	body := map[string]interface{}{"error": msg, "code": code}
	for k, v := range extra {
		body[k] = v
	}
	writeJSON(w, status, body)
}

// writeAddToCatalogError maps the operation's errors onto the status codes
// and codes the UI branches on: 400 for a request that cannot be read, 404
// for a cluster nobody registered, 409 for a repo in a shape this does not
// write, 422 for "the request names something that cannot work", and 502
// only for a genuine upstream failure.
//
// Before this, every user-fixable problem the entry builder found — no
// version, no chart location, an addon the Marketplace does not carry —
// fell through to the 502 default, and the wizard's Marketplace pick dead
// ended on "gateway error" with nothing to act on (review finding B-1).
func writeAddToCatalogError(w http.ResponseWriter, err error) {
	var semantic *orchestrator.V4SemanticValidationError
	var missing *catalog.MissingRequiredFieldError
	switch {
	case errors.Is(err, orchestrator.ErrCatalogRequestInvalid):
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrV4ClusterNotFound):
		writeCodedError(w, http.StatusNotFound, CodeClusterNotFound, err.Error(), nil)
	case orchestrator.IsV4RepoUnsupported(err), errors.Is(err, orchestrator.ErrMixedRepoLayout):
		writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrCatalogConfirmationRequired):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeConfirmationRequired, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrCatalogFileEmpty):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeEmptyCatalogFile, err.Error(), nil)
	case errors.As(err, &semantic):
		writeCodedError(w, http.StatusUnprocessableEntity, semanticErrorCode(semantic), semantic.Error(),
			map[string]interface{}{
				"cluster":  semantic.Cluster,
				"addon":    semantic.Addon,
				"problems": semantic.Problems,
			})
	case errors.As(err, &missing):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeIncompleteEntry, missing.Error(),
			map[string]interface{}{
				"addon":    missing.Addon,
				"problems": []string{missing.Error()},
			})
	case errors.Is(err, orchestrator.ErrAddonNotInMarketplace):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeNotInMarketplace, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrCatalogVersionUnknown):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeVersionRequired, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrCatalogEntryIncomplete):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeIncompleteEntry, err.Error(),
			map[string]interface{}{"problems": []string{err.Error()}})
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// handleEditOrgCatalogAddon godoc
//
// @Summary Edit an approved addon
// @Description Changes one or more fields of an addon ALREADY in catalog.yaml and opens a pull request with exactly that edit — merge semantics: only the fields present in the request body change, everything else on the existing entry is left exactly as it was. This is an edit, not a rebuild — POST /api/v1/catalog/addons is the door for a brand-new entry. `settings` merges field-by-field onto whatever settings block is already there; `secrets`, `additional_sources` and `extra_helm_values` each replace the existing value whole when sent. Supports dry_run (returns a preview with the real diff, no side effects) and the same auto_merge override every other catalog write accepts. The real write is one pull request touching only catalog.yaml.
// @Tags catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param body body orchestrator.EditCatalogEntryRequest true "Fields to change"
// @Success 200 {object} orchestrator.GitResult "Pull request opened (or dry-run preview)"
// @Failure 400 {object} map[string]interface{} "Bad request, invalid addon name, or an empty edit (code invalid_request)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon is not in your catalog yet (code not_in_catalog) — add it first"
// @Failure 409 {object} map[string]interface{} "The repo is in a layout this endpoint does not write (code repo_layout)"
// @Failure 422 {object} map[string]interface{} "catalog.yaml exists but is blank (code empty_catalog_file), or the edit leaves the entry incomplete (code incomplete_entry, with a problems array)"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /catalog/addons/{name} [patch]
func (s *Server) handleEditOrgCatalogAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "catalog.update") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest, "addon name is required", nil)
		return
	}
	if !models.IsValidResourceName(name) {
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("invalid addon name %q: %s", name, models.InvalidResourceNameMessage), nil)
		return
	}

	var req orchestrator.EditCatalogEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	req.Name = name

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 2: configuration change — same tiering as every other catalog write.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := s.attachPRTracker(orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsConfig(), s.repoPaths, nil))
	result, err := orch.EditCatalogEntry(ctx, req)
	if err != nil {
		writeEditCatalogEntryError(w, err)
		return
	}

	if req.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "catalog_addon_edited",
		Resource: "catalog:" + name,
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// writeEditCatalogEntryError maps EditCatalogEntry's errors onto status
// codes: 400 for a request that cannot be read, 404 for an addon that is
// not in the catalog yet, 409 for a repo in a shape this does not write,
// 422 for "the edit cannot work as asked", and 502 only for a genuine
// upstream failure.
func writeEditCatalogEntryError(w http.ResponseWriter, err error) {
	var missing *catalog.MissingRequiredFieldError
	switch {
	case errors.Is(err, orchestrator.ErrCatalogRequestInvalid):
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrV4AddonNotInCatalog):
		writeCodedError(w, http.StatusNotFound, CodeNotInCatalog, err.Error(), nil)
	case orchestrator.IsV4RepoUnsupported(err), errors.Is(err, orchestrator.ErrMixedRepoLayout), orchestrator.IsV3RepoUnsupported(err):
		writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrCatalogFileEmpty):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeEmptyCatalogFile, err.Error(), nil)
	case errors.As(err, &missing):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeIncompleteEntry, missing.Error(),
			map[string]interface{}{"addon": missing.Addon, "problems": []string{missing.Error()}})
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// handleDeleteOrgCatalogAddon godoc
//
// @Summary Remove an approved addon
// @Description Removes one addon's entry from catalog.yaml — and its values/global/{name}.yaml (plus any stray values/clusters/*/{name}.yaml left over from an earlier enable/disable cycle) — as one pull request. Refuses with 409 (code addon_enabled_on_clusters) when the addon is still switched on in any cluster: a delete must never leave a cluster pointing an enabled addon at a catalog entry that no longer exists — switch it off on those clusters first (DELETE /api/v1/v4/clusters/{cluster}/addons/{addon}), then delete it here. Without confirmation (yes: true in the body, or ?confirm=true) and without dry_run, returns a 400 impact report naming every file the delete would touch — the same contract shape as DELETE /api/v1/addons/{name} (the v3 door), so a client written against that one confirms this one the same way. dry_run returns a 200 preview with real diffs.
// @Tags catalog
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Param confirm query string false "Set to 'true' to confirm destructive removal"
// @Param body body orchestrator.DeleteFromCatalogRequest false "Confirmation and options"
// @Success 200 {object} orchestrator.GitResult "Pull request opened (or dry-run preview)"
// @Failure 400 {object} map[string]interface{} "Bad request, invalid addon name, or confirmation required (body carries an impact report)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 404 {object} map[string]interface{} "Addon is not in your catalog (code not_in_catalog)"
// @Failure 409 {object} map[string]interface{} "The repo is in a layout this endpoint does not write (code repo_layout), or the addon is still enabled on one or more clusters (code addon_enabled_on_clusters, with a clusters array)"
// @Failure 422 {object} map[string]interface{} "catalog.yaml exists but is blank (code empty_catalog_file)"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /catalog/addons/{name} [delete]
func (s *Server) handleDeleteOrgCatalogAddon(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "catalog.remove") {
		return
	}

	name := r.PathValue("name")
	if name == "" {
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest, "addon name is required", nil)
		return
	}
	if !models.IsValidResourceName(name) {
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest,
			fmt.Sprintf("invalid addon name %q: %s", name, models.InvalidResourceNameMessage), nil)
		return
	}

	var reqBody struct {
		Yes       bool  `json:"yes,omitempty"`
		DryRun    bool  `json:"dry_run,omitempty"`
		AutoMerge *bool `json:"auto_merge,omitempty"`
	}
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
	}
	// Same dual query/body support as the v3 delete door (?confirm=true,
	// ?dry_run=true common for DELETE) — a client written against that one
	// confirms this door the same way.
	if r.URL.Query().Get("confirm") == "true" {
		reqBody.Yes = true
	}
	if r.URL.Query().Get("dry_run") == "true" {
		reqBody.DryRun = true
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Tier 2: configuration change.
	ctx, git, tokRes, err := s.GitProviderForTier(r.Context(), r, audit.Tier2)
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active Git connection: "+err.Error())
		return
	}

	orch := s.attachPRTracker(orchestrator.New(&s.gitMu, nil, ac, git, s.gitopsConfig(), s.repoPaths, nil))
	result, err := orch.DeleteFromCatalog(ctx, orchestrator.DeleteFromCatalogRequest{
		Name:      name,
		Yes:       reqBody.Yes,
		DryRun:    reqBody.DryRun,
		AutoMerge: reqBody.AutoMerge,
	})
	if err != nil {
		writeDeleteFromCatalogError(w, err)
		return
	}

	if reqBody.DryRun {
		writeJSON(w, http.StatusOK, result)
		return
	}

	audit.Enrich(ctx, audit.Fields{
		Event:    "catalog_addon_removed",
		Resource: "catalog:" + name,
	})
	writeJSON(w, http.StatusOK, withAttributionWarning(result, tokRes))
}

// writeDeleteFromCatalogError maps DeleteFromCatalog's errors onto status
// codes. Two shapes get bespoke bodies rather than the plain {error, code}
// writeCodedError produces: *CatalogDeleteBlockedError (409, plus a
// `clusters` array) and *CatalogDeleteConfirmationError (400, plus an
// `impact` object — deliberately the v3 RemoveAddon status code, not the
// 422 every other catalog-write confirmation gate in this package uses).
func writeDeleteFromCatalogError(w http.ResponseWriter, err error) {
	var blocked *orchestrator.CatalogDeleteBlockedError
	var confirmErr *orchestrator.CatalogDeleteConfirmationError
	switch {
	case errors.Is(err, orchestrator.ErrCatalogRequestInvalid):
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrV4AddonNotInCatalog):
		writeCodedError(w, http.StatusNotFound, CodeNotInCatalog, err.Error(), nil)
	case orchestrator.IsV4RepoUnsupported(err), errors.Is(err, orchestrator.ErrMixedRepoLayout), orchestrator.IsV3RepoUnsupported(err):
		writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrCatalogFileEmpty):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeEmptyCatalogFile, err.Error(), nil)
	case errors.As(err, &blocked):
		writeCodedError(w, http.StatusConflict, CodeAddonEnabledOnClusters, blocked.Error(),
			map[string]interface{}{"addon": blocked.Addon, "clusters": blocked.Clusters})
	case errors.As(err, &confirmErr):
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": confirmErr.Error(),
			"impact": map[string]interface{}{
				"addon":         confirmErr.Addon,
				"files_removed": confirmErr.FilesRemoved,
			},
		})
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// semanticErrorCode splits the two things a V4SemanticValidationError can
// mean, so the UI can tell "this entry is half-written" (fix the catalog)
// from "this cluster is missing a value or a secret" (fix the cluster).
func semanticErrorCode(e *orchestrator.V4SemanticValidationError) string {
	if e.Code != "" {
		return e.Code
	}
	return CodeValidationFailed
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
