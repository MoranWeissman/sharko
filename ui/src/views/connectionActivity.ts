// connectionActivity — Story 3 of the connection-reconciliation epic
// (ruling 6): the connection page's activity feed shows LIFECYCLE
// transitions only, with human titles, and is honest about what it is — a
// feed of what the in-memory audit ring still holds since the server
// started, never a durable history.
//
// The rules, all pinned by tests:
//   - Only events in ACTIVITY_EVENT_TITLES render. Anything else — routine
//     reads (secret_resource_read), check triggers
//     (cluster_connection_secret_check_triggered), page opens, and any
//     event this table does not know — is SKIPPED. An unmapped event never
//     renders as a raw identifier.
//   - The feed's label is exactly "Recent activity since Sharko started" —
//     the same label the audit page carries (both surfaces, ruling 6).
//   - Read-only lifecycle events (a drift episode opening or clearing is a
//     noticing, not a change) carry "No changes made".

import type { AuditEntry } from '@/services/models'

/** The honest feed label — both the connection page and the audit page carry it (ruling 6). */
export const RECENT_ACTIVITY_LABEL = 'Recent activity since Sharko started'

/** The line a read-only lifecycle event carries — Sharko noticed something; nothing was written. */
export const NO_CHANGES_MADE = 'No changes made'

/** How the feed names the reconciler when an entry was not a person's request. */
export const BACKGROUND_RECONCILER_ACTOR = 'Background reconciler'

/** The quiet line when the ring holds no lifecycle events for this cluster yet. */
export const ACTIVITY_EMPTY_SENTENCE = 'Nothing recorded since Sharko started.'

/** How many entries the feed shows at most. */
export const ACTIVITY_FEED_LIMIT = 5

export interface ActivityEventMapping {
  /** The plain-English title the feed renders — never the raw event id. */
  title: string
  /** True for events that record a noticing, not a change. */
  readOnly?: boolean
}

/**
 * The ONLY audit events the feed renders, with their human titles. Growing
 * this table is how a new lifecycle event reaches the feed — there is no
 * fallback path that would render a raw identifier.
 */
export const ACTIVITY_EVENT_TITLES: Record<string, ActivityEventMapping> = {
  cluster_secret_create: { title: 'Connection Secret created' },
  cluster_secret_delete: { title: 'Connection Secret deleted' },
  cluster_secret_managed_self_heal: { title: 'Addon labels self-healed' },
  cluster_secret_user_label_sync: { title: 'Addon labels synced' },
  cluster_connection_repair: { title: 'Connection repaired' },
  cluster_connection_repair_requested: { title: 'Connection repair requested' },
  connection_credential_drift_detected: { title: 'Credential drift noticed', readOnly: true },
  connection_credential_drift_cleared: { title: 'Credential drift cleared', readOnly: true },
  cluster_registered: { title: 'Cluster registered' },
  cluster_adopted: { title: 'Cluster adopted' },
  cluster_taken_over: { title: 'Connection taken over' },
}

export interface ActivityFeedItem {
  title: string
  /** The person's username, or BACKGROUND_RECONCILER_ACTOR for the reconciler's own entries. */
  actor: string
  /** The door the request came through (ui / api / cli / webhook), when recorded. */
  door?: string
  /** The recorded outcome (success / failure), when recorded. */
  outcome?: string
  /** RFC3339 timestamp, straight off the entry. */
  timestamp: string
  readOnly: boolean
}

/**
 * mapActivityEntries filters an audit read down to the feed's items: mapped
 * lifecycle events only, newest first exactly as the server returned them,
 * capped at ACTIVITY_FEED_LIMIT.
 */
export function mapActivityEntries(entries: AuditEntry[]): ActivityFeedItem[] {
  const out: ActivityFeedItem[] = []
  for (const entry of entries) {
    const mapping = ACTIVITY_EVENT_TITLES[entry.event]
    if (!mapping) continue // reads, triggers, and anything unmapped: skipped, never a raw id
    out.push({
      title: mapping.title,
      // The reconciler writes its entries as user "sharko" (see
      // internal/clusterreconciler and the credential-check loop). A
      // person's entry carries their username.
      actor: entry.user === 'sharko' || entry.user === '' ? BACKGROUND_RECONCILER_ACTOR : entry.user,
      door: entry.source || undefined,
      outcome: entry.result || undefined,
      timestamp: entry.timestamp,
      readOnly: mapping.readOnly === true,
    })
    if (out.length >= ACTIVITY_FEED_LIMIT) break
  }
  return out
}
