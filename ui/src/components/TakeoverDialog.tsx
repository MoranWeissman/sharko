/**
 * TakeoverDialog — brownfield takeover (v4 Wave 2, Epic 6, stories 6.1-6.4).
 *
 * Taking over a cluster ArgoCD already runs is the most consequential thing
 * Sharko can do to a live fleet, so the dialog is built around one rule:
 * you cannot get to the button without having been shown what it will do.
 *
 * The shape of that:
 *
 *  - It opens on the preflight (a pure read) and shows all four checks,
 *    each with the server's own words for what it means and what to do.
 *    None of that copy is composed here — the UI renders what the API
 *    sends, so the same sentences are in the API, the CLI and the docs.
 *  - "Check again" re-runs the preflight in place. Fixing something and
 *    watching the same row go green is the whole point of story 6.2.
 *  - A blocked check hides the button entirely. Warnings show a checkbox
 *    that has to be ticked before the button turns on.
 *  - The labels being carried over are listed by name, with the
 *    ApplicationSets that select each one, BEFORE the confirm.
 *  - Nothing is deleted anywhere in this flow. The follow-up "remove the
 *    old labels" step is a separate, separately-confirmed screen.
 */
import { useCallback, useEffect, useState } from 'react'
import {
  AlertTriangle,
  CheckCircle,
  ExternalLink,
  Loader2,
  RefreshCw,
  ShieldAlert,
  Tag,
} from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
} from '@/components/ui/dialog'
import { takeoverPreflight, takeoverCluster, dropLegacyLabels } from '@/services/api'
import type {
  TakeoverFinding,
  TakeoverReport,
  TakeoverResponse,
  DropLegacyLabelsResponse,
} from '@/services/models'

export const TAKEOVER_LABEL = 'Take ownership'
// TAKEOVER_DIALOG_TITLE is the dialog's own title, separate from the button
// label above (walk day 4 locks, S3) — the button says what you're about to
// do, the dialog title says what you're about to do it to.
export const TAKEOVER_DIALOG_TITLE = 'Take ownership of this cluster'
export const TAKEOVER_HINT =
  "Bring this cluster under Sharko's management. It keeps the same name, the same address and — unless you say otherwise — every label already on it. Nothing is deleted, and no addon is turned on."

interface TakeoverDialogProps {
  clusterName: string
  open: boolean
  onClose: () => void
  /** Called after a successful takeover so the parent can refetch. */
  onDone?: () => void
}

function StatusIcon({ status }: { status: TakeoverFinding['status'] }) {
  if (status === 'ok') {
    return <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" aria-label="Nothing to do" />
  }
  if (status === 'warning') {
    return <AlertTriangle className="h-4 w-4 shrink-0 text-amber-500 dark:text-amber-400" aria-label="Read this first" />
  }
  return <ShieldAlert className="h-4 w-4 shrink-0 text-red-500 dark:text-red-400" aria-label="Must be fixed first" />
}

const ROW_TONE: Record<TakeoverFinding['status'], string> = {
  ok: 'bg-green-50 dark:bg-green-900/10',
  warning: 'bg-amber-50 dark:bg-amber-900/10',
  blocked: 'bg-red-50 dark:bg-red-900/10',
}

/**
 * PreflightReportView — the four checks. Presentational only, exported so
 * the same rendering can be dropped inline on the cluster page without the
 * modal wrapper.
 */
export function PreflightReportView({ report }: { report: TakeoverReport }) {
  const tone = report.ready
    ? report.needs_acknowledgement
      ? 'bg-amber-50 text-amber-800 dark:bg-amber-900/20 dark:text-amber-300'
      : 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'
    : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400'

  return (
    <div className="space-y-4">
      <div className={`rounded-md px-4 py-3 text-sm font-medium ${tone}`} data-testid="takeover-summary">
        {report.summary}
      </div>

      <div className="space-y-2" data-testid="takeover-findings">
        {report.findings.map((f) => (
          <div
            key={f.id}
            data-testid={`takeover-finding-${f.id}`}
            data-status={f.status}
            className={`rounded-md px-3 py-2.5 text-sm ${ROW_TONE[f.status]}`}
          >
            <div className="flex items-center gap-2">
              <StatusIcon status={f.status} />
              <span className="font-medium text-[#0a2a4a] dark:text-gray-200">{f.title}</span>
            </div>
            <p className="ml-6 mt-0.5 text-sm text-[#3a6a8a] dark:text-gray-400">{f.detail}</p>
            <p className="ml-6 mt-1 text-sm text-[#3a6a8a] dark:text-gray-400">{f.what_it_means}</p>
            {f.status !== 'ok' && (
              <p
                data-testid={`takeover-todo-${f.id}`}
                className="ml-6 mt-1.5 rounded-md bg-white/70 px-2.5 py-1.5 text-xs font-medium text-[#0a2a4a] ring-1 ring-black/5 dark:bg-black/20 dark:text-gray-200 dark:ring-white/10"
              >
                What to do: {f.what_to_do}
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * CarriedLabelsPanel — the labels that come across with the cluster, shown
 * BEFORE the confirm (story 6.3). Each one says which ApplicationSets pick
 * clusters using it, so "can I safely drop this later" is answerable from
 * the same screen.
 */
export function CarriedLabelsPanel({ report }: { report: TakeoverReport }) {
  const labels = report.legacy_labels ?? {}
  const keys = Object.keys(labels).sort()
  const selectedBy = report.legacy_labels_selected_by ?? {}

  if (keys.length === 0) {
    return (
      <div className="rounded-md bg-[#f0f7ff] px-3 py-2.5 text-sm text-[#3a6a8a] dark:bg-gray-800 dark:text-gray-400">
        This cluster's connection has no labels from a previous owner, so there is nothing to carry across.
      </div>
    )
  }

  return (
    <div className="space-y-2" data-testid="takeover-carried-labels">
      <div className="flex items-center gap-2 text-sm font-medium text-[#0a2a4a] dark:text-gray-200">
        <Tag className="h-4 w-4 shrink-0" />
        These {keys.length} label{keys.length === 1 ? '' : 's'} come across unchanged
      </div>
      <ul className="space-y-1.5">
        {keys.map((k) => (
          <li
            key={k}
            data-testid={`takeover-carried-label-${k}`}
            className="rounded-md bg-[#f0f7ff] px-3 py-2 text-sm dark:bg-gray-800"
          >
            <code className="font-mono text-xs text-[#0a2a4a] dark:text-gray-200">
              {k}: {labels[k]}
            </code>
            {selectedBy[k]?.length ? (
              <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-400">
                Picked up by: {selectedBy[k].join(', ')}
              </p>
            ) : (
              <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-400">
                Nothing Sharko can see picks clusters using this one.
              </p>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * warningFindingIDs — the ids of the findings currently shown as warnings.
 * These are exactly what the confirmation covers: the user ticked the box
 * having read THESE rows, so THESE are the ids sent back.
 */
function warningFindingIDs(report: TakeoverReport | null): string[] {
  if (!report) return []
  return report.findings.filter((f) => f.status === 'warning').map((f) => f.id)
}

export function TakeoverDialog({ clusterName, open, onClose, onDone }: TakeoverDialogProps) {
  const [report, setReport] = useState<TakeoverReport | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [acknowledged, setAcknowledged] = useState(false)
  const [preserve, setPreserve] = useState(true)
  const [submitting, setSubmitting] = useState(false)
  const [result, setResult] = useState<TakeoverResponse | null>(null)

  const runPreflight = useCallback(() => {
    setLoading(true)
    setError(null)
    takeoverPreflight(clusterName)
      .then((r) => {
        setReport(r)
        // A fresh report means a fresh decision: whatever was ticked
        // before was ticked against different findings.
        setAcknowledged(false)
      })
      .catch((e) => setError(e instanceof Error ? e.message : 'Could not run the checks'))
      .finally(() => setLoading(false))
  }, [clusterName])

  useEffect(() => {
    if (!open) {
      setReport(null)
      setError(null)
      setResult(null)
      setAcknowledged(false)
      setPreserve(true)
      return
    }
    runPreflight()
  }, [open, runPreflight])

  const canGoAhead =
    !!report && report.ready && (!report.needs_acknowledgement || acknowledged) && !submitting

  async function confirm() {
    setSubmitting(true)
    setError(null)
    try {
      const res = await takeoverCluster(clusterName, {
        yes: true,
        // Name the warnings that were actually on screen. The server
        // re-runs the checks and refuses if it finds one this list does
        // not cover — which is the point: a warning that turned up while
        // the dialog was open must not be waved through by a tickbox
        // ticked against a different set of findings.
        acknowledged_findings: acknowledged ? warningFindingIDs(report) : [],
        preserve_legacy_labels: preserve,
      })
      setResult(res)
      onDone?.()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'The takeover did not go through')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose() }}>
      <DialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto" data-testid="takeover-dialog">
        <DialogHeader>
          <DialogTitle>{TAKEOVER_DIALOG_TITLE}</DialogTitle>
          <DialogDescription>{TAKEOVER_HINT}</DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="flex items-center justify-center py-12">
            <Loader2 className="h-6 w-6 animate-spin text-[#2a5a7a] dark:text-gray-400" />
            <span className="ml-2 text-sm text-[#2a5a7a] dark:text-gray-400">Running the checks…</span>
          </div>
        )}

        {error && (
          <div
            data-testid="takeover-error"
            className="rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400"
          >
            <p>{error}</p>
            <button
              type="button"
              onClick={runPreflight}
              data-testid="takeover-retry"
              className="mt-2 inline-flex items-center gap-1.5 rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm font-medium text-red-700 hover:bg-red-50 dark:border-red-700 dark:bg-gray-800 dark:text-red-400 dark:hover:bg-red-900/30"
            >
              <RefreshCw className="h-3.5 w-3.5" /> Retry
            </button>
          </div>
        )}

        {result && (
          <div className="space-y-3" data-testid="takeover-result">
            <div className="rounded-md bg-green-50 p-4 text-sm text-green-800 dark:bg-green-900/20 dark:text-green-300">
              {result.message}
            </div>
            {result.git?.pr_url && (
              <a
                href={result.git.pr_url}
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1.5 text-sm font-medium text-[#2a5a7a] hover:underline dark:text-blue-400"
              >
                Open the pull request <ExternalLink className="h-3.5 w-3.5" />
              </a>
            )}
            <button
              type="button"
              onClick={onClose}
              className="rounded-md bg-[#2a5a7a] px-4 py-2 text-sm font-medium text-white hover:bg-[#1a4a6a]"
            >
              Done
            </button>
          </div>
        )}

        {report && !loading && !result && (
          <div className="space-y-4">
            <PreflightReportView report={report} />

            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={runPreflight}
                disabled={loading}
                data-testid="takeover-recheck"
                className="inline-flex items-center gap-1.5 rounded-md border border-[#cfe3f2] px-3 py-1.5 text-sm font-medium text-[#2a5a7a] hover:bg-[#f0f7ff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              >
                <RefreshCw className="h-3.5 w-3.5" /> Check again
              </button>
              <span className="text-xs text-[#5a8aaa] dark:text-gray-500">
                Fix something, then run this again — the check turns green when it is sorted.
              </span>
            </div>

            {report.ready && (
              <>
                <CarriedLabelsPanel report={report} />

                {Object.keys(report.legacy_labels ?? {}).length > 0 && (
                  <label className="flex items-start gap-2 text-sm text-[#3a6a8a] dark:text-gray-400">
                    <input
                      type="checkbox"
                      data-testid="takeover-preserve"
                      checked={preserve}
                      onChange={(e) => setPreserve(e.target.checked)}
                      className="mt-0.5"
                    />
                    <span>
                      Keep the labels listed above. Leave this ticked unless you already know nothing
                      picks clusters using them — unticking it removes them as part of the same change.
                    </span>
                  </label>
                )}

                {report.needs_acknowledgement && (
                  <label className="flex items-start gap-2 rounded-md bg-amber-50 px-3 py-2.5 text-sm text-amber-900 dark:bg-amber-900/20 dark:text-amber-300">
                    <input
                      type="checkbox"
                      data-testid="takeover-acknowledge"
                      checked={acknowledged}
                      onChange={(e) => setAcknowledged(e.target.checked)}
                      className="mt-0.5"
                    />
                    <span>I have read the warnings above and want to go ahead anyway.</span>
                  </label>
                )}

                <button
                  type="button"
                  disabled={!canGoAhead}
                  onClick={confirm}
                  data-testid="takeover-confirm"
                  className="inline-flex items-center gap-2 rounded-md bg-[#2a5a7a] px-4 py-2 text-sm font-medium text-white hover:bg-[#1a4a6a] disabled:cursor-not-allowed disabled:opacity-50"
                >
                  {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
                  Take ownership of {clusterName}
                </button>
              </>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// Story 6.4 — removing the labels that were carried over
// ─────────────────────────────────────────────────────────────────────────

export const DROP_LABELS_LABEL = 'Remove the old labels'
export const DROP_LABELS_HINT =
  'Takes the previous owner’s labels off this cluster’s connection. You are told first, by name, about anything that still picks clusters using them.'

interface DropLegacyLabelsDialogProps {
  clusterName: string
  open: boolean
  onClose: () => void
  onDone?: () => void
}

export function DropLegacyLabelsDialog({ clusterName, open, onClose, onDone }: DropLegacyLabelsDialogProps) {
  const [plan, setPlan] = useState<DropLegacyLabelsResponse | null>(null)
  const [result, setResult] = useState<DropLegacyLabelsResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [acknowledged, setAcknowledged] = useState(false)

  const runDryRun = useCallback(() => {
    setLoading(true)
    setError(null)
    // The dry run is what produces the warning list. It is always run
    // first — the user never sees the button before the warnings.
    dropLegacyLabels(clusterName, { dry_run: true })
      .then(setPlan)
      .catch((e) => setError(e instanceof Error ? e.message : 'Could not work out what would be removed'))
      .finally(() => setLoading(false))
  }, [clusterName])

  useEffect(() => {
    if (!open) {
      setPlan(null)
      setResult(null)
      setError(null)
      setAcknowledged(false)
      return
    }
    runDryRun()
  }, [open, runDryRun])

  const nothingToDo = !!plan && (plan.removed?.length ?? 0) === 0
  const hasWarnings = (plan?.warnings?.length ?? 0) > 0
  const canGoAhead = !!plan && !nothingToDo && (!hasWarnings || acknowledged) && !submitting

  async function confirm() {
    if (!plan) return
    setSubmitting(true)
    setError(null)
    try {
      const res = await dropLegacyLabels(clusterName, {
        yes: true,
        labels: plan.removed,
        // Same rule as the takeover: echo back the ids of the warnings the
        // dry run put on screen, not a blanket "I read them".
        acknowledged_findings: acknowledged ? (plan.warning_ids ?? []) : [],
      })
      setResult(res)
      onDone?.()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'The labels were not removed')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v) onClose() }}>
      <DialogContent className="max-w-xl max-h-[80vh] overflow-y-auto" data-testid="drop-labels-dialog">
        <DialogHeader>
          <DialogTitle>{DROP_LABELS_LABEL}: {clusterName}</DialogTitle>
          <DialogDescription>{DROP_LABELS_HINT}</DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="flex items-center justify-center py-10">
            <Loader2 className="h-6 w-6 animate-spin text-[#2a5a7a] dark:text-gray-400" />
          </div>
        )}

        {error && (
          <div
            data-testid="drop-labels-error"
            className="rounded-md bg-red-50 p-4 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-400"
          >
            <p>{error}</p>
            <button
              type="button"
              onClick={runDryRun}
              disabled={loading}
              data-testid="drop-labels-retry"
              className="mt-2 inline-flex items-center gap-1.5 rounded-md border border-red-300 bg-white px-3 py-1.5 text-sm font-medium text-red-700 hover:bg-red-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-red-700 dark:bg-gray-800 dark:text-red-400 dark:hover:bg-red-900/30"
            >
              <RefreshCw className="h-3.5 w-3.5" /> Retry
            </button>
          </div>
        )}

        {result && (
          <div className="space-y-3" data-testid="drop-labels-result">
            <div className="rounded-md bg-green-50 p-4 text-sm text-green-800 dark:bg-green-900/20 dark:text-green-300">
              {result.message}
            </div>
            <button
              type="button"
              onClick={onClose}
              className="rounded-md bg-[#2a5a7a] px-4 py-2 text-sm font-medium text-white hover:bg-[#1a4a6a]"
            >
              Done
            </button>
          </div>
        )}

        {plan && !loading && !result && (
          <div className="space-y-4">
            <div className="rounded-md bg-[#f0f7ff] px-3 py-2.5 text-sm text-[#3a6a8a] dark:bg-gray-800 dark:text-gray-400">
              {plan.message}
            </div>

            {(plan.warnings ?? []).length > 0 && (
              <ul className="space-y-2" data-testid="drop-labels-warnings">
                {plan.warnings!.map((wmsg, i) => (
                  <li
                    key={i}
                    className="flex gap-2 rounded-md bg-amber-50 px-3 py-2.5 text-sm text-amber-900 dark:bg-amber-900/20 dark:text-amber-300"
                  >
                    <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                    <span>{wmsg}</span>
                  </li>
                ))}
              </ul>
            )}

            {!nothingToDo && hasWarnings && (
              <label className="flex items-start gap-2 text-sm text-[#3a6a8a] dark:text-gray-400">
                <input
                  type="checkbox"
                  data-testid="drop-labels-acknowledge"
                  checked={acknowledged}
                  onChange={(e) => setAcknowledged(e.target.checked)}
                  className="mt-0.5"
                />
                <span>I have read the warnings above and want to remove these labels anyway.</span>
              </label>
            )}

            {!nothingToDo && (
              <button
                type="button"
                disabled={!canGoAhead}
                onClick={confirm}
                data-testid="drop-labels-confirm"
                className="inline-flex items-center gap-2 rounded-md bg-[#2a5a7a] px-4 py-2 text-sm font-medium text-white hover:bg-[#1a4a6a] disabled:cursor-not-allowed disabled:opacity-50"
              >
                {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
                Remove {plan.removed?.length ?? 0} label{(plan.removed?.length ?? 0) === 1 ? '' : 's'}
              </button>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
