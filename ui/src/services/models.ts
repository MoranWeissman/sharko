export interface Cluster {
  name: string
  labels: Record<string, string>
  region?: string
  secret_path?: string
  server_version?: string
  server_url?: string
  connection_status?: string
  managed?: boolean
  adopted?: boolean
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
  argocd_connection_status?: string
  argocd_connection_message?: string
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

export interface AddonSource {
  repoURL?: string
  path?: string
  chart?: string
  version?: string
  parameters?: Record<string, string>
  valueFiles?: string[]
}

export interface AddonCatalogItem {
  addon_name: string
  chart: string
  repo_url: string
  namespace?: string
  version: string
  total_clusters: number
  enabled_clusters: number
  healthy_applications: number
  degraded_applications: number
  missing_applications: number
  applications: AddonDeploymentInfo[]
  syncWave?: number
  selfHeal?: boolean
  syncOptions?: string[]
  additionalSources?: AddonSource[]
  ignoreDifferences?: Record<string, unknown>[]
  extraHelmValues?: Record<string, string>
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
  provider?: {
    type: string
    region?: string
    prefix?: string
  }
  gitops?: {
    base_branch?: string
    branch_prefix?: string
    commit_prefix?: string
    pr_auto_merge?: boolean
    host_cluster_name?: string
    default_addons?: string
  }
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
  bootstrap_app_health?: string
  bootstrap_app_sync?: string
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

// --- Audit & Diagnostics (Story 1.9) ---

export interface AuditEntry {
  id: string
  timestamp: string
  level: string
  event: string
  user: string
  action: string
  resource: string
  source: string
  result: string
  duration_ms: number
  error?: string
  request_id?: string
  detail?: string
  /**
   * Tier-aware attribution mode for the resulting Git commit (v1.20+):
   *  - "service"   service token, no user identity attached
   *  - "co_author" service token + Co-authored-by trailer for the user
   *  - "per_user"  per-user PAT — the user IS the commit author
   */
  attribution_mode?: 'service' | 'co_author' | 'per_user' | ''
  /**
   * Tier of the originating endpoint (v1.20+):
   *  - "tier1"     operational (cluster/addon/PR/connection ops)
   *  - "tier2"     configuration (catalog metadata, values)
   *  - "personal"  self-service on caller's own profile
   *  - "auth"      login/logout/hash
   *  - "webhook"   inbound webhook (no user identity)
   */
  tier?: 'tier1' | 'tier2' | 'personal' | 'auth' | 'webhook' | ''
}

/** Profile of the authenticated caller (GET /users/me). */
export interface MeResponse {
  username: string
  role: string
  has_github_token: boolean
}

/**
 * Response for GET /addons/{name}/values-schema.
 * `schema` is the parsed values.schema.json object when present (best-effort);
 * the editor falls back to plain YAML mode when it's null/undefined.
 */
export interface AddonValuesSchemaResponse {
  addon_name: string
  current_values: string
  schema?: Record<string, unknown> | null
}

/** Response for GET /clusters/{cluster}/addons/{name}/values. */
export interface ClusterAddonValuesResponse {
  cluster_name: string
  addon_name: string
  current_overrides: string
  schema?: Record<string, unknown> | null
}

/**
 * Response for the two PUT endpoints (global values + per-cluster overrides).
 * When `attribution_warning` is "no_per_user_pat", the UI should render the
 * AttributionNudge banner — the action succeeded but used the service token.
 */
export interface ValuesEditResult {
  // The orchestrator wraps results when there's an attribution warning, so the
  // PR fields can either be top-level (no warning) or nested under `result`.
  pr_url?: string
  pr_id?: number
  branch?: string
  merged?: boolean
  values_file?: string
  attribution_warning?: 'no_per_user_pat'
  result?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
    values_file?: string
  }
}

export interface PermCheck {
  permission: string
  passed: boolean
  error?: string
}

export interface Fix {
  description: string
  yaml: string
}

export interface DiagnosticReport {
  identity: string
  role_assumption: string
  namespace_access: PermCheck[]
  suggested_fixes: Fix[]
}

export interface VerifyStep {
  name: string
  status: 'pass' | 'fail' | 'skipped'
  detail?: string
}

export interface VerifyResult {
  success: boolean
  stage: string
  error_code?: string
  error_message?: string
  duration_ms: number
  server_version?: string
  steps?: VerifyStep[]
}

export interface APIToken {
  name: string
  role: string
  created_at: string
  expires_at?: string
  last_used_at?: string
  expiring_soon?: boolean
  expired?: boolean
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
  baseline_unavailable?: boolean
  baseline_note?: string
}

export interface RecommendationCard {
  label: string
  version: string
  has_security: boolean
  has_breaking: boolean
  cross_major: boolean
  advisory_summary?: string
  is_recommended: boolean
  reason?: string
}

export interface UpgradeRecommendations {
  current_version: string
  // Legacy fields (kept — backend still sends them; new UI doesn't use them)
  next_patch?: string
  next_minor?: string
  latest_stable?: string
  // New
  cards?: RecommendationCard[]
  recommended?: string
}

// --- Cluster Registration (Story 3.5) ---

// --- Cluster Adoption (Story 4.4) ---

export interface AdoptResult {
  cluster: string
  success: boolean
  error?: string
  pr_url?: string
  verification?: VerifyResult
}

export interface AdoptClustersResponse {
  results: AdoptResult[]
}

// --- Tracked PRs (Story 5.3) ---

export interface TrackedPR {
  pr_id: number
  pr_url: string
  pr_branch: string
  pr_title: string
  cluster?: string
  operation: string
  user: string
  source: string
  created_at: string
  last_status: string
  last_polled_at: string
}

export interface TrackedPRsResponse {
  prs: TrackedPR[]
}

// --- Drift Alerts (Story 6.4) ---

export interface DriftAlert {
  id: string
  timestamp: string
  event: string // orphan_detected, orphan_deleted_after_grace_period, drift_detected
  resource: string
  status: 'pending' | 'resolved'
}

export type ClusterProvider = 'eks' | 'gke' | 'aks' | 'generic'

export interface DiscoveredClusterItem {
  name: string
  region: string
  status?: string
  arn?: string
  version?: string
  already_managed?: boolean
}

export interface DiscoverClustersResponse {
  clusters: DiscoveredClusterItem[]
  errors?: string[]
}

export interface DryRunFileEntry {
  path: string
  action: 'create' | 'update'
}

export interface DryRunResult {
  effective_addons: string[]
  files: DryRunFileEntry[]
  pr_title: string
  secrets_to_create: string[]
}

export interface RegisterClusterResult {
  status: string
  pr_url?: string
  pull_request_url?: string
  merged?: boolean
  git?: {
    pr_url?: string
    merged?: boolean
  }
  dry_run?: DryRunResult
  errors?: string[]
  partial?: boolean
}
