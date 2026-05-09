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
  X,
  ChevronDown,
  ChevronRight,
  KeyRound,
} from 'lucide-react'
import {
  api,
  initRepo,
  getOperation,
  operationHeartbeat,
  isUnauthorizedError,
} from '@/services/api'
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
  provider_type: '' | 'aws-sm' | 'k8s-secrets'
  provider_region: string
  provider_prefix: string
}

const emptyForm: WizardForm = {
  git_url: '',
  git_token: '',
  argocd_server_url: '',
  argocd_token: '',
  argocd_namespace: 'argocd',
  provider_type: '',
  provider_region: '',
  provider_prefix: '',
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
  onClearConfig,
  hasSavedToken,
}: {
  form: WizardForm
  onChange: (patch: Partial<WizardForm>) => void
  testStatus: TestStatus
  onTest: () => void
  onNext: () => void
  onBack: () => void
  onClearConfig?: () => void
  // V124-17 / BUG-040: when an existing connection is loaded into the form
  // (resume mode), the password input renders empty by design — we never
  // re-display saved secrets. The previous "Personal access token (PAT)"
  // placeholder made it look like the user's saved credential had been
  // wiped. Surface the saved-credential affordance instead so the user
  // knows blank-submit preserves the saved token (PUT /connections/{name}
  // does the merge server-side, see internal/api/connections.go).
  hasSavedToken?: boolean
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
            placeholder={
              hasSavedToken
                ? '•••••• (saved — leave blank to keep, or enter new value to replace)'
                : 'Personal access token (PAT)'
            }
          />
          <p className="mt-1 text-[10px] text-[#3a6a8a]">
            {hasSavedToken
              ? 'Submitting blank keeps your saved credential. Enter a new value to replace it.'
              : 'Needs read/write access to the repository.'}
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

      {/* V124-16 / BUG-037: when the wizard runs in resume mode (existing
          connection loaded into the form), expose a "clear everything and
          start over" escape hatch. Only rendered when the parent wires
          onClearConfig — i.e. only for the resume-mode flow, not the
          fresh-install flow where there's nothing to clear. */}
      {onClearConfig && (
        <div className="pt-2 border-t border-[#bee0ff] dark:border-gray-700">
          <button
            type="button"
            onClick={onClearConfig}
            className="text-xs font-medium text-red-600 underline-offset-2 hover:underline dark:text-red-400"
          >
            Clear all configuration and start over
          </button>
          <p className="mt-1 text-[10px] text-[#3a6a8a] dark:text-gray-500">
            Removes the saved connection so you can configure Sharko from scratch.
          </p>
        </div>
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
  hasSavedToken,
}: {
  form: WizardForm
  onChange: (patch: Partial<WizardForm>) => void
  testStatus: TestStatus
  onTest: () => void
  onSave: () => void
  saving: boolean
  saveError: string | null
  onBack: () => void
  // V124-17 / BUG-040: same saved-credential affordance as StepGit. The
  // ArgoCD token input renders empty in resume mode by design; this
  // placeholder + helper text make the "blank-submit preserves saved
  // value" semantic visible to the user.
  hasSavedToken?: boolean
}) {
  const canSave = testStatus.argocd === 'ok'
  const [advancedOpen, setAdvancedOpen] = useState(false)

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
            placeholder={
              hasSavedToken
                ? '•••••• (saved — leave blank to keep, or enter new value to replace)'
                : 'ArgoCD API token (optional if in-cluster RBAC)'
            }
          />
          <p className="mt-1 text-[10px] text-[#3a6a8a]">
            {hasSavedToken
              ? 'Submitting blank keeps your saved credential. Enter a new value to replace it.'
              : 'Falls back to ARGOCD_TOKEN env var if not provided.'}
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

      {/* Advanced: Secrets Provider */}
      <div className="border-t border-[#bee0ff] pt-3 dark:border-gray-700">
        <button
          type="button"
          onClick={() => setAdvancedOpen((v) => !v)}
          className="flex items-center gap-2 text-xs font-medium text-[#3a6a8a] hover:text-[#0a3a5a] dark:text-gray-500 dark:hover:text-gray-300"
        >
          {advancedOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
          <KeyRound className="h-3.5 w-3.5" />
          Secrets Provider (optional)
          {form.provider_type && (
            <span className="ml-1 rounded-full bg-teal-100 px-2 py-0.5 text-[10px] font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
              {form.provider_type}
            </span>
          )}
        </button>

        {advancedOpen && (
          <div className="mt-3 space-y-3 rounded-lg bg-[#e8f4ff] p-4 dark:bg-gray-800">
            <p className="text-[11px] text-[#2a5a7a] dark:text-gray-400">
              Optional: configure how Sharko retrieves cluster credentials. You can always update this later in Settings.
            </p>
            <div>
              <label className={labelCls}>Provider Type</label>
              <select
                className={inputCls}
                value={form.provider_type}
                onChange={(e) => onChange({ provider_type: e.target.value as '' | 'aws-sm' | 'k8s-secrets' })}
              >
                <option value="">None</option>
                <option value="aws-sm">AWS Secrets Manager (aws-sm)</option>
                <option value="k8s-secrets">Kubernetes Secrets (k8s-secrets)</option>
              </select>
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
              </div>
            )}
          </div>
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

function StepInit({ onDone, resumed, onBack }: { onDone: () => void; resumed?: boolean; onBack?: () => void }) {
  const [state, setState] = useState<'idle' | 'running' | 'done' | 'error'>('idle')
  const [error, setError] = useState<string | null>(null)
  const [operationId, setOperationId] = useState<string | null>(null)
  const [steps, setSteps] = useState<OperationStep[]>([])
  const [operationStatus, setOperationStatus] = useState<string>('idle')
  const [prUrl, setPrUrl] = useState<string | null>(null)
  // V124-15 / BUG-033: when session expires mid-poll, render a "Log in
  // again" button (instead of the generic "Retry"/"Skip"). Tracked
  // separately from `error` so a malformed error string can't accidentally
  // trigger the auth-recovery flow.
  const [sessionExpired, setSessionExpired] = useState(false)

  const handleInit = async (autoMerge: boolean) => {
    setState('running')
    setError(null)
    setSessionExpired(false)
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
        if (status.wait_payload) setPrUrl(status.wait_payload)

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
      } catch (e: unknown) {
        // V124-15 / BUG-033: distinguish 401 (session expired — fatal,
        // surface to user) from transient errors (network blip, 5xx —
        // keep swallowing so a single transient doesn't tear down the
        // wizard). On 401 we stop both intervals so we don't keep
        // hammering the server, then drop into the error state with a
        // clear "log in again" call to action.
        if (isUnauthorizedError(e)) {
          clearInterval(pollInterval)
          clearInterval(heartbeatInterval)
          setSessionExpired(true)
          setError('Session expired — please log in again.')
          setState('error')
        }
        // Other errors: ignore transient poll errors (existing behavior).
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
      {resumed && (
        <div className="rounded-lg bg-[#e8f4ff] p-3 text-sm text-[#0a3a5a] dark:bg-gray-800 dark:text-gray-300 mb-4">
          <strong>Resuming setup</strong> — your connection is configured. Initialize the repository to complete setup.
        </div>
      )}
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
              {step.detail && (
                <span className="text-xs text-[#3a6a8a] dark:text-gray-400">— {step.detail}</span>
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
            {sessionExpired ? (
              // V124-15 / BUG-033: 401 during polling — clear the auth
              // token (keeps the rest of the SPA from re-auth-looping
              // against a stale token) and bounce the user to login.
              // We use window.location to force a full reload so all
              // in-flight effects in the wizard are torn down cleanly.
              <button
                type="button"
                onClick={() => {
                  sessionStorage.removeItem('sharko-auth-token')
                  window.location.assign('/login')
                }}
                className="inline-flex items-center gap-2 rounded-full bg-[#0a2a4a] px-5 py-2 text-sm font-semibold text-white transition-colors hover:bg-[#14466e]"
              >
                Log in again
              </button>
            ) : (
              <>
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
              </>
            )}
          </div>
        </div>
      )}

      {/* V124-16 / BUG-038 + V124-17 / BUG-039 + BUG-042: in resume mode the
          wizard hard-gates every route to step 4 (App.tsx behaviour), so
          StepInit needs a Back button to walk back to the connection-edit
          screens (step 3 → step 2). Two V124-17 refinements on top of the
          V124-16 button:
            - BUG-039: hide Back while state !== 'idle'. While the init
              operation is running, clicking Back navigates to step 3 mid-
              flight; the polling-effect cleanup runs, the operation
              continues on the backend, and a later Initialize click hits an
              already-init check that may detect a half-created repo state.
              The safe rule is: Back only when the wizard is not actively
              driving an operation. Done/error states are also non-Back: at
              done the user clicks "Go to Dashboard"; at error the user
              clicks Retry/Skip/"Log in again".
            - BUG-042: match the secondary-button styling used by
              StepGit/StepArgoCD's Back buttons exactly so the affordance
              looks the same on every wizard step.
          */}
      {resumed && onBack && state === 'idle' && (
        <div className="pt-2 border-t border-[#bee0ff] dark:border-gray-700">
          <button
            type="button"
            onClick={onBack}
            className="rounded-lg px-4 py-2 text-sm font-medium text-[#1a4a6a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200"
          >
            Back
          </button>
        </div>
      )}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/*  Helpers                                                             */
/* ------------------------------------------------------------------ */

/**
 * detectGitProvider — V124-10 / BUG-028.
 *
 * Inspect a git repo URL and return the canonical Sharko provider name
 * the backend expects (`'github'` / `'azuredevops'`), or `undefined` when
 * the host is unsupported or the URL is not parseable.
 *
 * Why client-side: the FirstRunWizard's `buildPayload` historically sent
 * only `repo_url` + `token`, which V124-4.2's required-field gate then
 * rejects with "git.provider is required". The backend now auto-derives
 * the provider too (defense in depth), but populating it here lets the
 * wizard's "Test Connection" round-trip surface a recognizable provider
 * value in the test response without round-tripping through derivation.
 *
 * Returning `undefined` (instead of throwing) lets `buildPayload`
 * naturally omit the field via `JSON.stringify` — the backend's
 * derivation then takes over and either succeeds or returns a clear
 * unsupported-host error that the wizard already surfaces in
 * `setSaveError` / `setTestStatus`.
 *
 * Recognized hosts (mirror of backend deriveProviderFromURL whitelist):
 *   - github.com, *.github.com
 *   - dev.azure.com, *.visualstudio.com
 */
export function detectGitProvider(repoURL: string): 'github' | 'azuredevops' | undefined {
  try {
    const host = new URL(repoURL).hostname.toLowerCase()
    if (host === 'github.com' || host.endsWith('.github.com')) return 'github'
    if (host === 'dev.azure.com' || host.endsWith('.visualstudio.com')) return 'azuredevops'
    return undefined
  } catch {
    return undefined
  }
}

/* ------------------------------------------------------------------ */
/*  Main wizard                                                         */
/* ------------------------------------------------------------------ */

export function FirstRunWizard({ initialStep = 1 }: { initialStep?: number } = {}) {
  const navigate = useNavigate()
  const { connections, refreshConnections } = useConnections()
  const [step, setStep] = useState(initialStep)
  const [form, setForm] = useState<WizardForm>({ ...emptyForm })
  const [testStatus, setTestStatus] = useState<TestStatus>({ git: 'idle', argocd: 'idle' })
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  // V124-16 / BUG-037 + BUG-038: in resume mode, pre-populate the form with
  // the existing connection so the user can navigate back through step 3 →
  // step 2 and see/edit the saved values (rather than blank fields). The
  // first connection wins — Sharko's data model is single-connection in
  // practice (see frontend-expert.md §"Single Connection Edit"), and the
  // wizard's resume gate fires only when at least one connection exists.
  // We track the loaded connection's name so the save path can hit
  // PUT /connections/{name} instead of POST, which guarantees an update
  // (with token-preserving merge) rather than relying on the backend's
  // upsert-by-derived-name behavior.
  const existingConnection = connections[0]
  const [loadedConnectionName, setLoadedConnectionName] = useState<string | null>(null)
  useEffect(() => {
    if (initialStep === 4 && existingConnection && loadedConnectionName === null) {
      let gitUrl = ''
      if (existingConnection.git_provider === 'github') {
        gitUrl = `https://github.com/${existingConnection.git_repo_identifier}`
      } else if (existingConnection.git_provider === 'azuredevops') {
        const parts = existingConnection.git_repo_identifier.split('/')
        if (parts.length >= 3) {
          gitUrl = `https://dev.azure.com/${parts[0]}/${parts[1]}/_git/${parts[2]}`
        }
      }
      setForm((prev) => ({
        ...prev,
        git_url: gitUrl,
        argocd_server_url: existingConnection.argocd_server_url,
        argocd_namespace: existingConnection.argocd_namespace || 'argocd',
        provider_type:
          (existingConnection.provider?.type as '' | 'aws-sm' | 'k8s-secrets') || '',
        provider_region: existingConnection.provider?.region || '',
        provider_prefix: existingConnection.provider?.prefix || '',
      }))
      setLoadedConnectionName(existingConnection.name)
    }
  }, [initialStep, existingConnection, loadedConnectionName])

  const patchForm = (patch: Partial<WizardForm>) => {
    setForm((prev) => ({ ...prev, ...patch }))
    // reset test statuses on change
    setTestStatus({ git: 'idle', argocd: 'idle' })
  }

  const buildPayload = () => ({
    git: {
      // V124-10 / BUG-028: send provider proactively so the create-time
      // required-field gate (V124-4.2) doesn't fire on every wizard
      // submission. `undefined` is dropped by JSON.stringify, in which
      // case the backend's deriveProviderFromURL takes over. See
      // detectGitProvider for the recognized-host list.
      provider: detectGitProvider(form.git_url),
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
      base_branch: 'main',
      branch_prefix: 'sharko/',
      commit_prefix: 'sharko:',
      pr_auto_merge: false,
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
      // V124-16 / BUG-038: in resume mode the wizard already has a saved
      // connection. Hit PUT /connections/{name} so the backend's
      // token-preserving merge runs (empty token fields keep the saved
      // values — the wizard's password inputs render empty even after
      // pre-population, so a blank submit must NOT wipe the saved token).
      // POST is left as the create path for the fresh-install flow.
      if (loadedConnectionName) {
        await api.updateConnection(loadedConnectionName, buildPayload())
      } else {
        await api.createConnection(buildPayload())
      }
      // DON'T call refreshConnections here — it causes App.tsx to re-render,
      // unmounting the wizard before setStep(4) can execute.
      // refreshConnections is called in handleDone instead.
      setStep(4)
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Failed to save connection')
    } finally {
      setSaving(false)
    }
  }, [form, loadedConnectionName])  // eslint-disable-line react-hooks/exhaustive-deps

  const handleDone = useCallback(() => {
    refreshConnections()
    navigate('/dashboard')
  }, [navigate, refreshConnections])

  // V124-16 / BUG-035: the X button used to navigate('/dashboard'), but
  // App.tsx's wizard gate immediately re-renders FirstRunWizard whenever a
  // connection exists with an un-initialized repo, so the navigation was a
  // no-op. Set a session-scoped sessionStorage flag that App.tsx consults
  // to skip the gate for the rest of this session. A fresh tab / hard
  // refresh clears the flag automatically — this is "dismiss for now",
  // not "permanently skip setup".
  const handleEscape = useCallback(() => {
    sessionStorage.setItem('sharko:dismiss-wizard', '1')
    refreshConnections()
    navigate('/dashboard')
  }, [navigate, refreshConnections])

  // V124-16 / BUG-037: nuclear "start over" affordance from step 2 in
  // resume mode. Confirm, then DELETE every saved connection so App.tsx's
  // empty-connections branch renders the wizard fresh from step 1.
  const handleClearConfig = useCallback(async () => {
    if (
      !window.confirm(
        'This will delete the connection and reset the wizard. Continue?',
      )
    ) {
      return
    }
    try {
      for (const c of connections) {
        await api.deleteConnection(c.name)
      }
      // Clear any local state derived from the deleted connection.
      setForm({ ...emptyForm })
      setLoadedConnectionName(null)
      setTestStatus({ git: 'idle', argocd: 'idle' })
      // App.tsx watches connections.length — refresh + jump to step 1.
      refreshConnections()
      setStep(1)
    } catch (err) {
      setSaveError(
        err instanceof Error
          ? `Failed to clear configuration: ${err.message}`
          : 'Failed to clear configuration',
      )
    }
  }, [connections, refreshConnections])

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
            <div className="flex items-center gap-3">
              <StepIndicator current={step} total={4} />
              <button
                type="button"
                onClick={handleEscape}
                title="Skip to Dashboard"
                className="ml-1 rounded-md p-1 text-blue-300/70 transition-colors hover:bg-white/10 hover:text-white focus:outline-none focus:ring-1 focus:ring-white/40"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
          </div>

          {/* Step label.
              V124-6.4 / BUG-024: when App.tsx resumes the wizard at step 4
              (because a connection exists but the repo is not yet
              initialized), drop the "Step N of M" counter — there is no
              counter that makes sense for "you skipped 1-3 because they
              already happened in a prior session". Show "Resuming setup —
              Initialize" instead. The counter still appears for the normal
              start-from-step-1 flow.
              */}
          <div className="border-b border-[#bee0ff] dark:border-gray-800 bg-[#e8f4ff] dark:bg-gray-800/50 px-6 py-2">
            <p
              className="text-xs font-semibold uppercase tracking-wider text-[#3a6a8a] dark:text-gray-400"
              data-testid="wizard-step-label"
            >
              {initialStep === 4
                ? `Resuming setup — ${stepLabels[step - 1]}`
                : `Step ${step} of ${stepLabels.length} — ${stepLabels[step - 1]}`}
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
                onClearConfig={loadedConnectionName ? handleClearConfig : undefined}
                // V124-17 / BUG-040: signal "the backend has a saved token
                // for this connection" so the password input renders the
                // saved-credential placeholder + helper text. Bound to
                // loadedConnectionName (truthy iff resume-mode pre-populate
                // ran) — fresh-install mode has no saved token to preserve.
                hasSavedToken={Boolean(loadedConnectionName)}
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
                // V124-17 / BUG-040: same signal as StepGit — the ArgoCD
                // token field gets the saved-credential affordance in
                // resume mode.
                hasSavedToken={Boolean(loadedConnectionName)}
              />
            )}
            {step === 4 && (
              <StepInit
                onDone={handleDone}
                resumed={initialStep === 4}
                onBack={initialStep === 4 ? () => setStep(3) : undefined}
              />
            )}
          </div>
        </div>

        {/* V124-16 / BUG-036: in resume mode the wizard hard-gates Settings,
            so claiming "you can update connections later in Settings" is a
            lie. Point the user at the in-wizard controls instead.
            */}
        <p className="mt-4 text-center text-xs text-[#3a6a8a] dark:text-gray-600">
          {initialStep === 4
            ? 'Initialize the repository to continue, or use the controls above to edit or reset.'
            : 'You can always update connections later in Settings.'}
        </p>
      </div>
    </div>
  )
}
