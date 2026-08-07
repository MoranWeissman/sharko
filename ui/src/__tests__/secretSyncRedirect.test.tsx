// secretSyncRedirect.test.tsx — gitops-proud P4-I (D1) route coverage.
//
// The page was renamed "Secret Sync" and moved to /secret-sync. The old
// /secrets route must not 404 or stop working — bookmarks and old links
// keep working via a redirect, the same RedirectPreservingQuery pattern
// already covering /upgrade and /version-matrix (see upgradeRedirect.test.tsx,
// which this file mirrors almost exactly).
//
// This mounts ConnectedApp with a real Outlet-rendering Layout stub (so
// child routes actually render) and a mocked ManagedSecrets view that
// prints its own search params, so the assertion covers both "we landed on
// /secret-sync" and "the query string survived the redirect" (an open
// panel's ?row= param, a chip filter's ?state=, etc. must not be dropped
// when someone follows an old bookmarked link).

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

// Sentinel for the Secret Sync page — echoes its own query string so the
// test can confirm the redirect preserved it, without rendering the real
// (heavy) ManagedSecrets view.
vi.mock('@/views/ManagedSecrets', () => ({
  default: () => {
    const [params] = useSearchParams()
    return <div data-testid="secret-sync-page">SECRET-SYNC:{params.toString()}</div>
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

describe('/secrets route alias (gitops-proud P4-I, D1)', () => {
  beforeEach(() => {
    mockState.connections = [{ name: 'default' }]
    sessionStorage.clear()
  })

  it('a bare /secret-sync hit renders the page directly (no redirect needed)', async () => {
    renderAt('/secret-sync')

    await waitFor(() => {
      expect(screen.getByTestId('secret-sync-page')).toBeInTheDocument()
    })
  })

  it('redirects a bare /secrets hit (the old bookmark/link) to /secret-sync', async () => {
    renderAt('/secrets')

    await waitFor(() => {
      expect(screen.getByTestId('secret-sync-page')).toBeInTheDocument()
    })
  })

  it('preserves the query string across the redirect (an old deep-link to a filtered/opened row keeps working)', async () => {
    renderAt('/secrets?row=connection-prod-eu&state=out_of_sync')

    await waitFor(() => {
      expect(screen.getByTestId('secret-sync-page')).toBeInTheDocument()
    })
    expect(screen.getByTestId('secret-sync-page').textContent).toBe(
      'SECRET-SYNC:row=connection-prod-eu&state=out_of_sync',
    )
  })
})
