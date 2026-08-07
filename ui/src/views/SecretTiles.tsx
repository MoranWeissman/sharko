// SecretTiles — the tiles view of Secret Sync (design-secret-sync-visual-
// pass, section 3): "where is view types i asked". One card per addon,
// plus one "Cluster connections" card, built from the exact same buckets
// (buildRowGroups(filtered, 'addon')) the list view's Group by addon
// already uses — no new grouping logic, no new honesty rules. His original
// mental model ("datadog → cluster → secret") starts at the addon; a
// per-cluster grid would have been a wall of 50 near-identical cards in
// the demo estate, so cluster-first reading stays available inside list
// view's own Group by cluster instead.
//
// Per card: a title row (kind glyph + addon/"Cluster connections" name), a
// status-mix bar (one segment per state present, worst-first, width
// proportional to count), and groupSummary()'s own plain-sums line — the
// G3 lock (no verdicts, no percentages, no group-level time) comes free by
// reusing the same function the list view's group headers already call.
// The only colour on the card is the worst-state left-edge strip and the
// bar segments — H2 holds here too: colour lives on marks, words stay ink.

import { KeyRound, Lock } from 'lucide-react'
import { statusBarClassName, statusStripClassName, toResourceStatus } from '@/components/resource/StatusMark'
import { CHIP_ORDER, CONNECTIONS_GROUP_KEY, groupSummary, worstStateInGroup, type RowGroup } from './ManagedSecrets'

function SecretTileCard({ group, onClick }: { group: RowGroup; onClick: () => void }) {
  const isConnections = group.key === CONNECTIONS_GROUP_KEY
  const worst = worstStateInGroup(group.rows)
  const total = group.rows.length

  const counts: Partial<Record<string, number>> = {}
  for (const row of group.rows) {
    const s = toResourceStatus(row.state)
    counts[s] = (counts[s] ?? 0) + 1
  }

  return (
    <button
      type="button"
      onClick={onClick}
      data-testid={`secret-tile-${group.key}`}
      className={`text-left rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] shadow-sm p-4 ${statusStripClassName(worst)} hover:bg-[#e0f0ff] focus-visible:ring-[#1a3d5c] dark:ring-gray-700 dark:bg-gray-800 dark:hover:bg-gray-750`}
    >
      <div className="flex items-center gap-2">
        {isConnections ? (
          <KeyRound className="h-4 w-4 shrink-0 text-[#5a8aaa]" aria-hidden="true" />
        ) : (
          <Lock className="h-4 w-4 shrink-0 text-[#5a8aaa]" aria-hidden="true" />
        )}
        <span className="truncate font-semibold text-[#0a2a4a] dark:text-gray-100" title={group.label}>
          {group.label}
        </span>
      </div>
      {isConnections && <p className="mt-0.5 text-[11px] text-[#5a8aaa] dark:text-gray-500">{group.sublabel}</p>}
      {/* The status-mix bar: one segment per state PRESENT, worst-first
          (CHIP_ORDER), width proportional to the group's own row count.
          min-w-[6px] keeps a 1-of-50 segment visible instead of vanishing
          to a sliver. Reads statusBarClassName off the SAME STATUS_META
          fill table the row dot, the row strip, and the filter chip all
          use — the bar can never disagree with any of them about colour. */}
      <div className="mt-3 flex h-1.5 w-full overflow-hidden rounded-full" data-testid={`tile-bar-${group.key}`}>
        {CHIP_ORDER.filter((status) => (counts[status] ?? 0) > 0).map((status) => (
          <div
            key={status}
            data-testid={`tile-bar-segment-${group.key}-${status}`}
            className={`h-full min-w-[6px] ${statusBarClassName(status)}`}
            style={{ width: `${((counts[status] ?? 0) / total) * 100}%` }}
          />
        ))}
      </div>
      <p className="mt-2 text-xs text-[#2a5a7a] dark:text-gray-400">{groupSummary(group.rows)}</p>
    </button>
  )
}

export function SecretTiles({ groups, onCardClick }: { groups: RowGroup[]; onCardClick: (group: RowGroup) => void }) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4" data-testid="secret-tiles">
      {groups.map((group) => (
        <SecretTileCard key={group.key} group={group} onClick={() => onCardClick(group)} />
      ))}
    </div>
  )
}

export default SecretTiles
