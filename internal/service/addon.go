package service

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/moran/argocd-addons-platform/internal/argocd"
	"github.com/moran/argocd-addons-platform/internal/config"
	"github.com/moran/argocd-addons-platform/internal/gitprovider"
	"github.com/moran/argocd-addons-platform/internal/models"
)

// extractEnvironment returns the environment segment from a cluster name by
// matching each dash-separated part against the configured environments list.
// e.g. "nms-core-dev-eks" with envs ["dev","prod"] → "dev"
func extractEnvironment(clusterName string, envs []string) string {
	if len(envs) == 0 {
		return ""
	}
	parts := strings.Split(clusterName, "-")
	for _, part := range parts {
		for _, env := range envs {
			if strings.EqualFold(part, env) {
				return env
			}
		}
	}
	return ""
}

// loadEnvironments reads the APP_ENVIRONMENTS env var and returns the list.
func loadEnvironments() []string {
	raw := os.Getenv("APP_ENVIRONMENTS")
	if raw == "" {
		return nil
	}
	var envs []string
	for _, e := range strings.Split(raw, ",") {
		if t := strings.TrimSpace(e); t != "" {
			envs = append(envs, t)
		}
	}
	return envs
}

// AddonService handles addon-related operations.
type AddonService struct {
	parser *config.Parser
}

// NewAddonService creates a new AddonService.
func NewAddonService() *AddonService {
	return &AddonService{
		parser: config.NewParser(),
	}
}

// ListAddons returns the raw addon catalog from Git.
func (s *AddonService) ListAddons(ctx context.Context, gp gitprovider.GitProvider) ([]models.AddonCatalogEntry, error) {
	catalogData, err := gp.GetFileContent(ctx, "configuration/addons-catalog.yaml", "main")
	if err != nil {
		return nil, err
	}

	return s.parser.ParseAddonsCatalog(catalogData)
}

// GetCatalog returns the full addon catalog with deployment stats across clusters.
func (s *AddonService) GetCatalog(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.AddonCatalogResponse, error) {
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

	// Fetch ArgoCD applications
	argocdSvc := argocd.NewService(ac)
	allApps, err := ac.ListApplications(ctx)
	if err != nil {
		log.Printf("Warning: could not fetch ArgoCD applications: %v", err)
	}

	appMap := make(map[string]models.ArgocdApplication)
	for _, app := range allApps {
		appMap[app.Name] = app
	}

	envList := loadEnvironments()
	items := make([]models.AddonCatalogItem, 0, len(repoCfg.Addons))
	totalClusters := len(repoCfg.Clusters)
	addonsOnlyInGit := 0

	for _, addon := range repoCfg.Addons {
		item := models.AddonCatalogItem{
			AddonName:   addon.AppName,
			Chart:       addon.Chart,
			RepoURL:     addon.RepoURL,
			Namespace:   addon.Namespace,
			Version:     addon.Version,
			InMigration: addon.InMigration,
		}

		// Check which clusters have this addon
		deployments := make([]models.AddonDeploymentInfo, 0)
		enabledCount, healthyCount, degradedCount, missingCount := 0, 0, 0, 0

		for _, cluster := range repoCfg.Clusters {
			labelVal, hasAddon := cluster.Labels[addon.AppName]
			if !hasAddon {
				continue
			}

			// Only care about enabled addons — disabled is the same as not configured
			enabled := labelVal == "enabled"
			if !enabled {
				continue
			}
			enabledCount++

			// Version for this cluster
			version := addon.Version
			versionKey := addon.AppName + "-version"
			if override, ok := cluster.Labels[versionKey]; ok {
				version = override
			}

			dep := models.AddonDeploymentInfo{
				ClusterName:        cluster.Name,
				ClusterEnvironment: extractEnvironment(cluster.Name, envList),
				Enabled:            true,
				ConfiguredVersion:  version,
				Namespace:          addon.Namespace,
			}

			{
				// Check ArgoCD status
				appName := addon.AppName + "-" + cluster.Name
				if app, ok := appMap[appName]; ok {
					dep.SyncStatus = app.SyncStatus
					dep.HealthStatus = app.HealthStatus
					dep.DeployedVersion = app.SourceTargetRevision
					dep.ApplicationName = app.Name

					switch app.HealthStatus {
					case "Healthy":
						dep.Status = "healthy"
						healthyCount++
					case "Degraded":
						dep.Status = "degraded"
						degradedCount++
					default:
						dep.Status = "unknown"
					}
				} else {
					dep.Status = "missing"
					missingCount++
				}
			}

			deployments = append(deployments, dep)
		}

		item.TotalClusters = len(deployments)
		item.EnabledClusters = enabledCount
		item.HealthyApplications = healthyCount
		item.DegradedApplications = degradedCount
		item.MissingApplications = missingCount
		item.Applications = deployments

		if enabledCount == 0 {
			addonsOnlyInGit++
		}

		items = append(items, item)
	}

	_ = argocdSvc // used for future enrichment

	return &models.AddonCatalogResponse{
		Addons:          items,
		TotalAddons:     len(items),
		TotalClusters:   totalClusters,
		AddonsOnlyInGit: addonsOnlyInGit,
	}, nil
}

// GetAddonDetail returns detailed information about a specific addon.
func (s *AddonService) GetAddonDetail(ctx context.Context, addonName string, gp gitprovider.GitProvider, ac *argocd.Client) (*models.AddonDetailResponse, error) {
	catalog, err := s.GetCatalog(ctx, gp, ac)
	if err != nil {
		return nil, err
	}

	for _, item := range catalog.Addons {
		if item.AddonName == addonName {
			return &models.AddonDetailResponse{
				Addon: item,
			}, nil
		}
	}

	return nil, nil
}

// GetVersionMatrix returns a version matrix showing addon versions and health across all clusters.
func (s *AddonService) GetVersionMatrix(ctx context.Context, gp gitprovider.GitProvider, ac *argocd.Client) (*models.VersionMatrixResponse, error) {
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

	// Fetch ArgoCD applications once and build a lookup map
	allApps, err := ac.ListApplications(ctx)
	if err != nil {
		log.Printf("Warning: could not fetch ArgoCD applications: %v", err)
	}

	appMap := make(map[string]models.ArgocdApplication)
	for _, app := range allApps {
		appMap[app.Name] = app
	}

	// Collect sorted cluster names
	clusterNames := make([]string, 0, len(repoCfg.Clusters))
	for _, cluster := range repoCfg.Clusters {
		clusterNames = append(clusterNames, cluster.Name)
	}
	sort.Strings(clusterNames)

	// Build a map for quick cluster lookup by name
	clusterMap := make(map[string]models.Cluster, len(repoCfg.Clusters))
	for _, cluster := range repoCfg.Clusters {
		clusterMap[cluster.Name] = cluster
	}

	// Build rows
	rows := make([]models.VersionMatrixRow, 0, len(repoCfg.Addons))
	for _, addon := range repoCfg.Addons {
		row := models.VersionMatrixRow{
			AddonName:      addon.AppName,
			CatalogVersion: addon.Version,
			Chart:          addon.Chart,
			Cells:          make(map[string]models.VersionMatrixCell),
		}

		for _, clusterName := range clusterNames {
			cluster := clusterMap[clusterName]
			labelVal, hasLabel := cluster.Labels[addon.AppName]
			if !hasLabel {
				continue
			}

			enabled := labelVal == "enabled"

			// Determine version
			version := addon.Version
			versionKey := addon.AppName + "-version"
			if override, ok := cluster.Labels[versionKey]; ok {
				version = override
			}

			cell := models.VersionMatrixCell{
				Version:          version,
				DriftFromCatalog: version != addon.Version,
			}

			if !enabled {
				cell.Health = "not_enabled"
			} else {
				// Check ArgoCD status
				appName := addon.AppName + "-" + clusterName
				if app, ok := appMap[appName]; ok {
					cell.Health = app.HealthStatus
					if cell.Health == "" {
						cell.Health = "Unknown"
					}
				} else {
					cell.Health = "missing"
				}
			}

			row.Cells[clusterName] = cell
		}

		rows = append(rows, row)
	}

	// Sort rows by addon name
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].AddonName < rows[j].AddonName
	})

	return &models.VersionMatrixResponse{
		Clusters: clusterNames,
		Addons:   rows,
	}, nil
}

// GetAddonValues returns the global default values YAML for a specific addon.
func (s *AddonService) GetAddonValues(ctx context.Context, addonName string, gp gitprovider.GitProvider) (*models.AddonValuesResponse, error) {
	path := fmt.Sprintf("configuration/addons-global-values/%s.yaml", addonName)
	data, err := gp.GetFileContent(ctx, path, "main")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch global values for addon %s: %w", addonName, err)
	}

	return &models.AddonValuesResponse{
		AddonName:  addonName,
		ValuesYAML: string(data),
	}, nil
}
