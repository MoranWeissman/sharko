package models

// ControlPlaneInfo contains ArgoCD control plane metadata and summary stats.
type ControlPlaneInfo struct {
	ArgocdVersion     string         `json:"argocd_version"`
	HelmVersion       string         `json:"helm_version"`
	KubectlVersion    string         `json:"kubectl_version"`
	TotalApps         int            `json:"total_apps"`
	TotalClusters     int            `json:"total_clusters"`
	ConnectedClusters int            `json:"connected_clusters"`
	HealthSummary     map[string]int `json:"health_summary"`
}

// SyncActivityEntry represents a single sync/deploy event in the timeline.
type SyncActivityEntry struct {
	Timestamp    string  `json:"timestamp"`
	Duration     string  `json:"duration"`
	DurationSecs float64 `json:"duration_secs"`
	AppName      string  `json:"app_name"`
	AddonName    string  `json:"addon_name"`
	ClusterName  string  `json:"cluster_name"`
	Revision     string  `json:"revision,omitempty"`
	Status       string  `json:"status"`
}

// SyncActivityResponse wraps sync activity entries.
type SyncActivityResponse struct {
	Entries    []SyncActivityEntry `json:"entries"`
	TotalSyncs int                `json:"total_syncs"`
}

// AddonHealthDetail provides per-addon health aggregation across clusters.
type AddonHealthDetail struct {
	AddonName        string               `json:"addon_name"`
	TotalClusters    int                  `json:"total_clusters"`
	HealthyClusters  int                  `json:"healthy_clusters"`
	DegradedClusters int                  `json:"degraded_clusters"`
	LastDeployTime   string               `json:"last_deploy_time,omitempty"`
	AvgSyncDuration  string               `json:"avg_sync_duration,omitempty"`
	AvgSyncSecs      float64              `json:"avg_sync_secs"`
	Clusters         []AddonClusterHealth `json:"clusters"`
}

// AddonClusterHealth represents health details for an addon on a specific cluster.
type AddonClusterHealth struct {
	ClusterName      string        `json:"cluster_name"`
	Health           string        `json:"health"`
	HealthSince      string        `json:"health_since,omitempty"`
	ReconciledAt     string        `json:"reconciled_at,omitempty"`
	LastDeployTime   string        `json:"last_deploy_time,omitempty"`
	LastSyncDuration string        `json:"last_sync_duration,omitempty"`
	ResourceCount    int           `json:"resource_count"`
	HealthyResources int           `json:"healthy_resources"`
	Resources        []AppResource `json:"resources,omitempty"`
}

// AddonGroupHealth represents an addon (ApplicationSet) with all its child apps across clusters.
type AddonGroupHealth struct {
	AddonName    string           `json:"addon_name"`
	TotalApps    int              `json:"total_apps"`
	HealthCounts map[string]int   `json:"health_counts"`
	ChildApps    []ChildAppHealth `json:"child_apps"`
}

// ChildAppHealth represents one ArgoCD application (child of an ApplicationSet).
type ChildAppHealth struct {
	AppName         string          `json:"app_name"`
	ClusterName     string          `json:"cluster_name"`
	Health          string          `json:"health"`
	SyncStatus      string          `json:"sync_status"`
	ReconciledAt    string          `json:"reconciled_at,omitempty"`
	ResourceSummary ResourceSummary `json:"resource_summary"`
	MissingLimits   []string        `json:"missing_limits,omitempty"`
}

// ResourceSummary holds aggregated resource counts for an app.
type ResourceSummary struct {
	TotalPods        int  `json:"total_pods"`
	RunningPods      int  `json:"running_pods"`
	TotalContainers  int  `json:"total_containers"`
	HasMissingLimits bool `json:"has_missing_limits"`
}

// ResourceAlert represents a resource configuration issue.
type ResourceAlert struct {
	AppName     string `json:"app_name"`
	ClusterName string `json:"cluster_name"`
	AddonName   string `json:"addon_name"`
	AlertType   string `json:"alert_type"`
	Details     string `json:"details"`
}

// ObservabilityOverviewResponse is the top-level response for the observability dashboard.
type ObservabilityOverviewResponse struct {
	ControlPlane   ControlPlaneInfo    `json:"control_plane"`
	RecentSyncs    []SyncActivityEntry `json:"recent_syncs"`
	AddonHealth    []AddonHealthDetail `json:"addon_health"`
	AddonGroups    []AddonGroupHealth  `json:"addon_groups"`
	ResourceAlerts []ResourceAlert     `json:"resource_alerts"`
}
