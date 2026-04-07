import { useState, useEffect, useCallback } from 'react'
import type { FormEvent } from 'react'
import {
  GitBranch,
  Server,
  Shield,
  Loader2,
  Pencil,
  X,
  Activity,
  Monitor,
  Globe,
  Sparkles,
  CheckCircle,
  XCircle,
  Play,
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  KeyRound,
  GitMerge,
} from 'lucide-react'
import { initRepo } from '@/services/api'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import type { AIConfigResponse, AIProviderInfo, ConnectionResponse } from '@/services/models'

interface PlatformInfo {
  status: string
  mode?: string
  dev_mode?: string
  version?: string
}

interface ConnectionFormData {
  git_url: string
  git_token: string
  argocd_server_url: string
  argocd_token: string
  argocd_namespace: string
  provider_type: '' | 'aws-sm' | 'k8s-secrets'
  provider_region: string
  provider_prefix: string
  gitops_base_branch: string
  gitops_branch_prefix: string
  gitops_commit_prefix: string
  gitops_pr_auto_merge: boolean
  gitops_host_cluster_name: string
  gitops_default_addons: string
}

const emptyForm: ConnectionFormData = {
  git_url: '',
  git_token: '',
  argocd_server_url: '',
  argocd_token: '',
  argocd_namespace: 'argocd',
  provider_type: '',
  provider_region: '',
  provider_prefix: '',
  gitops_base_branch: 'main',
  gitops_branch_prefix: 'sharko/',
  gitops_commit_prefix: 'sharko:',
  gitops_pr_auto_merge: false,
  gitops_host_cluster_name: '',
  gitops_default_addons: '',
}

function buildPayload(form: ConnectionFormData, name?: string) {
  return {
    name: name || undefined,
    git: {
      repo_url: form.git_url,
      token: form.git_token || undefined,
    },
    argocd: {
      server_url: form.argocd_server_url || '',
      token: form.argocd_token || undefined,
      namespace: form.argocd_namespace || 'argocd',
      insecure: true,
    },
    provider: form.provider_type
      ? {
          type: form.provider_type,
          region: form.provider_region || undefined,
          prefix: form.provider_prefix || undefined,
        }
      : undefined,
    gitops: {
      base_branch: form.gitops_base_branch || 'main',
      branch_prefix: form.gitops_branch_prefix || 'sharko/',
      commit_prefix: form.gitops_commit_prefix || 'sharko:',
      pr_auto_merge: form.gitops_pr_auto_merge,
      host_cluster_name: form.gitops_host_cluster_name || undefined,
      default_addons: form.gitops_default_addons || undefined,
    },
  }
}

function formFromConnection(conn: ConnectionResponse): ConnectionFormData {
  // Reconstruct URL from the identifier
  let gitUrl = ''
  if (conn.git_provider === 'github') {
    gitUrl = `https://github.com/${conn.git_repo_identifier}`
  } else if (conn.git_provider === 'azuredevops') {
    const parts = conn.git_repo_identifier.split('/')
    if (parts.length >= 3) {
      gitUrl = `https://dev.azure.com/${parts[0]}/${parts[1]}/_git/${parts[2]}`
    }
  }
  return {
    git_url: gitUrl,
    git_token: '',
    argocd_server_url: conn.argocd_server_url,
    argocd_token: '',
    argocd_namespace: conn.argocd_namespace,
    provider_type: '' as '' | 'aws-sm' | 'k8s-secrets',
    provider_region: '',
    provider_prefix: '',
    gitops_base_branch: 'main',
    gitops_branch_prefix: 'sharko/',
    gitops_commit_prefix: 'sharko:',
    gitops_pr_auto_merge: false,
    gitops_host_cluster_name: '',
    gitops_default_addons: '',
  }
}

/* ------------------------------------------------------------------ */
/*  Shared form fields                                                 */
/* ------------------------------------------------------------------ */

const labelCls =
  'block text-sm font-medium text-[#0a3a5a] dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm placeholder:text-[#3a6a8a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-[#2a5a7a]'
const selectCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100'

interface TestStatus {
  git: 'idle' | 'testing' | 'ok' | 'error'
  argocd: 'idle' | 'testing' | 'ok' | 'error'
  gitMessage?: string
  argocdMessage?: string
  gitAuth?: string
  argocdAuth?: string
}

function ConnectionFormFields({
  form,
  onChange,
  isEdit,
  testStatus,
  onTestGit,
  onTestArgocd,
}: {
  form: ConnectionFormData
  onChange: (patch: Partial<ConnectionFormData>) => void
  isEdit: boolean
  testStatus: TestStatus
  onTestGit: () => void
  onTestArgocd: () => void
}) {
  const [providerOpen, setProviderOpen] = useState(!!form.provider_type)

  const hasNonDefaultGitops =
    form.gitops_base_branch !== 'main' ||
    form.gitops_branch_prefix !== 'sharko/' ||
    form.gitops_commit_prefix !== 'sharko:' ||
    form.gitops_pr_auto_merge ||
    !!form.gitops_host_cluster_name ||
    !!form.gitops_default_addons

  const [gitopsOpen, setGitopsOpen] = useState(hasNonDefaultGitops)

  return (
    <div className="space-y-6">
      {/* Git Configuration */}
      <div>
        <div className="mb-3 flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-[#2a5a7a]" />
          <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Git Repository</h5>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <label className={labelCls}>Repository URL</label>
            <input className={inputCls} value={form.git_url} onChange={(e) => onChange({ git_url: e.target.value })}
              placeholder="https://github.com/org/repo" required />
            <p className="mt-1 text-[10px] text-[#3a6a8a]">GitHub, GitHub Enterprise, or Azure DevOps (auto-detected from URL)</p>
          </div>
          <div>
            <label className={labelCls}>Token</label>
            <input className={inputCls} type="password" value={form.git_token} onChange={(e) => onChange({ git_token: e.target.value })}
              placeholder={isEdit ? 'Leave blank to keep existing' : 'Personal access token'} />
          </div>
        </div>
        <div className="mt-3 flex items-center gap-3">
          <button type="button" onClick={onTestGit} disabled={testStatus.git === 'testing' || !form.git_url}
            className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
            {testStatus.git === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <GitBranch className="h-3 w-3" />}
            Test Git
          </button>
          {testStatus.git === 'ok' && <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400"><CheckCircle className="h-3.5 w-3.5" /> Connected{testStatus.gitAuth && testStatus.gitAuth !== 'provided' ? ` (via ${testStatus.gitAuth})` : ''}</span>}
          {testStatus.git === 'error' && <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400"><XCircle className="h-3.5 w-3.5" /> {testStatus.gitMessage || 'Failed'}</span>}
        </div>
      </div>

      {/* ArgoCD Configuration */}
      <div>
        <div className="mb-3 flex items-center gap-2">
          <Server className="h-4 w-4 text-[#2a5a7a]" />
          <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">ArgoCD</h5>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className={labelCls}>Server URL</label>
            <input className={inputCls} value={form.argocd_server_url} onChange={(e) => onChange({ argocd_server_url: e.target.value })}
              placeholder="Auto-discovered from cluster" />
            <p className="mt-1 text-[10px] text-[#3a6a8a]">Auto-filled for in-cluster. Override for external ArgoCD.</p>
          </div>
          <div>
            <label className={labelCls}>Token</label>
            <input className={inputCls} type="password" value={form.argocd_token} onChange={(e) => onChange({ argocd_token: e.target.value })}
              placeholder={isEdit ? 'Leave blank to keep existing' : 'ArgoCD API token'} />
            <p className="mt-1 text-[10px] text-[#3a6a8a]">ArgoCD account token (e.g. sharko-api-user). Falls back to ARGOCD_TOKEN env var.</p>
          </div>
          <div>
            <label className={labelCls}>Namespace</label>
            <input className={inputCls} value={form.argocd_namespace} onChange={(e) => onChange({ argocd_namespace: e.target.value })}
              placeholder="argocd" required />
          </div>
        </div>
        <div className="mt-3 flex items-center gap-3">
          <button type="button" onClick={onTestArgocd} disabled={testStatus.argocd === 'testing'}
            className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
            {testStatus.argocd === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Server className="h-3 w-3" />}
            Test ArgoCD
          </button>
          {testStatus.argocd === 'ok' && <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400"><CheckCircle className="h-3.5 w-3.5" /> Connected{testStatus.argocdAuth && testStatus.argocdAuth !== 'provided' ? ` (via ${testStatus.argocdAuth})` : ''}</span>}
          {testStatus.argocd === 'error' && <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400"><XCircle className="h-3.5 w-3.5" /> {testStatus.argocdMessage || 'Failed'}</span>}
        </div>
      </div>

      {/* Secrets Provider */}
      <div className="border-t border-[#bee0ff] pt-4 dark:border-gray-700">
        <button
          type="button"
          onClick={() => setProviderOpen((v) => !v)}
          className="flex w-full items-center justify-between text-left"
        >
          <div className="flex items-center gap-2">
            <KeyRound className="h-4 w-4 text-[#2a5a7a]" />
            <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Secrets Provider</h5>
            {form.provider_type && (
              <span className="rounded-full bg-teal-100 px-2 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
                {form.provider_type}
              </span>
            )}
          </div>
          {providerOpen
            ? <ChevronDown className="h-4 w-4 text-[#3a6a8a]" />
            : <ChevronRight className="h-4 w-4 text-[#3a6a8a]" />}
        </button>

        {providerOpen && (
          <div className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="sm:col-span-2">
              <label className={labelCls}>Provider Type</label>
              <select
                className={selectCls}
                value={form.provider_type}
                onChange={(e) => onChange({ provider_type: e.target.value as '' | 'aws-sm' | 'k8s-secrets' })}
              >
                <option value="">None</option>
                <option value="aws-sm">AWS Secrets Manager (aws-sm)</option>
                <option value="k8s-secrets">Kubernetes Secrets (k8s-secrets)</option>
              </select>
              <p className="mt-1 text-[10px] text-[#3a6a8a]">How Sharko retrieves cluster credentials for secret-based providers.</p>
            </div>
            {form.provider_type === 'aws-sm' && (
              <div>
                <label className={labelCls}>Region</label>
                <input
                  className={inputCls}
                  value={form.provider_region}
                  onChange={(e) => onChange({ provider_region: e.target.value })}
                  placeholder="e.g. eu-west-1"
                />
              </div>
            )}
            {form.provider_type && (
              <div>
                <label className={labelCls}>Prefix (optional)</label>
                <input
                  className={inputCls}
                  value={form.provider_prefix}
                  onChange={(e) => onChange({ provider_prefix: e.target.value })}
                  placeholder="e.g. k8s- (prepended to cluster name for SM lookup)"
                />
                <p className="mt-1 text-[10px] text-[#3a6a8a]">Prepended to cluster name when looking up the secret.</p>
              </div>
            )}
          </div>
        )}
      </div>

      {/* GitOps Settings */}
      <div className="border-t border-[#bee0ff] pt-4 dark:border-gray-700">
        <button
          type="button"
          onClick={() => setGitopsOpen((v) => !v)}
          className="flex w-full items-center justify-between text-left"
        >
          <div className="flex items-center gap-2">
            <GitMerge className="h-4 w-4 text-[#2a5a7a]" />
            <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">GitOps Settings (Advanced)</h5>
            {hasNonDefaultGitops && (
              <span className="rounded-full bg-teal-100 px-2 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
                customized
              </span>
            )}
          </div>
          {gitopsOpen
            ? <ChevronDown className="h-4 w-4 text-[#3a6a8a]" />
            : <ChevronRight className="h-4 w-4 text-[#3a6a8a]" />}
        </button>

        {gitopsOpen && (
          <div className="mt-4 space-y-4">
            {/* Git group */}
            <div>
              <p className="mb-2 text-xs font-medium uppercase tracking-wide text-[#3a6a8a] dark:text-gray-500">Git</p>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div>
                  <label className={labelCls}>Base Branch</label>
                  <input
                    className={inputCls}
                    value={form.gitops_base_branch}
                    onChange={(e) => onChange({ gitops_base_branch: e.target.value })}
                    placeholder="main"
                  />
                </div>
                <div>
                  <label className={labelCls}>Branch Prefix</label>
                  <input
                    className={inputCls}
                    value={form.gitops_branch_prefix}
                    onChange={(e) => onChange({ gitops_branch_prefix: e.target.value })}
                    placeholder="sharko/"
                  />
                </div>
                <div className="sm:col-span-2">
                  <label className={labelCls}>Commit Prefix</label>
                  <input
                    className={inputCls}
                    value={form.gitops_commit_prefix}
                    onChange={(e) => onChange({ gitops_commit_prefix: e.target.value })}
                    placeholder="sharko:"
                  />
                </div>
              </div>
            </div>

            {/* Automation group */}
            <div>
              <p className="mb-2 text-xs font-medium uppercase tracking-wide text-[#3a6a8a] dark:text-gray-500">Automation</p>
              <label className="flex items-center gap-3 cursor-pointer">
                <div className="relative">
                  <input
                    type="checkbox"
                    className="sr-only peer"
                    checked={form.gitops_pr_auto_merge}
                    onChange={(e) => onChange({ gitops_pr_auto_merge: e.target.checked })}
                  />
                  <div className="h-5 w-9 rounded-full border border-[#5a9dd0] bg-[#f0f7ff] peer-checked:bg-teal-500 peer-checked:border-teal-500 transition-colors dark:border-gray-600 dark:bg-gray-700" />
                  <div className="absolute top-0.5 left-0.5 h-4 w-4 rounded-full bg-white shadow transition-transform peer-checked:translate-x-4" />
                </div>
                <span className="text-sm font-medium text-[#0a3a5a] dark:text-gray-300">Auto-merge PRs</span>
              </label>
            </div>

            {/* Cluster group */}
            <div>
              <p className="mb-2 text-xs font-medium uppercase tracking-wide text-[#3a6a8a] dark:text-gray-500">Cluster</p>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div>
                  <label className={labelCls}>Host Cluster Name <span className="text-[#3a6a8a] font-normal">(optional)</span></label>
                  <input
                    className={inputCls}
                    value={form.gitops_host_cluster_name}
                    onChange={(e) => onChange({ gitops_host_cluster_name: e.target.value })}
                    placeholder="e.g. management"
                  />
                  <p className="mt-1 text-[10px] text-[#3a6a8a]">Name of the cluster running Sharko + ArgoCD</p>
                </div>
                <div>
                  <label className={labelCls}>Default Addons <span className="text-[#3a6a8a] font-normal">(optional)</span></label>
                  <input
                    className={inputCls}
                    value={form.gitops_default_addons}
                    onChange={(e) => onChange({ gitops_default_addons: e.target.value })}
                    placeholder="e.g. metrics-server,cert-manager"
                  />
                  <p className="mt-1 text-[10px] text-[#3a6a8a]">Comma-separated addon names enabled on new clusters by default</p>
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Main component                                                     */
/* ------------------------------------------------------------------ */

interface LiveStatus {
  git: 'idle' | 'testing' | 'ok' | 'error'
  argocd: 'idle' | 'testing' | 'ok' | 'error'
}

interface ProviderInfo {
  type: string
  region: string
  status: string
}

export function Connections({ embedded }: { embedded?: boolean } = {}) {
  const { connections, loading, error, refreshConnections } = useConnections()
  const [platformInfo, setPlatformInfo] = useState<PlatformInfo | null>(null)
  const [healthLoading, setHealthLoading] = useState(true)

  // Live status indicators
  const [liveStatus, setLiveStatus] = useState<LiveStatus>({ git: 'idle', argocd: 'idle' })

  // Secrets provider info
  const [providerInfo, setProviderInfo] = useState<ProviderInfo | null>(null)

  // Single connection form state
  const [form, setForm] = useState<ConnectionFormData>({ ...emptyForm })
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [testStatus, setTestStatus] = useState<TestStatus>({ git: 'idle', argocd: 'idle' })

  // Derive the single connection (first/active one)
  const existingConn = connections.find((c) => c.is_active) ?? connections[0] ?? null
  const isEdit = existingConn !== null

  // Populate form when connection loads
  useEffect(() => {
    if (existingConn) {
      setForm(formFromConnection(existingConn))
    }
  }, [existingConn?.name]) // eslint-disable-line react-hooks/exhaustive-deps

  // Repo initialization status
  const [repoStatus, setRepoStatus] = useState<{ initialized: boolean; reason?: string } | null>(null)

  const fetchRepoStatus = useCallback(() => {
    if (existingConn) {
      api.getRepoStatus()
        .then(data => setRepoStatus(data))
        .catch(() => setRepoStatus(null))
    }
  }, [existingConn?.name]) // eslint-disable-line react-hooks/exhaustive-deps

  // Initialize repo
  const [initRunning, setInitRunning] = useState(false)
  const [initResult, setInitResult] = useState<string | null>(null)
  const [initError, setInitError] = useState<string | null>(null)
  const [justSaved, setJustSaved] = useState(false)

  const handleInitRepo = useCallback(async () => {
    setInitRunning(true)
    setInitResult(null)
    setInitError(null)
    try {
      const result = await initRepo({ bootstrap_argocd: true })
      const prUrl = result?.pr_url || result?.pull_request_url
      const status = result?.status || result?.message || 'initialized'
      setInitResult(prUrl ? `Repository ${status}. PR: ${prUrl}` : `Repository ${status}.`)
      // Refresh repo status to update the banner
      fetchRepoStatus()
    } catch (e: unknown) {
      setInitError(e instanceof Error ? e.message : 'Failed to initialize repository')
    } finally {
      setInitRunning(false)
    }
  }, [fetchRepoStatus])

  const fetchHealth = useCallback(() => {
    setHealthLoading(true)
    api
      .health()
      .then((data) => setPlatformInfo(data))
      .catch(() => setPlatformInfo(null))
      .finally(() => setHealthLoading(false))
  }, [])

  const fetchLiveStatus = useCallback(() => {
    setLiveStatus({ git: 'testing', argocd: 'testing' })
    api
      .testConnection()
      .then((res) => {
        setLiveStatus({
          git: res.git.status === 'ok' ? 'ok' : 'error',
          argocd: res.argocd.status === 'ok' ? 'ok' : 'error',
        })
      })
      .catch(() => setLiveStatus({ git: 'error', argocd: 'error' }))
  }, [])

  const fetchProviderInfo = useCallback(() => {
    api
      .getProviders()
      .then((data) => {
        if (data.configured_provider) {
          setProviderInfo(data.configured_provider as ProviderInfo)
        }
      })
      .catch(() => setProviderInfo(null))
  }, [])

  useEffect(() => {
    fetchHealth()
    fetchProviderInfo()
  }, [fetchHealth, fetchProviderInfo])

  useEffect(() => {
    if (existingConn) {
      fetchLiveStatus()
      fetchRepoStatus()
    } else {
      setLiveStatus({ git: 'idle', argocd: 'idle' })
      setRepoStatus(null)
    }
  }, [existingConn?.name, fetchLiveStatus, fetchRepoStatus]) // eslint-disable-line react-hooks/exhaustive-deps

  async function testCredentials(which: 'git' | 'argocd' | 'both') {
    const payload = buildPayload(form, existingConn?.name)
    if (which === 'git' || which === 'both') setTestStatus(prev => ({ ...prev, git: 'testing', gitMessage: undefined }))
    if (which === 'argocd' || which === 'both') setTestStatus(prev => ({ ...prev, argocd: 'testing', argocdMessage: undefined }))
    try {
      const res = await api.testCredentials(payload)
      if (which === 'git' || which === 'both') {
        setTestStatus(prev => ({ ...prev, git: res.git.status === 'ok' ? 'ok' : 'error', gitMessage: res.git.message, gitAuth: res.git.auth }))
      }
      if (which === 'argocd' || which === 'both') {
        setTestStatus(prev => ({ ...prev, argocd: res.argocd.status === 'ok' ? 'ok' : 'error', argocdMessage: res.argocd.message, argocdAuth: res.argocd.auth }))
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Test failed'
      if (which === 'git' || which === 'both') setTestStatus(prev => ({ ...prev, git: 'error', gitMessage: msg }))
      if (which === 'argocd' || which === 'both') setTestStatus(prev => ({ ...prev, argocd: 'error', argocdMessage: msg }))
    }
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    setSaveError(null)
    setTestStatus({ git: 'testing', argocd: 'testing' })
    try {
      const payload = buildPayload(form, existingConn?.name)
      const res = await api.testCredentials(payload)
      const gitOk = res.git.status === 'ok'
      const argocdOk = res.argocd.status === 'ok'
      setTestStatus({
        git: gitOk ? 'ok' : 'error',
        argocd: argocdOk ? 'ok' : 'error',
        gitMessage: res.git.message,
        argocdMessage: res.argocd.message,
        gitAuth: res.git.auth,
        argocdAuth: res.argocd.auth,
      })
      if (!gitOk || !argocdOk) {
        const errors = []
        if (!gitOk) errors.push(`Git: ${res.git.message || 'failed'}`)
        if (!argocdOk) errors.push(`ArgoCD: ${res.argocd.message || 'failed'}`)
        setSaveError(`Connection test failed — ${errors.join(', ')}`)
        setSaving(false)
        return
      }
      if (isEdit && existingConn) {
        await api.updateConnection(existingConn.name, buildPayload(form, existingConn.name))
      } else {
        await api.createConnection(buildPayload(form))
      }
      refreshConnections()
      setJustSaved(true)
      fetchLiveStatus()
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Failed to save connection')
    } finally {
      setSaving(false)
    }
  }

  async function handleTestConnection() {
    setLiveStatus({ git: 'testing', argocd: 'testing' })
    try {
      const res = await api.testConnection()
      setLiveStatus({
        git: res.git.status === 'ok' ? 'ok' : 'error',
        argocd: res.argocd.status === 'ok' ? 'ok' : 'error',
      })
    } catch {
      setLiveStatus({ git: 'error', argocd: 'error' })
    }
  }

  if (loading) {
    return <LoadingState message="Loading settings..." />
  }

  if (error) {
    return <ErrorState message={error} onRetry={refreshConnections} />
  }

  return (
    <div className="space-y-8">
      {/* Header */}
      {!embedded && (
        <div>
          <h2 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">
            Settings
          </h2>
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
            Manage connection and view platform information.
          </p>
        </div>
      )}

      {/* Repo not initialized banner */}
      {existingConn && repoStatus && !repoStatus.initialized && (
        <div className="flex items-start gap-3 rounded-xl border border-amber-300 bg-amber-50 px-4 py-4 dark:border-amber-700 dark:bg-amber-950/30">
          <AlertTriangle className="mt-0.5 h-5 w-5 flex-shrink-0 text-amber-600 dark:text-amber-400" />
          <div className="flex-1 min-w-0">
            <p className="text-sm font-semibold text-amber-800 dark:text-amber-300">Repository not initialized</p>
            <p className="mt-0.5 text-sm text-amber-700 dark:text-amber-400">
              Your Git repository has not been bootstrapped yet. Sharko cannot manage addons until the repository is initialized.
            </p>
            <div className="mt-3 flex items-center gap-3 flex-wrap">
              <button
                type="button"
                onClick={handleInitRepo}
                disabled={initRunning}
                className="inline-flex items-center gap-1.5 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-amber-700 disabled:opacity-50 dark:bg-amber-700 dark:hover:bg-amber-600"
              >
                {initRunning ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
                Initialize Repository
              </button>
              {initResult && (
                <span className="flex items-center gap-1 text-sm text-green-700 dark:text-green-400">
                  <CheckCircle className="h-4 w-4" />
                  {initResult}
                </span>
              )}
              {initError && (
                <span className="flex items-center gap-1 text-sm text-red-700 dark:text-red-400">
                  <XCircle className="h-4 w-4" />
                  {initError}
                </span>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Connection Form */}
      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
            Connection
          </h3>
          {isEdit && (
            <div className="flex items-center gap-3">
              <span className="flex items-center gap-1 text-xs text-[#2a5a7a] dark:text-gray-400">
                <GitBranch className="h-3.5 w-3.5" />
                Git:
                {liveStatus.git === 'testing' && <Loader2 className="h-3 w-3 animate-spin text-[#3a6a8a]" />}
                {liveStatus.git === 'ok' && <CheckCircle className="h-3.5 w-3.5 text-green-500" />}
                {liveStatus.git === 'error' && <XCircle className="h-3.5 w-3.5 text-red-500" />}
                {liveStatus.git === 'idle' && <span className="text-[#3a6a8a]">—</span>}
              </span>
              <span className="flex items-center gap-1 text-xs text-[#2a5a7a] dark:text-gray-400">
                <Server className="h-3.5 w-3.5" />
                ArgoCD:
                {liveStatus.argocd === 'testing' && <Loader2 className="h-3 w-3 animate-spin text-[#3a6a8a]" />}
                {liveStatus.argocd === 'ok' && <CheckCircle className="h-3.5 w-3.5 text-green-500" />}
                {liveStatus.argocd === 'error' && <XCircle className="h-3.5 w-3.5 text-red-500" />}
                {liveStatus.argocd === 'idle' && <span className="text-[#3a6a8a]">—</span>}
              </span>
              <button
                type="button"
                onClick={handleTestConnection}
                disabled={liveStatus.git === 'testing' || liveStatus.argocd === 'testing'}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {(liveStatus.git === 'testing' || liveStatus.argocd === 'testing') ? (
                  <Loader2 className="h-3 w-3 animate-spin" />
                ) : (
                  <Activity className="h-3 w-3" />
                )}
                Test Connection
              </button>
            </div>
          )}
        </div>

        <form
          onSubmit={handleSubmit}
          className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800"
        >
          <ConnectionFormFields
            form={form}
            onChange={(patch) => {
              setForm((prev) => ({ ...prev, ...patch }))
              setTestStatus({ git: 'idle', argocd: 'idle' })
            }}
            isEdit={isEdit}
            testStatus={testStatus}
            onTestGit={() => testCredentials('git')}
            onTestArgocd={() => testCredentials('argocd')}
          />
          {saveError && (
            <p className="mt-3 text-sm text-red-600 dark:text-red-400">{saveError}</p>
          )}
          <div className="mt-6 flex items-center gap-3">
            <button
              type="submit"
              disabled={saving}
              className="inline-flex items-center gap-1.5 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {isEdit ? 'Update Connection' : 'Save Connection'}
            </button>
          </div>
        </form>
      </section>

      {/* Platform Info */}
      <section className="space-y-4">
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          Platform Info
        </h3>
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <dl className="grid grid-cols-1 gap-6 sm:grid-cols-2">
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Monitor className="h-3.5 w-3.5" />
                Deployment Mode
              </dt>
              <dd className="mt-1 text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                {platformInfo?.mode ?? 'Unknown'}
              </dd>
            </div>
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Activity className="h-3.5 w-3.5" />
                API Health
              </dt>
              <dd className="mt-1 flex items-center gap-2 text-sm font-medium">
                {healthLoading ? (
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-[#3a6a8a] dark:text-gray-500" />
                ) : platformInfo?.status === 'ok' || platformInfo?.status === 'healthy' ? (
                  <>
                    <span className="inline-block h-2.5 w-2.5 rounded-full bg-green-500" />
                    <span className="text-green-600 dark:text-green-400">Healthy</span>
                  </>
                ) : (
                  <>
                    <span className="inline-block h-2.5 w-2.5 rounded-full bg-red-500" />
                    <span className="text-red-600 dark:text-red-400">{platformInfo?.status ?? 'Unreachable'}</span>
                  </>
                )}
              </dd>
            </div>
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <GitBranch className="h-3.5 w-3.5" />
                Git Provider
              </dt>
              <dd className="mt-1 text-sm font-medium capitalize text-[#0a2a4a] dark:text-gray-100">
                {existingConn?.git_provider ?? 'N/A'}
              </dd>
            </div>
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                <Globe className="h-3.5 w-3.5" />
                ArgoCD Server
              </dt>
              <dd className="mt-1 break-all font-mono text-sm text-[#0a2a4a] dark:text-gray-100">
                {existingConn?.argocd_server_url ?? 'N/A'}
              </dd>
            </div>
          </dl>
        </div>
      </section>

      {/* Secrets Provider */}
      <section className="space-y-4">
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          Secrets Provider
        </h3>
        <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          {providerInfo ? (
            <dl className="grid grid-cols-1 gap-4 sm:grid-cols-3">
              <div>
                <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                  <Shield className="h-3.5 w-3.5" />
                  Type
                </dt>
                <dd className="mt-1 font-mono text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                  {providerInfo.type}
                </dd>
              </div>
              <div>
                <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                  <Globe className="h-3.5 w-3.5" />
                  Region
                </dt>
                <dd className="mt-1 font-mono text-sm font-medium text-[#0a2a4a] dark:text-gray-100">
                  {providerInfo.region || '—'}
                </dd>
              </div>
              <div>
                <dt className="flex items-center gap-1.5 text-sm text-[#2a5a7a] dark:text-gray-400">
                  <Activity className="h-3.5 w-3.5" />
                  Status
                </dt>
                <dd className="mt-1 flex items-center gap-1.5 text-sm font-medium">
                  {providerInfo.status === 'connected' ? (
                    <><span className="inline-block h-2 w-2 rounded-full bg-green-500" /><span className="text-green-600 dark:text-green-400">Connected</span></>
                  ) : providerInfo.status === 'configured' ? (
                    <><span className="inline-block h-2 w-2 rounded-full bg-yellow-500" /><span className="text-yellow-600 dark:text-yellow-400">Configured</span></>
                  ) : (
                    <><span className="inline-block h-2 w-2 rounded-full bg-red-500" /><span className="text-red-600 dark:text-red-400">Error</span></>
                  )}
                </dd>
              </div>
            </dl>
          ) : (
            <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
              No secrets provider configured. Set <code className="rounded bg-[#d6eeff] px-1 py-0.5 text-xs dark:bg-gray-700">SHARKO_PROVIDER_TYPE</code> to enable.
            </p>
          )}
        </div>
      </section>

      {/* Initialize Repository */}
      {isEdit && (
        <section className="space-y-4">
          <div className="flex items-center gap-2">
            <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
              Initialize Repository
            </h3>
            {justSaved && (
              <span className="rounded-full bg-teal-100 px-2.5 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-300">
                Connection saved
              </span>
            )}
          </div>
          <div className={`rounded-xl ring-2 bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800 transition-all ${
            justSaved
              ? 'ring-teal-400 border border-teal-200 dark:ring-teal-600 dark:border-teal-800'
              : 'ring-[#6aade0] dark:border-gray-700'
          }`}>
            {justSaved && (
              <div className="mb-4 flex items-start gap-2 rounded-lg bg-teal-50 p-3 dark:bg-teal-950/30">
                <Sparkles className="h-4 w-4 mt-0.5 shrink-0 text-teal-600 dark:text-teal-400" />
                <p className="text-sm text-teal-700 dark:text-teal-300">
                  Connection saved! If your repository is empty, initialize it now to create the required folder structure and ApplicationSet.
                </p>
              </div>
            )}
            <p className="mb-4 text-sm text-[#1a4a6a] dark:text-gray-400">
              Bootstrap the Git repository with the required Sharko directory structure and ArgoCD resources.
              Safe to run on an already-initialized repository.
            </p>
            <div className="flex items-center gap-3">
              <button
                type="button"
                onClick={handleInitRepo}
                disabled={initRunning}
                className="inline-flex items-center gap-2 rounded-lg bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
              >
                {initRunning ? (
                  <Loader2 className="h-4 w-4 animate-spin" />
                ) : (
                  <Play className="h-4 w-4" />
                )}
                Initialize Repository
              </button>
              {justSaved && !initRunning && !initResult && (
                <button
                  type="button"
                  onClick={() => setJustSaved(false)}
                  className="text-xs text-[#3a6a8a] hover:text-[#1a4a6a] dark:text-gray-500 dark:hover:text-gray-400"
                >
                  Dismiss
                </button>
              )}
            </div>
            {initError && (
              <p className="mt-3 text-sm text-red-600 dark:text-red-400">{initError}</p>
            )}
            {initResult && (
              <p className="mt-3 text-sm text-green-600 dark:text-green-400">{initResult}</p>
            )}
          </div>
        </section>
      )}

      {/* AI Configuration */}
      <section className="space-y-4">
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          AI Configuration
        </h3>
        <AIConfigSection />
      </section>

    </div>
  )
}

function AIConfigSection() {
  const [aiConfig, setAiConfig] = useState<AIConfigResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [testResult, setTestResult] = useState<string | null>(null)
  const [testing, setTesting] = useState(false)
  const [showForm, setShowForm] = useState(false)
  const [formProvider, setFormProvider] = useState('gemini')
  const [formApiKey, setFormApiKey] = useState('')
  const [formModel, setFormModel] = useState('')
  const [formBaseURL, setFormBaseURL] = useState('')
  const [formOllamaURL, setFormOllamaURL] = useState('http://localhost:11434')
  const [formTestStatus, setFormTestStatus] = useState<'idle' | 'testing' | 'ok' | 'error'>('idle')
  const [formTestMsg, setFormTestMsg] = useState('')
  const [saving, setSaving] = useState(false)

  const providerModels: Record<string, string[]> = {
    gemini: ['gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-pro'],
    claude: ['claude-sonnet-4-20250514', 'claude-haiku-4-5-20251001', 'claude-opus-4-20250514'],
    openai: ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'o3-mini'],
    ollama: ['llama3.2', 'llama3.1:8b', 'qwen2.5', 'mistral', 'llama3.1:70b'],
    'custom-openai': [],
  }
  const defaultModels: Record<string, string> = {
    gemini: 'gemini-2.5-flash',
    claude: 'claude-sonnet-4-20250514',
    openai: 'gpt-4o',
    ollama: 'llama3.2',
    'custom-openai': '',
  }

  const fetchConfig = useCallback(() => {
    setLoading(true)
    api.getAIConfig()
      .then(setAiConfig)
      .catch(() => setAiConfig(null))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { fetchConfig() }, [fetchConfig])

  const isEnabled = aiConfig?.current_provider && aiConfig.current_provider !== 'none' && aiConfig.current_provider !== ''
  const activeProvider = aiConfig?.available_providers.find((p: AIProviderInfo) => p.id === aiConfig.current_provider)

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const res = await api.testAI()
      setTestResult(res.status === 'ok' ? 'AI is responding correctly' : 'AI returned unexpected response')
    } catch (err) {
      setTestResult(err instanceof Error ? err.message : 'Connection failed')
    } finally { setTesting(false) }
  }

  const handleFormTest = async () => {
    setFormTestStatus('testing')
    setFormTestMsg('')
    try {
      const res = await api.testAIConfig({
        provider: formProvider,
        api_key: formApiKey || undefined,
        model: formModel || defaultModels[formProvider] || undefined,
        base_url: formBaseURL || undefined,
        ollama_url: formProvider === 'ollama' ? formOllamaURL : undefined,
      })
      if (res.status === 'ok') {
        setFormTestStatus('ok')
        setFormTestMsg(res.response || 'Connected')
      } else {
        setFormTestStatus('error')
        setFormTestMsg(res.message || 'Test failed')
      }
    } catch (err) {
      setFormTestStatus('error')
      setFormTestMsg(err instanceof Error ? err.message : 'Test failed')
    }
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      await api.saveAIConfig({
        provider: formProvider,
        api_key: formApiKey || undefined,
        model: formModel || defaultModels[formProvider] || undefined,
        base_url: formBaseURL || undefined,
        ollama_url: formProvider === 'ollama' ? formOllamaURL : undefined,
      })
      setShowForm(false)
      setFormTestStatus('idle')
      fetchConfig()
    } catch (err) {
      setFormTestMsg(err instanceof Error ? err.message : 'Save failed')
    } finally { setSaving(false) }
  }

  const handleDisable = async () => {
    try {
      await api.saveAIConfig({ provider: 'none' })
      fetchConfig()
    } catch { /* ignore */ }
  }

  const openEditForm = () => {
    const cfg = aiConfig
    if (cfg && isEnabled && activeProvider) {
      setFormProvider(activeProvider.id)
      setFormModel(activeProvider.model || '')
    }
    setFormApiKey('')
    setFormTestStatus('idle')
    setFormTestMsg('')
    setShowForm(true)
  }

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <div className={`flex h-10 w-10 items-center justify-center rounded-lg ${isEnabled ? 'bg-purple-100 dark:bg-purple-900/30' : 'bg-[#d6eeff] dark:bg-gray-700'}`}>
            <Sparkles className={`h-5 w-5 ${isEnabled ? 'text-purple-600 dark:text-purple-400' : 'text-[#3a6a8a]'}`} />
          </div>
          <div>
            <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              AI Analysis
              {loading ? '' : isEnabled ? (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                  <span className="inline-block h-1.5 w-1.5 rounded-full bg-green-500" />
                  Active
                </span>
              ) : (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#2a5a7a] dark:bg-gray-700 dark:text-gray-400">
                  Not Configured
                </span>
              )}
            </h4>
            <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400">
              {isEnabled && activeProvider
                ? `Using ${activeProvider.name}${activeProvider.model ? ` — ${activeProvider.model}` : ''}`
                : 'Configure an AI provider for upgrade analysis and migration assistance'}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {isEnabled && (
            <>
              <button onClick={handleTest} disabled={testing}
                className="rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
                {testing ? 'Testing...' : 'Test'}
              </button>
              <button onClick={handleDisable}
                className="rounded-lg border border-red-300 px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 dark:border-red-700 dark:text-red-400 dark:hover:bg-red-900/20">
                Disable
              </button>
            </>
          )}
          <button onClick={openEditForm}
            className="inline-flex items-center gap-1 rounded-lg bg-purple-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-purple-700 dark:bg-purple-700 dark:hover:bg-purple-600">
            <Pencil className="h-3 w-3" />
            {isEnabled ? 'Edit' : 'Configure'}
          </button>
        </div>
      </div>

      {testResult && (
        <div className={`mt-3 flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${
          testResult.includes('correctly') ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400'
        }`}>
          {testResult.includes('correctly') ? <CheckCircle className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
          {testResult}
        </div>
      )}

      {/* Configure Form */}
      {showForm && (
        <div className="mt-4 rounded-lg border border-purple-200 bg-purple-50/50 p-4 dark:border-purple-800 dark:bg-purple-950/20">
          <div className="mb-3 flex items-center justify-between">
            <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Configure AI Provider</h5>
            <button onClick={() => setShowForm(false)} className="text-[#3a6a8a] hover:text-[#1a4a6a] dark:hover:text-gray-200">
              <X className="h-4 w-4" />
            </button>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label className={labelCls}>Provider</label>
              <select className={selectCls} value={formProvider} onChange={(e) => {
                setFormProvider(e.target.value)
                setFormModel(defaultModels[e.target.value] || '')
                setFormTestStatus('idle')
              }}>
                <option value="gemini">Gemini (Google)</option>
                <option value="claude">Claude (Anthropic)</option>
                <option value="openai">OpenAI</option>
                <option value="ollama">Ollama (Local)</option>
                <option value="custom-openai">Custom OpenAI-compatible</option>
              </select>
            </div>
            {formProvider !== 'ollama' && (
              <div>
                <label className={labelCls}>API Key</label>
                <input className={inputCls} type="password" value={formApiKey} onChange={(e) => { setFormApiKey(e.target.value); setFormTestStatus('idle') }}
                  placeholder={formProvider === 'gemini' ? 'AIzaSy...' : formProvider === 'claude' ? 'sk-ant-...' : 'sk-...'} />
              </div>
            )}
            <div>
              <label className={labelCls}>Model</label>
              {providerModels[formProvider]?.length > 0 ? (
                <select className={selectCls} value={formModel} onChange={(e) => { setFormModel(e.target.value); setFormTestStatus('idle') }}>
                  {providerModels[formProvider].map(m => (
                    <option key={m} value={m}>{m}</option>
                  ))}
                </select>
              ) : (
                <input className={inputCls} value={formModel} onChange={(e) => { setFormModel(e.target.value); setFormTestStatus('idle') }}
                  placeholder="model name" />
              )}
            </div>
            {formProvider === 'ollama' && (
              <div>
                <label className={labelCls}>Ollama URL</label>
                <input className={inputCls} value={formOllamaURL} onChange={(e) => { setFormOllamaURL(e.target.value); setFormTestStatus('idle') }}
                  placeholder="http://localhost:11434" />
              </div>
            )}
            {formProvider === 'custom-openai' && (
              <div>
                <label className={labelCls}>Base URL</label>
                <input className={inputCls} value={formBaseURL} onChange={(e) => { setFormBaseURL(e.target.value); setFormTestStatus('idle') }}
                  placeholder="https://your-gateway.example.com/api" />
              </div>
            )}
          </div>
          <div className="mt-3 flex items-center gap-3">
            <button onClick={handleFormTest} disabled={formTestStatus === 'testing'}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
              {formTestStatus === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Sparkles className="h-3 w-3" />}
              Test AI
            </button>
            {formTestStatus === 'ok' && <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400"><CheckCircle className="h-3.5 w-3.5" /> Connected</span>}
            {formTestStatus === 'error' && <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400"><XCircle className="h-3.5 w-3.5" /> {formTestMsg}</span>}
          </div>
          <div className="mt-4 flex items-center gap-3">
            <button onClick={handleSave} disabled={saving || formTestStatus !== 'ok'}
              title={formTestStatus !== 'ok' ? 'Test the connection first' : undefined}
              className="inline-flex items-center gap-1.5 rounded-lg bg-purple-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-purple-700 disabled:opacity-50 dark:bg-purple-700 dark:hover:bg-purple-600">
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Save
            </button>
            <button onClick={() => setShowForm(false)} className="rounded-lg px-4 py-2 text-sm font-medium text-[#1a4a6a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200">
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
