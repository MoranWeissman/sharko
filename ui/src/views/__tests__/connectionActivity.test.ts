// connectionActivity — the mapping table behind "Recent activity since
// Sharko started" (Story 3, ruling 6). The rules pinned here:
//   - only mapped lifecycle events pass through; reads and unmapped events
//     are dropped, never rendered as raw identifiers;
//   - every mapped event has a plain-English title;
//   - the reconciler's own entries name the background reconciler, a
//     person's entries keep their username;
//   - the drift-noticing events are read-only ("No changes made");
//   - the feed caps at five entries.

import { describe, it, expect } from 'vitest'
import type { AuditEntry } from '@/services/models'
import {
  ACTIVITY_EVENT_TITLES,
  ACTIVITY_FEED_LIMIT,
  BACKGROUND_RECONCILER_ACTOR,
  mapActivityEntries,
  RECENT_ACTIVITY_LABEL,
} from '@/views/connectionActivity'

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

describe('the honest label', () => {
  it('is exactly "Recent activity since Sharko started"', () => {
    expect(RECENT_ACTIVITY_LABEL).toBe('Recent activity since Sharko started')
  })
})

describe('the mapping table', () => {
  it('maps every lifecycle event to a human title, none of which is a raw identifier', () => {
    const expected: Record<string, string> = {
      cluster_secret_create: 'Connection Secret created',
      cluster_secret_delete: 'Connection Secret deleted',
      cluster_secret_managed_self_heal: 'Addon labels self-healed',
      cluster_secret_user_label_sync: 'Addon labels synced',
      cluster_connection_repair: 'Connection repaired',
      cluster_connection_repair_requested: 'Connection repair requested',
      connection_credential_drift_detected: 'Credential drift noticed',
      connection_credential_drift_cleared: 'Credential drift cleared',
      cluster_registered: 'Cluster registered',
      cluster_adopted: 'Cluster adopted',
      cluster_taken_over: 'Connection taken over',
    }
    expect(Object.keys(ACTIVITY_EVENT_TITLES).sort()).toEqual(Object.keys(expected).sort())
    for (const [event, title] of Object.entries(expected)) {
      expect(ACTIVITY_EVENT_TITLES[event].title).toBe(title)
      // A title never contains an underscore — the raw-id giveaway.
      expect(ACTIVITY_EVENT_TITLES[event].title).not.toMatch(/_/)
    }
  })

  it('EXCLUDES reads: secret_resource_read and the check trigger are not in the table', () => {
    expect(ACTIVITY_EVENT_TITLES['secret_resource_read']).toBeUndefined()
    expect(ACTIVITY_EVENT_TITLES['cluster_connection_secret_check_triggered']).toBeUndefined()
  })

  it('marks the drift episodes read-only and the writes not', () => {
    expect(ACTIVITY_EVENT_TITLES['connection_credential_drift_detected'].readOnly).toBe(true)
    expect(ACTIVITY_EVENT_TITLES['connection_credential_drift_cleared'].readOnly).toBe(true)
    expect(ACTIVITY_EVENT_TITLES['cluster_connection_repair'].readOnly).toBeUndefined()
    expect(ACTIVITY_EVENT_TITLES['cluster_secret_create'].readOnly).toBeUndefined()
  })
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

  it('carries door, outcome and readOnly through', () => {
    const items = mapActivityEntries([
      entry('connection_credential_drift_cleared', { user: 'sharko', source: '', result: 'success' }),
      entry('cluster_connection_repair', { source: 'ui', result: 'success' }),
    ])
    expect(items[0].readOnly).toBe(true)
    expect(items[0].door).toBeUndefined()
    expect(items[1].readOnly).toBe(false)
    expect(items[1].door).toBe('ui')
    expect(items[1].outcome).toBe('success')
  })
})
