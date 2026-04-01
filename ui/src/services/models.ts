export interface Cluster {
  name: string
  labels: Record<string, string>
  region?: string
  server_version?: string
  connection_status?: string
}

export interface ClusterHealthStats {
  total_in_git: number
  connected: number
  failed: number
  missing_from_argocd: number
  not_in_git: number
}

export interface ClustersResponse {
  clusters: Cluster[]
  health_stats?: ClusterHealthStats
}

export interface ClusterAddonInfo {
  addon_name: string
  chart: string
  repo_url: string
  current_version: string
  enabled: boolean
  namespace?: string
  environment_version?: string
  custom_version?: string
  has_version_override: boolean
  argocd_sync_status?: string
  argocd_health_status?: string
  argocd_version?: string
}

export interface ClusterDetailResponse {
  cluster: Cluster
  addons: ClusterAddonInfo[]
}

export interface AddonComparisonStatus {
  addon_name: string
  git_configured: boolean
  git_chart?: string
  git_repo_url?: string
  git_version?: string
  git_namespace?: string
  git_enabled: boolean
  environment_version?: string
  custom_version?: string
  has_version_override: boolean
  argocd_deployed: boolean
  argocd_application_name?: string
  argocd_sync_status?: string
  argocd_health_status?: string
  argocd_deployed_version?: string
  argocd_namespace?: string
  status?: string
  issues: string[]
}

export interface ClusterComparisonResponse {
  cluster: Cluster
  git_total_addons: number
  git_enabled_addons: number
  git_disabled_addons: number
  argocd_total_applications: number
  argocd_healthy_applications: number
  argocd_synced_applications: number
  argocd_degraded_applications: number
  argocd_out_of_sync_applications: number
  addon_comparisons: AddonComparisonStatus[]
  total_healthy: number
  total_with_issues: number
  total_missing_in_argocd: number
  total_untracked_in_argocd: number
  total_disabled_in_git: number
  cluster_connection_state?: string
}

export interface AddonDeploymentInfo {
  cluster_name: string
  cluster_environment?: string
  enabled: boolean
  configured_version?: string
  deployed_version?: string
  namespace?: string
  sync_status?: string
  health_status?: string
  application_name?: string
  status: string
}

export interface AddonCatalogItem {
  addon_name: string
  chart: string
  repo_url: string
  namespace?: string
  version: string
  in_migration?: boolean
  total_clusters: number
  enabled_clusters: number
  healthy_applications: number
  degraded_applications: number
  missing_applications: number
  applications: AddonDeploymentInfo[]
}

export interface AddonCatalogResponse {
  addons: AddonCatalogItem[]
  total_addons: number
  total_clusters: number
  addons_only_in_git: number
}

export interface AddonDetailResponse {
  addon: AddonCatalogItem
}

export interface ConnectionResponse {
  name: string
  description?: string
  git_provider: string
  git_repo_identifier: string
  git_token_masked: string
  argocd_server_url: string
  argocd_token_masked: string
  argocd_namespace: string
  is_default: boolean
  is_active: boolean
}

export interface ConnectionsListResponse {
  connections: ConnectionResponse[]
  active_connection?: string
}

export interface DashboardStats {
  connections: { total: number; active: string }
  clusters: { total: number; connected_to_argocd: number; disconnected_from_argocd: number }
  applications: {
    total: number
    by_sync_status: { synced: number; out_of_sync: number; unknown: number }
    by_health_status: { healthy: number; progressing: number; degraded: number; unknown: number }
  }
  addons: { total_available: number; total_deployments: number; enabled_deployments: number }
}

export interface PullRequest {
  id: number
  title: string
  description?: string
  author: string
  status: string
  source_branch: string
  target_branch: string
  url: string
  created_at: string
}

export interface PullRequestsResponse {
  active_prs: PullRequest[]
  completed_prs: PullRequest[]
}

export interface VersionMatrixCell {
  version: string
  health: string
  drift_from_catalog: boolean
}

export interface VersionMatrixRow {
  addon_name: string
  catalog_version: string
  chart: string
  cells: Record<string, VersionMatrixCell>
}

export interface VersionMatrixResponse {
  clusters: string[]
  addons: VersionMatrixRow[]
}

export interface ConfigDiffEntry {
  addon_name: string
  has_overrides: boolean
  global_values: string
  cluster_values: string
}

export interface ConfigDiffResponse {
  cluster_name: string
  global_values?: Record<string, unknown>
  addon_diffs: ConfigDiffEntry[]
}

export interface ControlPlaneInfo {
  argocd_version: string
  helm_version: string
  kubectl_version: string
  total_apps: number
  total_clusters: number
  connected_clusters: number
  health_summary: Record<string, number>
}

export interface SyncActivityEntry {
  timestamp: string
  duration: string
  duration_secs: number
  app_name: string
  addon_name: string
  cluster_name: string
  revision?: string
  status: string
}

export interface AddonClusterHealth {
  cluster_name: string
  health: string
  health_since?: string
  reconciled_at?: string
  last_deploy_time?: string
  last_sync_duration?: string
  resource_count: number
  healthy_resources: number
}

export interface AddonHealthDetail {
  addon_name: string
  total_clusters: number
  healthy_clusters: number
  degraded_clusters: number
  last_deploy_time?: string
  avg_sync_duration?: string
  avg_sync_secs: number
  clusters: AddonClusterHealth[]
}

export interface ResourceSummary {
  total_pods: number
  running_pods: number
  total_containers: number
  has_missing_limits: boolean
}

export interface ChildAppHealth {
  app_name: string
  cluster_name: string
  health: string
  sync_status: string
  reconciled_at?: string
  resource_summary: ResourceSummary
  missing_limits?: string[]
}

export interface AddonGroupHealth {
  addon_name: string
  total_apps: number
  health_counts: Record<string, number>
  child_apps: ChildAppHealth[]
}

export interface ResourceAlert {
  app_name: string
  cluster_name: string
  addon_name: string
  alert_type: string
  details: string
}

export interface ObservabilityOverviewResponse {
  control_plane: ControlPlaneInfo
  recent_syncs: SyncActivityEntry[]
  addon_health: AddonHealthDetail[]
  addon_groups: AddonGroupHealth[]
  resource_alerts: ResourceAlert[]
}

export interface DatadogNamespaceMetrics {
  namespace: string
  cpu_usage_nanocores: number
  cpu_usage_cores: number
  memory_usage_bytes: number
  memory_usage_mb: number
  running_pods: number
}

export interface AddonMetricsData {
  addon_name: string
  namespace: string
  cpu_usage_cores: number
  cpu_request_cores: number
  cpu_limit_cores: number
  mem_usage_mb: number
  mem_request_mb: number
  mem_limit_mb: number
  pod_count: number
}

export interface ClusterMetricsData {
  cluster_name: string
  addons: AddonMetricsData[]
}

export interface AIProviderInfo {
  id: string
  name: string
  configured: boolean
  model: string
}

export interface AIConfigResponse {
  current_provider: string
  available_providers: AIProviderInfo[]
}

export interface AvailableVersion {
  version: string
  app_version?: string
}

export interface AvailableVersionsResponse {
  addon_name: string
  chart: string
  repo_url: string
  versions: AvailableVersion[]
}

export interface ValueDiffEntry {
  path: string
  type: 'added' | 'removed' | 'changed'
  old_value?: string
  new_value?: string
}

export interface ConflictCheckEntry {
  path: string
  configured_value: string
  old_default: string
  new_default: string
  source: string
}

export interface UpgradeCheckResponse {
  addon_name: string
  chart: string
  current_version: string
  target_version: string
  total_changes: number
  added: ValueDiffEntry[]
  removed: ValueDiffEntry[]
  changed: ValueDiffEntry[]
  conflicts: ConflictCheckEntry[]
  release_notes?: string
}
