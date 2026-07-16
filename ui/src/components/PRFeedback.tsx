import { useEffect, useRef, useState } from 'react'
import {
  CheckCircle2,
  Circle,
  ExternalLink,
  GitMerge,
  GitPullRequest,
  Loader2,
  X,
} from 'lucide-react'
import { refreshPR } from '@/services/api'

/**
 * PRFeedback — flow-agnostic, presentational building blocks for surfacing
 * the result of any write operation that opens (or auto-merges) a pull
 * request. Promoted (V2-cleanup-24) out of AddAddonFlow.tsx so EVERY write
 * flow — deploy/enable, upgrade, configure, values, secret-path, register,
 * remove, adopt, unadopt — shows ONE consistent PR experience: a clickable
 * "View PR #N" link and a merged-vs-open outcome, instead of each flow
 * hand-rolling its own (often dead, non-clickable) "PR #N →" text.
 *
 * Before this extraction the good banners lived only inside the add-addon
 * flow and were imported by just two screens; everything else drifted. These
 * components are deliberately prop-driven with no internal fetching — the
 * parent owns all state and passes in the orchestrator's response.
 *
 * Backend contract: every PR-opening write returns the PR fields either at
 * the top level (no attribution warning) OR wrapped under `result` (when an
 * attribution warning fired). `extractPR` normalizes both shapes so callers
 * never have to remember the `result?.` fallback.
 */

/**
 * The minimal PR-result shape returned by Sharko write endpoints. PR fields
 * may sit at the top level or be wrapped under `result` (attribution-warning
 * path). `pull_request_url` is a legacy alias still emitted by some handlers.
 */
export interface PRResult {
  pr_url?: string
  pr_id?: number
  branch?: string
  merged?: boolean
  /** Legacy alias for pr_url kept for older handlers. */
  pull_request_url?: string
  result?: {
    pr_url?: string
    pr_id?: number
    branch?: string
    merged?: boolean
  }
}

/** Normalized PR fields: top-level OR `result`-wrapped, whichever is present. */
export interface ExtractedPR {
  prUrl: string | null
  prId: number | null
  merged: boolean
}

/**
 * extractPR — single source of truth for reading PR fields off a write
 * response. Handles the top-level shape, the `result`-wrapped attribution
 * shape, and the legacy `pull_request_url` alias. Returns nulls when there's
 * no PR to link.
 */
export function extractPR(result: PRResult | null | undefined): ExtractedPR {
  if (!result) return { prUrl: null, prId: null, merged: false }
  const prUrl =
    result.pr_url ||
    result.result?.pr_url ||
    result.pull_request_url ||
    null
  const prId = result.pr_id ?? result.result?.pr_id ?? null
  const merged = result.merged ?? result.result?.merged ?? false
  return { prUrl, prId, merged }
}

export interface PRLinkProps {
  /** PR HTML URL. */
  url: string
  /** PR number, when known — renders "View PR #N on GitHub". */
  id?: number | null
  /** Smaller, inline variant for embedding in a sentence. */
  size?: 'xs' | 'sm'
  className?: string
}

/**
 * PRLink — the clickable "View PR #N on GitHub" affordance. The ONE place
 * the PR link is rendered, so the icon, target, and copy stay identical
 * everywhere. Replaces the dead, non-clickable "PR #N →" text the older
 * flows hand-rolled.
 */
export function PRLink({ url, id, size = 'xs', className = '' }: PRLinkProps) {
  const iconSize = size === 'sm' ? 'h-3.5 w-3.5' : 'h-3 w-3'
  const textSize = size === 'sm' ? 'text-sm' : 'text-xs'
  return (
    <a
      href={url}
      target="_blank"
      rel="noopener noreferrer"
      className={`inline-flex items-center gap-1 ${textSize} font-medium underline hover:no-underline ${className}`}
    >
      <GitPullRequest className={iconSize} aria-hidden="true" />
      {id ? `View PR #${id} on GitHub` : 'View PR on GitHub'}
      <ExternalLink className={iconSize} aria-hidden="true" />
    </a>
  )
}

export interface PRResultBannerProps {
  /** The write-operation response. PR fields are extracted via extractPR. */
  result: PRResult | null | undefined
  /**
   * Message shown when the PR is already merged. Defaults to a generic
   * "PR merged — change applied". Pass a flow-specific message (e.g.
   * "PR merged — addon added to your catalog").
   */
  mergedMessage?: string
  /** Message shown when the PR is open (not merged). */
  openMessage?: string
  /**
   * Optional helper line under the link (e.g. "The addon updates once the
   * PR merges and ArgoCD syncs.").
   */
  hint?: string
}

/**
 * PRResultBanner — terminal success block with a clickable PR link. Branches
 * STRICTLY on the response's `merged` flag (top-level or `result`-wrapped) so
 * an open PR is never presented as already applied. Returns null when there's
 * no PR URL to link — the caller surfaces its own fallback in that case.
 */
export function PRResultBanner({
  result,
  mergedMessage = 'PR merged — change applied',
  openMessage = 'PR opened — merge it to apply',
  hint,
}: PRResultBannerProps) {
  const { prUrl, prId, merged } = extractPR(result)
  if (!prUrl) return null
  return (
    <div
      role="status"
      className="flex items-start gap-2 rounded-md border border-green-300 bg-green-50 p-3 text-sm text-green-900 dark:border-green-700 dark:bg-green-950/40 dark:text-green-200"
    >
      <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="flex-1">
        <p className="font-medium">{merged ? mergedMessage : openMessage}</p>
        <PRLink url={prUrl} id={prId} className="mt-1" />
        {hint && (
          <p className="mt-1 text-sm text-green-700 dark:text-green-400">{hint}</p>
        )}
      </div>
    </div>
  )
}

/** The coarse submit phase shared by every write flow's progress banner. */
export type PRPhase = 'idle' | 'submitting' | 'merged' | 'opened'

export interface PRProgressBannerProps {
  phase: PRPhase
  /** Copy shown while the request is in flight. */
  submittingMessage?: string
  /** Copy shown on the terminal "merged" outcome. */
  mergedMessage?: string
  /** Copy shown on the terminal "opened" outcome. */
  openedMessage?: string
}

/**
 * PRProgressBanner — coarse branch → commit → PR → merge progress shown while
 * a synchronous write is in flight and on its terminal result. A single POST
 * can't stream the individual git steps, so we surface a spinner while
 * submitting and the merged/opened outcome afterwards.
 */
export function PRProgressBanner({
  phase,
  submittingMessage = 'Creating branch, committing, opening PR…',
  mergedMessage = 'PR merged — change applied',
  openedMessage = 'PR opened — merge it to apply',
}: PRProgressBannerProps) {
  if (phase === 'idle') return null
  return (
    <div
      role="status"
      className="flex items-center gap-2 rounded-md border border-[#c0ddf0] bg-[#f0f7ff] p-3 text-sm text-[#0a3a5a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300"
    >
      {phase === 'submitting' ? (
        <>
          <Loader2
            className="h-4 w-4 animate-spin text-teal-600 dark:text-teal-400"
            aria-hidden="true"
          />
          <span>{submittingMessage}</span>
        </>
      ) : (
        <>
          <CheckCircle2
            className="h-4 w-4 text-green-600 dark:text-green-400"
            aria-hidden="true"
          />
          <span>{phase === 'merged' ? mergedMessage : openedMessage}</span>
        </>
      )}
    </div>
  )
}

// ─── PR Lifecycle Progress (init-style step list) ────────────────────────────

/**
 * Internal phase of the PR lifecycle progress window.
 *  - creating  : POST in flight, PR not yet returned
 *  - created   : PR opened, auto-merge is on, polling for merge
 *  - merged    : PR merged (terminal, success)
 *  - open      : PR is open but not merging (auto-merge off, or timed out)
 */
type LifecyclePhase = 'creating' | 'created' | 'merged' | 'open'

export interface PRLifecycleProgressProps {
  /**
   * The PR result from the write endpoint. Pass null while the POST is
   * still in flight, then pass the result once it resolves. The component
   * advances through the step list automatically.
   */
  result: PRResult | null | undefined
  /**
   * When true the component polls `refreshPR` every ~7s (bounded at ~2 min)
   * to detect an auto-merge. When false it goes straight to the "open for
   * review" terminal state after showing PR created.
   *
   * If the result already says `merged: true`, polling is skipped entirely.
   */
  autoMergeExpected?: boolean
  /** Flow-specific label for the "PR merged" step. */
  mergedLabel?: string
  /** Flow-specific label for the "PR opened for review" terminal step. */
  openLabel?: string
}

/**
 * PRLifecycleProgress — an init-style step-list window (CheckCircle / Loader2
 * / Circle icons per step) driven by the PR lifecycle we already have.
 *
 * Steps shown:
 *   1. Creating PR…      (spinner while POST in flight)
 *   2. PR created ✓      (shown once we have a pr_id; shows PRLink)
 *   3. Merging… / Merged ✓ / Open for review ✓
 *
 * When the POST returns `merged: true` the window jumps straight to step 3
 * "Merged ✓". When auto-merge is on but the PR is still open we poll
 * `refreshPR` every 7 s for up to ~2 min then fall back to "open for review".
 * When auto-merge is off we show "Open for review" immediately after step 2.
 *
 * After reaching a terminal state the window stays visible as a compact
 * confirmation. The durable record is the existing PullRequestsPanel merged
 * section — we don't keep a loud persistent banner.
 *
 * The component is prop-driven: the PARENT controls all API calls. For the
 * refresh polling the component drives it internally via useEffect so callers
 * don't need to wire it.
 */
export function PRLifecycleProgress({
  result,
  autoMergeExpected = false,
  mergedLabel = 'PR merged — change applied',
  openLabel = 'PR open for review',
}: PRLifecycleProgressProps) {
  const { prUrl, prId, merged: initiallyMerged } = extractPR(result)

  // Internal phase. Derived from props but can advance via the polling effect.
  const [phase, setPhase] = useState<LifecyclePhase>('creating')

  // Sync phase from props on first receipt and when the result changes.
  useEffect(() => {
    if (!result) {
      setPhase('creating')
      return
    }
    if (initiallyMerged) {
      setPhase('merged')
    } else if (prId !== null) {
      // PR is open — decide whether to poll or go straight to "open".
      setPhase(autoMergeExpected ? 'created' : 'open')
    }
  }, [result, initiallyMerged, prId, autoMergeExpected])

  // Bounded polling: poll refreshPR every 7s, stop after ~2 min (≈18 polls).
  const pollCount = useRef(0)
  const MAX_POLLS = 18

  useEffect(() => {
    if (phase !== 'created' || prId === null) return

    pollCount.current = 0

    const interval = setInterval(async () => {
      pollCount.current += 1
      try {
        const res = await refreshPR(prId)
        if (res?.status === 'merged') {
          clearInterval(interval)
          setPhase('merged')
          return
        }
        if (res?.status === 'closed') {
          clearInterval(interval)
          setPhase('open')
          return
        }
      } catch {
        // Transient error — keep polling.
      }
      if (pollCount.current >= MAX_POLLS) {
        clearInterval(interval)
        setPhase('open')
      }
    }, 7000)

    return () => clearInterval(interval)
  }, [phase, prId])

  // Step definitions — rendered in order.
  const steps = [
    {
      key: 'creating',
      label: 'Creating PR…',
      status:
        phase === 'creating'
          ? 'running'
          : ('done' as 'running' | 'done' | 'pending'),
    },
    {
      key: 'created',
      label: prUrl ? undefined : 'PR created',
      prUrl,
      prId,
      status:
        phase === 'creating'
          ? 'pending'
          : ('done' as 'running' | 'done' | 'pending'),
    },
    {
      key: 'merging',
      label:
        phase === 'merged'
          ? mergedLabel
          : phase === 'open'
          ? openLabel
          : 'Merging…',
      status: (
        phase === 'creating' || phase === 'created'
          ? phase === 'created'
            ? 'running'
            : 'pending'
          : 'done'
      ) as 'running' | 'done' | 'pending',
    },
  ]

  return (
    <div
      role="status"
      className="space-y-2 rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] p-3 text-sm dark:ring-gray-700 dark:bg-gray-900"
    >
      {steps.map((step) => (
        <div key={step.key} className="flex items-center gap-2">
          {step.status === 'done' ? (
            <CheckCircle2
              className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400"
              aria-hidden="true"
            />
          ) : step.status === 'running' ? (
            <Loader2
              className="h-4 w-4 shrink-0 animate-spin text-teal-600 dark:text-teal-400"
              aria-hidden="true"
            />
          ) : (
            <Circle
              className="h-4 w-4 shrink-0 text-[#bee0ff] dark:text-gray-600"
              aria-hidden="true"
            />
          )}
          <span
            className={
              step.status === 'pending'
                ? 'text-[#3a6a8a] dark:text-gray-500'
                : 'text-[#0a2a4a] dark:text-gray-200'
            }
          >
            {step.key === 'created' && step.prUrl ? (
              <>
                PR created{' '}
                <PRLink
                  url={step.prUrl}
                  id={step.prId}
                  size="xs"
                  className="ml-1"
                />
              </>
            ) : (
              step.label
            )}
          </span>
          {step.key === 'merging' && phase === 'merged' && (
            <GitMerge
              className="h-3.5 w-3.5 text-green-600 dark:text-green-400"
              aria-hidden="true"
            />
          )}
        </div>
      ))}
    </div>
  )
}

// ─── One-time PR-model explainer (V2-cleanup-61.3, finding F1b) ─────────────

/**
 * localStorage key gating the one-time PR-model explainer. Exported so tests
 * can seed/clear it directly instead of guessing the string.
 */
export const PR_MODEL_EXPLAINER_DISMISSED_KEY = 'sharko-pr-model-explainer-dismissed'

/**
 * PRModelExplainer — a one-time, dismissible callout explaining WHY Sharko
 * opens a pull request instead of changing the cluster/repo directly. Meant
 * to be mounted next to the first PR-result banner a user sees.
 *
 * Deliberately flow-agnostic and stateless beyond the dismiss flag so it can
 * be mounted next to BOTH the addon-add outcome (AddonCatalog.tsx) and the
 * cluster-registration outcome (ClustersOverview.tsx) — whichever the user
 * hits first shows it, and dismissing it anywhere hides it everywhere (the
 * flag is a single shared localStorage key, not per-page state).
 *
 * Neutral/informational (blue) styling per the status-vocabulary color law —
 * this is a normal, expected part of the flow, not a warning.
 */
export function PRModelExplainer() {
  const [dismissed, setDismissed] = useState(
    () =>
      typeof window !== 'undefined' &&
      window.localStorage.getItem(PR_MODEL_EXPLAINER_DISMISSED_KEY) === '1',
  )

  if (dismissed) return null

  const dismiss = () => {
    try {
      window.localStorage.setItem(PR_MODEL_EXPLAINER_DISMISSED_KEY, '1')
    } catch {
      // Storage unavailable (private mode, quota) — still hide for this
      // render; it may reappear next load, which is an acceptable fallback.
    }
    setDismissed(true)
  }

  return (
    <div
      role="status"
      className="flex items-start gap-3 rounded-lg border border-blue-300 bg-blue-50 p-3 text-sm text-blue-900 dark:border-blue-700 dark:bg-blue-950/30 dark:text-blue-200"
    >
      <GitPullRequest className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="flex-1">
        <p className="font-medium">Every change opens a pull request</p>
        <p className="mt-1 text-blue-800 dark:text-blue-300">
          Sharko never changes your cluster or Git repo directly — merge the PR to make it take effect.
        </p>
      </div>
      <button
        type="button"
        onClick={dismiss}
        className="shrink-0 rounded p-0.5 text-blue-600 hover:bg-blue-100 dark:text-blue-400 dark:hover:bg-blue-900/40"
        aria-label="Dismiss"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}
