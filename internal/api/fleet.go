package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/readcache"
)

// fleetStatusCacheKey is the readcache key for the fleet status
// aggregation (perf M1).
const fleetStatusCacheKey = "fleet:status"

// fleetClusterSummary holds per-cluster health info for the cluster status overview response.
type fleetClusterSummary struct {
	Name             string `json:"name"`
	ConnectionStatus string `json:"connection_status"`
	// ConnectionManagedBy mirrors the cluster record's connectionManagedBy
	// (V2-cleanup-60.4 / review M6): "" or "sharko" — Sharko owns the ArgoCD
	// cluster Secret; "user" — self-managed connection (Sharko only syncs
	// addon labels). Sourced from the same cluster record the main
	// /clusters endpoint exposes, so fleet/dashboard consumers can render
	// the "connection managed by: me" mode without a second call.
	ConnectionManagedBy string `json:"connection_managed_by,omitempty"`
	TotalAddons         int    `json:"total_addons"`
	HealthyAddons       int    `json:"healthy_addons"`
	DegradedAddons      int    `json:"degraded_addons"`
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
//
// Cached for readcache.DefaultTTL (perf M1) — see internal/readcache's
// package doc for the invalidation contract. Fleet-wide, not per-user:
// every authenticated caller sees the same aggregation, so a single cache
// entry (not keyed by role/user) is safe.
func (s *Server) handleGetFleetStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := readcache.GetOrCompute(s.readCache, fleetStatusCacheKey, func() (*fleetStatusResponse, error) {
		return s.computeFleetStatus(r.Context())
	})
	if err != nil {
		// computeFleetStatus never actually returns an error today (Git/
		// ArgoCD unavailability is reported via response flags instead) —
		// this guards the contract if that ever changes rather than
		// writing a nil response.
		writeUpstreamError(w, "fleet_status", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// computeFleetStatus is handleGetFleetStatus's actual computation,
// unchanged in output from before perf M1/M2. Never returns an error —
// Git/ArgoCD unavailability is reported via the response's *Unavailable
// flags, matching the endpoint's original resilient-by-design contract —
// so the cache never sees a failed compute to (correctly) skip caching.
//
// perf M2: the cluster-list pipeline and the catalog pipeline used to run
// one after another even though both only depend on gp/ac, not on each
// other. They now run concurrently via sync.WaitGroup (not errgroup —
// errgroup.WithContext cancels sibling goroutines on the first error,
// which would discard the catalog fetch's result even though the ORIGINAL
// behavior here only ever depended on the clusters fetch succeeding, never
// on canceling the catalog fetch). The exact original conditional
// wiring — catalog data is only used when the clusters fetch also
// succeeded — is preserved below so a clusters-fetch failure still yields
// byte-identical output (AddonDataUnavailable stays false, TotalAddons
// stays 0) even though the catalog fetch itself still ran concurrently.
func (s *Server) computeFleetStatus(ctx context.Context) (*fleetStatusResponse, error) {
	v := s.version
	if v == "" {
		v = "dev"
	}
	resp := fleetStatusResponse{
		ServerVersion: v,
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
		var (
			clustersResp *models.ClustersResponse
			clustersErr  error
			catalog      *models.AddonCatalogResponse
			catalogErr   error
		)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			clustersResp, clustersErr = s.clusterSvc.ListClusters(ctx, gp, ac)
		}()
		go func() {
			defer wg.Done()
			catalog, catalogErr = s.addonSvc.GetCatalog(ctx, gp, ac)
		}()
		wg.Wait()

		if clustersErr != nil {
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
					Name:                c.Name,
					ConnectionStatus:    c.ConnectionStatus,
					ConnectionManagedBy: c.ConnectionManagedBy,
				})
			}

			if catalogErr != nil {
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

	return &resp, nil
}
