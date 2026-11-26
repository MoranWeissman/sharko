import { describe, expect, it } from 'vitest'
import { auditWho, API_KEY_MARK } from '@/views/audit/auditWho'

/*
 * Walk 6: nothing in the audit log said that a call came from an API key. A
 * key's row looked like a person's row.
 */
describe('auditWho', () => {
  it('marks a row made with an API key', () => {
    expect(API_KEY_MARK).toBe('(API key)')
    expect(auditWho({ user: 'ci-bot', auth_type: 'api_key' })).toBe('ci-bot (API key)')
  })

  it('shows a logged-in user by name only', () => {
    expect(auditWho({ user: 'bob' })).toBe('bob')
    expect(auditWho({ user: 'bob', auth_type: '' })).toBe('bob')
  })

  it('says anonymous when there is no name', () => {
    expect(auditWho({})).toBe('anonymous')
  })
})
