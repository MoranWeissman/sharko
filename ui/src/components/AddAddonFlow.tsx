import { useContext } from 'react'
import { AlertCircle } from 'lucide-react'
import type { DryRunResult } from '@/services/models'
import type { AddAddonResponse } from '@/services/api'
import { AuthContext } from '@/hooks/useAuth'
import {
  PRProgressBanner,
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

/**
 * useAutoMergeGate — the single source of truth for the admin-gated
 * auto-merge decision, mirroring the register/init/remove dialogs.
 *
 * Only admins may flip auto-merge; operators and viewers always open a PR
 * for human review (the toggle is disabled for them). `autoMergeValue`
 * resolves the choice to the boolean to send on the addAddon call: admins
 * send their toggle, everyone else sends false (manual review).
 */
export function useAutoMergeGate(autoMerge: boolean): {
  isAutoMergeDisabled: boolean
  autoMergeValue: boolean
} {
  const authCtx = useContext(AuthContext)
  const isAutoMergeDisabled =
    authCtx?.role === 'operator' || authCtx?.role === 'viewer'
  return {
    isAutoMergeDisabled,
    autoMergeValue: isAutoMergeDisabled ? false : autoMerge,
  }
}

export interface AutoMergeToggleProps {
  /** Stable DOM id for the checkbox/label pair. */
  id: string
  checked: boolean
  disabled: boolean
  onChange: (next: boolean) => void
}

/**
 * AutoMergeToggle — the admin-gated "Merge PR automatically" checkbox. Same
 * copy and admin-only hint the Marketplace screen and register/init dialogs
 * use. When checked (admins only) the catalog PR auto-merges as soon as
 * required checks pass; otherwise it's left open for review.
 */
export function AutoMergeToggle({
  id,
  checked,
  disabled,
  onChange,
}: AutoMergeToggleProps) {
  const title = disabled
    ? 'Admin-only. When checked, the catalog PR auto-merges as soon as required checks pass; otherwise the PR is left open for human review.'
    : 'When checked, the catalog PR auto-merges as soon as required checks pass. Uncheck to leave the PR open for review before the addon is added.'
  return (
    <div className="flex items-center gap-2">
      <input
        type="checkbox"
        id={id}
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        title={title}
        className="rounded border-[#5a9dd0] disabled:opacity-50 dark:border-gray-600"
      />
      <label
        htmlFor={id}
        title={title}
        className={`text-sm font-medium ${disabled ? 'text-[#5a8aaa] dark:text-gray-500' : 'text-[#0a3a5a] dark:text-gray-300'}`}
      >
        Merge PR automatically
      </label>
      {disabled && (
        <span className="text-xs text-[#5a8aaa] dark:text-gray-500">
          (admin only)
        </span>
      )}
    </div>
  )
}

export interface DryRunPreviewProps {
  result: DryRunResult
}

/**
 * DryRunPreview — renders the files the addAddon(dry_run:true) call WOULD
 * write, with create/update markers. No PR, no commit. Every array read is
 * null-safe (`?? []`) because the Go DryRunResult serializes the slice as
 * `files_to_write` while older fixtures may use `files`.
 */
export function DryRunPreview({ result }: DryRunPreviewProps) {
  const files = result.files_to_write ?? result.files ?? []
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
            <ul className="mt-1 space-y-0.5 font-mono">
              {files.map((f) => (
                <li key={f.path}>
                  <span
                    className={
                      f.action === 'create'
                        ? 'text-green-600 dark:text-green-400'
                        : 'text-amber-600 dark:text-amber-400'
                    }
                  >
                    {f.action === 'create' ? '+' : '~'}
                  </span>{' '}
                  {f.path}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  )
}

export interface SubmitPhaseBannerProps {
  phase: SubmitPhase
}

/**
 * SubmitPhaseBanner — coarse branch → commit → PR → merge progress shown
 * while the submit request is in flight and on its terminal result. A single
 * synchronous POST can't stream the individual git steps, so we surface a
 * spinner while submitting and the merged/opened outcome afterwards.
 */
export function SubmitPhaseBanner({ phase }: SubmitPhaseBannerProps) {
  return (
    <PRProgressBanner
      phase={phase}
      mergedMessage="PR merged — addon added to your catalog"
      openedMessage="PR opened — merge it to apply"
    />
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
