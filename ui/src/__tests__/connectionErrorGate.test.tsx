// connectionErrorGate.test.tsx — V2-cleanup-50 render coverage.
//
// A broken Git connection (e.g. a corporate Zscaler TLS-inspection proxy
// producing an x509 "unknown authority" error) used to make /repo/status
// report not-initialized, which threw the user into the re-bootstrap wizard.
// The fix: classify that as `connection_error` and, in the UI, KEEP the user
// in their working app while surfacing the problem via a non-blocking banner.
//
// This test mounts ConnectedApp with one existing connection and a
// `connection_error` repo status, then asserts:
//   1. the connection-error banner is rendered (with a Settings → Connections
//      link), and
//   2. the FirstRunWizard is NOT rendered.
//
// We mock the provider/view graph the same way the wizard tests do: vi.hoisted
// holder for the connections mock + a stubbed api + a sentinel FirstRunWizard
// so "did the wizard render?" is a single, unambiguous assertion.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

const mockState = vi.hoisted(() => ({
  connections: [{ name: 'default' }] as Array<Record<string, unknown>>,
  repoStatus: { initialized: false, bootstrap_synced: false, reason: 'connection_error' } as {
    initialized: boolean
    bootstrap_synced?: boolean
    reason?: string
  },
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
    getRepoStatus: vi.fn(() => Promise.resolve(mockState.repoStatus)),
  },
}))

// Sentinel wizard — a single testid so "did the wizard render?" is unambiguous.
vi.mock('@/components/FirstRunWizard', () => ({
  FirstRunWizard: () => <div data-testid="first-run-wizard">WIZARD</div>,
}))

// AddonStatesProvider depends on the active connection / poll loop; stub it to a
// passthrough so we can mount ConnectedApp in isolation.
vi.mock('@/hooks/useAddonStates', () => ({
  AddonStatesProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useAddonStates: () => ({ states: {}, loading: false }),
}))

// Layout pulls in the full nav/sidebar graph; stub it to a sentinel so the
// router has something cheap to render for the dashboard route.
vi.mock('@/components/Layout', () => ({
  Layout: () => <div data-testid="app-layout">APP</div>,
}))

import { ConnectedApp } from '@/App'

function renderConnectedApp() {
  return render(
    <MemoryRouter initialEntries={['/dashboard']}>
      <ConnectedApp />
    </MemoryRouter>,
  )
}

describe('ConnectedApp connection-error gate (V2-cleanup-50)', () => {
  beforeEach(() => {
    mockState.connections = [{ name: 'default' }]
    sessionStorage.clear()
  })

  it('renders the connection-error banner and NOT the wizard for a connection_error status', async () => {
    mockState.repoStatus = {
      initialized: false,
      bootstrap_synced: false,
      reason: 'connection_error',
    }
    renderConnectedApp()

    // Banner appears once the async repo-status probe resolves.
    await waitFor(() =>
      expect(screen.getByText(/can't reach your Git connection/i)).toBeInTheDocument(),
    )

    // The banner links to Settings → Connections.
    const link = screen.getByRole('link', { name: /Settings → Connections/i })
    expect(link).toHaveAttribute('href', '/settings?section=connections')

    // The wizard must NOT be shown — the user keeps their working app.
    expect(screen.queryByTestId('first-run-wizard')).not.toBeInTheDocument()
  })

  it('still shows the wizard (no banner) for a genuine not_bootstrapped status', async () => {
    mockState.repoStatus = {
      initialized: false,
      bootstrap_synced: false,
      reason: 'not_bootstrapped',
    }
    renderConnectedApp()

    await waitFor(() =>
      expect(screen.getByTestId('first-run-wizard')).toBeInTheDocument(),
    )
    expect(screen.queryByText(/can't reach your Git connection/i)).not.toBeInTheDocument()
  })
})
