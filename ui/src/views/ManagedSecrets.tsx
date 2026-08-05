// ManagedSecrets — the /secrets page, a dense resource list built to look
// like ArgoCD's own Application list. Every secret Sharko manages —
// connection secrets AND addon-values secrets — is one row in one table: a
// small kind glyph, the identity, a small grey "kind · follows source"
// line under the identity, a cluster (when it isn't already the identity),
// a time, a status dot + word, and a row menu. Click a row to open the
// detail side panel; the row itself never grows.
//
// House-style pass (maintainer's adopted design review, three reviewers
// vs. the ArgoCD screenshot — verdict: "shaped like ArgoCD, painted like
// the opposite of ArgoCD"). Six habits, applied here first and meant to be
// copied by later resource lists:
//
//   H1 — quiet page, near-white content. The page background token and
//        this table's own surface both changed — see ui/src/index.css for
//        the token, and the table wrapper below for the surface.
//   H2 — colour lives on the status DOT only; the status WORD is plain
//        dark ink. See StatusMark.tsx.
//   H3 — one dot shape (filled circle, 4 fills) instead of 4 different
//        icon shapes. See StatusMark.tsx's StatusDot.
//   H4 — hairline dividers, not saturated-blue rings. Global border token,
//        see ui/src/index.css; this file's own boxes (table frame, chips,
//        the old Engines card) follow suit locally.
//   H5 — almost no boxes: the Engines card became the quiet strip below
//        (no ring, no card bg), the table frame is a pale ring + near-white
//        surface instead of a thick blue ring, and the filter chips are
//        pale-ring pills instead of filled blocks.
//   H6 — the row's identity (namespace/secretName) is the darkest, boldest
//        text on the row; everything else — the kind/source subline, the
//        cluster, the time, the status word — is lighter ink.
//
// Bugs fixed in the same pass:
//
//   B1 — filter-chip counts now follow the search box (searchFiltered),
//        not the chip filter itself — selecting a chip no longer zeroes
//        every other chip's count.
//   B2 — the footer says "Showing 1–20 of 169", never "169 of 169 shown"
//        while 20 rows are on screen; it also says when a filter has
//        narrowed the total.
//   B3 — the active chip filter, the search text, and the selected row are
//        all in the URL (via useSearchParams) — reloadable, bookmarkable,
//        back-button-safe, and the red engine error below can deep-link
//        into a filtered view of its cluster.
//   B4 — selection (the active page-size button, the active page number)
//        no longer reuses the same green/teal StatusMark uses for
//        "in sync" — it's the navy the sidebar already uses, so "selected"
//        and "healthy" can never be confused. See PaginationControls.tsx.
//
// S3 HONESTY LOCK (carried forward, non-negotiable): every row states, in
// visible text, what it's compared against — connection secrets read
// "cluster connection · follows git", addon-values secrets read "addon
// values · follows <the real backend name>" (never "the vault" unless the
// configured backend genuinely is Vault — see addon_values_secret_source
// on the API response). Never tooltip-only.
//
// S3/S8 (carried forward): sorting by state uses a real priority order
// (StatusMark's statusSortRank: out_of_sync, missing, unknown, in_sync),
// never the alphabet. A "the last check failed" reason (addon-values rows
// only) is a MAPPED, pre-written sentence from the server — never raw
// error text.
//
// S5 (carried forward): the values-secret Diff makes NO server call, by
// construction — it renders canned sentences from the row's own state
// field. The connection-secret Diff keeps its existing getClusterComparison
// fetch (labels only, never credentials).

import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
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
import { StatusDot, StatusMark, statusLabel, statusSortRank, toResourceStatus, type ResourceStatus } from '@/components/resource/StatusMark'
import { TimeChip } from '@/components/resource/TimeChip'
import { ResourceDetailSheet } from '@/components/resource/ResourceDetailSheet'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

// ─────────────────────────────────────────────────────────────────────────────
// Unified row model — one shape for both secret kinds, hoisted at module
// scope so matchesSearch/compareRows/buildUnifiedRows all get a stable
// identity across renders — nothing here is redefined inline in JSX, so
// the page's useMemo calls actually memoize instead of re-running on
// every keystroke.
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
  /**
   * What this row is compared against, already resolved to display text —
   * the S3 honesty lock. "git" for connection secrets; the real backend
   * name (or the "secrets store" fallback) for values secrets — see
   * addon_values_secret_source on the API response. Resolved once here,
   * not re-derived at render time, so every place that prints it (the row,
   * the detail panel) says the exact same thing.
   */
  sourceLabel: string
}

function buildUnifiedRows(connectionRows: ConnectionSecretRow[], addonRows: AddonValuesSecretRow[], valuesSourceLabel: string): UnifiedRow[] {
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
    sourceLabel: 'git',
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
    sourceLabel: valuesSourceLabel,
  }))
  return [...conn, ...values]
}

/** The row's small grey "kind · follows source" line — the honesty lock, printed once so the row and nowhere else has to remember the exact wording. */
function kindSourceLine(row: UnifiedRow): string {
  return row.kind === 'connection' ? `cluster connection · follows ${row.sourceLabel}` : `addon values · follows ${row.sourceLabel}`
}

function matchesSearch(row: UnifiedRow, q: string): boolean {
  return (
    row.cluster.toLowerCase().includes(q) ||
    (row.addon ?? '').toLowerCase().includes(q) ||
    (row.secretName ?? '').toLowerCase().includes(q) ||
    (row.secretNamespace ?? '').toLowerCase().includes(q)
  )
}

type SortKey = 'name' | 'cluster' | 'checked' | 'state'

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
// Filter chips — plain sums of the real per-row states (B1: computed over
// the SEARCH-filtered rows, never over the chip filter itself — that's
// what kept every chip's count honest even while one of them is active).
// An "All" chip is the only way back to everything once a chip is chosen.
// H5: pale-ring pills, never filled blocks. B4: the active chip uses the
// navy "selected" ink, never the green/amber "in sync"/"out of sync"
// status colours — active is a fact about the UI, not about a secret.
// ─────────────────────────────────────────────────────────────────────────────

const CHIP_ORDER: ResourceStatus[] = ['out_of_sync', 'missing', 'unknown', 'in_sync']

function FilterChip({
  status,
  label,
  count,
  active,
  onClick,
}: {
  status: ResourceStatus | 'all'
  label: string
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
      className={`inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium ring-1 transition-colors ${
        active
          ? 'text-[#1a3d5c] underline decoration-2 underline-offset-4 ring-[#1a3d5c] dark:text-teal-300 dark:ring-teal-400'
          : 'text-[#3a5770] ring-[#d7e2ea] hover:ring-[#b7c9d6] dark:text-gray-400 dark:ring-gray-700 dark:hover:ring-gray-600'
      }`}
    >
      {status !== 'all' && <StatusDot status={status} />}
      {label}
      <span className={active ? 'font-semibold' : 'text-[#8098ac] dark:text-gray-500'}>{count}</span>
    </button>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Sortable column header — H5: lighter ink, reads as a label, not data.
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
        className="inline-flex items-center gap-1 text-xs font-semibold uppercase tracking-wide text-[#8098ac] hover:text-teal-700 dark:text-gray-500 dark:hover:text-teal-400"
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
// Panel action button — Refresh / Sync inside the detail panel. The info
// hint renders ONLY when the button is genuinely disabled.
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
        className="inline-flex items-center gap-1.5 rounded-lg border border-[#c7d6e0] bg-white px-3 py-1.5 text-sm font-medium text-[#13293f] hover:bg-[#f2f6f9] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
      >
        {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Icon className="h-3.5 w-3.5" />}
        {label}
      </button>
      {disabled && reason && <InfoHint text={reason} label={`Why is ${label} unavailable?`} />}
    </span>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Engines quiet strip (H5 + cull) — the old boxed "Engines" card is gone:
// no ring, no card fill, just tiny grey labels with the plain fact under
// each, separated by a thin vertical hairline, the same shape as ArgoCD's
// own top status strip. The cadence sentence ("re-checked every 30
// seconds…") moved into a hover, and — this is the part that used to be a
// lie waiting to happen — it's built from the server's own
// interval_seconds, not a hardcoded string, so a config change can never
// leave the page stating a cadence that isn't true anymore. "Engines" is
// our internal machinery, not a user-facing word, so the strip is labelled
// by what the user is actually looking at.
// ─────────────────────────────────────────────────────────────────────────────

function humanizeInterval(seconds?: number): string {
  if (!seconds || seconds <= 0) return ''
  if (seconds % 60 === 0) {
    const m = seconds / 60
    return `${m} minute${m === 1 ? '' : 's'}`
  }
  return `${seconds} second${seconds === 1 ? '' : 's'}`
}

function cadenceSentence(kind: 'connection' | 'values', intervalSeconds?: number): string {
  const human = humanizeInterval(intervalSeconds)
  if (!human) return ''
  return kind === 'connection' ? `Re-checked every ${human}, and right after each merge.` : `Checked every ${human} and repaired automatically.`
}

function EngineStat({
  label,
  kind,
  info,
  onErrorClick,
}: {
  label: string
  kind: 'connection' | 'values'
  info?: ManagedSecretsEngineInfo
  onErrorClick?: (cluster: string) => void
}) {
  const cadence = cadenceSentence(kind, info?.interval_seconds)
  return (
    <div className="px-5 py-1 first:pl-0">
      <div className="text-[11px] font-medium uppercase tracking-wide text-[#8098ac] dark:text-gray-500">{label}</div>
      {info?.wired ? (
        <div className="mt-0.5 text-sm text-[#13293f] dark:text-gray-200" title={cadence || undefined}>
          Checked <TimeChip iso={info.last_run} />
        </div>
      ) : (
        <div className="mt-0.5 text-sm text-[#8098ac] dark:text-gray-500">Not running on this server.</div>
      )}
      {info?.last_error &&
        (info.last_error_cluster && onErrorClick ? (
          <button
            type="button"
            data-testid={`engine-error-${kind}`}
            onClick={() => onErrorClick(info.last_error_cluster!)}
            className="mt-1 block max-w-xs text-left text-xs text-red-700 hover:underline dark:text-red-400"
          >
            Last error on <span className="font-medium">{info.last_error_cluster}</span> <TimeChip iso={info.last_error_at} /> — {info.last_error}
          </button>
        ) : (
          <p data-testid={`engine-error-${kind}`} className="mt-1 max-w-xs text-xs text-red-700 dark:text-red-400">
            Last error: {info.last_error}
          </p>
        ))}
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Detail side panel — carries everything the row no longer prints:
// purpose, source (+ mechanism on hover), state, timestamps, the
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

  // S3 honesty lock, panel copy: row.sourceLabel is the real backend name
  // (or the "secrets store" fallback) resolved once server-side — never a
  // hardcoded "the vault".
  const sourceSentence =
    row.kind === 'connection' ? 'Compared against git.' : `Compared against ${row.sourceLabel} — git only holds a pointer to it.`

  // Diff content:
  //  - connection: renders the fetched label-drift comparison.
  //  - values: NEVER a server call, by construction — a values secret's
  //    content must never reach the browser. This branch reads only the
  //    row's own already-fetched state field. If this ever needs a server
  //    call, that is a new design decision, not a refactor.
  let diffBody: ReactNode
  if (row.kind === 'connection') {
    const drift = diffData?.cluster?.last_reconcile?.label_drift
    const added = drift?.added ?? []
    const removed = drift?.removed ?? []
    const changed = drift?.changed ?? []
    if (diffError) {
      diffBody = <p className="text-sm text-red-600 dark:text-red-400">{diffError}</p>
    } else if (diffLoading || !diffData) {
      diffBody = <p className="text-sm text-[#3a5770] dark:text-gray-400">Loading…</p>
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
            <p className="text-sm text-[#3a5770] dark:text-gray-300">
              Missing {added.length} addon label{added.length === 1 ? '' : 's'} that git expects:{' '}
              <span className="font-mono text-xs text-[#5c7288] dark:text-gray-500">{added.join(', ')}</span>
            </p>
          )}
          {removed.length > 0 && (
            <p className="text-sm text-[#3a5770] dark:text-gray-300">
              Has {removed.length} addon label{removed.length === 1 ? '' : 's'} git doesn't expect:{' '}
              <span className="font-mono text-xs text-[#5c7288] dark:text-gray-500">{removed.join(', ')}</span>
            </p>
          )}
          {changed.length > 0 && (
            <p className="text-sm text-[#3a5770] dark:text-gray-300">
              {changed.length} addon label{changed.length === 1 ? '' : 's'} {changed.length === 1 ? 'has' : 'have'} a different
              value than git:{' '}
              <span className="font-mono text-xs text-[#5c7288] dark:text-gray-500">{changed.join(', ')}</span>
            </p>
          )}
        </div>
      )
    }
  } else if (row.state === 'unknown') {
    diffBody = (
      <p className="text-sm text-[#3a5770] dark:text-gray-400">Sharko hasn't checked this secret yet — click Refresh to check now.</p>
    )
  } else if (row.state === 'in_sync') {
    diffBody = (
      <div className="flex items-center gap-2">
        <CheckCircle className="h-4 w-4 shrink-0 text-green-600 dark:text-green-400" />
        <p className="text-sm font-medium text-green-700 dark:text-green-400">Matches its source.</p>
      </div>
    )
  } else if (row.state === 'missing') {
    diffBody = <p className="text-sm text-[#3a5770] dark:text-gray-300">This secret does not exist yet on the cluster — click Sync to create it.</p>
  } else {
    diffBody = (
      <p className="text-sm text-[#3a5770] dark:text-gray-300">Does not match its source right now — click Sync to push the current value.</p>
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
      <p className="text-sm text-[#08192b] dark:text-white">{purposeSentence}</p>
      <p
        className="text-xs text-[#5c7288] dark:text-gray-500"
        title={row.kind === 'values' ? `Git only holds a pointer to it — the value itself lives in ${row.sourceLabel}.` : undefined}
      >
        {sourceSentence}
      </p>

      <div className="flex items-center justify-between rounded-lg ring-1 ring-[#d7e2ea] bg-white p-3 dark:ring-gray-700 dark:bg-gray-800">
        <span className="text-sm text-[#3a5770] dark:text-gray-300">State</span>
        <StatusMark status={row.state} />
      </div>

      <dl className="space-y-1.5 text-sm">
        <div className="flex items-center justify-between">
          <dt className="text-[#5c7288] dark:text-gray-400">Last checked</dt>
          <dd>
            <TimeChip iso={row.lastChecked} />
          </dd>
        </div>
        <div className="flex items-center justify-between">
          <dt className="text-[#5c7288] dark:text-gray-400">Last repaired</dt>
          <dd className="text-right">
            <TimeChip iso={row.lastRepaired} />
            {row.lastRepairedDetail && (
              <span className="ml-1 text-xs text-[#5c7288] dark:text-gray-500">— {row.lastRepairedDetail}</span>
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
        <h3 className="mb-1 text-sm font-semibold text-[#08192b] dark:text-gray-100">Diff</h3>
        <div
          className="rounded-md ring-1 ring-[#d7e2ea] bg-white p-3 dark:ring-gray-700 dark:bg-gray-900"
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

const VALID_STATES: string[] = [...CHIP_ORDER]

export function ManagedSecrets() {
  const [data, setData] = useState<ManagedSecretsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshingAll, setRefreshingAll] = useState(false)
  const [busyRows, setBusyRows] = useState<Record<string, boolean>>({})
  const [syncTarget, setSyncTarget] = useState<UnifiedRow | null>(null)
  const [syncing, setSyncing] = useState(false)

  // B3 — the active chip filter, the search text, and the selected row all
  // live in the URL (?state=, ?q=, ?row=) so the page can be reloaded,
  // bookmarked, shared, and reached from elsewhere (the engine error below
  // deep-links into a filtered view of one cluster) without losing state,
  // and so the back button actually goes back to the previous filter
  // instead of out of the page.
  const [searchParams, setSearchParams] = useSearchParams()
  const updateParams = useCallback(
    (mutate: (p: URLSearchParams) => void) => {
      const params = new URLSearchParams(searchParams.toString())
      mutate(params)
      setSearchParams(params, { replace: true })
    },
    [searchParams, setSearchParams],
  )

  const [stateFilter, setStateFilterState] = useState<ResourceStatus | 'all'>(() => {
    const v = searchParams.get('state')
    return v && VALID_STATES.includes(v) ? (v as ResourceStatus) : 'all'
  })
  const setStateFilter = useCallback(
    (next: ResourceStatus | 'all') => {
      setStateFilterState(next)
      updateParams((p) => {
        if (next === 'all') p.delete('state')
        else p.set('state', next)
      })
    },
    [updateParams],
  )

  const [search, setSearchState] = useState(() => searchParams.get('q') ?? '')
  const setSearch = useCallback(
    (next: string) => {
      setSearchState(next)
      updateParams((p) => {
        if (next) p.set('q', next)
        else p.delete('q')
      })
    },
    [updateParams],
  )

  const [selectedRowKey, setSelectedRowKeyState] = useState<string | null>(() => searchParams.get('row'))
  const selectRow = useCallback(
    (key: string | null) => {
      setSelectedRowKeyState(key)
      updateParams((p) => {
        if (key) p.set('row', key)
        else p.delete('row')
      })
    },
    [updateParams],
  )

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
  const valuesSourceLabel = data?.addon_values_secret_source || 'secrets store'
  const unifiedRows = useMemo(
    () => buildUnifiedRows(connectionRows, addonRows, valuesSourceLabel),
    [connectionRows, addonRows, valuesSourceLabel],
  )

  const selectedRow = useMemo(() => unifiedRows.find((r) => r.key === selectedRowKey) ?? null, [unifiedRows, selectedRowKey])
  // Keeps the last-opened row visible while the sheet's close animation
  // plays — the row itself is cleared immediately on close, but the panel
  // shouldn't visibly go blank mid-slide-out.
  const lastRowRef = useRef<UnifiedRow | null>(null)
  if (selectedRow) lastRowRef.current = selectedRow

  // B1 fix: search narrows the rows chip COUNTS are computed over — the
  // chip filter itself must NOT be part of that computation, or selecting
  // a chip would make every other chip (and itself, after a search) read
  // 0.
  const searchFiltered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return unifiedRows
    return unifiedRows.filter((r) => matchesSearch(r, q))
  }, [unifiedRows, search])

  const counts = useMemo(() => {
    const c: Record<ResourceStatus, number> = { in_sync: 0, out_of_sync: 0, missing: 0, unknown: 0 }
    for (const r of searchFiltered) c[toResourceStatus(r.state)]++
    return c
  }, [searchFiltered])

  const filtered = useMemo(() => {
    if (stateFilter === 'all') return searchFiltered
    return searchFiltered.filter((r) => toResourceStatus(r.state) === stateFilter)
  }, [searchFiltered, stateFilter])

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

  // B2 — an honest "Showing X–Y of Z" line: never "169 of 169 shown" while
  // 20 rows are on screen, and plain about it when a filter has narrowed
  // the total below everything Sharko manages.
  const hasActiveFilter = stateFilter !== 'all' || search.trim() !== ''
  const rangeStart = sorted.length === 0 ? 0 : (clampedPage - 1) * pageSize + 1
  const rangeEnd = Math.min(clampedPage * pageSize, sorted.length)
  const paginationSummary =
    sorted.length === 0
      ? hasActiveFilter
        ? `No secrets match this filter (${unifiedRows.length} total)`
        : 'No secrets'
      : hasActiveFilter
        ? `Showing ${rangeStart}–${rangeEnd} of ${sorted.length} (filtered from ${unifiedRows.length})`
        : `Showing ${rangeStart}–${rangeEnd} of ${sorted.length}`

  const handleSort = (key: SortKey) => {
    if (key === sortKey) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortKey(key)
      setSortDir('asc')
    }
  }

  const handleChipClick = (status: ResourceStatus) => {
    setStateFilter(stateFilter === status ? 'all' : status)
  }

  // The red engine error names a cluster — clicking it clears any state
  // filter and searches for that cluster, so the table narrows down to
  // exactly the row(s) the error is about.
  const filterToCluster = useCallback(
    (cluster: string) => {
      setStateFilter('all')
      setSearch(cluster)
    },
    [setStateFilter, setSearch],
  )

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

  // Refresh all: values secrets have a real fleet-wide trigger
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
    <div className="space-y-5">
      {/* H5 cull: no subtitle (ArgoCD never explains its own screen — a
          subtitle goes stale the day the layout changes), and a shrunk h1
          — big type belongs on the data, not on the page's own name. */}
      <h1 className="text-base font-semibold text-[#08192b] dark:text-white">Managed Secrets</h1>

      {/* Engines quiet strip — see the block comment on EngineStat above. */}
      <div className="flex flex-wrap items-start justify-between gap-3 border-y border-[#d7e2ea] py-2 dark:border-gray-800">
        <div className="flex flex-wrap divide-x divide-[#d7e2ea] dark:divide-gray-800">
          <EngineStat label="Cluster connections" kind="connection" info={data?.engines.cluster_connection} onErrorClick={filterToCluster} />
          <EngineStat label="Addon values" kind="values" info={data?.engines.addon_values} />
        </div>
        <RoleGuard roles={['admin', 'operator']}>
          <div className="flex items-center gap-1.5">
            <button
              type="button"
              onClick={handleRefreshAll}
              disabled={refreshingAll}
              data-testid="refresh-all"
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#c7d6e0] bg-white px-2.5 py-1 text-xs font-medium text-[#13293f] hover:bg-[#f2f6f9] disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              <RefreshCw className={`h-3 w-3 ${refreshingAll ? 'animate-spin' : ''}`} />
              Refresh all
            </button>
            <InfoHint
              text={`Checks every secret in this table against its source right now — connection secrets against git, addon values secrets against ${valuesSourceLabel} — instead of waiting for each engine's regular pass.`}
              label="What does Refresh all do?"
            />
          </div>
        </RoleGuard>
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <FilterChip status="all" label="All" count={searchFiltered.length} active={stateFilter === 'all'} onClick={() => setStateFilter('all')} />
        {CHIP_ORDER.map((status) => (
          <FilterChip
            key={status}
            status={status}
            label={statusLabel(status)}
            count={counts[status]}
            active={stateFilter === status}
            onClick={() => handleChipClick(status)}
          />
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1" style={{ minWidth: 220, maxWidth: 360 }}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-[#5c7288] dark:text-gray-500" />
          <input
            type="text"
            placeholder="Search by cluster, addon, or secret name..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="w-full rounded-lg border border-[#c7d6e0] py-2 pl-10 pr-4 text-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200 dark:placeholder-gray-500"
          />
        </div>
        <div className="ml-auto">
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[10, 20, 50, 100]} />
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-24">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-[#d7e2ea] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
        </div>
      ) : paged.length === 0 ? (
        <div className="rounded-lg ring-1 ring-[#d7e2ea] bg-white p-6 text-center text-sm text-[#8098ac] dark:ring-gray-800 dark:bg-gray-900 dark:text-gray-500">
          {unifiedRows.length === 0 ? 'Sharko is not managing any secrets yet.' : 'No secrets match this search.'}
        </div>
      ) : (
        // H1/H5 — the table frame: a pale hairline ring and a near-white
        // surface (was a thick saturated-blue ring with no fill, so the
        // page's own blue showed straight through every row).
        <div className="overflow-hidden rounded-lg ring-1 ring-[#d7e2ea] bg-white dark:ring-gray-800 dark:bg-gray-900">
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <SortableTh label="Name" sortKeyName="name" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
                <SortableTh label="Cluster" sortKeyName="cluster" activeKey={sortKey} dir={sortDir} onSort={handleSort} />
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
                  onClick={() => selectRow(row.key)}
                  className="cursor-pointer"
                >
                  {/* H6: the identity is the darkest, boldest text on the
                      row. The small grey line under it carries the S3
                      honesty lock (kind + source) — this replaces the old
                      separate COMPARED AGAINST column, which said the same
                      thing two columns away from the kind glyph. */}
                  <TableCell className="py-1.5">
                    <div className="flex items-start gap-2">
                      {row.kind === 'connection' ? (
                        <KeyRound className="mt-0.5 h-3.5 w-3.5 shrink-0 text-[#8098ac] dark:text-gray-500" aria-hidden="true" />
                      ) : (
                        <Lock className="mt-0.5 h-3.5 w-3.5 shrink-0 text-[#8098ac] dark:text-gray-500" aria-hidden="true" />
                      )}
                      <div className="leading-tight">
                        <div className="whitespace-nowrap font-mono text-sm font-semibold text-[#08192b] dark:text-white">
                          {row.secretNamespace && row.secretName ? `${row.secretNamespace}/${row.secretName}` : '—'}
                        </div>
                        <div className="text-[11px] text-[#5c7288] dark:text-gray-500">{kindSourceLine(row)}</div>
                      </div>
                    </div>
                  </TableCell>
                  {/*
                    Cluster: connection rows leave this blank ON PURPOSE —
                    the identity above (namespace/secretName) already IS
                    the cluster name (the same fact printed twice, side by
                    side, was the exact duplicate flagged in review). Values
                    rows show it here because it's the one thing that
                    actually distinguishes two rows with the same addon on
                    different clusters — not a duplicate there.
                  */}
                  <TableCell className="py-1.5 text-sm text-[#3a5770] dark:text-gray-300">
                    {row.kind === 'values' ? row.cluster : null}
                  </TableCell>
                  <TableCell className="py-1.5">
                    <TimeChip iso={row.lastChecked} />
                  </TableCell>
                  <TableCell className="py-1.5">
                    <StatusMark status={row.state} />
                  </TableCell>
                  <TableCell className="py-1.5" onClick={(e) => e.stopPropagation()}>
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
        <span className="text-xs text-[#5c7288] dark:text-gray-400" data-testid="pagination-summary">
          {paginationSummary}
        </span>
        <PaginationControls page={clampedPage} totalPages={totalPages} onPageChange={setPage} />
      </div>

      <SecretDetailPanel
        row={displayRow}
        open={selectedRow !== null}
        onOpenChange={(open) => {
          if (!open) selectRow(null)
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
            : `Pushes the current value from ${syncTarget?.sourceLabel ?? 'its source'} to this cluster — creates the secret if missing, replaces it if the content differs.`
        }
        confirmText="Sync"
        loading={syncing}
      />
    </div>
  )
}

export default ManagedSecrets
