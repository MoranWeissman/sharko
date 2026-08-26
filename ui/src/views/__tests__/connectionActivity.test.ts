// connectionActivity — the mapping table behind "Recent activity since
// Sharko started" (Story 3, ruling 6), rewritten for RULING (f).
//
// The rules pinned here:
//   - only mapped lifecycle events pass through; reads are dropped, never
//     rendered as raw identifiers;
//   - every mapped event has a plain-English title;
//   - the reconciler's own entries name the background reconciler, a
//     person's entries keep their username;
//   - the feed caps at five entries;
//
// and the ruling's own rules, which are the reason this file grew a rule
// engine instead of a list of expected strings:
//
//   - "title, outcome and change result must NEVER contradict each other" —
//     driven over every (event × outcome × changes) combination the server
//     can actually write, checked against the SAME six kinds the Go rule
//     engine uses (internal/api/lifecycle_event_semantics_test.go);
//   - "No changes made" comes from the ENTRY (changes === 'none') and from
//     nowhere else. A read-only check renders nothing there;
//   - an unmapped event name is caught LOUDLY. The allowlist has no
//     fallback, so a server-side rename turns a lifecycle event into
//     silence that looks exactly like "nothing happened" — and ruling (f)
//     renamed and added nine names at once.

import { describe, it, expect } from 'vitest'
import type { AuditEntry } from '@/services/models'
import {
  ACTIVITY_EVENT_TITLES,
  ACTIVITY_FEED_LIMIT,
  ACTIVITY_IGNORED_EVENTS,
  BACKGROUND_RECONCILER_ACTOR,
  mapActivityEntries,
  RECENT_ACTIVITY_LABEL,
  unmappedActivityEvents,
  type ActivityEventKind,
} from '@/views/connectionActivity'
import { LIFECYCLE_EVENT_NAMES, type LifecycleEvent } from '@/generated/lifecycle-events'

function entry(event: string, overrides: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id: `id-${event}`,
    timestamp: '2026-08-18T09:00:00Z',
    level: 'info',
    event,
    user: 'admin',
    action: 'update',
    resource: 'cluster:spoke-eu',
    source: 'ui',
    result: 'success',
    duration_ms: 5,
    ...overrides,
  }
}

/**
 * The complete list of connection-lifecycle event names the SERVER writes.
 *
 * IT IS THE GENERATED CONTRACT, not a copy of it. Nineteen names used to be
 * typed out right here, and the module under test held its own hand-typed
 * copy of the same nineteen — so this test compared one person's list against
 * the same person's list. Neither copy was referred to by any .go file or any
 * CI job, which means the one failure it was written to catch (the server
 * renames an event and the feed goes quiet) could not have failed it.
 *
 * ui/src/generated/lifecycle-events.ts is written by cmd/gen-lifecycle-events
 * from internal/lifecycleevents, and CI's "Lifecycle Events Up To Date" job
 * regenerates and diffs it on every PR.
 */
const SERVER_LIFECYCLE_EVENTS: readonly LifecycleEvent[] = LIFECYCLE_EVENT_NAMES

describe('the honest label', () => {
  it('is exactly "Recent activity since Sharko started"', () => {
    expect(RECENT_ACTIVITY_LABEL).toBe('Recent activity since Sharko started')
  })
})

describe('the mapping table', () => {
  it('gives every lifecycle event a human title, none of which is a raw identifier', () => {
    for (const [event, mapping] of Object.entries(ACTIVITY_EVENT_TITLES)) {
      expect(mapping.title.length).toBeGreaterThan(0)
      // A title never contains an underscore — the raw-id giveaway.
      expect(mapping.title, `${event} renders a raw-looking title`).not.toMatch(/_/)
      expect(mapping.title).not.toBe(event)
    }
  })

  it('EXCLUDES reads: secret_resource_read and the check trigger are not in the table', () => {
    // Cast because the table's key type is the server's closed set, and
    // these two are deliberately outside it — which is the thing being
    // asserted. The cast makes the question askable, not the answer.
    const table = ACTIVITY_EVENT_TITLES as Record<string, unknown>
    expect(table['secret_resource_read']).toBeUndefined()
    expect(table['cluster_connection_secret_check_triggered']).toBeUndefined()
  })

  /**
   * Ruling (f), the browser half of "a genuinely failed check must have a
   * failure-shaped title". The server gave the failure paths their own event
   * names precisely so the feed could name them honestly; this proves the
   * feed took the offer.
   */
  it('gives every "…_failed" / "…_refused" event a title that reads as a failure on its own', () => {
    const failureWords = /(could not|failed|refused|did not)/i
    for (const [event, mapping] of Object.entries(ACTIVITY_EVENT_TITLES)) {
      if (!/_(failed|refused)$/.test(event)) continue
      expect(mapping.title, `${event} has a title that does not read as a failure: "${mapping.title}"`).toMatch(failureWords)
    }
  })

  it('never gives a SUCCESS event a failure-shaped title', () => {
    const failureWords = /(could not|failed|refused|did not)/i
    for (const [event, mapping] of Object.entries(ACTIVITY_EVENT_TITLES)) {
      if (mapping.kind !== 'completed_write') continue
      expect(mapping.title, `${event} is a completed write with a failure-shaped title`).not.toMatch(failureWords)
    }
  })

  it('classifies drift detection as a read-only check, not a failure — the check succeeded, the drift supplies the warning', () => {
    expect(ACTIVITY_EVENT_TITLES['connection_credential_drift_detected'].kind).toBe('read_only')
    expect(ACTIVITY_EVENT_TITLES['connection_credential_drift_cleared'].kind).toBe('read_only')
    expect(ACTIVITY_EVENT_TITLES['connection_credential_check_recovered'].kind).toBe('read_only')
    // The check that did not finish is read_only TOO, exactly as Go
    // classifies it — a check that broke is still a check, so it must claim
    // not_applicable changes, a rule the failure_shaped kind does not carry.
    // Its failure outcome comes from read_only's own "…_failed" sub-case.
    expect(ACTIVITY_EVENT_TITLES['connection_credential_check_failed'].kind).toBe('read_only')
  })

  /**
   * The reviewer's finding: the comment claimed the TS kinds mirror the Go
   * kinds one for one, and one event disagreed. This test makes the claim
   * checkable instead of aspirational — the map below is transcribed from
   * lifecycleEventCatalog in internal/api/lifecycle_event_semantics_test.go.
   */
  it('assigns every event the SAME kind Go assigns it', () => {
    const GO_KINDS: Record<string, ActivityEventKind> = {
      cluster_secret_create: 'completed_write',
      cluster_secret_delete: 'completed_write',
      cluster_secret_user_label_sync: 'completed_write',
      cluster_secret_managed_self_heal: 'completed_write',
      cluster_connection_repair: 'completed_write',
      cluster_secret_create_failed: 'failure_shaped',
      cluster_secret_delete_failed: 'failure_shaped',
      cluster_secret_user_label_sync_failed: 'failure_shaped',
      cluster_secret_managed_self_heal_failed: 'failure_shaped',
      cluster_connection_repair_failed: 'failure_shaped',
      cluster_connection_repair_requested: 'request_scoped',
      cluster_connection_repair_refused: 'refusal',
      connection_credential_drift_detected: 'read_only',
      connection_credential_drift_cleared: 'read_only',
      connection_credential_check_failed: 'read_only',
      connection_credential_check_recovered: 'read_only',
      cluster_adopted: 'batch',
      cluster_registered: 'batch',
    }
    for (const [event, kind] of Object.entries(GO_KINDS)) {
      const mapping = ACTIVITY_EVENT_TITLES[event as LifecycleEvent]
      expect(mapping, `${event} is missing from the feed's table`).toBeTruthy()
      expect(mapping.kind, `${event}: Go says ${kind}`).toBe(kind)
    }
    // cluster_taken_over is declared in internal/lifecycleevents like every
    // other name here, but it is not in the RULE ENGINE's table
    // (lifecycleEventCatalog) — it is the takeover handler's own event,
    // outside the connection-lifecycle rules. That is why it has no entry in
    // the map above and is checked on its own.
    expect(ACTIVITY_EVENT_TITLES['cluster_taken_over']).toBeTruthy()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The unmapped-event guard — the one that would have caught this round's
// rename before the feed went quiet.
// ─────────────────────────────────────────────────────────────────────────────

describe('unmapped events are caught loudly, never dropped silently', () => {
  it('names every server lifecycle event the feed would drop', () => {
    const missing = unmappedActivityEvents(SERVER_LIFECYCLE_EVENTS)
    expect(
      missing,
      `these server events would render NOTHING in the feed — add them to ACTIVITY_EVENT_TITLES: ${missing.join(', ')}`,
    ).toEqual([])
  })

  it('reports a renamed event rather than staying quiet about it', () => {
    // The shape of the failure this guard exists for: the server renames
    // cluster_secret_create to something else and nobody updates the table.
    expect(unmappedActivityEvents(['cluster_secret_create_v2'])).toEqual(['cluster_secret_create_v2'])
  })

  it('does not flag the events the feed deliberately ignores', () => {
    expect(unmappedActivityEvents(ACTIVITY_IGNORED_EVENTS)).toEqual([])
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The table the ruling asked for: every (event × outcome × changes) the
// writers can produce, and the assertion that the RENDERED line never
// contradicts itself.
// ─────────────────────────────────────────────────────────────────────────────

/**
 * The rendered line, exactly as ConnectionReconciliationView composes it:
 * "<title> · <outcome>[ · No changes made]".
 */
function renderedLine(e: AuditEntry): string {
  const [item] = mapActivityEntries([e])
  const parts = [item.title]
  if (item.outcome) parts.push(item.outcome)
  if (item.noChangesMade) parts.push('No changes made')
  return parts.join(' · ')
}

/**
 * The rules, from the product owner's sentences. Returns every violation in
 * the rendered line, so a failure names what is wrong rather than just "not
 * equal".
 */
function contradictions(e: AuditEntry): string[] {
  const mapping = ACTIVITY_EVENT_TITLES[e.event as LifecycleEvent] as { title: string; kind: ActivityEventKind } | undefined
  if (!mapping) return [`${e.event} is not in the feed's table`]
  const bad: string[] = []
  const line = renderedLine(e)
  const kind: ActivityEventKind = mapping.kind
  const failureShapedTitle = /(could not|failed|refused|did not)/i.test(mapping.title)

  // "a successful repair must not retain a failure title" / "a genuinely
  // failed check must have a failure-shaped title" — the same rule, both
  // ends.
  //
  // A completed write and a completed check are both claims that something
  // RAN TO COMPLETION, so neither may carry a non-success outcome: the
  // server gave those paths their own failure-shaped event names precisely
  // so they would not have to. Fan-out (batch) titles are exempt — they name
  // the operation, not a landed write, and "Cluster adopted · failure" when
  // every item failed is honest. request_scoped is exempt for the same
  // reason: "somebody asked for this" makes no claim about the outcome.
  if (e.result === 'success' && failureShapedTitle) {
    bad.push(`"${line}" — a success wearing a failure title`)
  }
  if (e.result !== 'success' && kind === 'completed_write') {
    bad.push(
      `"${line}" — a past-tense title claiming work landed, beside outcome "${e.result}"` +
        ` (the server has a failure-shaped event name for this path — use it)`,
    )
  }
  if (e.result === 'success' && kind === 'failure_shaped') {
    bad.push(`"${line}" — a failure-shaped title recorded as a success`)
  }

  // "a completed write says whether it wrote" — never not_applicable.
  if (kind === 'completed_write' && e.changes === 'not_applicable') {
    bad.push(`"${line}" — a completed write claiming the change question does not apply to it`)
  }

  // read_only mirrors Go's kindReadOnly rule character for character,
  // INCLUDING its "…_failed" sub-case: a check that did not finish is still a
  // check (changes not_applicable), but it must carry the failure outcome,
  // and one that ran to completion must carry success whatever it found —
  // "successfully detecting drift is a successful check with an attention
  // result, not an execution failure".
  if (kind === 'read_only') {
    if (e.changes !== undefined && e.changes !== 'not_applicable') {
      bad.push(`"${line}" — a read-only check reporting changes "${e.changes}"; it neither changed anything nor failed to`)
    }
    if (e.event.endsWith('_failed')) {
      if (e.result !== 'failure') bad.push(`"${line}" — a check-failure event carrying outcome "${e.result}"`)
    } else if (e.result !== 'success') {
      bad.push(`"${line}" — a check that ran to completion carrying outcome "${e.result}"; whatever it found, the check succeeded`)
    }
  }

  // '"No changes made" is an action result, not proof the check failed.'
  if (/No changes made/.test(line)) {
    if (e.changes !== 'none') {
      bad.push(`"${line}" — says no changes were made from something other than the entry's own changes field`)
    }
    if (kind === 'read_only') {
      bad.push(`"${line}" — a read-only check claiming it made no changes; it was never going to`)
    }
  }
  if (e.changes === 'none' && !/No changes made/.test(line)) {
    bad.push(`"${line}" — the entry says nothing changed and the line hides it`)
  }
  if (e.changes === 'applied' && /No changes made/.test(line)) {
    bad.push(`"${line}" — something really changed and the line denies it`)
  }
  return bad
}

describe('every producible (event × outcome × changes) row renders a line that does not contradict itself', () => {
  const rows: { what: string; entry: AuditEntry }[] = [
    // ── The reconciler's Secret lifecycle ──────────────────────────────
    { what: 'a Secret really was created', entry: entry('cluster_secret_create', { level: 'info', result: 'success', changes: 'applied' }) },
    { what: 'the create threw — no Secret exists (C1)', entry: entry('cluster_secret_create_failed', { level: 'error', result: 'failure', changes: 'none' }) },
    { what: 'a Secret really was deleted', entry: entry('cluster_secret_delete', { level: 'info', result: 'success', changes: 'applied' }) },
    { what: 'the delete threw (C7)', entry: entry('cluster_secret_delete_failed', { level: 'error', result: 'failure', changes: 'none' }) },
    { what: "the guest's addon labels really were synced", entry: entry('cluster_secret_user_label_sync', { level: 'info', result: 'success', changes: 'applied' }) },
    { what: 'the guest label sync threw (C7)', entry: entry('cluster_secret_user_label_sync_failed', { level: 'error', result: 'failure', changes: 'none' }) },
    { what: 'self-heal converged the labels', entry: entry('cluster_secret_managed_self_heal', { level: 'info', result: 'success', changes: 'applied' }) },
    { what: 'self-heal threw before writing (C6)', entry: entry('cluster_secret_managed_self_heal_failed', { level: 'error', result: 'failure', changes: 'none' }) },
    {
      what: 'self-heal WROTE but did not converge — the subtle one (C6)',
      entry: entry('cluster_secret_managed_self_heal_failed', { level: 'error', result: 'failure', changes: 'applied' }),
    },

    // ── Repair ─────────────────────────────────────────────────────────
    { what: 'the repair rewrote fields', entry: entry('cluster_connection_repair', { level: 'info', result: 'success', changes: 'applied' }) },
    {
      what: 'the repair ran and nothing needed changing (C3) — the ONE true "No changes made"',
      entry: entry('cluster_connection_repair', { level: 'info', result: 'success', changes: 'none' }),
    },
    { what: 'the repair threw', entry: entry('cluster_connection_repair_failed', { level: 'error', result: 'failure', changes: 'none' }) },
    { what: 'somebody asked for a repair', entry: entry('cluster_connection_repair_requested', { level: 'info', result: 'success' }) },
    { what: 'the repair was refused (C4/C5)', entry: entry('cluster_connection_repair_refused', { level: 'warn', result: 'rejected', changes: 'none' }) },

    // ── The background credential check (C2, the primary target) ───────
    {
      what: 'drift DETECTED — a check that SUCCEEDED and found something',
      entry: entry('connection_credential_drift_detected', { level: 'warn', result: 'success', changes: 'not_applicable' }),
    },
    { what: 'drift cleared', entry: entry('connection_credential_drift_cleared', { level: 'info', result: 'success', changes: 'not_applicable' }) },
    { what: 'the check itself did not finish', entry: entry('connection_credential_check_failed', { level: 'error', result: 'failure', changes: 'not_applicable' }) },
    { what: 'the check completes again', entry: entry('connection_credential_check_recovered', { level: 'info', result: 'success', changes: 'not_applicable' }) },

    // ── Fan-out endpoints (C8/C9) ──────────────────────────────────────
    { what: 'every adoption succeeded', entry: entry('cluster_adopted', { level: 'info', result: 'success', changes: 'applied' }) },
    { what: 'some adoptions failed', entry: entry('cluster_adopted', { level: 'warn', result: 'partial', changes: 'applied' }) },
    { what: 'EVERY adoption failed', entry: entry('cluster_adopted', { level: 'error', result: 'failure', changes: 'none' }) },
    { what: 'every registration succeeded', entry: entry('cluster_registered', { level: 'info', result: 'success', changes: 'applied' }) },
    { what: 'EVERY registration failed', entry: entry('cluster_registered', { level: 'error', result: 'failure', changes: 'none' }) },
    { what: 'the connection was taken over', entry: entry('cluster_taken_over', { level: 'info', result: 'success', changes: 'applied' }) },

    // ── Writers that predate the changes field ─────────────────────────
    { what: 'an old writer that never set changes', entry: entry('cluster_connection_repair', { level: 'info', result: 'success' }) },
  ]

  for (const row of rows) {
    it(row.what, () => {
      expect(contradictions(row.entry)).toEqual([])
    })
  }

  /**
   * The negatives: shapes the server can no longer produce. If the feed ever
   * stops catching them, the rule engine above has gone blind.
   */
  const negatives: { what: string; entry: AuditEntry }[] = [
    {
      what: 'C2 restored — drift detection recorded as a failure',
      entry: entry('connection_credential_drift_detected', { level: 'error', result: 'failure', changes: 'not_applicable' }),
    },
    {
      what: 'C1 restored — the past-tense create title beside a failure outcome',
      entry: entry('cluster_secret_create', { level: 'error', result: 'failure', changes: 'none' }),
    },
    {
      what: 'a "…_failed" event recorded as a success',
      entry: entry('cluster_secret_create_failed', { level: 'info', result: 'success', changes: 'none' }),
    },
    {
      what: 'C3 mirrored — a completed write that will not say whether it wrote',
      entry: entry('cluster_connection_repair', { level: 'info', result: 'success', changes: 'not_applicable' }),
    },
    {
      what: 'a read-only check claiming it deliberately wrote nothing — it was never going to write',
      entry: entry('connection_credential_drift_cleared', { level: 'info', result: 'success', changes: 'none' }),
    },
    {
      what: 'a refusal recorded as a success',
      entry: entry('cluster_connection_repair_refused', { level: 'warn', result: 'success', changes: 'none' }),
    },
  ]

  for (const row of negatives) {
    it(`NEGATIVE: ${row.what} — the suite must reject it`, () => {
      expect(contradictions(row.entry).length).toBeGreaterThan(0)
    })
  }
})

describe('mapActivityEntries', () => {
  it('drops unmapped events and reads; keeps order; caps at the feed limit', () => {
    const entries = [
      entry('secret_resource_read'),
      entry('cluster_connection_repair'),
      entry('unheard_of_event'),
      entry('cluster_secret_create'),
      entry('cluster_secret_delete'),
      entry('cluster_secret_user_label_sync'),
      entry('cluster_registered'),
      entry('cluster_adopted'),
    ]
    const items = mapActivityEntries(entries)
    expect(items).toHaveLength(ACTIVITY_FEED_LIMIT)
    expect(items[0].title).toBe('Connection repaired')
    expect(items.map((i) => i.title)).not.toContain('unheard_of_event')
  })

  it('names the background reconciler for "sharko" and empty users, keeps a person\'s username', () => {
    const items = mapActivityEntries([
      entry('connection_credential_drift_detected', { user: 'sharko' }),
      entry('cluster_secret_create', { user: '' }),
      entry('cluster_connection_repair', { user: 'moran' }),
    ])
    expect(items[0].actor).toBe(BACKGROUND_RECONCILER_ACTOR)
    expect(items[1].actor).toBe(BACKGROUND_RECONCILER_ACTOR)
    expect(items[2].actor).toBe('moran')
  })

  it('takes "no changes made" from the ENTRY, never from the event kind', () => {
    const items = mapActivityEntries([
      // A read-only check: nothing to say about changes.
      entry('connection_credential_drift_cleared', { user: 'sharko', source: '', changes: 'not_applicable' }),
      // A repair that ran and wrote nothing: the one true case.
      entry('cluster_connection_repair', { source: 'ui', result: 'success', changes: 'none' }),
      // A repair that wrote.
      entry('cluster_secret_create', { source: 'ui', result: 'success', changes: 'applied' }),
    ])
    expect(items[0].noChangesMade).toBe(false)
    expect(items[0].door).toBeUndefined()
    expect(items[1].noChangesMade).toBe(true)
    expect(items[1].door).toBe('ui')
    expect(items[1].outcome).toBe('success')
    expect(items[2].noChangesMade).toBe(false)
  })

  it('an entry with no changes field says nothing about changes', () => {
    const [item] = mapActivityEntries([entry('cluster_connection_repair')])
    expect(item.noChangesMade).toBe(false)
  })
})
