package models

// Cluster represents a Kubernetes cluster from the Git configuration.
type Cluster struct {
	Name             string            `json:"name" yaml:"name"`
	SecretPath       string            `json:"secret_path,omitempty" yaml:"secretPath,omitempty"`
	Labels           map[string]string `json:"labels" yaml:"labels"`
	Region           string            `json:"region,omitempty" yaml:"region,omitempty"`
	ServerVersion    string            `json:"server_version,omitempty"`
	ConnectionStatus string            `json:"connection_status,omitempty"`
	Managed          bool              `json:"managed"` // true if in cluster-addons.yaml
}

// ClusterHealthStats holds aggregated health statistics for the clusters overview.
type ClusterHealthStats struct {
	TotalInGit         int `json:"total_in_git"`
	Connected          int `json:"connected"`
	Failed             int `json:"failed"`
	MissingFromArgoCD  int `json:"missing_from_argocd"`
	NotInGit           int `json:"not_in_git"`
}

// PendingRegistration represents a cluster registration PR that has been
// opened but not yet merged. The cluster itself is NOT in
// managed-clusters.yaml (and may or may not yet be in ArgoCD), so it must be
// surfaced as a distinct lifecycle state — neither "managed" nor
// "discovered/not_in_git" — to avoid the V125-1.5 family of UX bugs
// (BUG-050..055) where a pending-PR cluster appeared as if it half-existed
// across multiple unrelated panels.
//
// V125-1.5: ClusterName/PRURL/Branch are populated from the GitHub provider's
// open-PRs list, filtered by the registration-PR title pattern emitted by
// the orchestrator (see internal/orchestrator/git_helpers.go's
// findOpenPRForCluster — same matching contract). OpenedAt is the upstream
// PR's createdAt timestamp (RFC3339 string from the provider).
type PendingRegistration struct {
	ClusterName string `json:"cluster_name"`
	PRURL       string `json:"pr_url"`
	Branch      string `json:"branch"`
	OpenedAt    string `json:"opened_at"`
}

// OrphanRegistration represents an ArgoCD cluster Secret that has NO
// corresponding entry in managed-clusters.yaml AND no open registration PR
// — i.e. a cluster Secret left behind in the live argocd ns after a
// registration PR was closed without merging (or after some other
// abandonment path). Surfaced as its own lifecycle state so the user has a
// recovery action ("delete the orphan Secret"); see V125-1-7 / BUG-058.
//
// Production diagnosis: internal/orchestrator/cluster.go:408's manual-mode
// register path falls through to a direct ArgoCD API RegisterCluster call
// when argoSecretManager is nil + PRAutoMerge is false, which writes the
// cluster Secret BEFORE the PR opens. Closing the PR without merging
// leaves that Secret behind. V125-1-5's pending-PR filter masked it while
// the PR was open; once closed, it surfaced in `not_in_git`. V125-1-8
// closes the bug class architecturally by deferring the ArgoCD register
// until post-PR-merge — this struct is the MVP unblock recovery surface.
//
// LastSeenAt is the response time of the orphan resolver call (i.e. "now"
// at API-handler time). The ArgoCD cluster Secret API exposes no stable
// creation timestamp, so this is a degraded approximation — it tells the
// user "as of this refresh, this orphan exists" rather than "this orphan
// has existed since X". Documented in the resolver in
// internal/api/clusters_orphans.go.
type OrphanRegistration struct {
	ClusterName string `json:"cluster_name"`
	ServerURL   string `json:"server_url"`
	LastSeenAt  string `json:"last_seen_at"`
}

// ClustersResponse is the API response for listing clusters.
//
// PendingRegistrations and OrphanRegistrations are always non-nil slices
// (default `[]`) — V125-1.4 hit a nil-array crash on the frontend's
// similar dry-run path; we do not repeat the lesson here. An empty slice
// means there are no matching items (or the underlying provider call
// degraded; see the handler for the V124-22 dignified-degrade pattern).
type ClustersResponse struct {
	Clusters             []Cluster             `json:"clusters"`
	HealthStats          *ClusterHealthStats   `json:"health_stats,omitempty"`
	PendingRegistrations []PendingRegistration `json:"pending_registrations"`
	// OrphanRegistrations: V125-1-7 / BUG-058. ArgoCD cluster Secrets that
	// have no managed-clusters.yaml entry AND no open registration PR. The
	// FE renders these in a dedicated "Cancelled / Orphan Registrations"
	// section with a per-row Delete cluster Secret button.
	OrphanRegistrations []OrphanRegistration `json:"orphan_registrations"`
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

	ClusterConnectionState  string `json:"cluster_connection_state,omitempty"`
	ArgocdConnectionStatus  string `json:"argocd_connection_status,omitempty"`  // e.g. "Successful", "Failed"
	ArgocdConnectionMessage string `json:"argocd_connection_message,omitempty"` // error details from ArgoCD
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
