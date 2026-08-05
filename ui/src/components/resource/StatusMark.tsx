// StatusMark — the house "does this resource match its source" indicator
// (S1.1). Built for the Managed Secrets rebuild but written to be adopted
// by any later resource list (the cluster page's managed-secret panel, the
// addon rows) that has this same one-question shape: does the live thing
// match where it's supposed to come from?
//
// Exactly four states, exact words — never invent a fifth:
//   in_sync      "In sync"          — matches its source right now
//   out_of_sync  "Out of sync"      — checked, and it does NOT match
//   missing      "Missing"          — checked, and there's nothing there
//   unknown      "Not checked yet"  — Sharko has no answer at all
//
// Two rules that matter more than they look like they do:
//
//  1. "Not checked yet" gets a STILL mark (a hollow circle outline), never
//     a spinner. A spinner reads as "working on it right now" — that is
//     not what "nobody has looked yet" means, and showing one would be
//     lying about what Sharko is doing.
//  2. "Not checked yet" is never grey-that-reads-as-fine sitting next to
//     green/amber/red — it gets its own muted-but-distinct blue-grey tone
//     with proper light AND dark variants (the bug this fixes: the old
//     STATE_CLASSES.unknown had no dark: variant at all).

import { AlertTriangle, Circle, CheckCircle2, XCircle, type LucideIcon } from 'lucide-react'

export type ResourceStatus = 'in_sync' | 'out_of_sync' | 'missing' | 'unknown'

interface StatusMeta {
  label: string
  icon: LucideIcon
  className: string
  /** true = a hollow/outline glyph (rule 1 above) — never spin this one. */
  hollow?: boolean
}

const STATUS_META: Record<ResourceStatus, StatusMeta> = {
  in_sync: {
    label: 'In sync',
    icon: CheckCircle2,
    className: 'text-green-700 dark:text-green-400',
  },
  out_of_sync: {
    label: 'Out of sync',
    icon: AlertTriangle,
    className: 'text-amber-700 dark:text-amber-400',
  },
  missing: {
    label: 'Missing',
    icon: XCircle,
    className: 'text-red-700 dark:text-red-400',
  },
  unknown: {
    label: 'Not checked yet',
    icon: Circle,
    className: 'text-[#5a7a95] dark:text-gray-400',
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
 * Worst-first sort rank (S3): out_of_sync, then missing, then unknown, then
 * in_sync last. Ascending sort on this rank is "problems float to the top",
 * matching ArgoCD's own status-priority sort — NEVER sort states
 * alphabetically (that buries "out_of_sync" between "in_sync" and
 * "missing").
 */
const STATUS_RANK: Record<ResourceStatus, number> = {
  out_of_sync: 0,
  missing: 1,
  unknown: 2,
  in_sync: 3,
}

export function statusSortRank(state: string): number {
  return STATUS_RANK[toResourceStatus(state)]
}

export function StatusMark({ status, className }: { status: string; className?: string }) {
  const meta = STATUS_META[toResourceStatus(status)]
  const Icon = meta.icon
  return (
    <span
      className={`inline-flex items-center gap-1.5 whitespace-nowrap text-sm font-medium ${meta.className} ${className ?? ''}`}
    >
      <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
      {meta.label}
    </span>
  )
}

export default StatusMark
