import { useEffect, useMemo, useRef, useState } from 'react'
import * as yaml from 'yaml'
import {
  AlertTriangle,
  CheckCircle2,
  CloudDownload,
  ExternalLink,
  GitPullRequest,
  Loader2,
  MoreHorizontal,
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
   * Callback for "Pull upstream defaults". When provided, the action renders
   * in an "Actions" menu (demoted from a top-level button in v1.20.2). Clicking
   * opens a confirm modal and then calls the pull-upstream API. Omitting this
   * hides the action — used on per-cluster editors where upstream defaults
   * don't apply.
   */
  onPullUpstream?: () => Promise<ValuesEditResult & { chart?: string; chart_version?: string }>
  /**
   * Optional summary text ("cert-manager@v1.14.4") shown in the confirm
   * modal body so the user knows what will be pulled.
   */
  pullUpstreamLabel?: string
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
  onPullUpstream,
  pullUpstreamLabel,
  belowEditor,
}: ValuesEditorProps) {
  const [draft, setDraft] = useState(initialYAML)
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [lastResult, setLastResult] = useState<ValuesEditResult | null>(null)
  const [pullConfirmOpen, setPullConfirmOpen] = useState(false)
  const [pulling, setPulling] = useState(false)
  const [actionsMenuOpen, setActionsMenuOpen] = useState(false)
  const [refreshKey, setRefreshKey] = useState(0)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const actionsMenuRef = useRef<HTMLDivElement>(null)

  // Reset when the underlying current values change (e.g. after a successful
  // submit + reload from the parent).
  useEffect(() => {
    setDraft(initialYAML)
    setSubmitError(null)
  }, [initialYAML])

  // Close the Actions menu on any outside click.
  useEffect(() => {
    if (!actionsMenuOpen) return
    const onDocClick = (e: MouseEvent) => {
      if (actionsMenuRef.current && !actionsMenuRef.current.contains(e.target as Node)) {
        setActionsMenuOpen(false)
      }
    }
    document.addEventListener('mousedown', onDocClick)
    return () => document.removeEventListener('mousedown', onDocClick)
  }, [actionsMenuOpen])

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
      setLastResult(res)
      if (prURL) {
        const prSlug = prURL.split('/').slice(-2).join('/')
        showToast(`PR opened — ${prSlug}`, 'success')
      } else {
        showToast('Values saved (auto-merge enabled)', 'success')
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

  // Demotion rule (v1.20.2): show "Pull upstream defaults" as a prominent
  // button ONLY when the values file is effectively empty (< 5 non-empty
  // lines). Otherwise bury it in the Actions menu so it isn't the first
  // thing a user reaches for on a populated file.
  const emptyishValues = useMemo(() => {
    if (!onPullUpstream) return false
    return initialYAML.split('\n').filter((l) => l.trim() !== '').length < 5
  }, [initialYAML, onPullUpstream])

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
          {/* Actions menu — holds the demoted "Pull upstream defaults" */}
          {onPullUpstream && !emptyishValues && (
            <div className="relative" ref={actionsMenuRef}>
              <button
                type="button"
                onClick={() => setActionsMenuOpen((v) => !v)}
                aria-haspopup="menu"
                aria-expanded={actionsMenuOpen}
                title="More actions"
                className="inline-flex items-center gap-1 rounded-md border border-[#c0ddf0] bg-white px-2 py-1 text-xs font-medium text-[#1a4a6a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
              >
                <MoreHorizontal className="h-3.5 w-3.5" />
              </button>
              {actionsMenuOpen && (
                <div
                  role="menu"
                  className="absolute right-0 z-20 mt-1 min-w-[220px] rounded-md border border-[#c0ddf0] bg-white py-1 shadow-lg dark:border-gray-600 dark:bg-gray-800"
                >
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      setActionsMenuOpen(false)
                      setPullConfirmOpen(true)
                    }}
                    className="flex w-full items-center gap-2 px-3 py-2 text-left text-xs text-[#1a4a6a] hover:bg-[#e0f0ff] dark:text-gray-300 dark:hover:bg-gray-700"
                  >
                    <CloudDownload className="h-3.5 w-3.5" />
                    Pull upstream defaults…
                  </button>
                </div>
              )}
            </div>
          )}
        </div>
      </div>

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
          <span>{draft.split('\n').length} lines · {draft.length} chars</span>
          {yamlError ? (
            <span className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
              <AlertTriangle className="h-3 w-3" />
              <span className="truncate" title={yamlError}>
                YAML error: {yamlError.slice(0, 80)}
              </span>
            </span>
          ) : isDirty ? (
            <span className="text-teal-600 dark:text-teal-400">Unsaved changes</span>
          ) : (
            <span>No changes</span>
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

      {/* PR-opened banner */}
      {responsePR && (
        <div className="mt-4 flex items-start gap-2 rounded-md border border-green-300 bg-green-50 p-3 text-sm text-green-900 dark:border-green-700 dark:bg-green-950/40 dark:text-green-200">
          <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
          <div className="flex-1">
            <p className="font-medium">PR opened for review</p>
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
        {/* When the file is empty-ish, surface Pull upstream as a first-class
            CTA (users have nothing to lose). Otherwise it lives in the
            Actions menu above. */}
        {onPullUpstream && emptyishValues && (
          <button
            type="button"
            onClick={() => setPullConfirmOpen(true)}
            disabled={submitting || pulling}
            title="Replace the current values file with the chart's upstream defaults"
            className="inline-flex items-center gap-1 rounded-md border border-[#c0ddf0] bg-white px-3 py-1.5 text-xs font-medium text-[#1a4a6a] hover:bg-[#e0f0ff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
          >
            <CloudDownload className="h-3 w-3" />
            Pull upstream defaults
          </button>
        )}
        <button
          type="button"
          onClick={() => setDraft(initialYAML)}
          disabled={!isDirty || submitting}
          className="inline-flex items-center gap-1 rounded-md border border-[#c0ddf0] bg-white px-3 py-1.5 text-xs font-medium text-[#1a4a6a] hover:bg-[#e0f0ff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
        >
          <RotateCcw className="h-3 w-3" />
          Reset
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

      {/* Pull-upstream confirm modal */}
      {pullConfirmOpen && onPullUpstream && (
        <div
          role="dialog"
          aria-modal="true"
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
        >
          <div className="w-full max-w-md rounded-xl bg-white p-5 shadow-xl dark:bg-gray-800">
            <h4 className="flex items-center gap-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
              <CloudDownload className="h-4 w-4 text-teal-600 dark:text-teal-400" />
              Pull upstream defaults
            </h4>
            <p className="mt-2 text-sm text-[#1a4a6a] dark:text-gray-300">
              This will replace the current <span className="font-mono">values.yaml</span>{' '}
              {pullUpstreamLabel && (
                <>
                  with upstream defaults from <span className="font-mono">{pullUpstreamLabel}</span>{' '}
                </>
              )}
              and open a PR. <span className="font-semibold">Your current edits will be lost.</span>{' '}
              Continue?
            </p>
            <div className="mt-4 flex items-center justify-end gap-2">
              <button
                type="button"
                onClick={() => setPullConfirmOpen(false)}
                disabled={pulling}
                className="rounded-md border border-[#c0ddf0] bg-white px-3 py-1.5 text-xs font-medium text-[#1a4a6a] hover:bg-[#e0f0ff] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
              >
                Cancel
              </button>
              <button
                type="button"
                disabled={pulling}
                onClick={async () => {
                  setPulling(true)
                  try {
                    const res = await onPullUpstream()
                    setLastResult(res)
                    const prURL = res.pr_url || res.result?.pr_url
                    if (prURL) {
                      showToast(`Upstream pulled — ${prURL.split('/').slice(-2).join('/')}`, 'success')
                    } else {
                      showToast('Upstream values applied (auto-merge)', 'success')
                    }
                    setRefreshKey((k) => k + 1)
                    setPullConfirmOpen(false)
                  } catch (e) {
                    const msg = e instanceof Error ? e.message : 'Failed to pull upstream values'
                    setSubmitError(msg)
                    showToast(`Failed to pull upstream — ${msg}`, 'info')
                  } finally {
                    setPulling(false)
                  }
                }}
                className="inline-flex items-center gap-1 rounded-md bg-teal-600 px-3 py-1.5 text-xs font-semibold text-white hover:bg-teal-700 disabled:opacity-50 dark:bg-teal-500 dark:hover:bg-teal-400"
              >
                {pulling ? (
                  <>
                    <Loader2 className="h-3 w-3 animate-spin" />
                    Pulling…
                  </>
                ) : (
                  <>
                    <CloudDownload className="h-3 w-3" />
                    Pull and open PR
                  </>
                )}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
