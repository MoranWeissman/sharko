import { useEffect, useMemo, useRef, useState } from 'react'
import * as yaml from 'yaml'
import {
  AlertTriangle,
  CheckCircle2,
  ExternalLink,
  Eye,
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
 * Two views via a tab toggle:
 *  - YAML: a monospace textarea editor with live YAML validation feedback.
 *  - Diff: side-by-side current vs. edited, line-numbered.
 *
 * Schema-driven autocomplete is intentionally NOT bundled here — Monaco was
 * not on the dependency list and pulling it in would add ~5MB to the bundle
 * for an MVP feature. When `schema` is supplied we surface a small "Schema
 * available — top-level keys" hint above the editor so the user knows what's
 * legal. Full schema-driven autocomplete is a v1.21+ enhancement.
 *
 * Submission calls `onSubmit` (an api wrapper closure provided by the parent
 * so the same component handles both global and per-cluster endpoints).
 * On a no-PAT response we render <AttributionNudge> inline near the Submit
 * button. Once the PR is created we surface a toast with the link AND a
 * persistent banner pointing at the PR.
 */
export interface ValuesEditorProps {
  /**
   * Current YAML on disk. Used as the diff baseline AND as the initial
   * editor content. Pass an empty string for "no values configured yet".
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
}

type Tab = 'yaml' | 'diff'

export function ValuesEditor({
  initialYAML,
  schema,
  hasPersonalToken,
  githubFileURL,
  onSubmit,
  title,
  subtitle,
  allowEmpty = false,
}: ValuesEditorProps) {
  const [draft, setDraft] = useState(initialYAML)
  const [activeTab, setActiveTab] = useState<Tab>('yaml')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [lastResult, setLastResult] = useState<ValuesEditResult | null>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Reset when the underlying current values change (e.g. after a successful
  // submit + reload from the parent).
  useEffect(() => {
    setDraft(initialYAML)
    setLastResult(null)
    setSubmitError(null)
  }, [initialYAML])

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
    setLastResult(null)
    try {
      const res = await onSubmit(draft)
      const prURL = res.pr_url || res.result?.pr_url
      setLastResult(res)
      if (prURL) {
        showToast(`PR opened — ${prURL.split('/').slice(-2).join('/')}`, 'success')
      } else {
        showToast('Values saved (auto-merge enabled)', 'success')
      }
    } catch (e) {
      setSubmitError(e instanceof Error ? e.message : 'Failed to submit values')
    } finally {
      setSubmitting(false)
    }
  }

  const showProactiveNudge = hasPersonalToken === false
  const responsePR = lastResult?.pr_url || lastResult?.result?.pr_url

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-white p-5 dark:ring-gray-700 dark:bg-gray-800">
      <div className="mb-3 flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 className="text-base font-semibold text-[#0a2a4a] dark:text-gray-100">{title}</h3>
          {subtitle && (
            <p className="mt-0.5 text-xs text-[#3a6a8a] dark:text-gray-400">{subtitle}</p>
          )}
        </div>
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
      </div>

      {/* Tabs */}
      <div className="mb-3 flex gap-1 border-b border-[#c0ddf0] dark:border-gray-700">
        <TabButton active={activeTab === 'yaml'} onClick={() => setActiveTab('yaml')}>
          YAML
        </TabButton>
        <TabButton active={activeTab === 'diff'} onClick={() => setActiveTab('diff')}>
          Diff{isDirty && ' •'}
        </TabButton>
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

      {/* YAML view */}
      {activeTab === 'yaml' && (
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
              <span className="flex items-center gap-1 text-teal-600 dark:text-teal-400">
                <Eye className="h-3 w-3" />
                Unsaved changes
              </span>
            ) : (
              <span>No changes</span>
            )}
          </div>
        </div>
      )}

      {/* Diff view */}
      {activeTab === 'diff' && <DiffPanel oldYAML={initialYAML} newYAML={draft} />}

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

      {/* Result + actions */}
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

      <div className="mt-4 flex flex-wrap items-center justify-end gap-2">
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
    </div>
  )
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`-mb-px border-b-2 px-3 py-1.5 text-xs font-medium transition ${
        active
          ? 'border-teal-500 text-teal-700 dark:text-teal-400'
          : 'border-transparent text-[#3a6a8a] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:text-gray-200'
      }`}
    >
      {children}
    </button>
  )
}

function DiffPanel({ oldYAML, newYAML }: { oldYAML: string; newYAML: string }) {
  const oldLines = useMemo(() => oldYAML.split('\n'), [oldYAML])
  const newLines = useMemo(() => newYAML.split('\n'), [newYAML])

  // Quick line-by-line diff: same index = compare. Lines unique to one side
  // are flagged. This is intentionally not LCS — for human review of values
  // files, line-aligned diff matches the way users edit YAML (in place).
  const rows = useMemo(() => {
    const max = Math.max(oldLines.length, newLines.length)
    const out: { left: string; right: string; same: boolean }[] = []
    for (let i = 0; i < max; i++) {
      const left = oldLines[i] ?? ''
      const right = newLines[i] ?? ''
      out.push({ left, right, same: left === right })
    }
    return out
  }, [oldLines, newLines])

  if (oldYAML === newYAML) {
    return (
      <div className="flex h-32 items-center justify-center rounded-md border border-dashed border-[#c0ddf0] text-sm text-[#3a6a8a] dark:border-gray-700 dark:text-gray-500">
        No changes to show.
      </div>
    )
  }

  return (
    <div className="overflow-auto rounded-md border border-[#c0ddf0] dark:border-gray-700">
      <table className="w-full font-mono text-[11px] leading-5">
        <thead className="bg-[#e0f0ff] text-left text-[10px] uppercase tracking-wide text-[#3a6a8a] dark:bg-gray-700 dark:text-gray-400">
          <tr>
            <th className="w-10 px-2 py-1 text-right">Line</th>
            <th className="px-2 py-1">Current</th>
            <th className="w-10 px-2 py-1 text-right">Line</th>
            <th className="px-2 py-1">Edited</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr
              key={i}
              className={
                row.same
                  ? ''
                  : 'bg-amber-50 dark:bg-amber-950/30'
              }
            >
              <td className="border-r border-[#e0f0ff] px-2 py-0.5 text-right text-[#3a6a8a] dark:border-gray-700 dark:text-gray-500">
                {i < oldLines.length ? i + 1 : ''}
              </td>
              <td className="whitespace-pre border-r border-[#e0f0ff] px-2 py-0.5 text-[#0a2a4a] dark:border-gray-700 dark:text-gray-100">
                {row.left || '\u00a0'}
              </td>
              <td className="border-r border-[#e0f0ff] px-2 py-0.5 text-right text-[#3a6a8a] dark:border-gray-700 dark:text-gray-500">
                {i < newLines.length ? i + 1 : ''}
              </td>
              <td className="whitespace-pre px-2 py-0.5 text-[#0a2a4a] dark:text-gray-100">
                {row.right || '\u00a0'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
