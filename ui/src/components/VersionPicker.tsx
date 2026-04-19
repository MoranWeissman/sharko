import { useId, useMemo } from 'react'
import { AlertCircle, Loader2 } from 'lucide-react'
import type {
  CatalogVersionEntry,
  CatalogVersionsResponse,
} from '@/services/models'

/**
 * VersionPicker — shared chart-version picker used by both the Marketplace
 * Configure modal and the manual "Add Addon" form (v1.21 QA Bundle 1).
 *
 * Renders three things, top to bottom:
 *   1. A text <input> with a backing <datalist> for type-as-you-go
 *      autocomplete. Free-text entry is allowed so power users can pin
 *      a version that's not in the visible top-N list.
 *   2. An explicit, always-visible scrollable list of version pills below
 *      the input. Datalists are notoriously inconsistent across browsers
 *      (Firefox in particular only shows them after the user starts
 *      typing), so we mirror the same options as clickable buttons. This
 *      is what makes "click on Chart version → see versions" actually work.
 *   3. A "Show pre-releases" checkbox + a tiny error / hint area.
 *
 * Visible list size: top 10 stable by default. When prereleases are toggled
 * on we expand to the full list. The 10-version cap matches the v1.21 QA
 * report — five was too few for charts like cert-manager that ship a new
 * patch every couple of weeks.
 *
 * Accessibility (WCAG 2.1 AA):
 *   - The input has an explicit <label> via the `labelHtmlFor` it accepts
 *     and surfaces aria-invalid + aria-describedby when validation fails.
 *   - The pill list is rendered as <ul role="listbox"> and each pill is a
 *     <button role="option"> — keyboard-navigable via Tab and selectable
 *     via Enter / Space.
 *   - Loading state surfaces an aria-label on the spinner so SR users hear
 *     "Loading chart versions" rather than nothing.
 */

export interface VersionPickerProps {
  /** Identifier reused for the <input> id and <datalist> id. Caller must
   *  ensure the parent <label> targets the same id (or pass labelHtmlFor). */
  inputId: string
  /** Current value (controlled). */
  value: string
  onChange: (next: string) => void
  /** Backing versions response from /catalog/.../versions or /catalog/validate. */
  versionsResp: CatalogVersionsResponse | null
  loading?: boolean
  /** Top-level fetch error to surface (e.g. "could not reach repo"). */
  error?: string | null
  showPrereleases: boolean
  onShowPrereleasesChange: (next: boolean) => void
  /** Marks the field as invalid (e.g. typed version not in index). */
  invalid?: boolean
  /** Optional aria-describedby override. Defaults to picker-internal error. */
  ariaDescribedBy?: string
  /** Pre-release toggle copy. Defaults to "Show pre-releases" — kept as a
   *  prop in case a host wants different wording. */
  togglePrereleasesLabel?: string
  /** Override the default 10-stable cap. Pre-releases when toggled always
   *  show the full list regardless. */
  maxStableVisible?: number
  /** Placeholder for the input when not loading. */
  placeholder?: string
}

export function VersionPicker({
  inputId,
  value,
  onChange,
  versionsResp,
  loading = false,
  error,
  showPrereleases,
  onShowPrereleasesChange,
  invalid = false,
  ariaDescribedBy,
  togglePrereleasesLabel = 'Show pre-releases',
  maxStableVisible = 10,
  placeholder,
}: VersionPickerProps) {
  const datalistId = `${inputId}-list`
  const errorId = useId()

  // Visible pill list:
  //   - With pre-releases off: top N stable (default 10).
  //   - With pre-releases on: the full list as returned by the server (which
  //     already sorts descending by SemVer with pre-releases interleaved).
  // We keep the existing "stable first 10" behaviour even when no
  // versionsResp.latest_stable is set — the backend filter on `prerelease`
  // is the source of truth.
  const visibleVersions: CatalogVersionEntry[] = useMemo(() => {
    if (!versionsResp) return []
    if (showPrereleases) return versionsResp.versions
    const stable = versionsResp.versions.filter((v) => !v.prerelease)
    return stable.slice(0, maxStableVisible)
  }, [versionsResp, showPrereleases, maxStableVisible])

  const totalCount = versionsResp?.versions.length ?? 0
  const visibleCount = visibleVersions.length

  const describedBy = ariaDescribedBy ?? (invalid ? errorId : undefined)

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-stretch gap-1">
        <input
          id={inputId}
          type="text"
          list={datalistId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          aria-invalid={invalid || undefined}
          aria-describedby={describedBy}
          placeholder={
            loading
              ? 'Loading versions…'
              : (placeholder ?? 'e.g. 1.20.0')
          }
          className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
        />
        {loading && (
          <Loader2
            className="my-auto h-4 w-4 animate-spin text-[#3a6a8a]"
            aria-label="Loading chart versions"
          />
        )}
      </div>

      {/* Datalist mirrors the visible pills so typed autocomplete keeps
          working in Chromium / Safari. Firefox users will rely on the pill
          list below since its datalist behaviour is inconsistent. */}
      <datalist id={datalistId}>
        {visibleVersions.map((v) => (
          <option
            key={v.version}
            value={v.version}
            label={v.app_version ? `app ${v.app_version}` : undefined}
          />
        ))}
      </datalist>

      {/* Always-visible pill list. This is what the QA fix in Bundle 1
          delivers — clicking the field now shows real options instead of
          relying on browser datalist quirks. */}
      {versionsResp && visibleCount > 0 && (
        <ul
          role="listbox"
          aria-label="Releases available for this chart"
          className="mt-1 flex max-h-32 flex-wrap gap-1 overflow-y-auto rounded-md border border-dashed border-[#c0ddf0] bg-[#f7fbff] p-1.5 dark:border-gray-700 dark:bg-gray-900"
        >
          {visibleVersions.map((v) => {
            const selected = v.version === value
            return (
              <li key={v.version} className="contents">
                <button
                  type="button"
                  role="option"
                  aria-selected={selected}
                  onClick={() => onChange(v.version)}
                  title={
                    v.app_version
                      ? `${v.version} (app ${v.app_version})`
                      : v.version
                  }
                  className={`rounded-full px-2 py-0.5 text-xs font-mono transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 ${
                    selected
                      ? 'bg-teal-600 text-white hover:bg-teal-700'
                      : 'bg-white text-[#0a3a5a] ring-1 ring-[#c0ddf0] hover:bg-[#d6eeff] dark:bg-gray-800 dark:text-gray-200 dark:ring-gray-700 dark:hover:bg-gray-700'
                  } ${v.prerelease ? 'italic' : ''}`}
                >
                  {v.version}
                  {v.prerelease && (
                    <span className="ml-1 text-[10px] opacity-75">pre</span>
                  )}
                </button>
              </li>
            )
          })}
        </ul>
      )}

      <label className="mt-1 flex cursor-pointer items-center gap-2 text-xs text-[#2a5a7a] dark:text-gray-400">
        <input
          type="checkbox"
          checked={showPrereleases}
          onChange={(e) => onShowPrereleasesChange(e.target.checked)}
          className="h-3.5 w-3.5 cursor-pointer accent-teal-600"
        />
        {togglePrereleasesLabel}
        {versionsResp && totalCount > visibleCount && !showPrereleases && (
          <span className="text-[#3a6a8a] dark:text-gray-500">
            ({totalCount - visibleCount} more, including pre-releases)
          </span>
        )}
      </label>

      {versionsResp?.latest_stable && (
        <p className="text-xs text-[#3a6a8a] dark:text-gray-500">
          Latest stable: <code>{versionsResp.latest_stable}</code>
        </p>
      )}

      {error && (
        <p className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
          <AlertCircle className="h-3 w-3" aria-hidden="true" />
          {error}
        </p>
      )}

      {invalid && !error && (
        <p
          id={errorId}
          className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400"
        >
          <AlertCircle className="h-3 w-3" aria-hidden="true" />
          Version not found in index.yaml
        </p>
      )}
    </div>
  )
}

export default VersionPicker
