// SecretTiles v2 — one BOX per SECRET, ArgoCD's own applications-grid
// mental model. Supersedes the addon-summary-card tiles from the earlier
// visual pass (one card per addon, click narrowed the list). Moran, after
// seeing that version: "in argocd each resource or application are
// represented as a box, and when u click on it you get all the stats. i
// want going in my mind for something similar."
//
// A box carries exactly what a list row carries, stacked instead of
// columned: the kind glyph (KeyRound for a connection secret, Lock for a
// values secret) + the secret NAME (truncate + full name in the hover
// title — a box doesn't fight five neighbour columns for width the way the
// list's NAME cell does, so no front-truncate trick is needed here), the
// same <StatusMark> word+dot the list row shows, and a small muted
// addon/cluster line — "—" for a connection row's addon, the same dash
// rule the list's cell-addon column already uses. The colored left-edge
// strip reads off statusStripClassName, the SAME STATUS_META table the
// list row's own strip and dot use, so a box can never disagree with the
// row it stands for.
//
// Clicking a box calls onRowClick with THAT box's row — ManagedSecrets
// hands both the list rows and these boxes the exact same handler
// (`(row) => selectRow(row.key)`), so a box opens the identical detail
// panel a list row opens. No new panel code, no navigation, no filter
// jump — the old card's "switch back to list, narrowed" click-through is
// gone; that behavior belonged to a card that stood for a whole addon; a
// box stands for one secret and just opens it.
//
// Sections: boxes render under the SAME group-by control the list view
// uses. ManagedSecrets builds `groups` once (buildRowGroups(sorted,
// groupBy)) and hands it to both views — group-by "None" makes
// buildRowGroups return an empty array by its own rule, so this component
// falls back to one flat grid of `rows` with no headings, pure
// applications-grid style. When groups is non-empty, each one gets a
// heading carrying what the old per-addon card carried on its own: the
// group label + sublabel, groupSummary()'s plain-sums line (G3 — no
// verdicts, no percentages, no group-level time), and the worst-first
// status-mix bar (statusBarClassName) — unchanged, just moved from "the
// whole card" to "the heading above a row of boxes."

import { KeyRound, Lock } from 'lucide-react'
import { StatusDot, StatusMark, statusBarClassName, statusStripClassName } from '@/components/resource/StatusMark'
import { CHIP_ORDER, connectionRowLabel, groupSummary, rowStatus, type RowGroup, type UnifiedRow } from './ManagedSecrets'

// ~4-6 boxes per row at 1280px — a box carries one secret, not a whole
// addon, so it's meant to sit noticeably smaller than the old addon card.
const GRID_CLASS = 'grid gap-3 sm:grid-cols-2 lg:grid-cols-4 2xl:grid-cols-6'

function SecretBox({ row, onClick }: { row: UnifiedRow; onClick: () => void }) {
  const name = row.secretName ?? '—'
  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={`secret-box-${row.key}`}
      className={`text-left rounded-lg ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm p-3 ${statusStripClassName(rowStatus(row))} hover:bg-[#e0f0ff] focus-visible:ring-[#1a3d5c] dark:ring-gray-700 dark:bg-gray-800 dark:hover:bg-gray-750`}
    >
      <div className="flex min-w-0 items-center gap-1.5">
        {row.kind === 'connection' ? (
          <KeyRound className="h-3.5 w-3.5 shrink-0 text-[#5a8aaa]" aria-hidden="true" />
        ) : (
          <Lock className="h-3.5 w-3.5 shrink-0 text-[#5a8aaa]" aria-hidden="true" />
        )}
        <span className="min-w-0 flex-1 truncate text-sm font-semibold text-[#0a2a4a] dark:text-gray-100" title={name}>
          {name}
        </span>
      </div>
      {/* B5: a connection box shows the SERVER's headline, the same string
          the list row and the connection page show. A values box keeps
          <StatusMark>. The strip and dot read rowStatus(), so the colour can
          never be greener than the word. */}
      <div className="mt-1.5">
        {row.kind === 'connection' ? (
          <span
            data-testid="status-mark"
            data-status={rowStatus(row)}
            className="inline-flex min-w-0 items-center gap-1.5 text-sm font-medium text-[#0a3a5a] dark:text-gray-200"
            title={row.qualifier ? `${connectionRowLabel(row)} — ${row.qualifier}` : connectionRowLabel(row)}
          >
            <StatusDot status={rowStatus(row)} />
            <span className="truncate">{connectionRowLabel(row)}</span>
          </span>
        ) : (
          <StatusMark status={row.state} />
        )}
      </div>
      {/* The addon/cluster line — same dash rule the list's cell-addon
          column uses ("—" on a connection row, it isn't an addon secret). */}
      <p className="mt-1.5 truncate text-[11px] text-[#5a8aaa] dark:text-gray-500">
        {row.kind === 'values' ? (row.addon ?? '—') : '—'} · {row.cluster}
      </p>
    </button>
  )
}

/**
 * TileSection — a group-by heading plus the grid of boxes under it. The
 * heading is what the old per-addon card used to carry on its own: label +
 * sublabel, groupSummary()'s plain-sums line, and the worst-first
 * status-mix bar. Both come from the exact same functions the list view's
 * GroupHeaderRow already calls, so a heading here can never say something
 * different than the equivalent group header in list view would.
 */
function TileSection({ group, onRowClick }: { group: RowGroup; onRowClick: (row: UnifiedRow) => void }) {
  const total = group.rows.length

  const counts: Partial<Record<string, number>> = {}
  for (const row of group.rows) {
    const s = rowStatus(row)
    counts[s] = (counts[s] ?? 0) + 1
  }

  return (
    <section data-testid={`tile-section-${group.key}`}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <span className="flex items-baseline gap-1.5">
          <span className="font-semibold text-[#0a2a4a] dark:text-gray-100">{group.label}</span>
          <span className="text-[11px] text-[#5a8aaa] dark:text-gray-500">{group.sublabel}</span>
        </span>
        <span className="text-xs text-[#2a5a7a] dark:text-gray-400" data-testid={`tile-section-summary-${group.key}`}>
          {groupSummary(group.rows)}
        </span>
      </div>
      {/* The status-mix bar: one segment per state PRESENT, worst-first
          (CHIP_ORDER), width proportional to the group's own row count —
          identical rule the old per-addon card's bar used. min-w-[6px]
          keeps a 1-of-many segment visible instead of vanishing to a
          sliver. Reads statusBarClassName off the SAME STATUS_META fill
          table the box's own strip/dot and the filter chips all use. */}
      <div className="mt-1.5 flex h-1.5 w-full overflow-hidden rounded-full" data-testid={`tile-bar-${group.key}`}>
        {CHIP_ORDER.filter((status) => (counts[status] ?? 0) > 0).map((status) => (
          <div
            key={status}
            data-testid={`tile-bar-segment-${group.key}-${status}`}
            className={`h-full min-w-[6px] ${statusBarClassName(status)}`}
            style={{ width: `${((counts[status] ?? 0) / total) * 100}%` }}
          />
        ))}
      </div>
      <div className={`mt-2 ${GRID_CLASS}`}>
        {group.rows.map((row) => (
          <SecretBox key={row.key} row={row} onClick={() => onRowClick(row)} />
        ))}
      </div>
    </section>
  )
}

export function SecretTiles({
  rows,
  groups,
  onRowClick,
}: {
  /** The already filtered-and-sorted rows — used directly for the flat grid when group-by is "None". */
  rows: UnifiedRow[]
  /** The SAME groups the list view's Group by builds (empty array when group-by is "None" — buildRowGroups' own rule). */
  groups: RowGroup[]
  /** Reused verbatim from list view — the exact handler that opens the detail panel for a row. */
  onRowClick: (row: UnifiedRow) => void
}) {
  if (groups.length === 0) {
    return (
      <div className={GRID_CLASS} data-testid="secret-tiles">
        {rows.map((row) => (
          <SecretBox key={row.key} row={row} onClick={() => onRowClick(row)} />
        ))}
      </div>
    )
  }
  return (
    <div className="space-y-5" data-testid="secret-tiles">
      {groups.map((group) => (
        <TileSection key={group.key} group={group} onRowClick={onRowClick} />
      ))}
    </div>
  )
}

export default SecretTiles
