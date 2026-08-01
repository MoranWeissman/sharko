package models

// DashboardConnectionStats holds connection statistics.
type DashboardConnectionStats struct {
	Total  int    `json:"total"`
	Active string `json:"active"`
}

// DashboardClusterStats holds the cluster connection breakdown, using the
// SAME five-state vocabulary as ui/src/lib/clusterStatus.ts
// (connected/pending/untested/missing/failed — "unmanaged" is not
// applicable here, this endpoint only counts Git-registered clusters).
// This replaces the old binary connected_to_argocd/disconnected_from_argocd
// pair (dashboard UX review 2026-08-01, blocker B1): that pair called a
// brand-new, zero-addon cluster "disconnected" forever, because ArgoCD
// never probes a cluster with nothing scheduled on it. Format v4 is
// unreleased, so this is a clean break rather than an additive field —
// there is no released client depending on the old shape.
//
//   - Connected: ArgoCD has probed the cluster and reports it healthy
//     (ConnectionState "Successful" or "Connected").
//   - Pending: ArgoCD has not yet reported a probe result
//     (ConnectionState "" or "Unknown"), AND the cluster has at least one
//     enabled addon — a probe is coming.
//   - Untested: same "" / "Unknown" state, but the cluster has ZERO
//     enabled addons, so ArgoCD has nothing to probe and never will until
//     an addon is enabled. Distinct from Pending so a freshly-registered,
//     addon-less cluster does not read as "connecting" forever.
//   - Missing: ArgoCD has no connection entry for this cluster at all (no
//     cluster secret) — mirrors ClusterService's "missing" status.
//   - Failed: ArgoCD observed an explicit failure (ConnectionState
//     "Failed" or anything else not covered above).
type DashboardClusterStats struct {
	Total     int `json:"total"`
	Connected int `json:"connected"`
	Pending   int `json:"pending"`
	Untested  int `json:"untested"`
	Missing   int `json:"missing"`
	Failed    int `json:"failed"`
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

// DashboardAddonStats holds addon statistics. TotalDeployments (the old
// "N/N" fake ratio — always equal to EnabledDeployments because it counted
// Git intent on both sides, dashboard UX review 2026-08-01 finding H5) is
// gone: EnabledDeployments is now a plain count, not a fraction's
// numerator. Clean break — format v4 is unreleased.
type DashboardAddonStats struct {
	TotalAvailable     int `json:"total_available"`
	EnabledDeployments int `json:"enabled_deployments"`
}

// DashboardStatisticsResponse is the API response for dashboard stats.
type DashboardStatisticsResponse struct {
	Connections        DashboardConnectionStats  `json:"connections"`
	Clusters           DashboardClusterStats     `json:"clusters"`
	Applications       DashboardApplicationStats `json:"applications"`
	Addons             DashboardAddonStats       `json:"addons"`
	BootstrapAppHealth string                    `json:"bootstrap_app_health"`
	BootstrapAppSync   string                    `json:"bootstrap_app_sync"`
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
