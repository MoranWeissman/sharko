// ManagedSecretsSection — "Managed secrets" on the System page. Visibility
// only: two searchable, filterable, sortable, paginated tables (connection
// secrets, addon values secrets) built from GET /api/v1/system/managed-secrets,
// plus a plain-English line about how often each engine runs and its last
// run/error. Nothing here changes anything except the two existing "run the
// engine now" actions the story allows reusing.

import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import { ArrowDown, ArrowUp, ArrowUpDown, KeyRound, RefreshCw, Search } from 'lucide-react'
import { getManagedSecrets, triggerSecretsReconcile } from '@/services/api'
import type { AddonValuesSecretRow, ConnectionSecretRow, ManagedSecretsResponse } from '@/services/models'
import { PaginationControls, PageSizeSelector, type PageSize } from '@/components/PaginationControls'
import { InfoHint } from '@/components/InfoHint'
import { RoleGuard } from '@/components/RoleGuard'
import { relativeTime } from '@/lib/time'

// ─────────────────────────────────────────────────────────────────────────────
// Small shared bits
// ─────────────────────────────────────────────────────────────────────────────

/** Honest "when" cell: plain "Unknown" instead of a blank or a fake "just now". */
function WhenCell({ iso }: { iso?: string }) {
  if (!iso) {
    return <span className="text-[#5a8aaa] dark:text-gray-500">Unknown</span>
  }
  return (
    <span title={iso} className="text-[#2a5a7a] dark:text-gray-300">
      {relativeTime(iso)}
    </span>
  )
}

const STATE_LABELS: Record<string, string> = {
  in_sync: 'In sync',
  out_of_sync: 'Out of sync',
  missing: 'Missing',
  unknown: 'Unknown',
}

const STATE_CLASSES: Record<string, string> = {
  in_sync: 'bg-green-50 text-green-700 dark:bg-green-900/30 dark:text-green-400',
  out_of_sync: 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-400',
  missing: 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-400',
  unknown: 'bg-gray-50 text-gray-700 dark:bg-gray-900/30 dark:text-gray-400',
}

function StateBadge({ state }: { state: string }) {
  const cls = STATE_CLASSES[state] ?? STATE_CLASSES.unknown
  const label = STATE_LABELS[state] ?? state
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${cls}`}>
      {label}
    </span>
  )
}

type SortDir = 'asc' | 'desc'

function SortableHeader({
  label,
  active,
  dir,
  onClick,
}: {
  label: string
  active: boolean
  dir: SortDir
  onClick: () => void
}) {
  const Icon = !active ? ArrowUpDown : dir === 'asc' ? ArrowUp : ArrowDown
  return (
    <th className="px-4 py-2.5">
      <button
        type="button"
        onClick={onClick}
        className="inline-flex items-center gap-1 font-semibold hover:text-[#0a2a4a] dark:hover:text-white"
      >
        {label}
        <Icon className="h-3 w-3" aria-hidden="true" />
      </button>
    </th>
  )
}

function EmptyRow({ colSpan, text }: { colSpan: number; text: string }) {
  return (
    <tr>
      <td colSpan={colSpan} className="px-4 py-6 text-center text-sm text-[#5a8aaa] dark:text-gray-500">
        {text}
      </td>
    </tr>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Connection secrets table
// ─────────────────────────────────────────────────────────────────────────────

type ConnSortKey = 'cluster' | 'state' | 'last_checked' | 'last_repaired'

function ConnectionSecretsTable({ rows }: { rows: ConnectionSecretRow[] }) {
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [stateFilter, setStateFilter] = useState('all')
  const [sortKey, setSortKey] = useState<ConnSortKey>('cluster')
  const [sortDir, setSortDir] = useState<SortDir>('asc')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<PageSize>(10)

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter((r) => {
      if (stateFilter !== 'all' && r.state !== stateFilter) return false
      if (!q) return true
      return (
        r.cluster.toLowerCase().includes(q) ||
        (r.secret_name ?? '').toLowerCase().includes(q) ||
        (r.secret_namespace ?? '').toLowerCase().includes(q)
      )
    })
  }, [rows, search, stateFilter])

  const sorted = useMemo(() => {
    const copy = [...filtered]
    copy.sort((a, b) => {
      const av = a[sortKey] ?? ''
      const bv = b[sortKey] ?? ''
      const cmp = av < bv ? -1 : av > bv ? 1 : 0
      return sortDir === 'asc' ? cmp : -cmp
    })
    return copy
  }, [filtered, sortKey, sortDir])

  useEffect(() => {
    setPage(1)
  }, [search, stateFilter, sortKey, sortDir, pageSize])

  const totalPages = Math.max(1, Math.ceil(sorted.length / pageSize))
  const clampedPage = Math.min(page, totalPages)
  const paged = useMemo(
    () => sorted.slice((clampedPage - 1) * pageSize, clampedPage * pageSize),
    [sorted, clampedPage, pageSize],
  )

  const toggleSort = (key: ConnSortKey) => {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 200, maxWidth: 320 }}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a] dark:text-gray-500" />
          <input
            type="text"
            placeholder="Search by cluster or secret name..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-[#5a9dd0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
          />
        </div>
        <div className="flex items-center gap-1.5">
          <label htmlFor="conn-secret-state-filter" className="text-sm text-[#2a5a7a] dark:text-gray-400">
            State
          </label>
          <select
            id="conn-secret-state-filter"
            value={stateFilter}
            onChange={(e) => setStateFilter(e.target.value)}
            className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-2 text-sm text-[#0a3a5a] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
          >
            <option value="all">All</option>
            <option value="in_sync">In sync</option>
            <option value="out_of_sync">Out of sync</option>
            <option value="missing">Missing</option>
            <option value="unknown">Unknown</option>
          </select>
        </div>
        <div className="ml-auto">
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[5, 10, 20, 50, 100]} />
        </div>
      </div>

      <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
            <tr>
              <SortableHeader label="Cluster" active={sortKey === 'cluster'} dir={sortDir} onClick={() => toggleSort('cluster')} />
              <th className="px-4 py-2.5">Secret</th>
              <SortableHeader label="State" active={sortKey === 'state'} dir={sortDir} onClick={() => toggleSort('state')} />
              <SortableHeader label="Last checked" active={sortKey === 'last_checked'} dir={sortDir} onClick={() => toggleSort('last_checked')} />
              <SortableHeader label="Last repaired" active={sortKey === 'last_repaired'} dir={sortDir} onClick={() => toggleSort('last_repaired')} />
            </tr>
          </thead>
          <tbody className="divide-y divide-[#d0e8f8] dark:divide-gray-700">
            {paged.length === 0 ? (
              <EmptyRow colSpan={5} text={rows.length === 0 ? 'No managed clusters yet.' : 'No secrets match this search.'} />
            ) : (
              paged.map((row) => (
                <tr
                  key={row.cluster}
                  onClick={() => navigate(`/clusters/${encodeURIComponent(row.cluster)}`)}
                  className="cursor-pointer hover:bg-[#e0f0ff] dark:hover:bg-gray-700"
                >
                  <td className="px-4 py-2.5 font-medium text-[#0a2a4a] dark:text-white">{row.cluster}</td>
                  <td className="px-4 py-2.5 text-[#2a5a7a] dark:text-gray-300">
                    {row.secret_namespace && row.secret_name ? `${row.secret_namespace}/${row.secret_name}` : 'Unknown'}
                  </td>
                  <td className="px-4 py-2.5">
                    <StateBadge state={row.state} />
                  </td>
                  <td className="px-4 py-2.5">
                    <WhenCell iso={row.last_checked} />
                  </td>
                  <td className="px-4 py-2.5">
                    {row.last_repaired ? (
                      <span title={row.last_repaired} className="text-[#2a5a7a] dark:text-gray-300">
                        {relativeTime(row.last_repaired)}
                        {row.last_repaired_detail ? ` — ${row.last_repaired_detail}` : ''}
                      </span>
                    ) : (
                      <span className="text-[#5a8aaa] dark:text-gray-500">Unknown</span>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="mt-3 flex items-center justify-between">
        <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
          {sorted.length} of {rows.length} shown
        </span>
        <PaginationControls page={clampedPage} totalPages={totalPages} onPageChange={setPage} />
      </div>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Addon values secrets table
// ─────────────────────────────────────────────────────────────────────────────

type AddonSortKey = 'cluster' | 'addon' | 'last_checked' | 'last_repaired'

function AddonValuesSecretsTable({ rows }: { rows: AddonValuesSecretRow[] }) {
  const navigate = useNavigate()
  const [search, setSearch] = useState('')
  const [sortKey, setSortKey] = useState<AddonSortKey>('cluster')
  const [sortDir, setSortDir] = useState<SortDir>('asc')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<PageSize>(10)

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return rows
    return rows.filter(
      (r) =>
        r.cluster.toLowerCase().includes(q) ||
        r.addon.toLowerCase().includes(q) ||
        (r.secret_name ?? '').toLowerCase().includes(q),
    )
  }, [rows, search])

  const sorted = useMemo(() => {
    const copy = [...filtered]
    copy.sort((a, b) => {
      const av = a[sortKey] ?? ''
      const bv = b[sortKey] ?? ''
      const cmp = av < bv ? -1 : av > bv ? 1 : 0
      return sortDir === 'asc' ? cmp : -cmp
    })
    return copy
  }, [filtered, sortKey, sortDir])

  useEffect(() => {
    setPage(1)
  }, [search, sortKey, sortDir, pageSize])

  const totalPages = Math.max(1, Math.ceil(sorted.length / pageSize))
  const clampedPage = Math.min(page, totalPages)
  const paged = useMemo(
    () => sorted.slice((clampedPage - 1) * pageSize, clampedPage * pageSize),
    [sorted, clampedPage, pageSize],
  )

  const toggleSort = (key: AddonSortKey) => {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 200, maxWidth: 320 }}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a] dark:text-gray-500" />
          <input
            type="text"
            placeholder="Search by cluster, addon, or secret name..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-[#5a9dd0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
          />
        </div>
        <div className="ml-auto">
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[5, 10, 20, 50, 100]} />
        </div>
      </div>

      <div className="overflow-x-auto rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm dark:ring-gray-700 dark:bg-gray-800">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-[#6aade0] bg-[#d0e8f8] text-xs uppercase text-[#2a5a7a] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400">
            <tr>
              <SortableHeader label="Cluster" active={sortKey === 'cluster'} dir={sortDir} onClick={() => toggleSort('cluster')} />
              <SortableHeader label="Addon" active={sortKey === 'addon'} dir={sortDir} onClick={() => toggleSort('addon')} />
              <th className="px-4 py-2.5">Secret</th>
              <SortableHeader label="Last checked" active={sortKey === 'last_checked'} dir={sortDir} onClick={() => toggleSort('last_checked')} />
              <SortableHeader label="Last repaired" active={sortKey === 'last_repaired'} dir={sortDir} onClick={() => toggleSort('last_repaired')} />
            </tr>
          </thead>
          <tbody className="divide-y divide-[#d0e8f8] dark:divide-gray-700">
            {paged.length === 0 ? (
              <EmptyRow
                colSpan={5}
                text={rows.length === 0 ? 'No addon values secrets registered yet.' : 'No secrets match this search.'}
              />
            ) : (
              paged.map((row) => (
                <tr
                  key={`${row.cluster}/${row.addon}`}
                  onClick={() => navigate(`/addons/${encodeURIComponent(row.addon)}`)}
                  className="cursor-pointer hover:bg-[#e0f0ff] dark:hover:bg-gray-700"
                >
                  <td className="px-4 py-2.5 font-medium text-[#0a2a4a] dark:text-white">{row.cluster}</td>
                  <td className="px-4 py-2.5 text-[#2a5a7a] dark:text-gray-300">{row.addon}</td>
                  <td className="px-4 py-2.5 text-[#2a5a7a] dark:text-gray-300">
                    {row.secret_namespace && row.secret_name ? `${row.secret_namespace}/${row.secret_name}` : 'Unknown'}
                  </td>
                  <td className="px-4 py-2.5">
                    <WhenCell iso={row.last_checked} />
                  </td>
                  <td className="px-4 py-2.5">
                    {row.last_repaired ? (
                      <span title={row.last_repaired} className="text-[#2a5a7a] dark:text-gray-300">
                        {relativeTime(row.last_repaired)}
                        {row.last_repaired_detail ? ` — ${row.last_repaired_detail}` : ''}
                      </span>
                    ) : (
                      <span className="text-[#5a8aaa] dark:text-gray-500">Unknown</span>
                    )}
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      <div className="mt-3 flex items-center justify-between">
        <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
          {sorted.length} of {rows.length} shown
        </span>
        <PaginationControls page={clampedPage} totalPages={totalPages} onPageChange={setPage} />
      </div>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine line
// ─────────────────────────────────────────────────────────────────────────────

function EngineLine({
  label,
  cadenceSentence,
  info,
  children,
}: {
  label: string
  cadenceSentence: string
  info?: { wired: boolean; last_run?: string; last_error?: string }
  children?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-[#2a5a7a] dark:text-gray-300">
      <span className="font-medium text-[#0a2a4a] dark:text-white">{label}:</span>
      <span>{cadenceSentence}</span>
      {info?.wired ? (
        <>
          <span className="text-[#5a8aaa] dark:text-gray-500">·</span>
          <span>
            Last run: <WhenCell iso={info.last_run} />
          </span>
          {info.last_error && (
            <span className="text-red-700 dark:text-red-400">· Last error: {info.last_error}</span>
          )}
        </>
      ) : (
        <span className="text-[#5a8aaa] dark:text-gray-500">· Not running on this server.</span>
      )}
      {children}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The section
// ─────────────────────────────────────────────────────────────────────────────

export function ManagedSecretsSection() {
  const [data, setData] = useState<ManagedSecretsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [reconciling, setReconciling] = useState(false)

  const load = () => {
    getManagedSecrets()
      .then((res) => setData(res))
      .catch(() => setData(null))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const handleReconcileNow = async () => {
    setReconciling(true)
    try {
      await triggerSecretsReconcile()
    } catch {
      // Best-effort — the button below simply re-fetches either way, and a
      // failed trigger just means the engine's last_run doesn't move.
    } finally {
      setReconciling(false)
      load()
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="h-6 w-6 animate-spin rounded-full border-4 border-[#6aade0] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
      </div>
    )
  }

  const connectionRows = data?.cluster_connection_secrets ?? []
  const addonRows = data?.addon_values_secrets ?? []

  return (
    <div className="space-y-6">
      <div className="space-y-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
        <EngineLine
          label="Cluster connection secrets"
          cadenceSentence="Re-checked every 30 seconds, and right after each merge."
          info={data?.engines.cluster_connection}
        />
        <EngineLine
          label="Addon values secrets"
          cadenceSentence="Checked every 5 minutes and repaired automatically."
          info={data?.engines.addon_values}
        >
          <RoleGuard roles={['admin', 'operator']}>
            <button
              type="button"
              onClick={handleReconcileNow}
              disabled={reconciling}
              className="ml-2 inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-2.5 py-1 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              <RefreshCw className={`h-3 w-3 ${reconciling ? 'animate-spin' : ''}`} />
              Refresh
            </button>
            <InfoHint
              text="Checks every addon values secret against its source right now, instead of waiting for the engine's regular 5-minute pass."
              label="What does Refresh do?"
            />
          </RoleGuard>
        </EngineLine>
      </div>

      <div>
        <h3 className="mb-3 flex items-center gap-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
          <KeyRound className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
          Cluster connection secrets
          <span className="rounded-full bg-teal-100 px-2 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
            {connectionRows.length}
          </span>
        </h3>
        <ConnectionSecretsTable rows={connectionRows} />
      </div>

      <div>
        <h3 className="mb-3 flex items-center gap-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
          <KeyRound className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
          Addon values secrets
          <span className="rounded-full bg-teal-100 px-2 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
            {addonRows.length}
          </span>
        </h3>
        <AddonValuesSecretsTable rows={addonRows} />
      </div>
    </div>
  )
}

export default ManagedSecretsSection
