import { AlertCircle, ChevronRight } from 'lucide-react'
import { useState } from 'react'
import type { DryRunResult } from '@/services/models'
import type { AddAddonResponse } from '@/services/api'
import {
  PRLifecycleProgress,
  PRResultBanner,
  type PRPhase,
} from '@/components/PRFeedback'

/**
 * AddAddonFlow — shared, presentational building blocks for the
 * "add an addon to the catalog" flow. Extracted (V2-cleanup-15) from
 * MarketplaceAddonDetail so the Marketplace detail page and the Addons
 * Catalog "Register addon" dialog share ONE implementation and can't drift
 * apart again (the twin-path gap that #397/#396 kept reopening).
 *
 * Each piece is a small, prop-driven component with no internal fetching —
 * the parent owns all state (form, submit phase, results). That keeps the
 * two outer flows (a full embedded page vs. a modal dialog) free to differ
 * in their chrome while the inner parity surfaces stay identical:
 *
 *   - useAutoMergeGate()  — the admin-gated auto-merge decision + payload value
 *   - <AutoMergeToggle>   — the checkbox + admin-only hint
 *   - <DryRunPreview>     — renders DryRunResult.files_to_write (the dry-run)
 *   - <SubmitPhaseBanner> — coarse branch→commit→PR→merge progress
 *   - <SubmitResultBanner> — terminal success block with a clickable PR link
 *
 * The two PR-feedback banners (SubmitPhaseBanner + SubmitResultBanner) are now
 * thin add-addon-specific wrappers around the flow-agnostic PRProgressBanner /
 * PRResultBanner in PRFeedback.tsx (promoted in V2-cleanup-24). Behavior and
 * copy for the two add-addon callers are unchanged — the wrappers exist so the
 * Marketplace + Catalog screens keep their exact imports while every OTHER
 * write flow shares the same components.
 *
 * Backend contract (unchanged, from #397): addAddon accepts auto_merge +
 * dry_run; the response carries dry_run (DryRunResult), pr_url/pr_id, and
 * merged. SubmitResultBanner branches strictly on `merged` so an open PR is
 * never presented as already-cataloged.
 */

/** The coarse submit phase shared by both add-addon callers. */
export type SubmitPhase = PRPhase

export interface DryRunPreviewProps {
  result: DryRunResult
}

/**
 * DryRunPreview — renders the files the dry-run call WOULD write/update/delete,
 * with distinct markers: green `+` for create, amber `~` for update, red `-`
 * for delete. Also surfaces secrets_to_create (names only) when present. No PR,
 * no commit. Every array read is null-safe (`?? []`) because the Go DryRunResult
 * serializes the slice as `files_to_write` while older fixtures may use `files`.
 */
export function DryRunPreview({ result }: DryRunPreviewProps) {
  const files = result.files_to_write ?? result.files ?? []
  const secrets = result.secrets_to_create ?? []
  const [expandedFiles, setExpandedFiles] = useState<Record<string, boolean>>({})

  const toggleFile = (path: string) => {
    setExpandedFiles((prev) => ({ ...prev, [path]: !prev[path] }))
  }

  return (
    <div className="rounded-md bg-[#e8f4ff] p-3 ring-2 ring-[#6aade0] dark:bg-gray-900 dark:ring-gray-700">
      <h4 className="mb-2 text-sm font-semibold text-[#0a2a4a] dark:text-gray-200">
        Preview
      </h4>
      <div className="space-y-2 text-xs text-[#2a5a7a] dark:text-gray-400">
        <div>
          <span className="font-medium text-[#0a3a5a] dark:text-gray-300">
            PR Title:
          </span>{' '}
          {result.pr_title}
        </div>
        {files.length > 0 && (
          <div>
            <span className="font-medium text-[#0a3a5a] dark:text-gray-300">
              Files:
            </span>
            <ul className="mt-1 space-y-1 font-mono">
              {files.map((f) => {
                const hasDiff = f.diff && f.diff.trim().length > 0
                const isExpanded = expandedFiles[f.path] || false
                return (
                  <li key={f.path}>
                    {hasDiff ? (
                      <button
                        onClick={() => toggleFile(f.path)}
                        className="flex w-full items-start gap-1 text-left hover:opacity-80"
                        aria-expanded={isExpanded}
                      >
                        <ChevronRight
                          className={`mt-0.5 h-3 w-3 flex-shrink-0 transition-transform ${
                            isExpanded ? 'rotate-90' : ''
                          }`}
                        />
                        <span
                          className={
                            f.action === 'create'
                              ? 'text-green-600 dark:text-green-400'
                              : f.action === 'delete'
                                ? 'text-red-600 dark:text-red-400'
                                : 'text-amber-600 dark:text-amber-400'
                          }
                        >
                          {f.action === 'create' ? '+' : f.action === 'delete' ? '-' : '~'}
                        </span>{' '}
                        <span className="break-all">{f.path}</span>
                      </button>
                    ) : (
                      <div className="flex items-start gap-1">
                        <span
                          className={
                            f.action === 'create'
                              ? 'text-green-600 dark:text-green-400'
                              : f.action === 'delete'
                                ? 'text-red-600 dark:text-red-400'
                                : 'text-amber-600 dark:text-amber-400'
                          }
                        >
                          {f.action === 'create' ? '+' : f.action === 'delete' ? '-' : '~'}
                        </span>{' '}
                        <span className="break-all">{f.path}</span>
                      </div>
                    )}
                    {hasDiff && isExpanded && f.diff && (
                      <div className="ml-4 mt-1 overflow-x-auto rounded border border-[#6aade0] bg-white p-2 dark:border-gray-600 dark:bg-gray-800">
                        <pre className="whitespace-pre text-xs">
                          {f.diff.split('\n').map((line, idx) => {
                            const lineColor = line.startsWith('+')
                              ? 'text-green-600 dark:text-green-400'
                              : line.startsWith('-')
                                ? 'text-red-600 dark:text-red-400'
                                : 'text-[#2a5a7a] dark:text-gray-400'
                            return (
                              <div key={idx} className={lineColor}>
                                {line}
                              </div>
                            )
                          })}
                        </pre>
                      </div>
                    )}
                  </li>
                )
              })}
            </ul>
          </div>
        )}
        {secrets.length > 0 && (
          <div>
            <span className="font-medium text-[#0a3a5a] dark:text-gray-300">
              Secrets:
            </span>{' '}
            {secrets.join(', ')}
          </div>
        )}
      </div>
    </div>
  )
}

export interface SubmitPhaseBannerProps {
  /** The coarse phase from the parent's state machine. */
  phase: SubmitPhase
  /**
   * The PR result once the POST resolves. When present the banner upgrades
   * to the init-style lifecycle step-list (PRLifecycleProgress) so the user
   * sees PR created → merging → merged/open-for-review instead of a plain
   * static "PR opened" message. While null (POST still in flight) a spinner
   * is shown.
   */
  result?: AddAddonResponse | null
}

/**
 * SubmitPhaseBanner — shows an init-style PR lifecycle progress when `result`
 * is available, otherwise falls back to a spinner while the POST is in flight.
 * The lifecycle window polls `refreshPR` automatically (bounded ~2 min) when
 * the PR is not yet merged, so the user sees it move to "Merged ✓" without
 * any manual refresh.
 */
export function SubmitPhaseBanner({ phase, result }: SubmitPhaseBannerProps) {
  if (phase === 'idle') return null
  if (result) {
    return (
      <PRLifecycleProgress
        result={result}
        autoMergeExpected={phase === 'merged'}
        mergedLabel="PR merged — addon added to your catalog"
        openLabel="PR open for review — merge it to catalog the addon"
      />
    )
  }
  // POST still in flight — show a simple spinner row.
  return (
    <div
      role="status"
      className="flex items-center gap-2 rounded-md ring-2 ring-[#6aade0] bg-[#f0f7ff] p-3 text-sm text-[#0a3a5a] dark:ring-gray-700 dark:bg-gray-900 dark:text-gray-300"
    >
      <span className="h-4 w-4 shrink-0 animate-spin rounded-full border-2 border-teal-500 border-t-transparent" aria-hidden="true" />
      <span>Creating branch, committing, opening PR…</span>
    </div>
  )
}

export interface SubmitResultBannerProps {
  result: AddAddonResponse
}

/**
 * SubmitResultBanner — terminal success block with a clickable PR link.
 * Branches STRICTLY on the response's `merged` flag (top-level or wrapped
 * under `result` when an attribution warning fired) so an open PR is never
 * presented as already-cataloged. Returns null when there's no PR URL to
 * link — the caller surfaces a defensive fallback in that case.
 *
 * Note: when flows use SubmitPhaseBanner with `result=` this banner becomes
 * redundant (the lifecycle step already shows the terminal state). It is kept
 * so existing callers that render both are not broken.
 */
export function SubmitResultBanner({ result }: SubmitResultBannerProps) {
  return (
    <PRResultBanner
      result={result}
      mergedMessage="PR merged — addon added to your catalog"
      openMessage="PR opened — merge it to apply"
    />
  )
}

export interface SubmitErrorBannerProps {
  message: string
}

/** SubmitErrorBanner — inline error block for a failed preview/submit. */
export function SubmitErrorBanner({ message }: SubmitErrorBannerProps) {
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
    >
      <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <p>{message}</p>
    </div>
  )
}
