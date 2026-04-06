package api

import (
	"fmt"
	"net/http"
	"time"
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
	ServerVersion        string                `json:"server_version"`
	Uptime               string                `json:"uptime"`
	GitUnavailable       bool                  `json:"git_unavailable,omitempty"`
	ArgoUnavailable      bool                  `json:"argo_unavailable,omitempty"`
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

// formatUptime returns a human-readable uptime string.
func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// handleGetFleetStatus godoc
//
// @Summary Get fleet status
// @Description Returns aggregated health and addon deployment status across all clusters
// @Tags clusters
// @Produce json
// @Security BearerAuth
// @Success 200 {object} fleetStatusResponse "Fleet status"
// @Failure 502 {object} map[string]interface{} "Gateway error"
// @Router /fleet/status [get]
// handleGetFleetStatus handles GET /api/v1/fleet/status — read-only cluster status aggregation.
// It is resilient: Git and ArgoCD unavailability are reported as flags, not errors.
func (s *Server) handleGetFleetStatus(w http.ResponseWriter, r *http.Request) {
	resp := fleetStatusResponse{
		ServerVersion: appVersion,
		Uptime:        formatUptime(time.Since(s.startTime)),
		Clusters:      make([]fleetClusterSummary, 0),
	}

	gp, err := s.connSvc.GetActiveGitProvider()
	if err != nil {
		resp.GitUnavailable = true
	}

	ac, err := s.connSvc.GetActiveArgocdClient()
	if err != nil {
		resp.ArgoUnavailable = true
	}

	// Only fetch cluster/addon data when both providers are available.
	if !resp.GitUnavailable && !resp.ArgoUnavailable {
		clustersResp, err := s.clusterSvc.ListClusters(r.Context(), gp, ac)
		if err != nil {
			resp.ArgoUnavailable = true
		} else {
			resp.TotalClusters = len(clustersResp.Clusters)
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
			} else {
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
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}
