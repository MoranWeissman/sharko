package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/observations"
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
		// Upstream call (Git provider + ArgoCD): classify so a Git timeout
		// reads as 504 and a refused TCP connection reads as 502.
		writeUpstreamError(w, "list_clusters", err)
		return
	}

	// Resolve pending-registration PRs and prune any pending-cluster
	// names from the `not_in_git` cluster set. Without this prune, the
	// FE would render the same cluster in both the "Pending
	// registrations" surface AND the "Discovered clusters / not_in_git"
	// surface (the argosecrets reconciler can create the ArgoCD cluster
	// secret BEFORE the values-file PR merges).
	pending := resolvePendingRegistrations(r.Context(), gp, s.gitopsCfg.CommitPrefix)
	resp.PendingRegistrations = pending
	pendingNames := make(map[string]struct{}, len(pending))
	for _, p := range pending {
		pendingNames[p.ClusterName] = struct{}{}
	}

	// Resolve orphan ArgoCD cluster Secrets (in ArgoCD, not in git, no
	// open register PR). Dignified-degrade: a transient ArgoCD blip
	// returns an empty slice + warn, never a 500. Pass the git-managed
	// cluster names (resp.Clusters with Managed==true) plus the
	// pending names to the resolver.
	gitManaged := make([]models.Cluster, 0, len(resp.Clusters))
	for _, c := range resp.Clusters {
		if c.Managed {
			gitManaged = append(gitManaged, c)
		}
	}
	// Ownership-label gate. Pull the set of Secret names in the argocd
	// namespace carrying app.kubernetes.io/managed-by=sharko so the
	// resolver only surfaces Sharko-owned orphans (unlabeled = Adopt
	// territory). Returns nil when the k8s client is not wired — the
	// resolver disables the gate in that case so dev-mode without K8s
	// does not silently lose the orphan surface.
	var sharkoOwnedNames map[string]struct{}
	if k8sClient, namespace, ok := s.k8sClientAndNamespace(); ok {
		sharkoOwnedNames = listSharkoOwnedSecretNames(r.Context(), k8sClient, namespace)
	}
	orphans := resolveOrphanRegistrations(r.Context(), ac, gitManaged, pendingNames, sharkoOwnedNames)
	resp.OrphanRegistrations = orphans
	orphanNames := make(map[string]struct{}, len(orphans))
	for _, o := range orphans {
		orphanNames[o.ClusterName] = struct{}{}
	}

	// Filter the `not_in_git` lane to remove BOTH pending and orphan
	// cluster names. A pending cluster belongs in PendingRegistrations
	// only; an orphan belongs in OrphanRegistrations only. Without this
	// prune the same cluster could appear in two surfaces at once.
	if len(pendingNames) > 0 || len(orphanNames) > 0 {
		filtered := resp.Clusters[:0]
		for _, c := range resp.Clusters {
			// Only prune clusters that are in the "not_in_git" lane. A
			// managed cluster that happens to share a name with an open
			// register-PR (idempotent retry case) is legitimately on the
			// managed list and must not disappear.
			if !c.Managed && c.ConnectionStatus == "not_in_git" {
				if _, hit := pendingNames[c.Name]; hit {
					continue
				}
				if _, hit := orphanNames[c.Name]; hit {
					continue
				}
			}
			filtered = append(filtered, c)
		}
		resp.Clusters = filtered
	}

	// Enrich clusters with connectivity status + Sharko obs fields.
	// Fetch the full application list once (no N+1 per cluster).
	// Best-effort: connectivity fields simply stay absent on failure.
	allApps, appsErr := ac.ListApplications(r.Context())
	if appsErr != nil {
		allApps = nil // degrade gracefully
	}

	// Resolve obs map once for the whole list (nil obsStore → empty map).
	var obsMap map[string]*observations.Observation
	if s.obsStore != nil {
		obsMap, _ = s.obsStore.ListObservations(r.Context())
	}

	for i := range resp.Clusters {
		c := &resp.Clusters[i]
		verdict := computeConnectivityVerdict(c.Name, c.ConnectionStatus, allApps)
		c.ConnectivityStatus = verdict.Status
		c.ConnectivityDetail = verdict.Detail
		if obsMap != nil {
			applyObsFields(c, obsMap[c.Name])
		}
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
		// Upstream call (Git provider + ArgoCD): classify.
		writeUpstreamError(w, "get_cluster", err)
		return
	}
	if resp == nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	// Enrich the single cluster with connectivity status + Sharko obs fields.
	// Re-use the per-cluster app list that GetClusterDetail already fetched
	// internally; fetch it again here to avoid threading it through the service
	// return value (the service layer is deliberately obs-free).
	// Best-effort: fields stay absent on failure.
	detailApps, detailAppsErr := ac.ListApplications(r.Context())
	if detailAppsErr == nil {
		verdict := computeConnectivityVerdict(resp.Cluster.Name, resp.Cluster.ConnectionStatus, detailApps)
		resp.Cluster.ConnectivityStatus = verdict.Status
		resp.Cluster.ConnectivityDetail = verdict.Detail
	}
	if s.obsStore != nil {
		obsMap, _ := s.obsStore.ListObservations(r.Context())
		if obsMap != nil {
			applyObsFields(&resp.Cluster, obsMap[name])
		}
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
		// Upstream call (Git provider): classify.
		writeUpstreamError(w, "get_cluster_values", err)
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
		// Upstream call (Git provider): classify.
		writeUpstreamError(w, "get_cluster_config_diff", err)
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
		// Upstream call (Git provider + ArgoCD): classify.
		writeUpstreamError(w, "get_cluster_comparison", err)
		return
	}
	if resp == nil {
		writeError(w, http.StatusNotFound, "cluster not found")
		return
	}

	// Enrich the cluster with connectivity status + Sharko obs fields.
	// GetClusterComparison already called ListApplications internally to
	// build the comparison; we call it again here rather than thread the
	// result through the service layer to avoid coupling service ↔ obs.
	// Best-effort: fields stay absent on failure.
	compApps, compAppsErr := ac.ListApplications(r.Context())
	if compAppsErr == nil {
		verdict := computeConnectivityVerdict(resp.Cluster.Name, resp.Cluster.ConnectionStatus, compApps)
		resp.Cluster.ConnectivityStatus = verdict.Status
		resp.Cluster.ConnectivityDetail = verdict.Detail
	}
	if s.obsStore != nil {
		obsMap, _ := s.obsStore.ListObservations(r.Context())
		if obsMap != nil {
			applyObsFields(&resp.Cluster, obsMap[name])
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
