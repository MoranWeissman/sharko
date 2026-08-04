// ManagedSecrets — every secret Sharko manages, on its own page (S1: split
// off the System page, whose job is "how is Sharko itself set up and
// doing" — a resource list doesn't belong bolted onto that).
//
// S2: each row is rendered as the resource it is — a real Kubernetes name
// and namespace as the primary label, what it's for, which source it
// follows, its state, when it was last checked and last repaired — with a
// per-row Refresh / Sync / Diff toolbar (ArgoCD vocabulary only), the same
// pattern the ClusterDetail "Managed cluster secret" panel established for
// one cluster, generalized to every row here.
//
// S3 honesty locks:
//   (a) "Out of sync" always says against WHAT — connection secrets are
//       compared against git, addon-values secrets against the vault (git
//       only holds a pointer to it). The two kinds get their own section
//       with their own source line; never one generic phrase.
//   (b) A values-secret Diff panel NEVER renders secret content — only
//       matches/doesn't-match, its source, and the two timestamps already
//       on the row. Connection-secret Diff keeps the existing label-only
//       behavior (ClusterDetail's panel, no credentials).

import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  CheckCircle,
  Eye,
  KeyRound,
  Loader2,
  RefreshCw,
  RotateCcw,
  Search,
} from 'lucide-react'
import {
  api,
  getManagedSecrets,
  reconcileCluster,
  refreshAddonValuesSecret,
  resyncClusterLabels,
  syncAddonValuesSecret,
  triggerSecretsReconcile,
} from '@/services/api'
import type {
  AddonValuesSecretRow,
  ClusterComparisonResponse,
  ConnectionSecretRow,
  ManagedSecretsEngineInfo,
  ManagedSecretsResponse,
} from '@/services/models'
import { PaginationControls, PageSizeSelector, type PageSize } from '@/components/PaginationControls'
import { InfoHint } from '@/components/InfoHint'
import { RoleGuard } from '@/components/RoleGuard'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import { showToast } from '@/components/ToastNotification'
import { relativeTime } from '@/lib/time'

// ─────────────────────────────────────────────────────────────────────────────
// Shared small bits
// ─────────────────────────────────────────────────────────────────────────────

function WhenCell({ iso, suffix }: { iso?: string; suffix?: string }) {
  if (!iso) {
    return <span className="text-[#5a8aaa] dark:text-gray-500">Unknown</span>
  }
  return (
    <span title={iso} className="text-[#2a5a7a] dark:text-gray-300">
      {relativeTime(iso)}
      {suffix}
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
    <span className={`inline-flex shrink-0 items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${cls}`}>
      {label}
    </span>
  )
}

// Small toolbar button shared by both row kinds — ArgoCD vocabulary only
// (Refresh / Sync / Diff), greyed with a plain-words reason via InfoHint
// when it doesn't apply, matching the ClusterDetail secret panel's pattern.
function RowActionButton({
  onClick,
  disabled,
  loading,
  icon: Icon,
  label,
  reason,
  testId,
}: {
  onClick: () => void
  disabled?: boolean
  loading?: boolean
  icon: typeof RefreshCw
  label: string
  reason?: string
  testId?: string
}) {
  return (
    <span className="inline-flex items-center gap-1">
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation()
          onClick()
        }}
        disabled={disabled || loading}
        data-testid={testId}
        className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-2.5 py-1 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
      >
        {loading ? <Loader2 className="h-3 w-3 animate-spin" /> : <Icon className="h-3 w-3" />}
        {label}
      </button>
      {reason && <InfoHint text={reason} label={`Why is ${label} unavailable?`} />}
    </span>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Search / filter / sort / pagination — one generic list shell shared by
// both sections, so connection secrets and addon-values secrets behave
// identically (search, state filter, sort, pagination) with only the row
// rendering differing between them.
// ─────────────────────────────────────────────────────────────────────────────

interface SortOption<T> {
  key: string
  label: string
  get: (row: T) => string
}

function SecretListSection<T>({
  icon: Icon,
  title,
  rows,
  searchPlaceholder,
  matchesSearch,
  stateOf,
  sortOptions,
  defaultSortKey,
  renderRow,
  rowKey,
  emptyText,
}: {
  icon: typeof KeyRound
  title: string
  rows: T[]
  searchPlaceholder: string
  matchesSearch: (row: T, q: string) => boolean
  stateOf: (row: T) => string
  sortOptions: SortOption<T>[]
  defaultSortKey: string
  renderRow: (row: T) => ReactNode
  rowKey: (row: T) => string
  emptyText: string
}) {
  const [search, setSearch] = useState('')
  const [stateFilter, setStateFilter] = useState('all')
  const [sortKey, setSortKey] = useState(defaultSortKey)
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<PageSize>(10)

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return rows.filter((r) => {
      if (stateFilter !== 'all' && stateOf(r) !== stateFilter) return false
      if (!q) return true
      return matchesSearch(r, q)
    })
  }, [rows, search, stateFilter, matchesSearch, stateOf])

  const sorted = useMemo(() => {
    const getter = sortOptions.find((o) => o.key === sortKey)?.get
    if (!getter) return filtered
    const copy = [...filtered]
    copy.sort((a, b) => {
      const av = getter(a)
      const bv = getter(b)
      const cmp = av < bv ? -1 : av > bv ? 1 : 0
      return sortDir === 'asc' ? cmp : -cmp
    })
    return copy
  }, [filtered, sortKey, sortDir, sortOptions])

  useEffect(() => {
    setPage(1)
  }, [search, stateFilter, sortKey, sortDir, pageSize])

  const totalPages = Math.max(1, Math.ceil(sorted.length / pageSize))
  const clampedPage = Math.min(page, totalPages)
  const paged = useMemo(
    () => sorted.slice((clampedPage - 1) * pageSize, clampedPage * pageSize),
    [sorted, clampedPage, pageSize],
  )

  return (
    <section>
      <h2 className="mb-3 flex items-center gap-2 text-base font-semibold text-[#0a2a4a] dark:text-gray-100">
        <Icon className="h-4 w-4 text-[#3a6a8a] dark:text-gray-400" aria-hidden="true" />
        {title}
        <span className="rounded-full bg-teal-100 px-2 py-0.5 text-xs font-medium text-teal-700 dark:bg-teal-900/30 dark:text-teal-400">
          {rows.length}
        </span>
      </h2>

      <div className="mb-3 flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 200, maxWidth: 320 }}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#3a6a8a] dark:text-gray-500" />
          <input
            type="text"
            placeholder={searchPlaceholder}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-[#5a9dd0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-[#5a8aaa]"
          />
        </div>
        <div className="flex items-center gap-1.5">
          <span className="text-sm text-[#2a5a7a] dark:text-gray-400" aria-hidden="true">
            State
          </span>
          <select
            aria-label="State"
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
        <div className="flex items-center gap-1.5">
          <span className="text-sm text-[#2a5a7a] dark:text-gray-400" aria-hidden="true">
            Sort
          </span>
          <select
            aria-label="Sort"
            value={sortKey}
            onChange={(e) => setSortKey(e.target.value)}
            className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-2 text-sm text-[#0a3a5a] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200"
          >
            {sortOptions.map((o) => (
              <option key={o.key} value={o.key}>
                {o.label}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={() => setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))}
            className="rounded-md border border-[#5a9dd0] bg-[#f0f7ff] px-2 py-2 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700"
          >
            {sortDir === 'asc' ? 'A→Z' : 'Z→A'}
          </button>
        </div>
        <div className="ml-auto">
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[5, 10, 20, 50, 100]} />
        </div>
      </div>

      {paged.length === 0 ? (
        <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 text-center text-sm text-[#5a8aaa] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-500">
          {rows.length === 0 ? emptyText : 'No secrets match this search.'}
        </div>
      ) : (
        <div className="space-y-2">{paged.map((row) => <div key={rowKey(row)}>{renderRow(row)}</div>)}</div>
      )}

      <div className="mt-3 flex items-center justify-between">
        <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
          {sorted.length} of {rows.length} shown
        </span>
        <PaginationControls page={clampedPage} totalPages={totalPages} onPageChange={setPage} />
      </div>
    </section>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Connection-secret row — compared against git.
// ─────────────────────────────────────────────────────────────────────────────

function ConnectionRow({
  row,
  onRequestSync,
  onChanged,
}: {
  row: ConnectionSecretRow
  onRequestSync: (cluster: string) => void
  onChanged: () => void
}) {
  const navigate = useNavigate()
  const [diffOpen, setDiffOpen] = useState(false)
  const [diffLoading, setDiffLoading] = useState(false)
  const [diffError, setDiffError] = useState<string | null>(null)
  const [diffData, setDiffData] = useState<ClusterComparisonResponse | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  const handleDiffToggle = async () => {
    setDiffOpen((open) => !open)
    if (diffOpen || diffData || diffLoading) return
    setDiffLoading(true)
    setDiffError(null)
    try {
      const result = await api.getClusterComparison(row.cluster)
      setDiffData(result)
    } catch (err) {
      setDiffError(err instanceof Error ? err.message : 'Failed to load the diff')
    } finally {
      setDiffLoading(false)
    }
  }

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      await reconcileCluster(row.cluster)
      showToast(`Refresh triggered for cluster "${row.cluster}".`, 'success')
      onChanged()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to trigger refresh', 'error')
    } finally {
      setRefreshing(false)
    }
  }

  const drift = diffData?.cluster?.last_reconcile?.label_drift
  const added = drift?.added ?? []
  const removed = drift?.removed ?? []
  const changed = drift?.changed ?? []

  const syncReason =
    row.state === 'in_sync'
      ? 'Nothing to apply — this secret already matches git.'
      : row.state === 'missing'
        ? "This secret hasn't been created yet — there's nothing to sync onto."
        : row.state === 'unknown'
          ? 'Click Refresh first to check this secret.'
          : undefined
  const syncDisabled = row.state !== 'out_of_sync'

  return (
    <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <button
          type="button"
          onClick={() => navigate(`/clusters/${encodeURIComponent(row.cluster)}`)}
          data-testid={`connection-identity-${row.cluster}`}
          className="text-left"
        >
          <div className="font-mono text-sm text-[#5a8aaa] dark:text-gray-500">
            {row.secret_namespace && row.secret_name ? `${row.secret_namespace}/${row.secret_name}` : 'Unknown'}
          </div>
          <div className="text-sm text-[#0a2a4a] hover:underline dark:text-white">
            Connects <span className="font-medium">{row.cluster}</span> to ArgoCD
          </div>
        </button>
        <StateBadge state={row.state} />
      </div>

      <p className="mt-1 text-xs text-[#5a8aaa] dark:text-gray-500">Compared against git.</p>

      <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1.5 text-xs text-[#3a6a8a] dark:text-gray-400">
        <span>
          Last checked: <WhenCell iso={row.last_checked} />
        </span>
        <span>
          Last repaired: <WhenCell iso={row.last_repaired} suffix={row.last_repaired_detail ? ` — ${row.last_repaired_detail}` : ''} />
        </span>
      </div>

      <div className="mt-3 flex items-center gap-2">
        <RoleGuard roles={['admin', 'operator']}>
          <RowActionButton
            onClick={handleRefresh}
            loading={refreshing}
            icon={RefreshCw}
            label="Refresh"
            reason="Checks this cluster's ArgoCD secret against git right now, instead of waiting for the reconciler's regular check."
            testId={`connection-refresh-${row.cluster}`}
          />
          <RowActionButton
            onClick={() => onRequestSync(row.cluster)}
            disabled={syncDisabled}
            icon={RotateCcw}
            label="Sync"
            reason={syncReason ?? "Applies git's addon labels to this cluster's secret now."}
            testId={`connection-sync-${row.cluster}`}
          />
        </RoleGuard>
        <RowActionButton
          onClick={handleDiffToggle}
          loading={diffLoading}
          icon={Eye}
          label="Diff"
          testId={`connection-diff-${row.cluster}`}
        />
      </div>

      {diffOpen && (
        <div
          className="mt-3 rounded-md ring-2 ring-[#6aade0] bg-white p-3 dark:ring-gray-700 dark:bg-gray-900"
          data-testid={`connection-diff-panel-${row.cluster}`}
        >
          {diffError ? (
            <p className="text-sm text-red-600 dark:text-red-400">{diffError}</p>
          ) : !diffData ? (
            <p className="text-sm text-[#3a6a8a] dark:text-gray-400">Loading…</p>
          ) : added.length === 0 && removed.length === 0 && changed.length === 0 ? (
            <div className="flex items-center gap-2">
              <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" />
              <p className="text-sm font-medium text-green-700 dark:text-green-400">
                No differences — this secret's addon labels match git.
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              {added.length > 0 && (
                <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
                  Missing {added.length} addon label{added.length === 1 ? '' : 's'} that git expects:{' '}
                  <span className="font-mono text-xs text-[#5a8aaa] dark:text-gray-500">{added.join(', ')}</span>
                </p>
              )}
              {removed.length > 0 && (
                <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
                  Has {removed.length} addon label{removed.length === 1 ? '' : 's'} git doesn't expect:{' '}
                  <span className="font-mono text-xs text-[#5a8aaa] dark:text-gray-500">{removed.join(', ')}</span>
                </p>
              )}
              {changed.length > 0 && (
                <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
                  {changed.length} addon label{changed.length === 1 ? '' : 's'} {changed.length === 1 ? 'has' : 'have'} a
                  different value than git:{' '}
                  <span className="font-mono text-xs text-[#5a8aaa] dark:text-gray-500">{changed.join(', ')}</span>
                </p>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Addon-values-secret row — compared against the vault. Diff NEVER fetches
// or renders secret content (S3(b)) — it only restates the row's own
// state/timestamps in plain words.
// ─────────────────────────────────────────────────────────────────────────────

function ValuesRow({
  row,
  onRequestSync,
  onChanged,
}: {
  row: AddonValuesSecretRow
  onRequestSync: (cluster: string, addon: string) => void
  onChanged: () => void
}) {
  const navigate = useNavigate()
  const [diffOpen, setDiffOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      const result = await refreshAddonValuesSecret(row.cluster, row.addon)
      showToast(result.message, 'success')
      onChanged()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to check this secret', 'error')
    } finally {
      setRefreshing(false)
    }
  }

  const syncReason =
    row.state === 'in_sync'
      ? 'Nothing to push — this secret already matches its source.'
      : row.state === 'unknown'
        ? 'Click Refresh first to check this secret.'
        : undefined
  const syncDisabled = row.state === 'in_sync' || row.state === 'unknown'

  let diffBody: ReactNode
  if (row.state === 'unknown') {
    diffBody = (
      <p className="text-sm text-[#3a6a8a] dark:text-gray-400">
        Sharko hasn't checked this secret yet — click Refresh to check now.
      </p>
    )
  } else if (row.state === 'in_sync') {
    diffBody = (
      <div className="flex items-center gap-2">
        <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" />
        <p className="text-sm font-medium text-green-700 dark:text-green-400">Matches its source.</p>
      </div>
    )
  } else if (row.state === 'missing') {
    diffBody = (
      <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
        This secret does not exist yet on the cluster — click Sync to create it.
      </p>
    )
  } else {
    diffBody = (
      <p className="text-sm text-[#2a5a7a] dark:text-gray-300">
        Does not match its source right now — click Sync to push the current value.
      </p>
    )
  }

  return (
    <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <button
          type="button"
          onClick={() => navigate(`/addons/${encodeURIComponent(row.addon)}`)}
          data-testid={`values-identity-${row.cluster}-${row.addon}`}
          className="text-left"
        >
          <div className="font-mono text-sm text-[#5a8aaa] dark:text-gray-500">
            {row.secret_namespace && row.secret_name ? `${row.secret_namespace}/${row.secret_name}` : 'Unknown'}
          </div>
          <div className="text-sm text-[#0a2a4a] hover:underline dark:text-white">
            Carries values for addon <span className="font-medium">{row.addon}</span> on cluster{' '}
            <span className="font-medium">{row.cluster}</span>
          </div>
        </button>
        <StateBadge state={row.state} />
      </div>

      <p className="mt-1 text-xs text-[#5a8aaa] dark:text-gray-500">
        Compared against the vault — git only holds a pointer to it.
      </p>

      <div className="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1.5 text-xs text-[#3a6a8a] dark:text-gray-400">
        <span>
          Last checked: <WhenCell iso={row.last_checked} />
        </span>
        <span>
          Last repaired: <WhenCell iso={row.last_repaired} suffix={row.last_repaired_detail ? ` — ${row.last_repaired_detail}` : ''} />
        </span>
      </div>

      <div className="mt-3 flex items-center gap-2">
        <RoleGuard roles={['admin', 'operator']}>
          <RowActionButton
            onClick={handleRefresh}
            loading={refreshing}
            icon={RefreshCw}
            label="Refresh"
            reason="Checks this secret against its source (the vault) right now, instead of waiting for the engine's regular pass."
            testId={`values-refresh-${row.cluster}-${row.addon}`}
          />
          <RowActionButton
            onClick={() => onRequestSync(row.cluster, row.addon)}
            disabled={syncDisabled}
            icon={RotateCcw}
            label="Sync"
            reason={syncReason ?? 'Pushes the current value from the vault to this cluster.'}
            testId={`values-sync-${row.cluster}-${row.addon}`}
          />
        </RoleGuard>
        <RowActionButton
          onClick={() => setDiffOpen((open) => !open)}
          icon={Eye}
          label="Diff"
          testId={`values-diff-${row.cluster}-${row.addon}`}
        />
      </div>

      {diffOpen && (
        <div
          className="mt-3 rounded-md ring-2 ring-[#6aade0] bg-white p-3 dark:ring-gray-700 dark:bg-gray-900"
          data-testid={`values-diff-panel-${row.cluster}-${row.addon}`}
        >
          {diffBody}
        </div>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine line — cadence + last run/error for one engine.
// ─────────────────────────────────────────────────────────────────────────────

function EngineLine({
  label,
  cadenceSentence,
  info,
  children,
}: {
  label: string
  cadenceSentence: string
  info?: ManagedSecretsEngineInfo
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
// The page
// ─────────────────────────────────────────────────────────────────────────────

type SyncTarget = { kind: 'connection'; cluster: string } | { kind: 'values'; cluster: string; addon: string }

export function ManagedSecrets() {
  const [data, setData] = useState<ManagedSecretsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [reconciling, setReconciling] = useState(false)
  const [syncTarget, setSyncTarget] = useState<SyncTarget | null>(null)
  const [syncing, setSyncing] = useState(false)

  const load = useCallback(() => {
    return getManagedSecrets()
      .then((res) => setData(res))
      .catch(() => setData(null))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const handleReconcileNow = async () => {
    setReconciling(true)
    try {
      await triggerSecretsReconcile()
    } catch {
      // Best-effort — the page re-fetches either way; a failed trigger just
      // means the engine's last_run doesn't move.
    } finally {
      setReconciling(false)
      load()
    }
  }

  const handleConfirmSync = async () => {
    if (!syncTarget) return
    setSyncing(true)
    try {
      if (syncTarget.kind === 'connection') {
        const result = await resyncClusterLabels(syncTarget.cluster)
        showToast(result.message, 'success')
      } else {
        const result = await syncAddonValuesSecret(syncTarget.cluster, syncTarget.addon)
        showToast(result.message, 'success')
      }
      setSyncTarget(null)
      load()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Sync failed', 'error')
    } finally {
      setSyncing(false)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-24">
        <div className="h-8 w-8 animate-spin rounded-full border-4 border-[#6aade0] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
      </div>
    )
  }

  const connectionRows = data?.cluster_connection_secrets ?? []
  const addonRows = data?.addon_values_secrets ?? []

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-white">Managed Secrets</h1>
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
          Every secret Sharko manages, shown as the real Kubernetes resource it is — with Refresh, Sync, and Diff
          on each one.
        </p>
      </div>

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
              Refresh all
            </button>
            <InfoHint
              text="Checks every addon values secret against its source right now, instead of waiting for the engine's regular 5-minute pass."
              label="What does Refresh all do?"
            />
          </RoleGuard>
        </EngineLine>
      </div>

      <SecretListSection<ConnectionSecretRow>
        icon={KeyRound}
        title="Cluster connection secrets"
        rows={connectionRows}
        searchPlaceholder="Search by cluster or secret name..."
        matchesSearch={(r, q) =>
          r.cluster.toLowerCase().includes(q) ||
          (r.secret_name ?? '').toLowerCase().includes(q) ||
          (r.secret_namespace ?? '').toLowerCase().includes(q)
        }
        stateOf={(r) => r.state}
        sortOptions={[
          { key: 'cluster', label: 'Cluster', get: (r) => r.cluster },
          { key: 'state', label: 'State', get: (r) => r.state },
          { key: 'last_checked', label: 'Last checked', get: (r) => r.last_checked ?? '' },
          { key: 'last_repaired', label: 'Last repaired', get: (r) => r.last_repaired ?? '' },
        ]}
        defaultSortKey="cluster"
        rowKey={(r) => r.cluster}
        emptyText="No managed clusters yet."
        renderRow={(r) => (
          <ConnectionRow row={r} onRequestSync={(cluster) => setSyncTarget({ kind: 'connection', cluster })} onChanged={load} />
        )}
      />

      <SecretListSection<AddonValuesSecretRow>
        icon={KeyRound}
        title="Addon values secrets"
        rows={addonRows}
        searchPlaceholder="Search by cluster, addon, or secret name..."
        matchesSearch={(r, q) =>
          r.cluster.toLowerCase().includes(q) ||
          r.addon.toLowerCase().includes(q) ||
          (r.secret_name ?? '').toLowerCase().includes(q)
        }
        stateOf={(r) => r.state}
        sortOptions={[
          { key: 'cluster', label: 'Cluster', get: (r) => r.cluster },
          { key: 'addon', label: 'Addon', get: (r) => r.addon },
          { key: 'state', label: 'State', get: (r) => r.state },
          { key: 'last_checked', label: 'Last checked', get: (r) => r.last_checked ?? '' },
          { key: 'last_repaired', label: 'Last repaired', get: (r) => r.last_repaired ?? '' },
        ]}
        defaultSortKey="cluster"
        rowKey={(r) => `${r.cluster}/${r.addon}`}
        emptyText="No addon values secrets registered yet."
        renderRow={(r) => (
          <ValuesRow
            row={r}
            onRequestSync={(cluster, addon) => setSyncTarget({ kind: 'values', cluster, addon })}
            onChanged={load}
          />
        )}
      />

      <ConfirmationModal
        open={syncTarget !== null}
        onClose={() => setSyncTarget(null)}
        onConfirm={handleConfirmSync}
        title={
          syncTarget?.kind === 'connection'
            ? `Sync cluster "${syncTarget.cluster}"?`
            : syncTarget?.kind === 'values'
              ? `Sync secret for cluster "${syncTarget.cluster}", addon "${syncTarget.addon}"?`
              : 'Sync?'
        }
        description={
          syncTarget?.kind === 'connection'
            ? "Applies git's addon labels to this cluster's ArgoCD secret — one time; the self-heal setting is not changed."
            : 'Pushes the current value from the vault to this cluster — creates the secret if missing, replaces it if the content differs.'
        }
        confirmText="Sync"
        loading={syncing}
      />
    </div>
  )
}

export default ManagedSecrets
