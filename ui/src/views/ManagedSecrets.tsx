// ManagedSecrets — the /secrets page, rebuilt as ONE dense resource list
// (maintainer's design call, "chaos in our eyes" vs. ArgoCD's Application
// Details view). Every secret Sharko manages — connection secrets AND
// addon-values secrets — is now one row in one table: a small kind glyph,
// dim mono identity, a COMPARED AGAINST column, cluster, addon, a time
// chip, a status mark, and a single row menu. Click a row to open the
// detail side panel; the row itself never grows.
//
// What moved where, and why:
//
//   - S1: the shared house pieces this page introduces — StatusMark
//     (@/components/resource/StatusMark), TimeChip
//     (@/components/resource/TimeChip), ResourceDetailSheet
//     (@/components/resource/ResourceDetailSheet) — plus the existing
//     kebab menu (@/components/RowActionsMenu), extended here with
//     disabled/reason/loading support. Later lanes (the cluster page's
//     managed-secret panel, the addon rows) are meant to adopt these same
//     four pieces rather than growing their own.
//
//   - S3 HONESTY LOCK, carried forward from the old two-section layout:
//     each row's "source" — what it's compared against — used to live in
//     a section header. Merging the sections would have destroyed that,
//     so it is now a COLUMN printed on every row (COMPARED AGAINST),
//     surviving every sort and every filter: connection secrets read
//     "git", addon-values secrets read "the vault" (with the mechanism —
//     "git only holds a pointer to it" — on hover, never tooltip-only).
//
//   - S3/S8: sorting by state uses a real priority order (StatusMark's
//     statusSortRank: out_of_sync, missing, unknown, in_sync), never the
//     alphabet — that is the page's default sort. A "the last check
//     failed" reason (S8, addon-values rows only) is a MAPPED, pre-written
//     sentence from the server — see internal/api's
//     addonValuesSecretCheckFailureSentence — never raw error text.
//
//   - S5: the values-secret Diff makes NO server call, by construction —
//     it renders canned sentences from the row's own state field. If this
//     ever needs a server call, that is a new design decision, not a
//     refactor. The connection-secret Diff keeps its existing
//     getClusterComparison fetch (labels only, never credentials).

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  CheckCircle,
  ChevronDown,
  ChevronsUpDown,
  ChevronUp,
  KeyRound,
  Loader2,
  Lock,
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
import { RowActionsMenu, type RowAction } from '@/components/RowActionsMenu'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import { showToast } from '@/components/ToastNotification'
import { StatusMark, statusLabel, statusSortRank, toResourceStatus, type ResourceStatus } from '@/components/resource/StatusMark'
import { TimeChip } from '@/components/resource/TimeChip'
import { ResourceDetailSheet } from '@/components/resource/ResourceDetailSheet'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

// ─────────────────────────────────────────────────────────────────────────────
// Unified row model — one shape for both secret kinds, hoisted at module
// scope (S7.3: matchesSearch/compareRows/buildUnifiedRows all get a stable
// identity across renders — nothing here is redefined inline in JSX, so
// the page's useMemo calls actually memoize instead of re-running on
// every keystroke).
// ─────────────────────────────────────────────────────────────────────────────

interface UnifiedRow {
  kind: 'connection' | 'values'
  key: string
  cluster: string
  addon?: string
  secretNamespace?: string
  secretName?: string
  state: string
  lastChecked?: string
  lastRepaired?: string
  lastRepairedDetail?: string
  lastCheckError?: string
  /** What this row is compared against — the S3 honesty lock, now a column value instead of a section header. */
  source: 'git' | 'vault'
}

function buildUnifiedRows(connectionRows: ConnectionSecretRow[], addonRows: AddonValuesSecretRow[]): UnifiedRow[] {
  const conn: UnifiedRow[] = connectionRows.map((r) => ({
    kind: 'connection',
    key: `connection-${r.cluster}`,
    cluster: r.cluster,
    secretNamespace: r.secret_namespace,
    secretName: r.secret_name,
    state: r.state,
    lastChecked: r.last_checked,
    lastRepaired: r.last_repaired,
    lastRepairedDetail: r.last_repaired_detail,
    source: 'git',
  }))
  const values: UnifiedRow[] = addonRows.map((r) => ({
    kind: 'values',
    key: `values-${r.cluster}-${r.addon}`,
    cluster: r.cluster,
    addon: r.addon,
    secretNamespace: r.secret_namespace,
    secretName: r.secret_name,
    state: r.state,
    lastChecked: r.last_checked,
    lastRepaired: r.last_repaired,
    lastRepairedDetail: r.last_repaired_detail,
    lastCheckError: r.last_check_error,
    source: 'vault',
  }))
  return [...conn, ...values]
}

function matchesSearch(row: UnifiedRow, q: string): boolean {
  return (
    row.cluster.toLowerCase().includes(q) ||
    (row.addon ?? '').toLowerCase().includes(q) ||
    (row.secretName ?? '').toLowerCase().includes(q) ||
    (row.secretNamespace ?? '').toLowerCase().includes(q)
  )
}

type SortKey = 'name' | 'cluster' | 'addon' | 'checked' | 'state'

// compareRows never sorts state alphabetically (S3) — it defers to
// StatusMark's statusSortRank, the same worst-first priority order ArgoCD
// uses, so a click on the State header surfaces problems instead of
// burying "out_of_sync" between "in_sync" and "missing".
function compareRows(a: UnifiedRow, b: UnifiedRow, key: SortKey): number {
  switch (key) {
    case 'name': {
      const an = `${a.secretNamespace ?? ''}/${a.secretName ?? ''}`
      const bn = `${b.secretNamespace ?? ''}/${b.secretName ?? ''}`
      return an < bn ? -1 : an > bn ? 1 : 0
    }
    case 'cluster':
      return a.cluster < b.cluster ? -1 : a.cluster > b.cluster ? 1 : 0
    case 'addon': {
      const aa = a.addon ?? ''
      const bb = b.addon ?? ''
      return aa < bb ? -1 : aa > bb ? 1 : 0
    }
    case 'checked': {
      const ac = a.lastChecked ?? ''
      const bc = b.lastChecked ?? ''
      return ac < bc ? -1 : ac > bc ? 1 : 0
    }
    case 'state':
      return statusSortRank(a.state) - statusSortRank(b.state)
    default:
      return 0
  }
}

// syncGateFor is the one place that decides whether Sync makes sense for a
// row right now, and why not when it doesn't — shared by the row's ⋯ menu
// and the detail panel's own Sync button so the two never disagree.
function syncGateFor(row: UnifiedRow): { disabled: boolean; reason?: string } {
  if (row.kind === 'connection') {
    if (row.state === 'in_sync') return { disabled: true, reason: 'Nothing to apply — this secret already matches git.' }
    if (row.state === 'missing')
      return { disabled: true, reason: "This secret hasn't been created yet — there's nothing to sync onto." }
    if (row.state === 'unknown') return { disabled: true, reason: 'Click Refresh first to check this secret.' }
    return { disabled: false }
  }
  if (row.state === 'in_sync') return { disabled: true, reason: 'Nothing to push — this secret already matches its source.' }
  if (row.state === 'unknown') return { disabled: true, reason: 'Click Refresh first to check this secret.' }
  return { disabled: false }
}

function actionsForRow(row: UnifiedRow, opts: { busy: boolean; onRefresh: () => void; onRequestSync: () => void }): RowAction[] {
  const gate = syncGateFor(row)
  return [
    {
      label: 'Refresh',
      icon: <RefreshCw className="h-3.5 w-3.5" />,
      onSelect: opts.onRefresh,
      loading: opts.busy,
    },
    {
      label: 'Sync',
      icon: <RotateCcw className="h-3.5 w-3.5" />,
      onSelect: opts.onRequestSync,
      disabled: gate.disabled,
      disabledReason: gate.reason,
    },
  ]
}

// ─────────────────────────────────────────────────────────────────────────────
// Filter chips (S4) — plain sums of the real per-row states. Never a
// rolled-up verdict, never a percentage, never total-only.
// ─────────────────────────────────────────────────────────────────────────────

const CHIP_ORDER: ResourceStatus[] = ['out_of_sync', 'missing', 'unknown', 'in_sync']

function FilterChip({
  status,
  count,
  active,
  onClick,
}: {
  status: ResourceStatus
  count: number
  active: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      data-testid={`filter-chip-${status}`}
      className={`inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition-colors ${
        active
          ? 'border-teal-600 bg-teal-600 text-white dark:border-teal-500 dark:bg-teal-600'
          : 'border-[#5a9dd0] bg-[#f0f7ff] text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
      }`}
    >
      {statusLabel(status)} {count}
    </button>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Sortable column header
// ─────────────────────────────────────────────────────────────────────────────

function SortableTh({
  label,
  sortKeyName,
  activeKey,
  dir,
  onSort,
  className,
}: {
  label: string
  sortKeyName: SortKey
  activeKey: SortKey
  dir: 'asc' | 'desc'
  onSort: (key: SortKey) => void
  className?: string
}) {
  const active = activeKey === sortKeyName
  return (
    <TableHead className={className}>
      <button
        type="button"
        onClick={() => onSort(sortKeyName)}
        className="inline-flex items-center gap-1 text-xs font-semibold uppercase tracking-wide text-[#2a5a7a] hover:text-teal-700 dark:text-gray-400 dark:hover:text-teal-400"
      >
        {label}
        {active ? (
          dir === 'asc' ? (
            <ChevronUp className="h-3 w-3" aria-hidden="true" />
          ) : (
            <ChevronDown className="h-3 w-3" aria-hidden="true" />
          )
        ) : (
          <ChevronsUpDown className="h-3 w-3 opacity-30" aria-hidden="true" />
        )}
      </button>
    </TableHead>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Panel action button — Refresh / Sync inside the detail panel. S7.1 fix
// applies here too: the info hint renders ONLY when the button is
// genuinely disabled.
// ─────────────────────────────────────────────────────────────────────────────

function PanelActionButton({
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
        onClick={onClick}
        disabled={disabled || loading}
        data-testid={testId}
        className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-1.5 text-sm font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
      >
        {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Icon className="h-3.5 w-3.5" />}
        {label}
      </button>
      {disabled && reason && <InfoHint text={reason} label={`Why is ${label} unavailable?`} />}
    </span>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine line — cadence + last run/error for one engine. Unchanged wording
// contract: the "Not running on this server" line stays exactly as-is
// (S4) when an engine isn't wired.
// ─────────────────────────────────────────────────────────────────────────────

function EngineLine({
  label,
  cadenceSentence,
  info,
}: {
  label: string
  cadenceSentence: string
  info?: ManagedSecretsEngineInfo
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-[#2a5a7a] dark:text-gray-300">
      <span className="font-medium text-[#0a2a4a] dark:text-white">{label}:</span>
      <span>{cadenceSentence}</span>
      {info?.wired ? (
        <>
          <span className="text-[#5a8aaa] dark:text-gray-500">·</span>
          <span>
            Last run: <TimeChip iso={info.last_run} />
          </span>
          {info.last_error && <span className="text-red-700 dark:text-red-400">· Last error: {info.last_error}</span>}
        </>
      ) : (
        <span className="text-[#5a8aaa] dark:text-gray-500">· Not running on this server.</span>
      )}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Detail side panel (S5) — carries everything the row no longer prints:
// purpose, source (+ mechanism on hover), state, timestamps, the S8
// check-failure sentence, Diff content, and Refresh/Sync.
// ─────────────────────────────────────────────────────────────────────────────

function SecretDetailPanel({
  row,
  open,
  onOpenChange,
  onRequestSync,
  onChanged,
}: {
  row: UnifiedRow | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onRequestSync: (row: UnifiedRow) => void
  onChanged: () => void
}) {
  const navigate = useNavigate()
  const [refreshing, setRefreshing] = useState(false)
  const [diffLoading, setDiffLoading] = useState(false)
  const [diffError, setDiffError] = useState<string | null>(null)
  const [diffData, setDiffData] = useState<ClusterComparisonResponse | null>(null)

  // Connection-secret Diff keeps its existing getClusterComparison fetch
  // (labels only, never credentials) — fired once per row the panel opens
  // for. Re-runs whenever a DIFFERENT row is shown.
  useEffect(() => {
    setDiffData(null)
    setDiffError(null)
    if (!row || row.kind !== 'connection') {
      setDiffLoading(false)
      return
    }
    let cancelled = false
    setDiffLoading(true)
    api
      .getClusterComparison(row.cluster)
      .then((result) => {
        if (!cancelled) setDiffData(result)
      })
      .catch((err) => {
        if (!cancelled) setDiffError(err instanceof Error ? err.message : 'Failed to load the diff')
      })
      .finally(() => {
        if (!cancelled) setDiffLoading(false)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row?.key])

  if (!row) return null

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      if (row.kind === 'connection') {
        await reconcileCluster(row.cluster)
        showToast(`Refresh triggered for cluster "${row.cluster}".`, 'success')
      } else {
        const result = await refreshAddonValuesSecret(row.cluster, row.addon!)
        showToast(result.message, 'success')
      }
      onChanged()
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to trigger refresh', 'error')
    } finally {
      setRefreshing(false)
    }
  }

  const gate = syncGateFor(row)
  const identity = row.secretNamespace && row.secretName ? `${row.secretNamespace}/${row.secretName}` : '—'

  const purposeSentence: ReactNode =
    row.kind === 'connection' ? (
      <>
        Connects <span className="font-medium">{row.cluster}</span> to ArgoCD.
      </>
    ) : (
      <>
        Carries values for addon <span className="font-medium">{row.addon}</span> on cluster{' '}
        <span className="font-medium">{row.cluster}</span>.
      </>
    )

  const sourceSentence = row.kind === 'connection' ? 'Compared against git.' : 'Compared against the vault — git only holds a pointer to it.'

  // Diff content:
  //  - connection: renders the fetched label-drift comparison.
  //  - values: NEVER a server call, by construction — a values secret's
  //    content must never reach the browser (S3(b)/S5). This branch reads
  //    only the row's own already-fetched state field. If this ever needs
  //    a server call, that is a new design decision, not a refactor.
  let diffBody: ReactNode
  if (row.kind === 'connection') {
    const drift = diffData?.cluster?.last_reconcile?.label_drift
    const added = drift?.added ?? []
    const removed = drift?.removed ?? []
    const changed = drift?.changed ?? []
    if (diffError) {
      diffBody = <p className="text-sm text-red-600 dark:text-red-400">{diffError}</p>
    } else if (diffLoading || !diffData) {
      diffBody = <p className="text-sm text-[#3a6a8a] dark:text-gray-400">Loading…</p>
    } else if (added.length === 0 && removed.length === 0 && changed.length === 0) {
      diffBody = (
        <div className="flex items-center gap-2">
          <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" />
          <p className="text-sm font-medium text-green-700 dark:text-green-400">
            No differences — this secret's addon labels match git.
          </p>
        </div>
      )
    } else {
      diffBody = (
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
              {changed.length} addon label{changed.length === 1 ? '' : 's'} {changed.length === 1 ? 'has' : 'have'} a different
              value than git:{' '}
              <span className="font-mono text-xs text-[#5a8aaa] dark:text-gray-500">{changed.join(', ')}</span>
            </p>
          )}
        </div>
      )
    }
  } else if (row.state === 'unknown') {
    diffBody = (
      <p className="text-sm text-[#3a6a8a] dark:text-gray-400">Sharko hasn't checked this secret yet — click Refresh to check now.</p>
    )
  } else if (row.state === 'in_sync') {
    diffBody = (
      <div className="flex items-center gap-2">
        <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" />
        <p className="text-sm font-medium text-green-700 dark:text-green-400">Matches its source.</p>
      </div>
    )
  } else if (row.state === 'missing') {
    diffBody = <p className="text-sm text-[#2a5a7a] dark:text-gray-300">This secret does not exist yet on the cluster — click Sync to create it.</p>
  } else {
    diffBody = (
      <p className="text-sm text-[#2a5a7a] dark:text-gray-300">Does not match its source right now — click Sync to push the current value.</p>
    )
  }

  return (
    <ResourceDetailSheet
      open={open}
      onOpenChange={onOpenChange}
      title={identity}
      subtitle={row.kind === 'connection' ? 'Cluster connection secret' : 'Addon values secret'}
      testId="secret-detail-panel"
    >
      <p className="text-sm text-[#0a2a4a] dark:text-white">{purposeSentence}</p>
      <p
        className="text-xs text-[#5a8aaa] dark:text-gray-500"
        title={row.kind === 'values' ? 'Git only holds a pointer to it — the value itself lives in the vault.' : undefined}
      >
        {sourceSentence}
      </p>

      <div className="flex items-center justify-between rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-3 dark:ring-gray-700 dark:bg-gray-800">
        <span className="text-sm text-[#2a5a7a] dark:text-gray-300">State</span>
        <StatusMark status={row.state} />
      </div>

      <dl className="space-y-1.5 text-sm">
        <div className="flex items-center justify-between">
          <dt className="text-[#3a6a8a] dark:text-gray-400">Last checked</dt>
          <dd>
            <TimeChip iso={row.lastChecked} />
          </dd>
        </div>
        <div className="flex items-center justify-between">
          <dt className="text-[#3a6a8a] dark:text-gray-400">Last repaired</dt>
          <dd className="text-right">
            <TimeChip iso={row.lastRepaired} />
            {row.lastRepairedDetail && (
              <span className="ml-1 text-xs text-[#5a8aaa] dark:text-gray-500">— {row.lastRepairedDetail}</span>
            )}
          </dd>
        </div>
      </dl>

      {row.lastCheckError && (
        <p className="text-sm text-red-700 dark:text-red-400" data-testid="last-check-error">
          The last check failed: {row.lastCheckError}
        </p>
      )}

      <RoleGuard roles={['admin', 'operator']}>
        <div className="flex items-center gap-2">
          <PanelActionButton onClick={handleRefresh} loading={refreshing} icon={RefreshCw} label="Refresh" testId="detail-refresh" />
          <PanelActionButton
            onClick={() => onRequestSync(row)}
            disabled={gate.disabled}
            icon={RotateCcw}
            label="Sync"
            reason={gate.reason}
            testId="detail-sync"
          />
        </div>
      </RoleGuard>

      <div>
        <h3 className="mb-1 text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Diff</h3>
        <div
          className="rounded-md ring-2 ring-[#6aade0] bg-white p-3 dark:ring-gray-700 dark:bg-gray-900"
          data-testid="detail-diff-panel"
        >
          {diffBody}
        </div>
      </div>

      <button
        type="button"
        onClick={() => navigate(row.kind === 'connection' ? `/clusters/${encodeURIComponent(row.cluster)}` : `/addons/${encodeURIComponent(row.addon!)}`)}
        data-testid="detail-view-page-link"
        className="text-sm text-teal-700 hover:underline dark:text-teal-400"
      >
        {row.kind === 'connection' ? 'View cluster page' : 'View addon page'}
      </button>
    </ResourceDetailSheet>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The page
// ─────────────────────────────────────────────────────────────────────────────

export function ManagedSecrets() {
  const [data, setData] = useState<ManagedSecretsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshingAll, setRefreshingAll] = useState(false)
  const [busyRows, setBusyRows] = useState<Record<string, boolean>>({})
  const [syncTarget, setSyncTarget] = useState<UnifiedRow | null>(null)
  const [syncing, setSyncing] = useState(false)
  const [selectedRow, setSelectedRow] = useState<UnifiedRow | null>(null)
  // Keeps the last-opened row visible while the sheet's close animation
  // plays — the row itself is cleared immediately on close, but the panel
  // shouldn't visibly go blank mid-slide-out.
  const lastRowRef = useRef<UnifiedRow | null>(null)
  if (selectedRow) lastRowRef.current = selectedRow

  const [search, setSearch] = useState('')
  const [stateFilter, setStateFilter] = useState<ResourceStatus | 'all'>('all')
  const [sortKey, setSortKey] = useState<SortKey>('state')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState<PageSize>(20)

  const load = useCallback(() => {
    return getManagedSecrets()
      .then((res) => setData(res))
      .catch(() => setData(null))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const connectionRows = data?.cluster_connection_secrets ?? []
  const addonRows = data?.addon_values_secrets ?? []
  const unifiedRows = useMemo(() => buildUnifiedRows(connectionRows, addonRows), [connectionRows, addonRows])

  const counts = useMemo(() => {
    const c: Record<ResourceStatus, number> = { in_sync: 0, out_of_sync: 0, missing: 0, unknown: 0 }
    for (const r of unifiedRows) c[toResourceStatus(r.state)]++
    return c
  }, [unifiedRows])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return unifiedRows.filter((r) => {
      if (stateFilter !== 'all' && toResourceStatus(r.state) !== stateFilter) return false
      if (!q) return true
      return matchesSearch(r, q)
    })
  }, [unifiedRows, search, stateFilter])

  const sorted = useMemo(() => {
    const copy = [...filtered]
    copy.sort((a, b) => {
      const cmp = compareRows(a, b, sortKey)
      return sortDir === 'asc' ? cmp : -cmp
    })
    return copy
  }, [filtered, sortKey, sortDir])

  useEffect(() => {
    setPage(1)
  }, [search, stateFilter, sortKey, sortDir, pageSize])

  const totalPages = Math.max(1, Math.ceil(sorted.length / pageSize))
  const clampedPage = Math.min(page, totalPages)
  const paged = useMemo(() => sorted.slice((clampedPage - 1) * pageSize, clampedPage * pageSize), [sorted, clampedPage, pageSize])

  const handleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const handleChipClick = (status: ResourceStatus) => {
    setStateFilter((prev) => (prev === status ? 'all' : status))
  }

  const handleRefreshRow = useCallback(
    async (row: UnifiedRow) => {
      setBusyRows((b) => ({ ...b, [row.key]: true }))
      try {
        if (row.kind === 'connection') {
          await reconcileCluster(row.cluster)
          showToast(`Refresh triggered for cluster "${row.cluster}".`, 'success')
        } else {
          const result = await refreshAddonValuesSecret(row.cluster, row.addon!)
          showToast(result.message, 'success')
        }
        load()
      } catch (err) {
        showToast(err instanceof Error ? err.message : 'Failed to trigger refresh', 'error')
      } finally {
        setBusyRows((b) => {
          const next = { ...b }
          delete next[row.key]
          return next
        })
      }
    },
    [load],
  )

  // Refresh all (S6): values secrets have a real fleet-wide trigger
  // (triggerSecretsReconcile). Connection secrets' own trigger
  // (POST /clusters/{name}/reconcile) is ALSO fleet-wide despite taking a
  // cluster name in its path — see internal/api/clusters_reconcile.go's
  // handleReconcileCluster doc comment — so as long as at least one
  // connection-secret row exists, this button genuinely refreshes both
  // engines, not just one.
  const handleRefreshAll = async () => {
    setRefreshingAll(true)
    try {
      const tasks: Promise<unknown>[] = [triggerSecretsReconcile()]
      if (connectionRows.length > 0) {
        tasks.push(reconcileCluster(connectionRows[0].cluster))
      }
      await Promise.allSettled(tasks)
    } finally {
      setRefreshingAll(false)
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
        const result = await syncAddonValuesSecret(syncTarget.cluster, syncTarget.addon!)
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

  const displayRow = selectedRow ?? lastRowRef.current

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-white">Managed Secrets</h1>
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
          Every secret Sharko manages, one row per secret — click a row for the full detail, Refresh and Sync live in
          its menu.
        </p>
      </div>

      <div className="space-y-2 rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-4 dark:ring-gray-700 dark:bg-gray-800">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Engines</h2>
          <RoleGuard roles={['admin', 'operator']}>
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                onClick={handleRefreshAll}
                disabled={refreshingAll}
                data-testid="refresh-all"
                className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-2.5 py-1 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
              >
                <RefreshCw className={`h-3 w-3 ${refreshingAll ? 'animate-spin' : ''}`} />
                Refresh all
              </button>
              <InfoHint
                text="Checks every secret in this table against its source right now — connection secrets against git, addon values secrets against the vault — instead of waiting for each engine's regular pass."
                label="What does Refresh all do?"
              />
            </div>
          </RoleGuard>
        </div>
        <EngineLine
          label="Cluster connection secrets"
          cadenceSentence="Re-checked every 30 seconds, and right after each merge."
          info={data?.engines.cluster_connection}
        />
        <EngineLine
          label="Addon values secrets"
          cadenceSentence="Checked every 5 minutes and repaired automatically."
          info={data?.engines.addon_values}
        />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        {CHIP_ORDER.map((status) => (
          <FilterChip
            key={status}
            status={status}
            count={counts[status]}
            active={stateFilter === status}
            onClick={() => handleChipClick(status)}
          />
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 220, maxWidth: 360 }}>
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
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[10, 20, 50, 100]} />
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-24">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-[#6aade0] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
        </div>
      ) : paged.length === 0 ? (
        <div className="rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 text-center text-sm text-[#5a8aaa] dark:ring-gray-700 dark:bg-gray-800 dark:text-gray-500">
          {unifiedRows.length === 0 ? 'Sharko is not managing any secrets yet.' : 'No secrets match this search.'}
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg ring-2 ring-[#6aade0] dark:ring-gray-700">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <SortableTh label="Name" sortKeyName="name" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                <TableHead className="text-xs font-semibold uppercase tracking-wide text-[#2a5a7a] dark:text-gray-400">
                  Compared against
                </TableHead>
                <SortableTh label="Cluster" sortKeyName="cluster" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                <SortableTh label="Addon" sortKeyName="addon" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                <SortableTh label="Checked" sortKeyName="checked" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                <SortableTh label="State" sortKeyName="state" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {paged.map((row) => (
                <TableRow
                  key={row.key}
                  data-testid={`secret-row-${row.key}`}
                  onClick={() => setSelectedRow(row)}
                  className="cursor-pointer"
                >
                  <TableCell>
                    <div className="flex items-start gap-2">
                      <div className="flex w-9 shrink-0 flex-col items-center pt-0.5 text-[#5a8aaa] dark:text-gray-500">
                        {row.kind === 'connection' ? (
                          <KeyRound className="h-4 w-4" aria-hidden="true" />
                        ) : (
                          <Lock className="h-4 w-4" aria-hidden="true" />
                        )}
                        <span className="text-[9px] font-semibold uppercase tracking-wide">
                          {row.kind === 'connection' ? 'Conn' : 'Values'}
                        </span>
                      </div>
                      <span className="whitespace-nowrap font-mono text-xs text-[#5a8aaa] dark:text-gray-500">
                        {row.secretNamespace && row.secretName ? `${row.secretNamespace}/${row.secretName}` : '—'}
                      </span>
                    </div>
                  </TableCell>
                  <TableCell>
                    {row.source === 'git' ? (
                      <span className="text-sm text-[#2a5a7a] dark:text-gray-300">git</span>
                    ) : (
                      <span
                        className="cursor-help text-sm text-[#2a5a7a] underline decoration-dotted decoration-[#6aade0] dark:text-gray-300"
                        title="Git only holds a pointer to it — the value itself lives in the vault."
                      >
                        the vault
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="text-sm text-[#2a5a7a] dark:text-gray-300">{row.cluster}</TableCell>
                  <TableCell className="text-sm text-[#2a5a7a] dark:text-gray-300">
                    {row.addon ?? <span className="text-[#5a8aaa] dark:text-gray-500">—</span>}
                  </TableCell>
                  <TableCell>
                    <TimeChip iso={row.lastChecked} />
                  </TableCell>
                  <TableCell>
                    <StatusMark status={row.state} />
                  </TableCell>
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <RoleGuard roles={['admin', 'operator']}>
                      <RowActionsMenu
                        label={`Actions for ${row.cluster}${row.addon ? ' / ' + row.addon : ''}`}
                        actions={actionsForRow(row, {
                          busy: !!busyRows[row.key],
                          onRefresh: () => handleRefreshRow(row),
                          onRequestSync: () => setSyncTarget(row),
                        })}
                      />
                    </RoleGuard>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <div className="flex items-center justify-between">
        <span className="text-xs text-[#3a6a8a] dark:text-gray-400">
          {sorted.length} of {unifiedRows.length} shown
        </span>
        <PaginationControls page={clampedPage} totalPages={totalPages} onPageChange={setPage} />
      </div>

      <SecretDetailPanel
        row={displayRow}
        open={selectedRow !== null}
        onOpenChange={(open) => {
          if (!open) setSelectedRow(null)
        }}
        onRequestSync={(row) => setSyncTarget(row)}
        onChanged={load}
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
