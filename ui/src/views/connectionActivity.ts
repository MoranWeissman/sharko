// connectionActivity — Story 3 of the connection-reconciliation epic
// (ruling 6): the connection page's activity feed shows LIFECYCLE
// transitions only, with human titles, and is honest about what it is — a
// feed of what the in-memory audit ring still holds since the server
// started, never a durable history.
//
// RULING (f), 2026-08-19 — why this file was rewritten.
//
// The feed used to render "<title> · <outcome> · No changes made" with all
// three parts coming from different places and nothing reconciling them.
// The title came from a table here, the outcome from the entry, and the
// third from a STATIC read-only flag in the same table — so it could never
// agree with reality. It said "No changes made" on a repair that had
// rewritten fields, and stayed silent on the one repair that genuinely
// wrote nothing.
//
// The server fixed the root: audit entries now carry `changes`, and the
// failure paths have their OWN event names so a past-tense title can never
// pair with a failure outcome. This module mirrors that model exactly:
//
//   - every event carries a KIND that says what its title claims
//     (completed write / failure-shaped / refusal / read-only / batch /
//     request-scoped), the same six kinds the Go rule engine uses
//     (internal/api/lifecycle_event_semantics_test.go);
//   - a "…_failed" or "…_refused" event gets a failure-shaped TITLE, so the
//     words a reader sees can never contradict the outcome beside them;
//   - "No changes made" comes from the ENTRY's `changes === 'none'` and
//     from nowhere else. A read-only check ('not_applicable') renders
//     nothing rather than claiming no changes were made, which is a
//     different statement.
//
// The other rules, all pinned by tests:
//   - Only events in ACTIVITY_EVENT_TITLES render. Anything else — routine
//     reads (secret_resource_read), check triggers
//     (cluster_connection_secret_check_triggered), page opens — is SKIPPED.
//     An unmapped event never renders as a raw identifier. Because the
//     server's event names CHANGED and GREW in this round, a silently
//     dropped name is now a real risk: unmappedActivityEvents below exists
//     so a test can catch a rename loudly instead of a feed quietly
//     emptying out.
//   - The feed's label is exactly "Recent activity since Sharko started" —
//     the same label the audit page carries (both surfaces, ruling 6).

import type { AuditEntry } from '@/services/models'

/** The honest feed label — both the connection page and the audit page carry it (ruling 6). */
export const RECENT_ACTIVITY_LABEL = 'Recent activity since Sharko started'

/**
 * The line an entry carries when it ran and deliberately wrote nothing.
 * Rendered ONLY for `changes === 'none'` — never for a read-only check,
 * which neither changed anything nor failed to.
 */
export const NO_CHANGES_MADE = 'No changes made'

/** How the feed names the reconciler when an entry was not a person's request. */
export const BACKGROUND_RECONCILER_ACTOR = 'Background reconciler'

/** The quiet line when the ring holds no lifecycle events for this cluster yet. */
export const ACTIVITY_EMPTY_SENTENCE = 'Nothing recorded since Sharko started.'

/** How many entries the feed shows at most. */
export const ACTIVITY_FEED_LIMIT = 5

/**
 * How many audit entries the feed ASKS the server for. The server truncates
 * to its 50-entry default BEFORE this module's read-exclusion runs, and every
 * open of the connection page writes a secret_resource_read (the
 * redacted-YAML fetch writes another) — so with the default budget, ~25 page
 * opens push every lifecycle event out of the returned window while they
 * still sit in the ring, and the feed would falsely say nothing was
 * recorded. 1000 is the ring's own default size (internal/api/router.go
 * builds it as audit.NewLog(1000) — a hardcoded literal, not a configurable
 * one), so this budget covers everything the server can still hold.
 */
export const ACTIVITY_FETCH_LIMIT = 1000

/**
 * What an event's TITLE claims happened. The six kinds, and which kind each
 * event gets, are IDENTICAL to the Go rule engine's
 * (internal/api/lifecycle_event_semantics_test.go `lifecycleEventCatalog`),
 * so a title here can never disagree with the outcome the server wrote.
 *
 * The RULES are identical too, with ONE deliberate difference, stated here
 * rather than left for someone to trip over: Go requires a completed write to
 * SET its change answer, because Go governs what the code writes today. This
 * side governs what is RENDERED, and the feed reads a live in-memory ring
 * that still holds entries from before the `changes` field existed. So an
 * unset `changes` is tolerated here and says nothing, where Go rejects it.
 */
export type ActivityEventKind =
  /** A past-tense title claiming work landed. Only ever a success outcome. */
  | 'completed_write'
  /** A title that says on its face the work did not happen. Never a success. */
  | 'failure_shaped'
  /** Sharko declined to act. The request completed and wrote nothing. */
  | 'refusal'
  /** A check, a comparison, a read. It never changes anything and never fails to. */
  | 'read_only'
  /** A fan-out over many items: honestly success, partial or failure. */
  | 'batch'
  /** The up-front "somebody asked for this" entry. It claims no work landed. */
  | 'request_scoped'

export interface ActivityEventMapping {
  /** The plain-English title the feed renders — never the raw event id. */
  title: string
  /** What that title claims. */
  kind: ActivityEventKind
}

/**
 * The ONLY audit events the feed renders, with their human titles and what
 * each title claims. Growing this table is how a new lifecycle event reaches
 * the feed — there is no fallback path that would render a raw identifier.
 *
 * Kept in step with lifecycleEventCatalog in
 * internal/api/lifecycle_event_semantics_test.go. Every "…_failed" and
 * "…_refused" name has a title that reads as a failure on its own, so the
 * three parts of a rendered line — title, outcome, change result — say the
 * same thing.
 */
export const ACTIVITY_EVENT_TITLES: Record<string, ActivityEventMapping> = {
  // ── The reconciler's connection-Secret lifecycle ──────────────────────
  cluster_secret_create: { title: 'Connection Secret created', kind: 'completed_write' },
  cluster_secret_create_failed: { title: 'Connection Secret could not be created', kind: 'failure_shaped' },
  cluster_secret_delete: { title: 'Connection Secret deleted', kind: 'completed_write' },
  cluster_secret_delete_failed: { title: 'Connection Secret could not be deleted', kind: 'failure_shaped' },
  cluster_secret_user_label_sync: { title: 'Addon labels synced', kind: 'completed_write' },
  cluster_secret_user_label_sync_failed: { title: 'Addon labels could not be synced', kind: 'failure_shaped' },
  cluster_secret_managed_self_heal: { title: 'Addon labels self-healed', kind: 'completed_write' },
  cluster_secret_managed_self_heal_failed: { title: 'Addon labels could not be self-healed', kind: 'failure_shaped' },

  // ── Repair ────────────────────────────────────────────────────────────
  cluster_connection_repair: { title: 'Connection repaired', kind: 'completed_write' },
  cluster_connection_repair_requested: { title: 'Connection repair requested', kind: 'request_scoped' },
  cluster_connection_repair_refused: { title: 'Connection repair refused', kind: 'refusal' },
  cluster_connection_repair_failed: { title: 'Connection repair failed', kind: 'failure_shaped' },

  // ── The background credential check — read-only, always ───────────────
  // Detecting drift is a check that SUCCEEDED and found something worth
  // attention; the drift state itself supplies the warning, so none of
  // these titles is failure-shaped except the one where the check itself
  // did not finish.
  connection_credential_drift_detected: { title: 'Credential drift noticed', kind: 'read_only' },
  connection_credential_drift_cleared: { title: 'Credential drift cleared', kind: 'read_only' },
  // read_only, NOT failure_shaped, matching Go exactly. A check that did not
  // finish is still a check: it neither changed anything nor failed to, so
  // its change answer is not_applicable — which is the rule the
  // failure_shaped kind does NOT impose. Classifying it failure_shaped here
  // (the first version of this table did) left that rule unenforced on the
  // one event most likely to need it. The failure OUTCOME is still required:
  // read_only carries a "…_failed" sub-case below, exactly as Go does.
  connection_credential_check_failed: { title: 'Credential check did not finish', kind: 'read_only' },
  connection_credential_check_recovered: { title: 'Credential check completed again', kind: 'read_only' },

  // ── Fan-out endpoints ─────────────────────────────────────────────────
  cluster_registered: { title: 'Cluster registered', kind: 'batch' },
  cluster_adopted: { title: 'Cluster adopted', kind: 'batch' },
  cluster_taken_over: { title: 'Connection taken over', kind: 'completed_write' },
}

export interface ActivityFeedItem {
  title: string
  /** The person's username, or BACKGROUND_RECONCILER_ACTOR for the reconciler's own entries. */
  actor: string
  /** The door the request came through (ui / api / cli / webhook), when recorded. */
  door?: string
  /** The recorded outcome (success / failure / rejected / partial), when recorded. */
  outcome?: string
  /** RFC3339 timestamp, straight off the entry. */
  timestamp: string
  /** What the title claims — carried through so a renderer can style by it. */
  kind: ActivityEventKind
  /**
   * True ONLY when the ENTRY says `changes: 'none'` — an action that ran and
   * deliberately wrote nothing. Never inferred from the event's kind, and
   * never true for a read-only check.
   */
  noChangesMade: boolean
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
      kind: mapping.kind,
      // The reconciler writes its entries as user "sharko" (see
      // internal/clusterreconciler and the credential-check loop). A
      // person's entry carries their username.
      actor: entry.user === 'sharko' || entry.user === '' ? BACKGROUND_RECONCILER_ACTOR : entry.user,
      door: entry.source || undefined,
      outcome: entry.result || undefined,
      timestamp: entry.timestamp,
      // The one true source. 'not_applicable' and an absent field both
      // render nothing — a read-only check made no changes to report, which
      // is not the same claim as "it made none".
      noChangesMade: entry.changes === 'none',
    })
    if (out.length >= ACTIVITY_FEED_LIMIT) break
  }
  return out
}

/**
 * The events the feed deliberately ignores because they are not lifecycle
 * transitions — routine reads and check triggers. Named here so
 * unmappedActivityEvents can tell "we chose to skip this" apart from "the
 * server renamed something and the feed went quiet".
 */
export const ACTIVITY_IGNORED_EVENTS: readonly string[] = [
  'secret_resource_read',
  'cluster_connection_secret_check_triggered',
  'clusters_created',
]

/**
 * unmappedActivityEvents reports every event name in `events` that this
 * module would silently drop and has not deliberately ignored.
 *
 * WHY THIS EXISTS. The allowlist has no fallback on purpose — a raw event id
 * must never reach a reader. The cost is that a server-side RENAME turns a
 * visible lifecycle event into silence, which looks exactly like "nothing
 * happened". Ruling (f) renamed and added nine event names at once, so the
 * risk is not theoretical. A test feeds this function the server's real
 * catalog; anything it returns is a feed entry that has gone missing.
 */
export function unmappedActivityEvents(events: readonly string[]): string[] {
  const ignored = new Set(ACTIVITY_IGNORED_EVENTS)
  return events.filter((e) => !(e in ACTIVITY_EVENT_TITLES) && !ignored.has(e))
}
