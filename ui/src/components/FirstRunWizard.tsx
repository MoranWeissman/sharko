import { useState, useCallback, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  GitBranch,
  Server,
  FolderGit2,
  Loader2,
  CheckCircle,
  XCircle,
  ArrowRight,
  Sparkles,
  Clock,
  Circle,
} from 'lucide-react'
import { api, initRepo, getOperation, operationHeartbeat } from '@/services/api'
import type { OperationStep } from '@/services/api'
import { useConnections } from '@/hooks/useConnections'

/* ------------------------------------------------------------------ */
/*  Shared styles                                                       */
/* ------------------------------------------------------------------ */

const labelCls = 'block text-sm font-medium text-[#0a3a5a] dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm placeholder:text-[#3a6a8a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-gray-500'

/* ------------------------------------------------------------------ */
/*  Types                                                               */
/* ------------------------------------------------------------------ */

interface WizardForm {
  git_url: string
  git_token: string
  argocd_server_url: string
  argocd_token: string
  argocd_namespace: string
}

const emptyForm: WizardForm = {
  git_url: '',
  git_token: '',
  argocd_server_url: '',
  argocd_token: '',
  argocd_namespace: 'argocd',
}

type TestState = 'idle' | 'testing' | 'ok' | 'error'

interface TestStatus {
  git: TestState
  argocd: TestState
  gitMessage?: string
  argocdMessage?: string
}

/* ------------------------------------------------------------------ */
/*  Step indicator                                                      */
/* ------------------------------------------------------------------ */

function StepIndicator({ current, total }: { current: number; total: number }) {
  return (
    <div className="flex items-center gap-2">
      {Array.from({ length: total }, (_, i) => (
        <div key={i} className="flex items-center gap-2">
          <div
            className={`h-2.5 w-2.5 rounded-full transition-all ${
              i + 1 < current
                ? 'bg-teal-500'
                : i + 1 === current
                ? 'bg-[#0a2a4a] dark:bg-blue-400 scale-125'
                : 'bg-[#bee0ff] dark:bg-gray-700'
            }`}
          />
          {i < total - 1 && (
            <div
              className={`h-0.5 w-6 rounded-full transition-all ${
                i + 1 < current ? 'bg-teal-400' : 'bg-[#bee0ff] dark:bg-gray-700'
              }`}
            />
          )}
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Step 1: Welcome                                                     */
/* ------------------------------------------------------------------ */

function StepWelcome({ onNext }: { onNext: () => void }) {
  return (
    <div className="flex flex-col items-center text-center gap-6 py-4">
      <img
        src="/sharko-mascot.png"
        alt="Sharko mascot"
        className="h-32 w-auto object-contain"
      />
      <div className="space-y-2">
        <h2
          className="text-3xl text-[#0a2a4a] dark:text-gray-100"
          style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}
        >
          Welcome to Sharko!
        </h2>
        <p className="text-[#2a5a7a] dark:text-gray-400 max-w-md">
          Sharko manages your Kubernetes addons through ArgoCD and Git. Let's
          connect your Git repository and ArgoCD instance to get started.
        </p>
      </div>
      <button
        onClick={onNext}
        className="inline-flex items-center gap-2 rounded-full bg-[#0a2a4a] px-6 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-[#14466e] focus:outline-none focus:ring-2 focus:ring-[#6aade0] focus:ring-offset-2"
      >
        Get Started
        <ArrowRight className="h-4 w-4" />
      </button>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Step 2: Git Connection                                              */
/* ------------------------------------------------------------------ */

function StepGit({
  form,
  onChange,
  testStatus,
  onTest,
  onNext,
  onBack,
}: {
  form: WizardForm
  onChange: (patch: Partial<WizardForm>) => void
  testStatus: TestStatus
  onTest: () => void
  onNext: () => void
  onBack: () => void
}) {
  const canNext = testStatus.git === 'ok'

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-2">
        <GitBranch className="h-5 w-5 text-[#1a3d5c] dark:text-blue-400" />
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          Git Repository
        </h3>
      </div>
      <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
        Connect Sharko to your Git repository where addon configuration will be stored.
      </p>

      <div className="space-y-4">
        <div>
          <label className={labelCls}>Repository URL</label>
          <input
            className={inputCls}
            value={form.git_url}
            onChange={(e) => onChange({ git_url: e.target.value })}
            placeholder="https://github.com/your-org/your-repo"
          />
          <p className="mt-1 text-[10px] text-[#3a6a8a]">
            GitHub, GitHub Enterprise, or Azure DevOps (auto-detected from URL)
          </p>
        </div>

        <div>
          <label className={labelCls}>Access Token</label>
          <input
            className={inputCls}
            type="password"
            value={form.git_token}
            onChange={(e) => onChange({ git_token: e.target.value })}
            placeholder="Personal access token (PAT)"
          />
          <p className="mt-1 text-[10px] text-[#3a6a8a]">
            Needs read/write access to the repository.
          </p>
        </div>
      </div>

      <div className="flex items-center gap-3 pt-1">
        <button
          type="button"
          onClick={onTest}
          disabled={testStatus.git === 'testing' || !form.git_url}
          className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
        >
          {testStatus.git === 'testing' ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <GitBranch className="h-3 w-3" />
          )}
          Test Connection
        </button>
        {testStatus.git === 'ok' && (
          <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
            <CheckCircle className="h-3.5 w-3.5" /> Connected
          </span>
        )}
        {testStatus.git === 'error' && (
          <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
            <XCircle className="h-3.5 w-3.5" /> {testStatus.gitMessage || 'Failed'}
          </span>
        )}
      </div>

      <div className="flex items-center justify-between pt-2 border-t border-[#bee0ff] dark:border-gray-700">
        <button
          type="button"
          onClick={onBack}
          className="rounded-lg px-4 py-2 text-sm font-medium text-[#1a4a6a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200"
        >
          Back
        </button>
        <button
          type="button"
          onClick={onNext}
          disabled={!canNext}
          className="inline-flex items-center gap-2 rounded-full bg-[#0a2a4a] px-5 py-2 text-sm font-semibold text-white transition-colors hover:bg-[#14466e] disabled:opacity-40 focus:outline-none focus:ring-2 focus:ring-[#6aade0]"
        >
          Next
          <ArrowRight className="h-4 w-4" />
        </button>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Step 3: ArgoCD Connection                                          */
/* ------------------------------------------------------------------ */

function StepArgoCD({
  form,
  onChange,
  testStatus,
  onTest,
  onSave,
  saving,
  saveError,
  onBack,
}: {
  form: WizardForm
  onChange: (patch: Partial<WizardForm>) => void
  testStatus: TestStatus
  onTest: () => void
  onSave: () => void
  saving: boolean
  saveError: string | null
  onBack: () => void
}) {
  const canSave = testStatus.argocd === 'ok'

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-2">
        <Server className="h-5 w-5 text-[#1a3d5c] dark:text-blue-400" />
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          ArgoCD
        </h3>
      </div>
      <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
        Connect to your ArgoCD instance. If Sharko is running in-cluster, the URL will be auto-discovered.
      </p>

      <div className="space-y-4">
        <div>
          <label className={labelCls}>ArgoCD URL</label>
          <input
            className={inputCls}
            value={form.argocd_server_url}
            onChange={(e) => onChange({ argocd_server_url: e.target.value })}
            placeholder="Auto-discovered from cluster"
          />
          {form.argocd_server_url.includes('.svc.cluster.local') && form.argocd_server_url.startsWith('https://') && (
            <p className="mt-1 text-[10px] text-amber-600 dark:text-amber-400">
              ⚠ In-cluster services typically use http://, not https://
            </p>
          )}
          {!(form.argocd_server_url.includes('.svc.cluster.local') && form.argocd_server_url.startsWith('https://')) && (
            <p className="mt-1 text-[10px] text-[#3a6a8a]">
              Leave blank for in-cluster auto-discovery. Override for external ArgoCD.
            </p>
          )}
        </div>

        <div>
          <label className={labelCls}>ArgoCD Token</label>
          <input
            className={inputCls}
            type="password"
            value={form.argocd_token}
            onChange={(e) => onChange({ argocd_token: e.target.value })}
            placeholder="ArgoCD API token (optional if in-cluster RBAC)"
          />
          <p className="mt-1 text-[10px] text-[#3a6a8a]">
            Falls back to ARGOCD_TOKEN env var if not provided.
          </p>
        </div>

        <div>
          <label className={labelCls}>ArgoCD Namespace</label>
          <input
            className={inputCls}
            value={form.argocd_namespace}
            onChange={(e) => onChange({ argocd_namespace: e.target.value })}
            placeholder="argocd"
          />
        </div>
      </div>

      <div className="flex items-center gap-3 pt-1">
        <button
          type="button"
          onClick={onTest}
          disabled={testStatus.argocd === 'testing'}
          className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
        >
          {testStatus.argocd === 'testing' ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : (
            <Server className="h-3 w-3" />
          )}
          Test Connection
        </button>
        {testStatus.argocd === 'ok' && (
          <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400">
            <CheckCircle className="h-3.5 w-3.5" /> Connected
          </span>
        )}
        {testStatus.argocd === 'error' && (
          <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
            <XCircle className="h-3.5 w-3.5" /> {testStatus.argocdMessage || 'Failed'}
          </span>
        )}
      </div>

      {saveError && (
        <p className="text-sm text-red-600 dark:text-red-400">{saveError}</p>
      )}

      <div className="flex items-center justify-between pt-2 border-t border-[#bee0ff] dark:border-gray-700">
        <button
          type="button"
          onClick={onBack}
          className="rounded-lg px-4 py-2 text-sm font-medium text-[#1a4a6a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200"
        >
          Back
        </button>
        <button
          type="button"
          onClick={onSave}
          disabled={!canSave || saving}
          className="inline-flex items-center gap-2 rounded-full bg-teal-600 px-5 py-2 text-sm font-semibold text-white transition-colors hover:bg-teal-700 disabled:opacity-40 focus:outline-none focus:ring-2 focus:ring-teal-400"
        >
          {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          Save & Continue
          <ArrowRight className="h-4 w-4" />
        </button>
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Step 4: Initialize Repository                                      */
/* ------------------------------------------------------------------ */

function StepInit({ onDone }: { onDone: () => void }) {
  const [state, setState] = useState<'idle' | 'running' | 'done' | 'error'>('idle')
  const [error, setError] = useState<string | null>(null)
  const [operationId, setOperationId] = useState<string | null>(null)
  const [steps, setSteps] = useState<OperationStep[]>([])
  const [operationStatus, setOperationStatus] = useState<string>('idle')
  const [prUrl, setPrUrl] = useState<string | null>(null)

  const handleInit = async (autoMerge: boolean) => {
    setState('running')
    setError(null)
    setSteps([])
    setOperationStatus('idle')
    setPrUrl(null)
    try {
      const res = await initRepo({ bootstrap_argocd: true, auto_merge: autoMerge })
      if (res?.operation_id) {
        setOperationId(res.operation_id)
      } else {
        // Legacy synchronous response fallback
        const url = res?.pr_url || res?.pull_request_url || null
        setPrUrl(url)
        setOperationStatus('completed')
        setState('done')
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to initialize repository')
      setState('error')
    }
  }

  useEffect(() => {
    if (!operationId) return

    const pollInterval = setInterval(async () => {
      try {
        const status = await getOperation(operationId)
        setSteps(status.steps || [])
        setOperationStatus(status.status)
        if (status.pr_url) setPrUrl(status.pr_url)

        if (
          status.status === 'completed' ||
          status.status === 'failed' ||
          status.status === 'cancelled'
        ) {
          clearInterval(pollInterval)
          clearInterval(heartbeatInterval)
          if (status.status === 'completed') {
            setState('done')
          } else {
            setError(status.error || `Operation ${status.status}`)
            setState('error')
          }
        }
      } catch {
        // ignore transient poll errors
      }
    }, 2000)

    const heartbeatInterval = setInterval(() => {
      operationHeartbeat(operationId)
    }, 15000)

    return () => {
      clearInterval(pollInterval)
      clearInterval(heartbeatInterval)
    }
  }, [operationId])

  return (
    <div className="space-y-5">
      <div className="flex items-center gap-2">
        <FolderGit2 className="h-5 w-5 text-[#1a3d5c] dark:text-blue-400" />
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">
          Initialize Repository
        </h3>
      </div>

      <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-5 dark:bg-gray-800">
        <p className="text-sm text-[#0a2a4a] dark:text-gray-200 font-medium mb-1">
          Your repository appears to be empty.
        </p>
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
          Sharko can initialize it with the standard template — creating the folder
          structure, ApplicationSet, and a PR for your review.
        </p>
      </div>

      {state === 'idle' && (
        <div className="flex flex-col gap-3 sm:flex-row">
          <button
            type="button"
            onClick={() => handleInit(true)}
            className="inline-flex items-center gap-2 rounded-full bg-[#0a2a4a] px-5 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-[#14466e] focus:outline-none focus:ring-2 focus:ring-[#6aade0]"
          >
            <Sparkles className="h-4 w-4" />
            Initialize &amp; Auto-merge
          </button>
          <button
            type="button"
            onClick={() => handleInit(false)}
            className="inline-flex items-center gap-2 rounded-full border border-[#5a9dd0] bg-[#f0f7ff] px-5 py-2.5 text-sm font-semibold text-[#0a3a5a] transition-colors hover:bg-[#d6eeff] focus:outline-none focus:ring-2 focus:ring-[#6aade0] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:hover:bg-gray-700"
          >
            <FolderGit2 className="h-4 w-4" />
            Initialize (manual PR review)
          </button>
        </div>
      )}

      {/* Step-by-step progress */}
      {state === 'running' && steps.length === 0 && (
        <div className="flex items-center gap-2 text-sm text-[#2a5a7a] dark:text-gray-400">
          <Loader2 className="h-4 w-4 animate-spin" />
          Starting initialization…
        </div>
      )}

      {steps.length > 0 && (
        <div className="space-y-2">
          {steps.map((step, i) => (
            <div key={i} className="flex items-center gap-2 text-sm">
              {step.status === 'completed' && <CheckCircle className="h-4 w-4 text-green-600 shrink-0" />}
              {step.status === 'running' && <Loader2 className="h-4 w-4 animate-spin text-[#2a5a7a] shrink-0" />}
              {step.status === 'waiting' && <Clock className="h-4 w-4 text-amber-500 shrink-0" />}
              {step.status === 'pending' && <Circle className="h-4 w-4 text-[#bee0ff] shrink-0" />}
              {step.status === 'failed' && <XCircle className="h-4 w-4 text-red-500 shrink-0" />}
              <span className={step.status === 'pending' ? 'text-[#3a6a8a]' : 'text-[#0a2a4a] dark:text-gray-200'}>
                {step.name}
              </span>
              {step.message && (
                <span className="text-xs text-[#3a6a8a] dark:text-gray-400">— {step.message}</span>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Waiting for PR merge */}
      {operationStatus === 'waiting' && prUrl && (
        <div className="rounded-xl ring-2 ring-amber-300 bg-amber-50 p-4 dark:bg-amber-900/20">
          <p className="text-sm font-medium text-amber-800 dark:text-amber-300">
            Waiting for PR to be merged…
          </p>
          <a
            href={prUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="text-xs text-[#1a4a8a] underline hover:text-[#0a3a6a] dark:text-blue-400"
          >
            Review and merge the PR →
          </a>
        </div>
      )}

      {/* Waiting without a PR URL yet */}
      {operationStatus === 'waiting' && !prUrl && (
        <div className="rounded-xl ring-2 ring-amber-300 bg-amber-50 p-4 dark:bg-amber-900/20">
          <p className="text-sm font-medium text-amber-800 dark:text-amber-300">
            Waiting for PR to be merged…
          </p>
        </div>
      )}

      {state === 'done' && (
        <div className="space-y-3">
          <div className="flex items-start gap-2 text-sm text-green-700 dark:text-green-400">
            <CheckCircle className="h-4 w-4 mt-0.5 shrink-0" />
            <div>
              <p>Repository initialized successfully.</p>
              {prUrl && (
                <a
                  href={prUrl}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="mt-1 inline-block text-xs text-[#1a4a8a] underline hover:text-[#0a3a6a] dark:text-blue-400 dark:hover:text-blue-300"
                >
                  View Pull Request
                </a>
              )}
            </div>
          </div>
          <button
            type="button"
            onClick={onDone}
            className="inline-flex items-center gap-2 rounded-full bg-teal-600 px-6 py-2.5 text-sm font-semibold text-white transition-colors hover:bg-teal-700 focus:outline-none focus:ring-2 focus:ring-teal-400"
          >
            Go to Dashboard
            <ArrowRight className="h-4 w-4" />
          </button>
        </div>
      )}

      {state === 'error' && (
        <div className="space-y-3">
          <div className="flex items-start gap-2 text-sm text-red-600 dark:text-red-400">
            <XCircle className="h-4 w-4 mt-0.5 shrink-0" />
            <p>{error}</p>
          </div>
          <div className="flex items-center gap-3">
            <button
              type="button"
              onClick={() => setState('idle')}
              className="inline-flex items-center gap-2 rounded-full bg-[#0a2a4a] px-5 py-2 text-sm font-semibold text-white transition-colors hover:bg-[#14466e]"
            >
              Retry
            </button>
            <button
              type="button"
              onClick={onDone}
              className="rounded-lg px-4 py-2 text-sm font-medium text-[#1a4a6a] hover:text-[#0a3a5a] dark:text-gray-400"
            >
              Skip, go to Dashboard
            </button>
          </div>
        </div>
      )}

      {state === 'idle' && (
        <button
          type="button"
          onClick={onDone}
          className="inline-flex items-center gap-2 rounded-lg border border-[#5a9dd0] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
        >
          Skip — I'll initialize manually
        </button>
      )}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Main wizard                                                         */
/* ------------------------------------------------------------------ */

export function FirstRunWizard() {
  const navigate = useNavigate()
  const { refreshConnections } = useConnections()
  const [step, setStep] = useState(1)
  const [form, setForm] = useState<WizardForm>({ ...emptyForm })
  const [testStatus, setTestStatus] = useState<TestStatus>({ git: 'idle', argocd: 'idle' })
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  const patchForm = (patch: Partial<WizardForm>) => {
    setForm((prev) => ({ ...prev, ...patch }))
    // reset test statuses on change
    setTestStatus({ git: 'idle', argocd: 'idle' })
  }

  const buildPayload = () => ({
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
  })

  const testGit = useCallback(async () => {
    setTestStatus((prev) => ({ ...prev, git: 'testing', gitMessage: undefined }))
    try {
      const res = await api.testCredentials(buildPayload())
      setTestStatus((prev) => ({
        ...prev,
        git: res.git.status === 'ok' ? 'ok' : 'error',
        gitMessage: res.git.message,
      }))
    } catch (err) {
      setTestStatus((prev) => ({
        ...prev,
        git: 'error',
        gitMessage: err instanceof Error ? err.message : 'Test failed',
      }))
    }
  }, [form])  // eslint-disable-line react-hooks/exhaustive-deps

  const testArgocd = useCallback(async () => {
    setTestStatus((prev) => ({ ...prev, argocd: 'testing', argocdMessage: undefined }))
    try {
      const res = await api.testCredentials(buildPayload())
      setTestStatus((prev) => ({
        ...prev,
        argocd: res.argocd.status === 'ok' ? 'ok' : 'error',
        argocdMessage: res.argocd.message,
      }))
    } catch (err) {
      setTestStatus((prev) => ({
        ...prev,
        argocd: 'error',
        argocdMessage: err instanceof Error ? err.message : 'Test failed',
      }))
    }
  }, [form])  // eslint-disable-line react-hooks/exhaustive-deps

  const handleSaveAndContinue = useCallback(async () => {
    setSaving(true)
    setSaveError(null)
    try {
      await api.createConnection(buildPayload())
      // DON'T call refreshConnections here — it causes App.tsx to re-render,
      // unmounting the wizard before setStep(4) can execute.
      // refreshConnections is called in handleDone instead.
      setStep(4)
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Failed to save connection')
    } finally {
      setSaving(false)
    }
  }, [form])  // eslint-disable-line react-hooks/exhaustive-deps

  const handleDone = useCallback(() => {
    refreshConnections()
    navigate('/dashboard')
  }, [navigate, refreshConnections])

  // On step 3, try to auto-discover ArgoCD URL
  const handleGoToStep3 = useCallback(async () => {
    setStep(3)
    if (!form.argocd_server_url) {
      try {
        const disc = await api.discoverArgocd()
        if (disc.server_url) {
          let url = disc.server_url
          // In-cluster services use HTTP, not HTTPS
          if (url.includes('.svc.cluster.local')) {
            url = url.replace('https://', 'http://')
          }
          setForm((prev) => ({ ...prev, argocd_server_url: url }))
        }
      } catch { /* ignore */ }
    }
  }, [form.argocd_server_url])

  const stepLabels = ['Welcome', 'Git', 'ArgoCD', 'Initialize']

  return (
    <div className="flex min-h-screen items-center justify-center bg-[#bee0ff] dark:bg-gray-950 p-4">
      <div className="w-full max-w-lg">
        {/* Card */}
        <div className="rounded-2xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-xl dark:bg-gray-900 overflow-hidden">
          {/* Top bar */}
          <div className="bg-[#1a3d5c] px-6 py-4 flex items-center justify-between">
            <div className="flex items-center gap-2">
              <img src="/sharko-mascot.png" alt="" className="h-8 w-auto" />
              <span
                className="text-lg text-blue-300"
                style={{ fontFamily: '"Quicksand", sans-serif', fontWeight: 700 }}
              >
                Sharko
              </span>
            </div>
            <StepIndicator current={step} total={4} />
          </div>

          {/* Step label */}
          <div className="border-b border-[#bee0ff] dark:border-gray-800 bg-[#e8f4ff] dark:bg-gray-800/50 px-6 py-2">
            <p className="text-xs font-semibold uppercase tracking-wider text-[#3a6a8a] dark:text-gray-400">
              Step {step} of {stepLabels.length} — {stepLabels[step - 1]}
            </p>
          </div>

          {/* Content */}
          <div className="p-6">
            {step === 1 && <StepWelcome onNext={() => setStep(2)} />}
            {step === 2 && (
              <StepGit
                form={form}
                onChange={patchForm}
                testStatus={testStatus}
                onTest={testGit}
                onNext={handleGoToStep3}
                onBack={() => setStep(1)}
              />
            )}
            {step === 3 && (
              <StepArgoCD
                form={form}
                onChange={patchForm}
                testStatus={testStatus}
                onTest={testArgocd}
                onSave={handleSaveAndContinue}
                saving={saving}
                saveError={saveError}
                onBack={() => setStep(2)}
              />
            )}
            {step === 4 && <StepInit onDone={handleDone} />}
          </div>
        </div>

        <p className="mt-4 text-center text-xs text-[#3a6a8a] dark:text-gray-600">
          You can always update connections later in Settings.
        </p>
      </div>
    </div>
  )
}
