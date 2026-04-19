import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  AlertCircle,
  CheckCircle2,
  ExternalLink,
  GitPullRequest,
  Info,
  Loader2,
} from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScorecardBadge } from '@/components/ScorecardBadge'
import { AttributionNudge } from '@/components/AttributionNudge'
import { showToast } from '@/components/ToastNotification'
import { api, addAddon, isAddonAlreadyExistsError, type AddAddonResponse } from '@/services/api'
import type {
  CatalogEntry,
  CatalogVersionEntry,
  CatalogVersionsResponse,
} from '@/services/models'

/**
 * MarketplaceConfigureModal — opens when an operator clicks a Marketplace card.
 *
 * Pre-fills name/namespace/sync-wave from the curated entry's defaults and
 * fetches available chart versions from /catalog/addons/{name}/versions.
 *
 * V121-5 wires Submit to POST /api/v1/addons (the existing v1.20 Tier 2 endpoint
 * that opens a PR against `addons-catalog.yaml`). Three behaviours land here:
 *   - 5.1 Duplicate-guard: pre-flight check against /addons/catalog so Submit
 *     is disabled before the user clicks; defence-in-depth via the server's
 *     409 response (handled inline, not as a generic toast).
 *   - 5.2 Tier 2 attribution: the body carries `source: "marketplace"` so the
 *     existing `addon_added` audit event records the originating UI flow
 *     without inventing a new event name (LOCKED 2026-04-19, Moran).
 *   - 5.3 Success: PR-link banner inside the modal + toast with the PR url.
 *     Auto-merge handled — toast says "merged" vs. "opened" based on response.
 */

export interface MarketplaceConfigureModalProps {
  entry: CatalogEntry | null
  open: boolean
  onOpenChange: (open: boolean) => void
  /**
   * Optional return-focus target. The modal restores focus here on close so the
   * V121-5.3 acceptance criteria ("focus returns to Add Addon button") is met
   * when wired by the parent. When omitted, browsers fall back to the body.
   */
  returnFocusRef?: React.RefObject<HTMLElement>
}

interface DuplicateInfo {
  addon: string
  existingUrl: string
}

export function MarketplaceConfigureModal({
  entry,
  open,
  onOpenChange,
  returnFocusRef,
}: MarketplaceConfigureModalProps) {
  const [name, setName] = useState('')
  const [namespace, setNamespace] = useState('')
  const [syncWave, setSyncWave] = useState<string>('0')
  const [version, setVersion] = useState('')
  const [showPrereleases, setShowPrereleases] = useState(false)
  const [versionTouched, setVersionTouched] = useState(false)

  const [versionsResp, setVersionsResp] = useState<CatalogVersionsResponse | null>(null)
  const [versionsLoading, setVersionsLoading] = useState(false)
  const [versionsError, setVersionsError] = useState<string | null>(null)

  // Pre-flight duplicate check: pull the user's existing catalog once and check
  // every keystroke against the lower-cased name set. Cheap (one fetch per
  // modal open) and immediate feedback before the user submits.
  const [existingNames, setExistingNames] = useState<Set<string> | null>(null)
  const [hasPersonalToken, setHasPersonalToken] = useState<boolean | undefined>(undefined)

  const [submitting, setSubmitting] = useState(false)
  // duplicateInfo populated either by the pre-flight check (5.1) or by the
  // server's 409 fallback (defence in depth).
  const [duplicateInfo, setDuplicateInfo] = useState<DuplicateInfo | null>(null)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [submitResult, setSubmitResult] = useState<AddAddonResponse | null>(null)

  // Reset state every time a new entry is loaded.
  useEffect(() => {
    if (!entry || !open) return
    setName(entry.name)
    setNamespace(entry.default_namespace)
    setSyncWave(String(entry.default_sync_wave ?? 0))
    setVersion('')
    setVersionTouched(false)
    setShowPrereleases(false)
    setVersionsResp(null)
    setVersionsError(null)
    setSubmitting(false)
    setDuplicateInfo(null)
    setSubmitError(null)
    setSubmitResult(null)
  }, [entry, open])

  // Pre-flight: load the user's existing catalog once per modal open so the
  // V121-5.1 duplicate inline message can render without a server round-trip
  // on every keystroke.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    api
      .getAddonCatalog()
      .then((resp) => {
        if (cancelled) return
        const names = new Set(
          resp.addons.map((a) => a.addon_name.trim().toLowerCase()),
        )
        setExistingNames(names)
      })
      .catch(() => {
        // Non-fatal — server-side 409 is the safety net. Skip the inline
        // pre-flight when the catalog can't be fetched.
        if (!cancelled) setExistingNames(new Set())
      })
    return () => {
      cancelled = true
    }
  }, [open])

  // Pre-flight: look up whether the user has a personal PAT so the inline
  // AttributionNudge (V121-5 prompt) can render proactively. Best effort.
  useEffect(() => {
    if (!open) return
    let cancelled = false
    api
      .getMe()
      .then((me) => {
        if (!cancelled) setHasPersonalToken(me.has_github_token)
      })
      .catch(() => {
        if (!cancelled) setHasPersonalToken(undefined)
      })
    return () => {
      cancelled = true
    }
  }, [open])

  // Fetch versions when the modal opens for a given entry.
  useEffect(() => {
    if (!entry || !open) return
    let cancelled = false
    setVersionsLoading(true)
    setVersionsError(null)
    api
      .listCuratedCatalogVersions(entry.name)
      .then((resp) => {
        if (cancelled) return
        setVersionsResp(resp)
        // Default to latest stable when present; fall back to first item.
        if (resp.latest_stable) {
          setVersion(resp.latest_stable)
        } else if (resp.versions[0]) {
          setVersion(resp.versions[0].version)
        }
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setVersionsError(
          e instanceof Error ? e.message : 'Failed to load chart versions',
        )
      })
      .finally(() => {
        if (!cancelled) setVersionsLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [entry, open])

  // Visible version list — top 5 stable by default; full list when prereleases enabled.
  const visibleVersions: CatalogVersionEntry[] = useMemo(() => {
    if (!versionsResp) return []
    const stable = versionsResp.versions.filter((v) => !v.prerelease)
    if (showPrereleases) return versionsResp.versions
    return stable.slice(0, 5)
  }, [versionsResp, showPrereleases])

  const versionInList = useMemo(
    () =>
      versionsResp?.versions.some((v) => v.version === version) ?? false,
    [versionsResp, version],
  )

  const versionInvalid =
    versionTouched && !!version && versionsResp !== null && !versionInList

  // V121-5.1: live duplicate detection. Lower-cased compare to match the
  // catalog reader (catalog names are stored lowercase by convention).
  const trimmedName = name.trim()
  const isDuplicate = useMemo(() => {
    if (!existingNames || trimmedName.length === 0) return false
    return existingNames.has(trimmedName.toLowerCase())
  }, [existingNames, trimmedName])

  const formValid =
    trimmedName.length > 0 &&
    namespace.trim().length > 0 &&
    !Number.isNaN(parseInt(syncWave, 10)) &&
    version.trim().length > 0 &&
    !versionInvalid &&
    !isDuplicate &&
    !submitting &&
    submitResult === null

  // Once we've successfully opened a PR, reading the result off either the
  // top-level fields or the wrapped `result` (when attribution_warning was set
  // by withAttributionWarning on the server). Same shape as ValuesEditor.
  const prURL = submitResult?.pr_url || submitResult?.result?.pr_url
  const prID = submitResult?.pr_id ?? submitResult?.result?.pr_id
  const merged = submitResult?.merged ?? submitResult?.result?.merged ?? false

  if (!entry) return null

  const handleSubmit = async () => {
    if (!formValid || !entry) return
    setSubmitting(true)
    setSubmitError(null)
    setDuplicateInfo(null)
    try {
      const res = await addAddon({
        name: trimmedName,
        chart: entry.chart,
        repo_url: entry.repo,
        version: version.trim(),
        namespace: namespace.trim(),
        sync_wave: parseInt(syncWave, 10),
        // V121-5.2 (LOCKED 2026-04-19, Moran): identifies the originating UI
        // flow so the existing `addon_added` audit event records source
        // without a new event name.
        source: 'marketplace',
      })
      setSubmitResult(res)
      const label = prID || res.pr_id || res.result?.pr_id ? `PR #${res.pr_id ?? res.result?.pr_id}` : 'PR'
      const wasMerged = res.merged ?? res.result?.merged ?? false
      const url = res.pr_url || res.result?.pr_url
      if (url) {
        if (wasMerged) {
          showToast(`${label} merged →`, 'success')
        } else {
          showToast(`${label} opened →`, 'success')
        }
      } else {
        showToast(`${entry.name} added`, 'success')
      }
    } catch (e) {
      if (isAddonAlreadyExistsError(e)) {
        // Server-side defence-in-depth tripped — render the same inline
        // duplicate message used for the pre-flight check.
        setDuplicateInfo({ addon: e.addon, existingUrl: e.existingUrl })
      } else {
        const msg = e instanceof Error ? e.message : 'Failed to open PR'
        setSubmitError(msg)
        showToast(`Failed to add addon — ${msg}`, 'info')
      }
    } finally {
      setSubmitting(false)
    }
  }

  // V121-5.3: when the modal closes after a successful submit, restore focus
  // to the Add Addon button (or whatever ref the parent passed).
  const handleOpenChange = (next: boolean) => {
    onOpenChange(next)
    if (!next && returnFocusRef?.current) {
      // setTimeout to wait for Radix to release focus.
      setTimeout(() => returnFocusRef.current?.focus(), 0)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        className="max-w-2xl"
        aria-describedby="marketplace-configure-desc"
      >
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 capitalize">
            Configure {entry.name}
            {entry.deprecated && (
              <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-amber-800 dark:bg-amber-900/40 dark:text-amber-300">
                Deprecated
              </span>
            )}
          </DialogTitle>
          <DialogDescription id="marketplace-configure-desc">
            Review and adjust the defaults from the curated catalog entry before
            opening a pull request.
          </DialogDescription>
        </DialogHeader>

        {/* Read-only summary */}
        <div className="rounded-md bg-[#f0f7ff] p-3 ring-1 ring-[#c0ddf0] dark:bg-gray-900 dark:ring-gray-700">
          <p className="text-sm text-[#0a3a5a] dark:text-gray-300">
            {entry.description}
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-[#2a5a7a] dark:text-gray-400">
            <span title={`License: ${entry.license}`}>{entry.license}</span>
            <span aria-hidden="true">·</span>
            <span className="capitalize">{entry.category}</span>
            <span aria-hidden="true">·</span>
            <ScorecardBadge
              score={entry.security_score}
              tier={entry.security_tier}
              updated={entry.security_score_updated}
            />
            {entry.docs_url && (
              <>
                <span aria-hidden="true">·</span>
                <a
                  href={entry.docs_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-0.5 underline focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0]"
                >
                  Docs <ExternalLink className="h-3 w-3" aria-hidden="true" />
                </a>
              </>
            )}
          </div>
          {entry.maintainers.length > 0 && (
            <p className="mt-1 truncate text-xs text-[#2a5a7a] dark:text-gray-400">
              Maintainers: {entry.maintainers.join(', ')}
            </p>
          )}
        </div>

        {/* Form */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Display name" htmlFor="cfg-name" required>
            <input
              id="cfg-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
            />
          </Field>
          <Field label="Namespace" htmlFor="cfg-ns" required>
            <input
              id="cfg-ns"
              type="text"
              value={namespace}
              onChange={(e) => setNamespace(e.target.value)}
              className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
            />
          </Field>
          <Field label="Sync wave" htmlFor="cfg-wave">
            <input
              id="cfg-wave"
              type="number"
              value={syncWave}
              onChange={(e) => setSyncWave(e.target.value)}
              className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
            />
          </Field>
          <Field label="Chart version" htmlFor="cfg-version" required>
            <div className="flex flex-col gap-1">
              <div className="flex items-stretch gap-1">
                <input
                  id="cfg-version"
                  type="text"
                  list="cfg-version-list"
                  value={version}
                  onChange={(e) => {
                    setVersion(e.target.value)
                    setVersionTouched(false)
                  }}
                  onBlur={() => setVersionTouched(true)}
                  aria-invalid={versionInvalid || undefined}
                  aria-describedby={
                    versionInvalid ? 'cfg-version-error' : undefined
                  }
                  placeholder={
                    versionsLoading ? 'Loading versions…' : 'e.g. 1.20.0'
                  }
                  className="w-full rounded-md border border-[#5a9dd0] bg-white px-3 py-2 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-900 dark:text-gray-100"
                />
                {versionsLoading && (
                  <Loader2
                    className="my-auto h-4 w-4 animate-spin text-[#3a6a8a]"
                    aria-label="Loading chart versions"
                  />
                )}
              </div>
              <datalist id="cfg-version-list">
                {visibleVersions.map((v) => (
                  <option
                    key={v.version}
                    value={v.version}
                    label={v.app_version ? `app ${v.app_version}` : undefined}
                  />
                ))}
              </datalist>
              <label className="mt-1 flex cursor-pointer items-center gap-2 text-xs text-[#2a5a7a] dark:text-gray-400">
                <input
                  type="checkbox"
                  checked={showPrereleases}
                  onChange={(e) => setShowPrereleases(e.target.checked)}
                  className="h-3.5 w-3.5 cursor-pointer accent-teal-600"
                />
                Show pre-releases
              </label>
              {versionsResp?.latest_stable && (
                <p className="text-xs text-[#3a6a8a] dark:text-gray-500">
                  Latest stable: <code>{versionsResp.latest_stable}</code>
                </p>
              )}
              {versionsError && (
                <p className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400">
                  <AlertCircle className="h-3 w-3" aria-hidden="true" />
                  {versionsError}
                </p>
              )}
              {versionInvalid && (
                <p
                  id="cfg-version-error"
                  className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400"
                >
                  <AlertCircle className="h-3 w-3" aria-hidden="true" />
                  Version not found in index.yaml
                </p>
              )}
            </div>
          </Field>
        </div>

        {/* V121-5.1 — duplicate inline message. Both pre-flight (set when the
            user types a name already in the catalog) and server-side 409
            land in the same banner so the user sees one consistent error. */}
        {(isDuplicate || duplicateInfo) && !submitResult && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-700 dark:bg-amber-950/40 dark:text-amber-200"
          >
            <Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <p>
              <span className="font-semibold">{duplicateInfo?.addon ?? trimmedName}</span>{' '}
              is already in the catalog.{' '}
              <Link
                to={duplicateInfo?.existingUrl ?? `/addons/${encodeURIComponent(trimmedName)}`}
                onClick={() => onOpenChange(false)}
                className="font-medium underline hover:no-underline"
              >
                Open its page
              </Link>{' '}
              to edit or enable it on a cluster, or change the Display name to
              register a different copy.
            </p>
          </div>
        )}

        {/* V121-5 — proactive attribution nudge inline near Submit when the
            user has no personal PAT yet (Tier 2 will fall back to the
            service token + Co-authored-by trailer). */}
        {hasPersonalToken === false && !submitResult && (
          <AttributionNudge inline />
        )}

        {/* V121-5 — reactive attribution nudge if the server told us it fell
            back to the service token. */}
        {submitResult?.attribution_warning === 'no_per_user_pat' && hasPersonalToken !== false && (
          <AttributionNudge inline />
        )}

        {/* V121-5.3 — success banner with PR link. Auto-merge handled: the
            language doesn't claim "opened for review" when the PR is already
            merged. */}
        {submitResult && prURL && (
          <div
            role="status"
            className="flex items-start gap-2 rounded-md border border-green-300 bg-green-50 p-3 text-sm text-green-900 dark:border-green-700 dark:bg-green-950/40 dark:text-green-200"
          >
            <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <div className="flex-1">
              <p className="font-medium">
                {merged
                  ? 'PR merged — addon added to your catalog'
                  : 'PR opened — your addon is on its way'}
              </p>
              <a
                href={prURL}
                target="_blank"
                rel="noopener noreferrer"
                className="mt-1 inline-flex items-center gap-1 text-xs font-medium underline hover:no-underline"
              >
                <GitPullRequest className="h-3 w-3" aria-hidden="true" />
                {prID ? `View PR #${prID} on GitHub` : 'View PR on GitHub'}
                <ExternalLink className="h-3 w-3" aria-hidden="true" />
              </a>
            </div>
          </div>
        )}

        {submitError && !submitResult && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-700 dark:bg-red-950/40 dark:text-red-200"
          >
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <p>{submitError}</p>
          </div>
        )}

        <DialogFooter>
          <button
            type="button"
            onClick={() => handleOpenChange(false)}
            className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            {submitResult ? 'Close' : 'Cancel'}
          </button>
          {!submitResult && (
            <button
              type="button"
              onClick={handleSubmit}
              disabled={!formValid}
              title={
                isDuplicate
                  ? `${trimmedName} is already in the catalog`
                  : !formValid
                    ? 'Fix the highlighted fields first'
                    : 'Open a PR adding this addon to your catalog'
              }
              className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
            >
              {submitting ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
                  Submitting…
                </>
              ) : (
                'Submit & open PR'
              )}
            </button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Field({
  label,
  htmlFor,
  required,
  children,
}: {
  label: string
  htmlFor: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <div>
      <label
        htmlFor={htmlFor}
        className="mb-1 block text-sm font-medium text-[#0a3a5a] dark:text-gray-300"
      >
        {label}
        {required && (
          <span className="text-red-500" aria-hidden="true">
            {' '}
            *
          </span>
        )}
      </label>
      {children}
    </div>
  )
}

export default MarketplaceConfigureModal
