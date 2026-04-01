package models

// Cluster represents a Kubernetes cluster from the Git configuration.
type Cluster struct {
	Name             string            `json:"name" yaml:"name"`
	Labels           map[string]string `json:"labels" yaml:"labels"`
	Region           string            `json:"region,omitempty" yaml:"region,omitempty"`
	ServerVersion    string            `json:"server_version,omitempty"`
	ConnectionStatus string            `json:"connection_status,omitempty"`
}

// ClusterHealthStats holds aggregated health statistics for the clusters overview.
type ClusterHealthStats struct {
	TotalInGit         int `json:"total_in_git"`
	Connected          int `json:"connected"`
	Failed             int `json:"failed"`
	MissingFromArgoCD  int `json:"missing_from_argocd"`
	NotInGit           int `json:"not_in_git"`
}

// ClustersResponse is the API response for listing clusters.
type ClustersResponse struct {
	Clusters    []Cluster           `json:"clusters"`
	HealthStats *ClusterHealthStats `json:"health_stats,omitempty"`
}

// ClusterAddonInfo holds combined information about an addon in a specific cluster.
type ClusterAddonInfo struct {
	AddonName          string `json:"addon_name"`
	Chart              string `json:"chart"`
	RepoURL            string `json:"repo_url"`
	CurrentVersion     string `json:"current_version"`
	Enabled            bool   `json:"enabled"`
	Namespace          string `json:"namespace,omitempty"`
	EnvironmentVersion string `json:"environment_version,omitempty"`
	CustomVersion      string `json:"custom_version,omitempty"`
	HasVersionOverride bool   `json:"has_version_override"`

	// ArgoCD status fields
	ArgocdSyncStatus   string `json:"argocd_sync_status,omitempty"`
	ArgocdHealthStatus string `json:"argocd_health_status,omitempty"`
	ArgocdVersion      string `json:"argocd_version,omitempty"`
}

// ClusterDetailResponse is the API response for a single cluster's details.
type ClusterDetailResponse struct {
	Cluster Cluster          `json:"cluster"`
	Addons  []ClusterAddonInfo `json:"addons"`
}

// AddonComparisonStatus holds the comparison between Git config and ArgoCD deployment for one addon.
type AddonComparisonStatus struct {
	AddonName string `json:"addon_name"`

	// Git configuration
	GitConfigured bool   `json:"git_configured"`
	GitChart      string `json:"git_chart,omitempty"`
	GitRepoURL    string `json:"git_repo_url,omitempty"`
	GitVersion    string `json:"git_version,omitempty"`
	GitNamespace  string `json:"git_namespace,omitempty"`
	GitEnabled    bool   `json:"git_enabled"`

	// Version tracking
	EnvironmentVersion string `json:"environment_version,omitempty"`
	CustomVersion      string `json:"custom_version,omitempty"`
	HasVersionOverride bool   `json:"has_version_override"`

	// ArgoCD deployment
	ArgocdDeployed          bool   `json:"argocd_deployed"`
	ArgocdApplicationName   string `json:"argocd_application_name,omitempty"`
	ArgocdSyncStatus        string `json:"argocd_sync_status,omitempty"`
	ArgocdHealthStatus      string `json:"argocd_health_status,omitempty"`
	ArgocdDeployedVersion   string `json:"argocd_deployed_version,omitempty"`
	ArgocdNamespace         string `json:"argocd_namespace,omitempty"`
	ArgocdSourceRepoURL     string `json:"argocd_source_repo_url,omitempty"`
	ArgocdSourcePath        string `json:"argocd_source_path,omitempty"`
	ArgocdDestinationServer string `json:"argocd_destination_server,omitempty"`
	ArgocdOperationState    string `json:"argocd_operation_state,omitempty"`

	// Comparison results
	Status string   `json:"status,omitempty"`
	Issues []string `json:"issues"`

	LastSyncTime string `json:"last_sync_time,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

// ClusterComparisonResponse is the API response for Git vs ArgoCD comparison.
type ClusterComparisonResponse struct {
	Cluster Cluster `json:"cluster"`

	// Git summary
	GitTotalAddons    int `json:"git_total_addons"`
	GitEnabledAddons  int `json:"git_enabled_addons"`
	GitDisabledAddons int `json:"git_disabled_addons"`

	// ArgoCD summary
	ArgocdTotalApplications      int `json:"argocd_total_applications"`
	ArgocdHealthyApplications    int `json:"argocd_healthy_applications"`
	ArgocdSyncedApplications     int `json:"argocd_synced_applications"`
	ArgocdDegradedApplications   int `json:"argocd_degraded_applications"`
	ArgocdOutOfSyncApplications  int `json:"argocd_out_of_sync_applications"`

	// Per-addon comparison
	AddonComparisons []AddonComparisonStatus `json:"addon_comparisons"`

	// Overall totals
	TotalHealthy            int `json:"total_healthy"`
	TotalWithIssues         int `json:"total_with_issues"`
	TotalMissingInArgocd    int `json:"total_missing_in_argocd"`
	TotalUntrackedInArgocd  int `json:"total_untracked_in_argocd"`
	TotalDisabledInGit      int `json:"total_disabled_in_git"`

	ClusterConnectionState string `json:"cluster_connection_state,omitempty"`
}

// ClusterValuesResponse is the API response for raw cluster values YAML.
type ClusterValuesResponse struct {
	ClusterName string `json:"cluster_name"`
	ValuesYAML  string `json:"values_yaml"`
}

// ConfigDiffEntry holds the diff between global defaults and cluster overrides for one addon.
type ConfigDiffEntry struct {
	AddonName     string `json:"addon_name"`
	HasOverrides  bool   `json:"has_overrides"`
	GlobalValues  string `json:"global_values"`
	ClusterValues string `json:"cluster_values"`
}

// ConfigDiffResponse is the API response for config diff between cluster values and global defaults.
type ConfigDiffResponse struct {
	ClusterName  string                 `json:"cluster_name"`
	GlobalValues map[string]interface{} `json:"global_values,omitempty"`
	AddonDiffs   []ConfigDiffEntry      `json:"addon_diffs"`
}
