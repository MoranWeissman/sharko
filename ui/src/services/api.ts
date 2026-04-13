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

export async function addAddon(data: { name: string; chart: string; repo_url: string; version: string; namespace?: string; sync_wave?: number }) {
  return postJSON<any>('/addons', data)
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
  return patchJSON<{ status: string; pr_url?: string; pull_request_url?: string }>(
    `/addons/${encodeURIComponent(name)}/configure`,
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

export async function getOperation(id: string): Promise<OperationSession> {
  const res = await fetch(`${BASE_URL}/operations/${id}`, { headers: authHeaders() })
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY)
    window.location.reload()
    throw new Error('Session expired')
  }
  if (!res.ok) throw new Error('Failed to get operation')
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

export async function refreshPR(id: number) {
  return postJSON<{ status: string }>(`/prs/${id}/refresh`)
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
  testCredentials: (data: unknown) => postJSON<{ git: { status: string; message?: string; auth?: string }; argocd: { status: string; message?: string; auth?: string } }>('/connections/test-credentials', data),
  discoverArgocd: (namespace?: string) => fetchJSON<{ server_url: string; has_env_token: boolean; namespace: string }>(`/connections/discover-argocd${namespace ? `?namespace=${namespace}` : ''}`),

  // Observability
  getObservability: () => fetchJSON<ObservabilityOverviewResponse>('/observability/overview'),

  // Upgrade
  getUpgradeVersions: (addonName: string) => fetchJSON<AvailableVersionsResponse>(`/upgrade/${addonName}/versions`),
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
  saveAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string }) => postJSON<{ status: string }>('/ai/config', data),
  setAIProvider: (provider: string) => postJSON<{ status: string; provider: string }>('/ai/provider', { provider }),
  testAI: () => postJSON<{ status: string; response: string }>('/ai/test', {}),
  testAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string }) => postJSON<{ status: string; message?: string; response?: string }>('/ai/test-config', data),


  // Auth
  updatePassword: (currentPassword: string, newPassword: string) => postJSON<{ status: string }>('/auth/update-password', { current_password: currentPassword, new_password: newPassword }),

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

  // Notifications
  getNotifications: () => fetchJSON<{
    notifications: { id: string; type: string; title: string; description: string; timestamp: string; read: boolean }[]
    unread_count: number
  }>('/notifications'),

  markAllNotificationsRead: () => postJSON<unknown>('/notifications/read-all'),

}
