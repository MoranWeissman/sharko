package models

// DashboardConnectionStats holds connection statistics.
type DashboardConnectionStats struct {
	Total  int    `json:"total"`
	Active string `json:"active"`
}

// DashboardClusterStats holds cluster statistics.
type DashboardClusterStats struct {
	Total                  int `json:"total"`
	ConnectedToArgocd      int `json:"connected_to_argocd"`
	DisconnectedFromArgocd int `json:"disconnected_from_argocd"`
}

// DashboardSyncStatusStats holds sync status breakdown.
type DashboardSyncStatusStats struct {
	Synced    int `json:"synced"`
	OutOfSync int `json:"out_of_sync"`
	Unknown   int `json:"unknown"`
}

// DashboardHealthStatusStats holds health status breakdown.
type DashboardHealthStatusStats struct {
	Healthy     int `json:"healthy"`
	Progressing int `json:"progressing"`
	Degraded    int `json:"degraded"`
	Unknown     int `json:"unknown"`
}

// DashboardApplicationStats holds application statistics.
type DashboardApplicationStats struct {
	Total          int                        `json:"total"`
	BySyncStatus   DashboardSyncStatusStats   `json:"by_sync_status"`
	ByHealthStatus DashboardHealthStatusStats `json:"by_health_status"`
}

// DashboardAddonStats holds addon statistics.
type DashboardAddonStats struct {
	TotalAvailable     int `json:"total_available"`
	TotalDeployments   int `json:"total_deployments"`
	EnabledDeployments int `json:"enabled_deployments"`
}

// DashboardStatisticsResponse is the API response for dashboard stats.
type DashboardStatisticsResponse struct {
	Connections  DashboardConnectionStats  `json:"connections"`
	Clusters     DashboardClusterStats     `json:"clusters"`
	Applications DashboardApplicationStats `json:"applications"`
	Addons       DashboardAddonStats       `json:"addons"`
}

// PullRequest represents a PR from the Git provider.
type PullRequest struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author"`
	Status      string `json:"status"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	URL         string `json:"url"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	ClosedAt    string `json:"closed_at,omitempty"`
}

// DashboardPullRequestsResponse is the API response for PR listing.
type DashboardPullRequestsResponse struct {
	ActivePRs    []PullRequest `json:"active_prs"`
	CompletedPRs []PullRequest `json:"completed_prs"`
}
