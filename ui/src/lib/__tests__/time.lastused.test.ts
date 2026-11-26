import { describe, it, expect } from 'vitest'
import { formatLastUsed, NEVER_USED } from '@/lib/time'

// Walk 5, L4: the "Last used" column showed "Jan 1, 1" for a key nobody used.
describe('formatLastUsed', () => {
  it('says "Never used" for no value and for the zero time the server writes', () => {
    expect(NEVER_USED).toBe('Never used')
    expect(formatLastUsed(undefined)).toBe('Never used')
    expect(formatLastUsed('')).toBe('Never used')
    expect(formatLastUsed('0001-01-01T00:00:00Z')).toBe('Never used')
    expect(formatLastUsed('0001-01-01T00:00:00Z')).not.toMatch(/Jan 1, 1\b/)
  })
  it('prints a real date', () => {
    expect(formatLastUsed('2026-11-26T09:00:00Z')).toMatch(/2026/)
  })
})
