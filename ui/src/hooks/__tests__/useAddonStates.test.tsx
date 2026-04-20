/**
 * useAddonStates — unit coverage for the display-state mapping.
 *
 * The provider's polling behaviour is exercised end-to-end by Dashboard
 * tests; this file pins down the pure mapping function so the green/blue/red
 * boundaries don't drift between the hook and the consumers without a
 * deliberate change.
 */
import { describe, expect, it } from 'vitest'
import { mapHealthToDisplayState } from '@/hooks/useAddonStates'

describe('mapHealthToDisplayState', () => {
  it('maps Healthy + Synced to healthy', () => {
    expect(mapHealthToDisplayState('Healthy', 'Synced')).toBe('healthy')
  })

  it('treats Healthy + OutOfSync as healthy at the rollup level', () => {
    // OutOfSync nuance is shown separately on detail pages via the Sync
    // badge — at the rollup level (Dashboard counters) it stays green.
    expect(mapHealthToDisplayState('Healthy', 'OutOfSync')).toBe('healthy')
  })

  it('maps Progressing to progressing-advisory (NOT degraded)', () => {
    expect(mapHealthToDisplayState('Progressing', 'OutOfSync')).toBe('progressing-advisory')
  })

  it('maps Degraded / Suspended / Error to degraded', () => {
    expect(mapHealthToDisplayState('Degraded', 'Synced')).toBe('degraded')
    expect(mapHealthToDisplayState('Suspended', 'Synced')).toBe('degraded')
    expect(mapHealthToDisplayState('Error', '')).toBe('degraded')
  })

  it('maps Missing to missing (its own bucket so the UI can label it)', () => {
    expect(mapHealthToDisplayState('Missing', 'Unknown')).toBe('missing')
  })

  it('maps Unknown / empty to unknown (unsafe default)', () => {
    expect(mapHealthToDisplayState('Unknown', '')).toBe('unknown')
    expect(mapHealthToDisplayState('', '')).toBe('unknown')
  })

  it('is case-insensitive', () => {
    expect(mapHealthToDisplayState('healthy', 'synced')).toBe('healthy')
    expect(mapHealthToDisplayState('PROGRESSING', 'outofsync')).toBe('progressing-advisory')
  })
})
