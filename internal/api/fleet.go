package api

import (
	"net/http"
)

// fleetClusterSummary holds per-cluster health info for the cluster status overview response.
type fleetClusterSummary struct {
	Name             string `json:"name"`
	ConnectionStatus string `json:"connection_status"`
	TotalAddons      int    `json:"total_addons"`
	HealthyAddons    int    `json:"healthy_addons"`
	DegradedAddons   int    `json:"degraded_addons"`
}

// fleetStatusResponse is the response for GET /api/v1/fleet/status (cluster status overview).
type fleetStatusResponse struct {
	TotalClusters        int                   `json:"total_clusters"`
	HealthyClusters      int                   `json:"healthy_clusters"`
	DegradedClusters     int                   `json:"degraded_clusters"`
	DisconnectedClusters int                   `json:"disconnected_clusters"`
	TotalAddons          int                   `json:"total_addons"`
	TotalDeployments     int                   `json:"total_deployments"`
	HealthyDeployments   int                   `json:"healthy_deployments"`
	DegradedDeployments  int                   `json:"degraded_deployments"`
	OutOfSyncDeployments int                   `json:"out_of_sync_deployments"`
	AddonDataUnavailable bool                  `json:"addon_data_unavailable,omitempty"`
	Clusters             []fleetClusterSummary `json:"clusters"`
}

// handleGetFleetStatus handles GET /api/v1/fleet/status — read-only cluster status aggregation.
func (s *Server) handleGetFleetStatus(w http.ResponseWriter, r *http.Request) {
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

	clustersResp, err := s.clusterSvc.ListClusters(r.Context(), gp, ac)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to fetch clusters: "+err.Error())
		return
	}

	resp := fleetStatusResponse{
		TotalClusters: len(clustersResp.Clusters),
		Clusters:      make([]fleetClusterSummary, 0, len(clustersResp.Clusters)),
	}

	for _, c := range clustersResp.Clusters {
		switch c.ConnectionStatus {
		case "Successful":
			resp.HealthyClusters++
		case "Failed":
			resp.DegradedClusters++
		default:
			resp.DisconnectedClusters++
		}
		resp.Clusters = append(resp.Clusters, fleetClusterSummary{
			Name:             c.Name,
			ConnectionStatus: c.ConnectionStatus,
		})
	}

	catalog, err := s.addonSvc.GetCatalog(r.Context(), gp, ac)
	if err != nil {
		resp.AddonDataUnavailable = true
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp.TotalAddons = catalog.TotalAddons

	// Build per-cluster addon stats from catalog data.
	clusterAddons := make(map[string]*fleetClusterSummary)
	for i := range resp.Clusters {
		clusterAddons[resp.Clusters[i].Name] = &resp.Clusters[i]
	}

	for _, addon := range catalog.Addons {
		for _, app := range addon.Applications {
			if !app.Enabled {
				continue
			}
			resp.TotalDeployments++

			switch app.HealthStatus {
			case "Healthy":
				resp.HealthyDeployments++
			case "Degraded", "Unknown":
				resp.DegradedDeployments++
			}

			if app.SyncStatus == "OutOfSync" {
				resp.OutOfSyncDeployments++
			}

			if cs, ok := clusterAddons[app.ClusterName]; ok {
				cs.TotalAddons++
				switch app.HealthStatus {
				case "Healthy":
					cs.HealthyAddons++
				case "Degraded", "Unknown":
					cs.DegradedAddons++
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
