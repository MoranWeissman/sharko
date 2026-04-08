package api

import (
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// discoverClusterEntry is a single cluster in the discover response.
type discoverClusterEntry struct {
	Name       string `json:"name"`
	Region     string `json:"region"`
	Registered bool   `json:"registered"`
}

// handleDiscoverClusters godoc
//
// @Summary Discover available clusters
// @Description Lists clusters from the credentials provider and marks which are registered in ArgoCD
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Available clusters"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 501 {object} map[string]interface{} "Provider not configured"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /clusters/available [get]
// handleDiscoverClusters handles GET /api/v1/clusters/available — list provider clusters
// and mark which are already registered in ArgoCD.
func (s *Server) handleDiscoverClusters(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.credProvider == nil {
		writeError(w, http.StatusNotImplemented, "secrets provider not configured")
		return
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		writeError(w, http.StatusBadGateway, "no active ArgoCD connection: "+err.Error())
		return
	}

	// Get all clusters from the credentials provider.
	providerClusters, err := s.credProvider.ListClusters()
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list provider clusters: "+err.Error())
		return
	}

	// Get all clusters registered in ArgoCD.
	argoClusters, err := ac.ListClusters(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to list ArgoCD clusters: "+err.Error())
		return
	}

	// Build a set of registered cluster names.
	registered := make(map[string]bool, len(argoClusters))
	for _, c := range argoClusters {
		registered[c.Name] = true
	}

	// Cross-reference provider clusters with ArgoCD.
	entries := make([]discoverClusterEntry, 0, len(providerClusters))
	for _, pc := range providerClusters {
		entries = append(entries, discoverClusterEntry{
			Name:       pc.Name,
			Region:     pc.Region,
			Registered: registered[pc.Name],
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clusters": entries,
	})
}

// handleTestCluster godoc
//
// @Summary Test cluster connectivity
// @Description Attempts to connect to a cluster using credentials from the provider and returns the server version
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Param name path string true "Cluster name"
// @Success 200 {object} map[string]interface{} "Connectivity result (reachable true/false)"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 503 {object} map[string]interface{} "No credentials provider configured"
// @Router /clusters/{name}/test [post]
// handleTestCluster handles POST /api/v1/clusters/{name}/test — test connectivity to a cluster.
func (s *Server) handleTestCluster(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}

	name := r.PathValue("name")
	slog.Info("[cluster-test] testing cluster", "name", name)

	if s.credProvider == nil {
		writeError(w, http.StatusServiceUnavailable, "no credentials provider configured")
		return
	}

	slog.Info("[cluster-test] fetching credentials", "name", name)
	creds, err := s.credProvider.GetCredentials(name)
	if err != nil {
		slog.Error("[cluster-test] failed", "name", name, "step", "fetch-credentials", "error", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"name":      name,
			"reachable": false,
			"error":     err.Error(),
		})
		return
	}
	slog.Info("[cluster-test] credentials obtained", "name", name, "server", creds.Server)

	slog.Info("[cluster-test] building k8s client", "name", name)
	client, err := remoteclient.NewClientFromKubeconfig(creds.Raw)
	if err != nil {
		slog.Error("[cluster-test] failed", "name", name, "step", "build-client", "error", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"name":      name,
			"reachable": false,
			"error":     "failed to build client: " + err.Error(),
		})
		return
	}

	slog.Info("[cluster-test] calling ServerVersion", "name", name)
	version, err := client.Discovery().ServerVersion()
	if err != nil {
		slog.Error("[cluster-test] failed", "name", name, "step", "server-version", "error", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"name":      name,
			"reachable": false,
			"error":     err.Error(),
		})
		return
	}

	slog.Info("[cluster-test] cluster reachable", "name", name, "version", version.GitVersion)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":           name,
		"reachable":      true,
		"server_version": version.GitVersion,
		"platform":       version.Platform,
	})
}
