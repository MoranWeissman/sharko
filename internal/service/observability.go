package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/moran/argocd-addons-platform/internal/argocd"
	"github.com/moran/argocd-addons-platform/internal/config"
	"github.com/moran/argocd-addons-platform/internal/gitprovider"
	"github.com/moran/argocd-addons-platform/internal/models"
)

// ObservabilityService provides aggregated observability data from ArgoCD.
type ObservabilityService struct{}

// NewObservabilityService creates a new ObservabilityService.
func NewObservabilityService() *ObservabilityService {
	return &ObservabilityService{}
}

// GetOverview returns the full observability dashboard data.
func (s *ObservabilityService) GetOverview(ctx context.Context, ac *argocd.Client, gp gitprovider.GitProvider) (*models.ObservabilityOverviewResponse, error) {
	// 1. Get ArgoCD version info
	versionInfo, err := ac.GetVersion(ctx)
	if err != nil {
		log.Printf("Warning: could not fetch ArgoCD version: %v", err)
		versionInfo = map[string]string{}
	}

	// 2. List clusters
	clusters, err := ac.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing clusters: %w", err)
	}

	clusterNames := make(map[string]bool)
	connectedClusters := 0
	for _, c := range clusters {
		clusterNames[c.Name] = true
		if c.ConnectionState == "Successful" {
			connectedClusters++
		}
	}

	// 3. List all apps (summary)
	appsSummary, err := ac.ListApplicationsSummary(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing applications: %w", err)
	}

	// 4. Filter to addon apps only (exclude bootstrap/infrastructure)
	var addonApps []models.ArgocdApplication
	for _, app := range appsSummary {
		if isInfrastructureApp(app.Name) {
			continue
		}
		addonApps = append(addonApps, app)
	}

	healthSummary := make(map[string]int)
	for _, app := range addonApps {
		h := app.HealthStatus
		if h == "" {
			h = "Unknown"
		}
		healthSummary[h]++
	}

	// 5. Fetch full detail for each addon app to get history and resources
	var fullApps []models.ArgocdApplication
	for _, app := range addonApps {
		detail, err := ac.GetApplication(ctx, app.Name)
		if err != nil {
			log.Printf("Warning: could not fetch detail for app %s: %v", app.Name, err)
			fullApps = append(fullApps, app) // fall back to summary
			continue
		}
		fullApps = append(fullApps, *detail)
	}

	// 5. Build sync activity timeline from history entries (exclude infra apps)
	var allSyncs []models.SyncActivityEntry
	for _, app := range fullApps {
		if isInfrastructureApp(app.Name) {
			continue
		}
		addonName, clusterName := extractAddonCluster(app.Name, clusterNames)
		for _, h := range app.History {
			entry := models.SyncActivityEntry{
				Timestamp:   h.DeployedAt,
				AppName:     app.Name,
				AddonName:   addonName,
				ClusterName: clusterName,
				Revision:    h.Revision,
				Status:      "Succeeded", // history entries are completed deploys
			}

			// Calculate duration if we have both start and end times
			if h.DeployStartedAt != "" && h.DeployedAt != "" {
				dur := parseDuration(h.DeployStartedAt, h.DeployedAt)
				entry.DurationSecs = dur.Seconds()
				entry.Duration = formatDuration(dur)
			}

			allSyncs = append(allSyncs, entry)
		}
	}

	// Sort by timestamp descending (most recent first)
	sort.Slice(allSyncs, func(i, j int) bool {
		return allSyncs[i].Timestamp > allSyncs[j].Timestamp
	})

	// Take last 20 for recent syncs
	recentSyncs := allSyncs
	if len(recentSyncs) > 20 {
		recentSyncs = recentSyncs[:20]
	}

	// 6. Group by addon and build per-addon health detail
	addonMap := make(map[string]*models.AddonHealthDetail)
	for _, app := range fullApps {
		addonName, clusterName := extractAddonCluster(app.Name, clusterNames)

		detail, ok := addonMap[addonName]
		if !ok {
			detail = &models.AddonHealthDetail{
				AddonName: addonName,
			}
			addonMap[addonName] = detail
		}

		detail.TotalClusters++
		if app.HealthStatus == "Healthy" {
			detail.HealthyClusters++
		} else if app.HealthStatus == "Degraded" {
			detail.DegradedClusters++
		}

		// Per-cluster health
		ch := models.AddonClusterHealth{
			ClusterName:   clusterName,
			Health:        app.HealthStatus,
			HealthSince:   app.HealthLastTransition,
			ReconciledAt:  app.ReconciledAt,
			ResourceCount: len(app.Resources),
			Resources:     app.Resources,
		}

		for _, r := range app.Resources {
			if r.Health == "Healthy" {
				ch.HealthyResources++
			}
		}

		// Last deploy from history
		if len(app.History) > 0 {
			last := app.History[len(app.History)-1]
			ch.LastDeployTime = last.DeployedAt
			if last.DeployStartedAt != "" && last.DeployedAt != "" {
				dur := parseDuration(last.DeployStartedAt, last.DeployedAt)
				ch.LastSyncDuration = formatDuration(dur)
			}

			// Track latest deploy time for addon
			if detail.LastDeployTime == "" || last.DeployedAt > detail.LastDeployTime {
				detail.LastDeployTime = last.DeployedAt
			}
		}

		detail.Clusters = append(detail.Clusters, ch)
	}

	// Calculate avg sync duration per addon
	for addonName, detail := range addonMap {
		var totalSecs float64
		var count int
		for _, entry := range allSyncs {
			if entry.AddonName == addonName && entry.DurationSecs > 0 {
				totalSecs += entry.DurationSecs
				count++
			}
		}
		if count > 0 {
			detail.AvgSyncSecs = totalSecs / float64(count)
			detail.AvgSyncDuration = formatDuration(time.Duration(detail.AvgSyncSecs * float64(time.Second)))
		}
	}

	// Convert map to sorted slice
	addonHealth := make([]models.AddonHealthDetail, 0, len(addonMap))
	for _, detail := range addonMap {
		addonHealth = append(addonHealth, *detail)
	}
	sort.Slice(addonHealth, func(i, j int) bool {
		return addonHealth[i].AddonName < addonHealth[j].AddonName
	})

	// 7. Build addon groups (grouped by addon name with health counts and child apps)
	addonGroups := s.buildAddonGroups(fullApps, clusterNames)

	// 8. Check resource alerts via Git values
	resourceAlerts := s.checkResourceAlerts(ctx, gp, addonGroups)

	controlPlane := models.ControlPlaneInfo{
		ArgocdVersion:     versionInfo["Version"],
		HelmVersion:       versionInfo["HelmVersion"],
		KubectlVersion:    versionInfo["KubectlVersion"],
		TotalApps:         len(addonApps),
		TotalClusters:     len(clusters),
		ConnectedClusters: connectedClusters,
		HealthSummary:     healthSummary,
	}

	return &models.ObservabilityOverviewResponse{
		ControlPlane:   controlPlane,
		RecentSyncs:    recentSyncs,
		AddonHealth:    addonHealth,
		AddonGroups:    addonGroups,
		ResourceAlerts: resourceAlerts,
	}, nil
}

// buildAddonGroups groups apps by addon name, builds health counts and child app details.
func (s *ObservabilityService) buildAddonGroups(apps []models.ArgocdApplication, clusterNames map[string]bool) []models.AddonGroupHealth {
	groupMap := make(map[string]*models.AddonGroupHealth)

	for _, app := range apps {
		// Skip infrastructure apps (not actual addons)
		if isInfrastructureApp(app.Name) {
			continue
		}

		addonName, clusterName := extractAddonCluster(app.Name, clusterNames)

		// Skip apps where we couldn't extract a valid cluster name
		// (these are typically bootstrap/infra apps like "external-secrets-operator")
		if clusterName == "" || !clusterNames[clusterName] {
			// Check if this looks like an addon by seeing if addonName is a known addon
			// If clusterName doesn't match any known cluster, skip it
			if clusterName != "" && clusterName != addonName && !clusterNames[clusterName] {
				continue
			}
		}

		group, ok := groupMap[addonName]
		if !ok {
			group = &models.AddonGroupHealth{
				AddonName:    addonName,
				HealthCounts: make(map[string]int),
			}
			groupMap[addonName] = group
		}

		group.TotalApps++

		health := app.HealthStatus
		if health == "" {
			health = "Unknown"
		}
		group.HealthCounts[health]++

		// Build resource summary from the app's resource tree
		rs := models.ResourceSummary{}
		for _, r := range app.Resources {
			switch r.Kind {
			case "Pod":
				rs.TotalPods++
				if r.Health == "Healthy" {
					rs.RunningPods++
				}
			case "Deployment", "DaemonSet", "StatefulSet":
				rs.TotalContainers++
			}
		}

		child := models.ChildAppHealth{
			AppName:         app.Name,
			ClusterName:     clusterName,
			Health:          health,
			SyncStatus:      app.SyncStatus,
			ReconciledAt:    app.ReconciledAt,
			ResourceSummary: rs,
		}

		group.ChildApps = append(group.ChildApps, child)
	}

	// Convert to sorted slice (most unhealthy first)
	groups := make([]models.AddonGroupHealth, 0, len(groupMap))
	for _, g := range groupMap {
		// Sort child apps by health: degraded first, then progressing, then unknown, then healthy
		sort.Slice(g.ChildApps, func(i, j int) bool {
			return healthPriority(g.ChildApps[i].Health) < healthPriority(g.ChildApps[j].Health)
		})
		groups = append(groups, *g)
	}

	sort.Slice(groups, func(i, j int) bool {
		// Sort by most unhealthy first (count of non-healthy apps descending)
		unhealthyI := groups[i].TotalApps - groups[i].HealthCounts["Healthy"]
		unhealthyJ := groups[j].TotalApps - groups[j].HealthCounts["Healthy"]
		if unhealthyI != unhealthyJ {
			return unhealthyI > unhealthyJ
		}
		return groups[i].AddonName < groups[j].AddonName
	})

	return groups
}

// healthPriority returns a sort priority for health status (lower = worse = sorts first).
func healthPriority(health string) int {
	switch strings.ToLower(health) {
	case "degraded":
		return 0
	case "progressing":
		return 1
	case "missing":
		return 2
	case "unknown", "":
		return 3
	case "healthy":
		return 4
	default:
		return 3
	}
}

// checkResourceAlerts checks Git values files for addons missing resource requests/limits.
// Only checks addons that are actually deployed (have running ArgoCD applications).
// Skips CRD-only charts (no values file = no workloads to configure).
func (s *ObservabilityService) checkResourceAlerts(ctx context.Context, gp gitprovider.GitProvider, addonGroups []models.AddonGroupHealth) []models.ResourceAlert {
	if gp == nil {
		return nil
	}

	// Build set of actually deployed addon names from the groups
	deployedAddons := make(map[string]bool)
	for _, group := range addonGroups {
		if group.TotalApps > 0 {
			deployedAddons[group.AddonName] = true
		}
	}

	// Only check addons from the catalog that are actually deployed
	parser := config.NewParser()
	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return nil
	}
	catalog, err := parser.ParseAddonsCatalog(catalogData)
	if err != nil {
		return nil
	}

	var alerts []models.ResourceAlert
	for _, addon := range catalog {
		// Skip addons that aren't deployed anywhere
		if !deployedAddons[addon.AppName] {
			continue
		}
		missing, detail := checkMissingResources(ctx, gp, addon.AppName)
		if missing {
			alerts = append(alerts, models.ResourceAlert{
				AddonName: addon.AppName,
				AlertType: "missing_limits",
				Details:   detail,
			})
		}
	}

	// Sort alerts alphabetically by addon name
	sort.Slice(alerts, func(i, j int) bool {
		return alerts[i].AddonName < alerts[j].AddonName
	})

	return alerts
}

// checkMissingResources checks if an addon's global values file contains resource configuration.
// Returns false (no alert) if the addon has no values file — likely a CRD-only chart with no workloads.
func checkMissingResources(ctx context.Context, gp gitprovider.GitProvider, addonName string) (bool, string) {
	path := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
	data, err := gp.GetFileContent(ctx, path, "main")
	if err != nil {
		// No values file = likely a CRD-only or config-only chart (no pods to configure)
		return false, ""
	}
	content := string(data)
	if !strings.Contains(content, "resources:") {
		// Has a values file but no resources section — might need attention
		return true, "No resource requests/limits configured in global values"
	}
	return false, ""
}

// extractAddonCluster extracts addon name and cluster name from an ArgoCD app name.
// App names follow the pattern {addon}-{cluster}. Cluster names from the known set are
// matched against the suffix; the remainder is the addon name.
func extractAddonCluster(appName string, clusterNames map[string]bool) (addon, cluster string) {
	// Try to match a known cluster name as suffix
	for name := range clusterNames {
		suffix := "-" + name
		if strings.HasSuffix(appName, suffix) {
			candidate := strings.TrimSuffix(appName, suffix)
			// Pick the longest cluster name match (most specific)
			if len(name) > len(cluster) {
				addon = candidate
				cluster = name
			}
		}
	}
	if cluster != "" {
		return addon, cluster
	}
	// Fallback: split on last hyphen
	if idx := strings.LastIndex(appName, "-"); idx > 0 {
		return appName[:idx], appName[idx+1:]
	}
	return appName, "unknown"
}

// parseDuration calculates the duration between two RFC3339 timestamps.
func parseDuration(start, end string) time.Duration {
	startTime, err1 := time.Parse(time.RFC3339, start)
	endTime, err2 := time.Parse(time.RFC3339, end)
	if err1 != nil || err2 != nil {
		return 0
	}
	d := endTime.Sub(startTime)
	if d < 0 {
		return 0
	}
	return d
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, mins)
}
