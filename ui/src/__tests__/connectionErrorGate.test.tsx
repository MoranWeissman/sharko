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
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

const mockState = vi.hoisted(() => ({
  connections: [{ name: 'default' }] as Array<Record<string, unknown>>,
  connectionsError: null as string | null,
  refreshConnections: vi.fn(),
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
    error: mockState.connectionsError,
    refreshConnections: mockState.refreshConnections,
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

import { ConnectedApp, REPO_STATUS_POLL_MS } from '@/App'

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
    mockState.connectionsError = null
    mockState.refreshConnections = vi.fn()
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

  it('the banner goes away by itself when the problem is fixed, no reload (ep3 fix 5)', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      mockState.repoStatus = { initialized: true, bootstrap_synced: false, reason: 'argocd_unreachable' }
      renderConnectedApp()
      await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())

      mockState.repoStatus = { initialized: true, bootstrap_synced: true }
      await vi.advanceTimersByTimeAsync(REPO_STATUS_POLL_MS + 100)
      await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())

      // And it comes back if the problem comes back.
      mockState.repoStatus = { initialized: true, bootstrap_synced: false, reason: 'argocd_unreachable' }
      await vi.advanceTimersByTimeAsync(REPO_STATUS_POLL_MS + 100)
      await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    } finally {
      vi.useRealTimers()
    }
  })

  it('no banner when an older server sends no bootstrap_synced (ep3 fix 23)', async () => {
    mockState.repoStatus = { initialized: true }
    renderConnectedApp()
    await waitFor(() => expect(screen.getByTestId('app-layout')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('the banner and the page share one screen-high column (ep3 fix 23)', async () => {
    mockState.repoStatus = { initialized: true, bootstrap_synced: false, reason: 'argocd_unreachable' }
    renderConnectedApp()
    const alert = await screen.findByRole('alert')
    const column = alert.parentElement as HTMLElement
    expect(column.className).toContain('h-screen')
    expect(column).toContainElement(screen.getByTestId('app-layout'))
  })

  it('a failed connection list shows a message and Try again, never the wizard (ep3 fix 22)', async () => {
    mockState.connections = []
    mockState.connectionsError = 'network down'
    renderConnectedApp()
    expect(await screen.findByText("Sharko couldn't load your saved connections.")).toBeInTheDocument()
    expect(screen.queryByTestId('first-run-wizard')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }))
    expect(mockState.refreshConnections).toHaveBeenCalledTimes(1)
  })

  it('a list that loaded fine and is empty still opens the wizard (ep3 fix 22)', async () => {
    mockState.connections = []
    renderConnectedApp()
    expect(await screen.findByTestId('first-run-wizard')).toBeInTheDocument()
    expect(screen.queryByText("Sharko couldn't load your saved connections.")).not.toBeInTheDocument()
  })
})
