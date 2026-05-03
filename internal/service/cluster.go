package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// isGitFileNotFound reports whether err signals "file does not exist" from a
// gitprovider.GitProvider.GetFileContent call.
//
// Detection is type-based — every provider implementation wraps actual
// missing-file conditions with gitprovider.ErrFileNotFound (see
// internal/gitprovider/provider.go). The previous substring-matching
// implementation silently masked legitimate auth/branch/perm errors that
// happened to contain the words "not found" or "404" as "missing file →
// empty list" (review finding H2 against PR #318) — examples that would
// have tripped the old helper:
//
//   - "GitHub repository not found — check the URL and credentials"
//   - "branch 'main' not found"
//   - "deployment 'foo' not found"
//   - "got 4040 bytes" (the "404" substring case)
//
// fs.ErrNotExist is also accepted for callers that go through stdlib paths
// (e.g. local filesystem in tests).
//
// A nil err returns false. This helper is intentionally lenient so a missing
// managed-clusters.yaml in a brand-new install (the Sharko v1.24 reproducer
// for BUG-005) is treated as an empty list rather than a 500.
func isGitFileNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, gitprovider.ErrFileNotFound) || errors.Is(err, fs.ErrNotExist)
}

// ClusterService handles cluster-related operations.
type ClusterService struct {
	parser              *config.Parser
	managedClustersPath string // path in Git repo to managed-clusters.yaml
}

// NewClusterService creates a new ClusterService.
// managedClustersPath is the Git repo path to the managed clusters YAML
// (e.g. "configuration/managed-clusters.yaml"). An empty string defaults to
// "configuration/managed-clusters.yaml".
func NewClusterService(managedClustersPath string) *ClusterService {
	if managedClustersPath == "" {
		managedClustersPath = "configuration/managed-clusters.yaml"
	}
	return &ClusterService{
		parser:              config.NewParser(),
		managedClustersPath: managedClustersPath,
	}
}

// ListClusters returns all clusters with health stats from Git + ArgoCD.
func (s *ClusterService) ListClusters(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.ClustersResponse, error) {
	// Fetch Git config
	clusterData, err := gp.GetFileContent(ctx, s.managedClustersPath, "main")
	if err != nil {
		if isGitFileNotFound(err) {
			clusterData = []byte("clusters: []")
		} else {
			return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
		}
	}

	clusters, err := s.parser.ParseClusterAddons(clusterData)
	if err != nil {
		return nil, err
	}

	// Fetch ArgoCD clusters for health stats
	argocdClusters, err := ac.ListClusters(ctx)
	if err != nil {
		slog.Warn("could not fetch argocd clusters", "error", err)
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

	// Enrich clusters with ArgoCD status; mark as managed (in managed-clusters.yaml)
	for i := range clusters {
		clusters[i].Managed = true
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
	clusterData, err := gp.GetFileContent(ctx, s.managedClustersPath, "main")
	if err != nil {
		if isGitFileNotFound(err) {
			clusterData = []byte("clusters: []")
		} else {
			return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
		}
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		if isGitFileNotFound(err) {
			catalogData = []byte("applicationsets: []")
		} else {
			return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
		}
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
		slog.Warn("could not fetch argocd apps for cluster", "cluster", clusterName, "error", err)
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
	clusterData, err := gp.GetFileContent(ctx, s.managedClustersPath, "main")
	if err != nil {
		if isGitFileNotFound(err) {
			clusterData = []byte("clusters: []")
		} else {
			return nil, fmt.Errorf("reading managed-clusters.yaml: %w", err)
		}
	}

	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		if isGitFileNotFound(err) {
			catalogData = []byte("applicationsets: []")
		} else {
			return nil, fmt.Errorf("reading addons-catalog.yaml: %w", err)
		}
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
		slog.Warn("could not fetch argocd apps", "error", err)
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

	connStatus, connMessage, connErr := argocdSvc.GetClusterConnectionInfo(ctx, clusterName)
	if connErr != nil {
		slog.Warn("could not fetch argocd connection info", "cluster", clusterName, "error", connErr)
		if connStatus == "" {
			connStatus = "Unknown"
		}
		connMessage = connErr.Error()
	}
	cluster.ConnectionStatus = connStatus

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
		ClusterConnectionState:       connStatus,
		ArgocdConnectionStatus:       connStatus,
		ArgocdConnectionMessage:      connMessage,
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
			slog.Warn("could not marshal cluster values for addon", "addon", addonName, "error", err)
			continue
		}

		// Fetch global defaults for this addon
		globalPath := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
		globalData, err := gp.GetFileContent(ctx, globalPath, "main")
		globalYAML := ""
		if err != nil {
			slog.Info("no global defaults found for addon", "addon", addonName, "error", err)
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

// GetClusterAddonValues extracts the YAML for one addon's section in a
// cluster's overrides file. CurrentOverrides is the empty string when the
// cluster file does not exist yet, or when the file exists but does not
// contain a section for this addon.
//
// Schema lookup mirrors AddonService.GetAddonValuesAndSchema — best-effort
// read of `configuration/addons-global-values/<addon>.schema.json`.
func (s *ClusterService) GetClusterAddonValues(ctx context.Context, clusterName, addonName string, gp gitprovider.GitProvider) (*models.ClusterAddonValuesResponse, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}
	if addonName == "" {
		return nil, fmt.Errorf("addon name is required")
	}

	resp := &models.ClusterAddonValuesResponse{
		ClusterName: clusterName,
		AddonName:   addonName,
	}

	clusterPath := fmt.Sprintf("configuration/addons-clusters-values/%s.yaml", clusterName)
	if data, err := gp.GetFileContent(ctx, clusterPath, "main"); err == nil && len(data) > 0 {
		root := map[string]interface{}{}
		if uerr := yaml.Unmarshal(data, &root); uerr != nil {
			slog.Warn("could not parse cluster overrides file", "cluster", clusterName, "error", uerr)
		} else if section, ok := root[addonName]; ok {
			if marshalled, merr := yaml.Marshal(section); merr == nil {
				resp.CurrentOverrides = string(marshalled)
			}
		}
	}

	schemaPath := fmt.Sprintf("configuration/addons-global-values/%s.schema.json", addonName)
	if schemaData, err := gp.GetFileContent(ctx, schemaPath, "main"); err == nil && len(schemaData) > 0 {
		var schema map[string]interface{}
		if jerr := json.Unmarshal(schemaData, &schema); jerr != nil {
			slog.Warn("ignoring unparseable values schema", "addon", addonName, "error", jerr)
		} else {
			resp.Schema = schema
		}
	}

	return resp, nil
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
