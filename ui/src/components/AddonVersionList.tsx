import { useEffect, useMemo, useState } from 'react'
import { ChevronDown, ChevronUp, X } from 'lucide-react'
import type { VersionMatrixResponse } from '@/services/models'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { StatusBadge } from '@/components/StatusBadge'
import { isNewerVersion } from '@/lib/utils'

// AddonVersionList — S2 (scale-walk day 7). Replaces both VersionMatrixTable
// (a 50-column grid, "a big mess" on a real 50-cluster estate) and
// BehindCatalogList (the flat behind-only view that used to sit next to
// it). One addon-first list now serves every case that used to need two
// components: the plain "every addon" view, the "behind catalog" filter,
// and the "update available" (upstream-freshness) filter — same data
// source (GET /addons/version-matrix), one render path.
//
// The parent (AddonCatalog) owns the fetch — it already needs the matrix
// response early to fill in the "Behind catalog version" stat card, so
// this component takes data/loading/error as props instead of fetching a
// second time.

interface AddonVersionGroup {
  addonName: string
  catalogVersion: string
  totalCells: number
  behindCount: number
  hasUpgrade: boolean
  rows: { clusterName: string; deployedVersion: string; health: string; behind: boolean }[]
}

function buildGroups(data: VersionMatrixResponse | null): AddonVersionGroup[] {
  const rows = data?.addons ?? []
  return rows.map((row) => {
    const cellEntries = Object.entries(row.cells || {})
    const behindCount = cellEntries.filter(([, cell]) => cell?.drift_from_catalog).length
    const hasUpgrade =
      !!row.newest_available &&
      cellEntries.some(([, cell]) => cell?.version && isNewerVersion(cell.version, row.newest_available!))
    const clusterRows = cellEntries
      .map(([clusterName, cell]) => ({
        clusterName,
        deployedVersion: cell?.version || '—',
        health: cell?.health || 'Unknown',
        behind: !!cell?.drift_from_catalog,
      }))
      .sort((a, b) => a.clusterName.localeCompare(b.clusterName))
    return {
      addonName: row.addon_name,
      catalogVersion: row.catalog_version || '—',
      totalCells: cellEntries.length,
      behindCount,
      hasUpgrade,
      rows: clusterRows,
    }
  })
}

/** "42 on 1.5.2 · 8 behind" — the plain-words spread summary shown on the
 *  collapsed addon row. When every deployed cluster matches the catalog
 *  version, say so plainly instead of a zero count nobody needs to read. */
export function spreadSummary(group: AddonVersionGroup): string {
  if (group.totalCells === 0) return 'not deployed on any cluster'
  if (group.behindCount === 0) {
    return `all ${group.totalCells} on ${group.catalogVersion}`
  }
  const onCount = group.totalCells - group.behindCount
  return `${onCount} on ${group.catalogVersion} · ${group.behindCount} behind`
}

const PAGE_SIZE = 10
const CLUSTER_PAGE_SIZE = 10

function AddonVersionRow({ group }: { group: AddonVersionGroup }) {
  const [expanded, setExpanded] = useState(false)
  const [clusterVisibleCount, setClusterVisibleCount] = useState(CLUSTER_PAGE_SIZE)

  const visibleRows = group.rows.slice(0, clusterVisibleCount)
  const remainingRows = group.rows.length - visibleRows.length

  return (
    <div className="overflow-hidden rounded-xl ring-2 ring-[#6aade0] dark:ring-gray-700">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="flex w-full flex-wrap items-center justify-between gap-2 bg-[#e0f0ff] px-4 py-2.5 text-left hover:bg-[#d6eeff] dark:bg-gray-900 dark:hover:bg-gray-800"
      >
        <span className="flex items-center gap-1.5">
          {expanded ? (
            <ChevronUp className="h-4 w-4 shrink-0 text-[#3a6a8a] dark:text-gray-400" />
          ) : (
            <ChevronDown className="h-4 w-4 shrink-0 text-[#3a6a8a] dark:text-gray-400" />
          )}
          <span className="font-semibold text-[#0a2a4a] dark:text-gray-100">{group.addonName}</span>
        </span>
        <span className="flex items-center gap-3 text-xs">
          <span className="font-mono text-[#5a8aaa] dark:text-gray-500">
            catalog {group.catalogVersion}
          </span>
          <span
            className={
              group.behindCount > 0
                ? 'font-medium text-amber-700 dark:text-amber-400'
                : 'font-medium text-[#2a5a7a] dark:text-gray-400'
            }
          >
            {spreadSummary(group)}
          </span>
        </span>
      </button>

      {expanded && (
        <>
          <table className="w-full divide-y divide-[#d6eeff] text-left text-sm dark:divide-gray-800">
            <thead className="bg-[#f0f7ff] dark:bg-gray-800/60">
              <tr>
                <th className="px-4 py-1.5 font-medium text-[#3a6a8a] dark:text-gray-400">Cluster</th>
                <th className="px-4 py-1.5 font-medium text-[#3a6a8a] dark:text-gray-400">Deployed version</th>
                <th className="px-4 py-1.5 font-medium text-[#3a6a8a] dark:text-gray-400">Health</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-[#d6eeff] dark:divide-gray-800">
              {visibleRows.map((row) => (
                <tr key={row.clusterName} className="bg-[#f0f7ff] dark:bg-gray-800">
                  <td className="px-4 py-2 text-[#0a2a4a] dark:text-gray-200">{row.clusterName}</td>
                  <td className="px-4 py-2">
                    <span className="flex items-center gap-2">
                      <span className="font-mono text-xs text-[#0a2a4a] dark:text-gray-200">
                        {row.deployedVersion}
                      </span>
                      {row.behind && (
                        <span className="inline-flex items-center rounded-full bg-amber-100 px-2 py-0.5 text-xs font-semibold text-amber-800 ring-1 ring-amber-300 dark:bg-amber-900/30 dark:text-amber-300 dark:ring-amber-700">
                          Behind catalog
                        </span>
                      )}
                    </span>
                  </td>
                  <td className="px-4 py-2">
                    <StatusBadge status={row.health} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {remainingRows > 0 && (
            <div className="bg-[#f0f7ff] px-4 py-2 dark:bg-gray-800">
              <button
                type="button"
                onClick={() => setClusterVisibleCount((c) => c + CLUSTER_PAGE_SIZE)}
                className="rounded-md border border-[#5a9dd0] bg-white px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-900 dark:text-gray-300 dark:hover:bg-gray-700"
              >
                Show {Math.min(CLUSTER_PAGE_SIZE, remainingRows)} more
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}

interface ChipProps {
  active: boolean
  label: string
  testId: string
  onActivate: () => void
  onDismiss: () => void
}

function FilterChip({ active, label, testId, onActivate, onDismiss }: ChipProps) {
  if (active) {
    return (
      <span
        data-testid={testId}
        className="inline-flex items-center gap-1.5 rounded-full bg-amber-50 px-3 py-1 text-xs font-medium text-amber-700 ring-1 ring-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:ring-amber-800"
      >
        {label}
        <button
          type="button"
          onClick={onDismiss}
          aria-label={`Clear the ${label.toLowerCase()} filter`}
          className="rounded-full hover:text-amber-900 dark:hover:text-amber-200"
        >
          <X className="h-3 w-3" />
        </button>
      </span>
    )
  }
  return (
    <button
      type="button"
      data-testid={testId}
      onClick={onActivate}
      className="inline-flex items-center gap-1.5 rounded-full border border-[#a0d0f0] bg-[#f0f7ff] px-3 py-1 text-xs font-medium text-[#3a6a8a] hover:bg-[#d6eeff] dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700"
    >
      {label}
    </button>
  )
}

export interface AddonVersionListProps {
  data: VersionMatrixResponse | null
  loading: boolean
  error: string | null
  onRetry: () => void
  initialBehindCatalogOnly?: boolean
  initialOutdatedOnly?: boolean
  onClearBehindCatalogFilter?: () => void
  onClearOutdatedFilter?: () => void
}

export function AddonVersionList({
  data,
  loading,
  error,
  onRetry,
  initialBehindCatalogOnly = false,
  initialOutdatedOnly = false,
  onClearBehindCatalogFilter,
  onClearOutdatedFilter,
}: AddonVersionListProps) {
  const [behindCatalogOnly, setBehindCatalogOnly] = useState(initialBehindCatalogOnly)
  const [outdatedOnly, setOutdatedOnly] = useState(initialOutdatedOnly)
  // Deep links land with these already true (App.tsx's version-matrix
  // redirect + FleetStatusStrip's ?filter=behind-catalog link) — sync
  // whenever the parent's initial value changes, e.g. clicking the new
  // "Behind catalog version" stat card while already on this view.
  useEffect(() => setBehindCatalogOnly(initialBehindCatalogOnly), [initialBehindCatalogOnly])
  useEffect(() => setOutdatedOnly(initialOutdatedOnly), [initialOutdatedOnly])

  // Drift-first (walk day 7): addons with at least one cell behind the
  // catalog version sort to the top — same headline metric as the new
  // "Behind catalog version" stat card. Stable within each group (keeps
  // the server's original order among ties), same idiom the old
  // VersionMatrixTable used for its own hasUpgrade-first sort.
  const groups = useMemo(() => {
    const built = buildGroups(data)
    return built
      .map((group, index) => ({ group, index }))
      .sort((a, b) => {
        const aDrift = a.group.behindCount > 0
        const bDrift = b.group.behindCount > 0
        if (aDrift !== bDrift) return aDrift ? -1 : 1
        return a.index - b.index
      })
      .map((entry) => entry.group)
  }, [data])

  const filteredGroups = useMemo(() => {
    let result = groups
    if (behindCatalogOnly) result = result.filter((g) => g.behindCount > 0)
    if (outdatedOnly) result = result.filter((g) => g.hasUpgrade)
    return result
  }, [groups, behindCatalogOnly, outdatedOnly])

  const [visibleCount, setVisibleCount] = useState(PAGE_SIZE)
  useEffect(() => {
    setVisibleCount(PAGE_SIZE)
  }, [behindCatalogOnly, outdatedOnly, data])

  const visibleGroups = filteredGroups.slice(0, visibleCount)
  const remaining = filteredGroups.length - visibleGroups.length

  if (loading) return <LoadingState message="Loading addon versions..." />
  if (error) return <ErrorState message={error} onRetry={onRetry} />
  if (!data || groups.length === 0) {
    return (
      <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
        No addons to show yet — add one to the catalog first.
      </div>
    )
  }

  const toggleBehindCatalog = () => setBehindCatalogOnly(true)
  const dismissBehindCatalog = () => {
    setBehindCatalogOnly(false)
    onClearBehindCatalogFilter?.()
  }
  const toggleOutdated = () => setOutdatedOnly(true)
  const dismissOutdated = () => {
    setOutdatedOnly(false)
    onClearOutdatedFilter?.()
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <FilterChip
          active={behindCatalogOnly}
          label="Behind catalog"
          testId="version-list-behind-chip"
          onActivate={toggleBehindCatalog}
          onDismiss={dismissBehindCatalog}
        />
        <FilterChip
          active={outdatedOnly}
          label="Update available"
          testId="version-list-outdated-chip"
          onActivate={toggleOutdated}
          onDismiss={dismissOutdated}
        />
      </div>

      <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
        {filteredGroups.length} addon{filteredGroups.length === 1 ? '' : 's'} across {data.clusters.length} cluster
        {data.clusters.length === 1 ? '' : 's'}
      </p>

      {filteredGroups.length === 0 ? (
        <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
          {behindCatalogOnly && outdatedOnly
            ? 'No addons match the current filters.'
            : behindCatalogOnly
              ? "Nothing behind — every application is on its addon's catalog version."
              : 'Nothing outdated — every addon is on its newest known version.'}
        </div>
      ) : (
        <>
          <div className="space-y-3">
            {visibleGroups.map((group) => (
              <AddonVersionRow key={group.addonName} group={group} />
            ))}
          </div>
          {remaining > 0 && (
            <button
              type="button"
              onClick={() => setVisibleCount((c) => c + PAGE_SIZE)}
              className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
            >
              Show {Math.min(PAGE_SIZE, remaining)} more
            </button>
          )}
        </>
      )}
    </div>
  )
}
