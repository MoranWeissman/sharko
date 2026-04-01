import type {
  AddonCatalogResponse,
  AddonDetailResponse,
  AIConfigResponse,
  AvailableVersionsResponse,
  ClusterComparisonResponse,
  ClusterDetailResponse,
  ClusterMetricsData,
  ClustersResponse,
  ConfigDiffResponse,
  ConnectionsListResponse,
  DashboardStats,
  DatadogNamespaceMetrics,
  ObservabilityOverviewResponse,
  PullRequestsResponse,
  UpgradeCheckResponse,
  VersionMatrixResponse,
} from './models'

const BASE_URL = '/api/v1'
const TOKEN_KEY = 'aap-auth-token'

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

export const api = {
  // Health
  health: () => fetchJSON<{ status: string }>('/health'),

  // Clusters
  getClusters: () => fetchJSON<ClustersResponse>('/clusters'),
  getCluster: (name: string) => fetchJSON<ClusterDetailResponse>(`/clusters/${name}`),
  getClusterComparison: (name: string) => fetchJSON<ClusterComparisonResponse>(`/clusters/${name}/comparison`),
  getClusterValues: (name: string) => fetchJSON<{ cluster_name: string; values_yaml: string }>(`/clusters/${name}/values`),
  getConfigDiff: (name: string) => fetchJSON<ConfigDiffResponse>(`/clusters/${name}/config-diff`),

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

  // AI
  getAIStatus: () => fetchJSON<{ enabled: boolean }>('/upgrade/ai-status'),
  getAISummary: (addonName: string, targetVersion: string) => postJSON<{ summary: string }>('/upgrade/ai-summary', { addon_name: addonName, target_version: targetVersion }),
  getAIConfig: () => fetchJSON<AIConfigResponse>('/ai/config'),
  saveAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string }) => postJSON<{ status: string }>('/ai/config', data),
  setAIProvider: (provider: string) => postJSON<{ status: string; provider: string }>('/ai/provider', { provider }),
  testAI: () => postJSON<{ status: string; response: string }>('/ai/test', {}),
  testAIConfig: (data: { provider: string; api_key?: string; model?: string; base_url?: string; ollama_url?: string }) => postJSON<{ status: string; message?: string; response?: string }>('/ai/test-config', data),

  // Datadog
  getDatadogStatus: () => fetchJSON<{ enabled: boolean; site: string }>('/datadog/status'),
  getDatadogNamespaceMetrics: (namespace: string) => fetchJSON<DatadogNamespaceMetrics>(`/datadog/metrics/${namespace}`),
  getClusterMetrics: (clusterName: string) => fetchJSON<ClusterMetricsData>(`/datadog/cluster-metrics/${clusterName}`),

  // Auth
  updatePassword: (currentPassword: string, newPassword: string) => postJSON<{ status: string }>('/auth/update-password', { current_password: currentPassword, new_password: newPassword }),

  // Agent Chat
  agentChat: (sessionId: string, message: string, pageContext?: string) => postJSON<{ session_id: string; response: string }>('/agent/chat', { session_id: sessionId, message, page_context: pageContext }),
  agentReset: (sessionId: string) => postJSON<{ status: string }>('/agent/reset', { session_id: sessionId }),

  // Migration
  getMigrationSettings: () => fetchJSON<MigrationSettings>('/migration/settings'),
  saveMigrationSettings: (settings: MigrationSettings) => postJSON<void>('/migration/settings', settings),
  testMigrationConnection: () => postJSON<{ git: boolean; argocd: boolean }>('/migration/settings/test'),
  listMigrations: () => fetchJSON<Migration[]>('/migration/list'),
  startMigration: (data: { addon_name: string; cluster_name: string; mode?: string }) => postJSON<Migration>('/migration/start', data),
  getMigration: (id: string) => fetchJSON<Migration>(`/migration/${id}`),
  continueMigration: (id: string) => postJSON<void>(`/migration/${id}/continue`),
  pauseMigration: (id: string) => postJSON<void>(`/migration/${id}/pause`),
  retryMigration: (id: string) => postJSON<void>(`/migration/${id}/retry`),
  cancelMigration: (id: string) => postJSON<void>(`/migration/${id}/cancel`),
  mergeMigrationPR: (id: string, step: number) => postJSON<{ status: string }>(`/migration/${id}/merge-pr`, { step }),
  migrationChat: (id: string, message: string) => postJSON<{ response: string }>(`/migration/${id}/chat`, { message }),
  rollbackMigration: (id: string) => postJSON<Migration>(`/migration/${id}/rollback`),
  deleteMigration: (id: string) => deleteJSON<void>(`/migration/${id}`),
  azureListProjects: (org: string, pat: string) => fetchJSON<string[]>(`/migration/azure/projects?org=${encodeURIComponent(org)}&pat=${encodeURIComponent(pat)}`),
  azureListRepos: (org: string, project: string, pat: string) => fetchJSON<string[]>(`/migration/azure/repos?org=${encodeURIComponent(org)}&project=${encodeURIComponent(project)}&pat=${encodeURIComponent(pat)}`),
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

  oldRepoAddons: () => fetchJSON<string[]>('/migration/old-repo/addons'),
  oldRepoClusters: () => fetchJSON<string[]>('/migration/old-repo/clusters'),
  oldRepoClusterAddons: (cluster: string) => fetchJSON<ClusterAddonInfo[]>(`/migration/old-repo/cluster-addons?cluster=${encodeURIComponent(cluster)}`),
  startBatch: (data: { addons: string[]; cluster_name: string; mode: string }) =>
    postJSON<MigrationBatch>('/migration/start-batch', data),
  getActiveBatch: () => fetchJSON<MigrationBatch | null>('/migration/batch-active'),
}

export interface ClusterAddonInfo {
  name: string
  already_migrated: boolean
}

export interface MigrationBatch {
  id: string
  cluster_name: string
  mode: string
  addons: string[]
  migration_ids: string[]
  current_index: number
  status: string
  created_at: string
  updated_at: string
}

// Migration types
export interface LogEntry {
  timestamp: string
  step: number
  repo: string
  action: string
  detail: string
}

export interface MigrationStep {
  number: number
  title: string
  description: string
  status: 'pending' | 'running' | 'waiting' | 'completed' | 'failed' | 'skipped'
  message: string
  pr_url?: string
  pr_number?: number
  pr_repo?: string
  pr_status?: string
  started_at?: string
  completed_at?: string
  error?: string
}

export interface Migration {
  id: string
  addon_name: string
  cluster_name: string
  mode: string
  status: 'pending' | 'running' | 'waiting' | 'paused' | 'gated' | 'completed' | 'failed' | 'cancelled'
  current_step: number
  steps: MigrationStep[]
  logs: LogEntry[]
  created_at: string
  updated_at: string
  completed_at?: string
  error?: string
}

export interface MigrationSettings {
  old_git: {
    provider: string
    owner?: string
    repo?: string
    token?: string
    organization?: string
    project?: string
    repository?: string
    pat?: string
  }
  old_argocd: {
    server_url: string
    token: string
    namespace: string
    insecure?: boolean
  }
  configured: boolean
}
