package config

import (
	"fmt"
	"strings"

	"github.com/moran/argocd-addons-platform/internal/models"
	"gopkg.in/yaml.v3"
)

// clusterAddonsFile represents the structure of cluster-addons.yaml.
type clusterAddonsFile struct {
	Clusters []clusterEntry `yaml:"clusters"`
}

type clusterEntry struct {
	Name   string      `yaml:"name"`
	Labels interface{} `yaml:"labels"` // Can be map[string]string or []interface{} (empty)
	Region string      `yaml:"region,omitempty"`
}

// addonsCatalogFile represents the structure of addons-catalog.yaml.
type addonsCatalogFile struct {
	ApplicationSets           []models.AddonCatalogEntry `yaml:"applicationsets"`
	MigrationIgnoreDifferences []map[string]interface{}  `yaml:"migrationIgnoreDifferences,omitempty"`
}

// clusterValuesFile represents a per-cluster values file.
type clusterValuesFile struct {
	ClusterGlobalValues map[string]interface{} `yaml:"clusterGlobalValues"`
}

// RepoConfig holds the fully parsed configuration from the Git repository.
type RepoConfig struct {
	Clusters []models.Cluster
	Addons   []models.AddonCatalogEntry
}

// Parser parses the argocd-cluster-addons repo configuration files.
type Parser struct{}

// NewParser creates a new config parser.
func NewParser() *Parser {
	return &Parser{}
}

// ParseClusterAddons parses cluster-addons.yaml content.
func (p *Parser) ParseClusterAddons(data []byte) ([]models.Cluster, error) {
	var file clusterAddonsFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing cluster-addons.yaml: %w", err)
	}

	clusters := make([]models.Cluster, 0, len(file.Clusters))
	for _, entry := range file.Clusters {
		cluster := models.Cluster{
			Name:   entry.Name,
			Labels: parseLabels(entry.Labels),
			Region: entry.Region,
		}
		clusters = append(clusters, cluster)
	}

	return clusters, nil
}

// parseLabels handles the labels field which can be a map or an empty array.
func parseLabels(raw interface{}) map[string]string {
	if raw == nil {
		return map[string]string{}
	}

	switch v := raw.(type) {
	case map[string]interface{}:
		labels := make(map[string]string, len(v))
		for key, val := range v {
			labels[key] = fmt.Sprintf("%v", val)
		}
		return labels
	case []interface{}:
		// Empty array means no labels
		return map[string]string{}
	default:
		return map[string]string{}
	}
}

// ParseAddonsCatalog parses addons-catalog.yaml content.
func (p *Parser) ParseAddonsCatalog(data []byte) ([]models.AddonCatalogEntry, error) {
	var file addonsCatalogFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing addons-catalog.yaml: %w", err)
	}

	return file.ApplicationSets, nil
}

// ParseClusterValues parses a per-cluster values file and extracts global values.
func (p *Parser) ParseClusterValues(data []byte) (map[string]interface{}, error) {
	var file clusterValuesFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parsing cluster values: %w", err)
	}

	return file.ClusterGlobalValues, nil
}

// GetEnabledAddons returns which addons are enabled/disabled for a cluster based on its labels.
func (p *Parser) GetEnabledAddons(cluster models.Cluster, catalog []models.AddonCatalogEntry) []models.ClusterAddonInfo {
	addons := make([]models.ClusterAddonInfo, 0)

	for _, catalogEntry := range catalog {
		addonName := catalogEntry.AppName
		labelValue, hasLabel := cluster.Labels[addonName]

		if !hasLabel {
			continue
		}

		// Only include addons where the label value is "enabled".
		// The ArgoCD cluster generator only listens to key: enabled;
		// disabled, commented out, or missing labels are all equivalent.
		if !strings.EqualFold(labelValue, "enabled") {
			continue
		}

		// Check for version override: <addon-name>-version label
		versionKey := addonName + "-version"
		customVersion, hasOverride := cluster.Labels[versionKey]

		currentVersion := catalogEntry.Version
		if hasOverride && customVersion != "" {
			currentVersion = customVersion
		}

		addon := models.ClusterAddonInfo{
			AddonName:          addonName,
			Chart:              catalogEntry.Chart,
			RepoURL:            catalogEntry.RepoURL,
			CurrentVersion:     currentVersion,
			Enabled:            true,
			Namespace:          catalogEntry.Namespace,
			EnvironmentVersion: catalogEntry.Version,
			CustomVersion:      customVersion,
			HasVersionOverride: hasOverride,
		}

		addons = append(addons, addon)
	}

	return addons
}

// ParseAll parses all configuration files and returns a combined RepoConfig.
func (p *Parser) ParseAll(clusterAddonsData, addonsCatalogData []byte) (*RepoConfig, error) {
	clusters, err := p.ParseClusterAddons(clusterAddonsData)
	if err != nil {
		return nil, err
	}

	addons, err := p.ParseAddonsCatalog(addonsCatalogData)
	if err != nil {
		return nil, err
	}

	return &RepoConfig{
		Clusters: clusters,
		Addons:   addons,
	}, nil
}
