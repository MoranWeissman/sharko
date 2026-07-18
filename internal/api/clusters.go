package api

import (
	"net/http"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	pending := resolvePendingRegistrations(r.Context(), gp, s.gitopsConfig().CommitPrefix)

	// LW-18 (Part 2): Filter out any pending-registration PRs for clusters
	// that are already managed (in git). This handles the idempotent-retry
	// case where someone re-runs `register` on an already-registered cluster,
	// opening a redundant PR. Without this filter, the cluster would appear
	// BOTH in the managed list AND in PendingRegistrations, inflating the
	// "All Clusters" total and confusing the UI. The managed cluster is the
	// source of truth — the open PR is noise.
	managedNames := make(map[string]struct{}, len(resp.Clusters))
	for _, c := range resp.Clusters {
		if c.Managed {
			managedNames[c.Name] = struct{}{}
		}
	}
	filteredPending := pending[:0]
	for _, p := range pending {
		if _, alreadyManaged := managedNames[p.ClusterName]; !alreadyManaged {
			filteredPending = append(filteredPending, p)
		}
	}
	resp.PendingRegistrations = filteredPending
	pendingNames := make(map[string]struct{}, len(filteredPending))
	for _, p := range filteredPending {
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
	// cluster names, and correct HealthStats.NotInGit by the same amount
	// (see prunePendingAndOrphanFromNotInGit for the full contract).
	prunePendingAndOrphanFromNotInGit(resp, pendingNames, orphanNames)

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

	backendConfigured := s.credProvider() != nil
	for i := range resp.Clusters {
		c := &resp.Clusters[i]
		verdict := computeConnectivityVerdict(c.Name, c.ConnectionStatus, allApps)
		c.ConnectivityStatus = verdict.Status
		c.ConnectivityDetail = verdict.Detail
		// W4a (V3 RW1.8): For clusters stuck at "check_pending", detect
		// connectivity-check ApplicationSet label-selector drift and augment
		// ConnectivityDetail with a plain reason + next step. Best-effort: if
		// the k8s client is unavailable or the Secret fetch fails, degrade
		// gracefully by leaving the baseline ConnectivityDetail as-is.
		if verdict.Status == "check_pending" && allApps != nil {
			if k8sClient, namespace, ok := s.k8sClientAndNamespace(); ok {
				secret, err := k8sClient.CoreV1().Secrets(namespace).Get(r.Context(), c.Name, metav1.GetOptions{})
				if err == nil && secret != nil {
					if driftReason := detectConnectivityCheckDrift(c.Name, secret.Labels, allApps); driftReason != "" {
						c.ConnectivityDetail = driftReason
					}
				}
			}
		}
		// V2-cleanup-85.4: auto-derived health, independent of any manual
		// "Test connection" click — see computeDerivedHealth.
		hasHealthyAddon := clusterHasHealthyAddon(c.Name, c.ServerURL, allApps)
		c.DerivedHealthStatus = computeDerivedHealth(hasHealthyAddon, verdict, c.ConnectionStatus)
		// V2-cleanup-88.1: pure string/shape derivation, no extra call —
		// always computed, unlike the ArgoCD-app-dependent fields above.
		c.TargetPlatform = computeTargetPlatform(c.ServerURL, c.CredsSource)
		// V2-cleanup-88.3: cheap presence-of-config readiness signal for
		// "can a secret-bearing addon be enabled here" — see
		// models.Cluster.CredentialsResolvable.
		c.AddonSecretsReady = c.CredentialsResolvable(backendConfigured)
		// V2-cleanup-89.4: per-cluster reconcile visibility — no ArgoCD call,
		// just an in-memory lookup on the reconciler.
		applyLastReconcile(c, s.clusterRecon)
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

// prunePendingAndOrphanFromNotInGit drops clusters that are in the
// "not_in_git" lane but actually belong to the Pending or Orphan
// registration surfaces, and corrects resp.HealthStats.NotInGit by the
// exact number pruned.
//
// A pending cluster belongs in PendingRegistrations only; an orphan belongs
// in OrphanRegistrations only. Without this prune the same cluster could
// appear in two surfaces at once (the argosecrets reconciler can create the
// ArgoCD cluster Secret BEFORE the values-file PR merges).
//
// LW-10: resp.HealthStats.NotInGit was computed by the service layer
// (computeHealthStats) BEFORE this prune, so it counts every cluster that
// was in the "not_in_git" lane — including the pending/orphan ones we drop
// here. We decrement it by exactly the number pruned so the stat card
// ("Available to manage") and any derived total match the pruned cluster
// list. Fixing NotInGit at the source also fixes the FE's "All Clusters"
// total, which is total_in_git + not_in_git. Guards against a nil
// HealthStats and never underflows below zero. Only clusters genuinely in
// the not_in_git lane (!Managed && ConnectionStatus == "not_in_git") are
// counted, so a managed cluster that shares a name with an open register PR
// (idempotent-retry case) is neither dropped nor decremented.
func prunePendingAndOrphanFromNotInGit(resp *models.ClustersResponse, pendingNames, orphanNames map[string]struct{}) {
	if resp == nil || (len(pendingNames) == 0 && len(orphanNames) == 0) {
		return
	}
	prunedNotInGit := 0
	filtered := resp.Clusters[:0]
	for _, c := range resp.Clusters {
		if !c.Managed && c.ConnectionStatus == "not_in_git" {
			if _, hit := pendingNames[c.Name]; hit {
				prunedNotInGit++
				continue
			}
			if _, hit := orphanNames[c.Name]; hit {
				prunedNotInGit++
				continue
			}
		}
		filtered = append(filtered, c)
	}
	resp.Clusters = filtered

	if resp.HealthStats != nil && prunedNotInGit > 0 {
		resp.HealthStats.NotInGit -= prunedNotInGit
		if resp.HealthStats.NotInGit < 0 {
			resp.HealthStats.NotInGit = 0
		}
	}
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
		// V2-cleanup-85.4: auto-derived health, independent of any manual
		// "Test connection" click — see computeDerivedHealth.
		hasHealthyAddon := clusterHasHealthyAddon(resp.Cluster.Name, resp.Cluster.ServerURL, detailApps)
		resp.Cluster.DerivedHealthStatus = computeDerivedHealth(hasHealthyAddon, verdict, resp.Cluster.ConnectionStatus)
	}
	// V2-cleanup-88.1: pure string/shape derivation, no ArgoCD call needed
	// — set unconditionally, unlike the ArgoCD-app-dependent fields above.
	resp.Cluster.TargetPlatform = computeTargetPlatform(resp.Cluster.ServerURL, resp.Cluster.CredsSource)
	// V2-cleanup-88.3: cheap presence-of-config readiness signal — does not
	// depend on the ArgoCD application list, so it is computed unconditionally.
	resp.Cluster.AddonSecretsReady = resp.Cluster.CredentialsResolvable(s.credProvider() != nil)
	// V2-cleanup-89.4: per-cluster reconcile visibility — no ArgoCD call,
	// just an in-memory lookup on the reconciler.
	applyLastReconcile(&resp.Cluster, s.clusterRecon)
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
		// V2-cleanup-85.4: auto-derived health, independent of any manual
		// "Test connection" click — see computeDerivedHealth.
		hasHealthyAddon := clusterHasHealthyAddon(resp.Cluster.Name, resp.Cluster.ServerURL, compApps)
		resp.Cluster.DerivedHealthStatus = computeDerivedHealth(hasHealthyAddon, verdict, resp.Cluster.ConnectionStatus)
	}
	// V2-cleanup-88.1: pure string/shape derivation, no ArgoCD call needed
	// — set unconditionally, unlike the ArgoCD-app-dependent fields above.
	resp.Cluster.TargetPlatform = computeTargetPlatform(resp.Cluster.ServerURL, resp.Cluster.CredsSource)
	// V2-cleanup-88.3: cheap presence-of-config readiness signal — does not
	// depend on the ArgoCD application list, so it is computed unconditionally.
	resp.Cluster.AddonSecretsReady = resp.Cluster.CredentialsResolvable(s.credProvider() != nil)
	// V2-cleanup-89.4: per-cluster reconcile visibility — no ArgoCD call,
	// just an in-memory lookup on the reconciler.
	applyLastReconcile(&resp.Cluster, s.clusterRecon)
	if s.obsStore != nil {
		obsMap, _ := s.obsStore.ListObservations(r.Context())
		if obsMap != nil {
			applyObsFields(&resp.Cluster, obsMap[name])
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
