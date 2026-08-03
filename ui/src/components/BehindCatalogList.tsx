import { useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { api } from '@/services/api'
import type { VersionMatrixResponse } from '@/services/models'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { StatusBadge } from '@/components/StatusBadge'

// BehindCatalogList — S6 (scale-walk). ?filter=behind-catalog used to
// render the full 50-column VersionMatrixTable filtered down by row — on a
// real estate that's still a wall of near-empty columns per addon (the
// maintainer's screenshot verdict: "a big mess", cut off the right edge of
// the screen). This is the replacement view for that same filter: a flat
// list, one row per behind CELL, grouped by addon so the catalog version
// only has to be stated once per addon instead of once per cluster column.
//
// Deliberately a separate small component rather than a mode inside
// VersionMatrixTable — the matrix itself is out of scope here (its own
// outdatedOnly filter still renders the real grid, untouched).

interface AddonBehindGroup {
  addonName: string
  catalogVersion: string
  rows: { clusterName: string; deployedVersion: string; health: string }[]
}

function buildBehindGroups(data: VersionMatrixResponse | null): AddonBehindGroup[] {
  const groups: AddonBehindGroup[] = []
  for (const row of data?.addons ?? []) {
    const rows: AddonBehindGroup['rows'] = []
    for (const [clusterName, cell] of Object.entries(row.cells || {})) {
      if (cell?.drift_from_catalog) {
        rows.push({ clusterName, deployedVersion: cell.version || '—', health: cell.health })
      }
    }
    if (rows.length > 0) {
      // Stable cluster order within a group — alphabetical reads better
      // than whatever order Object.entries happened to return.
      rows.sort((a, b) => a.clusterName.localeCompare(b.clusterName))
      groups.push({ addonName: row.addon_name, catalogVersion: row.catalog_version || '—', rows })
    }
  }
  groups.sort((a, b) => a.addonName.localeCompare(b.addonName))
  return groups
}

export function BehindCatalogList({ onClear }: { onClear?: () => void }) {
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

  if (loading) return <LoadingState message="Loading version matrix..." />
  if (error) return <ErrorState message={error} onRetry={load} />

  const groups = buildBehindGroups(data)
  const totalBehind = groups.reduce((sum, g) => sum + g.rows.length, 0)

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <span
          data-testid="matrix-behind-catalog-chip"
          className="inline-flex items-center gap-1.5 rounded-full bg-amber-50 px-3 py-1 text-xs font-medium text-amber-700 ring-1 ring-amber-200 dark:bg-amber-900/30 dark:text-amber-400 dark:ring-amber-800"
        >
          behind catalog version only
          <button
            type="button"
            onClick={onClear}
            aria-label="Clear the behind-catalog filter"
            className="rounded-full hover:text-amber-900 dark:hover:text-amber-200"
          >
            <X className="h-3 w-3" />
          </button>
        </span>
      </div>

      {groups.length === 0 ? (
        <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
          Nothing behind — every application is on its addon&rsquo;s catalog version.
        </div>
      ) : (
        <>
          <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
            {totalBehind} application{totalBehind === 1 ? '' : 's'} behind, across {groups.length} addon
            {groups.length === 1 ? '' : 's'}
          </p>
          <div className="space-y-4">
            {groups.map((group) => (
              <div
                key={group.addonName}
                className="overflow-hidden rounded-xl ring-2 ring-[#6aade0] dark:ring-gray-700"
              >
                <div className="flex flex-wrap items-baseline justify-between gap-2 bg-[#e0f0ff] px-4 py-2.5 dark:bg-gray-900">
                  <span className="font-semibold text-[#0a2a4a] dark:text-gray-100">{group.addonName}</span>
                  <span className="font-mono text-xs text-[#5a8aaa] dark:text-gray-500">
                    catalog {group.catalogVersion}
                  </span>
                </div>
                <table className="w-full divide-y divide-[#d6eeff] text-left text-sm dark:divide-gray-800">
                  <thead className="bg-[#f0f7ff] dark:bg-gray-800/60">
                    <tr>
                      <th className="px-4 py-1.5 font-medium text-[#3a6a8a] dark:text-gray-400">Cluster</th>
                      <th className="px-4 py-1.5 font-medium text-[#3a6a8a] dark:text-gray-400">Deployed version</th>
                      <th className="px-4 py-1.5 font-medium text-[#3a6a8a] dark:text-gray-400">Health</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-[#d6eeff] dark:divide-gray-800">
                    {group.rows.map((row) => (
                      <tr key={row.clusterName} className="bg-[#f0f7ff] dark:bg-gray-800">
                        <td className="px-4 py-2 text-[#0a2a4a] dark:text-gray-200">{row.clusterName}</td>
                        <td className="px-4 py-2 font-mono text-xs text-amber-600 dark:text-amber-400">
                          {row.deployedVersion}
                          <span className="mx-1.5 text-[#5a8aaa] dark:text-gray-500">&rarr;</span>
                          <span className="text-[#0a2a4a] dark:text-gray-200">{group.catalogVersion}</span>
                        </td>
                        <td className="px-4 py-2">
                          <StatusBadge status={row.health} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
