package models

// AddonCatalogEntry represents an addon definition from addons-catalog.yaml.
type AddonCatalogEntry struct {
	AppName            string                   `json:"appName" yaml:"appName"`
	RepoURL            string                   `json:"repoURL" yaml:"repoURL"`
	Chart              string                   `json:"chart" yaml:"chart"`
	Version            string                   `json:"version" yaml:"version"`
	Namespace          string                   `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	InMigration        bool                     `json:"inMigration,omitempty" yaml:"inMigration,omitempty"`
	IgnoreDifferences  []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`
}

// AddonDeploymentInfo holds information about an addon's deployment in a specific cluster.
type AddonDeploymentInfo struct {
	ClusterName        string `json:"cluster_name"`
	ClusterEnvironment string `json:"cluster_environment,omitempty"`
	Enabled            bool   `json:"enabled"`
	ConfiguredVersion  string `json:"configured_version,omitempty"`
	DeployedVersion    string `json:"deployed_version,omitempty"`
	Namespace          string `json:"namespace,omitempty"`

	// ArgoCD status
	SyncStatus      string `json:"sync_status,omitempty"`
	HealthStatus    string `json:"health_status,omitempty"`
	ApplicationName string `json:"application_name,omitempty"`

	Status string `json:"status"`
}

// AddonCatalogItem is the catalog view of an addon with stats across clusters.
type AddonCatalogItem struct {
	AddonName   string `json:"addon_name"`
	Chart       string `json:"chart"`
	RepoURL     string `json:"repo_url"`
	Namespace   string `json:"namespace,omitempty"`
	Version     string `json:"version"`
	InMigration bool   `json:"in_migration,omitempty"`

	// Stats
	TotalClusters        int `json:"total_clusters"`
	EnabledClusters      int `json:"enabled_clusters"`
	HealthyApplications  int `json:"healthy_applications"`
	DegradedApplications int `json:"degraded_applications"`
	MissingApplications  int `json:"missing_applications"`

	// Per-cluster details
	Applications []AddonDeploymentInfo `json:"applications"`
}

// AddonCatalogResponse is the API response for the addon catalog.
type AddonCatalogResponse struct {
	Addons        []AddonCatalogItem `json:"addons"`
	TotalAddons   int                `json:"total_addons"`
	TotalClusters int                `json:"total_clusters"`
	AddonsOnlyInGit int              `json:"addons_only_in_git"`
}

// AddonDetailResponse is the API response for a single addon's details.
type AddonDetailResponse struct {
	Addon AddonCatalogItem `json:"addon"`
}

// AddonValuesResponse is the API response for raw addon global values YAML.
type AddonValuesResponse struct {
	AddonName  string `json:"addon_name"`
	ValuesYAML string `json:"values_yaml"`
}

// VersionMatrixCell holds version and health info for one addon on one cluster.
type VersionMatrixCell struct {
	Version          string `json:"version"`            // Deployed or configured version
	Health           string `json:"health"`             // Healthy, Degraded, Progressing, Unknown, missing, not_enabled
	DriftFromCatalog bool   `json:"drift_from_catalog"` // True if version differs from catalog default
}

// VersionMatrixRow represents one addon across all clusters.
type VersionMatrixRow struct {
	AddonName      string                       `json:"addon_name"`
	CatalogVersion string                       `json:"catalog_version"`
	Chart          string                       `json:"chart"`
	Cells          map[string]VersionMatrixCell `json:"cells"` // key = cluster name
}

// VersionMatrixResponse is the API response.
type VersionMatrixResponse struct {
	Clusters []string           `json:"clusters"` // Column headers (cluster names)
	Addons   []VersionMatrixRow `json:"addons"`   // Rows
}
