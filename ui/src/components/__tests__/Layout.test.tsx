import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
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

vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => ({
    token: 'test-token',
    login: vi.fn(),
    logout: vi.fn(),
    isAuthenticated: true,
    isAdmin: true,
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
})
