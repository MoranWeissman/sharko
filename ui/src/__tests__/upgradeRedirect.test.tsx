// upgradeRedirect.test.tsx — V2-cleanup-61.4 (F2) route coverage.
//
// The standalone Upgrade Checker page (743-line hidden-route duplicate of
// AddonDetail's Upgrade tab) was deleted. The old `/upgrade` route must not
// 404 or keep rendering the deleted page — it redirects to the addon
// catalog, preserving any query string, the same pattern already used for
// `/version-matrix` (see RedirectPreservingQuery in App.tsx).
//
// This mounts ConnectedApp with a real Outlet-rendering Layout stub (so
// child routes actually render) and a mocked AddonCatalog view that prints
// its own search params, so the assertion covers both "we landed on
// /addons" and "the query string survived the redirect".

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Outlet, useSearchParams } from 'react-router-dom'

const mockState = vi.hoisted(() => ({
  connections: [{ name: 'default' }] as Array<Record<string, unknown>>,
}))

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: mockState.connections,
    activeConnection: mockState.connections[0]?.name ?? null,
    setActiveConnection: vi.fn(),
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/services/api', () => ({
  api: {
    getRepoStatus: vi.fn(() =>
      Promise.resolve({ initialized: true, bootstrap_synced: true }),
    ),
  },
}))

vi.mock('@/hooks/useAddonStates', () => ({
  AddonStatesProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useAddonStates: () => ({ states: {}, loading: false }),
}))

// Layout DOES render an Outlet here (unlike the connection-error-gate
// fixture) — we need the child route to actually mount to prove the
// redirect landed somewhere real.
vi.mock('@/components/Layout', () => ({
  Layout: () => (
    <div data-testid="app-layout">
      <Outlet />
    </div>
  ),
}))

// Sentinel for the catalog page — echoes its own query string so the test
// can confirm the redirect preserved it, without rendering the real
// (heavy) AddonCatalog view.
vi.mock('@/views/AddonCatalog', () => ({
  default: () => {
    const [params] = useSearchParams()
    return <div data-testid="addons-page">ADDONS:{params.toString()}</div>
  },
}))

import { ConnectedApp } from '@/App'

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <ConnectedApp />
    </MemoryRouter>,
  )
}

describe('/upgrade route redirect (V2-cleanup-61.4, F2)', () => {
  beforeEach(() => {
    mockState.connections = [{ name: 'default' }]
    sessionStorage.clear()
  })

  it('redirects a bare /upgrade hit to the addon catalog', async () => {
    renderAt('/upgrade')

    await waitFor(() => {
      expect(screen.getByTestId('addons-page')).toBeInTheDocument()
    })
  })

  it('preserves the query string across the redirect (old deep-links keep their params)', async () => {
    renderAt('/upgrade?addon=istio&version=1.22.0')

    await waitFor(() => {
      expect(screen.getByTestId('addons-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('addons-page').textContent).toBe(
      'ADDONS:addon=istio&version=1.22.0',
    )
  })
})
