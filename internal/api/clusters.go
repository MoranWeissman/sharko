package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
)

// handleListClusters handles GET /api/v1/clusters
//
// @Summary List clusters
// @Description Returns all registered clusters with health stats
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{}
// @Router /clusters [get]
func (s *Server) handleListClusters(w http.ResponseWriter, r *http.Request) {
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

	resp, err := s.clusterSvc.ListClusters(r.Context(), gp, ac)
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "list_clusters", err)
		return
	}

	qp := parseQueryParams(r)

	// Apply filter before pagination.
	resp.Clusters = filterClusters(resp.Clusters, qp.Filter)

	// Apply sort.
	sortClusters(resp.Clusters, qp.Sort, qp.Order)

	p := paginationParams{Page: qp.Page, PerPage: qp.PerPage}
	setPaginationHeaders(w, len(resp.Clusters), p)
	resp.Clusters = applyPagination(resp.Clusters, p)

	writeJSON(w, http.StatusOK, resp)
}

// filterClusters filters a cluster slice by the given filter expression.
// Supported forms:
//   - "name:<prefix>*"  — cluster name starts with prefix
//   - "name:<value>"    — cluster name equals value
//   - "managed:true"    — only managed clusters
//   - "managed:false"   — only unmanaged clusters
func filterClusters(clusters []models.Cluster, filter string) []models.Cluster {
	if filter == "" {
		return clusters
	}
	field, value, found := strings.Cut(filter, ":")
	if !found {
		return clusters
	}
	result := clusters[:0:0]
	for _, c := range clusters {
		switch field {
		case "name":
			if strings.HasSuffix(value, "*") {
				if strings.HasPrefix(c.Name, strings.TrimSuffix(value, "*")) {
					result = append(result, c)
				}
			} else if c.Name == value {
				result = append(result, c)
			}
		case "managed":
			if (value == "true") == c.Managed {
				result = append(result, c)
			}
		default:
			result = append(result, c)
		}
	}
	return result
}

// sortClusters sorts a cluster slice in place by the given field and order.
// Supported sort fields: "name" (default), "status".
func sortClusters(clusters []models.Cluster, field, order string) {
	sort.SliceStable(clusters, func(i, j int) bool {
		var less bool
		switch field {
		case "status":
			less = clusters[i].ConnectionStatus < clusters[j].ConnectionStatus
		default: // "name" and anything else
			less = clusters[i].Name < clusters[j].Name
		}
		if order == "desc" {
			return !less
		}
		return less
	})
}

// handleGetCluster godoc
//
// @Summary Get cluster
// @Description Returns details for a single registered cluster
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Cluster detail"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 404 {object} map[string]interface{} "Cluster not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /clusters/{name} [get]
func (s *Server) handleGetCluster(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
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

	resp, err := s.clusterSvc.GetClusterDetail(r.Context(), name, gp, ac)
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "get_cluster", err)
		return
	}
	if resp == nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetClusterValues godoc
//
// @Summary Get cluster values
// @Description Returns the Helm values file for a specific cluster
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Cluster values"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /clusters/{name}/values [get]
func (s *Server) handleGetClusterValues(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}

	resp, err := s.clusterSvc.GetClusterValues(r.Context(), name, gp)
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "get_cluster_values", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetConfigDiff godoc
//
// @Summary Get cluster config diff
// @Description Returns a diff of the cluster's current config versus the desired state
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Config diff"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /clusters/{name}/config-diff [get]
func (s *Server) handleGetConfigDiff(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
		return
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		writeServerError(w, http.StatusServiceUnavailable, "get_active_git_provider", err)
		return
	}

	resp, err := s.clusterSvc.GetConfigDiff(r.Context(), name, gp)
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "get_cluster_config_diff", err)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleGetClusterComparison godoc
//
// @Summary Get cluster comparison
// @Description Compares the cluster's Git state against ArgoCD live state
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Comparison result"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 404 {object} map[string]interface{} "Cluster not found"
// @Failure 500 {object} map[string]interface{} "Internal error"
// @Router /clusters/{name}/comparison [get]
func (s *Server) handleGetClusterComparison(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "cluster name is required")
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

	resp, err := s.clusterSvc.GetClusterComparison(r.Context(), name, gp, ac)
	if err != nil {
		writeServerError(w, http.StatusInternalServerError, "get_cluster_comparison", err)
		return
	}
	if resp == nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}
