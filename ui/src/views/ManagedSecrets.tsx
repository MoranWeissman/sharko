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
// H1 follow-up (maintainer: "gray doesnt suits the original colors of
// sharko... this is literally copying the colors from argocd one to
// one"): the page background and border tokens went back to a light,
// calm SHARKO blue instead of neutral grey — see ui/src/index.css. And
// a thing we were missing that ArgoCD does have: a thin (3px) status
// colour strip down the left edge of every row, copied from ArgoCD's own
// list and tile views. It reads off the exact same colour table as the
// status dot and the filter chips (StatusMark.tsx's statusStripClassName)
// so the three can never disagree, and it adds no other colour to the
// row — the status word stays plain dark ink (H2 still holds).
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
// never the alphabet. A "the last check failed" reason — on BOTH kinds of
// row since P1-B, a connection row's failed check renders unknown, not
// out_of_sync — is a MAPPED, pre-written sentence from the server, never
// raw error text.
//
// S5 (carried forward): the values-secret Diff makes NO server call, by
// construction — it renders canned sentences from the row's own state
// field. The connection-secret Diff keeps its existing getClusterComparison
// fetch (labels only, never credentials).
//
// ─────────────────────────────────────────────────────────────────────────
// This pass (maintainer's ask: "datadog → cluster name → the secret name
// with namespace, backend type, synced/out of synced… and clicking on it
// should open the resource as it looks inside the cluster right now, read
// only"):
//
//   G1 — every row carries its own backend (row.source, from the server's
//        per-row field) instead of one label for the whole page, so
//        grouping, filtering and sorting by backend all read a real
//        per-row fact.
//   G2 — a "Group by" control: none (the default, today's flat list),
//        addon, or cluster. Grouped view is a collapsible parent line with
//        its rows under it, the same interaction AddonVersionList already
//        uses. Group by addon gives exactly his datadog → cluster →
//        secret picture; group by cluster gives one cluster's whole set,
//        both kinds together.
//   G3 — a group header may only state PLAIN SUMS of the real per-row
//        states ("12 secrets · 9 in sync · 2 out of sync"). Never a
//        rolled-up verdict, never a percentage, and never a group-level
//        "last checked" — different rows were checked at different times.
//   G4 — the detail panel gained a Resource section: the live Secret as it
//        is on the cluster right now, rendered the way ArgoCD renders one.
//        EVERY VALUE IS BLANKED BY THE SERVER — the browser never receives
//        one, and there is no field in the response a value could arrive
//        in. One live read per opened row, on the click that opened it;
//        never during a list render, never on a timer, never fanned out.
//
// ─────────────────────────────────────────────────────────────────────────
// P3-F2 — the panel rebuilt AROUND the resource.
//
// G4's live view arrived as an afterthought: it sat second-to-last in the
// panel, below the Diff; it was OUTSIDE the role guard, so a viewer got a
// permission error where a sentence belonged; it fired a read even for a
// row already known to be missing, which could only ever come back 404;
// and a failed read was a dead end with no way to try again.
//
// The panel now reads top to bottom the way ArgoCD lays out a resource —
// header, state and actions, the diff, the keys, then everything else —
// and the diff itself is two cards with ONE sentence between them: what
// this secret should be, what is actually on the cluster, and how the two
// relate. See SecretDetailPanel and diffVerdictFor.
//
// The rule that decided every call in that rebuild, and the one to hold
// on to if it is ever extended: Sharko may describe the DELIVERY, never
// the secret. Where a value comes from, when it was written, which commit
// it was built from, whether the cluster has a key at all — all fine. The
// value, its length, a hash of it, or a per-key "this one matches" verdict
// the engines never actually computed — none of those, ever.

import { Fragment, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
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
  getAddonValuesSecretResource,
  getConnectionSecretResource,
  getManagedSecrets,
  reconcileCluster,
  refreshAddonValuesSecret,
  resyncClusterLabels,
  syncAddonValuesSecret,
  checkAllAddonValuesSecrets,
} from '@/services/api'
import type {
  AddonValuesSecretRow,
  ClusterComparisonResponse,
  ConnectionSecretRow,
  ManagedSecretsEngineInfo,
  ManagedSecretsResponse,
  SecretResource,
} from '@/services/models'
import { PaginationControls, PageSizeSelector, type PageSize } from '@/components/PaginationControls'
import { InfoHint } from '@/components/InfoHint'
import { RoleGuard } from '@/components/RoleGuard'
import { AuthContext } from '@/hooks/useAuth'
import { RowActionsMenu, type RowAction } from '@/components/RowActionsMenu'
import { ConfirmationModal } from '@/components/ConfirmationModal'
import { showToast } from '@/components/ToastNotification'
import { StatusDot, StatusMark, statusLabel, statusSortRank, statusStripClassName, toResourceStatus, type ResourceStatus } from '@/components/resource/StatusMark'
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

// The four states, worst first — the order the filter chips render in and
// the order a group header lists its sums in. Declared here because both
// the chips (far below) and groupSummary (just below) read it.
const CHIP_ORDER: ResourceStatus[] = ['out_of_sync', 'missing', 'foreign', 'unknown', 'in_sync']

/**
 * The one sentence Sharko says about a secret somebody else created — on the
 * disabled Sync button here, and word-for-word the same sentence the server
 * returns if an API call gets past the button (internal/secrets.
 * ErrForeignSecret). One boundary, one sentence, everywhere.
 */
const FOREIGN_SYNC_REASON = 'Someone else created this one — Sharko will not touch it.'

/**
 * What "Refresh all" does, in the user's words. Both engines are checked and
 * neither is written to; the repairs are the engines' own job on their own
 * schedule, which is what makes Refresh safe to press.
 */
const REFRESH_ALL_HINT =
  "Asks Sharko to check every secret against its source — connection secrets against git, addon values against your secrets store. Checking only: nothing is written. The engines' own loops do the repairs."

/** The toast a connection row's Refresh raises. The check is estate-wide by nature, so the sentence says so instead of implying one cluster was singled out. */
function connectionRefreshToast(cluster: string): string {
  return `Checking every cluster's connection secret now, ${cluster} included.`
}

/**
 * P2-D D3: the bar both quiet row warnings below use — three in a row is
 * worth a person's attention. Matches the backend's fightGaugeThreshold
 * (internal/clusterreconciler and internal/secrets), which also drives
 * sharko_reconciler_fights, so the page and the metric agree on what
 * counts as "a fight worth mentioning."
 */
const ROW_WARNING_THRESHOLD = 3

/** The connection-row panel line for D3's fight warning — only shown at ROW_WARNING_THRESHOLD or more. */
function fightWarningSentence(count: number): string {
  return `Something keeps changing this secret back — Sharko has re-applied it ${count} times in a row.`
}

/** The values-row panel line for D3's consecutive-failure warning — only shown at ROW_WARNING_THRESHOLD or more. */
function consecutiveFailuresSentence(count: number): string {
  return `The last ${count} checks of this secret failed in a row.`
}

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
   * fightCount (connection rows) / consecutiveFailures (values rows) — P2-D
   * D3's two quiet row warnings. Only one is ever set for a given row (a
   * row is one kind or the other); kept as two separate fields rather than
   * one shared "warningCount" so each panel sentence stays specific about
   * what it's counting instead of a generic number nobody can place.
   */
  fightCount?: number
  consecutiveFailures?: number
  /**
   * What this row is compared against, already resolved to display text —
   * the S3 honesty lock, now a PER-ROW fact (G1). It comes straight off
   * the row the server sent (row.source): "git" for a connection secret,
   * the real backend name (or the honest "secrets store" fallback) for a
   * values secret. The page-level addon_values_secret_source is only the
   * fallback for a server that predates the per-row field, and the copy
   * that is genuinely about the whole page.
   */
  sourceLabel: string
  /**
   * (P2-C1) Full branch head commit SHA the pass that produced this row's
   * state read git at. Connection rows only — values rows never get a
   * commit (their intent is the store, not git; see comparedPath's doc
   * comment). Absent when the git provider couldn't say.
   */
  comparedRevision?: string
  /** (P2-C1) The exact managed-clusters file path this row was compared against. Connection rows only. */
  comparedPath?: string
  /** (P2-C1) Full commit SHA the last successful write was built from. Connection rows only. */
  appliedRevision?: string
  /** (P2-C3) Will Sharko fix this row on its own, without a human clicking Sync. */
  selfHeals: boolean
  /** (P2-C6) Which side moved for an out-of-sync connection row: 'git' or 'cluster'. Connection rows only. */
  driftSource?: 'git' | 'cluster'
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
    // P1-B: connection rows now carry the same "why didn't the last check
    // finish" fact values rows already did — shares the exact rendering
    // below (the panel's lastCheckError paragraph), no per-kind branch.
    lastCheckError: r.last_check_error,
    fightCount: r.fight_count,
    sourceLabel: r.source || 'git',
    comparedRevision: r.compared_revision,
    comparedPath: r.compared_path,
    appliedRevision: r.applied_revision,
    selfHeals: r.self_heals,
    driftSource: r.drift_source,
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
    consecutiveFailures: r.consecutive_failures,
    sourceLabel: r.source || valuesSourceLabel,
    selfHeals: r.self_heals,
  }))
  return [...conn, ...values]
}

// ─────────────────────────────────────────────────────────────────────────────
// Grouping (G2/G3)
// ─────────────────────────────────────────────────────────────────────────────

export type GroupBy = 'none' | 'addon' | 'cluster'

export interface RowGroup {
  key: string
  label: string
  /** The small grey line under a group's name — what KIND of thing it is. */
  sublabel: string
  rows: UnifiedRow[]
}

/** The bucket connection secrets land in when grouping by addon: they are
 *  not an addon, and pretending otherwise would be the kind of tidy lie
 *  this page keeps refusing to tell. */
const CONNECTIONS_GROUP_KEY = '__connections__'

/**
 * buildRowGroups splits already-sorted rows into groups, in the order each
 * group's FIRST row appears. That is deliberate: the page's default sort
 * is worst-first, so groups with problems in them float to the top without
 * needing a second, separate rule anyone has to learn.
 */
export function buildRowGroups(rows: UnifiedRow[], groupBy: GroupBy): RowGroup[] {
  if (groupBy === 'none') return []
  const order: string[] = []
  const byKey = new Map<string, RowGroup>()

  for (const row of rows) {
    let key: string
    let label: string
    let sublabel: string
    if (groupBy === 'cluster') {
      key = `cluster-${row.cluster}`
      label = row.cluster
      sublabel = 'cluster'
    } else if (row.kind === 'values') {
      key = `addon-${row.addon}`
      label = row.addon ?? '—'
      sublabel = 'addon'
    } else {
      key = CONNECTIONS_GROUP_KEY
      label = 'Cluster connections'
      sublabel = 'not an addon — one secret per cluster'
    }

    let group = byKey.get(key)
    if (!group) {
      group = { key, label, sublabel, rows: [] }
      byKey.set(key, group)
      order.push(key)
    }
    group.rows.push(row)
  }

  return order.map((k) => byKey.get(k)!)
}

/**
 * groupSummary is the ONLY thing a group header is allowed to say about
 * its rows (G3): plain sums of the real per-row states, in the page's own
 * worst-first order, and only the states that are actually present.
 *
 * Deliberately absent, and not to be added later:
 *   - a rolled-up verdict ("healthy", "all good") — a group is not a
 *     thing that has a state; its rows are.
 *   - a percentage — it hides how many rows it is over.
 *   - a group-level "last checked" — different rows were checked at
 *     different times, so any single time here would be wrong for most of
 *     them. If one is ever genuinely wanted it must be the OLDEST, and it
 *     must say that in words.
 */
export function groupSummary(rows: UnifiedRow[]): string {
  const counts: Record<ResourceStatus, number> = { in_sync: 0, out_of_sync: 0, missing: 0, foreign: 0, unknown: 0 }
  for (const r of rows) counts[toResourceStatus(r.state)]++
  const parts = [`${rows.length} secret${rows.length === 1 ? '' : 's'}`]
  for (const status of CHIP_ORDER) {
    if (counts[status] > 0) parts.push(`${counts[status]} ${statusLabel(status).toLowerCase()}`)
  }
  return parts.join(' · ')
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
  // Checked first, and for both kinds of row: a secret Sharko did not create
  // is never Sharko's to write, whatever else is true about it (P1-A).
  if (row.state === 'foreign') return { disabled: true, reason: FOREIGN_SYNC_REASON }
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
// Resource section (G4) — the live Secret, the way ArgoCD shows one.
//
// EVERY VALUE HERE WAS BLANKED BY THE SERVER. The response carries key
// NAMES paired with a fixed mask the server put in; there is no field a
// real value could arrive in, and nothing below un-hides anything. Do not
// "improve" this by adding a reveal toggle, a copy button, or a request
// for the real content — that is a new design decision, not a refactor.
//
// COST: one live read per opened row, fired by the click that opened the
// panel. Not on a timer, not prefetched, not fanned out across rows. The
// server handler says the same thing in its own header.
// ─────────────────────────────────────────────────────────────────────────────

function KeyValueList({
  items,
  empty,
  testId,
}: {
  items: { key: string; value: string; blanked?: boolean }[]
  empty: string
  testId: string
}) {
  if (items.length === 0) {
    return <p className="text-xs text-[#8098ac] dark:text-gray-500">{empty}</p>
  }
  return (
    <dl className="space-y-0.5" data-testid={testId}>
      {items.map((item) => (
        <div key={item.key} className="flex items-baseline justify-between gap-3">
          <dt className="truncate font-mono text-xs text-[#3a5770] dark:text-gray-400" title={item.key}>
            {item.key}
          </dt>
          <dd
            className={`shrink-0 font-mono text-xs ${item.blanked ? 'text-[#8098ac] dark:text-gray-500' : 'text-[#08192b] dark:text-gray-200'}`}
            title={item.blanked ? 'Sharko blanks this on the server — only its own provenance notes are shown as written.' : undefined}
          >
            {item.value}
          </dd>
        </div>
      ))}
    </dl>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The live read (P3-F2) — one hook, so the right-hand card and the key
// table below it read the SAME fetch instead of firing two.
//
// Four rules this hook exists to hold:
//
//   1. NEVER PREFETCH. The read fires when the panel is open on a row and
//      the reader is allowed to see it — never on a list render, never on
//      a timer, never fanned out. The server handler's own header says the
//      same thing; see internal/api/secret_resource.go.
//   2. A row already known to be MISSING fires nothing. The row state
//      already answers "is it on the cluster" — sending a read that can
//      only come back 404 is a doomed call, and it made the panel show a
//      red error for a secret whose absence is the ordinary, expected
//      thing.
//   3. A reader who is not allowed to read live secrets fires nothing.
//      Before this, a viewer's panel fired the request anyway and rendered
//      the 403 as an error, which is a permission dialog dressed up as a
//      fault.
//   4. It ends. A request that never settles used to leave a spinner
//      turning forever; after LIVE_READ_TIMEOUT_MS the panel says so and
//      offers Retry.
// ─────────────────────────────────────────────────────────────────────────────

/** How long the panel waits for a live read before giving up and offering Retry. */
const LIVE_READ_TIMEOUT_MS = 15000

/** What the panel says when the wait ran out. Sharko's own sentence — the request never answered, so there is no server sentence to quote. */
const LIVE_READ_TIMEOUT_SENTENCE = 'The cluster did not answer in time.'

type LiveSecretState =
  | { status: 'skipped' }
  | { status: 'loading' }
  | { status: 'ready'; resource: SecretResource }
  | { status: 'error'; message: string; notFound: boolean }

function useLiveSecret(row: UnifiedRow | null, allowed: boolean) {
  const [state, setState] = useState<LiveSecretState>({ status: 'skipped' })
  const [attempt, setAttempt] = useState(0)

  const rowKey = row?.key ?? ''
  const kind = row?.kind
  const cluster = row?.cluster ?? ''
  const addon = row?.addon
  // Rule 2 + rule 3, in one place: these are the only two reasons the read
  // is skipped, and both are facts already in hand — no request needed to
  // discover either.
  const skip = !row || !allowed || row.state === 'missing'

  useEffect(() => {
    if (skip) {
      setState({ status: 'skipped' })
      return
    }
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    setState({ status: 'loading' })

    const request = kind === 'connection' ? getConnectionSecretResource(cluster) : getAddonValuesSecretResource(cluster, addon!)
    const timeout = new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(LIVE_READ_TIMEOUT_SENTENCE)), LIVE_READ_TIMEOUT_MS)
    })

    Promise.race([request, timeout])
      .then((res) => {
        if (!cancelled) setState({ status: 'ready', resource: res as SecretResource })
      })
      .catch((err: unknown) => {
        if (cancelled) return
        // Whatever the server said, verbatim — it only ever sends
        // pre-written sentences here, never raw provider or cluster text.
        // The status code is read off the error rather than through an
        // `instanceof ApiError`: a 404 is not a fault, it is the cluster
        // saying the object is not there, and the panel says exactly that
        // instead of painting an error.
        const status = (err as { status?: number } | null)?.status
        setState({
          status: 'error',
          message: err instanceof Error ? err.message : 'Sharko could not read this secret.',
          notFound: status === 404,
        })
      })
      .finally(() => {
        if (timer) clearTimeout(timer)
      })

    return () => {
      cancelled = true
      // Cleared here too, not only in .finally: an unmount before the race
      // settles must not leave a timer running past the test that made it.
      if (timer) clearTimeout(timer)
    }
    // rowKey (a stable string) is the dependency, NOT the row object — the
    // list re-reads itself on every Refresh/Sync and hands down a brand new
    // object each time. Keying on the object would reload the panel's live
    // card every time the list refreshed behind it, which is exactly the
    // behaviour a 30-second auto-refresh would turn into a flicker.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rowKey, attempt, skip, kind, cluster, addon])

  return { live: state, retry: useCallback(() => setAttempt((a) => a + 1), []) }
}

// ─────────────────────────────────────────────────────────────────────────────
// The diff, rebuilt around the resource (P3-F2)
//
// Two cards and one sentence, the way ArgoCD shows a resource: on the left
// what this secret SHOULD be (its intent — a git file and a commit, or the
// store its values come from), on the right what IS on the cluster right
// now. The sentence above them says how the two relate, and it is chosen
// from exactly five, one per real row state.
//
// WHAT THIS DELIBERATELY DOES NOT SAY, ever: a per-key verdict. The key
// table below lists each key, where its value comes from, and whether the
// cluster has it — and stops there. Both engines compare WHOLE secrets, so
// "this key matches and that one doesn't" is a fact nothing in Sharko ever
// established; printing one would be inventing it. Do not add a tick.
// ─────────────────────────────────────────────────────────────────────────────

type DiffVerdict = 'match' | 'differ' | 'never_created' | 'could_not_look' | 'foreign'

/**
 * diffVerdictFor picks which of the five sentences the panel says.
 *
 * The order of the checks is the whole logic:
 *   - foreign first: whoever else owns this secret, that fact outranks
 *     everything else Sharko might say about it.
 *   - a row already known missing next: no read was fired, so there is
 *     nothing on the right to describe.
 *   - a live read that came back 404: the object is not there NOW, which is
 *     fresher than whatever the last check recorded.
 *   - any other failed read: Sharko genuinely could not look, and says so
 *     rather than guessing from a stale state.
 *   - otherwise the row's own state, which is a real comparison the engine
 *     performed: in sync, out of sync, or "unknown" — and unknown IS
 *     "could not look", because it means the last check never finished.
 */
function diffVerdictFor(row: UnifiedRow, live: LiveSecretState): DiffVerdict {
  if (row.state === 'foreign') return 'foreign'
  if (row.state === 'missing') return 'never_created'
  if (live.status === 'error') return live.notFound ? 'never_created' : 'could_not_look'
  if (row.state === 'in_sync') return 'match'
  if (row.state === 'out_of_sync') return 'differ'
  return 'could_not_look'
}

/**
 * The five sentences. "never created" is the one that changes wording by
 * kind, and honestly so: on a values row Sync really is what creates the
 * secret, but on a connection row Sync is disabled for exactly this state
 * (there is nothing to sync onto yet) — promising it there would send a
 * reader to a button that refuses. Who fixes it is answered one line below
 * by the self-heal promise, which already knows.
 */
function diffVerdictSentence(verdict: DiffVerdict, row: UnifiedRow): string {
  switch (verdict) {
    case 'match':
      return 'These match — the cluster has what the source says.'
    case 'differ':
      return "These differ — Sync writes the source's version onto the cluster."
    case 'never_created':
      return row.kind === 'values'
        ? 'This secret was never created on the cluster — Sync creates it.'
        : 'This secret was never created on the cluster.'
    case 'foreign':
      return 'Someone else created this secret — Sharko will not touch it.'
    case 'could_not_look':
      return 'Sharko could not look at the cluster just now.'
  }
}

function DiffCard({ title, children, testId }: { title: string; children: ReactNode; testId: string }) {
  return (
    <div className="rounded-md ring-1 ring-[#d7e2ea] bg-white p-3 dark:ring-gray-700 dark:bg-gray-900" data-testid={testId}>
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-[#8098ac] dark:text-gray-500">{title}</div>
      {children}
    </div>
  )
}

/**
 * The left card — what this secret should be. It paints the instant the
 * panel opens, from the row the list already has: no request, nothing to
 * wait for. That is the point of splitting the two cards apart.
 */
function IntentCard({ row }: { row: UnifiedRow }) {
  if (row.kind === 'connection') {
    return (
      <DiffCard title="What it should be" testId="diff-intent-card">
        {row.comparedPath ? (
          <p className="break-all font-mono text-xs text-[#08192b] dark:text-gray-200">{row.comparedPath}</p>
        ) : (
          <p className="text-sm text-[#3a5770] dark:text-gray-400">Sharko hasn't compared this secret against git yet.</p>
        )}
        {row.comparedRevision && (
          <p className="mt-1 text-xs text-[#5c7288] dark:text-gray-500" title={`Full commit: ${row.comparedRevision}`}>
            at commit <span className="font-mono">{row.comparedRevision.slice(0, 7)}</span>
          </p>
        )}
        <p className="mt-2 text-xs text-[#5c7288] dark:text-gray-500">
          The addon labels in this file are what this secret is built from.
        </p>
      </DiffCard>
    )
  }
  return (
    <DiffCard title="What it should be" testId="diff-intent-card">
      <p className="text-sm text-[#08192b] dark:text-gray-200">The values come from {row.sourceLabel}.</p>
      <p className="mt-2 text-xs text-[#5c7288] dark:text-gray-500">
        Git holds a pointer to where each value lives, never the value itself. Each key's pointer is in the key list below.
      </p>
    </DiffCard>
  )
}

/**
 * The right card — what is on the cluster right now.
 *
 * EVERY VALUE HERE WAS BLANKED BY THE SERVER. The response carries key
 * NAMES paired with a fixed mask the server put in; there is no field a
 * real value could arrive in, and nothing below un-hides anything. Do not
 * "improve" this by adding a reveal toggle, a copy button, or a request for
 * the real content — that is a new design decision, not a refactor.
 */
function LiveCard({ row, live, onRetry }: { row: UnifiedRow; live: LiveSecretState; onRetry: () => void }) {
  return (
    <DiffCard title="What is on the cluster" testId="diff-live-card">
      <div className="space-y-3" data-testid="detail-resource-panel">
        {live.status === 'skipped' && row.state === 'missing' && (
          <p className="text-sm text-[#3a5770] dark:text-gray-400" data-testid="resource-not-there">
            Nothing is there — this secret has not been created yet.
          </p>
        )}
        {live.status === 'loading' && <p className="text-sm text-[#3a5770] dark:text-gray-400">Reading it from the cluster…</p>}
        {live.status === 'error' && (
          // A failed read says so and shows nothing else. It never falls
          // back to the last thing we saw, or to anything made up.
          <>
            <p className="text-sm text-red-700 dark:text-red-400" data-testid="resource-error">
              {live.message}
            </p>
            <button
              type="button"
              onClick={onRetry}
              data-testid="resource-retry"
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#c7d6e0] bg-white px-2.5 py-1 text-xs font-medium text-[#13293f] hover:bg-[#f2f6f9] dark:border-gray-600 dark:bg-gray-700 dark:text-gray-300 dark:hover:bg-gray-600"
            >
              <RefreshCw className="h-3 w-3" />
              Retry
            </button>
          </>
        )}
        {live.status === 'ready' && (
          <>
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1 text-xs text-[#5c7288] dark:text-gray-500">
              <span className="break-all font-mono text-sm font-semibold text-[#08192b] dark:text-white">
                {live.resource.namespace}/{live.resource.name}
              </span>
              {live.resource.secret_type && <span>type {live.resource.secret_type}</span>}
            </div>
            <p className="text-xs text-[#5c7288] dark:text-gray-500">Read from {live.resource.read_from}.</p>

            <div>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-[#8098ac] dark:text-gray-500">Labels</div>
              <KeyValueList items={live.resource.labels} empty="No labels." testId="resource-labels" />
            </div>

            <div>
              <div className="mb-1 text-[11px] font-medium uppercase tracking-wide text-[#8098ac] dark:text-gray-500">Annotations</div>
              <KeyValueList items={live.resource.annotations} empty="No annotations." testId="resource-annotations" />
            </div>
          </>
        )}
      </div>
    </DiffCard>
  )
}

/** The sentence a reader who cannot open live secrets sees where the right card would be. Calm, and about access — not an error. */
const LIVE_READ_NEEDS_OPERATOR = 'Reading the live secret needs operator access. What Sharko already knows about it is on the left.'

/**
 * The key table — each key, where its value comes from, and whether the
 * cluster has it. Read off the same live read the right card uses.
 *
 * NO PER-KEY MATCH VERDICT. See the block comment above diffVerdictFor.
 */
function KeyTable({ live }: { live: LiveSecretState }) {
  if (live.status !== 'ready') return null
  const keys = live.resource.data_keys
  return (
    <div data-testid="detail-key-table">
      <h3 className="mb-1 text-sm font-semibold text-[#08192b] dark:text-gray-100">Keys</h3>
      <div className="rounded-md ring-1 ring-[#d7e2ea] bg-white p-3 dark:ring-gray-700 dark:bg-gray-900">
        {keys.length === 0 ? (
          <p className="text-xs text-[#8098ac] dark:text-gray-500">This secret has no keys.</p>
        ) : (
          <dl className="space-y-1" data-testid="resource-data-keys">
            {keys.map((k) => (
              <div key={k.key} className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
                <dt className="break-all font-mono text-xs text-[#3a5770] dark:text-gray-400">
                  {k.key}
                  {/* P2-C2: the store pointer this key's value comes from — a location, not a value. */}
                  {k.path && <span className="text-[#8098ac] dark:text-gray-500"> ← {k.path}</span>}
                </dt>
                <dd className="shrink-0 text-xs">
                  {k.present === false ? (
                    <span className="text-amber-700 dark:text-amber-400">not on the cluster</span>
                  ) : (
                    <span className="font-mono text-[#8098ac] dark:text-gray-500">••••••••</span>
                  )}
                </dd>
              </div>
            ))}
          </dl>
        )}
        <p className="mt-2 text-xs text-[#5c7288] dark:text-gray-500">
          Sharko blanks every value on the server. The values never leave the cluster.
        </p>
      </div>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// Detail side panel — rebuilt around the RESOURCE (P3-F2), the way ArgoCD
// lays out one, top to bottom:
//
//   1. the resource header: kind, the secret's own name, its cluster, its age
//   2. state, with Refresh and Sync right next to it — the two things a
//      reader who just opened this panel is most likely to want
//   3. the diff: two cards and one sentence (see diffVerdictFor)
//   4. the key table: key, where its value comes from, on the cluster or not
//   5. everything else the panel already carried — purpose, source,
//      timestamps, repair detail, the warnings, the link out
//
// Before this, the live "On the cluster right now" view was second-to-last,
// below the Diff, outside the role guard (so a viewer got a permission
// error where a sentence belonged), fired a doomed read on rows already
// known to be missing, and had no way back from a failed read.
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

  // The same role predicate RoleGuard applies below, read here so the
  // REQUEST is gated too and not just the rendering. A viewer's panel used
  // to fire the read anyway and paint the 403 as an error — a permission
  // dialog dressed up as a fault.
  const auth = useContext(AuthContext)
  const canReadLive = auth?.role === 'admin' || auth?.role === 'operator'
  const { live, retry } = useLiveSecret(row, canReadLive)

  if (!row) return null

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      if (row.kind === 'connection') {
        await reconcileCluster(row.cluster)
        showToast(connectionRefreshToast(row.cluster), 'success')
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

  // The connection-secret label drift — WHICH labels differ, under the
  // verdict sentence that already said THAT they differ. Kept as its own
  // fetch (getClusterComparison: labels only, never credentials).
  //
  // Values rows have no equivalent and must never grow one: a values
  // secret's content must never reach the browser, so "what differs" for
  // one of those rows is answered by the row's own state field and nothing
  // more. If that ever needs a server call, it is a new design decision,
  // not a refactor.
  let driftDetail: ReactNode = null
  if (row.kind === 'connection') {
    const drift = diffData?.cluster?.last_reconcile?.label_drift
    const added = drift?.added ?? []
    const removed = drift?.removed ?? []
    const changed = drift?.changed ?? []
    if (diffError) {
      driftDetail = <p className="text-sm text-red-600 dark:text-red-400">{diffError}</p>
    } else if (diffLoading && row.state === 'out_of_sync') {
      // The waiting line only shows where content is actually expected. On
      // an in-sync row this box has nothing to say once it loads, and a
      // "Loading…" that appears and then vanishes is just a flicker.
      driftDetail = <p className="text-sm text-[#3a5770] dark:text-gray-400">Loading…</p>
    } else if (added.length > 0 || removed.length > 0 || changed.length > 0) {
      driftDetail = (
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
  }

  const verdict = diffVerdictFor(row, live)

  return (
    <ResourceDetailSheet
      open={open}
      onOpenChange={onOpenChange}
      title={identity}
      subtitle={row.kind === 'connection' ? 'Cluster connection secret' : 'Addon values secret'}
      testId="secret-detail-panel"
      // Two cards side by side need the room; every other user of this
      // sheet keeps the narrow default.
      wide
    >
      {/* ── 1. The resource header ─────────────────────────────────────── */}
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1" data-testid="detail-resource-header">
        <span className="rounded-full bg-[#eef4f9] px-2 py-0.5 text-[11px] font-medium text-[#3a5770] dark:bg-gray-800 dark:text-gray-300">
          Secret
        </span>
        <span className="break-all font-mono text-sm font-semibold text-[#08192b] dark:text-white">{identity}</span>
        <span className="text-xs text-[#5c7288] dark:text-gray-500">on {row.cluster}</span>
        {/* The age is a live-read fact, so it appears once the read lands
            and stays absent otherwise — never an invented one. */}
        {live.status === 'ready' && live.resource.created_at && (
          <span className="text-xs text-[#5c7288] dark:text-gray-500">
            created <TimeChip iso={live.resource.created_at} />
          </span>
        )}
      </div>

      {/* ── 2. State, with the two actions right beside it ─────────────── */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg ring-1 ring-[#d7e2ea] bg-white p-3 dark:ring-gray-700 dark:bg-gray-800">
        <StatusMark status={row.state} />
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
      </div>

      {/* ── 3. The diff: one sentence, two cards ───────────────────────── */}
      <div>
        <h3 className="mb-1 text-sm font-semibold text-[#08192b] dark:text-gray-100">Diff</h3>
        <p
          className={`mb-2 text-sm ${verdict === 'match' ? 'text-green-700 dark:text-green-400' : 'text-[#08192b] dark:text-gray-200'}`}
          data-testid="diff-verdict"
        >
          {verdict === 'match' && <CheckCircle className="mr-1.5 inline h-4 w-4 align-text-bottom" aria-hidden="true" />}
          {diffVerdictSentence(verdict, row)}
        </p>
        <div className="grid gap-3 sm:grid-cols-2">
          <IntentCard row={row} />
          {/* The live half sits INSIDE the role guard, so a viewer sees a
              sentence about access rather than a permission error where a
              card should be — and the read is never fired for them either
              (useLiveSecret's `allowed`). */}
          <RoleGuard
            roles={['admin', 'operator']}
            fallback={
              <DiffCard title="What is on the cluster" testId="diff-live-card">
                <p className="text-sm text-[#3a5770] dark:text-gray-400" data-testid="live-needs-operator">
                  {LIVE_READ_NEEDS_OPERATOR}
                </p>
              </DiffCard>
            }
          >
            <LiveCard row={row} live={live} onRetry={retry} />
          </RoleGuard>
        </div>
        {driftDetail && (
          <div
            className="mt-3 rounded-md ring-1 ring-[#d7e2ea] bg-white p-3 dark:ring-gray-700 dark:bg-gray-900"
            data-testid="detail-diff-panel"
          >
            {driftDetail}
          </div>
        )}
      </div>

      {/* ── 4. The key table ───────────────────────────────────────────── */}
      <RoleGuard roles={['admin', 'operator']}>
        <KeyTable live={live} />
      </RoleGuard>

      {/* ── 5. Everything the panel already carried ────────────────────── */}
      <p className="text-sm text-[#08192b] dark:text-white">{purposeSentence}</p>
      <p
        className="text-xs text-[#5c7288] dark:text-gray-500"
        title={row.kind === 'values' ? `Git only holds a pointer to it — the value itself lives in ${row.sourceLabel}.` : undefined}
      >
        {sourceSentence}
      </p>
      {/* P2-C1: which commit and which file this row was compared against — short SHA here, full SHA on hover. Connection rows only; values rows have no commit to point at (their intent is the store, C2 covers that instead). */}
      {row.kind === 'connection' && row.comparedRevision && (
        <p className="text-xs text-[#5c7288] dark:text-gray-500" title={`Full commit: ${row.comparedRevision}`} data-testid="detail-compared-revision">
          Compared against git <span className="font-mono">{row.comparedRevision.slice(0, 7)}</span>
          {row.comparedPath && (
            <>
              {' '}
              · <span className="font-mono">{row.comparedPath}</span>
            </>
          )}
        </p>
      )}

      {/* P2-C3: the one-line self-heal promise — only where it changes what the reader should do (an out-of-sync or missing row). */}
      {(row.state === 'out_of_sync' || row.state === 'missing') && (
        <p className="text-xs text-[#5c7288] dark:text-gray-500" data-testid="detail-self-heals">
          {row.selfHeals ? 'Sharko will fix this on the next pass.' : 'Waiting for Sync.'}
        </p>
      )}
      {/* P2-C6: drift blame — which side moved. Connection rows, out-of-sync only, and only when both revisions are known. */}
      {row.kind === 'connection' && row.state === 'out_of_sync' && row.driftSource && (
        <p className="text-xs text-[#5c7288] dark:text-gray-500" data-testid="detail-drift-source">
          {row.driftSource === 'git'
            ? 'Git moved — a newer commit changed what this secret should be.'
            : 'The cluster moved — something changed this secret outside git.'}
        </p>
      )}

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

      {row.kind === 'connection' && (row.fightCount ?? 0) >= ROW_WARNING_THRESHOLD && (
        <p className="text-sm text-amber-700 dark:text-amber-400" data-testid="fight-warning">
          {fightWarningSentence(row.fightCount ?? 0)}
        </p>
      )}
      {row.kind === 'values' && (row.consecutiveFailures ?? 0) >= ROW_WARNING_THRESHOLD && (
        <p className="text-sm text-amber-700 dark:text-amber-400" data-testid="consecutive-failures-warning">
          {consecutiveFailuresSentence(row.consecutiveFailures ?? 0)}
        </p>
      )}

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
// One secret row. Hoisted out of the page so the flat list and the grouped
// list render the exact same markup — a second copy would drift, and the
// two views would quietly start disagreeing about what a row says.
// ─────────────────────────────────────────────────────────────────────────────

function SecretTableRow({
  row,
  indented,
  busy,
  onSelect,
  onRefresh,
  onRequestSync,
}: {
  row: UnifiedRow
  /** true when the row sits under a group parent — a small left inset, nothing else changes. */
  indented?: boolean
  busy: boolean
  onSelect: () => void
  onRefresh: () => void
  onRequestSync: () => void
}) {
  // P3-F2: the row opens the panel, so it has to BE a control — reachable
  // by Tab, opened by Enter or Space, and announced as a button. Before
  // this it was a click-only <tr>, which a keyboard reader could see and
  // never open.
  const openOnKey = (e: React.KeyboardEvent<HTMLTableRowElement>) => {
    if (e.key !== 'Enter' && e.key !== ' ') return
    e.preventDefault() // Space would otherwise scroll the page
    onSelect()
  }

  return (
    <TableRow
      data-testid={`secret-row-${row.key}`}
      onClick={onSelect}
      onKeyDown={openOnKey}
      tabIndex={0}
      role="button"
      aria-label={`Open ${row.secretNamespace && row.secretName ? `${row.secretNamespace}/${row.secretName}` : row.cluster}`}
      className="cursor-pointer focus:outline-none focus-visible:ring-2 focus-visible:ring-[#1a3d5c] dark:focus-visible:ring-teal-400"
    >
      {/* H6: the identity is the darkest, boldest text on the row. The
          small grey line under it carries the S3 honesty lock (kind +
          source) — this replaces the old separate COMPARED AGAINST
          column, which said the same thing two columns away from the
          kind glyph. The source now comes from the ROW (G1), not from
          one label for the whole page.

          The status edge strip (copied from ArgoCD's own list and tile
          views) lives on this same cell, as a left border — a `<td>`
          always wins the collapsed-border fight for its own edge, so it
          renders reliably regardless of the table's border-collapse
          mode. Its colour is read off the exact same STATUS_META table
          as the row's own <StatusMark> dot and the filter chips, via
          statusStripClassName — it cannot disagree with the dot two
          columns over. */}
      <TableCell className={`py-1.5 ${statusStripClassName(row.state)} ${indented ? 'pl-6' : ''}`}>
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
        Cluster: connection rows leave this blank ON PURPOSE — the
        identity above (namespace/secretName) already IS the cluster name
        (the same fact printed twice, side by side, was the exact
        duplicate flagged in review). Values rows show it here because
        it's the one thing that actually distinguishes two rows with the
        same addon on different clusters — not a duplicate there.
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
            actions={actionsForRow(row, { busy, onRefresh, onRequestSync })}
          />
        </RoleGuard>
      </TableCell>
    </TableRow>
  )
}

// ─────────────────────────────────────────────────────────────────────────────
// The collapsible group parent (G2/G3). Same interaction as
// AddonVersionList: click the line, the children appear under it.
//
// The right-hand summary is groupSummary's output and nothing else — see
// that function for what a header is and is not allowed to say.
// ─────────────────────────────────────────────────────────────────────────────

function GroupHeaderRow({ group, expanded, onToggle }: { group: RowGroup; expanded: boolean; onToggle: () => void }) {
  return (
    <TableRow className="hover:bg-transparent">
      <TableCell colSpan={5} className="p-0">
        <button
          type="button"
          onClick={onToggle}
          aria-expanded={expanded}
          data-testid={`secret-group-${group.key}`}
          className="flex w-full flex-wrap items-center justify-between gap-2 bg-[#f2f6f9] px-3 py-2 text-left hover:bg-[#e8eff5] dark:bg-gray-800/60 dark:hover:bg-gray-800"
        >
          <span className="flex items-center gap-1.5">
            {expanded ? (
              <ChevronUp className="h-4 w-4 shrink-0 text-[#5c7288] dark:text-gray-400" aria-hidden="true" />
            ) : (
              <ChevronDown className="h-4 w-4 shrink-0 text-[#5c7288] dark:text-gray-400" aria-hidden="true" />
            )}
            <span className="font-semibold text-[#08192b] dark:text-gray-100">{group.label}</span>
            <span className="text-[11px] text-[#8098ac] dark:text-gray-500">{group.sublabel}</span>
          </span>
          <span className="text-xs text-[#3a5770] dark:text-gray-400" data-testid={`secret-group-summary-${group.key}`}>
            {groupSummary(group.rows)}
          </span>
        </button>
      </TableCell>
    </TableRow>
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

  // G2 — "Group by" lives in the URL too (?group=addon|cluster), for the
  // same reasons the chip filter and the search text do: reloadable,
  // bookmarkable, back-button-safe. `none` is the default and is never
  // written to the URL.
  const [groupBy, setGroupByState] = useState<GroupBy>(() => {
    const v = searchParams.get('group')
    return v === 'addon' || v === 'cluster' ? v : 'none'
  })
  const setGroupBy = useCallback(
    (next: GroupBy) => {
      setGroupByState(next)
      updateParams((p) => {
        if (next === 'none') p.delete('group')
        else p.set('group', next)
      })
    },
    [updateParams],
  )
  // Which group parents are open. Collapsed by default — the same
  // interaction AddonVersionList uses, which the maintainer already knows.
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({})
  const toggleGroup = useCallback((key: string) => {
    setExpandedGroups((g) => ({ ...g, [key]: !g[key] }))
  }, [])

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
    const c: Record<ResourceStatus, number> = { in_sync: 0, out_of_sync: 0, missing: 0, foreign: 0, unknown: 0 }
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

  // G2 — grouping. `none` is the default and is today's flat list,
  // unchanged. Grouped, the page pages over GROUPS rather than rows, so a
  // group is never split in half across a page boundary.
  const groups = useMemo(() => buildRowGroups(sorted, groupBy), [sorted, groupBy])

  useEffect(() => {
    setPage(1)
  }, [search, stateFilter, sortKey, sortDir, pageSize, groupBy])

  const grouped = groupBy !== 'none'
  const pageUnitCount = grouped ? groups.length : sorted.length
  const totalPages = Math.max(1, Math.ceil(pageUnitCount / pageSize))
  const clampedPage = Math.min(page, totalPages)
  const paged = useMemo(() => sorted.slice((clampedPage - 1) * pageSize, clampedPage * pageSize), [sorted, clampedPage, pageSize])
  const pagedGroups = useMemo(
    () => groups.slice((clampedPage - 1) * pageSize, clampedPage * pageSize),
    [groups, clampedPage, pageSize],
  )

  // B2 — an honest "Showing X–Y of Z" line: never "169 of 169 shown" while
  // 20 rows are on screen, and plain about it when a filter has narrowed
  // the total below everything Sharko manages. Grouped, it counts groups
  // and SAYS it counts groups — "1–5 of 12" with no noun would be exactly
  // the kind of quietly-wrong number this page keeps refusing to print.
  const hasActiveFilter = stateFilter !== 'all' || search.trim() !== ''
  const unit = grouped ? (groupBy === 'addon' ? 'addons' : 'clusters') : ''
  const rangeStart = pageUnitCount === 0 ? 0 : (clampedPage - 1) * pageSize + 1
  const rangeEnd = Math.min(clampedPage * pageSize, pageUnitCount)
  const paginationSummary =
    pageUnitCount === 0
      ? hasActiveFilter
        ? `No secrets match this filter (${unifiedRows.length} total)`
        : 'No secrets'
      : grouped
        ? hasActiveFilter
          ? `Showing ${rangeStart}–${rangeEnd} of ${pageUnitCount} ${unit}, ${sorted.length} secrets (filtered from ${unifiedRows.length})`
          : `Showing ${rangeStart}–${rangeEnd} of ${pageUnitCount} ${unit}, ${sorted.length} secrets`
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
          showToast(connectionRefreshToast(row.cluster), 'success')
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

  // Refresh all drives the CHECK on both engines and writes nothing (P1-A
  // A3). Values secrets go to POST /secrets/check — NOT
  // triggerSecretsReconcile, which runs the pass that creates and rotates
  // secrets; a button labelled "Refresh" must never do that. Connection
  // secrets go to POST /clusters/{name}/reconcile, which is now the
  // read-only check and covers every cluster despite taking one name in its
  // path (see internal/api/clusters_reconcile.go), so one call with any
  // connection row's cluster checks them all.
  const handleRefreshAll = async () => {
    setRefreshingAll(true)
    try {
      const tasks: Promise<unknown>[] = [checkAllAddonValuesSecrets()]
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
          <EngineStat label="Addon values" kind="values" info={data?.engines.addon_values} onErrorClick={filterToCluster} />
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
            <InfoHint text={REFRESH_ALL_HINT} label="What does Refresh all do?" />
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
        {/* G2 — Group by. `None` is the default and is the flat list this
            page has always shown; the other two fold the same rows under a
            parent line you click to open. */}
        <div className="flex items-center gap-1.5">
          <span className="text-xs text-[#5c7288] dark:text-gray-400">Group by</span>
          <div className="inline-flex overflow-hidden rounded-lg ring-1 ring-[#d7e2ea] dark:ring-gray-700">
            {(
              [
                ['none', 'None'],
                ['addon', 'Addon'],
                ['cluster', 'Cluster'],
              ] as [GroupBy, string][]
            ).map(([value, label]) => (
              <button
                key={value}
                type="button"
                onClick={() => setGroupBy(value)}
                aria-pressed={groupBy === value}
                data-testid={`group-by-${value}`}
                className={`px-2.5 py-1 text-xs font-medium ${
                  groupBy === value
                    ? 'bg-[#1a3d5c] text-white dark:bg-teal-700'
                    : 'bg-white text-[#3a5770] hover:bg-[#f2f6f9] dark:bg-gray-800 dark:text-gray-300 dark:hover:bg-gray-700'
                }`}
              >
                {label}
              </button>
            ))}
          </div>
        </div>
        <div className="ml-auto">
          <PageSizeSelector pageSize={pageSize} onChange={setPageSize} sizes={[10, 20, 50, 100]} />
        </div>
      </div>

      {loading ? (
        <div className="flex items-center justify-center py-24">
          <div className="h-8 w-8 animate-spin rounded-full border-4 border-[#d7e2ea] border-t-[#1a3d5c] dark:border-gray-700 dark:border-t-teal-500" />
        </div>
      ) : sorted.length === 0 ? (
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
              {grouped
                ? pagedGroups.map((group) => (
                    <Fragment key={group.key}>
                      <GroupHeaderRow
                        group={group}
                        expanded={!!expandedGroups[group.key]}
                        onToggle={() => toggleGroup(group.key)}
                      />
                      {expandedGroups[group.key] &&
                        group.rows.map((row) => (
                          <SecretTableRow
                            key={row.key}
                            row={row}
                            indented
                            busy={!!busyRows[row.key]}
                            onSelect={() => selectRow(row.key)}
                            onRefresh={() => handleRefreshRow(row)}
                            onRequestSync={() => setSyncTarget(row)}
                          />
                        ))}
                    </Fragment>
                  ))
                : paged.map((row) => (
                    <SecretTableRow
                      key={row.key}
                      row={row}
                      busy={!!busyRows[row.key]}
                      onSelect={() => selectRow(row.key)}
                      onRefresh={() => handleRefreshRow(row)}
                      onRequestSync={() => setSyncTarget(row)}
                    />
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
