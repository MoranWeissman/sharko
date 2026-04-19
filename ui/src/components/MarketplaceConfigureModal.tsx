import { useEffect, useMemo, useState } from 'react'
import { Loader2, Info, ExternalLink, AlertCircle } from 'lucide-react'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScorecardBadge } from '@/components/ScorecardBadge'
import { api } from '@/services/api'
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
 * Submit lands in V121-5 (the actual orchestrator-backed Add-PR flow). For
 * this epic we wire the click but show a placeholder dialog explaining the
 * deferred work — this lets the rest of the modal (validation, version
 * picker, A11y) be reviewed and shipped without the orchestration tail.
 */

export interface MarketplaceConfigureModalProps {
  entry: CatalogEntry | null
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function MarketplaceConfigureModal({
  entry,
  open,
  onOpenChange,
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

  const [submitNotice, setSubmitNotice] = useState<string | null>(null)

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
    setSubmitNotice(null)
  }, [entry, open])

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

  const formValid =
    name.trim().length > 0 &&
    namespace.trim().length > 0 &&
    !Number.isNaN(parseInt(syncWave, 10)) &&
    version.trim().length > 0 &&
    !versionInvalid

  if (!entry) return null

  const handleSubmit = () => {
    if (!formValid) return
    // V121-5 will replace this with a real call to addAddon() that uses the
    // curated entry's `chart`/`repo` fields. Emitting a placeholder here so
    // the wiring is clear and reviewers see the contract.
    setSubmitNotice(
      `V121-5 will wire Submit to the Add-PR orchestrator. ` +
        `Submitted payload preview: name=${name.trim()}, namespace=${namespace.trim()}, ` +
        `sync_wave=${parseInt(syncWave, 10)}, version=${version.trim()}, ` +
        `chart=${entry.chart}, repo=${entry.repo}.`,
    )
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
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

        {submitNotice && (
          <div
            role="status"
            className="flex items-start gap-2 rounded-md bg-amber-50 p-3 text-sm text-amber-900 ring-1 ring-amber-200 dark:bg-amber-900/30 dark:text-amber-200 dark:ring-amber-700"
          >
            <Info className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <p>{submitNotice}</p>
          </div>
        )}

        <DialogFooter>
          <button
            type="button"
            onClick={() => onOpenChange(false)}
            className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-4 py-2 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] focus:outline-none focus-visible:ring-2 focus-visible:ring-[#6aade0] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            disabled={!formValid}
            className="inline-flex items-center gap-2 rounded-md bg-teal-600 px-4 py-2 text-sm font-medium text-white hover:bg-teal-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-teal-500 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-teal-700 dark:hover:bg-teal-600"
          >
            Submit (preview)
          </button>
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
