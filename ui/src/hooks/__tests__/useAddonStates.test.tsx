/**
 * useAddonStates — unit coverage for the display-state mapping and the
 * settling-window (first-seen-in-bad-state) tracking added by the
 * dashboard UX review 2026-08-01 (Package 3, finding H6).
 *
 * The provider's polling behaviour beyond settling is exercised
 * end-to-end by Dashboard tests; this file pins down the pure mapping
 * function and the settling clock so the green/blue/amber/red boundaries
 * don't drift between the hook and its consumers without a deliberate
 * change.
 */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import {
  mapHealthToDisplayState,
  isSettling,
  SETTLING_WINDOW_MS,
  AddonStatesProvider,
  useAddonStates,
  type AddonState,
} from '@/hooks/useAddonStates'
import { api } from '@/services/api'

vi.mock('@/services/api', () => ({
  api: {
    getAttentionItems: vi.fn(),
  },
}))

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

  it('maps Degraded / Error to degraded', () => {
    expect(mapHealthToDisplayState('Degraded', 'Synced')).toBe('degraded')
    expect(mapHealthToDisplayState('Error', '')).toBe('degraded')
  })

  it('maps Missing to missing (its own bucket so the UI can label it)', () => {
    expect(mapHealthToDisplayState('Missing', 'Unknown')).toBe('missing')
  })

  it('maps Unknown / empty to unknown (unsafe default)', () => {
    expect(mapHealthToDisplayState('Unknown', '')).toBe('unknown')
    expect(mapHealthToDisplayState('', '')).toBe('unknown')
  })

  // dashboard UX review 2026-08-01, finding M: Suspended is an
  // intentional pause (ArgoCD's own precedent), not a failure — it used
  // to fold into 'degraded' (red), which was harsher than ArgoCD itself
  // treats it. It now lands in the same neutral bucket as Unknown.
  it('maps Suspended to unknown, NOT degraded', () => {
    expect(mapHealthToDisplayState('Suspended', 'Synced')).toBe('unknown')
  })

  it('is case-insensitive', () => {
    expect(mapHealthToDisplayState('healthy', 'synced')).toBe('healthy')
    expect(mapHealthToDisplayState('PROGRESSING', 'outofsync')).toBe('progressing-advisory')
  })
})

function baseState(overrides: Partial<AddonState>): AddonState {
  return {
    appName: 'cert-manager-prod',
    addonName: 'cert-manager',
    cluster: 'prod',
    healthStatus: 'Degraded',
    syncStatus: 'Synced',
    displayState: 'degraded',
    lastSeen: Date.now(),
    ...overrides,
  }
}

describe('isSettling (pure)', () => {
  const now = 1_700_000_000_000

  it('is true when a bad app entered its bad state less than 10 minutes ago', () => {
    const state = baseState({ badSince: now - 5 * 60_000 })
    expect(isSettling(state, now)).toBe(true)
  })

  it('is false once the app has been bad for 10 minutes or more', () => {
    const state = baseState({ badSince: now - SETTLING_WINDOW_MS })
    expect(isSettling(state, now)).toBe(false)
    const stateJustUnder = baseState({ badSince: now - (SETTLING_WINDOW_MS - 1) })
    expect(isSettling(stateJustUnder, now)).toBe(true)
  })

  it('is false when there is no badSince at all', () => {
    const state = baseState({ badSince: undefined })
    expect(isSettling(state, now)).toBe(false)
  })

  it('is false for a healthy/progressing/unknown app even if badSince is stale-set', () => {
    expect(isSettling(baseState({ displayState: 'healthy', badSince: now - 1000 }), now)).toBe(false)
    expect(isSettling(baseState({ displayState: 'progressing-advisory', badSince: now - 1000 }), now)).toBe(false)
    expect(isSettling(baseState({ displayState: 'unknown', badSince: now - 1000 }), now)).toBe(false)
  })

  it('is true for missing, not just degraded', () => {
    expect(isSettling(baseState({ displayState: 'missing', badSince: now - 1000 }), now)).toBe(true)
  })
})

// --- Provider integration: badSince is tracked across real polls ---

function Consumer() {
  const { byApp } = useAddonStates()
  const item = byApp.get('cert-manager@prod')
  return (
    <div data-testid="state">
      {item ? `${item.displayState}:${isSettling(item)}` : 'none'}
    </div>
  )
}

describe('AddonStatesProvider — settling window over real polls', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-01-01T00:00:00.000Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.clearAllMocks()
  })

  it('a freshly-degraded app is settling (amber) at first, then confirmed (red) once it has been bad past the window', async () => {
    (api.getAttentionItems as ReturnType<typeof vi.fn>).mockResolvedValue([
      { app_name: 'cert-manager-prod', addon_name: 'cert-manager', cluster: 'prod', health: 'Degraded', sync: 'Synced' },
    ])

    render(
      <AddonStatesProvider>
        <Consumer />
      </AddonStatesProvider>,
    )

    // Initial fetch on mount.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.getByTestId('state').textContent).toBe('degraded:true')

    // Advance well past the settling window — the poll loop (30s ticks)
    // keeps re-observing the SAME degraded app, so badSince never resets.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SETTLING_WINDOW_MS + 60_000)
    })
    expect(screen.getByTestId('state').textContent).toBe('degraded:false')
  })

  it('recovering (leaving the attention feed) resets the clock for next time', async () => {
    (api.getAttentionItems as ReturnType<typeof vi.fn>).mockResolvedValue([
      { app_name: 'cert-manager-prod', addon_name: 'cert-manager', cluster: 'prod', health: 'Degraded', sync: 'Synced' },
    ])

    render(
      <AddonStatesProvider>
        <Consumer />
      </AddonStatesProvider>,
    )
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.getByTestId('state').textContent).toBe('degraded:true');

    // Push well past the settling window, confirming the "becomes red"
    // half of the contract first.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SETTLING_WINDOW_MS + 60_000)
    })
    expect(screen.getByTestId('state').textContent).toBe('degraded:false');

    // App recovers — /dashboard/attention stops returning it entirely.
    const mockGetAttention = api.getAttentionItems as ReturnType<typeof vi.fn>;
    mockGetAttention.mockResolvedValue([])
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    expect(screen.getByTestId('state').textContent).toBe('none');

    // App goes bad again — the clock restarts, so it's settling (amber),
    // not immediately red.
    mockGetAttention.mockResolvedValue([
      { app_name: 'cert-manager-prod', addon_name: 'cert-manager', cluster: 'prod', health: 'Degraded', sync: 'Synced' },
    ])
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    expect(screen.getByTestId('state').textContent).toBe('degraded:true')
  })
})
