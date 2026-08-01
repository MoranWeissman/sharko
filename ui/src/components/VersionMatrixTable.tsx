import { useEffect, useMemo, useState } from 'react'
import { RefreshCw, ArrowUpCircle, X } from 'lucide-react'
import { api } from '@/services/api'
import type { VersionMatrixResponse } from '@/services/models'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { StatusBadge } from '@/components/StatusBadge'
import { isNewerVersion } from '@/lib/utils'

// VersionMatrixTable — Epic 7 Story 7.1 (v4 Wave 2). Fleet-wide grid: one
// row per addon, one column per cluster, showing the actual version
// deployed on that cluster, its health, and whether it has drifted from
// the catalog default. The two summary columns (Newest available, Last
// checked) come from the backend's catalog freshness scheduler snapshot —
// GET /addons/version-matrix already re-points to the v4 data model
// (cluster-addons/*.yaml + catalog/addons.yaml) for a v4 repo, same endpoint,
// same response shape, no separate v3/v4 UI branch needed here.
//
// outdatedOnly (WQ-2) — the Fleet Status Strip's Upgrades segment is now a
// bare clickable number that deep-links here with
// ?view=matrix&filter=outdated (AddonCatalog reads the param and passes
// this down). When true, only rows with at least one cell behind the row's
// newest_available are shown — the same isNewerVersion check that already
// sorts outdated rows first. A dismissible chip above the table clears it.
interface VersionMatrixTableProps {
  outdatedOnly?: boolean
  onClearOutdatedFilter?: () => void
}

export function VersionMatrixTable({ outdatedOnly = false, onClearOutdatedFilter }: VersionMatrixTableProps) {
  const [data, setData] = useState<VersionMatrixResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = () => {
    setLoading(true)
    setError(null)
    api
      .getVersionMatrix()
      .then(setData)
      .catch((err) => setError(err instanceof Error ? err.message : 'Could not load the version matrix'))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    load()
  }, [])

  // Sort-first-what's-outdated (dashboard UX review 2026-08-01, walk
  // finding #1): a row "has an upgrade" if any deployed cell is behind the
  // row's newest_available. Rows with an upgrade sort to the top; ties keep
  // the server's original order (stable sort). Same isNewerVersion the
  // Dashboard's Upgrades stat uses, so "outdated" means the same thing in
  // both places.
  const sortedAddons = useMemo(() => {
    const rows = data?.addons ?? []
    const hasUpgrade = (row: VersionMatrixResponse['addons'][number]) => {
      if (!row.newest_available) return false
      return Object.values(row.cells || {}).some(
        (cell) => cell?.version && isNewerVersion(cell.version, row.newest_available!),
      )
    }
    return rows
      .map((row, index) => ({ row, index, hasUpgrade: hasUpgrade(row) }))
      .sort((a, b) => {
        if (a.hasUpgrade !== b.hasUpgrade) return a.hasUpgrade ? -1 : 1
        return a.index - b.index
      })
      .map((entry) => entry)
  }, [data])

  // outdatedOnly (WQ-2) — narrow to rows that already carry the
  // "newer version available" marker. Sort order (outdated-first) is
  // untouched either way.
  const displayedAddons = useMemo(
    () => (outdatedOnly ? sortedAddons.filter((entry) => entry.hasUpgrade) : sortedAddons),
    [sortedAddons, outdatedOnly],
  )

  if (loading) return <LoadingState message="Loading version matrix..." />
  if (error) return <ErrorState message={error} onRetry={load} />
  if (!data || data.addons.length === 0) {
    return (
      <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
        No addons to show yet — add one to the catalog first.
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {outdatedOnly && (
        <div className="flex items-center gap-2">
          <span
            data-testid="matrix-outdated-chip"
            className="inline-flex items-center gap-1.5 rounded-full bg-amber-50 px-3 py-1 text-xs font-medium text-amber-700 ring-1 ring-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:ring-amber-800"
          >
            outdated only
            <button
              type="button"
              onClick={onClearOutdatedFilter}
              aria-label="Clear the outdated filter"
              className="rounded-full hover:text-amber-900 dark:hover:text-amber-200"
            >
              <X className="h-3 w-3" />
            </button>
          </span>
        </div>
      )}

      <div className="flex items-center justify-between">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
          {displayedAddons.length} addon{displayedAddons.length === 1 ? '' : 's'} across {data.clusters.length} cluster
          {data.clusters.length === 1 ? '' : 's'}
        </p>
        <button
          type="button"
          onClick={load}
          className="inline-flex items-center gap-1.5 rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2.5 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
        >
          <RefreshCw className="h-3.5 w-3.5" />
          Refresh
        </button>
      </div>

      {displayedAddons.length === 0 ? (
        <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
          Nothing outdated — every addon is on its newest known version.
        </div>
      ) : (
      <>
      <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] dark:ring-gray-700">
        <table className="min-w-full divide-y divide-[#c0ddf0] dark:divide-gray-700 text-sm">
          <thead className="bg-[#e0f0ff] dark:bg-gray-900">
            <tr>
              <th className="sticky left-0 z-10 bg-[#e0f0ff] px-3 py-2 text-left font-semibold text-[#0a2a4a] dark:bg-gray-900 dark:text-gray-100">
                Addon
              </th>
              {data.clusters.map((cluster) => (
                <th
                  key={cluster}
                  className="px-3 py-2 text-left font-semibold whitespace-nowrap text-[#0a2a4a] dark:text-gray-100"
                >
                  {cluster}
                </th>
              ))}
              <th className="px-3 py-2 text-left font-semibold whitespace-nowrap text-[#0a2a4a] dark:text-gray-100">
                Newest available
              </th>
              <th className="px-3 py-2 text-left font-semibold whitespace-nowrap text-[#0a2a4a] dark:text-gray-100">
                Last checked
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-[#d6eeff] dark:divide-gray-800">
            {displayedAddons.map(({ row, hasUpgrade }) => (
              <tr key={row.addon_name} className="bg-[#f0f7ff] dark:bg-gray-800">
                <td className="sticky left-0 z-10 bg-[#f0f7ff] px-3 py-2 font-medium text-[#0a2a4a] dark:bg-gray-800 dark:text-gray-100">
                  <div className="flex items-center gap-1.5">
                    <span>{row.addon_name}</span>
                    {hasUpgrade && (
                      <span title="A newer version is available">
                        <ArrowUpCircle
                          className="h-3.5 w-3.5 shrink-0 text-amber-600 dark:text-amber-400"
                          aria-label="A newer version is available"
                        />
                      </span>
                    )}
                  </div>
                  <div className="font-mono text-xs text-[#5a8aaa] dark:text-gray-500">
                    catalog {row.catalog_version || '—'}
                  </div>
                </td>
                {data.clusters.map((cluster) => {
                  const cell = row.cells[cluster]
                  if (!cell) {
                    return (
                      <td key={cluster} className="px-3 py-2 text-[#5a8aaa] dark:text-gray-600">
                        &mdash;
                      </td>
                    )
                  }
                  return (
                    <td key={cluster} className="px-3 py-2 align-top">
                      <div className="flex flex-col gap-1">
                        <span
                          className={`font-mono text-xs ${
                            cell.drift_from_catalog
                              ? 'font-semibold text-amber-600 dark:text-amber-400'
                              : 'text-[#0a2a4a] dark:text-gray-200'
                          }`}
                          title={cell.drift_from_catalog ? 'Differs from the catalog default version' : undefined}
                        >
                          {cell.version || '—'}
                          {cell.drift_from_catalog && ' *'}
                        </span>
                        <StatusBadge status={cell.health} />
                      </div>
                    </td>
                  )
                })}
                <td className="px-3 py-2 font-mono text-xs text-[#0a2a4a] dark:text-gray-200">
                  {row.newest_available || '—'}
                </td>
                <td className="px-3 py-2 text-xs text-[#5a8aaa] dark:text-gray-400">
                  {row.last_checked ? new Date(row.last_checked).toLocaleString() : 'never'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-[#5a8aaa] dark:text-gray-500">* differs from the catalog default version</p>
      </>
      )}
    </div>
  )
}
