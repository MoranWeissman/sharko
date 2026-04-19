import { useCallback, useEffect, useId, useRef, useState } from 'react'
import {
  AlertCircle,
  CheckCircle2,
  Clipboard,
  Loader2,
  PackageOpen,
} from 'lucide-react'
import { api } from '@/services/api'
import type {
  CatalogEntry,
  CatalogValidateResponse,
  CatalogVersionsResponse,
} from '@/services/models'
import { MarketplaceConfigureModal } from '@/components/MarketplaceConfigureModal'

/**
 * MarketplacePasteURLTab — V121-4.1 power-user tab.
 *
 * Lets the operator paste a Helm repo URL + chart name and validate the chart
 * exists before opening Configure. Useful for charts that aren't in our
 * curated catalog and aren't on ArtifactHub (e.g. internal repos behind a CDN
 * or homepage-hosted vendor charts).
 *
 * Flow:
 *   1. User fills repo + chart, optionally version.
 *   2. Click Validate (or tab out of chart input) → call /catalog/validate.
 *   3. On valid: green check + "Found N versions" + Configure button.
 *      Clicking Configure opens MarketplaceConfigureModal pre-filled with the
 *      validated chart + the latest stable version (or the user's override).
 *   4. On invalid: structured inline error keyed off `error_code` so we can
 *      give targeted remediation advice (typo'd repo vs. typo'd chart name).
 *
 * Accessibility (WCAG 2.1 AA, design §4.8 / NFR-V121-4):
 *   - Each input has an associated <label htmlFor>.
 *   - Validation result is announced via role="status" / role="alert" with
 *     aria-live="polite" so screen readers hear the success/failure.
 *   - Error messages reference their input via aria-describedby.
 *   - The Configure button receives focus on successful validation so
 *     keyboard users land in the next logical control.
 *   - Tab is the third sibling on MarketplaceTab; arrow-key nav is wired
 *     in the parent's tablist.
 *
 * The handoff to MarketplaceConfigureModal:
 *   We synthesise a minimal CatalogEntry from the validate response and pass
 *   `source="paste_url"` + `skipVersionFetch` so the modal doesn't try to
 *   re-fetch versions from /catalog/addons/{name}/versions (which would 404
 *   for non-curated charts).
 */

const PLACEHOLDER_REPO = 'https://charts.jetstack.io'
const PLACEHOLDER_CHART = 'cert-manager'

interface ValidateState {
  status: 'idle' | 'loading' | 'valid' | 'invalid' | 'error'
  resp?: CatalogValidateResponse
  /** Network or unexpected error (validate handler returns 200 even on failure). */
  message?: string
}

export function MarketplacePasteURLTab() {
  // Form state.
  const [repo, setRepo] = useState('')
  const [chart, setChart] = useState('')
  const [versionOverride, setVersionOverride] = useState('')

  // Validation result.
  const [validateState, setValidateState] = useState<ValidateState>({ status: 'idle' })

  // Configure modal state. We open it on Configure-click with a synthesised
  // CatalogEntry built from the validate response.
  const [configureEntry, setConfigureEntry] = useState<CatalogEntry | null>(null)
  const [configureSeed, setConfigureSeed] = useState<CatalogVersionsResponse | null>(null)
  const [modalOpen, setModalOpen] = useState(false)

  const repoInputId = useId()
  const chartInputId = useId()
  const versionInputId = useId()
  const errorId = useId()

  const repoRef = useRef<HTMLInputElement>(null)
  const configureBtnRef = useRef<HTMLButtonElement>(null)

  // Auto-focus the repo input when the tab mounts so power users can start
  // typing/pasting immediately.
  useEffect(() => {
    repoRef.current?.focus()
  }, [])

  // Form validity for the Validate button. We do basic shape checks here so
  // the button is meaningfully disabled — the backend re-validates anyway.
  const repoTrimmed = repo.trim()
  const chartTrimmed = chart.trim()
  const canValidate =
    repoTrimmed.length > 0 &&
    chartTrimmed.length > 0 &&
    (repoTrimmed.startsWith('http://') || repoTrimmed.startsWith('https://')) &&
    validateState.status !== 'loading'

  const runValidate = useCallback(async () => {
    if (!canValidate) return
    setValidateState({ status: 'loading' })
    try {
      const resp = await api.validateCatalogChart(repoTrimmed, chartTrimmed)
      if (resp.valid) {
        setValidateState({ status: 'valid', resp })
        // Default the version override to latest stable so the inline display
        // matches what the modal will pre-fill.
        if (resp.latest_stable && versionOverride.trim() === '') {
          setVersionOverride(resp.latest_stable)
        }
        // Move focus to Configure so keyboard users don't have to tab
        // through the form again.
        setTimeout(() => configureBtnRef.current?.focus(), 0)
      } else {
        setValidateState({ status: 'invalid', resp })
      }
    } catch (e) {
      setValidateState({
        status: 'error',
        message: e instanceof Error ? e.message : 'Validation request failed',
      })
    }
  }, [canValidate, repoTrimmed, chartTrimmed, versionOverride])

  // Reset validation when the inputs change so a stale green check doesn't
  // mislead the user after they edited the chart name.
  const onRepoChange = (next: string) => {
    setRepo(next)
    if (validateState.status !== 'idle') setValidateState({ status: 'idle' })
  }
  const onChartChange = (next: string) => {
    setChart(next)
    if (validateState.status !== 'idle') setValidateState({ status: 'idle' })
  }

  const onValidateKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      void runValidate()
    }
  }

  const handleConfigure = () => {
    if (validateState.status !== 'valid' || !validateState.resp) return
    const r = validateState.resp
    const versionToUse =
      versionOverride.trim() ||
      r.latest_stable ||
      r.versions?.[0]?.version ||
      ''

    // Synthesise a CatalogEntry the modal can consume. Fields that have no
    // upstream source (license, category, security_score) get sensible
    // defaults — they're cosmetic in the Configure summary and not persisted.
    const synthesised: CatalogEntry = {
      name: chartTrimmed,
      description: r.description ?? '',
      chart: chartTrimmed,
      repo: r.repo,
      default_namespace: chartTrimmed,
      default_sync_wave: 0,
      maintainers: [],
      license: '',
      category: 'developer-tools',
      curated_by: [],
      homepage: r.icon_url ? undefined : undefined,
    }

    // Seed the picker with versions from the validate response so the modal
    // doesn't re-fetch from the curated endpoint (which would 404).
    const seeded: CatalogVersionsResponse = {
      addon: chartTrimmed,
      chart: chartTrimmed,
      repo: r.repo,
      versions: r.versions ?? [],
      latest_stable: versionToUse,
      cached_at: r.cached_at ?? new Date().toISOString(),
    }
    setConfigureEntry(synthesised)
    setConfigureSeed(seeded)
    setModalOpen(true)
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Intro card — terse explainer so users know when this tab is the
          right tool vs. Browse / Search. */}
      <div className="rounded-lg border border-[#c0ddf0] bg-[#f0f7ff] p-4 text-sm text-[#0a3a5a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300">
        <div className="mb-1 flex items-center gap-2 font-semibold">
          <Clipboard className="h-4 w-4 text-teal-600" aria-hidden="true" />
          Add a chart by URL
        </div>
        <p className="text-xs text-[#2a5a7a] dark:text-gray-400">
          Use this when the chart isn&rsquo;t in our curated catalog or on
          ArtifactHub &mdash; e.g. internal repos or vendor-hosted charts. We
          fetch <code className="rounded bg-[#e8f3fb] px-1 py-0.5 text-[11px] dark:bg-gray-800">{'<repo>/index.yaml'}</code>{' '}
          and confirm the chart exists before opening Configure.
        </p>
      </div>

      {/* Form */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <label
            htmlFor={repoInputId}
            className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
          >
            Chart repo URL
            <span className="text-red-500" aria-hidden="true">
              {' '}
              *
            </span>
          </label>
          <input
            ref={repoRef}
            id={repoInputId}
            type="url"
            value={repo}
            onChange={(e) => onRepoChange(e.target.value)}
            onKeyDown={onValidateKeyDown}
            placeholder={PLACEHOLDER_REPO}
            aria-required="true"
            aria-invalid={validateState.status === 'invalid' || undefined}
            aria-describedby={
              validateState.status === 'invalid' || validateState.status === 'error'
                ? errorId
                : undefined
            }
            className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
          />
          <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-500">
            The URL of the Helm repository, not the chart archive. Example:{' '}
            <code>{PLACEHOLDER_REPO}</code>.
          </p>
        </div>

        <div>
          <label
            htmlFor={chartInputId}
            className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
          >
            Chart name
            <span className="text-red-500" aria-hidden="true">
              {' '}
              *
            </span>
          </label>
          <input
            id={chartInputId}
            type="text"
            value={chart}
            onChange={(e) => onChartChange(e.target.value)}
            onKeyDown={onValidateKeyDown}
            placeholder={PLACEHOLDER_CHART}
            aria-required="true"
            aria-invalid={validateState.status === 'invalid' || undefined}
            aria-describedby={
              validateState.status === 'invalid' || validateState.status === 'error'
                ? errorId
                : undefined
            }
            className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
          />
          <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-500">
            As listed in the repo&rsquo;s <code>index.yaml</code>.
          </p>
        </div>

        <div>
          <label
            htmlFor={versionInputId}
            className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
          >
            Chart version <span className="text-xs font-normal text-[#5a8aaa]">(optional)</span>
          </label>
          <input
            id={versionInputId}
            type="text"
            value={versionOverride}
            onChange={(e) => setVersionOverride(e.target.value)}
            onKeyDown={onValidateKeyDown}
            placeholder={
              validateState.status === 'valid' && validateState.resp?.latest_stable
                ? `latest stable: ${validateState.resp.latest_stable}`
                : 'auto-fills on validate'
            }
            className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
          />
          <p className="mt-1 text-xs text-[#3a6a8a] dark:text-gray-500">
            Leave blank to use the latest stable version reported by the repo.
          </p>
        </div>
      </div>

      {/* Action row: Validate (always available) + Configure (after valid). */}
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => void runValidate()}
          disabled={!canValidate}
          className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
        >
          {validateState.status === 'loading' ? (
            <>
              <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
              Validating&hellip;
            </>
          ) : (
            'Validate'
          )}
        </button>

        {validateState.status === 'valid' && (
          <button
            ref={configureBtnRef}
            type="button"
            onClick={handleConfigure}
            className="inline-flex items-center gap-2 rounded-md bg-[#0a2a4a] px-4 py-2 text-sm font-semibold text-white hover:bg-[#0d3558] focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0] dark:bg-blue-700 dark:hover:bg-blue-600"
          >
            <PackageOpen className="h-4 w-4" aria-hidden="true" />
            Configure
          </button>
        )}
      </div>

      {/* Live region wraps both success and failure states so screen readers
          hear the validation outcome. role="status" → polite by default. */}
      <div role="status" aria-live="polite" aria-atomic="true">
        {validateState.status === 'valid' && validateState.resp && (
          <div className="flex items-start gap-2 rounded-md border border-green-300 bg-green-50 p-3 text-sm text-green-900 dark:border-green-700 dark:bg-green-950/40 dark:text-green-200">
            <CheckCircle2
              className="mt-0.5 h-4 w-4 shrink-0 text-green-600 dark:text-green-400"
              aria-hidden="true"
            />
            <div className="flex-1">
              <p className="font-medium">
                Found {validateState.resp.versions?.length ?? 0} version
                {(validateState.resp.versions?.length ?? 0) === 1 ? '' : 's'}
                {validateState.resp.latest_stable
                  ? ` (latest stable ${validateState.resp.latest_stable})`
                  : ''}
              </p>
              {validateState.resp.description && (
                <p className="mt-1 line-clamp-2 text-xs text-green-800 dark:text-green-300">
                  {validateState.resp.description}
                </p>
              )}
              <p className="mt-1 text-[11px] text-green-800/70 dark:text-green-400/70">
                {validateState.resp.repo}
                {validateState.resp.chart ? ` · ${validateState.resp.chart}` : ''}
              </p>
            </div>
          </div>
        )}

        {validateState.status === 'invalid' && validateState.resp && (
          <div
            id={errorId}
            role="alert"
            className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
          >
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <div className="flex-1">
              <p className="font-medium">
                {humanLabelForCode(validateState.resp.error_code)}
              </p>
              <p className="mt-1 text-xs">{validateState.resp.message}</p>
              {hintForCode(validateState.resp.error_code)}
            </div>
          </div>
        )}

        {validateState.status === 'error' && (
          <div
            id={errorId}
            role="alert"
            className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
          >
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <div className="flex-1">
              <p className="font-medium">Validation request failed</p>
              <p className="mt-1 text-xs">{validateState.message}</p>
            </div>
          </div>
        )}
      </div>

      {/* Configure modal — pre-filled with the validated chart. We pass
          source="paste_url" so the audit detail records the originating UI
          flow per V121-5.2's locked decision. */}
      <MarketplaceConfigureModal
        entry={configureEntry}
        open={modalOpen}
        onOpenChange={(v) => {
          setModalOpen(v)
          if (!v) {
            setConfigureEntry(null)
            setConfigureSeed(null)
          }
        }}
        source="paste_url"
        skipVersionFetch
        seededVersions={configureSeed}
      />
    </div>
  )
}

// humanLabelForCode maps the structured error_code to a one-line headline.
function humanLabelForCode(code?: string): string {
  switch (code) {
    case 'invalid_input':
      return 'That URL doesn\u2019t look right'
    case 'repo_unreachable':
      return 'Repository unreachable'
    case 'chart_not_found':
      return 'Chart not found in this repo'
    case 'index_parse_error':
      return 'Repository index is malformed'
    case 'timeout':
      return 'Validation timed out'
    default:
      return 'Validation failed'
  }
}

// hintForCode returns a contextual remediation tip, or null when the message
// alone is enough.
function hintForCode(code?: string): React.ReactNode {
  switch (code) {
    case 'repo_unreachable':
      return (
        <p className="mt-1 text-xs">
          Check the URL spelling, that the repo is publicly reachable from the Sharko
          server, and that <code>{'<repo>/index.yaml'}</code> returns 200.
        </p>
      )
    case 'chart_not_found':
      return (
        <p className="mt-1 text-xs">
          The repo is reachable but doesn&rsquo;t list this chart. The chart
          name must match an entry under <code>entries:</code> in{' '}
          <code>index.yaml</code> (case-sensitive).
        </p>
      )
    case 'index_parse_error':
      return (
        <p className="mt-1 text-xs">
          The server fetched <code>index.yaml</code> but couldn&rsquo;t parse
          it as YAML &mdash; the repo may be misconfigured.
        </p>
      )
    case 'timeout':
      return (
        <p className="mt-1 text-xs">
          The repo took longer than 8 seconds to respond. Try again, or check
          for upstream issues.
        </p>
      )
    case 'invalid_input':
      return (
        <p className="mt-1 text-xs">
          URLs must start with <code>http://</code> or <code>https://</code>{' '}
          and include a host.
        </p>
      )
    default:
      return null
  }
}

export default MarketplacePasteURLTab
