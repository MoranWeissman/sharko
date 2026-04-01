package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/moran/argocd-addons-platform/internal/argocd"
	"github.com/moran/argocd-addons-platform/internal/config"
	"github.com/moran/argocd-addons-platform/internal/gitprovider"
	"github.com/moran/argocd-addons-platform/internal/models"
)

// ClusterService handles cluster-related operations.
type ClusterService struct {
	parser *config.Parser
}

// NewClusterService creates a new ClusterService.
func NewClusterService() *ClusterService {
	return &ClusterService{
		parser: config.NewParser(),
	}
}

// ListClusters returns all clusters with health stats from Git + ArgoCD.
func (s *ClusterService) ListClusters(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClustersResponse, error) {
	// Fetch Git config
	clusterData, err := gp.GetFileContent(ctx, "configuration/cluster-addons.yaml", "main")
	if err != nil {
		return nil, err
	}

	clusters, err := s.parser.ParseClusterAddons(clusterData)
	if err != nil {
		return nil, err
	}

	// Fetch ArgoCD clusters for health stats
	argocdClusters, err := ac.ListClusters(ctx)
	if err != nil {
		log.Printf("Warning: could not fetch ArgoCD clusters: %v", err)
		// Continue without ArgoCD data
		return &models.ClustersResponse{
			Clusters:    clusters,
			HealthStats: s.computeHealthStats(clusters, nil),
		}, nil
	}

	// Build ArgoCD cluster lookup
	argocdMap := make(map[string]models.ArgocdCluster)
	for _, ac := range argocdClusters {
		argocdMap[ac.Name] = ac
	}

	// Enrich clusters with ArgoCD status
	for i := range clusters {
		if ac, ok := argocdMap[clusters[i].Name]; ok {
			clusters[i].ConnectionStatus = ac.ConnectionState
			clusters[i].ServerVersion = ac.ServerVersion
			delete(argocdMap, clusters[i].Name)
		} else {
			clusters[i].ConnectionStatus = "missing"
		}
	}

	// Add clusters that exist in ArgoCD but not in Git
	notInGitClusters := make([]models.Cluster, 0)
	for name, ac := range argocdMap {
		// Skip the in-cluster entry
		if name == "in-cluster" || strings.HasPrefix(ac.Server, "https://kubernetes.default") {
			continue
		}
		notInGitClusters = append(notInGitClusters, models.Cluster{
			Name:             name,
			Labels:           map[string]string{},
			ConnectionStatus: "not_in_git",
			ServerVersion:    ac.ServerVersion,
		})
	}

	allClusters := append(clusters, notInGitClusters...)

	return &models.ClustersResponse{
		Clusters:    allClusters,
		HealthStats: s.computeHealthStats(clusters, notInGitClusters),
	}, nil
}

// GetClusterDetail returns detail for a single cluster.
func (s *ClusterService) GetClusterDetail(ctx context.Context, clusterName string, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClusterDetailResponse, error) {
	clusterData, err := gp.GetFileContent(ctx, "configuration/cluster-addons.yaml", "main")
	if err != nil {
		return nil, err
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return nil, err
	}

	repoCfg, err := s.parser.ParseAll(clusterData, catalogData)
	if err != nil {
		return nil, err
	}

	// Find the target cluster
	var cluster *models.Cluster
	for i := range repoCfg.Clusters {
		if repoCfg.Clusters[i].Name == clusterName {
			cluster = &repoCfg.Clusters[i]
			break
		}
	}
	if cluster == nil {
		return nil, nil
	}

	// Get enabled addons for this cluster
	addons := s.parser.GetEnabledAddons(*cluster, repoCfg.Addons)

	// Enrich with ArgoCD status
	argocdSvc := argocd.NewService(ac)
	apps, err := argocdSvc.GetClusterApplications(ctx, clusterName)
	if err != nil {
		log.Printf("Warning: could not fetch ArgoCD apps for cluster %s: %v", clusterName, err)
	} else {
		appMap := make(map[string]models.ArgocdApplication)
		for _, app := range apps {
			appMap[app.Name] = app
		}

		for i := range addons {
			appName := addons[i].AddonName + "-" + clusterName
			if app, ok := appMap[appName]; ok {
				addons[i].ArgocdSyncStatus = app.SyncStatus
				addons[i].ArgocdHealthStatus = app.HealthStatus
				addons[i].ArgocdVersion = app.SourceTargetRevision
			} else if app, ok := appMap[addons[i].AddonName]; ok {
				addons[i].ArgocdSyncStatus = app.SyncStatus
				addons[i].ArgocdHealthStatus = app.HealthStatus
				addons[i].ArgocdVersion = app.SourceTargetRevision
			}
		}
	}

	// Get connection state
	connState, _ := argocdSvc.GetClusterConnectionState(ctx, clusterName)
	cluster.ConnectionStatus = connState

	return &models.ClusterDetailResponse{
		Cluster: *cluster,
		Addons:  addons,
	}, nil
}

// GetClusterComparison returns Git vs ArgoCD comparison for a cluster.
func (s *ClusterService) GetClusterComparison(ctx context.Context, clusterName string, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClusterComparisonResponse, error) {
	clusterData, err := gp.GetFileContent(ctx, "configuration/cluster-addons.yaml", "main")
	if err != nil {
		return nil, err
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return nil, err
	}

	repoCfg, err := s.parser.ParseAll(clusterData, catalogData)
	if err != nil {
		return nil, err
	}

	// Find cluster
	var cluster *models.Cluster
	for i := range repoCfg.Clusters {
		if repoCfg.Clusters[i].Name == clusterName {
			cluster = &repoCfg.Clusters[i]
			break
		}
	}
	if cluster == nil {
		return nil, nil
	}

	gitAddons := s.parser.GetEnabledAddons(*cluster, repoCfg.Addons)

	// Fetch ArgoCD apps
	argocdSvc := argocd.NewService(ac)
	argocdApps, err := argocdSvc.GetClusterApplications(ctx, clusterName)
	if err != nil {
		log.Printf("Warning: could not fetch ArgoCD apps: %v", err)
		argocdApps = nil
	}

	argocdAppMap := make(map[string]models.ArgocdApplication)
	for _, app := range argocdApps {
		argocdAppMap[app.Name] = app
	}

	// Build comparisons
	comparisons := make([]models.AddonComparisonStatus, 0)
	trackedArgocdApps := make(map[string]bool)

	totalHealthy, totalIssues, totalMissing := 0, 0, 0

	for _, addon := range gitAddons {
		// GetEnabledAddons now only returns enabled addons, so no need to check addon.Enabled
		comp := models.AddonComparisonStatus{
			AddonName:          addon.AddonName,
			GitConfigured:      true,
			GitChart:           addon.Chart,
			GitRepoURL:         addon.RepoURL,
			GitVersion:         addon.CurrentVersion,
			GitNamespace:       addon.Namespace,
			GitEnabled:         true,
			EnvironmentVersion: addon.EnvironmentVersion,
			CustomVersion:      addon.CustomVersion,
			HasVersionOverride: addon.HasVersionOverride,
			Issues:             []string{},
		}

		// Try to find matching ArgoCD app
		appName := addon.AddonName + "-" + clusterName
		app, found := argocdAppMap[appName]
		if !found {
			app, found = argocdAppMap[addon.AddonName]
			if found {
				appName = addon.AddonName
			}
		}

		if found {
			trackedArgocdApps[appName] = true
			comp.ArgocdDeployed = true
			comp.ArgocdApplicationName = app.Name
			comp.ArgocdSyncStatus = app.SyncStatus
			comp.ArgocdHealthStatus = app.HealthStatus
			comp.ArgocdDeployedVersion = app.SourceTargetRevision
			comp.ArgocdNamespace = app.DestinationNamespace
			comp.ArgocdSourceRepoURL = app.SourceRepoURL
			comp.ArgocdDestinationServer = app.DestinationServer
			comp.ArgocdOperationState = app.OperationState

			comp.Status = classifyHealth(app.HealthStatus, app.SyncStatus)
			if comp.Status == "healthy" {
				totalHealthy++
			} else {
				totalIssues++
			}
		} else {
			comp.Status = "missing_in_argocd"
			totalMissing++
		}

		comparisons = append(comparisons, comp)
	}

	// Find untracked ArgoCD apps (not in Git), excluding infrastructure apps
	totalUntracked := 0
	for appName, app := range argocdAppMap {
		if trackedArgocdApps[appName] {
			continue
		}
		// Skip known infrastructure apps that aren't addons
		if isInfrastructureApp(appName) {
			continue
		}
		totalUntracked++
		comparisons = append(comparisons, models.AddonComparisonStatus{
			AddonName:               appName,
			ArgocdDeployed:          true,
			ArgocdApplicationName:   app.Name,
			ArgocdSyncStatus:        app.SyncStatus,
			ArgocdHealthStatus:      app.HealthStatus,
			ArgocdDeployedVersion:   app.SourceTargetRevision,
			ArgocdNamespace:         app.DestinationNamespace,
			ArgocdSourceRepoURL:     app.SourceRepoURL,
			ArgocdDestinationServer: app.DestinationServer,
			Status:                  "untracked_in_argocd",
			Issues:                  []string{"Application exists in ArgoCD but not configured in Git"},
		})
	}

	// ArgoCD summary stats
	argocdHealthy, argocdSynced, argocdDegraded, argocdOutOfSync := 0, 0, 0, 0
	for _, app := range argocdApps {
		if app.HealthStatus == "Healthy" {
			argocdHealthy++
		}
		if app.HealthStatus == "Degraded" {
			argocdDegraded++
		}
		if app.SyncStatus == "Synced" {
			argocdSynced++
		}
		if app.SyncStatus == "OutOfSync" {
			argocdOutOfSync++
		}
	}

	connState, _ := argocdSvc.GetClusterConnectionState(ctx, clusterName)
	cluster.ConnectionStatus = connState

	return &models.ClusterComparisonResponse{
		Cluster:                      *cluster,
		GitTotalAddons:               len(gitAddons),
		GitEnabledAddons:             len(gitAddons),
		GitDisabledAddons:            0,
		ArgocdTotalApplications:      len(argocdApps),
		ArgocdHealthyApplications:    argocdHealthy,
		ArgocdSyncedApplications:     argocdSynced,
		ArgocdDegradedApplications:   argocdDegraded,
		ArgocdOutOfSyncApplications:  argocdOutOfSync,
		AddonComparisons:             comparisons,
		TotalHealthy:                 totalHealthy,
		TotalWithIssues:              totalIssues,
		TotalMissingInArgocd:         totalMissing,
		TotalUntrackedInArgocd:       totalUntracked,
		TotalDisabledInGit:           0,
		ClusterConnectionState:       connState,
	}, nil
}

// GetConfigDiff returns the diff between a cluster's addon values and global defaults.
func (s *ClusterService) GetConfigDiff(ctx context.Context, clusterName string, gp gitprovider.GitProvider) (*models.ConfigDiffResponse, error) {
	// Fetch cluster values file
	clusterValuesPath := fmt.Sprintf("configuration/addons-clusters-values/%s.yaml", clusterName)
	clusterValuesData, err := gp.GetFileContent(ctx, clusterValuesPath, "main")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster values for %s: %w", clusterName, err)
	}

	// Parse cluster values YAML
	var clusterConfig map[string]interface{}
	if err := yaml.Unmarshal(clusterValuesData, &clusterConfig); err != nil {
		return nil, fmt.Errorf("failed to parse cluster values: %w", err)
	}

	resp := &models.ConfigDiffResponse{
		ClusterName: clusterName,
		AddonDiffs:  []models.ConfigDiffEntry{},
	}

	// Extract clusterGlobalValues if present
	if globalVals, ok := clusterConfig["clusterGlobalValues"]; ok {
		if m, ok := globalVals.(map[string]interface{}); ok {
			resp.GlobalValues = m
		}
	}

	// Iterate over addon sections (everything except clusterGlobalValues)
	addonNames := make([]string, 0, len(clusterConfig))
	for key := range clusterConfig {
		if key == "clusterGlobalValues" {
			continue
		}
		addonNames = append(addonNames, key)
	}
	sort.Strings(addonNames)

	for _, addonName := range addonNames {
		addonSection := clusterConfig[addonName]

		// Marshal cluster addon values to YAML
		clusterYAML, err := yaml.Marshal(addonSection)
		if err != nil {
			log.Printf("Warning: could not marshal cluster values for addon %s: %v", addonName, err)
			continue
		}

		// Fetch global defaults for this addon
		globalPath := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
		globalData, err := gp.GetFileContent(ctx, globalPath, "main")
		globalYAML := ""
		if err != nil {
			log.Printf("Info: no global defaults found for addon %s: %v", addonName, err)
		} else {
			globalYAML = string(globalData)
		}

		clusterYAMLStr := string(clusterYAML)
		hasOverrides := strings.TrimSpace(clusterYAMLStr) != strings.TrimSpace(globalYAML)

		resp.AddonDiffs = append(resp.AddonDiffs, models.ConfigDiffEntry{
			AddonName:     addonName,
			HasOverrides:  hasOverrides,
			GlobalValues:  globalYAML,
			ClusterValues: clusterYAMLStr,
		})
	}

	return resp, nil
}

func (s *ClusterService) computeHealthStats(gitClusters []models.Cluster, notInGit []models.Cluster) *models.ClusterHealthStats {
	stats := &models.ClusterHealthStats{
		TotalInGit: len(gitClusters),
		NotInGit:   len(notInGit),
	}

	for _, c := range gitClusters {
		switch c.ConnectionStatus {
		case "Successful", "connected":
			stats.Connected++
		case "Failed", "failed":
			stats.Failed++
		case "missing":
			stats.MissingFromArgoCD++
		}
	}

	return stats
}

// infrastructureAppPrefixes are ArgoCD app name prefixes for infrastructure
// components that are not managed via the addons catalog. These are excluded
// from the "untracked in ArgoCD" list in the comparison view.
var infrastructureAppPrefixes = []string{
	"karpenter-nodepool",
	"bootstrap-",
	"eso-",
	"cluster-addons",
	"clusters",
	"external-secrets-operator",
	"eso-remote-prerequisites",
	"github-repo-credentials",
}

func isInfrastructureApp(appName string) bool {
	lower := strings.ToLower(appName)
	for _, prefix := range infrastructureAppPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// GetClusterValues returns the raw cluster-specific values YAML.
func (s *ClusterService) GetClusterValues(ctx context.Context, clusterName string, gp gitprovider.GitProvider) (*models.ClusterValuesResponse, error) {
	path := fmt.Sprintf("configuration/addons-clusters-values/%s.yaml", clusterName)
	data, err := gp.GetFileContent(ctx, path, "main")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster values for %s: %w", clusterName, err)
	}

	return &models.ClusterValuesResponse{
		ClusterName: clusterName,
		ValuesYAML:  string(data),
	}, nil
}

func classifyHealth(healthStatus, _ string) string {
	switch healthStatus {
	case "Healthy":
		return "healthy"
	case "Progressing":
		return "progressing"
	case "Degraded":
		return "unhealthy"
	case "Unknown", "":
		return "unknown_health"
	default:
		return "unknown_state"
	}
}
