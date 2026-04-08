package models

// AddonSecretRef describes a Kubernetes Secret that an addon needs on remote clusters.
// Keys maps the secret data key (as it will appear in the K8s Secret) to the
// provider path that holds the actual value (e.g. "secrets/datadog/api-key").
type AddonSecretRef struct {
	SecretName string            `json:"secretName" yaml:"secretName"`
	Namespace  string            `json:"namespace" yaml:"namespace"`
	Keys       map[string]string `json:"keys" yaml:"keys"`
}

// AddonSource represents an additional Helm chart or manifest source for an addon.
type AddonSource struct {
	RepoURL    string            `json:"repoURL,omitempty" yaml:"repoURL,omitempty"`
	Path       string            `json:"path,omitempty" yaml:"path,omitempty"`
	Chart      string            `json:"chart,omitempty" yaml:"chart,omitempty"`
	Version    string            `json:"version,omitempty" yaml:"version,omitempty"`
	Parameters map[string]string `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	ValueFiles []string          `json:"valueFiles,omitempty" yaml:"valueFiles,omitempty"`
}

// AddonCatalogEntry represents an addon definition from addons-catalog.yaml.
type AddonCatalogEntry struct {
	// Basic (required)
	Name string `json:"name" yaml:"name"`
	RepoURL string `json:"repoURL" yaml:"repoURL"`
	Chart   string `json:"chart" yaml:"chart"`
	Version string `json:"version" yaml:"version"`

	// Basic (optional)
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`

	// Advanced — deployment behavior
	SyncWave    int      `json:"syncWave,omitempty" yaml:"syncWave,omitempty"`
	SelfHeal    *bool    `json:"selfHeal,omitempty" yaml:"selfHeal,omitempty"`
	SyncOptions []string `json:"syncOptions,omitempty" yaml:"syncOptions,omitempty"`

	// Advanced — additional sources
	AdditionalSources []AddonSource `json:"additionalSources,omitempty" yaml:"additionalSources,omitempty"`

	// Advanced — ArgoCD behavior
	IgnoreDifferences []map[string]interface{} `json:"ignoreDifferences,omitempty" yaml:"ignoreDifferences,omitempty"`

	// Advanced — extra Helm configuration
	ExtraHelmValues map[string]string `json:"extraHelmValues,omitempty" yaml:"extraHelmValues,omitempty"`

	// Dependency ordering — addon names that must be synced before this one.
	// Sharko uses this to warn when sync waves conflict and to validate the dependency graph.
	DependsOn []string `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`

	// Secret requirements — Sharko creates these K8s Secrets on remote clusters
	Secrets []AddonSecretRef `json:"secrets,omitempty" yaml:"secrets,omitempty"`
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
	AddonName string `json:"addon_name"`
	Chart     string `json:"chart"`
	RepoURL   string `json:"repo_url"`
	Namespace string `json:"namespace,omitempty"`
	Version   string `json:"version"`

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

// ApplicationSetCondition holds a single condition from an ArgoCD ApplicationSet status.
type ApplicationSetCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ApplicationSetStatusInfo holds status information for an ArgoCD ApplicationSet.
type ApplicationSetStatusInfo struct {
	Name          string                    `json:"name"`
	Conditions    []ApplicationSetCondition `json:"conditions"`
	GeneratedApps int                       `json:"generated_apps"`
}

// AddonDetailResponse is the API response for a single addon's details.
type AddonDetailResponse struct {
	Addon            AddonCatalogItem           `json:"addon"`
	ApplicationSet   *ApplicationSetStatusInfo  `json:"application_set,omitempty"`
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
