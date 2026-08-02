package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/readcache"
)

// observabilityOverviewCacheKey is the readcache key for GetOverview (perf M1).
const observabilityOverviewCacheKey = "observability:overview"

// ObservabilityService provides aggregated observability data from ArgoCD.
type ObservabilityService struct {
	clusterSvc *ClusterService

	// baseBranchFn is the per-instance test seam for the configured GitOps
	// base branch, mirroring AddonService.baseBranchFn (Wave 2 ride-along
	// w2-q6 item 1). nil (tests, e2e harness) falls back to "main" via the
	// branch() helper below.
	baseBranchFn func() string

	// cache is the per-instance TTL cache for GetOverview (perf M1). See
	// DashboardService.cache's doc for the shared-vs-private-per-test
	// discipline; SetCache mirrors DashboardService.SetCache exactly.
	cache *readcache.Cache
}

// SetCache replaces this service's read cache with a shared instance (see
// internal/readcache and DashboardService.SetCache).
func (s *ObservabilityService) SetCache(c *readcache.Cache) {
	s.cache = c
}

// NewObservabilityService creates a new ObservabilityService.
func NewObservabilityService(clusterSvc *ClusterService) *ObservabilityService {
	return &ObservabilityService{
		clusterSvc: clusterSvc,
		cache:      readcache.New(readcache.DefaultTTL),
	}
}

// SetBaseBranchFn wires in a live accessor for the configured GitOps base
// branch (e.g. api.Server.GitopsBaseBranch), matching
// AddonService.SetBaseBranchFn.
func (s *ObservabilityService) SetBaseBranchFn(fn func() string) {
	s.baseBranchFn = fn
}

// branch returns the configured GitOps base branch, defaulting to "main"
// when no seam is wired (tests, e2e harness) or it resolves to "".
func (s *ObservabilityService) branch() string {
	if s.baseBranchFn == nil {
		return "main"
	}
	if b := s.baseBranchFn(); b != "" {
		return b
	}
	return "main"
}

// GetOverview returns the full observability dashboard data. Cached for
// readcache.DefaultTTL (perf M1) — see internal/readcache's package doc
// for the invalidation contract. Fleet-wide, not per-user: every
// authenticated caller sees the same health/sync data, so a single cache
// entry (not keyed by role/user) is safe.
func (s *ObservabilityService) GetOverview(ctx context.Context, ac *argocd.Client, gp gitprovider.GitProvider) (*models.ObservabilityOverviewResponse, error) {
	return readcache.GetOrCompute(s.cache, observabilityOverviewCacheKey, func() (*models.ObservabilityOverviewResponse, error) {
		return s.getOverviewUncached(ctx, ac, gp)
	})
}

// getOverviewUncached is GetOverview's actual computation, unchanged in
// output from before perf M1/M2.
func (s *ObservabilityService) getOverviewUncached(ctx context.Context, ac *argocd.Client, gp gitprovider.GitProvider) (*models.ObservabilityOverviewResponse, error) {
	log := logging.LoggerFromContext(ctx)
	// 1. Get ArgoCD version info
	versionInfo, err := ac.GetVersion(ctx)
	if err != nil {
		log.Warn("could not fetch argocd version", "error", err)
		versionInfo = map[string]string{}
	}

	// 2. List clusters
	rawClusters, err := ac.ListClusters(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing clusters: %w", err)
	}
	// Filter out the host/management cluster (in-cluster) from
	// observability. Sharko itself runs there; it is the control
	// plane, not a workload target. Listing it as a "monitored
	// cluster" would mislead operators.
	clusters := filterInClusterEntries(rawClusters)

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

	// 5. addonApps already carries full detail (history + resources) — no
	// per-app fetch needed. ac.ListApplicationsSummary (called above) is a
	// plain alias for ac.ListApplications (internal/argocd/client.go): both
	// decode the ArgoCD response through the exact same argocdApplicationItem
	// struct that GetApplication uses, including the History and Resources
	// fields. The per-app ac.GetApplication follow-up that used to live here
	// was therefore a pure N+1 — one extra ArgoCD round trip per addon app
	// for data the list call had already returned (perf M2).
	fullApps := addonApps

	// 6. Build sync activity timeline from history entries (exclude infra apps)
	allSyncs := buildRecentSyncs(fullApps, clusterNames)

	// Sort by timestamp descending (most recent first)
	sort.Slice(allSyncs, func(i, j int) bool {
		return allSyncs[i].Timestamp > allSyncs[j].Timestamp
	})

	// Take last 20 for recent syncs
	recentSyncs := allSyncs
	if len(recentSyncs) > 20 {
		recentSyncs = recentSyncs[:20]
	}

	// 7. Group by addon and build per-addon health detail
	addonMap := make(map[string]*models.AddonHealthDetail)
	for _, app := range fullApps {
		addonName, clusterName := ExtractAddonCluster(app.Name, clusterNames)

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

	// 8. Build addon groups (grouped by addon name with health counts and child apps)
	addonGroups := s.buildAddonGroups(fullApps, clusterNames)

	// 9. Check resource alerts via Git values
	resourceAlerts := s.checkResourceAlerts(ctx, gp, addonGroups)

	// 10. Get Sharko-configured cluster count from Git (exclude in-cluster)
	var configuredClusters int
	var configuredClustersAvailable bool
	if gp != nil && s.clusterSvc != nil {
		gitClustersResp, err := s.clusterSvc.ListClusters(ctx, gp, ac)
		if err != nil {
			log.Warn("could not fetch git clusters for configured count", "error", err)
			configuredClustersAvailable = false
		} else {
			// Clusters list from ClusterService already excludes in-cluster
			configuredClusters = len(gitClustersResp.Clusters)
			configuredClustersAvailable = true
		}
	}

	// 11. Count addon groups (1 per addon in catalog)
	totalAppSets := len(addonGroups)

	controlPlane := models.ControlPlaneInfo{
		ArgocdVersion:               versionInfo["Version"],
		HelmVersion:                 versionInfo["HelmVersion"],
		KubectlVersion:              versionInfo["KubectlVersion"],
		TotalApps:                   len(addonApps),
		TotalClusters:               len(clusters),
		ConfiguredClusters:          configuredClusters,
		ConfiguredClustersAvailable: configuredClustersAvailable,
		ConnectedClusters:           connectedClusters,
		TotalAppSets:                totalAppSets,
		HealthSummary:               healthSummary,
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

		addonName, clusterName := ExtractAddonCluster(app.Name, clusterNames)

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
	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", s.branch())
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
		if !deployedAddons[addon.Name] {
			continue
		}
		missing, detail := checkMissingResources(ctx, gp, addon.Name, s.branch())
		if missing {
			alerts = append(alerts, models.ResourceAlert{
				AddonName: addon.Name,
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
func checkMissingResources(ctx context.Context, gp gitprovider.GitProvider, addonName, baseBranch string) (bool, string) {
	path := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
	data, err := gp.GetFileContent(ctx, path, baseBranch)
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

// buildRecentSyncs builds the recent-syncs feed from full app details,
// turning each app's ArgoCD history entries into feed rows.
//
// Apps are skipped if isInfrastructureApp matches their name (a prefix
// list of known non-addon infra apps), OR if orchestrator.IsSharkoSystemApp
// says they're one of Sharko's own system apps (the engine pin app
// "sharko-engine", or a "connectivity-check-<cluster>" probe). That second
// check matters because isInfrastructureApp's prefix list does not know
// about those names — without it, "sharko-engine" reaches
// extractAddonCluster's last-hyphen fallback and gets misread as addon
// "sharko" running on a cluster named "engine", a cluster that doesn't
// exist (walk finding).
func buildRecentSyncs(apps []models.ArgocdApplication, clusterNames map[string]bool) []models.SyncActivityEntry {
	var allSyncs []models.SyncActivityEntry
	for _, app := range apps {
		if isInfrastructureApp(app.Name) || orchestrator.IsSharkoSystemApp(app.Name) {
			continue
		}
		addonName, clusterName := ExtractAddonCluster(app.Name, clusterNames)
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
	return allSyncs
}

// ExtractAddonCluster extracts addon name and cluster name from an ArgoCD app name.
// App names follow the pattern {addon}-{cluster}. Cluster names from the known set are
// matched against the suffix; the remainder is the addon name.
//
// Exported (perf M2 / walk finding #691) so internal/api's attention-items
// handler can reuse the exact same split instead of the naive
// `addonName := app.Name` it used to do — see handleGetAttentionItems in
// internal/api/dashboard.go.
func ExtractAddonCluster(appName string, clusterNames map[string]bool) (addon, cluster string) {
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

// isInClusterEntry reports whether an ArgoCD cluster entry represents the
// host/management cluster (the one running Sharko + ArgoCD themselves). The
// host cluster is identified either by the conventional ArgoCD name
// "in-cluster" OR by the in-cluster API server URL "https://kubernetes.default.svc".
// Matching both axes keeps the filter robust against installations that
// rename the in-cluster secret. Exported intentionally — the observability
// service is the only consumer today, but the dashboard service and
// any future "monitored clusters" listing should reuse this helper
// rather than re-inventing the predicate.
func isInClusterEntry(c models.ArgocdCluster) bool {
	if c.Name == "in-cluster" {
		return true
	}
	if strings.HasPrefix(c.Server, "https://kubernetes.default") {
		return true
	}
	return false
}

// filterInClusterEntries returns a copy of the slice with any host/management
// cluster entries removed. See isInClusterEntry for the predicate definition.
// Pure function — exposes the filter at the data-source boundary so every
// downstream computation (cluster names map, total counts, addon-app
// cluster extraction) inherits the fix without per-call-site sprinkling.
func filterInClusterEntries(clusters []models.ArgocdCluster) []models.ArgocdCluster {
	filtered := make([]models.ArgocdCluster, 0, len(clusters))
	for _, c := range clusters {
		if isInClusterEntry(c) {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
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
