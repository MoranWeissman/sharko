import type {
  AddonCatalogResponse,
  AddonDetailResponse,
  AdoptClustersResponse,
  AIConfigResponse,
  APIToken,
  AuditEntry,
  AvailableVersionsResponse,
  ClusterComparisonResponse,
  ClusterDetailResponse,
  ClusterProvider,
  ClustersResponse,
  ConfigDiffResponse,
  ConnectionsListResponse,
  DashboardStats,
  DiagnosticReport,
  DiscoverClustersResponse,
  ObservabilityOverviewResponse,
  PullRequestsResponse,
  RegisterClusterResult,
  SyncActivityEntry,
  TrackedPRsResponse,
  UpgradeCheckResponse,
  UpgradeRecommendations,
  VerifyResult,
  VersionMatrixResponse,
} from './models'

const BASE_URL = '/api/v1'
const TOKEN_KEY = 'sharko-auth-token'

function authHeaders(): Record<string, string> {
  const token = sessionStorage.getItem(TOKEN_KEY)
  return token ? { Authorization: `Bearer ${token}` } : {}
}

async function fetchJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    headers: authHeaders(),
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

async function postJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

async function putJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

async function patchJSON<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

async function deleteJSON<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'DELETE',
    headers: authHeaders(),
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

async function fetchJSONMethod<T>(path: string, method: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE_URL}${path}`, {
    method,
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined,
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

// --- Write operations (Phase 7) ---

export async function registerCluster(data: {
  name: string;
  addons?: Record<string, boolean>;
  region?: string;
  secret_path?: string;
  provider?: ClusterProvider;
  role_arn?: string;
  auto_merge?: boolean;
  dry_run?: boolean;
}) {
  return postJSON<RegisterClusterResult>('/clusters', data)
}

export async function discoverEKSClusters(data: { role_arns: string[]; region?: string }) {
  return postJSON<DiscoverClustersResponse>('/clusters/discover', data)
}

export async function testClusterConnection(name: string) {
  return postJSON<VerifyResult & { reachable?: boolean; platform?: string; suggestions?: string[] }>(`/clusters/${encodeURIComponent(name)}/test`, {})
}

export async function diagnoseCluster(name: string) {
  return postJSON<DiagnosticReport>(`/clusters/${encodeURIComponent(name)}/diagnose`, {})
}

export async function fetchAuditLog(filters?: {
  user?: string
  action?: string
  source?: string
  result?: string
  cluster?: string
  since?: string
  limit?: number
}) {
  const params = new URLSearchParams()
  if (filters) {
    if (filters.user) params.set('user', filters.user)
    if (filters.action) params.set('action', filters.action)
    if (filters.source) params.set('source', filters.source)
    if (filters.result) params.set('result', filters.result)
    if (filters.cluster) params.set('cluster', filters.cluster)
    if (filters.since) params.set('since', filters.since)
    if (filters.limit) params.set('limit', String(filters.limit))
  }
  const qs = params.toString()
  return fetchJSON<{ entries: AuditEntry[] }>(`/audit${qs ? `?${qs}` : ''}`)
}

export function createAuditStream(): EventSource {
  const token = sessionStorage.getItem('sharko-auth-token')
  const url = `/api/v1/audit/stream${token ? `?token=${encodeURIComponent(token)}` : ''}`
  return new EventSource(url)
}

export async function deregisterCluster(name: string) {
  return deleteJSON<any>(`/clusters/${encodeURIComponent(name)}`)
}

export async function adoptClusters(data: { clusters: string[]; auto_merge?: boolean; dry_run?: boolean }) {
  return postJSON<AdoptClustersResponse>('/clusters/adopt', data)
}

export async function unadoptCluster(name: string) {
  return deleteJSON<{ status: string; pr_url?: string }>(`/clusters/${encodeURIComponent(name)}?unadopt=true`)
}

export async function updateClusterAddons(name: string, addons: Record<string, boolean>) {
  return patchJSON<any>(`/clusters/${encodeURIComponent(name)}`, { addons })
}

export async function updateClusterSettings(name: string, settings: { secret_path?: string }) {
  return patchJSON<any>(`/clusters/${encodeURIComponent(name)}`, settings)
}

export interface AddAddonResponse {
  // Top-level fields when no attribution warning fired (raw orchestrator result).
  pr_url?: string
  pr_id?: number
  branch?: string
  merged?: boolean
  attribution_warning?: 'no_per_user_pat'
  // Legacy alias surfaced by the raw form before v1.20 — kept on the type so
  // existing callers (AddonCatalog.tsx Add Addon dialog) compile cleanly.
  pull_request_url?: string
  // When attribution_warning is set, the orchestrator result is wrapped under `result`.
  result?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
  }
}

/**
 * Structured 409 body returned when the addon is already in the catalog.
 * The Marketplace Configure modal renders this inline (with a deep-link to
 * the existing addon page) instead of a generic toast. Backed by V121-5.1.
 */
export interface AddonAlreadyExistsError extends Error {
  code: 'addon_already_exists'
  status: 409
  addon: string
  existingUrl: string
}

export function isAddonAlreadyExistsError(e: unknown): e is AddonAlreadyExistsError {
  return (
    typeof e === 'object' &&
    e !== null &&
    (e as { code?: string }).code === 'addon_already_exists'
  )
}

export async function addAddon(data: {
  name: string
  chart: string
  repo_url: string
  version: string
  namespace?: string
  sync_wave?: number
  /**
   * V121-5.2 / V121-4.1: identifies the originating UI flow.
   *   "marketplace" — Browse curated card → Configure
   *   "artifacthub" — Search tab → Configure (external pkg)
   *   "paste_url"   — Paste Helm URL tab → Configure
   *   "manual"      — legacy direct "Add Addon" form
   */
  source?: 'marketplace' | 'artifacthub' | 'paste_url' | 'manual'
}): Promise<AddAddonResponse> {
  // Use raw fetch so we can detect the structured 409 body that V121-5.1
  // returns when the addon already exists in the catalog. postJSON throws a
  // plain Error with just `error` text and we'd lose `addon` / `existing_url`.
  const res = await fetch(`${BASE_URL}/addons`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(data),
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (res.status === 409) {
    const body = (await res.json().catch(() => ({}))) as {
      error?: string
      code?: string
      addon?: string
      existing_url?: string
    }
    const err = new Error(body.error || 'Addon already in catalog') as AddonAlreadyExistsError
    err.code = 'addon_already_exists'
    err.status = 409
    err.addon = body.addon || data.name
    err.existingUrl = body.existing_url || `/addons/${encodeURIComponent(data.name)}`
    throw err
  }
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error((body as { error?: string }).error || res.statusText)
  }
  return (await res.json()) as AddAddonResponse
}

export async function removeAddon(name: string) {
  return deleteJSON<any>(`/addons/${encodeURIComponent(name)}?confirm=true`)
}

export async function upgradeAddon(name: string, data: { version: string; cluster?: string }) {
  return postJSON<any>(`/addons/${encodeURIComponent(name)}/upgrade`, data)
}

export async function configureAddon(
  name: string,
  config: {
    version?: string
    sync_wave?: number
    self_heal?: boolean
    sync_options?: string[]
    extra_helm_values?: Record<string, string>
    ignore_differences?: Record<string, unknown>[]
    additional_sources?: Record<string, unknown>[]
  },
) {
  // NOTE: the backend handler is registered at PATCH /api/v1/addons/{name}
  // (see internal/api/router.go). The earlier implementation posted to
  // `.../configure` which 404s — fixed alongside the v1.20.1 catalog editor.
  return patchJSON<{
    status?: string
    pr_url?: string
    pr_id?: number
    pull_request_url?: string
    attribution_warning?: 'no_per_user_pat'
    result?: { pr_url?: string; pr_id?: number }
  }>(
    `/addons/${encodeURIComponent(name)}`,
    { name, ...config },
  )
}

export async function createToken(data: { name: string; role: string; expires?: string }) {
  return postJSON<any>('/tokens', data)
}

export async function listTokens() {
  return fetchJSON<APIToken[]>('/tokens')
}

export async function revokeToken(name: string) {
  return deleteJSON<any>(`/tokens/${encodeURIComponent(name)}`)
}

export async function batchRegisterClusters(clusters: Array<{ name: string; addons?: Record<string, boolean>; region?: string }>) {
  return postJSON<any>('/clusters/batch', { clusters })
}

export async function discoverClusters() {
  return fetchJSON<any>('/clusters/available')
}

export interface OperationStep {
  name: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'waiting'
  detail?: string
}

export interface OperationSession {
  id: string
  status: 'pending' | 'running' | 'completed' | 'failed' | 'cancelled' | 'waiting'
  steps: OperationStep[]
  wait_payload?: string
  wait_detail?: string
  error?: string
}

export async function initRepo(data?: { bootstrap_argocd?: boolean; auto_merge?: boolean }): Promise<{ operation_id: string } | any> {
  const res = await fetch(`${BASE_URL}/init`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body: JSON.stringify(data || { bootstrap_argocd: true }),
  })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

/**
 * V124-15 / BUG-033: typed error thrown by `getOperation` so the wizard can
 * distinguish a 401 (session expired — fatal, stop polling) from transient
 * network errors (swallow + retry).
 *
 * We intentionally do NOT call `window.location.reload()` from `getOperation`
 * (unlike most other helpers in this file). The wizard polls every 2s; a
 * silent reload mid-init would interrupt the in-progress operation display
 * before the user understands what happened, and a queued reload during a
 * 401 storm produces the "wizard appears frozen" symptom that BUG-033
 * documents. The wizard owns the error UX instead.
 *
 * Rest of the UI keeps its existing 401 → reload behavior — this typed
 * error is opt-in and only used by `getOperation` today. Generalizing the
 * pattern is V125+ scope.
 */
export class OperationApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'OperationApiError'
    this.status = status
  }
}

export function isUnauthorizedError(err: unknown): boolean {
  if (err instanceof OperationApiError) return err.status === 401
  if (err instanceof Error) {
    // Defense in depth: `getOperation` always throws OperationApiError, but
    // other layers (e.g. a fetch wrapper) might surface 401 as a plain
    // Error with the status in the message. Match the few common shapes.
    return /\b401\b|unauthorized|unauthenticated|session expired/i.test(
      err.message,
    )
  }
  return false
}

export async function getOperation(id: string): Promise<OperationSession> {
  const res = await fetch(`${BASE_URL}/operations/${id}`, { headers: authHeaders() })
  if (res.status === 401) {
    // Don't auto-reload — let the wizard surface a "Session expired"
    // error and provide a Log in again button. See OperationApiError above.
    throw new OperationApiError('Unauthorized: session expired', 401)
  }
  if (!res.ok) {
    throw new OperationApiError(`Failed to get operation (HTTP ${res.status})`, res.status)
  }
  return res.json()
}

export async function operationHeartbeat(id: string): Promise<void> {
  await fetch(`${BASE_URL}/operations/${id}/heartbeat`, {
    method: 'POST',
    headers: authHeaders(),
  })
}

// --- Tracked PRs (Story 5.3) ---

export async function fetchTrackedPRs(filters?: { status?: string; cluster?: string; user?: string }) {
  const params = new URLSearchParams()
  if (filters) {
    if (filters.status) params.set('status', filters.status)
    if (filters.cluster) params.set('cluster', filters.cluster)
    if (filters.user) params.set('user', filters.user)
  }
  const qs = params.toString()
  return fetchJSON<TrackedPRsResponse>(`/prs${qs ? `?${qs}` : ''}`)
}

export async function getAddonPRs(addonName: string) {
  return fetchJSON<TrackedPRsResponse>(`/prs?addon=${encodeURIComponent(addonName)}`)
}

export async function refreshPR(id: number) {
  return postJSON<{ status: string }>(`/prs/${id}/refresh`)
}

// ─── Merged PRs (v1.21 QA Bundle 3) ────────────────────────────────────────
//
// /api/v1/prs only returns OPEN PRs (the prtracker drops PRs once they merge).
// /api/v1/prs/merged goes back to the Git provider directly and lists merged
// PRs. Cached server-side for 60s. Used by the Dashboard Pending/Merged toggle.
export interface MergedPRItem {
  pr_id: number
  pr_url: string
  pr_title: string
  pr_branch: string
  description?: string
  author?: string
  cluster?: string
  addon?: string
  operation?: string
  created_at?: string
  merged_at?: string
}

export interface MergedPRsResponse {
  prs: MergedPRItem[]
  limit: number
}

export async function fetchMergedPRs(filters?: { cluster?: string; addon?: string; limit?: number }) {
  const params = new URLSearchParams()
  if (filters?.cluster) params.set('cluster', filters.cluster)
  if (filters?.addon) params.set('addon', filters.addon)
  if (filters?.limit) params.set('limit', String(filters.limit))
  const qs = params.toString()
  return fetchJSON<MergedPRsResponse>(`/prs/merged${qs ? `?${qs}` : ''}`)
}

export const api = {
  // Health
  health: () => fetchJSON<{ status: string }>('/health'),

  // Clusters
  getClusters: () => fetchJSON<ClustersResponse>('/clusters'),
  getDiscoveredClusters: () => fetchJSON<ClustersResponse>('/clusters?managed=false'),
  getCluster: (name: string) => fetchJSON<ClusterDetailResponse>(`/clusters/${name}`),
  getClusterComparison: (name: string) => fetchJSON<ClusterComparisonResponse>(`/clusters/${name}/comparison`),
  getClusterValues: (name: string) => fetchJSON<{ cluster_name: string; values_yaml: string }>(`/clusters/${name}/values`),
  getConfigDiff: (name: string) => fetchJSON<ConfigDiffResponse>(`/clusters/${name}/config-diff`),
  getClusterHistory: (name: string) => fetchJSON<{ cluster_name: string; history: SyncActivityEntry[] }>(`/clusters/${name}/history`),

  // Addons
  getAddonCatalog: () => fetchJSON<AddonCatalogResponse>('/addons/catalog'),
  getAddonDetail: (name: string) => fetchJSON<AddonDetailResponse>(`/addons/${name}`),
  getAddonValues: (name: string) => fetchJSON<{ addon_name: string; values_yaml: string }>(`/addons/${name}/values`),
  getVersionMatrix: () => fetchJSON<VersionMatrixResponse>('/addons/version-matrix'),

  // Dashboard
  getDashboardStats: () => fetchJSON<DashboardStats>('/dashboard/stats'),
  getPullRequests: () => fetchJSON<PullRequestsResponse>('/dashboard/pull-requests'),

  // Connections
  getConnections: () => fetchJSON<ConnectionsListResponse>('/connections/'),
  createConnection: (data: unknown) => postJSON('/connections/', data),
  updateConnection: (name: string, data: unknown) => putJSON(`/connections/${encodeURIComponent(name)}`, data),
  deleteConnection: (name: string) => deleteJSON(`/connections/${encodeURIComponent(name)}`),
  setActiveConnection: (name: string) => postJSON('/connections/active', { connection_name: name }),
  testConnection: () => postJSON<{ git: { status: string }; argocd: { status: string } }>('/connections/test'),
  // V124-19 / BUG-044: `data` may include `use_saved: true` along with `name`
  // to instruct the backend to fetch the named saved connection's stored
  // credentials and test with those (instead of the request body's tokens).
  // Unlocks the wizard's "leave blank to keep, or enter new value to replace"
  // contract end-to-end — Test Connection works on a blank token field when
  // a saved connection exists. Backend returns 400 if use_saved=true but no
  // matching saved connection.
  testCredentials: (data: unknown) => postJSON<{ git: { status: string; message?: string; auth?: string }; argocd: { status: string; message?: string; auth?: string } }>('/connections/test-credentials', data),
  discoverArgocd: (namespace?: string) => fetchJSON<{ server_url: string; has_env_token: boolean; namespace: string }>(`/connections/discover-argocd${namespace ? `?namespace=${namespace}` : ''}`),

  // Observability
  getObservability: () => fetchJSON<ObservabilityOverviewResponse>('/observability/overview'),

  // Upgrade
  getUpgradeVersions: (addonName: string) => fetchJSON<AvailableVersionsResponse>(`/upgrade/${addonName}/versions`),
  getUpgradeRecommendations: (addonName: string) => fetchJSON<UpgradeRecommendations>(`/upgrade/${addonName}/recommendations`),
  checkUpgrade: (addonName: string, targetVersion: string) => postJSON<UpgradeCheckResponse>('/upgrade/check', { addon_name: addonName, target_version: targetVersion }),
  getAddonChangelog: (name: string, from?: string, to?: string) => {
    const params = new URLSearchParams()
    if (from) params.set('from', from)
    if (to) params.set('to', to)
    return fetchJSON<{
      addon_name: string
      current_version: string
      target_version: string
      versions: { version: string; app_version: string; created: string; description: string }[]
      total_versions_between: number
    }>(`/addons/${name}/changelog?${params.toString()}`)
  },

  // AI
  getAIStatus: () => fetchJSON<{ enabled: boolean }>('/upgrade/ai-status'),
  getAISummary: (addonName: string, targetVersion: string) => postJSON<{ summary: string }>('/upgrade/ai-summary', { addon_name: addonName, target_version: targetVersion }),
  getAIConfig: () => fetchJSON<AIConfigResponse>('/ai/config'),
  saveAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string; annotate_on_seed?: boolean }) => postJSON<{ status: string }>('/ai/config', data),
  setAIProvider: (provider: string) => postJSON<{ status: string; provider: string }>('/ai/provider', { provider }),
  testAI: () => postJSON<{ status: string; response: string }>('/ai/test', {}),
  testAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string }) => postJSON<{ status: string; message?: string; response?: string }>('/ai/test-config', data),


  // Auth
  updatePassword: (currentPassword: string, newPassword: string) => postJSON<{ status: string }>('/auth/update-password', { current_password: currentPassword, new_password: newPassword }),

  // My account (v1.20 — tiered attribution)
  getMe: () => fetchJSON<import('./models').MeResponse>('/users/me'),
  setMyGitHubToken: (token: string) => putJSON<{ status: string; has_github_token: boolean }>('/users/me/github-token', { token }),
  clearMyGitHubToken: () => deleteJSON<{ status: string; has_github_token: boolean }>('/users/me/github-token'),
  testMyGitHubToken: () => postJSON<{ status: string; github_login: string }>('/users/me/github-token/test', {}),

  // Values editor (v1.20)
  getAddonValuesSchema: (addonName: string) =>
    fetchJSON<import('./models').AddonValuesSchemaResponse>(
      `/addons/${encodeURIComponent(addonName)}/values-schema`,
    ),
  setAddonValues: (addonName: string, valuesYAML: string) =>
    putJSON<import('./models').ValuesEditResult>(
      `/addons/${encodeURIComponent(addonName)}/values`,
      { values: valuesYAML },
    ),
  // V121-6.4: refresh-from-upstream uses the SAME endpoint as
  // setAddonValues. Backend ignores `values` when `refresh_from_upstream`
  // is true, fetches the chart's upstream values.yaml, runs the
  // smart-values pipeline, and overwrites the global file.
  refreshAddonValuesFromUpstream: (addonName: string) =>
    putJSON<import('./models').ValuesEditResult>(
      `/addons/${encodeURIComponent(addonName)}/values`,
      { values: '', refresh_from_upstream: true },
    ),
  // v1.21 QA Bundle 4 Fix #4: preview an additive merge of upstream values
  // into the user's current file. Returns a candidate body the UI can
  // show in a diff modal; applying calls setAddonValues with that body
  // (no dedicated "apply merge" endpoint — same PR flow as a manual edit).
  previewMergeAddonValues: (addonName: string) =>
    postJSON<import('./models').PreviewMergeResponse>(
      `/addons/${encodeURIComponent(addonName)}/values/preview-merge`,
      {},
    ),
  getClusterAddonValues: (clusterName: string, addonName: string) =>
    fetchJSON<import('./models').ClusterAddonValuesResponse>(
      `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/values`,
    ),
  setClusterAddonValues: (clusterName: string, addonName: string, valuesYAML: string) =>
    putJSON<import('./models').ValuesEditResult>(
      `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/values`,
      { values: valuesYAML },
    ),

  // V121-7.4: manual AI annotate. Returns 200 with an AnnotateAddonValuesResponse,
  // 422 with an AIAnnotateBlockedResponse when the secret guard fires, or
  // 503 when AI is not configured. We use a dedicated wrapper (not the
  // shared postJSON) so the caller can inspect the typed 422 body via
  // `(err as { body }).body` — the shared wrapper drops the body to a
  // plain message string which would lose the secret-leak match list.
  annotateAddonValues: async (addonName: string) => {
    const res = await fetch(`${BASE_URL}/addons/${encodeURIComponent(addonName)}/values/annotate`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders() },
      body: '{}',
    })
    if (res.status === 401) {
      sessionStorage.removeItem(TOKEN_KEY)
      window.location.reload()
      throw new Error('Session expired')
    }
    const body = await res.json().catch(() => ({}))
    if (!res.ok) {
      const err = new Error((body as { error?: string; message?: string }).error || (body as { message?: string }).message || res.statusText) as Error & { body?: unknown; status?: number }
      err.body = body
      err.status = res.status
      throw err
    }
    return body as import('./models').AnnotateAddonValuesResponse
  },
  // V121-7.3: per-addon AI opt-out toggle. Idempotent — flipping to the
  // current state returns 200 with `status: "noop"`.
  setAddonAIOptOut: (addonName: string, optOut: boolean) =>
    putJSON<{ status: string; opt_out: boolean; addon: string; pr_url?: string; pr_id?: number; merged?: boolean }>(
      `/addons/${encodeURIComponent(addonName)}/values/ai-opt-out`,
      { opt_out: optOut },
    ),

  // v1.21 Bundle 5: legacy `<addon>:` wrap migration. Pass `addon` to
  // migrate a single file (used by the per-addon "Migrate this file"
  // banner button). Omit it to migrate every wrapped file in the repo.
  unwrapGlobalValues: (addonName?: string) => {
    const qs = addonName ? `?addon=${encodeURIComponent(addonName)}` : ''
    return postJSON<{
      migrated: number
      skipped: number
      files: Array<{ file: string; addon: string; status: string; message?: string }>
      message?: string
      pr_url?: string
      pr_id?: number
      branch?: string
      merged?: boolean
      attribution_warning?: string
    }>(`/addons/unwrap-globals${qs}`, {})
  },

  // Values editor extras (v1.20.1) — note: pullUpstreamValues was
  // removed in v1.21 (Story V121-6.5) and replaced by
  // refreshAddonValuesFromUpstream above, which calls the existing
  // PUT /api/v1/addons/{name}/values handler with refresh_from_upstream=true.
  getAddonValuesRecentPRs: (addonName: string, limit = 5) =>
    fetchJSON<import('./models').RecentPRsResponse>(
      `/addons/${encodeURIComponent(addonName)}/values/recent-prs?limit=${limit}`,
    ),
  getClusterAddonValuesRecentPRs: (clusterName: string, addonName: string, limit = 5) =>
    fetchJSON<import('./models').RecentPRsResponse>(
      `/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}/values/recent-prs?limit=${limit}`,
    ),

  // Catalog editor (v1.20.1) — same endpoint as existing configureAddon() but
  // a typed wrapper that understands the ValuesEditResult shape with the
  // attribution_warning field.
  setAddonCatalog: (
    addonName: string,
    body: {
      version?: string
      sync_wave?: number
      self_heal?: boolean
      sync_options?: string[]
      ignore_differences?: Record<string, unknown>[]
      additional_sources?: Record<string, unknown>[]
      extra_helm_values?: Record<string, string>
    },
  ) =>
    fetchJSONMethod<import('./models').ValuesEditResult>(
      `/addons/${encodeURIComponent(addonName)}`,
      'PATCH',
      { name: addonName, ...body },
    ),

  // Agent Chat
  agentChat: (sessionId: string, message: string, pageContext?: string) => postJSON<{ session_id: string; response: string }>('/agent/chat', { session_id: sessionId, message, page_context: pageContext }),
  agentReset: (sessionId: string) => postJSON<{ status: string }>('/agent/reset', { session_id: sessionId }),

  // Users
  listUsers: () => fetchJSON<{ username: string; enabled: boolean; role: string }[]>('/users'),
  createUser: (data: { username: string; role: string }) => postJSON<{ username: string; temp_password: string; message: string }>('/users', data),
  updateUser: (username: string, data: { enabled?: boolean; role?: string }) =>
    fetchJSONMethod<{ username: string; enabled: boolean; role: string }>(`/users/${encodeURIComponent(username)}`, 'PUT', data),
  deleteUser: (username: string) => deleteJSON<void>(`/users/${encodeURIComponent(username)}`),
  resetPassword: (username: string) => postJSON<{ username: string; temp_password: string }>(`/users/${encodeURIComponent(username)}/reset-password`),

  // Embedded dashboards (persisted in K8s ConfigMap)
  getEmbeddedDashboards: () => fetchJSON<{ id: string; name: string; url: string; provider: string }[]>('/embedded-dashboards'),
  saveEmbeddedDashboards: (dashboards: { id: string; name: string; url: string; provider: string }[]) =>
    postJSON<unknown>('/embedded-dashboards', dashboards),

  // Cluster nodes
  getNodeInfo: () => fetchJSON<{ nodes: unknown[]; total: number; ready: number; not_ready: number; message?: string }>('/cluster/nodes'),

  // Dashboard
  getAttentionItems: () => fetchJSON<{ app_name: string; addon_name: string; cluster: string; health: string; sync: string; error?: string; error_type?: string }[]>('/dashboard/attention'),

  // Docs
  docsList: () => fetchJSON<{ slug: string; title: string; order: number }[]>('/docs/list'),
  docsGet: (slug: string) => fetchJSON<{ slug: string; content: string }>(`/docs/${encodeURIComponent(slug)}`),

  // Providers
  getProviders: () => fetchJSON<{ configured_provider: { type: string; region: string; prefix?: string; status: string; error?: string } | null; available_types: string[] }>('/providers'),

  // Repo status
  getRepoStatus: () => fetchJSON<{ initialized: boolean; reason?: string }>('/repo/status'),

  // Cluster addons
  enableAddonOnCluster: (clusterName: string, addonName: string) =>
    postJSON<any>(`/clusters/${encodeURIComponent(clusterName)}/addons/${encodeURIComponent(addonName)}`, { yes: true }),

  // Notifications
  getNotifications: () => fetchJSON<{
    notifications: { id: string; type: string; title: string; description: string; timestamp: string; read: boolean }[]
    unread_count: number
  }>('/notifications'),

  markAllNotificationsRead: () => postJSON<unknown>('/notifications/read-all'),

  // ─── Curated catalog (v1.21 Marketplace) ────────────────────────────────
  // Three reads:
  //   1. listCuratedCatalog       — Browse tab grid (server-side filters)
  //   2. getCuratedCatalogEntry   — single-entry detail (Configure modal)
  //   3. listCuratedCatalogVersions — chart versions for the version picker
  //
  // All three are GET-only; no audit/tier coverage required.

  listCuratedCatalog: (filters?: import('./models').CatalogListFilters) => {
    const params = new URLSearchParams()
    if (filters?.q) params.set('q', filters.q)
    if (filters?.category && filters.category.length > 0) {
      // Backend handler only honours a single `category` query param today.
      // Multi-category is handled client-side in MarketplaceBrowseTab so we
      // just send the first when there's exactly one — otherwise let the
      // client filter the full list. Same approach for license below.
      if (filters.category.length === 1) params.set('category', filters.category[0])
    }
    if (filters?.curated_by && filters.curated_by.length > 0) {
      // The backend takes a comma-separated list and ANDs them; that maps
      // exactly to multi-select semantics.
      params.set('curated_by', filters.curated_by.join(','))
    }
    if (filters?.license && filters.license.length === 1) {
      params.set('license', filters.license[0])
    }
    if (filters?.min_score && filters.min_score > 0) {
      params.set('min_score', String(filters.min_score))
    }
    const qs = params.toString()
    return fetchJSON<import('./models').CatalogListResponse>(
      `/catalog/addons${qs ? `?${qs}` : ''}`,
    )
  },

  getCuratedCatalogEntry: (name: string) =>
    fetchJSON<import('./models').CatalogEntry>(
      `/catalog/addons/${encodeURIComponent(name)}`,
    ),

  /**
   * V123-1.7: list configured catalog sources (embedded + third-party).
   * Powers the source-badge tooltip (last-fetched / status) on Browse
   * tiles and the "Source" section on the addon detail page.
   */
  listCatalogSources: () =>
    fetchJSON<import('./models').CatalogSourceRecord[]>('/catalog/sources'),

  /**
   * V123-1.8: force-refresh all configured catalog sources. Tier-2 (admin);
   * backend side emits an audit entry. Returns the fresh record list.
   * Powers the "Refresh now" button in the Settings → Catalog Sources view.
   */
  refreshCatalogSources: () =>
    postJSON<import('./models').CatalogSourceRecord[]>('/catalog/sources/refresh', {}),

  listCuratedCatalogVersions: (
    name: string,
    options?: { includePrereleases?: boolean },
  ) => {
    const params = new URLSearchParams()
    if (options?.includePrereleases === false) {
      params.set('include_prereleases', 'false')
    }
    const qs = params.toString()
    return fetchJSON<import('./models').CatalogVersionsResponse>(
      `/catalog/addons/${encodeURIComponent(name)}/versions${qs ? `?${qs}` : ''}`,
    )
  },

  /**
   * V121-4: Paste-URL validator. Confirms an arbitrary `<repo>/index.yaml` is
   * reachable and contains the named chart. Returns 200 in both the happy and
   * the structured-failure path — branch on `resp.valid` and `resp.error_code`.
   */
  validateCatalogChart: (repo: string, chart: string) => {
    const params = new URLSearchParams({ repo, chart })
    return fetchJSON<import('./models').CatalogValidateResponse>(
      `/catalog/validate?${params.toString()}`,
    )
  },

  /**
   * v1.21 QA Bundle 1: list every chart name in a Helm repo's index.yaml.
   * Powers the chart-name dropdown in the manual "Add Addon" form. Same
   * `valid` + `error_code` envelope as validateCatalogChart so the UI can
   * reuse its existing error switch table.
   */
  listRepoCharts: (repo: string) => {
    const params = new URLSearchParams({ repo })
    return fetchJSON<import('./models').CatalogRepoChartsResponse>(
      `/catalog/repo-charts?${params.toString()}`,
    )
  },

  // ─── ArtifactHub proxy (V121-3 Search tab) ─────────────────────────────
  // Server-side proxy: the browser never calls ArtifactHub directly. The
  // backend handles caching, rate-limit backoff, and stale-serve.

  /**
   * Blended search across the Sharko-curated catalog and ArtifactHub. Returns
   * curated hits and ArtifactHub hits in one envelope; if ArtifactHub is
   * unreachable, curated hits are still returned and `artifacthub_error` is
   * populated so the UI can show the unreachable banner.
   */
  searchCatalog: (q: string, limit = 20) => {
    const params = new URLSearchParams({ q, limit: String(limit) })
    return fetchJSON<import('./models').CatalogSearchResponse>(
      `/catalog/search?${params.toString()}`,
    )
  },

  /**
   * Per-package detail (proxied from ArtifactHub). Used to pre-fill the
   * Configure modal for an external chart. Returns the trimmed package shape
   * (description, maintainers, available versions, links).
   */
  getRemoteCatalogPackage: (repo: string, name: string) =>
    fetchJSON<import('./models').CatalogRemotePackageResponse>(
      `/catalog/remote/${encodeURIComponent(repo)}/${encodeURIComponent(name)}`,
    ),

  /**
   * v1.21 QA Bundle 2: README markdown for a curated catalog addon.
   * The backend resolves the curated chart → ArtifactHub package
   * (best-match heuristic), fetches the package detail, and returns
   * just the README. Empty `readme` means the chart was found but
   * doesn't ship a README — render an empty state, not an error.
   */
  getCuratedCatalogReadme: (name: string) =>
    fetchJSON<import('./models').CatalogReadmeResponse>(
      `/catalog/addons/${encodeURIComponent(name)}/readme`,
    ),

  /**
   * Force ArtifactHub connectivity re-check. Resets the in-memory backoff and
   * purges the search/package caches; returns whether ArtifactHub is currently
   * reachable from the Sharko process. Tier 1 (admin) — used by the "Retry"
   * button on the unreachable banner.
   */
  reprobeArtifactHub: () =>
    postJSON<import('./models').CatalogReprobeResponse>(`/catalog/reprobe`),

}
