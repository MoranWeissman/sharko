// ProviderType is generated from the backend factory in
// internal/providers/provider.go via cmd/gen-provider-types. Re-exported
// here so non-UI consumers (api.ts, ConnectionResponse, etc.) can refer
// to it without reaching across the @/generated/* path.
export type { ProviderType } from '@/generated/provider-types'
import type { ProviderType as _ProviderType } from '@/generated/provider-types'

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
  // Who owns this cluster's ArgoCD cluster secret (V2-cleanup-57.2):
  // absent/'' or 'sharko' = Sharko writes and rotates it (default);
  // 'user' = self-managed — the user created the secret by hand and
  // Sharko only syncs addon labels onto it.
  connection_managed_by?: string
  // Connectivity check fields (V2-cleanup-29/30)
  // connectivity_status values: 'verified_argocd' | 'verified_check' | 'check_pending' | 'check_failed' | ''
  connectivity_status?: string
  connectivity_detail?: string
  // Auto-derived reachability verdict (V2-cleanup-85.4) — computed fresh on
  // every read from live ArgoCD state, with NO manual "Test connection"
  // click required. Values: 'healthy' | 'reachable' | 'unknown'. Prefer
  // this over sharko_status below when answering "is this cluster OK".
  derived_health_status?: string
  // Sharko's auto-detected guess at whether THIS cluster looks like EKS
  // (V2-cleanup-88.1, design L11) — distinct from Sharko's OWN AWS identity
  // (see SystemCapabilitiesResponse below). One of 'eks' | 'unknown'.
  target_platform?: string
  // Whether Sharko currently has resolvable connection credentials for this
  // cluster (V2-cleanup-88.3 — lazy credentials). Registration never
  // requires credentials; this only matters once an addon that carries
  // addon secrets is enabled — false means the two-layer dialog's Layer 2
  // should show "add connection credentials" before that addon is picked,
  // instead of letting the enable call round-trip into a 422.
  addon_secrets_ready?: boolean
  // Sharko observability fields (V2-cleanup-27 folded in)
  sharko_status?: string
  last_test_at?: string   // RFC3339
  test_failing?: boolean
  test_error_code?: string
  // Most recent cluster-secret reconciler outcome for this cluster
  // (V2-cleanup-89.4) — ArgoCD shows a failed apply; before this, Sharko
  // showed nothing. Computed at read time from the reconciler's in-memory
  // per-cluster record; absent when the reconciler hasn't processed this
  // cluster on this server instance yet.
  last_reconcile?: ClusterLastReconcile
  // Where this cluster's credentials live, mirrored from the
  // managed-clusters.yaml entry (V2-cleanup-60.4): 'inline-kubeconfig'
  // (credentials were pasted at registration and live only in the ArgoCD
  // cluster Secret), 'secret-kubeconfig' / 'eks-token' (a secrets backend
  // holds them), or absent for records that predate the field. Used by the
  // V2-cleanup-89.6 migrate-nudge on ClusterDetail.
  creds_source?: string
  // The real "namespace/name" of the ArgoCD cluster Secret this cluster's
  // page is about (walk day 4 locks, S1) — e.g. "argocd/prod-eu". Absent
  // when no cluster-secret reconciler is wired in this deployment mode.
  managed_secret_name?: string
  // True when the live ArgoCD cluster Secret already carries the
  // app.kubernetes.io/managed-by=sharko label (walk day 4 locks, S2).
  // Absent/false means either the Secret is foreign (a real "Take
  // ownership" candidate) or Sharko could not check — the UI treats both
  // the same way (show the button) rather than hide it on a guess.
  already_managed_by_sharko?: boolean
}

// ClusterLastReconcileLabelDrift mirrors internal/models.ClusterLastReconcileLabelDrift
// (V3 G1 — drift detection). Only populated for Sharko-managed clusters when
// labels don't match; nil otherwise.
export interface ClusterLastReconcileLabelDrift {
  added?: string[]   // keys in git but not on cluster
  removed?: string[] // keys on cluster but not in git
  changed?: string[] // keys in both but values differ
}

// ClusterLastReconcile mirrors internal/models.ClusterLastReconcile
// (V2-cleanup-89.4). message is always set on 'failed' and 'skipped'; it is
// normally empty on 'succeeded' but CAN be set there too, when the
// reconciler detects a label fight (something outside Sharko re-applying
// conflicting labels) — do not assume a succeeded reconcile has no message.
//
// label_drift (V3 G1) carries the git-vs-live label comparison for
// Sharko-managed clusters; absent when labels are in sync or for self-managed
// connections.
export interface ClusterLastReconcile {
  time: string // RFC3339
  outcome: 'succeeded' | 'failed' | 'skipped'
  message?: string
  label_drift?: ClusterLastReconcileLabelDrift
}

// ClusterResyncResponse mirrors internal/models.ClusterResyncResponse
// (v4-8-5 — the drift view's "Re-sync now" action). Unlike reconcileCluster
// (async 202, fleet-wide pass), this is a synchronous 200 response scoped to
// one cluster: label_diff is the added/removed/changed/unchanged addon-label
// diff THIS resync applied, and message always confirms the self-heal
// setting was left alone.
export interface ClusterResyncLabelDiff {
  added?: string[]
  removed?: string[]
  changed?: string[]
  unchanged?: string[]
}

export interface ClusterResyncResponse {
  status: string
  cluster: string
  outcome: 'succeeded' | 'failed' | 'skipped'
  message: string
  label_diff: ClusterResyncLabelDiff
}

// SystemCapabilitiesResponse mirrors GET /api/v1/system/capabilities
// (V2-cleanup-88.1) — what Sharko has auto-detected about its own runtime.
// The two-layer registration dialog's Layer 1 (Identity) fetches this once
// when it opens and never asks the user to self-report what Sharko can
// already tell them.
export interface AWSIdentity {
  detected: boolean
  // One of 'pod-identity' | 'irsa' | 'chain' | 'none'.
  method: string
  identity_arn?: string
}

export interface SystemCapabilitiesResponse {
  aws: AWSIdentity
  // One of 'eks' | 'unknown'.
  hub_platform: string
}

// DoctorCheck / DoctorClusterResponse mirror POST
// /api/v1/clusters/{name}/doctor (V2-cleanup-88.4, V2-cleanup-89.5's fifth
// check, and the 'warn' status added by V2-cleanup-90.1) — the connection
// doctor's five real-attempt checks, each with a plain-English fix on
// failure or warning. Check IDs are stable — the UI keys copy/icons off
// them. 'warn' (V2-cleanup-90.1) is additive: currently only
// 'secret-ownership' ever returns it, for a soft-confidence foreign-owner
// signal (e.g. a plain Helm release label) that isn't certain enough to
// fail the connection outright.
export interface DoctorCheck {
  id: 'connection-credentials' | 'addon-secret-paths' | 'assume-role' | 'cluster-access' | 'secret-ownership'
  status: 'pass' | 'fail' | 'not-applicable' | 'warn'
  detail: string
  fix?: string
}

export interface DoctorClusterResponse {
  checks: DoctorCheck[]
  overall: 'pass' | 'fail' | 'partial'
}

// ── Brownfield takeover (v4 Wave 2, Epic 6) ───────────────────────────────
//
// Taking over a cluster ArgoCD already manages. The preflight is a pure
// read that can be run as often as you like; every write below refuses
// without an explicit confirmation.

export type TakeoverFindingStatus = 'ok' | 'warning' | 'blocked'

export type TakeoverFindingID =
  | 'secret-owner'
  | 'appset-deletion-safety'
  | 'cluster-applications'
  | 'name-collision'

// Every finding says what it means and what to do about it, in words a
// person who is not a developer can act on. The UI renders these strings
// verbatim — it never composes its own explanation from the status.
export interface TakeoverFinding {
  id: TakeoverFindingID
  title: string
  status: TakeoverFindingStatus
  detail: string
  what_it_means: string
  what_to_do: string
  application_sets?: string[]
  applications?: string[]
}

export interface TakeoverReport {
  cluster: string
  // ready is false when something must be fixed first.
  ready: boolean
  // needs_acknowledgement is true when at least one check is a warning.
  needs_acknowledgement: boolean
  summary: string
  findings: TakeoverFinding[]
  server?: string
  // legacy_labels are the previous owner's labels that will be carried
  // over. This is the list shown to the user before they confirm.
  legacy_labels?: Record<string, string>
  // legacy_labels_selected_by maps each of those label keys to the
  // ApplicationSets that pick clusters using it.
  legacy_labels_selected_by?: Record<string, string[]>
}

export interface TakeoverRequestBody {
  yes?: boolean
  dry_run?: boolean
  // acknowledged_findings names the warnings the user has read, by the
  // finding id shown on screen. The server re-runs the checks on this call
  // and 409s on any warning whose id is not in here — so a warning that
  // appeared after the user looked can never be covered by accident.
  acknowledged_findings?: string[]
  preserve_legacy_labels?: boolean
  region?: string
  auto_merge?: boolean
}

export interface TakeoverResponse {
  cluster: string
  status: 'success' | 'partial' | 'planned'
  server?: string
  preserved_labels?: Record<string, string>
  dropped_labels?: Record<string, string>
  secret_swapped: boolean
  already_owned?: boolean
  protection_repaired?: boolean
  git?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
  }
  dry_run?: DryRunResult
  preflight?: TakeoverReport
  warnings?: string[]
  message: string
}

export interface DropLegacyLabelsRequestBody {
  yes?: boolean
  dry_run?: boolean
  labels?: string[]
  // acknowledged_findings echoes back the warning_ids the dry run returned.
  acknowledged_findings?: string[]
}

export interface DropLegacyLabelsResponse {
  cluster: string
  status: 'success' | 'planned'
  removed?: string[]
  remaining?: string[]
  warnings?: string[]
  // warning_ids are the stable ids of those warnings, same order, to send
  // back in acknowledged_findings.
  warning_ids?: string[]
  message: string
}

export interface UnregisterConsequence {
  id: string
  title: string
  detail: string
  what_it_means: string
  severity: 'info' | 'warning'
}

export interface UnregisterConsequencesResponse {
  cluster: string
  summary: string
  confirmation_required: string
  consequences: UnregisterConsequence[]
}

// Server-wide connectivity probe mode (V2-cleanup-85.4). Controls whether
// Sharko deploys a transient connectivity-check ArgoCD app to newly
// registered, zero-addon clusters.
export type ProbeMode = 'check-app' | 'api-test'

export interface ProbeModeResponse {
  probe_mode: ProbeMode
}

// Server-wide admin kill switch for the "Paste a kubeconfig" registration
// path (V2-cleanup-89.6). Defaults to true (today's behavior, unchanged).
// When false, registration requests that actually supply inline kubeconfig
// bytes are rejected server-side, and the UI hides the paste option from
// the Register dialog's Connection source select.
export interface AllowInlineCredentialsResponse {
  allow_inline_credentials: boolean
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

// ArgoCD cluster Secret with no managed-clusters.yaml entry AND no open
// registration PR — typically a leftover from a manual-mode register PR
// that was closed without merging. Surfaced in its own "Cancelled / Orphan
// Registrations" section. last_seen_at is the resolver-call time on the
// BE (the ArgoCD cluster Secret API has no stable creation timestamp);
// see internal/api/clusters_orphans.go for the contract.
export interface OrphanRegistration {
  cluster_name: string
  server_url: string
  last_seen_at: string
}

export interface ClustersResponse {
  clusters: Cluster[]
  health_stats?: ClusterHealthStats
  // Open cluster-registration PRs whose values-file changes have NOT yet
  // merged. Optional with `?` for defensive forward-compat — runtime code
  // reads this with `?? []` at every callsite.
  pending_registrations?: PendingRegistration[]
  // ArgoCD cluster Secrets with no git entry and no open PR. Same
  // forward-compat contract as pending_registrations.
  orphan_registrations?: OrphanRegistration[]
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
  argocd_operation_state?: string
  /** First line of operationState.message when the operation is failing (capped at 300 chars). */
  argocd_operation_message?: string
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
  /**
   * Paired counts that drive the tile-level "Running on N/M clusters"
   * badge. Both default to 0 when the backend doesn't supply them — the
   * badge falls through to "Not deployed anywhere".
   *
   * deployed_cluster_count (N): clusters where the ArgoCD Application for
   * this addon is BOTH Synced AND Healthy.
   * total_target_cluster_count (M): clusters where the addon is labelled
   * enabled in managed-clusters.yaml.
   */
  deployed_cluster_count?: number
  total_target_cluster_count?: number
  applications: AddonDeploymentInfo[]
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
    // Generated union of accepted provider Type strings (mirrors
    // providers.New()'s switch arms). Typed instead of `string` so a
    // future hand-edit can't accidentally ship a value the backend factory
    // rejects.
    type: _ProviderType
    region?: string
    prefix?: string
  }
  addon_secret_provider?: {
    type: _ProviderType
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
  // Five-state cluster breakdown — same vocabulary as
  // ui/src/lib/clusterStatus.ts's ClusterConnectionKind (minus 'unmanaged',
  // which doesn't apply to this Git-registered-clusters-only count).
  // Replaces the old binary connected_to_argocd/disconnected_from_argocd
  // pair (dashboard UX review 2026-08-01, blocker B1 — that pair called a
  // brand-new, zero-addon cluster "disconnected" forever).
  clusters: {
    total: number
    connected: number
    pending: number
    untested: number
    missing: number
    failed: number
  }
  applications: {
    total: number
    by_sync_status: { synced: number; out_of_sync: number; unknown: number }
    by_health_status: { healthy: number; progressing: number; degraded: number; unknown: number }
  }
  // total_deployments (the old "N/N" fake ratio) is gone — enabled_deployments
  // is a plain count now (dashboard UX review 2026-08-01, finding H5).
  addons: { total_available: number; enabled_deployments: number }
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
  newest_available?: string
  last_checked?: string
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
  configured_clusters: number
  configured_clusters_available: boolean
  connected_clusters: number
  total_appsets: number
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

// ClusterChange — one entry in the durable per-cluster change log
// (GET /clusters/{name}/changes, V2-cleanup-84.1/84.2). One row per
// completed (merged or closed) pull request touching this cluster, newest
// first. `deploy_outcome` is computed fresh at read time from the addon's
// current ArgoCD health — it is never persisted, so it always reflects the
// live state, not the state at merge time.
export interface ClusterChange {
  operation: string
  addon?: string
  cluster: string
  pr_id: number
  pr_url: string
  opened_at: string
  completed_at: string
  status: string // 'merged' | 'closed'
  deploy_outcome: string // 'healthy' | 'failed' | 'unknown'
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
  // Global Settings toggle for "Annotate values on generate". Reported
  // false when AI is not configured. Default-true on first install when AI
  // is configured; the Save handler stamps the explicit value going
  // forward so subsequent reads are authoritative.
  annotate_on_seed?: boolean
}

// Secret-leak guard match (redacted, never carries the raw secret value).
export interface AISecretMatch {
  pattern: string
  field: string
  line: number
}

// Response from POST /addons/{name}/values/annotate when the secret-leak
// guard hard-blocks the LLM call. UI matches on `code` to render the
// dedicated banner.
export interface AIAnnotateBlockedResponse {
  code: string // "secret_detected_blocked"
  message: string
  matches: AISecretMatch[]
}

// Success body for the manual annotate endpoint.
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

// --- Curated catalog (Marketplace) ---
//
// Mirrors `internal/catalog.CatalogEntry` and the catalog handlers.
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
 * Optional cosign-keyless signature pointer on a CatalogEntry (schema
 * v1.1+). Verified at load time.
 */
export interface CatalogEntrySignature {
  bundle: string  // https URL to a Sigstore bundle file
}

/**
 * One Helm value a curated addon expects an operator to set, in plain
 * English (v4 wave 1 Story 3.1, "extended entries"). Mirrors
 * `internal/catalog.RequiredValue`.
 */
export interface CatalogRequiredValue {
  key: string
  description: string
}

/**
 * One secret a curated addon needs to run, in plain English. Mirrors
 * `internal/catalog.SecretRequirement`.
 */
export interface CatalogSecretRequirement {
  name: string
  description: string
}

export interface CatalogEntry {
  name: string
  description: string
  chart: string
  repo: string
  default_namespace: string
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
   * The addon's operational knowledge (v4 wave 1 Story 3.1): what a person
   * needs to know to actually run it. All three are optional — plenty of
   * addons need none of them.
   */
  required_values?: CatalogRequiredValue[]
  secrets?: CatalogSecretRequirement[]
  quirks?: string[]
  /**
   * Origin of this entry — "embedded" for the binary-shipped catalog, or
   * the full third-party catalog URL. Absent on older API responses —
   * treat missing as embedded for backwards compat.
   */
  source?: string
  /**
   * Optional cosign-keyless attestation. Present only when the entry was
   * signed; absent on older catalogs.
   */
  signature?: CatalogEntrySignature
  /**
   * Post-load cosign-verification outcome. True only when the entry had a
   * valid `signature.bundle` whose Sigstore bundle verified against the
   * configured trust policy AND whose OIDC subject matched a
   * TrustPolicy.Identities regex. False for unsigned entries, fail-closed
   * defaults, mismatches, untrusted identities, and infra failures.
   * Computed on the backend; UI treats missing as `false` for forwards-compat.
   */
  verified?: boolean
  /**
   * OIDC subject (cert SAN) of the verified signer when `verified` is
   * true. Used by VerifiedBadge for the "Verified — signed by <identity>"
   * tooltip.
   */
  signature_identity?: string
}

/**
 * Response shape of GET /api/v1/catalog/sources + POST
 * /api/v1/catalog/sources/refresh. Mirrors internal/api.catalogSourceRecord
 * from the Go side.
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
  /**
   * True when Sharko could not determine any versions — today that's an
   * oci:// registry needing credentials it doesn't have (v4 wave 1 Story
   * 3.3: graceful degrade). `versions`/`latest_stable` are empty in that
   * case. Render an "unknown" pill, never an error, when this is true.
   */
  version_check_unknown?: boolean
  /**
   * v4 wave 2.5 — a full plain-English sentence explaining why no version
   * list could be produced for this source (e.g. an org-added chart repo
   * the freshness scanner can't read). Empty/absent whenever a version
   * list exists. Render this sentence verbatim — never a made-up
   * "up to date" claim — wherever freshness/versions would otherwise show.
   */
  no_data_reason?: string
  /** What this snapshot covers — e.g. "curated" vs "catalog". Optional;
   *  only present on the wave-2.5 freshness-extended responses. */
  scope?: string
}

/**
 * v4 wave 1 Story 3.4 — catalog-wide version-freshness summary. Powers the
 * Marketplace Browse tab's "Last checked" header line (when Sharko last
 * ran its background freshness pass over the curated catalog), distinct
 * from the per-addon `cached_at` on CatalogVersionsResponse above.
 */
export interface CatalogFreshnessResponse {
  enabled: boolean
  interval_seconds?: number
  last_run?: string
  next_run?: string
  addons_checked: number
  /**
   * v4 wave 2.5 — how many of the org's OWN catalog.yaml entries the
   * freshness scanner covered in the last pass (distinct from
   * addons_checked, which counts the curated/Marketplace list). Extends
   * freshness to org-added charts per the catalog-approved-model design
   * (landmine 4) — absent on older backends.
   */
  catalog_addons_checked?: number
  engine_pin?: {
    last_checked?: string
    v4_repo: boolean
    upgrade_available: boolean
    message?: string
    error?: string
  }
}

/**
 * v4 wave 2.5 — "catalog = the approved list". `origin` distinguishes an
 * entry the Marketplace also knows about ("curated") from one the org
 * typed in by hand ("internal") — both are equally real, approved
 * entries; origin only affects whether the knowledge fields below are
 * filled in.
 */
export type CatalogOrigin = 'curated' | 'internal'

/**
 * One entry in the org's approved catalog (GET/POST /api/v1/catalog/addons
 * — reads/writes catalog.yaml ONLY; a fresh repo returns zero of these).
 * Distinct from `CatalogEntry` (the read-only Marketplace/curated
 * knowledge) — this is what your org actually allows. The deployment
 * fields (chart/repo_url/version/namespace/settings) are the full entry
 * copied at approval time — the repo alone tells the whole story.
 * Knowledge fields (description, docs_url, homepage, security_score,
 * required_values, quirks) are filled in only when the Marketplace
 * recognizes the name; leave them unrendered rather than inventing a
 * placeholder when they're absent.
 */
export interface CatalogAddon {
  name: string
  origin: CatalogOrigin
  chart?: string
  repo_url?: string
  version?: string
  namespace?: string
  settings?: Record<string, unknown>
  /** False when this entry can't actually be deployed yet — see
   *  `missing_fields` for what's needed (e.g. an internal entry someone
   *  hand-edited into catalog.yaml without a version). */
  deployable: boolean
  missing_fields?: string[]
  secrets?: CatalogSecretRequirement[]
  // Knowledge fields — Marketplace-sourced, present only when known.
  description?: string
  docs_url?: string
  homepage?: string
  security_score?: CatalogScore
  required_values?: CatalogRequiredValue[]
  quirks?: string[]
}

export interface CatalogAddonListResponse {
  addons: CatalogAddon[]
  total: number
}

/**
 * One addon to add in a POST /api/v1/catalog/addons request. `from_marketplace:
 * true` tells the server to resolve chart/repo_url/version/namespace from
 * the curated Marketplace entry — the caller only needs to supply `name`
 * (and optionally `version` to pin something other than latest). When
 * `from_marketplace` is false, chart/repo_url/version are required (the
 * "add your own chart" door).
 */
export interface AddToCatalogAddonInput {
  name: string
  from_marketplace: boolean
  version?: string
  repo_url?: string
  chart?: string
  namespace?: string
  settings?: Record<string, unknown>
  secrets?: unknown[]
}

/**
 * Request body for POST /api/v1/catalog/addons. One element in `addons` =
 * a single add; N elements = ONE batch pull request, never N. Adding
 * `enable_on_cluster` makes it the combo: one PR touching both
 * catalog.yaml and cluster-addons/<name>.yaml — and REQUIRES `yes: true` (the
 * same confirmation EnableAddonV4 asks for, because that half changes what
 * runs on a real cluster). A catalog-only add needs no confirmation.
 */
export interface AddToCatalogRequest {
  addons: AddToCatalogAddonInput[]
  enable_on_cluster?: string
  yes?: boolean
  dry_run?: boolean
  auto_merge?: boolean | null
}

/**
 * 201 response from POST /api/v1/catalog/addons. When `dry_run` was set on
 * the request, the server returns a preview under `dry_run` instead of
 * opening anything (added/enabled/pr_url are then empty). PR fields mirror
 * every other write endpoint (orchestrator.GitResult, embedded) — top-level
 * when no attribution warning fired, wrapped under `result` when one did.
 */
export interface AddToCatalogResult {
  added: string[]
  enabled: string[]
  cluster?: string
  pr_url?: string
  pr_id?: number
  branch?: string
  merged?: boolean
  commit_sha?: string
  attribution_warning?: 'no_per_user_pat'
  result?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
  }
  warnings?: string[]
  dry_run?: DryRunResult
  /**
   * Maps each added addon name to the version that actually landed in
   * catalog.yaml — the caller's own version, or the newest one Sharko
   * filled in for a from_marketplace entry sent with no version. Absent on
   * a dry-run response (nothing has been committed yet — see
   * DryRunResult.files_to_write for the pin in the diff).
   */
  resolved_versions?: Record<string, string>
}

// Paste Helm URL validator. The handler returns 200 in both the happy and
// structured-failure paths; UI keys off `valid` and `error_code`.
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
 * Listing of all chart names in a Helm repo's index.yaml. Returned by
 * `GET /api/v1/catalog/repo-charts`. Used by the manual "Add Addon" form
 * to populate a chart-name dropdown after the operator validates a repo
 * URL. Same `valid` + `error_code` envelope as /catalog/validate.
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

// --- ArtifactHub proxy (Search tab) ---
//
// Mirrors the slimmed shapes the backend returns. Types are deliberately
// narrow — the proxy hands us only the fields the UI renders.

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
 * README payload for a curated catalog addon. The backend resolves the
 * curated entry to an ArtifactHub package and returns the README
 * markdown. `readme: ""` means the chart was located but doesn't ship a
 * README — the UI renders an empty state, not an error.
 */
export interface CatalogReadmeResponse {
  readme: string
  /** Source of the README — "artifacthub" today; "fallback" reserved
   *  for a direct chart-tarball extractor. */
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
   * Tier-aware attribution mode for the resulting Git commit:
   *  - "service"   service token, no user identity attached
   *  - "co_author" service token + Co-authored-by trailer for the user
   *  - "per_user"  per-user PAT — the user IS the commit author
   */
  attribution_mode?: 'service' | 'co_author' | 'per_user' | ''
  /**
   * Tier of the originating endpoint:
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
   * Present when the chart version pinned in `addons-catalog.yaml` is
   * ahead of the version stamped in the values file's smart-values header.
   * The Values tab renders a yellow refresh banner. Absent on legacy files
   * (no `# sharko: managed=true` header).
   */
  values_version_mismatch?: { catalog_version: string; values_version: string } | null
  /**
   * Header-derived AI annotation state. Both default-false on legacy
   * files. The Values tab uses these (with the global AI config state) to
   * render the "AI not configured" banner and the per-addon opt-out toggle.
   */
  ai_annotated?: boolean
  ai_opt_out?: boolean
  /**
   * True when the current values file is wrapped under a legacy
   * `<addonName>:` (or `<chartName>:`) root key. Helm receives this file
   * directly via `valueFiles:` in the ApplicationSet template and silently
   * ignores everything nested under that root. The Values tab renders a
   * yellow migration banner with a "Migrate this file" button when set.
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
 * Response for POST /addons/{name}/values/preview-merge. Returns a
 * candidate values body that adds NEW upstream keys to the user's current
 * file without touching keys the user already set. Submitting goes through
 * the existing PUT /addons/{name}/values endpoint.
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
  /** Null for tokens stored before expiry dates existed. Those keep working. */
  expires_at?: string | null
  last_used_at?: string
  /** 'active' | 'expired' | 'legacy-no-expiry' */
  status?: string
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

// --- Cluster Adoption ---

export interface AdoptResult {
  name: string
  status: 'success' | 'partial' | 'failed' | 'skipped'
  error?: string
  git?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
    commit_sha?: string
    values_file?: string
  }
  verification?: VerifyResult
  // Plain-English advisories that do NOT fail the adoption — e.g. this
  // cluster's ArgoCD cluster secret turning out to be rendered by another
  // ArgoCD Application (V2-cleanup-89.5).
  warnings?: string[]
  // Preview returned when dry_run is true
  preview?: DryRunResult
}

export interface AdoptClustersResponse {
  results: AdoptResult[]
}

// --- Tracked PRs ---

export interface TrackedPR {
  pr_id: number
  pr_url: string
  pr_branch: string
  pr_title: string
  cluster?: string
  // Addon attribution surfaced for the per-row badge.
  addon?: string
  // Canonical operation enum — see internal/prtracker/types.go for the
  // full list. The dashboard PR-panel filter chips bucket operations into
  // Clusters / Addons / Init / AI on the FE side.
  operation: string
  user: string
  source: string
  created_at: string
  last_status: string
  last_polled_at: string
}

export interface TrackedPRsResponse {
  prs: TrackedPR[]
  // Server echoes the effective limit so the FE can render a "View all on
  // GitHub →" escape hatch when the response is at the cap.
  limit?: number
}

// --- Drift Alerts ---

export interface DriftAlert {
  id: string
  timestamp: string
  event: string // orphan_detected, orphan_deleted_after_grace_period, drift_detected
  resource: string
  status: 'pending' | 'resolved'
}

// 'kubeconfig' is the inline-kubeconfig provider path. 'gke' / 'aks' are
// kept in the type union for backwards compatibility with persisted UI
// state, but are not surfaced as selectable options anywhere in the UI —
// no backend support exists for them. 'generic' is likewise kept for
// backwards compatibility; the wizard no longer emits it.
//
// As of the creds-reframe (creds-reframe-2), the registration dialog no
// longer asks "which platform?" first — it asks "how should Sharko get
// this cluster's credentials?" (see CredsSource below). `provider` is kept
// as optional cluster-type metadata and is sent alongside `creds_source`
// so anything that still reads `provider` keeps working; the backend keys
// on the effective creds source.
export type ClusterProvider = 'eks' | 'gke' | 'aks' | 'generic' | 'kubeconfig'

// CredsSource is the primary question the Register New Cluster dialog asks:
// "How should Sharko get this cluster's credentials?" It maps 1:1 to the
// backend's `creds_source` field (locked in creds-reframe story 1). When
// set, it WINS over the legacy `provider` field for edge-validation,
// audit-event split, and PR-title hints.
//
//   - 'inline-kubeconfig'  → user pastes a kubeconfig YAML inline.
//   - 'secret-kubeconfig'  → Sharko reads the kubeconfig from a named
//                            secret in the configured backend.
//   - 'eks-token'          → Sharko generates a token from cloud identity
//                            (EKS / IRSA) using region + role ARN.
export type CredsSource = 'inline-kubeconfig' | 'secret-kubeconfig' | 'eks-token'

export interface DryRunFileEntry {
  path: string
  action: 'create' | 'update' | 'delete'
  diff?: string
}

// The Go DryRunResult struct serializes its slice fields as
// `effective_addons`, `files_to_write`, and `secrets_to_create`. All
// three are `?: T[]` because some payloads return null/missing — the
// preview panel handles that with `?? []` guards. The legacy `files`
// alias is kept because the FE historically read the wrong key; both are
// supported here so a backend roll-forward keeps the FE working without
// a coordinated deploy.
export interface DryRunResult {
  effective_addons?: string[]
  files_to_write?: DryRunFileEntry[]
  /** Legacy alias kept only for backwards compatibility with stale clients;
   * server emits `files_to_write`. The view component reads `files` via the
   * post-processing layer below. */
  files?: DryRunFileEntry[]
  pr_title: string
  secrets_to_create?: string[]
  verification?: VerifyResult
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
  // Plain-English advisories that do NOT fail the operation — e.g. a
  // self-managed connection's ArgoCD cluster secret turning out to be
  // rendered by another ArgoCD Application (V2-cleanup-89.5).
  warnings?: string[]
}

/**
 * V4GitResult — mirrors the Go orchestrator.GitResult struct returned by
 * the v4-format addon endpoints (POST/DELETE
 * /api/v1/v4/clusters/{name}/addons/{addon} — v4 Wave 1 Story 4.3). Same
 * PR fields every write endpoint returns (pr_url/pr_id/merged/branch),
 * so PRResultBanner/extractPR (PRFeedback.tsx) read it directly with no
 * adapter — plus dry_run, which carries the SAME DryRunResult shape as
 * every other preview-capable write, so DryRunPreview also reads it
 * directly.
 */
export interface V4GitResult {
  pr_url?: string
  pr_id?: number
  branch?: string
  merged?: boolean
  commit_sha?: string
  dry_run?: DryRunResult
  // Plain-English advisories that do NOT block the operation — e.g. a
  // needed-at-runtime secret (v4 wave 2 w2-q4): the addon installs fine
  // now, but will need the secret later. Present on both the dry-run
  // preview response and the real (non-dry-run) response.
  warnings?: string[]
}

/**
 * V4AddonValidationErrorBody — the JSON body of a 422 from
 * POST/DELETE /api/v1/v4/clusters/{name}/addons/{addon}. Two shapes share
 * this type (v4 wave 2.5 review fix round, B-2):
 *
 *   - `*orchestrator.V4SemanticValidationError` (code `incomplete_entry` or
 *     `validation_failed`) — `problems` is non-empty, plain English, one
 *     sentence per missing thing, plus `cluster`/`addon`.
 *   - a plain coded body (code `not_in_catalog` or `empty_catalog_file`) —
 *     `error` + `code` only, no `problems`/`cluster`/`addon`.
 *
 * `code` is the machine-readable field callers branch on; the message text
 * changes and must never be pattern-matched (that was review finding B-2 —
 * the catalog-gate combo used to fire on the word "catalog" anywhere in the
 * message, which caught the wrong 422s and missed the real one).
 */
export interface V4AddonValidationErrorBody {
  error: string
  code?: string
  cluster?: string
  addon?: string
  problems?: string[]
}

// ─── v3 → v4 repo migration (v4 Wave 2, Epic 5 backend / migration-ui) ─────
//
// Three endpoints, in the order a person uses them:
//   GET  /api/v1/migration/status   — is there anything to migrate?
//   POST /api/v1/migration/preview  — show me every file it would touch
//   POST /api/v1/migration/migrate  — do it, one pull request, all or nothing

/** Response for GET /api/v1/migration/status. */
export interface MigrationStatus {
  /** "v3", "v4", or "empty". */
  format: 'v3' | 'v4' | 'empty'
  /** True only for "v3" — the one state with something to convert. */
  migration_available: boolean
  /** Plain-English sentence the UI can render as-is. */
  message: string
  /** Set (format "v3" only) when a previous migrate call already opened a
   * pull request that is still open — server truth, so a remounted banner
   * learns this from the next status poll instead of trusting component
   * state that a remount would have wiped. */
  migration_pr_url?: string
  migration_pr_number?: number
  /** Where the ArgoCD side of the migration has got to (v4 Wave 2 review
   * finding B-1/H-2). The ApplicationSets that keep a fleet's addons
   * running live in ArgoCD, not in the repo, so moving the files across is
   * only half the job. Set on v4 repos. */
  handoff?: RuntimeHandoffReport
}

/** What the ArgoCD half of a migration did, in plain words. */
export interface RuntimeHandoffReport {
  state: 'not_needed' | 'prepared' | 'pending' | 'complete' | 'skipped'
  /** One sentence to render as-is. */
  message: string
  /** The old ApplicationSets this handoff prepared, or retired. */
  application_sets?: string[]
  /** Applications whose delete-everything marker was removed, so their
   * workloads outlive the transition. */
  released_applications?: string[]
  /** Whether engine/application.yaml has been handed to ArgoCD. */
  engine_applied: boolean
}

/** One file the migration pull request would add, convert, or remove. */
export interface MigrationFileChange {
  path: string
  from_path?: string
  action: 'add' | 'convert' | 'remove'
  /** Rendered body for adds/conversions, redacted like every other preview. */
  content?: string
}

/** Response for POST /api/v1/migration/preview, and the `plan` field on migrate. */
export interface MigrationPlan {
  format: string
  add: MigrationFileChange[]
  convert: MigrationFileChange[]
  remove: MigrationFileChange[]
  /** Plain-English notes about anything that could not be carried across
   * (e.g. a v3 catalog `secrets:` block, which has no v4 home yet). */
  notes: string[]
  pr_title: string
}

/** Request body for POST /api/v1/migration/migrate. */
export interface MigrationMigrateRequest {
  dry_run?: boolean
  yes: boolean
  auto_merge?: boolean
  /** Leave unset and Sharko decides whether the ArgoCD side is needed.
   * "skip" migrates the files only — the escape hatch for a repo with
   * nothing actually running. */
  runtime_handoff?: 'skip'
}

/** Response for POST /api/v1/migration/migrate. */
export interface MigrateResult {
  /** "migrated", "preview", or "already_migrated". */
  status: 'migrated' | 'preview' | 'already_migrated'
  plan?: MigrationPlan
  git?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
  }
  /** What the ArgoCD half did before the pull request was opened. */
  handoff?: RuntimeHandoffReport
  /** Advisories that do NOT mean the migration failed — chiefly "the pull
   * request is open and correct, but auto-merge could not merge it". */
  warnings?: string[]
}

// One Kubernetes node, from GET /cluster/nodes (internal/api/nodes.go).
// S4 (walk day 4) only needs name + status, but the wire shape carries more.
export interface NodeInfo {
  name: string
  status: string // "Ready" or "NotReady"
  instance_type?: string
  architecture?: string
  os?: string
  capacity_cpu?: string
  capacity_memory?: string
  allocatable_cpu?: string
  allocatable_memory?: string
}

export interface NodeInfoResponse {
  nodes: NodeInfo[]
  total: number
  ready: number
  not_ready: number
  message?: string
}
