package api

import (
	"net/http"
	"sort"
	"strings"

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
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	addons, err := s.addonSvc.ListAddons(r.Context(), gp)
	if err != nil {
		writeServerError(w, "list_addons", err)
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

	writeJSON(w, http.StatusOK, map[string]interface{}{"applicationsets": paged})
}

// handleGetAddonCatalog godoc
//
// @Summary Get addon catalog
// @Description Returns the full addon catalog with per-cluster deployment status
// @Tags addons
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Addon catalog"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Failure 503 {object} map[string]interface{} "Service unavailable"
// @Router /addons/catalog [get]
func (s *Server) handleGetAddonCatalog(w http.ResponseWriter, r *http.Request) {
	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.addonSvc.GetCatalog(r.Context(), gp, ac)
	if err != nil {
		writeServerError(w, "get_addon_catalog", err)
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
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.addonSvc.GetAddonDetail(r.Context(), name, gp, ac)
	if err != nil {
		writeServerError(w, "get_addon_detail", err)
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
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.addonSvc.GetAddonValues(r.Context(), name, gp)
	if err != nil {
		writeServerError(w, "get_addon_values", err)
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
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	resp, err := s.addonSvc.GetVersionMatrix(r.Context(), gp, ac)
	if err != nil {
		writeServerError(w, "get_version_matrix", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
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
