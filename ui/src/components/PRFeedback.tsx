import {
  CheckCircle2,
  ExternalLink,
  GitPullRequest,
  Loader2,
} from 'lucide-react'

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
          <p className="mt-1 text-xs text-green-700 dark:text-green-400">{hint}</p>
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
