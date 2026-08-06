// StatusMark — the house "does this resource match its source" indicator
// (S1.1). Built for the Managed Secrets rebuild but written to be adopted
// by any later resource list (the cluster page's managed-secret panel, the
// addon rows) that has this same one-question shape: does the live thing
// match where it's supposed to come from?
//
// Exactly five states, exact words — never invent a sixth:
//   in_sync      "In sync"            — matches its source right now
//   out_of_sync  "Out of sync"        — checked, and it does NOT match
//   missing      "Not on the cluster" — checked, and there's nothing there
//   foreign      "Foreign"            — something IS there, and Sharko did
//                                        not create it, so Sharko leaves it
//                                        alone
//   unknown      "Not checked yet"    — Sharko has no answer at all
//
// H3 word pass (gitops-proud P4-H): "missing" used to read "Missing" — a
// one-word label that answers a different question than the one a reader
// actually has ("missing FROM WHERE?"). "Not on the cluster" says the same
// fact the per-key presence flag already used (see KeyTable in
// ManagedSecrets.tsx) — one word for one fact, used consistently.
//
// "Foreign" is deliberately NOT red and NOT amber. Nothing is broken and
// nothing needs fixing — somebody else owns that secret, and Sharko staying
// off it is the system working correctly. It gets a neutral slate dot: its
// own thing, calm, and impossible to mistake for damage.
//
// Two rules that matter more than they look like they do:
//
//  1. "Not checked yet" gets a STILL mark (a hollow circle outline), never
//     a spinner. A spinner reads as "working on it right now" — that is
//     not what "nobody has looked yet" means, and showing one would be
//     lying about what Sharko is doing.
//  2. "Not checked yet" is never grey-that-reads-as-fine sitting next to
//     green/amber/red — it gets its own muted-but-distinct blue-grey ring
//     with proper light AND dark variants.
//
// House-style pass (maintainer's adopted design review, "shaped like
// ArgoCD but painted like the opposite of ArgoCD"):
//
//  - H2: colour lives ONLY on the small dot below — the word next to it is
//    always plain dark ink, never a colour. Twenty amber words filling a
//    screen was the bug; ArgoCD's own "Healthy"/"Synced" are black text
//    next to a small coloured mark, and this rule keeps the same honesty
//    ("state has a word, not just a colour") without the colour-noise.
//  - H3: ONE dot shape (a filled circle) for all three "checked" states —
//    only the fill colour and the mark inside change (check / "!" / cross).
//    "Not checked yet" stays its own shape (a hollow ring) ON PURPOSE — it
//    must never be mistaken for a filled, checked state at a glance.

import { Check, Minus, X, type LucideIcon } from 'lucide-react'

export type ResourceStatus = 'in_sync' | 'out_of_sync' | 'missing' | 'foreign' | 'unknown'

interface StatusMeta {
  label: string
  /** Fill colour for the dot — in_sync/out_of_sync/missing only (unknown is hollow, no fill). */
  dotClassName: string
  /**
   * Border colour for the row edge strip (S3) — the SAME colour as the dot,
   * just as a `border-*` class instead of `bg-*`, so the strip can never
   * drift out of sync with the dot and the filter chip (they all read off
   * this one table).
   */
  stripClassName: string
  /** The small mark drawn inside the filled dot — absent for the hollow "unknown" ring. */
  mark?: LucideIcon
  /** true = a hollow/outline ring (rule 1 above) — never fill or spin this one. */
  hollow?: boolean
}

const STATUS_META: Record<ResourceStatus, StatusMeta> = {
  in_sync: {
    label: 'In sync',
    dotClassName: 'bg-green-600 dark:bg-green-500',
    stripClassName: 'border-green-600 dark:border-green-500',
    mark: Check,
  },
  out_of_sync: {
    label: 'Out of sync',
    dotClassName: 'bg-amber-600 dark:bg-amber-500',
    stripClassName: 'border-amber-600 dark:border-amber-500',
    mark: undefined, // exclamation mark is drawn as text — see StatusDot
  },
  missing: {
    label: 'Not on the cluster',
    dotClassName: 'bg-red-600 dark:bg-red-500',
    stripClassName: 'border-red-600 dark:border-red-500',
    mark: X,
  },
  foreign: {
    label: 'Foreign',
    // Neutral slate, on purpose — see the note at the top of this file.
    // Filled (something IS there, Sharko looked and knows what) with a dash
    // inside (Sharko does nothing here).
    dotClassName: 'bg-slate-500 dark:bg-slate-400',
    stripClassName: 'border-slate-500 dark:border-slate-400',
    mark: Minus,
  },
  unknown: {
    label: 'Not checked yet',
    dotClassName: '',
    // Same blue-grey as the hollow ring (line ~115 below) — "not checked
    // yet" reads as its own distinct thing everywhere it shows up, not as
    // a fainter version of "in sync".
    stripClassName: 'border-[#5a8aaa] dark:border-gray-500',
    hollow: true,
  },
}

/** Normalizes any string the server sends into one of the four known states — an unrecognized value reads as "not checked yet" rather than crashing or rendering nothing. */
export function toResourceStatus(state: string): ResourceStatus {
  return state in STATUS_META ? (state as ResourceStatus) : 'unknown'
}

export function statusLabel(state: string): string {
  return STATUS_META[toResourceStatus(state)].label
}

/**
 * Worst-first sort rank (S3, reordered for G3 — gitops-proud P4-G): missing
 * first, then out_of_sync, then foreign, then a FAILED check, then a
 * genuinely never-checked row, in_sync last. Ascending sort on this rank is
 * "problems float to the top", matching ArgoCD's own status-priority sort —
 * NEVER sort states alphabetically (that buries "out_of_sync" between
 * "in_sync" and "missing").
 *
 * G3's honesty call: "missing" now outranks "out_of_sync" — nothing exists
 * to Sync onto yet, which is a harder stop than a wrong value Sync can
 * simply overwrite. "Foreign" keeps its P1-A spot right after out_of_sync —
 * a boundary worth knowing about, but not damage Sharko can fix — and this
 * lane found no evidence in the old table (out_of_sync, missing, foreign,
 * unknown, in_sync) for a stronger claim than "adjacent to the two real
 * comparisons, ahead of the two 'Sharko doesn't know' cases", so it keeps
 * that same relative seat.
 *
 * "unknown" used to be one bucket. It still is ONE STATE the server sends,
 * but it is no longer one RANK: a row whose last check genuinely FAILED
 * (hasCheckError) is a nearer, more actionable "Sharko doesn't know" than a
 * row that has simply never been looked at — so it sorts one slot higher.
 * Both stay the exact same status word, dot, and strip colour; only the
 * ORDER changes. Pass the row's own `!!lastCheckError` — omitting it always
 * reads as "not checked yet", the safe default when the caller has no
 * opinion.
 */
const STATUS_RANK: Record<ResourceStatus, number> = {
  missing: 0,
  out_of_sync: 1,
  foreign: 2,
  // "unknown" WITHOUT a check error ("not checked yet") sits here — a
  // FAILED check (see CHECK_FAILED_RANK below) outranks it by one slot.
  unknown: 4,
  in_sync: 5,
}

/** Where a FAILED check ("Sharko looked and the check itself broke") ranks — one slot ahead of a genuinely never-checked row, both still the "unknown" status word. */
const CHECK_FAILED_RANK = 3

export function statusSortRank(state: string, hasCheckError = false): number {
  const s = toResourceStatus(state)
  if (s === 'unknown' && hasCheckError) return CHECK_FAILED_RANK
  return STATUS_RANK[s]
}

/**
 * statusStripClassName (S3) — the thin coloured strip on the left edge of a
 * resource row, copied from ArgoCD's own list and tile views. Returns the
 * full className including the 3px width, so a row/cell can just spread it
 * in. The colour is read off the same STATUS_META table as <StatusDot> and
 * the filter chips, so the strip, the dot, and the chip can never disagree
 * about what colour a state is. Meant to work on any `<td>`/row-shaped
 * element that already sits at the left edge of its list — apply it to the
 * first cell of a row, not the row's own background, so it survives
 * table border-collapse without extra CSS.
 */
export function statusStripClassName(state: string): string {
  return `border-l-[3px] ${STATUS_META[toResourceStatus(state)].stripClassName}`
}

/**
 * StatusDot — H3's one dot shape. A filled circle whose fill colour and
 * inner mark carry the state; "unknown" is a hollow ring instead of a
 * filled circle so it can never be mistaken for a checked, healthy state at
 * a glance (still, never a spinner — rule 1 above).
 */
export function StatusDot({ status, className }: { status: ResourceStatus; className?: string }) {
  const meta = STATUS_META[status]
  if (meta.hollow) {
    return (
      <span
        aria-hidden="true"
        data-testid="status-dot"
        data-status={status}
        data-hollow="true"
        className={`inline-block h-3 w-3 shrink-0 rounded-full border-2 border-[#5a8aaa] dark:border-gray-500 ${className ?? ''}`}
      />
    )
  }
  const Mark = meta.mark
  return (
    <span
      aria-hidden="true"
      data-testid="status-dot"
      data-status={status}
      data-hollow="false"
      className={`inline-flex h-3 w-3 shrink-0 items-center justify-center rounded-full ${meta.dotClassName} ${className ?? ''}`}
    >
      {Mark ? (
        <Mark className="h-2 w-2 text-white" strokeWidth={4} />
      ) : (
        // out_of_sync's "!" — text, not an icon: matches ArgoCD's own
        // exclamation-in-a-dot degraded mark without adding a lucide
        // triangle (a triangle would break the one-shape rule).
        <span className="text-[8px] font-bold leading-none text-white">!</span>
      )}
    </span>
  )
}

export function StatusMark({ status, className }: { status: string; className?: string }) {
  const s = toResourceStatus(status)
  const meta = STATUS_META[s]
  return (
    <span
      data-testid="status-mark"
      data-status={s}
      // H2: this classname is the ONLY colour carrier that ever changes
      // per-status on the WORD — it doesn't, on purpose. text-[#0a3a5a] /
      // dark:text-gray-200 is plain dark ink for all four states; colour
      // lives on the <StatusDot> glyph only.
      className={`inline-flex items-center gap-1.5 whitespace-nowrap text-sm font-medium text-[#0a3a5a] dark:text-gray-200 ${className ?? ''}`}
    >
      <StatusDot status={s} />
      {meta.label}
    </span>
  )
}

export default StatusMark
