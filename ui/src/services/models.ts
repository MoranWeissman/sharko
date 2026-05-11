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

export interface PendingRegistration {
  cluster_name: string
  pr_url: string
  branch: string
  opened_at: string
}

export interface ClustersResponse {
  clusters: Cluster[]
  health_stats?: ClusterHealthStats
  // V125-1.5: open cluster-registration PRs whose values-file changes
  // have NOT yet merged. Optional with `?` for defensive forward-compat —
  // an older server that pre-dates this field must not crash a newer FE.
  // The runtime code reads this defensively (`?? []`) at every callsite.
  pending_registrations?: PendingRegistration[]
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
  // V121-7.3 — global Settings toggle for "Annotate values on generate".
  // Reported false when AI is not configured. Default-true on first install
  // when AI is configured; the Save handler stamps the explicit value going
  // forward so subsequent reads are authoritative.
  annotate_on_seed?: boolean
}

// V121-7.1 / 7.4: secret-leak guard match (redacted, never carries the
// raw secret value).
export interface AISecretMatch {
  pattern: string
  field: string
  line: number
}

// V121-7.4: response from POST /addons/{name}/values/annotate when the
// secret-leak guard hard-blocks the LLM call. UI matches on `code` to
// render the dedicated banner.
export interface AIAnnotateBlockedResponse {
  code: string // "secret_detected_blocked"
  message: string
  matches: AISecretMatch[]
}

// V121-7.4: success body for the manual annotate endpoint.
export interface AnnotateAddonValuesResponse {
  pr_url?: string
  pr_id?: number
  branch?: string
  merged: boolean
  commit_sha?: string
  ai_skip_reason?: string
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

// --- Curated catalog (v1.21 Marketplace) ---
//
// Mirrors `internal/catalog.CatalogEntry` and the v1.21 catalog handlers.
// `security_score` may be the literal string `"unknown"` when ScoreValue.Known
// is false on the backend; the UI handles both shapes.

export type CatalogScore = number | 'unknown'

export type CatalogCategory =
  | 'security'
  | 'observability'
  | 'networking'
  | 'autoscaling'
  | 'gitops'
  | 'storage'
  | 'database'
  | 'backup'
  | 'chaos'
  | 'developer-tools'

export type CatalogCuratedBy =
  | 'cncf-graduated'
  | 'cncf-incubating'
  | 'cncf-sandbox'
  | 'aws-eks-blueprints'
  | 'azure-aks-addon'
  | 'gke-marketplace'
  | 'artifacthub-verified'
  | 'artifacthub-official'

export type CatalogSecurityTier = 'Strong' | 'Moderate' | 'Weak' | ''

/**
 * Optional cosign-keyless signature pointer on a CatalogEntry
 * (schema v1.1+; V123-2.1). Verified at load time by V123-2.2.
 */
export interface CatalogEntrySignature {
  bundle: string  // https URL to a Sigstore bundle file
}

export interface CatalogEntry {
  name: string
  description: string
  chart: string
  repo: string
  default_namespace: string
  default_sync_wave: number
  docs_url?: string
  homepage?: string
  source_url?: string
  maintainers: string[]
  license: string
  category: CatalogCategory
  curated_by: CatalogCuratedBy[]
  security_score?: CatalogScore
  security_score_updated?: string
  security_tier?: CatalogSecurityTier
  github_stars?: number
  min_kubernetes_version?: string
  deprecated?: boolean
  superseded_by?: string
  /**
   * V123-1.4: origin of this entry — "embedded" for the binary-shipped
   * catalog, or the full third-party catalog URL. Absent on older API
   * responses — treat missing as embedded for backwards compat.
   */
  source?: string
  /**
   * Optional cosign-keyless attestation (schema v1.1+; V123-2.1).
   * Present only when the entry was signed; absent on older catalogs.
   */
  signature?: CatalogEntrySignature
  /**
   * V123-2.2: post-load cosign-verification outcome. True only when the
   * entry had a valid `signature.bundle` whose Sigstore bundle verified
   * against the configured trust policy AND whose OIDC subject matched
   * a TrustPolicy.Identities regex. False for unsigned entries, fail-
   * closed defaults, mismatches, untrusted identities, and infra failures.
   * Computed on the backend; UI treats missing as `false` for forwards-compat.
   */
  verified?: boolean
  /**
   * V123-2.2: OIDC subject (cert SAN) of the verified signer when
   * `verified` is true. Empty/absent otherwise. Used by VerifiedBadge
   * (V123-2.4) for the "Verified — signed by <identity>" tooltip.
   */
  signature_identity?: string
}

/**
 * Response shape of GET /api/v1/catalog/sources + POST
 * /api/v1/catalog/sources/refresh. Mirrors internal/api.catalogSourceRecord
 * from the Go side (V123-1.5 / V123-1.6).
 */
export interface CatalogSourceRecord {
  url: string // "embedded" sentinel OR full third-party URL
  status: 'ok' | 'stale' | 'failed'
  last_fetched: string | null // RFC3339 or null
  entry_count: number
  verified: boolean
  issuer?: string
}

export interface CatalogListResponse {
  addons: CatalogEntry[]
  total: number
}

export interface CatalogVersionEntry {
  version: string
  app_version?: string
  created?: string
  prerelease: boolean
}

export interface CatalogVersionsResponse {
  addon: string
  chart: string
  repo: string
  versions: CatalogVersionEntry[]
  latest_stable?: string
  cached_at: string
}

// V121-4 — Paste Helm URL validator. The handler returns 200 in both the happy
// and the structured-failure path; UI keys off `valid` and `error_code`.
export type CatalogValidateErrorCode =
  | 'invalid_input'
  | 'repo_unreachable'
  | 'index_parse_error'
  | 'chart_not_found'
  | 'timeout'
  | 'ssrf_blocked'

export interface CatalogValidateResponse {
  valid: boolean
  chart: string
  repo: string
  description?: string
  icon_url?: string
  versions?: CatalogVersionEntry[]
  latest_stable?: string
  cached_at?: string
  error_code?: CatalogValidateErrorCode
  message?: string
}

/**
 * v1.21 QA Bundle 1 — listing of all chart names in a Helm repo's index.yaml.
 * Returned by `GET /api/v1/catalog/repo-charts`. Used by the manual "Add
 * Addon" form to populate a chart-name dropdown after the operator
 * validates a repo URL. Same `valid` + `error_code` envelope as
 * /catalog/validate so the UI can reuse its existing switch table.
 */
export interface CatalogRepoChartsResponse {
  valid: boolean
  repo: string
  charts?: string[]
  cached_at?: string
  error_code?: CatalogValidateErrorCode
  message?: string
}

/** Filter shape used by the Marketplace Browse tab. AND semantics across keys. */
export interface CatalogListFilters {
  q?: string
  category?: CatalogCategory[]
  curated_by?: CatalogCuratedBy[]
  license?: string[]
  /**
   * Coarse OpenSSF tier the user picked in the sidebar. The backend takes a
   * numeric `min_score`; the UI maps tier → numeric here.
   */
  min_score?: number
  /** When true, entries with `security_score: "unknown"` stay visible. */
  include_unknown_score?: boolean
}

// --- ArtifactHub proxy (V121-3 Search tab) ---
//
// Mirrors the slimmed shapes the backend returns. We deliberately keep these
// types narrow — the proxy hands us only the fields the UI renders, so the TS
// definitions match.

export interface ArtifactHubRepo {
  repository_id?: string
  kind: number
  name: string
  display_name?: string
  url?: string
  organization_name?: string
  user_alias?: string
  verified_publisher?: boolean
  official?: boolean
}

export interface ArtifactHubSearchResult {
  package_id: string
  name: string
  normalized_name?: string
  display_name?: string
  description?: string
  logo_image_id?: string
  version?: string
  app_version?: string
  stars?: number
  repository: ArtifactHubRepo
}

export interface ArtifactHubMaintainer {
  name?: string
  email?: string
}

export interface ArtifactHubLink {
  name?: string
  url?: string
}

export interface ArtifactHubVersionMeta {
  version: string
  ts?: number
  prerelease?: boolean
}

export interface ArtifactHubPackage {
  package_id: string
  name: string
  normalized_name?: string
  display_name?: string
  description?: string
  home_url?: string
  readme?: string
  version?: string
  app_version?: string
  license?: string
  stars?: number
  maintainers?: ArtifactHubMaintainer[]
  repository: ArtifactHubRepo
  available_versions?: ArtifactHubVersionMeta[]
  links?: ArtifactHubLink[]
  keywords?: string[]
}

export interface CatalogSearchResponse {
  query: string
  curated: CatalogEntry[]
  artifacthub: ArtifactHubSearchResult[]
  /**
   * Set when the upstream ArtifactHub call failed. Classification: rate_limited
   * | server_error | timeout | not_found | malformed | invalid_input | unknown.
   * Curated hits are still populated when this is set.
   */
  artifacthub_error?: string
  /** True when ArtifactHub hits came from the stale window (upstream failed). */
  stale?: boolean
  cached_at?: string
}

export interface CatalogRemotePackageResponse {
  package: ArtifactHubPackage | null
  stale?: boolean
  cached_at?: string
}

/**
 * v1.21 QA Bundle 2: README payload for a curated catalog addon. The backend
 * resolves the curated entry to an ArtifactHub package and returns the
 * README markdown. `readme: ""` means the chart was located but doesn't
 * ship a README — the UI renders an empty state, not an error.
 */
export interface CatalogReadmeResponse {
  readme: string
  /** Source of the README — "artifacthub" today; "fallback" reserved for
   *  v1.22's direct chart-tarball extractor. */
  source: string
  ah_repo?: string
  ah_chart?: string
  stale?: boolean
  cached_at?: string
}

export interface CatalogReprobeResponse {
  reachable: boolean
  last_error?: string
  probed_at: string
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
  /**
   * v1.21 (Story V121-6.5): present when the chart version pinned in
   * `addons-catalog.yaml` is ahead of the version stamped in the values
   * file's smart-values header. The Values tab renders a yellow refresh
   * banner. Absent on legacy files (no `# sharko: managed=true` header).
   */
  values_version_mismatch?: { catalog_version: string; values_version: string } | null
  /**
   * V121-7.4: header-derived AI annotation state. Both default-false on
   * legacy files. The Values tab uses these (with the global AI config
   * state) to render the "AI not configured" banner and the per-addon
   * opt-out toggle.
   */
  ai_annotated?: boolean
  ai_opt_out?: boolean
  /**
   * v1.21 Bundle 5: true when the current values file is wrapped under a
   * legacy `<addonName>:` (or `<chartName>:`) root key. Helm receives
   * this file directly via `valueFiles:` in the ApplicationSet template
   * and silently ignores everything nested under that root. The Values
   * tab renders a yellow migration banner with a "Migrate this file"
   * button when this is set.
   */
  legacy_wrap_detected?: boolean
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

/**
 * Response for GET /addons/{name}/values/recent-prs and the per-cluster
 * variant. Fed into the "Recent changes" panel beneath the values editor.
 */
export interface RecentPRsResponse {
  entries: RecentPRsEntry[]
  view_all_url?: string
  values_file: string
}

export interface RecentPRsEntry {
  pr_id: number
  title: string
  url: string
  author: string
  merged_at: string
}

/**
 * Response for POST /addons/{name}/values/preview-merge — v1.21 QA Bundle 4
 * Fix #4. Returns a candidate values body that adds NEW upstream keys to
 * the user's current file without touching keys the user already set.
 * Submitting goes through the existing PUT /addons/{name}/values endpoint.
 */
export interface PreviewMergeResponse {
  current: string
  merged: string
  diff_summary: PreviewMergeSummary
  upstream_version: string
}

export interface PreviewMergeSummary {
  new_keys: string[]
  preserved_user_keys: string[]
  no_op: boolean
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
  // V125-1-6: addon attribution surfaced for the per-row badge.
  addon?: string
  // V125-1-6: canonical operation enum — see internal/prtracker/types.go
  // for the full list. The dashboard PR-panel filter chips bucket
  // operations into Clusters / Addons / Init / AI on the FE side.
  operation: string
  user: string
  source: string
  created_at: string
  last_status: string
  last_polled_at: string
}

export interface TrackedPRsResponse {
  prs: TrackedPR[]
  // V125-1-6: server echoes the effective limit so the FE can render a
  // "View all on GitHub →" escape hatch when the response is at the cap.
  limit?: number
}

// --- Drift Alerts (Story 6.4) ---

export interface DriftAlert {
  id: string
  timestamp: string
  event: string // orphan_detected, orphan_deleted_after_grace_period, drift_detected
  resource: string
  status: 'pending' | 'resolved'
}

// V125-1.1: 'kubeconfig' is the inline-kubeconfig path enabled in v1.25.
// 'gke' / 'aks' remain disabled options surfaced as "coming soon" — no
// backend support yet (V125-1.x). The legacy 'generic' literal is kept
// in the union for backwards compatibility with any persisted UI state
// that might still reference it; the wizard no longer emits it.
export type ClusterProvider = 'eks' | 'gke' | 'aks' | 'generic' | 'kubeconfig'

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

// V125-1.4 (BUG-049): the Go DryRunResult struct serializes its slice
// fields as `effective_addons`, `files_to_write`, and `secrets_to_create`.
// All three are now `?: T[]` because some past payloads (V125-1.1
// kubeconfig path with no addons) and some future provider paths can
// return `null`/missing — the preview panel handles that with `?? []`
// guards. We keep the legacy `files` alias because the FE has been
// reading the wrong key since dry-run shipped (FE was always undefined
// in production); both are supported here so a server roll-forward to
// the corrected backend keeps the FE working without a coordinated
// deploy.
export interface DryRunResult {
  effective_addons?: string[]
  files_to_write?: DryRunFileEntry[]
  /** Legacy alias kept only for backwards compatibility with stale clients;
   * server emits `files_to_write`. The view component reads `files` via the
   * post-processing layer below. */
  files?: DryRunFileEntry[]
  pr_title: string
  secrets_to_create?: string[]
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
