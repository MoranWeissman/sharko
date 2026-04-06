package api

import (
	"net/http"
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	p := parsePagination(r)
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
