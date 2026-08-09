// StatusMark — the house "does this resource match its source" indicator
// (S1.1). Built for the Managed Secrets rebuild but written to be adopted
// by any later resource list (the cluster page's managed-secret panel, the
// addon rows) that has this same one-question shape: does the live thing
// match where it's supposed to come from?
//
// Six states, exact words — never invent a seventh:
//   in_sync      "Synced"             — matches its source right now
//   out_of_sync  "Out of sync"        — checked, and it does NOT match
//   missing      "Missing"            — checked, and there's nothing there
//   orphaned     "Not in config"      — Sharko wrote this once, but its
//                                        source in git is gone; nothing
//                                        asks for it anymore, and Sharko
//                                        never deletes it on its own
//   foreign      "Managed elsewhere"  — something IS there, and Sharko did
//                                        not create it, so Sharko leaves it
//                                        alone
//   unknown      "Not checked yet"    — Sharko has no answer at all
//
// H3 word pass (gitops-proud P4-H): "missing" used to read "Missing" — a
// one-word label that answers a different question than the one a reader
// actually has ("missing FROM WHERE?"). "Not on the cluster" said the same
// fact the per-key presence flag already used (see KeyTable in
// ManagedSecrets.tsx) — one word for one fact, used consistently.
//
// Walk finding #140 (maintainer-approved, supersedes H3 directly above):
// once NAME and NAMESPACE became their own columns, this row reads like an
// ArgoCD Application list on purpose — and ArgoCD's own status words are
// "Synced" and "Missing", not a sentence fragment squeezed into a status
// column. "In sync" → "Synced", "Not on the cluster" → "Missing". The other
// three words are untouched, and so is the plain-English sentence in the
// detail panel ("This secret was never created on the cluster — Sync
// creates it.") — the short word on the row and the full sentence in the
// panel are still the intended pairing, only the row word changed.
//
// "Managed elsewhere" (state key: foreign) is deliberately NOT red and NOT
// amber. Nothing is broken and nothing needs fixing — somebody else owns
// that secret, and Sharko staying off it is the system working correctly.
// It gets a neutral slate dot: its own thing, calm, and impossible to
// mistake for damage.
//
// leftover-secrets S1.2 adds "orphaned" (label now "Not in config", see the
// 2026-08-09 PM decision in the SSF-8 word pass below) — distinct from
// "foreign": a foreign secret is something Sharko never owned; an orphaned
// secret is something SHARKO WROTE whose source definition someone later
// deleted from git. It gets its own violet dot — not red (nothing is
// broken), not amber (there's no fix Sharko is waiting on), not slate
// (unlike foreign, this one is Sharko's own past delivery, worth calling
// out on its own) — with a Ghost mark, the one state where the mark itself
// carries a hint of what the word already says.
//
// SSF-8 word pass (2026-08-09 PM decision, drawer calm-down): "Orphaned"
// and "Foreign" read as jargon to a reader who isn't already fluent in
// Sharko's own vocabulary — both now say the plain fact instead. "Orphaned"
// → "Not in config" (its source definition isn't in git anymore — that IS
// the fact). "Foreign" → "Managed elsewhere" (something else owns this
// secret, stated plainly). The internal state keys (`orphaned`, `foreign`),
// the dot colours, and the sort rank are UNCHANGED — only the label text a
// reader sees changed.
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
//  - H3: ONE dot shape (a filled circle) for every "checked" state — only
//    the fill colour and the mark inside change (check / "!" / cross /
//    dash / ghost). "Not checked yet" stays its own shape (a hollow ring)
//    ON PURPOSE — it must never be mistaken for a filled, checked state at
//    a glance.

import { Check, Ghost, Minus, X, type LucideIcon } from 'lucide-react'

export type ResourceStatus = 'in_sync' | 'out_of_sync' | 'missing' | 'orphaned' | 'foreign' | 'unknown'

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
    label: 'Synced',
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
    label: 'Missing',
    dotClassName: 'bg-red-600 dark:bg-red-500',
    stripClassName: 'border-red-600 dark:border-red-500',
    mark: X,
  },
  orphaned: {
    label: 'Not in config',
    // Violet/purple, on purpose — distinct from every other fill on this
    // page (green/amber/red/slate). Not red or amber: nothing is broken
    // and there's no fix Sharko is waiting on. Not slate like foreign:
    // unlike a foreign secret (never Sharko's), this one WAS Sharko's own
    // delivery, which is worth its own colour rather than blending into
    // "not my problem" slate.
    dotClassName: 'bg-violet-600 dark:bg-violet-500',
    stripClassName: 'border-violet-600 dark:border-violet-500',
    mark: Ghost,
  },
  foreign: {
    label: 'Managed elsewhere',
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

/** Normalizes any string the server sends into one of the six known states — an unrecognized value reads as "not checked yet" rather than crashing or rendering nothing. */
export function toResourceStatus(state: string): ResourceStatus {
  return state in STATUS_META ? (state as ResourceStatus) : 'unknown'
}

export function statusLabel(state: string): string {
  return STATUS_META[toResourceStatus(state)].label
}

/**
 * Worst-first sort rank (S3, reordered for G3 — gitops-proud P4-G, then
 * again for leftover-secrets S1.2): missing first, then out_of_sync, then
 * orphaned, then foreign, then a FAILED check, then a genuinely
 * never-checked row, in_sync last. Ascending sort on this rank is "problems
 * float to the top", matching ArgoCD's own status-priority sort — NEVER
 * sort states alphabetically (that buries "out_of_sync" between "in_sync"
 * and "missing").
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
 * leftover-secrets S1.2 inserts "orphaned" right after out_of_sync, ahead
 * of foreign: an orphaned row is a real leftover from Sharko's OWN past
 * delivery (worth a look, even though nothing is actively broken), which
 * outranks foreign — a boundary Sharko was never going to cross regardless.
 * Every rank at or after foreign shifted down one slot to make room.
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
  orphaned: 2,
  foreign: 3,
  // "unknown" WITHOUT a check error ("not checked yet") sits here — a
  // FAILED check (see CHECK_FAILED_RANK below) outranks it by one slot.
  unknown: 5,
  in_sync: 6,
}

/** Where a FAILED check ("Sharko looked and the check itself broke") ranks — one slot ahead of a genuinely never-checked row, both still the "unknown" status word. */
const CHECK_FAILED_RANK = 4

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
 * statusBarClassName (design-secret-sync-visual-pass, section 3) — the
 * tiles view's per-card status-mix bar reads off this, the SAME
 * STATUS_META fill each row's own <StatusDot> uses (dotClassName), so the
 * bar segment, the dot, the strip, and the filter chip can never disagree
 * about what colour a state is. "unknown" has no fill of its own (it's a
 * hollow ring everywhere else it appears) — the bar still needs a solid
 * segment colour for it, so this returns the same blue-grey the hollow
 * ring's border uses, as a `bg-*` class.
 */
export function statusBarClassName(state: string): string {
  const s = toResourceStatus(state)
  const meta = STATUS_META[s]
  if (meta.dotClassName) return meta.dotClassName
  // "unknown" — no fill in STATUS_META (the dot is a hollow ring instead).
  // Same blue-grey family as its ring/strip, as a background fill.
  return 'bg-[#5a8aaa] dark:bg-gray-500'
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
