import { useState, useEffect, useCallback } from 'react'
import type { FormEvent } from 'react'
import {
  GitBranch,
  Server,
  Loader2,
  CheckCircle,
  XCircle,
  Activity,
  AlertTriangle,
  Play,
  Sparkles,
} from 'lucide-react'
import { initRepo } from '@/services/api'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'

interface ConnectionFormData {
  git_url: string
  git_token: string
  argocd_server_url: string
  argocd_token: string
  argocd_namespace: string
}

interface TestStatus {
  git: 'idle' | 'testing' | 'ok' | 'error'
  argocd: 'idle' | 'testing' | 'ok' | 'error'
  gitMessage?: string
  argocdMessage?: string
  gitAuth?: string
  argocdAuth?: string
}

interface LiveStatus {
  git: 'idle' | 'testing' | 'ok' | 'error'
  argocd: 'idle' | 'testing' | 'ok' | 'error'
}

const labelCls = 'block text-sm font-medium text-[#0a3a5a] dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm placeholder:text-[#3a6a8a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-[#2a5a7a]'

export function ConnectionSection() {
  const { connections, loading, error, refreshConnections } = useConnections()

  const existingConn = connections.find((c) => c.is_active) ?? connections[0] ?? null
  const isEdit = existingConn !== null

  const [form, setForm] = useState<ConnectionFormData>({
    git_url: '',
    git_token: '',
    argocd_server_url: '',
    argocd_token: '',
    argocd_namespace: 'argocd',
  })

  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [testStatus, setTestStatus] = useState<TestStatus>({ git: 'idle', argocd: 'idle' })
  const [liveStatus, setLiveStatus] = useState<LiveStatus>({ git: 'idle', argocd: 'idle' })
  const [justSaved, setJustSaved] = useState(false)

  const [repoStatus, setRepoStatus] = useState<{ initialized: boolean; reason?: string } | null>(null)
  const [initRunning, setInitRunning] = useState(false)
  const [initResult, setInitResult] = useState<string | null>(null)
  const [initError, setInitError] = useState<string | null>(null)

  useEffect(() => {
    if (existingConn) {
      let gitUrl = ''
      if (existingConn.git_provider === 'github') {
        gitUrl = `https://github.com/${existingConn.git_repo_identifier}`
      } else if (existingConn.git_provider === 'azuredevops') {
        const parts = existingConn.git_repo_identifier.split('/')
        if (parts.length >= 3) {
          gitUrl = `https://dev.azure.com/${parts[0]}/${parts[1]}/_git/${parts[2]}`
        }
      }
      setForm({
        git_url: gitUrl,
        git_token: '',
        argocd_server_url: existingConn.argocd_server_url,
        argocd_token: '',
        argocd_namespace: existingConn.argocd_namespace,
      })
    }
  }, [existingConn?.name]) // eslint-disable-line react-hooks/exhaustive-deps

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

  const fetchRepoStatus = useCallback(() => {
    if (existingConn) {
      api.getRepoStatus()
        .then(data => setRepoStatus(data))
        .catch(() => setRepoStatus(null))
    }
  }, [existingConn?.name]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (existingConn) {
      fetchLiveStatus()
      fetchRepoStatus()
    }
  }, [existingConn?.name, fetchLiveStatus, fetchRepoStatus]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleInitRepo = useCallback(async () => {
    setInitRunning(true)
    setInitResult(null)
    setInitError(null)
    try {
      const result = await initRepo({ bootstrap_argocd: true })
      const prUrl = result?.pr_url || result?.pull_request_url
      const status = result?.status || result?.message || 'initialized'
      setInitResult(prUrl ? `Repository ${status}. PR: ${prUrl}` : `Repository ${status}.`)
      fetchRepoStatus()
    } catch (e: unknown) {
      setInitError(e instanceof Error ? e.message : 'Failed to initialize repository')
    } finally {
      setInitRunning(false)
    }
  }, [fetchRepoStatus])

  async function testCredentials(which: 'git' | 'argocd') {
    const payload = buildPayload(form, existingConn?.name)
    if (which === 'git') setTestStatus(prev => ({ ...prev, git: 'testing', gitMessage: undefined }))
    if (which === 'argocd') setTestStatus(prev => ({ ...prev, argocd: 'testing', argocdMessage: undefined }))
    try {
      const res = await api.testCredentials(payload)
      if (which === 'git') {
        setTestStatus(prev => ({ ...prev, git: res.git.status === 'ok' ? 'ok' : 'error', gitMessage: res.git.message, gitAuth: res.git.auth }))
      }
      if (which === 'argocd') {
        setTestStatus(prev => ({ ...prev, argocd: res.argocd.status === 'ok' ? 'ok' : 'error', argocdMessage: res.argocd.message, argocdAuth: res.argocd.auth }))
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Test failed'
      if (which === 'git') setTestStatus(prev => ({ ...prev, git: 'error', gitMessage: msg }))
      if (which === 'argocd') setTestStatus(prev => ({ ...prev, argocd: 'error', argocdMessage: msg }))
    }
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setSaving(true)
    setSaveError(null)
    try {
      const payload = buildPayload(form, existingConn?.name)
      if (isEdit && existingConn) {
        await api.updateConnection(existingConn.name, payload)
      } else {
        await api.createConnection(payload)
      }
      refreshConnections()
      setJustSaved(true)
      fetchLiveStatus()
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
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

  if (loading) return <LoadingState message="Loading connection..." />
  if (error) return <ErrorState message={error} onRetry={refreshConnections} />

  return (
    <div className="space-y-6">
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

      {/* Live status bar */}
      {isEdit && (
        <div className="flex items-center justify-between">
          <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Connection</h3>
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
        </div>
      )}

      <form
        onSubmit={handleSubmit}
        className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:bg-gray-800"
      >
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
                <input
                  className={inputCls}
                  value={form.git_url}
                  onChange={(e) => { setForm(prev => ({ ...prev, git_url: e.target.value })); setTestStatus({ git: 'idle', argocd: 'idle' }) }}
                  placeholder="https://github.com/org/repo"
                  required
                />
                <p className="mt-1 text-[10px] text-[#3a6a8a]">GitHub, GitHub Enterprise, or Azure DevOps (auto-detected from URL)</p>
              </div>
              <div>
                <label className={labelCls}>Token</label>
                <input
                  className={inputCls}
                  type="password"
                  value={form.git_token}
                  onChange={(e) => { setForm(prev => ({ ...prev, git_token: e.target.value })); setTestStatus({ git: 'idle', argocd: 'idle' }) }}
                  placeholder={isEdit ? 'Leave blank to keep existing' : 'Personal access token'}
                />
              </div>
            </div>
            <div className="mt-3 flex items-center gap-3">
              <button
                type="button"
                onClick={() => testCredentials('git')}
                disabled={testStatus.git === 'testing' || !form.git_url}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {testStatus.git === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <GitBranch className="h-3 w-3" />}
                Test Git
              </button>
              {testStatus.git === 'ok' && (
                <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                  <CheckCircle className="h-3.5 w-3.5" />
                  Connected{testStatus.gitAuth && testStatus.gitAuth !== 'provided' ? ` (via ${testStatus.gitAuth})` : ''}
                </span>
              )}
              {testStatus.git === 'error' && (
                <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
                  <XCircle className="h-3.5 w-3.5" />
                  {testStatus.gitMessage || 'Failed'}
                </span>
              )}
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
                <input
                  className={inputCls}
                  value={form.argocd_server_url}
                  onChange={(e) => { setForm(prev => ({ ...prev, argocd_server_url: e.target.value })); setTestStatus({ git: 'idle', argocd: 'idle' }) }}
                  placeholder="Auto-discovered from cluster"
                />
                <p className="mt-1 text-[10px] text-[#3a6a8a]">Auto-filled for in-cluster. Override for external ArgoCD.</p>
              </div>
              <div>
                <label className={labelCls}>Token</label>
                <input
                  className={inputCls}
                  type="password"
                  value={form.argocd_token}
                  onChange={(e) => { setForm(prev => ({ ...prev, argocd_token: e.target.value })); setTestStatus({ git: 'idle', argocd: 'idle' }) }}
                  placeholder={isEdit ? 'Leave blank to keep existing' : 'ArgoCD API token'}
                />
                <p className="mt-1 text-[10px] text-[#3a6a8a]">ArgoCD account token (e.g. sharko-api-user). Falls back to ARGOCD_TOKEN env var.</p>
              </div>
              <div>
                <label className={labelCls}>Namespace</label>
                <input
                  className={inputCls}
                  value={form.argocd_namespace}
                  onChange={(e) => { setForm(prev => ({ ...prev, argocd_namespace: e.target.value })); setTestStatus({ git: 'idle', argocd: 'idle' }) }}
                  placeholder="argocd"
                  required
                />
              </div>
            </div>
            <div className="mt-3 flex items-center gap-3">
              <button
                type="button"
                onClick={() => testCredentials('argocd')}
                disabled={testStatus.argocd === 'testing'}
                className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                {testStatus.argocd === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Server className="h-3 w-3" />}
                Test ArgoCD
              </button>
              {testStatus.argocd === 'ok' && (
                <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
                  <CheckCircle className="h-3.5 w-3.5" />
                  Connected{testStatus.argocdAuth && testStatus.argocdAuth !== 'provided' ? ` (via ${testStatus.argocdAuth})` : ''}
                </span>
              )}
              {testStatus.argocd === 'error' && (
                <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
                  <XCircle className="h-3.5 w-3.5" />
                  {testStatus.argocdMessage || 'Failed'}
                </span>
              )}
            </div>
          </div>
        </div>

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
          {justSaved && (
            <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
              <CheckCircle className="h-3.5 w-3.5" /> Saved
            </span>
          )}
        </div>
      </form>

      {/* Initialize Repository */}
      {isEdit && (
        <div>
          <div className="mb-3 flex items-center gap-2">
            <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">Initialize Repository</h3>
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
        </div>
      )}
    </div>
  )
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
  }
}
