import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api } from '@/services/api'
import type { VersionMatrixResponse } from '@/services/models'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'
import { StatusBadge } from '@/components/StatusBadge'

// VersionMatrixTable — Epic 7 Story 7.1 (v4 Wave 2). Fleet-wide grid: one
// row per addon, one column per cluster, showing the actual version
// deployed on that cluster, its health, and whether it has drifted from
// the catalog default. The two summary columns (Newest available, Last
// checked) come from the backend's catalog freshness scheduler snapshot —
// GET /addons/version-matrix already re-points to the v4 data model
// (clusters/*.yaml + catalog/addons.yaml) for a v4 repo, same endpoint,
// same response shape, no separate v3/v4 UI branch needed here.
export function VersionMatrixTable() {
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
  if (!data || data.addons.length === 0) {
    return (
      <div className="rounded-lg border border-teal-200 bg-teal-50 p-6 text-center text-sm text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
        No addons to show yet — add one to the catalog first.
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <p className="text-sm text-[#2a5a7a] dark:text-gray-400">
          {data.addons.length} addon{data.addons.length === 1 ? '' : 's'} across {data.clusters.length} cluster
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
            {data.addons.map((row) => (
              <tr key={row.addon_name} className="bg-[#f0f7ff] dark:bg-gray-800">
                <td className="sticky left-0 z-10 bg-[#f0f7ff] px-3 py-2 font-medium text-[#0a2a4a] dark:bg-gray-800 dark:text-gray-100">
                  <div>{row.addon_name}</div>
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
    </div>
  )
}
