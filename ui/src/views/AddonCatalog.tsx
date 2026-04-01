import { useState, useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Search,
  Filter,
  ArrowUpDown,
  ChevronDown,
  ChevronUp,
  Package,
  CheckCircle,
  XCircle,
  AlertTriangle,
  ExternalLink,
  LayoutGrid,
  LayoutList,
} from 'lucide-react'
import { api } from '@/services/api'
import type { AddonCatalogItem, AddonCatalogResponse } from '@/services/models'
import { StatCard } from '@/components/StatCard'
import { StatusBadge } from '@/components/StatusBadge'
import { LoadingState } from '@/components/LoadingState'
import { ErrorState } from '@/components/ErrorState'

type FilterType = 'all' | 'healthy' | 'unhealthy' | 'git-only'
type SortBy = 'name' | 'applications'
type PageSize = 15 | 30 | 60

function HealthProgressBar({ healthy, total }: { healthy: number; total: number }) {
  if (total === 0) return null
  const pct = (healthy / total) * 100
  const barColor =
    pct === 100 ? 'bg-green-500' : pct > 50 ? 'bg-yellow-500' : 'bg-red-500'

  return (
    <div>
      <div className="h-2 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-gray-700">
        <div
          className={`h-full rounded-full transition-all ${barColor}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="mt-1 text-xs text-gray-500 dark:text-gray-400">
        {healthy}/{total} healthy ({Math.round(pct)}%)
      </p>
    </div>
  )
}

function StatusChip({
  label,
  count,
  color,
}: {
  label: string
  count: number
  color: 'green' | 'red' | 'yellow'
}) {
  if (count === 0) return null
  const colors = {
    green: 'bg-green-50 text-green-700 border-green-200 dark:bg-green-900/30 dark:text-green-400 dark:border-green-700',
    red: 'bg-red-50 text-red-700 border-red-200 dark:bg-red-900/30 dark:text-red-400 dark:border-red-700',
    yellow: 'bg-yellow-50 text-yellow-700 border-yellow-200 dark:bg-yellow-900/30 dark:text-yellow-400 dark:border-yellow-700',
  }
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-medium ${colors[color]}`}
    >
      {count} {label}
    </span>
  )
}

function AddonCard({ addon }: { addon: AddonCatalogItem }) {
  const [expanded, setExpanded] = useState(false)
  const navigate = useNavigate()

  const enabledApps = addon.applications.filter((a) => a.enabled).length
  const namespace =
    addon.applications.find((a) => a.enabled && a.namespace)?.namespace ??
    addon.namespace ??
    addon.addon_name

  const handleCardClick = (e: React.MouseEvent) => {
    if ((e.target as HTMLElement).closest('button, a')) return
    navigate(`/addons/${addon.addon_name}`)
  }

  return (
    <div
      onClick={handleCardClick}
      className="group flex cursor-pointer flex-col rounded-lg border border-gray-200 bg-white shadow-sm transition-all hover:-translate-y-1 hover:border-cyan-400 hover:shadow-md dark:border-gray-700 dark:bg-gray-800 dark:hover:border-cyan-500"
    >
      <div className="flex flex-1 flex-col p-4">
        {/* Header */}
        <div className="mb-2 flex items-start justify-between">
          <div className="min-w-0 flex-1">
            <h3 className="truncate text-lg font-bold capitalize text-cyan-700 dark:text-cyan-400">
              {addon.addon_name}
            </h3>
            <p className="truncate text-xs text-gray-500 dark:text-gray-400">
              Namespace: {namespace}
            </p>
            {enabledApps > 0 ? (
              <p className="mt-1 text-sm font-semibold text-cyan-600 dark:text-cyan-400">
                {enabledApps} Active Applications
              </p>
            ) : (
              <p className="mt-1 text-sm font-semibold text-amber-600 dark:text-amber-400">
                Not Deployed
              </p>
            )}
          </div>
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation()
              setExpanded((v) => !v)
            }}
            className="ml-2 shrink-0 rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700 dark:hover:text-gray-300"
            title={expanded ? 'Collapse details' : 'Expand details'}
          >
            {expanded ? (
              <ChevronUp className="h-5 w-5" />
            ) : (
              <ChevronDown className="h-5 w-5" />
            )}
          </button>
        </div>

        {/* Stats */}
        <p className="mb-2 text-xs text-gray-500 dark:text-gray-400">
          {enabledApps > 0
            ? `Deployed on ${enabledApps} ${enabledApps === 1 ? 'cluster' : 'clusters'}`
            : 'Not deployed on any cluster'}
        </p>

        <HealthProgressBar healthy={addon.healthy_applications} total={enabledApps} />

        {/* Status chips */}
        <div className="mt-2 flex flex-wrap gap-1">
          <StatusChip label="Healthy" count={addon.healthy_applications} color="green" />
          <StatusChip label="Degraded" count={addon.degraded_applications} color="yellow" />
          <StatusChip label="Not Deployed" count={addon.missing_applications} color="red" />
        </div>

        {/* View Details button */}
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation()
            navigate(`/addons/${addon.addon_name}`)
          }}
          className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-md bg-cyan-600 px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-cyan-700 dark:bg-cyan-700 dark:hover:bg-cyan-600"
        >
          <ExternalLink className="h-3.5 w-3.5" />
          View Details
        </button>

        {/* Expanded section */}
        {expanded && (
          <div className="mt-3 border-t border-gray-200 pt-3 dark:border-gray-700">
            <h4 className="mb-2 text-xs font-semibold text-gray-700 dark:text-gray-300">
              Cluster Deployments
            </h4>
            <div className="max-h-60 overflow-auto rounded border text-xs dark:border-gray-700">
              <table className="w-full">
                <thead className="sticky top-0 bg-gray-50 dark:bg-gray-900">
                  <tr>
                    <th className="px-2 py-1 text-left font-medium text-gray-600 dark:text-gray-400">
                      Cluster
                    </th>
                    <th className="px-2 py-1 text-left font-medium text-gray-600 dark:text-gray-400">
                      Env
                    </th>
                    <th className="px-2 py-1 text-left font-medium text-gray-600 dark:text-gray-400">
                      Health
                    </th>
                    <th className="px-2 py-1 text-left font-medium text-gray-600 dark:text-gray-400">
                      Version
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-gray-100 dark:divide-gray-700">
                  {addon.applications
                    .filter((a) => a.enabled)
                    .map((app) => (
                      <tr key={app.cluster_name}>
                        <td className="px-2 py-1 font-medium dark:text-gray-200">
                          {app.cluster_name}
                        </td>
                        <td className="px-2 py-1 text-gray-500 dark:text-gray-400">
                          {app.cluster_environment ?? 'unknown'}
                        </td>
                        <td className="px-2 py-1">
                          <StatusBadge status={app.health_status ?? app.status} />
                        </td>
                        <td className="px-2 py-1 font-mono dark:text-gray-300">
                          {app.deployed_version ?? app.configured_version ?? 'N/A'}
                        </td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function PaginationControls({
  page,
  totalPages,
  onPageChange,
}: {
  page: number
  totalPages: number
  onPageChange: (p: number) => void
}) {
  if (totalPages <= 1) return null
  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        disabled={page <= 1}
        onClick={() => onPageChange(page - 1)}
        className="rounded border px-3 py-1 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
      >
        Prev
      </button>
      {Array.from({ length: totalPages }, (_, i) => i + 1)
        .filter(
          (p) =>
            p === 1 ||
            p === totalPages ||
            Math.abs(p - page) <= 1,
        )
        .reduce<(number | 'ellipsis')[]>((acc, p, idx, arr) => {
          if (idx > 0 && p - (arr[idx - 1] as number) > 1) {
            acc.push('ellipsis')
          }
          acc.push(p)
          return acc
        }, [])
        .map((item, idx) =>
          item === 'ellipsis' ? (
            <span key={`e-${idx}`} className="px-1 text-gray-400">
              ...
            </span>
          ) : (
            <button
              key={item}
              type="button"
              onClick={() => onPageChange(item)}
              className={`rounded px-3 py-1 text-sm font-medium transition-colors ${
                item === page
                  ? 'bg-cyan-600 text-white'
                  : 'text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-700'
              }`}
            >
              {item}
            </button>
          ),
        )}
      <button
        type="button"
        disabled={page >= totalPages}
        onClick={() => onPageChange(page + 1)}
        className="rounded border px-3 py-1 text-sm font-medium text-gray-700 transition-colors hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700"
      >
        Next
      </button>
    </div>
  )
}

function AddonListTable({ addons }: { addons: AddonCatalogItem[] }) {
  const navigate = useNavigate()
  return (
    <div className="overflow-x-auto rounded-xl border border-gray-200 bg-white shadow-sm dark:border-gray-700 dark:bg-gray-800">
      <table className="w-full text-left text-sm">
        <thead className="border-b border-gray-200 bg-gray-50 text-xs uppercase text-gray-500 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
          <tr>
            <th className="px-6 py-3">Addon Name</th>
            <th className="px-6 py-3">Version</th>
            <th className="px-6 py-3">Deployed</th>
            <th className="px-6 py-3">Healthy</th>
            <th className="px-6 py-3">Degraded</th>
            <th className="px-6 py-3">Not Deployed</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-200 dark:divide-gray-700">
          {addons.map((addon) => (
            <tr
              key={addon.addon_name}
              onClick={() => navigate(`/addons/${addon.addon_name}`)}
              className="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700"
            >
              <td className="px-6 py-3 font-medium capitalize text-gray-900 dark:text-gray-100">
                {addon.addon_name}
              </td>
              <td className="px-6 py-3 font-mono text-xs text-gray-500 dark:text-gray-400">
                {addon.version}
              </td>
              <td className="px-6 py-3 text-gray-700 dark:text-gray-300">
                {addon.enabled_clusters === 0 ? (
                  <span className="inline-flex items-center rounded-full bg-amber-50 px-2 py-0.5 text-xs font-medium text-amber-700 dark:bg-amber-900/30 dark:text-amber-400">
                    Not Deployed
                  </span>
                ) : (
                  <span className="inline-flex items-center rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                    {addon.enabled_clusters} {addon.enabled_clusters === 1 ? 'cluster' : 'clusters'}
                  </span>
                )}
              </td>
              <td className="px-6 py-3">
                {addon.healthy_applications > 0 && (
                  <span className="inline-flex items-center rounded-full bg-green-50 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                    {addon.healthy_applications}
                  </span>
                )}
              </td>
              <td className="px-6 py-3">
                {addon.degraded_applications > 0 && (
                  <span className="inline-flex items-center rounded-full bg-yellow-50 px-2 py-0.5 text-xs font-medium text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400">
                    {addon.degraded_applications}
                  </span>
                )}
              </td>
              <td className="px-6 py-3">
                {addon.missing_applications > 0 && (
                  <span className="inline-flex items-center rounded-full bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/30 dark:text-red-400">
                    {addon.missing_applications}
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function AddonCatalog() {
  const [catalogData, setCatalogData] = useState<AddonCatalogResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [viewMode, setViewMode] = useState<'grid' | 'list'>('list')
  const [search, setSearch] = useState('')
  const [filterType, setFilterType] = useState<FilterType>('all')
  const [sortBy, setSortBy] = useState<SortBy>('name')
  const [pageSize, setPageSize] = useState<PageSize>(15)
  const [page, setPage] = useState(1)

  useEffect(() => {
    api
      .getAddonCatalog()
      .then((data) => setCatalogData(data))
      .catch((e: unknown) =>
        setError(e instanceof Error ? e.message : 'Failed to load addon catalog'),
      )
      .finally(() => setLoading(false))
  }, [])

  // Reset page on filter/search/sort/pageSize change
  useEffect(() => {
    setPage(1)
  }, [search, filterType, sortBy, pageSize])

  const filteredAddons = useMemo(() => {
    if (!catalogData) return []

    let result = catalogData.addons

    // Search
    if (search) {
      const q = search.toLowerCase()
      result = result.filter(
        (a) =>
          a.addon_name.toLowerCase().includes(q) ||
          a.chart.toLowerCase().includes(q) ||
          a.namespace?.toLowerCase().includes(q),
      )
    }

    // Filter
    if (filterType !== 'all') {
      result = result.filter((a) => {
        switch (filterType) {
          case 'healthy':
            return (
              a.enabled_clusters > 0 &&
              a.degraded_applications === 0 &&
              a.missing_applications === 0
            )
          case 'unhealthy':
            return a.degraded_applications > 0 || a.missing_applications > 0
          case 'git-only':
            return a.enabled_clusters === 0
          default:
            return true
        }
      })
    }

    // Sort
    result = [...result].sort((a, b) => {
      if (sortBy === 'applications') return b.enabled_clusters - a.enabled_clusters
      return a.addon_name.localeCompare(b.addon_name)
    })

    return result
  }, [catalogData, search, filterType, sortBy])

  const totalPages = Math.ceil(filteredAddons.length / pageSize)
  const paginatedAddons = useMemo(() => {
    const start = (page - 1) * pageSize
    return filteredAddons.slice(start, start + pageSize)
  }, [filteredAddons, page, pageSize])

  const healthyCount = catalogData
    ? catalogData.addons.filter(
        (a) =>
          a.enabled_clusters > 0 &&
          a.degraded_applications === 0 &&
          a.missing_applications === 0,
      ).length
    : 0

  const unhealthyCount = catalogData
    ? catalogData.addons.filter(
        (a) => a.degraded_applications > 0 || a.missing_applications > 0,
      ).length
    : 0

  const handleStatFilter = (filter: FilterType) => {
    setFilterType(filterType === filter ? 'all' : filter)
  }

  if (loading) {
    return (
      <div className="space-y-4">
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Add-ons Catalog</h2>
        <LoadingState message="Loading addon catalog..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="space-y-4">
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Add-ons Catalog</h2>
        <ErrorState message={error} />
      </div>
    )
  }

  if (!catalogData) {
    return (
      <div className="space-y-4">
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Add-ons Catalog</h2>
        <p className="text-gray-500 dark:text-gray-400">No addon catalog data available.</p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h2 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Add-ons Catalog</h2>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          All add-ons defined in your Git catalog. See deployment coverage, health, and version per add-on. <span className="font-medium text-amber-600 dark:text-amber-400">Catalog Only</span> means the add-on is defined in your catalog but not yet enabled on any cluster.
        </p>
      </div>

      {/* Summary stat cards — click to filter */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title="All Addons"
          value={catalogData.total_addons}
          icon={<Package className="h-5 w-5" />}
          onClick={() => handleStatFilter('all')}
          selected={filterType === 'all'}
        />
        <StatCard
          title="Healthy"
          value={healthyCount}
          icon={<CheckCircle className="h-5 w-5" />}
          color="success"
          onClick={() => handleStatFilter('healthy')}
          selected={filterType === 'healthy'}
        />
        <StatCard
          title="Unhealthy"
          value={unhealthyCount}
          icon={<XCircle className="h-5 w-5" />}
          color="error"
          onClick={() => handleStatFilter('unhealthy')}
          selected={filterType === 'unhealthy'}
        />
        <StatCard
          title="Catalog Only"
          value={catalogData.addons_only_in_git}
          icon={<AlertTriangle className="h-5 w-5" />}
          color="warning"
          onClick={() => handleStatFilter('git-only')}
          selected={filterType === 'git-only'}
          subtitle="Defined in catalog, not deployed anywhere"
        />
      </div>

      {/* Search & filter controls */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 220, maxWidth: 350 }}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400" />
          <input
            type="text"
            placeholder="Search addons by name, chart, or namespace..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-gray-300 py-2 pl-10 pr-4 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-gray-500"
          />
        </div>

        <div className="flex items-center gap-1">
          <Filter className="h-4 w-4 text-gray-400" />
          <select
            value={filterType}
            onChange={(e) => setFilterType(e.target.value as FilterType)}
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
          >
            <option value="all">All Addons</option>
            <option value="healthy">Healthy Only</option>
            <option value="unhealthy">Not Healthy</option>
            <option value="git-only">Catalog Only (not deployed)</option>
          </select>
        </div>

        <div className="flex items-center gap-1">
          <ArrowUpDown className="h-4 w-4 text-gray-400" />
          <select
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as SortBy)}
            className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
          >
            <option value="name">A-Z</option>
            <option value="applications">Most Apps</option>
          </select>
        </div>

        <select
          value={pageSize}
          onChange={(e) => setPageSize(Number(e.target.value) as PageSize)}
          className="rounded-lg border border-gray-300 px-3 py-2 text-sm focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
        >
          <option value={15}>15 per page</option>
          <option value={30}>30 per page</option>
          <option value={60}>60 per page</option>
        </select>

        {/* View mode toggle */}
        <div className="ml-auto flex items-center rounded-lg border border-gray-300 dark:border-gray-600">
          <button
            type="button"
            onClick={() => setViewMode('grid')}
            className={`rounded-l-lg p-2 ${
              viewMode === 'grid'
                ? 'bg-cyan-600 text-white'
                : 'bg-white text-gray-500 hover:bg-gray-50 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
            }`}
            aria-label="Grid view"
            title="Grid view"
          >
            <LayoutGrid className="h-4 w-4" />
          </button>
          <button
            type="button"
            onClick={() => setViewMode('list')}
            className={`rounded-r-lg p-2 ${
              viewMode === 'list'
                ? 'bg-cyan-600 text-white'
                : 'bg-white text-gray-500 hover:bg-gray-50 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700'
            }`}
            aria-label="List view"
            title="List view"
          >
            <LayoutList className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Results count + top pagination */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-gray-500 dark:text-gray-400">
          {search
            ? `Showing ${filteredAddons.length} of ${catalogData.total_addons} addons`
            : `Showing ${filteredAddons.length} addons`}
          {totalPages > 1 && (
            <span>
              {' '}
              &middot; Page {page} of {totalPages} (
              {(page - 1) * pageSize + 1}-
              {Math.min(page * pageSize, filteredAddons.length)} of{' '}
              {filteredAddons.length})
            </span>
          )}
        </p>
        <PaginationControls
          page={page}
          totalPages={totalPages}
          onPageChange={setPage}
        />
      </div>

      {/* Addon grid / list */}
      {paginatedAddons.length === 0 ? (
        <div className="rounded-lg border border-cyan-200 bg-cyan-50 p-6 text-center text-sm text-cyan-700 dark:border-cyan-700 dark:bg-cyan-900/30 dark:text-cyan-400">
          {search
            ? `No addons found matching "${search}"`
            : 'No addons available in the catalog'}
        </div>
      ) : viewMode === 'grid' ? (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          {paginatedAddons.map((addon) => (
            <AddonCard key={addon.addon_name} addon={addon} />
          ))}
        </div>
      ) : (
        <AddonListTable addons={paginatedAddons} />
      )}

      {/* Bottom pagination */}
      {totalPages > 1 && (
        <div className="flex justify-center">
          <PaginationControls
            page={page}
            totalPages={totalPages}
            onPageChange={setPage}
          />
        </div>
      )}
    </div>
  )
}
