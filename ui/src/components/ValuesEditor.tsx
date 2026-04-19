import { useEffect, useMemo, useRef, useState } from 'react'
import * as yaml from 'yaml'
import {
  AlertTriangle,
  CheckCircle2,
  CloudDownload,
  ExternalLink,
  GitPullRequest,
  Loader2,
  RotateCcw,
  Save,
} from 'lucide-react'
import { AttributionNudge } from '@/components/AttributionNudge'
import { showToast } from '@/components/ToastNotification'
import type { ValuesEditResult } from '@/services/models'

/**
 * ValuesEditor — the v1.20 in-app YAML editor for addon values.
 *
 * Single textarea preloaded with the Git content. Edit. Submit. The PR-diff
 * lives on GitHub; we don't replicate it here (v1.20.2 — diff pane removed
 * after maintainer feedback: "values is values, Helm takes it as-is").
 *
 * Schema-driven autocomplete intentionally not bundled — Monaco would add
 * ~5MB to the build. When `schema` is supplied we surface a top-level-keys
 * hint as a breadcrumb so editors know what's legal.
 *
 * Submission calls `onSubmit` (an api wrapper closure provided by the parent
 * so the same component handles both global and per-cluster endpoints). On
 * success: toast + PR-link banner + buffer reset + parent-triggered refresh.
 * On a no-PAT response, renders <AttributionNudge> inline.
 */
export interface ValuesEditorProps {
  /**
   * Current YAML on disk. Used as the baseline AND the initial editor
   * content. Pass an empty string for "no values configured yet".
   */
  initialYAML: string
  /** Optional JSON Schema; if missing, the schema-hint section is hidden. */
  schema?: Record<string, unknown> | null
  /** Pre-fetched "user has a personal PAT" flag — drives the proactive nudge. */
  hasPersonalToken?: boolean
  /** A direct GitHub URL to the values file. Renders the "Edit in GitHub" link. */
  githubFileURL?: string
  /** Called when the user clicks Submit. Should return the API response. */
  onSubmit: (newYAML: string) => Promise<ValuesEditResult>
  /**
   * Heading shown above the editor (e.g. "Global Values", "Cluster Overrides").
   */
  title: string
  /** Subtitle/explanation. Optional. */
  subtitle?: string
  /**
   * When true, the empty buffer ("") is treated as a valid submission meaning
   * "remove the file / reset to defaults". Otherwise empty submits are blocked.
   * Default: false.
   */
  allowEmpty?: boolean
  /**
   * v1.21 (Story V121-6.5): version-mismatch banner.
   *
   * When the backend reports `values_version_mismatch` on the
   * GET /addons/{name}/values-schema response, the parent passes the
   * pair plus a refresh callback. The banner only renders when both
   * versions are present.
   *
   * The refresh handler should call
   * `api.refreshAddonValuesFromUpstream(addon)` (which is the existing
   * PUT endpoint with `refresh_from_upstream: true`). The endpoint
   * regenerates the global file via the smart-values pipeline and opens
   * a Tier 2 PR — the locked decision is to keep this on the same
   * handler as manual edits.
   */
  versionMismatch?: { catalogVersion: string; valuesVersion: string } | null
  onRefreshFromUpstream?: () => Promise<ValuesEditResult>
  /**
   * Children rendered below the editor (before the Reset/Submit button row).
   * Used by parents to slot in a "Recent changes" panel without requiring
   * a ValuesEditorWrapper component.
   *
   * The render-prop form receives a `refreshKey` that changes after every
   * successful submit — pass it through to children so they re-fetch.
   */
  belowEditor?: React.ReactNode | ((ctx: { refreshKey: number }) => React.ReactNode)
}

export function ValuesEditor({
  initialYAML,
  schema,
  hasPersonalToken,
  githubFileURL,
  onSubmit,
  title,
  subtitle,
  allowEmpty = false,
  versionMismatch,
  onRefreshFromUpstream,
  belowEditor,
}: ValuesEditorProps) {
  const [draft, setDraft] = useState(initialYAML)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [lastResult, setLastResult] = useState<ValuesEditResult | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [bannerDismissed, setBannerDismissed] = useState(false)
  const [discardConfirmOpen, setDiscardConfirmOpen] = useState(false)
  const [refreshKey, setRefreshKey] = useState(0)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Reset when the underlying current values change (e.g. after a successful
  // submit + reload from the parent).
  useEffect(() => {
    setDraft(initialYAML)
    setSubmitError(null)
  }, [initialYAML])

  // Reset the banner-dismiss flag whenever the mismatch pair changes — a
  // new chart upgrade should re-surface the banner even if the user
  // dismissed the previous one.
  useEffect(() => {
    setBannerDismissed(false)
  }, [versionMismatch?.catalogVersion, versionMismatch?.valuesVersion])

  // Live YAML validation — non-blocking; we surface the parser error in a
  // small inline strip above the editor and disable the Submit button when
  // the YAML cannot be parsed (unless `allowEmpty` permits a blank document).
  const yamlError = useMemo(() => {
    if (draft.trim() === '') return null
    try {
      yaml.parse(draft)
      return null
    } catch (e) {
      return e instanceof Error ? e.message : 'Invalid YAML'
    }
  }, [draft])

  const isDirty = draft !== initialYAML
  const canSubmit =
    isDirty && !submitting && !yamlError && (allowEmpty || draft.trim() !== '')

  const schemaTopLevelKeys = useMemo(() => {
    if (!schema || typeof schema !== 'object') return null
    const props = (schema as { properties?: Record<string, unknown> }).properties
    if (!props || typeof props !== 'object') return null
    return Object.keys(props).sort()
  }, [schema])

  const handleSubmit = async () => {
    if (!canSubmit) return
    setSubmitting(true)
    setSubmitError(null)
    try {
      const res = await onSubmit(draft)
      const prURL = res.pr_url || res.result?.pr_url
      const prID = res.pr_id ?? res.result?.pr_id
      const merged = res.merged ?? res.result?.merged ?? false
      setLastResult(res)
      if (prURL) {
        // Auto-merge may already have fired server-side; don't claim
        // "opened for review" when the PR is already merged. Otherwise
        // stay neutral — the maintainer may have GitHub auto-merge on.
        const label = prID ? `PR #${prID}` : 'PR'
        if (merged) {
          showToast(`${label} merged →`, 'success')
        } else {
          showToast(`${label} opened →`, 'success')
        }
      } else {
        showToast('Values saved', 'success')
      }
      // Bump refresh key so <RecentPRsPanel> re-fetches and the new PR shows up.
      setRefreshKey((k) => k + 1)
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Failed to submit values'
      setSubmitError(msg)
      showToast(`Failed to submit values — ${msg}`, 'info')
    } finally {
      setSubmitting(false)
    }
  }

  const showProactiveNudge = hasPersonalToken === false
  const responsePR = lastResult?.pr_url || lastResult?.result?.pr_url

  // Story V121-6.5 banner: only render when the backend reported a real
  // mismatch AND the user has not dismissed the banner for this pair AND
  // a refresh handler is wired (parents that don't support refresh —
  // per-cluster overrides editor — will pass undefined).
  const showMismatchBanner =
    !!versionMismatch && !!onRefreshFromUpstream && !bannerDismissed

  const handleRefresh = async () => {
    if (!onRefreshFromUpstream) return
    setRefreshing(true)
    setSubmitError(null)
    try {
      const res = await onRefreshFromUpstream()
      setLastResult(res)
      const prURL = res.pr_url || res.result?.pr_url
      const prID = res.pr_id ?? res.result?.pr_id
      const merged = res.merged ?? res.result?.merged ?? false
      if (prURL) {
        const label = prID ? `PR #${prID}` : 'PR'
        showToast(merged ? `${label} merged →` : `${label} opened →`, 'success')
      } else {
        showToast('Upstream values applied', 'success')
      }
      setBannerDismissed(true)
      setRefreshKey((k) => k + 1)
    } catch (e) {
      const msg = e instanceof Error ? e.message : 'Failed to refresh from upstream'
      setSubmitError(msg)
      showToast(`Failed to refresh — ${msg}`, 'info')
    } finally {
      setRefreshing(false)
    }
  }

  const renderedBelow =
    typeof belowEditor === 'function' ? belowEditor({ refreshKey }) : belowEditor

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-white p-5 dark:ring-gray-700 dark:bg-gray-800">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">{title}</h3>
          {subtitle && (
            <p className="mt-0.5 text-xs text-[#3a6a8a] dark:text-gray-400">{subtitle}</p>
          )}
        </div>
        <div className="flex items-center gap-2">
          {githubFileURL && (
            <a
              href={githubFileURL}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1 rounded-md border border-[#6aade0] bg-[#e0f0ff] px-3 py-1 text-xs font-medium text-[#0a6aaa] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-700 dark:text-[#6aade0] dark:hover:bg-gray-600"
            >
              <ExternalLink className="h-3 w-3" />
              Edit in GitHub
            </a>
          )}
          {/*
            v1.21 (Story V121-6.5): the always-visible "Pull upstream
            defaults" actions menu was removed. The same functionality
            now appears as a contextual banner above the editor when the
            values file's stamped chart version is older than the
            catalog's pinned version. See `showMismatchBanner` below.
          */}
        </div>
      </div>

      {/* Version-mismatch banner (Story V121-6.5). */}
      {showMismatchBanner && versionMismatch && (
        <div
          role="alert"
          className="mb-3 flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <div className="flex-1">
            <p>
              Chart upgraded to{' '}
              <span className="font-mono font-semibold">{versionMismatch.catalogVersion}</span> —
              values were generated for{' '}
              <span className="font-mono">{versionMismatch.valuesVersion}</span>. Refresh values
              from upstream?
            </p>
            <div className="mt-2 flex items-center gap-2">
              <button
                type="button"
                onClick={handleRefresh}
                disabled={refreshing}
                className="inline-flex items-center gap-1 rounded-md bg-amber-600 px-3 py-1 text-xs font-semibold text-white hover:bg-amber-700 disabled:opacity-50 dark:bg-amber-500 dark:hover:bg-amber-400"
              >
                {refreshing ? (
                  <>
                    <Loader2 className="h-3 w-3 animate-spin" />
                    Refreshing…
                  </>
                ) : (
                  <>
                    <CloudDownload className="h-3 w-3" />
                    Refresh now
                  </>
                )}
              </button>
              <button
                type="button"
                onClick={() => setBannerDismissed(true)}
                disabled={refreshing}
                className="rounded-md border border-amber-400 bg-white px-3 py-1 text-xs font-medium text-amber-900 hover:bg-amber-100 disabled:opacity-50 dark:border-amber-600 dark:bg-gray-800 dark:text-amber-200 dark:hover:bg-amber-900/40"
              >
                Dismiss
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Schema hint */}
      {schemaTopLevelKeys && schemaTopLevelKeys.length > 0 && (
        <div className="mb-3 rounded-md border border-[#c0ddf0] bg-[#f0f7ff] px-3 py-2 text-xs text-[#1a4a6a] dark:border-gray-700 dark:bg-gray-700/40 dark:text-gray-300">
          <span className="font-semibold">Schema available — top-level keys: </span>
          <span className="font-mono">
            {schemaTopLevelKeys.slice(0, 12).join(', ')}
            {schemaTopLevelKeys.length > 12 && ` (+${schemaTopLevelKeys.length - 12} more)`}
          </span>
        </div>
      )}

      {/* YAML editor — single textarea, no diff pane. GitHub renders the
          diff on the PR; duplicating it here was confusing. */}
      <div>
        <textarea
          ref={textareaRef}
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          spellCheck={false}
          className="block min-h-[320px] w-full resize-y rounded-md border border-[#c0ddf0] bg-[#f8fbff] p-3 font-mono text-xs leading-5 text-[#0a2a4a] focus:border-[#6aade0] focus:outline-none focus:ring-2 focus:ring-[#6aade0]/30 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-100"
          placeholder="# YAML values&#10;# e.g.&#10;# replicaCount: 2&#10;# resources:&#10;#   limits:&#10;#     memory: 256Mi"
        />
        <div className="mt-1 flex items-center justify-between text-xs text-[#3a6a8a] dark:text-gray-500">
          <span>
            {draft.split('\n').length} lines · {draft.length} chars
            {isDirty && !yamlError && (
              <span className="ml-2 text-[#3a6a8a] dark:text-gray-500">· edited</span>
            )}
          </span>
          {yamlError && (
            <span className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
              <AlertTriangle className="h-3 w-3" />
              <span className="truncate" title={yamlError}>
                YAML error: {yamlError.slice(0, 80)}
              </span>
            </span>
          )}
        </div>
      </div>

      {/* Proactive nudge — render when the user has no personal PAT yet. */}
      {showProactiveNudge && (
        <div className="mt-4">
          <AttributionNudge inline />
        </div>
      )}

      {/* Reactive nudge — backend told us it fell back to the service token. */}
      {lastResult?.attribution_warning === 'no_per_user_pat' && !showProactiveNudge && (
        <div className="mt-4">
          <AttributionNudge inline />
        </div>
      )}

      {/* PR banner — neutral language; auto-merge may have already fired so
          we don't claim "opened for review". "merged" is shown when the
          response confirms it; otherwise just "opened". */}
      {responsePR && (
        <div className="mt-4 flex items-start gap-2 rounded-md border border-green-300 bg-green-50 p-3 text-sm text-green-900 dark:border-green-700 dark:bg-green-950/40 dark:text-green-200">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
          <div className="flex-1">
            <p className="font-medium">
              {(lastResult?.merged ?? lastResult?.result?.merged) ? 'PR merged' : 'PR opened'}
            </p>
            <a
              href={responsePR}
              target="_blank"
              rel="noopener noreferrer"
              className="mt-1 inline-flex items-center gap-1 text-xs font-medium underline hover:no-underline"
            >
              <GitPullRequest className="h-3 w-3" />
              {responsePR}
              <ExternalLink className="h-3 w-3" />
            </a>
          </div>
        </div>
      )}

      {submitError && (
        <p className="mt-3 text-sm text-red-600 dark:text-red-400">{submitError}</p>
      )}

      {renderedBelow && <div className="mt-4">{renderedBelow}</div>}

      <div className="mt-4 flex flex-wrap items-center justify-end gap-2">
        <button
          type="button"
          onClick={() => setDiscardConfirmOpen(true)}
          disabled={!isDirty || submitting}
          title="Discard your edits and revert to the saved version"
          className="inline-flex items-center gap-1 rounded-md border border-[#c0ddf0] bg-white px-3 py-1.5 text-xs font-medium text-[#1a4a6a] hover:bg-[#e0f0ff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
        >
          <RotateCcw className="h-3 w-3" />
          Discard changes
        </button>
        <button
          type="button"
          onClick={handleSubmit}
          disabled={!canSubmit}
          title={
            !isDirty
              ? 'No changes to submit'
              : yamlError
                ? 'Fix YAML errors first'
                : 'Open a PR with your changes'
          }
          className="inline-flex items-center gap-1 rounded-md bg-teal-600 px-4 py-1.5 text-xs font-semibold text-white shadow hover:bg-teal-700 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-500 dark:hover:bg-teal-400"
        >
          {submitting ? (
            <>
              <Loader2 className="h-3 w-3 animate-spin" />
              Submitting…
            </>
          ) : (
            <>
              <Save className="h-3 w-3" />
              Submit changes
            </>
          )}
        </button>
      </div>

      {/* Discard-changes confirm modal — uncommitted edits are easy to lose
          by accident, so we always confirm before reverting the buffer. */}
      {discardConfirmOpen && (
        <div
          role="dialog"
          aria-modal="true"
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
        >
          <div className="w-full max-w-md rounded-xl bg-white p-5 shadow-xl dark:bg-gray-800">
            <h4 className="flex items-center gap-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
              <RotateCcw className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" />
              Discard your edits?
            </h4>
            <p className="mt-2 text-sm text-[#1a4a6a] dark:text-gray-300">
              You'll lose the changes you've made. The editor will revert to the
              currently saved version.
            </p>
            <div className="mt-4 flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={() => setDiscardConfirmOpen(false)}
                className="rounded-md border border-[#c0ddf0] bg-white px-3 py-1.5 text-xs font-medium text-[#1a4a6a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
              >
                Keep editing
              </button>
              <button
                type="button"
                onClick={() => {
                  setDraft(initialYAML)
                  setSubmitError(null)
                  setDiscardConfirmOpen(false)
                }}
                className="inline-flex items-center gap-1 rounded-md bg-amber-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-amber-700 dark:bg-amber-500 dark:hover:bg-amber-400"
              >
                <RotateCcw className="h-3 w-3" />
                Discard changes
              </button>
            </div>
          </div>
        </div>
      )}

      {/*
        Note: the v1.20.2 "Pull upstream defaults" confirm modal lived
        here. It was removed in v1.21 (Story V121-6.5). The contextual
        version-mismatch banner above replaces it — refresh now happens
        in-place from the banner, no second confirm step. The locked
        rationale is that the banner only fires when the values are
        actually stale, so the "you'll lose your edits" warning the
        modal carried is no longer the user's first encounter with the
        action.
      */}
    </div>
  )
}
