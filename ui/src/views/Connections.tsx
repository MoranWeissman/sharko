import { useState, useEffect, useCallback } from 'react'
import type { FormEvent } from 'react'
import {
  GitBranch,
  Server,
  Shield,
  Loader2,
  Plus,
  Pencil,
  X,
  Activity,
  Monitor,
  Globe,
  Sparkles,
  BarChart2,
  CheckCircle,
  XCircle,
} from 'lucide-react'
import { useConnections } from '@/hooks/useConnections'
import { api } from '@/services/api'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { Badge } from '@/components/ui/badge'
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
}

const emptyForm: ConnectionFormData = {
  git_url: '',
  git_token: '',
  argocd_server_url: '',
  argocd_token: '',
  argocd_namespace: 'argocd',
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
  }
}

/* ------------------------------------------------------------------ */
/*  Shared form fields                                                 */
/* ------------------------------------------------------------------ */

const labelCls =
  'block text-sm font-medium text-gray-700 dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 shadow-sm placeholder:text-gray-400 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-gray-500'
const selectCls =
  'mt-1 block w-full rounded-lg border border-gray-300 bg-white px-3 py-2 text-sm text-gray-900 shadow-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100'

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
  return (
    <div className="space-y-6">
      {/* Git Configuration */}
      <div>
        <div className="mb-3 flex items-center gap-2">
          <GitBranch className="h-4 w-4 text-gray-500" />
          <h5 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Git Repository</h5>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <label className={labelCls}>Repository URL</label>
            <input className={inputCls} value={form.git_url} onChange={(e) => onChange({ git_url: e.target.value })}
              placeholder="https://github.com/org/repo" required />
            <p className="mt-1 text-[10px] text-gray-400">GitHub, GitHub Enterprise, or Azure DevOps (auto-detected from URL)</p>
          </div>
          <div>
            <label className={labelCls}>Token</label>
            <input className={inputCls} type="password" value={form.git_token} onChange={(e) => onChange({ git_token: e.target.value })}
              placeholder={isEdit ? 'Leave blank to keep existing' : 'Personal access token'} />
          </div>
        </div>
        <div className="mt-3 flex items-center gap-3">
          <button type="button" onClick={onTestGit} disabled={testStatus.git === 'testing' || !form.git_url}
            className="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
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
          <Server className="h-4 w-4 text-gray-500" />
          <h5 className="text-sm font-semibold text-gray-900 dark:text-gray-100">ArgoCD</h5>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label className={labelCls}>Server URL</label>
            <input className={inputCls} value={form.argocd_server_url} onChange={(e) => onChange({ argocd_server_url: e.target.value })}
              placeholder="Auto-discovered from cluster" />
            <p className="mt-1 text-[10px] text-gray-400">Auto-filled for in-cluster. Override for external ArgoCD.</p>
          </div>
          <div>
            <label className={labelCls}>Token</label>
            <input className={inputCls} type="password" value={form.argocd_token} onChange={(e) => onChange({ argocd_token: e.target.value })}
              placeholder={isEdit ? 'Leave blank to keep existing' : 'ArgoCD API token'} />
            <p className="mt-1 text-[10px] text-gray-400">ArgoCD account token (e.g. aap-api-user). Falls back to ARGOCD_TOKEN env var.</p>
          </div>
          <div>
            <label className={labelCls}>Namespace</label>
            <input className={inputCls} value={form.argocd_namespace} onChange={(e) => onChange({ argocd_namespace: e.target.value })}
              placeholder="argocd" required />
          </div>
        </div>
        <div className="mt-3 flex items-center gap-3">
          <button type="button" onClick={onTestArgocd} disabled={testStatus.argocd === 'testing'}
            className="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
            {testStatus.argocd === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Server className="h-3 w-3" />}
            Test ArgoCD
          </button>
          {testStatus.argocd === 'ok' && <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400"><CheckCircle className="h-3.5 w-3.5" /> Connected{testStatus.argocdAuth && testStatus.argocdAuth !== 'provided' ? ` (via ${testStatus.argocdAuth})` : ''}</span>}
          {testStatus.argocd === 'error' && <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400"><XCircle className="h-3.5 w-3.5" /> {testStatus.argocdMessage || 'Failed'}</span>}
        </div>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Main component                                                     */
/* ------------------------------------------------------------------ */

export function Connections() {
  const { connections, loading, error, refreshConnections } =
    useConnections()
  const [switchingTo, setSwitchingTo] = useState<string | null>(null)
  const [platformInfo, setPlatformInfo] = useState<PlatformInfo | null>(null)
  const [healthLoading, setHealthLoading] = useState(true)

  // Add form state
  const [showAddForm, setShowAddForm] = useState(false)
  const [addForm, setAddForm] = useState<ConnectionFormData>({ ...emptyForm })
  const [addSaving, setAddSaving] = useState(false)
  const [addError, setAddError] = useState<string | null>(null)
  const [addTestStatus, setAddTestStatus] = useState<TestStatus>({ git: 'idle', argocd: 'idle' })

  // Edit form state
  const [editingName, setEditingName] = useState<string | null>(null)
  const [editForm, setEditForm] = useState<ConnectionFormData>({ ...emptyForm })
  const [editSaving, setEditSaving] = useState(false)
  const [editError, setEditError] = useState<string | null>(null)
  const [editTestStatus, setEditTestStatus] = useState<TestStatus>({ git: 'idle', argocd: 'idle' })

  const fetchHealth = useCallback(() => {
    setHealthLoading(true)
    api
      .health()
      .then((data) => setPlatformInfo(data))
      .catch(() => setPlatformInfo(null))
      .finally(() => setHealthLoading(false))
  }, [])

  useEffect(() => {
    fetchHealth()
  }, [fetchHealth])

  async function handleSwitch(name: string) {
    setSwitchingTo(name)
    try {
      await api.setActiveConnection(name)
      refreshConnections()
    } finally {
      setSwitchingTo(null)
    }
  }

  async function testCredentials(form: ConnectionFormData, which: 'git' | 'argocd' | 'both', setStatus: (s: TestStatus | ((prev: TestStatus) => TestStatus)) => void) {
    const payload = buildPayload(form)
    if (which === 'git' || which === 'both') setStatus(prev => ({ ...prev, git: 'testing', gitMessage: undefined }))
    if (which === 'argocd' || which === 'both') setStatus(prev => ({ ...prev, argocd: 'testing', argocdMessage: undefined }))
    try {
      const res = await api.testCredentials(payload)
      if (which === 'git' || which === 'both') {
        setStatus(prev => ({ ...prev, git: res.git.status === 'ok' ? 'ok' : 'error', gitMessage: res.git.message, gitAuth: res.git.auth }))
      }
      if (which === 'argocd' || which === 'both') {
        setStatus(prev => ({ ...prev, argocd: res.argocd.status === 'ok' ? 'ok' : 'error', argocdMessage: res.argocd.message, argocdAuth: res.argocd.auth }))
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Test failed'
      if (which === 'git' || which === 'both') setStatus(prev => ({ ...prev, git: 'error', gitMessage: msg }))
      if (which === 'argocd' || which === 'both') setStatus(prev => ({ ...prev, argocd: 'error', argocdMessage: msg }))
    }
  }

  async function testAndSave(form: ConnectionFormData, setStatus: (s: TestStatus | ((prev: TestStatus) => TestStatus)) => void, saveFn: () => Promise<void>, setError: (e: string | null) => void, setSaving: (b: boolean) => void, connectionName?: string) {
    setSaving(true)
    setError(null)
    // Auto-test before saving (name included so backend can fill missing tokens from saved connection)
    setStatus({ git: 'testing', argocd: 'testing' })
    try {
      const payload = buildPayload(form, connectionName)
      const res = await api.testCredentials(payload)
      const gitOk = res.git.status === 'ok'
      const argocdOk = res.argocd.status === 'ok'
      setStatus({
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
        setError(`Connection test failed — ${errors.join(', ')}`)
        setSaving(false)
        return
      }
      // Tests passed — save
      await saveFn()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save connection')
    } finally {
      setSaving(false)
    }
  }

  async function handleAddSubmit(e: FormEvent) {
    e.preventDefault()
    await testAndSave(addForm, setAddTestStatus, async () => {
      await api.createConnection(buildPayload(addForm))
      refreshConnections()
      setShowAddForm(false)
      setAddForm({ ...emptyForm })
      setAddTestStatus({ git: 'idle', argocd: 'idle' })
    }, setAddError, setAddSaving)
  }

  function handleEditStart(conn: ConnectionResponse) {
    setEditingName(conn.name)
    setEditForm(formFromConnection(conn))
    setEditError(null)
    setEditTestStatus({ git: 'idle', argocd: 'idle' })
  }

  async function handleEditSubmit(e: FormEvent) {
    e.preventDefault()
    if (!editingName) return
    const name = editingName
    await testAndSave(editForm, setEditTestStatus, async () => {
      await api.updateConnection(name, buildPayload(editForm, name))
      refreshConnections()
      setEditingName(null)
    }, setEditError, setEditSaving, name)
  }

  if (loading) {
    return <LoadingState message="Loading settings..." />
  }

  if (error) {
    return <ErrorState message={error} onRetry={refreshConnections} />
  }

  const activeConn = connections.find((c) => c.is_active) ?? null

  return (
    <div className="space-y-8">
      {/* Header */}
      <div>
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">
          Settings
        </h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Manage connections and view platform information.
        </p>
      </div>

      {/* Active Connections */}
      <section className="space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            Active Connections
          </h3>
          <button
            onClick={async () => {
              const opening = !showAddForm
              setShowAddForm(opening)
              setAddForm({ ...emptyForm })
              setAddError(null)
              setAddTestStatus({ git: 'idle', argocd: 'idle' })
              if (opening) {
                try {
                  const disc = await api.discoverArgocd()
                  if (disc.server_url) {
                    setAddForm(prev => ({ ...prev, argocd_server_url: disc.server_url }))
                  }
                } catch { /* ignore — user can enter manually */ }
              }
            }}
            className="inline-flex items-center gap-1.5 rounded-lg bg-cyan-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm hover:bg-cyan-700 focus:outline-none focus:ring-2 focus:ring-cyan-500 focus:ring-offset-2 dark:bg-cyan-700 dark:hover:bg-cyan-600 dark:focus:ring-offset-gray-900"
          >
            <Plus className="h-4 w-4" />
            Add Connection
          </button>
        </div>

        {/* Add Connection Form */}
        {showAddForm && (
          <form
            onSubmit={handleAddSubmit}
            className="rounded-xl border border-cyan-200 bg-cyan-50/50 p-6 shadow-sm dark:border-cyan-800 dark:bg-cyan-950/20"
          >
            <h4 className="mb-4 text-base font-semibold text-gray-900 dark:text-gray-100">
              New Connection
            </h4>
            <ConnectionFormFields
              form={addForm}
              onChange={(patch) => {
                setAddForm((prev) => ({ ...prev, ...patch }))
                setAddTestStatus({ git: 'idle', argocd: 'idle' })
              }}
              isEdit={false}
              testStatus={addTestStatus}
              onTestGit={() => testCredentials(addForm, 'git', setAddTestStatus)}
              onTestArgocd={() => testCredentials(addForm, 'argocd', setAddTestStatus)}
            />
            {addError && (
              <p className="mt-3 text-sm text-red-600 dark:text-red-400">
                {addError}
              </p>
            )}
            <div className="mt-4 flex items-center gap-3">
              <button
                type="submit"
                disabled={addSaving}
                className="inline-flex items-center gap-1.5 rounded-lg bg-cyan-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-cyan-700 disabled:opacity-50 dark:bg-cyan-700 dark:hover:bg-cyan-600"
              >
                {addSaving && (
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                )}
                Save
              </button>
              <button
                type="button"
                onClick={() => setShowAddForm(false)}
                className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200"
              >
                Cancel
              </button>
            </div>
          </form>
        )}

        {connections.length === 0 ? (
          <p className="py-8 text-center text-gray-400 dark:text-gray-500">
            No connections configured.
          </p>
        ) : (
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {connections.map((conn) => (
              <div key={conn.name}>
                <div
                  className={`rounded-xl border bg-white p-6 shadow-sm dark:bg-gray-800 ${
                    conn.is_active
                      ? 'border-cyan-500 ring-2 ring-cyan-100 dark:ring-cyan-900/50'
                      : 'border-gray-200 dark:border-gray-700'
                  }`}
                >
                  {/* Name + badges */}
                  <div className="mb-4 flex items-center justify-between">
                    <div className="flex flex-wrap items-center gap-2">
                      <h4 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
                        {conn.name}
                      </h4>
                      {conn.is_default && (
                        <Badge variant="secondary" className="text-xs">
                          Default
                        </Badge>
                      )}
                      {conn.is_active && (
                        <Badge className="bg-cyan-100 text-xs text-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400">
                          Active
                        </Badge>
                      )}
                    </div>

                    <div className="flex items-center gap-2">
                      <button
                        onClick={() => handleEditStart(conn)}
                        className="inline-flex items-center gap-1 text-xs font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
                      >
                        <Pencil className="h-3 w-3" />
                        Edit
                      </button>
                      {!conn.is_active && (
                        <button
                          onClick={() => handleSwitch(conn.name)}
                          disabled={switchingTo === conn.name}
                          className="text-xs font-medium text-cyan-600 hover:text-cyan-700 disabled:opacity-50 dark:text-cyan-400 dark:hover:text-cyan-300"
                        >
                          {switchingTo === conn.name ? (
                            <Loader2 className="inline h-3 w-3 animate-spin" />
                          ) : (
                            'Switch'
                          )}
                        </button>
                      )}
                    </div>
                  </div>

                  {/* Details */}
                  <dl className="space-y-2 text-sm">
                    <div className="flex items-center justify-between">
                      <dt className="flex items-center gap-1.5 text-gray-500 dark:text-gray-400">
                        <GitBranch className="h-3.5 w-3.5" />
                        Git Provider
                      </dt>
                      <dd className="font-medium capitalize text-gray-900 dark:text-gray-100">
                        {conn.git_provider}
                      </dd>
                    </div>
                    <div className="flex items-center justify-between">
                      <dt className="flex items-center gap-1.5 text-gray-500 dark:text-gray-400">
                        <GitBranch className="h-3.5 w-3.5" />
                        Repository
                      </dt>
                      <dd className="font-mono text-xs text-gray-700 dark:text-gray-300">
                        {conn.git_repo_identifier}
                      </dd>
                    </div>
                    <div className="flex items-start justify-between gap-2">
                      <dt className="flex shrink-0 items-center gap-1.5 text-gray-500 dark:text-gray-400">
                        <Server className="h-3.5 w-3.5" />
                        ArgoCD URL
                      </dt>
                      <dd className="break-all text-right font-mono text-xs text-gray-700 dark:text-gray-300">
                        {conn.argocd_server_url}
                      </dd>
                    </div>
                    <div className="flex items-center justify-between">
                      <dt className="flex items-center gap-1.5 text-gray-500 dark:text-gray-400">
                        <Shield className="h-3.5 w-3.5" />
                        Namespace
                      </dt>
                      <dd className="font-mono text-xs text-gray-700 dark:text-gray-300">
                        {conn.argocd_namespace}
                      </dd>
                    </div>
                  </dl>
                </div>

                {/* Inline Edit Form */}
                {editingName === conn.name && (
                  <form
                    onSubmit={handleEditSubmit}
                    className="mt-2 rounded-xl border border-amber-200 bg-amber-50/50 p-6 shadow-sm dark:border-amber-800 dark:bg-amber-950/20"
                  >
                    <div className="mb-4 flex items-center justify-between">
                      <h4 className="text-base font-semibold text-gray-900 dark:text-gray-100">
                        Edit Connection
                      </h4>
                      <button
                        type="button"
                        onClick={() => setEditingName(null)}
                        className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
                      >
                        <X className="h-4 w-4" />
                      </button>
                    </div>
                    <ConnectionFormFields
                      form={editForm}
                      onChange={(patch) => {
                        setEditForm((prev) => ({ ...prev, ...patch }))
                        setEditTestStatus({ git: 'idle', argocd: 'idle' })
                      }}
                      isEdit={true}
                      testStatus={editTestStatus}
                      onTestGit={() => testCredentials(editForm, 'git', setEditTestStatus)}
                      onTestArgocd={() => testCredentials(editForm, 'argocd', setEditTestStatus)}
                    />
                    {editError && (
                      <p className="mt-3 text-sm text-red-600 dark:text-red-400">
                        {editError}
                      </p>
                    )}
                    <div className="mt-4 flex items-center gap-3">
                      <button
                        type="submit"
                        disabled={editSaving}
                        className="inline-flex items-center gap-1.5 rounded-lg bg-amber-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-amber-700 disabled:opacity-50 dark:bg-amber-700 dark:hover:bg-amber-600"
                      >
                        {editSaving && (
                          <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        )}
                        Update
                      </button>
                      <button
                        type="button"
                        onClick={() => setEditingName(null)}
                        className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200"
                      >
                        Cancel
                      </button>
                    </div>
                  </form>
                )}
              </div>
            ))}
          </div>
        )}
      </section>

      {/* Platform Info */}
      <section className="space-y-4">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Platform Info
        </h3>
        <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
          <dl className="grid grid-cols-1 gap-6 sm:grid-cols-2">
            {/* Deployment Mode */}
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400">
                <Monitor className="h-3.5 w-3.5" />
                Deployment Mode
              </dt>
              <dd className="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">
                {platformInfo?.mode ?? 'Unknown'}
              </dd>
            </div>

            {/* API Health */}
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400">
                <Activity className="h-3.5 w-3.5" />
                API Health
              </dt>
              <dd className="mt-1 flex items-center gap-2 text-sm font-medium">
                {healthLoading ? (
                  <Loader2 className="h-3.5 w-3.5 animate-spin text-gray-400 dark:text-gray-500" />
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

            {/* Git Provider */}
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400">
                <GitBranch className="h-3.5 w-3.5" />
                Git Provider
              </dt>
              <dd className="mt-1 text-sm font-medium capitalize text-gray-900 dark:text-gray-100">
                {activeConn?.git_provider ?? 'N/A'}
              </dd>
            </div>

            {/* ArgoCD Server */}
            <div>
              <dt className="flex items-center gap-1.5 text-sm text-gray-500 dark:text-gray-400">
                <Globe className="h-3.5 w-3.5" />
                ArgoCD Server
              </dt>
              <dd className="mt-1 break-all font-mono text-sm text-gray-900 dark:text-gray-100">
                {activeConn?.argocd_server_url ?? 'N/A'}
              </dd>
            </div>
          </dl>
        </div>
      </section>

      {/* AI Configuration */}
      <section className="space-y-4">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          AI Configuration
        </h3>
        <AIConfigSection />
      </section>

      {/* Datadog Metrics */}
      <section className="space-y-4">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Datadog Metrics
        </h3>
        <DatadogConfigSection />
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
    <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <div className={`flex h-10 w-10 items-center justify-center rounded-lg ${isEnabled ? 'bg-purple-100 dark:bg-purple-900/30' : 'bg-gray-100 dark:bg-gray-700'}`}>
            <Sparkles className={`h-5 w-5 ${isEnabled ? 'text-purple-600 dark:text-purple-400' : 'text-gray-400'}`} />
          </div>
          <div>
            <h4 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
              AI Analysis
              {loading ? '' : isEnabled ? (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                  <span className="inline-block h-1.5 w-1.5 rounded-full bg-green-500" />
                  Active
                </span>
              ) : (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-500 dark:bg-gray-700 dark:text-gray-400">
                  Not Configured
                </span>
              )}
            </h4>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
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
                className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
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
            <h5 className="text-sm font-semibold text-gray-900 dark:text-gray-100">Configure AI Provider</h5>
            <button onClick={() => setShowForm(false)} className="text-gray-400 hover:text-gray-600 dark:hover:text-gray-200">
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
              className="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
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
            <button onClick={() => setShowForm(false)} className="rounded-lg px-4 py-2 text-sm font-medium text-gray-600 hover:text-gray-800 dark:text-gray-400 dark:hover:text-gray-200">
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function DatadogConfigSection() {
  const [status, setStatus] = useState<{ enabled: boolean; site: string } | null>(null)
  const [loading, setLoading] = useState(true)
  const [testResult, setTestResult] = useState<string | null>(null)
  const [testing, setTesting] = useState(false)

  const fetchStatus = useCallback(() => {
    setLoading(true)
    api.getDatadogStatus()
      .then(setStatus)
      .catch(() => setStatus(null))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    fetchStatus()
  }, [fetchStatus])

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      // Test by fetching metrics for a known namespace
      await api.getDatadogNamespaceMetrics('default')
      setTestResult('Datadog connection successful')
    } catch (err) {
      setTestResult(err instanceof Error ? err.message : 'Connection failed')
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="rounded-xl border border-gray-200 bg-white p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <div className={`flex h-10 w-10 items-center justify-center rounded-lg ${
            status?.enabled
              ? 'bg-indigo-100 dark:bg-indigo-900/30'
              : 'bg-gray-100 dark:bg-gray-700'
          }`}>
            <BarChart2 className={`h-5 w-5 ${status?.enabled ? 'text-indigo-600 dark:text-indigo-400' : 'text-gray-400'}`} />
          </div>
          <div>
            <h4 className="text-sm font-semibold text-gray-900 dark:text-gray-100">
              Datadog Metrics
              {loading ? '' : status?.enabled ? (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                  <span className="inline-block h-1.5 w-1.5 rounded-full bg-green-500" />
                  Active
                </span>
              ) : (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-500 dark:bg-gray-700 dark:text-gray-400">
                  Disabled
                </span>
              )}
            </h4>
            <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {status?.enabled
                ? `Connected to ${status.site}`
                : 'Real-time K8s resource metrics from Datadog (CPU, memory, pods)'
              }
            </p>
          </div>
        </div>
        {status?.enabled && (
          <button
            onClick={handleTest}
            disabled={testing}
            className="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            {testing ? 'Testing...' : 'Test Connection'}
          </button>
        )}
      </div>

      {testResult && (
        <div className={`mt-3 flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${
          testResult.includes('successful')
            ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'
            : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400'
        }`}>
          {testResult.includes('successful') ? (
            <CheckCircle className="h-3.5 w-3.5" />
          ) : (
            <XCircle className="h-3.5 w-3.5" />
          )}
          {testResult}
        </div>
      )}

      {!loading && !status?.enabled && (
        <div className="mt-4 rounded-lg bg-gray-50 p-4 dark:bg-gray-900">
          <p className="text-sm font-medium text-gray-700 dark:text-gray-300">How to enable Datadog metrics</p>
          <p className="mt-2 text-xs text-gray-600 dark:text-gray-400">
            Add the following to{' '}
            <code className="rounded bg-gray-200 px-1 dark:bg-gray-700">.env.secrets</code> and restart:
          </p>
          <pre className="mt-2 rounded-lg bg-gray-900 p-2 font-mono text-xs text-gray-300">
{`DATADOG_API_KEY=your-datadog-api-key
DATADOG_APP_KEY=your-datadog-app-key
DATADOG_SITE=datadoghq.com`}
          </pre>
          <p className="mt-3 text-xs text-gray-500 dark:text-gray-400">
            Then restart the platform with <code className="rounded bg-gray-200 px-1 dark:bg-gray-700">make dev</code>
          </p>
        </div>
      )}
    </div>
  )
}
