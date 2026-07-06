import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { Layout } from '@/components/Layout'

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
      <Layout />
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
    expect(screen.getByText('Clusters')).toBeInTheDocument()
    expect(screen.getByText('Addons')).toBeInTheDocument()
    expect(screen.getByText('Settings')).toBeInTheDocument()
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
})
