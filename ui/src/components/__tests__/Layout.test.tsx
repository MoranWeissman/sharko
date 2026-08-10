import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { Layout } from '@/components/Layout'
// WQ-3 — Layout now reads useNavBadges(), which calls useAddonStates();
// has to be mounted inside the provider or the hook throws.
import { AddonStatesProvider } from '@/hooks/useAddonStates'
import { api } from '@/services/api'

// Controls the AI-assistant opt-in gate (V2-cleanup-55.4). Default: not
// configured → assistant entry points hidden.
const mockGetAIStatus = vi.fn()

vi.mock('@/services/api', () => ({
  fetchTrackedPRs: vi.fn().mockResolvedValue({ prs: [] }),
  api: {
    getNotifications: vi.fn().mockResolvedValue({ notifications: [], unread_count: 0 }),
    markAllNotificationsRead: vi.fn().mockResolvedValue({}),
    getAIStatus: (...args: unknown[]) => mockGetAIStatus(...args),
    agentChat: vi.fn().mockResolvedValue({ response: 'hi' }),
    agentReset: vi.fn().mockResolvedValue({ status: 'ok' }),
    // WQ-3 — nav badge reads (hooks/useNavBadges.tsx). Defaulted to a
    // problem-free state so existing tests that don't care about badges
    // keep passing unchanged.
    getAttentionItems: vi.fn().mockResolvedValue([]),
    getDashboardStats: vi.fn().mockResolvedValue({
      clusters: { total: 0, connected: 0, pending: 0, untested: 0, missing: 0, failed: 0 },
    }),
    getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
    getRepoStatus: vi.fn().mockResolvedValue({ initialized: true, bootstrap_synced: true }),
  },
}))

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: [
      {
        name: 'dev',
        is_active: true,
        git_provider: 'github',
        git_repo_identifier: 'org/repo',
      },
    ],
    activeConnection: 'dev',
    setActiveConnection: vi.fn(),
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
  }),
}))

vi.mock('@/hooks/useTheme', () => ({
  useTheme: () => ({
    theme: 'light',
    toggleTheme: vi.fn(),
  }),
}))

// Mutable so individual tests (A6: non-admin Settings visibility) can flip
// the role without re-declaring the whole mock module.
const authState = vi.hoisted(() => ({ isAdmin: true }))

vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => ({
    token: 'test-token',
    login: vi.fn(),
    logout: vi.fn(),
    isAuthenticated: true,
    isAdmin: authState.isAdmin,
    loading: false,
    error: null,
  }),
}))

function renderLayout() {
  return render(
    <MemoryRouter>
      <AddonStatesProvider>
        <Layout />
      </AddonStatesProvider>
    </MemoryRouter>,
  )
}

describe('Layout', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetAIStatus.mockResolvedValue({ enabled: false })
    authState.isAdmin = true
  })

  it('renders without crashing', () => {
    renderLayout()
    expect(screen.getByText('Sharko')).toBeInTheDocument()
  })

  it('renders all navigation links', () => {
    renderLayout()
    expect(screen.getByText('Dashboard')).toBeInTheDocument()
    expect(screen.getByText('Managed Clusters')).toBeInTheDocument()
    expect(screen.getByText('Addons')).toBeInTheDocument()
    expect(screen.getByText('Settings')).toBeInTheDocument()
  })

  // Secrets-area rename (SN-2/SN-7): one sidebar item for the whole
  // area, named "Secrets", pointing at the Cluster connections subpage.
  // The retired names must not come back as nav labels.
  it('shows the Secrets nav item under Monitor, pointing at /secrets/connections', () => {
    renderLayout()
    const link = screen.getByRole('link', { name: 'Secrets' })
    expect(link).toHaveAttribute('href', '/secrets/connections')
  })

  it('never renders "Secret Sync" or "Managed Secrets" as a navigation label', () => {
    renderLayout()
    expect(screen.queryByText('Secret Sync')).not.toBeInTheDocument()
    expect(screen.queryByText('Managed Secrets')).not.toBeInTheDocument()
  })

  // SN-2: the Secrets item is active for EVERY path under /secrets —
  // both inventories and both detail routes — and inactive elsewhere.
  it.each([
    '/secrets/connections',
    '/secrets/addons',
    '/secrets/connections/prod-eu',
    '/secrets/addons/prod-eu/datadog',
  ])('marks the Secrets item active at %s', (path) => {
    render(
      <MemoryRouter initialEntries={[path]}>
        <AddonStatesProvider>
          <Layout />
        </AddonStatesProvider>
      </MemoryRouter>,
    )
    // On /secrets/* the breadcrumb renders its own "Secrets" link (to
    // /secrets) — the SIDEBAR item is the one pointing at
    // /secrets/connections.
    const link = screen
      .getAllByRole('link', { name: 'Secrets' })
      .find((l) => l.getAttribute('href') === '/secrets/connections')
    expect(link).toBeDefined()
    expect(link!.className).toContain('bg-[#14466e]')
    expect(link!.className).toContain('border-[#9fcffb]')
  })

  // SN-2: breadcrumb labels for the area — and a cluster/addon segment
  // still falls through to its decoded raw name.
  it('renders Secrets-area breadcrumbs with the approved subpage names', () => {
    render(
      <MemoryRouter initialEntries={['/secrets/addons/prod-eu/datadog']}>
        <AddonStatesProvider>
          <Layout />
        </AddonStatesProvider>
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: 'Addon secrets' })).toHaveAttribute('href', '/secrets/addons')
    expect(screen.getByText('prod-eu')).toBeInTheDocument()
    expect(screen.getByText('datadog')).toBeInTheDocument()
    expect(screen.queryByText('Secret Sync')).not.toBeInTheDocument()
  })

  it('does not mark the Secrets item active on an unrelated page', () => {
    render(
      <MemoryRouter initialEntries={['/dashboard']}>
        <AddonStatesProvider>
          <Layout />
        </AddonStatesProvider>
      </MemoryRouter>,
    )
    const link = screen.getByRole('link', { name: 'Secrets' })
    expect(link.className).not.toContain('border-[#9fcffb]')
  })

  it('collapses sidebar when toggle button is clicked', () => {
    renderLayout()
    const collapseBtn = screen.getByLabelText('Collapse sidebar')
    fireEvent.click(collapseBtn)
    expect(screen.getByLabelText('Expand sidebar')).toBeInTheDocument()
  })

  // V2-cleanup-61.3 (A3/A4): read-only pages section renamed "Manage" →
  // "Monitor"; "Dashboards" (the external-dashboards shelf) renamed to
  // "External Dashboards" so it stops reading as a sibling/typo of
  // "Dashboard" above it.
  it('renames the "Manage" nav section to "Monitor" and "Dashboards" to "External Dashboards"', () => {
    renderLayout()
    expect(screen.getByText('Monitor')).toBeInTheDocument()
    expect(screen.queryByText('Manage')).not.toBeInTheDocument()
    expect(screen.getByText('External Dashboards')).toBeInTheDocument()
    expect(screen.queryByText('Dashboards')).not.toBeInTheDocument()
  })

  // V2-cleanup-61.3 (A6): non-admins have 5 sections allowlisted inside
  // Settings (Settings.tsx ALLOWED_NON_ADMIN) and SystemView links every
  // role there, but the nav item used to be adminOnly — no path to reach
  // it. It must render for every role now.
  it('shows the Settings nav item for non-admin roles too', () => {
    authState.isAdmin = false
    renderLayout()
    expect(screen.getByText('Settings')).toBeInTheDocument()
  })

  // V2-cleanup-55.4: the AI assistant is OPT-IN — hidden by default, shown
  // only when an AI provider is configured (GET /upgrade/ai-status).
  describe('AI assistant opt-in gate', () => {
    it('hides every assistant entry point when no AI provider is configured (default)', async () => {
      mockGetAIStatus.mockResolvedValue({ enabled: false })
      renderLayout()

      // Let the ai-status fetch settle.
      await waitFor(() => expect(mockGetAIStatus).toHaveBeenCalled())

      // No "Ask AI" top-bar toggle, no floating bubble.
      expect(screen.queryByLabelText('Toggle AI Assistant')).not.toBeInTheDocument()
      expect(screen.queryByLabelText('Open AI Assistant')).not.toBeInTheDocument()

      // open-assistant events are ignored — no panel appears.
      fireEvent(window, new CustomEvent('open-assistant', { detail: { message: 'help', nonce: 'n1' } }))
      expect(screen.queryByText('Sharko AI')).not.toBeInTheDocument()
    })

    it('hides assistant entry points when the ai-status check fails', async () => {
      mockGetAIStatus.mockRejectedValue(new Error('boom'))
      renderLayout()

      await waitFor(() => expect(mockGetAIStatus).toHaveBeenCalled())

      expect(screen.queryByLabelText('Toggle AI Assistant')).not.toBeInTheDocument()
      expect(screen.queryByLabelText('Open AI Assistant')).not.toBeInTheDocument()
    })

    it('shows the Ask AI toggle and floating bubble when an AI provider is configured', async () => {
      mockGetAIStatus.mockResolvedValue({ enabled: true })
      renderLayout()

      await waitFor(() => {
        expect(screen.getByLabelText('Toggle AI Assistant')).toBeInTheDocument()
      })
      expect(screen.getByLabelText('Open AI Assistant')).toBeInTheDocument()
    })

    it('opens the assistant panel from the Ask AI toggle when configured', async () => {
      mockGetAIStatus.mockResolvedValue({ enabled: true })
      renderLayout()

      const toggle = await screen.findByLabelText('Toggle AI Assistant')
      fireEvent.click(toggle)

      expect(screen.getByText('Sharko AI')).toBeInTheDocument()
    })
  })

  // V2-cleanup-61.4 (G2): the user avatar menu used to be a hand-rolled
  // `absolute` panel + a `fixed inset-0` click-catcher for "outside click"
  // — no Escape handling, no focus trap, no ARIA menu semantics. It's now
  // the shadcn/Radix DropdownMenu primitive.
  describe('user avatar menu', () => {
    it('opens with Account / theme toggle / Log out items', async () => {
      const user = userEvent.setup()
      renderLayout()
      await user.click(screen.getByLabelText('User menu'))

      await waitFor(() => {
        expect(screen.getByText('Log out')).toBeInTheDocument()
      })
      expect(screen.getByText('Account')).toBeInTheDocument()
      expect(screen.getByText('Dark Mode')).toBeInTheDocument()
    })

    it('closes on Escape', async () => {
      const user = userEvent.setup()
      renderLayout()
      await user.click(screen.getByLabelText('User menu'))
      await waitFor(() => {
        expect(screen.getByText('Log out')).toBeInTheDocument()
      })

      await user.keyboard('{Escape}')

      await waitFor(() => {
        expect(screen.queryByText('Log out')).not.toBeInTheDocument()
      })
    })

    it('closes on outside click', async () => {
      const user = userEvent.setup()
      renderLayout()
      await user.click(screen.getByLabelText('User menu'))
      await waitFor(() => {
        expect(screen.getByText('Log out')).toBeInTheDocument()
      })

      // Radix's DropdownMenu is modal — it sets pointer-events:none on the
      // body while open, so a real pointer can't reach outside content
      // (matches real browser behavior). Dispatch the raw event Radix's own
      // dismissable-layer listens for instead of simulating a hardware click.
      fireEvent.pointerDown(document.body)

      await waitFor(() => {
        expect(screen.queryByText('Log out')).not.toBeInTheDocument()
      })
    })
  })

  // WQ-3 — messenger-style unread badges. Same shared computation as the
  // Dashboard's thin attention line (hooks/useNavBadges.tsx).
  describe('nav unread badges', () => {
    it('shows no badge on Observability or System when there is nothing broken', async () => {
      renderLayout()
      await waitFor(() => expect(screen.getByText('Observability')).toBeInTheDocument())
      expect(screen.queryByTitle(/unread problem/)).not.toBeInTheDocument()
    })

    it('shows the confirmed cluster-problem count on the Observability entry', async () => {
      (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
        clusters: { total: 2, connected: 0, pending: 0, untested: 0, missing: 1, failed: 1 },
      })
      renderLayout()

      await waitFor(() => {
        expect(screen.getByTitle('2 unread problems')).toBeInTheDocument()
      })
    })

    it('caps the badge display at "9+"', async () => {
      (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
        clusters: { total: 20, connected: 0, pending: 0, untested: 0, missing: 5, failed: 10 },
      })
      renderLayout()

      await waitFor(() => {
        expect(screen.getByTitle('15 unread problems')).toBeInTheDocument();
      })
      expect(screen.getByTitle('15 unread problems').textContent).toBe('9+')
    })

    // A degraded addon inside the 10-minute settling window must NOT bump
    // the Observability badge — same rule the Dashboard's thin line
    // follows (see useAddonStates.tsx SETTLING_WINDOW_MS + the shared
    // getConfirmedProblemCount in components/AttentionSection.tsx).
    it('a freshly-degraded (settling) addon does not flash a badge', async () => {
      // Isolate from earlier tests' overrides — mockResolvedValue persists
      // across vi.clearAllMocks() (it only clears call history).
      (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
        clusters: { total: 0, connected: 0, pending: 0, untested: 0, missing: 0, failed: 0 },
      });
      (api.getAttentionItems as ReturnType<typeof vi.fn>).mockResolvedValue([
        { app_name: 'cert-manager-prod', addon_name: 'cert-manager', cluster: 'prod', health: 'Degraded', sync: 'Synced' },
      ]);
      renderLayout()

      await waitFor(() => expect(screen.getByText('Observability')).toBeInTheDocument())
      expect(screen.queryByTitle(/unread problem/)).not.toBeInTheDocument()
    })

    it('shows a machinery-problem badge on System when ArgoCD is unreachable', async () => {
      (api.getRepoStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ initialized: true, bootstrap_synced: true });
      (api.getClusters as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('network error'));
      renderLayout()

      await waitFor(() => {
        expect(screen.getByTitle('1 unread problem')).toBeInTheDocument()
      })
    })

    it('shows a machinery-problem badge on System when the Git connection is down', async () => {
      // Isolate from the previous test's rejected getClusters mock —
      // mockRejectedValue persists across vi.clearAllMocks() too.
      (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
      (api.getRepoStatus as ReturnType<typeof vi.fn>).mockResolvedValue({
        initialized: false,
        bootstrap_synced: false,
        reason: 'connection_error',
      });
      renderLayout()

      await waitFor(() => {
        expect(screen.getByTitle('1 unread problem')).toBeInTheDocument()
      })
    })
  })
})
