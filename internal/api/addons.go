package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/metrics"
	"github.com/MoranWeissman/sharko/internal/models"
)

// handleListAddons godoc
//
// @Summary List addons
// @Description Returns all addon ApplicationSets defined in the GitOps repository
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Addon list"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /addons/list [get]
func (s *Server) handleListAddons(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}

	addons, err := s.addonSvc.ListAddons(r.Context(), gp)
	if err != nil {
		// Upstream call (Git provider): classify.
		writeUpstreamError(w, "list_addons", err)
		return
	}

	qp := parseQueryParams(r)

	// Apply filter before pagination.
	addons = filterAddons(addons, qp.Filter)

	// Apply sort.
	sortAddons(addons, qp.Sort, qp.Order)

	p := paginationParams{Page: qp.Page, PerPage: qp.PerPage}
	setPaginationHeaders(w, len(addons), p)
	paged := applyPagination(addons, p)

	// B11: the catalog entries are the ON-DISK shape — models.AddonCatalogEntry
	// carries yaml tags as well as json ones, and Sharko reads this same struct
	// out of addons-catalog.yaml, changes one field and writes the whole thing
	// back. Its repoURL is routinely written with the access token inside it,
	// and this endpoint used to marshal the parsed entries straight out.
	//
	// The raw entries stay exactly as they were read — anything that fetches a
	// chart or writes the file back still has the operator's credential. Only
	// this copy, which exists solely to be a response, has it removed.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applicationsets": models.NewAddonCatalogEntryViews(paged),
	})
}

// handleGetAddonCatalog godoc
//
// @Summary Get addon catalog
// @Description Returns the full addon catalog with per-cluster deployment status.
// @Description Each addon carries deployed_cluster_count (clusters where the
// @Description ArgoCD Application is Synced + Healthy) and total_target_cluster_count
// @Description (clusters where the addon is labelled enabled in managed-clusters.yaml);
// @Description the UI uses the pair to render the tile-level "Running on N/M clusters" badge.
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Success 200 {object} models.AddonCatalogResponse "Addon catalog"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /addons/catalog [get]
func (s *Server) handleGetAddonCatalog(w http.ResponseWriter, r *http.Request) {
	// V2-3 SLO surface: catalog_scan. End-to-end timing only for PR 1;
	// per-phase wiring (catalog_load / list_addons / sources_refresh) is
	// deferred to V2-3.x because the existing service.GetCatalog call
	// composes the three phases internally and per-phase instrumentation
	// would require restructuring AddonService — explicitly out of scope
	// for this PR.
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	w = rec
	defer func() {
		code := strconv.Itoa(rec.statusCode)
		metrics.Observe(metrics.PathCatalogScan, "total", time.Since(start).Seconds(), logging.RequestID(r.Context()))
		metrics.IncTotal(metrics.PathCatalogScan, code)
		if rec.statusCode >= 400 {
			metrics.IncError(metrics.PathCatalogScan, code)
		}
	}()

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_argocd_client", err)
		return
	}

	resp, err := s.addonSvc.GetCatalog(r.Context(), gp, ac)
	if err != nil {
		// Upstream call (Git provider + ArgoCD): classify.
		writeUpstreamError(w, "get_addon_catalog", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetAddonDetail godoc
//
// @Summary Get addon detail
// @Description Returns detailed information for a specific addon including deployment status across clusters
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Addon detail"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 404 {object} map[string]interface{} "Addon not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /addons/{name} [get]
func (s *Server) handleGetAddonDetail(w http.ResponseWriter, r *http.Request) {
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

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_argocd_client", err)
		return
	}

	resp, err := s.addonSvc.GetAddonDetail(r.Context(), name, gp, ac)
	if err != nil {
		// Upstream call (Git provider + ArgoCD): classify.
		writeUpstreamError(w, "get_addon_detail", err)
		return
	}
	if resp == nil {
		writeError(w, http.StatusNotFound, "addon not found")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetAddonValues godoc
//
// @Summary Get addon values
// @Description Returns the default Helm values file for a specific addon
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Param name path string true "Addon name"
// @Success 200 {object} map[string]interface{} "Addon values"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /addons/{name}/values [get]
func (s *Server) handleGetAddonValues(w http.ResponseWriter, r *http.Request) {
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

	resp, err := s.addonSvc.GetAddonValues(r.Context(), name, gp)
	if err != nil {
		// Upstream call (Git provider): classify.
		writeUpstreamError(w, "get_addon_values", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetVersionMatrix godoc
//
// @Summary Get version matrix
// @Description Returns a matrix of addon versions deployed across all clusters
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Version matrix"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /addons/version-matrix [get]
func (s *Server) handleGetVersionMatrix(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_argocd_client", err)
		return
	}

	resp, err := s.addonSvc.GetVersionMatrix(r.Context(), gp, ac)
	if err != nil {
		// Upstream call (Git provider + ArgoCD): classify.
		writeUpstreamError(w, "get_version_matrix", err)
		return
	}

	s.enrichVersionMatrixFreshness(resp)
	writeJSON(w, http.StatusOK, resp)
}

// enrichVersionMatrixFreshness fills NewestAvailable/LastChecked on every
// row from the catalog version-freshness scheduler's background snapshots
// (v4 Wave 2 Epic 7 Story 7.1) — reusing the daily-cadence data
// catalog_freshness.go already exposes rather than issuing a live Helm
// fetch per addon on every matrix load. A nil scheduler (freshness
// disabled) or a row with no snapshot yet leaves both fields empty —
// never an error, matching the rest of the freshness surface's
// graceful-degrade contract.
func (s *Server) enrichVersionMatrixFreshness(resp *models.VersionMatrixResponse) {
	if s.freshness == nil || resp == nil {
		return
	}
	for i := range resp.Addons {
		snap, ok := s.freshness.VersionSnapshot(resp.Addons[i].AddonName)
		if !ok || snap.Unknown || len(snap.Versions) == 0 {
			continue
		}
		resp.Addons[i].NewestAvailable = snap.Versions[0].Version
		if !snap.CheckedAt.IsZero() {
			resp.Addons[i].LastChecked = snap.CheckedAt.UTC().Format(time.RFC3339)
		}
	}
}

// filterAddons filters an AddonCatalogEntry slice by the given filter expression.
// Supported forms:
//   - "name:<prefix>*"  — addon name starts with prefix
//   - "name:<value>"    — addon name equals value
func filterAddons(addons []models.AddonCatalogEntry, filter string) []models.AddonCatalogEntry {
	if filter == "" {
		return addons
	}
	field, value, found := strings.Cut(filter, ":")
	if !found {
		return addons
	}
	result := addons[:0:0]
	for _, a := range addons {
		switch field {
		case "name":
			if strings.HasSuffix(value, "*") {
				if strings.HasPrefix(a.Name, strings.TrimSuffix(value, "*")) {
					result = append(result, a)
				}
			} else if a.Name == value {
				result = append(result, a)
			}
		default:
			result = append(result, a)
		}
	}
	return result
}

// sortAddons sorts an AddonCatalogEntry slice in place by the given field and order.
// Supported sort fields: "name" (default), "chart", "version".
func sortAddons(addons []models.AddonCatalogEntry, field, order string) {
	sort.SliceStable(addons, func(i, j int) bool {
		var less bool
		switch field {
		case "chart":
			less = addons[i].Chart < addons[j].Chart
		case "version":
			less = addons[i].Version < addons[j].Version
		default: // "name" and anything else
			less = addons[i].Name < addons[j].Name
		}
		if order == "desc" {
			return !less
		}
		return less
	})
}
