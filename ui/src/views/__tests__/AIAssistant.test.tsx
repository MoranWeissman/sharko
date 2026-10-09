import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AIAssistant } from '@/views/AIAssistant'

const mockAgentChat = vi.fn()
const mockGetAIStatus = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getAIStatus: (...args: unknown[]) => mockGetAIStatus(...args),
    agentChat: (...args: unknown[]) => mockAgentChat(...args),
    agentReset: vi.fn().mockResolvedValue({ status: 'ok' }),
  },
}))

function renderComponent() {
  return render(
    <MemoryRouter>
      <AIAssistant />
    </MemoryRouter>,
  )
}

describe('AIAssistant', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sessionStorage.clear()
    // Mock crypto.randomUUID
    vi.stubGlobal('crypto', {
      ...crypto,
      randomUUID: () => 'test-uuid-1234',
    })
  })

  it('renders suggested prompts when chat is empty and AI is enabled', async () => {
    mockGetAIStatus.mockResolvedValue({ enabled: true })
    renderComponent()

    await waitFor(() => {
      expect(
        screen.getByText('What addons are deployed across my clusters?'),
      ).toBeInTheDocument()
    })

    expect(
      screen.getByText('Is everything healthy right now?'),
    ).toBeInTheDocument()
    expect(
      screen.getByText('Compare datadog versions across clusters'),
    ).toBeInTheDocument()
    expect(
      screen.getByText('Should I upgrade istio-base to the latest version?'),
    ).toBeInTheDocument()
  })

  it('renders the input area', async () => {
    mockGetAIStatus.mockResolvedValue({ enabled: true })
    renderComponent()

    await waitFor(() => {
      expect(
        screen.getByPlaceholderText(
          'Ask about your addons, clusters, or upgrades...',
        ),
      ).toBeInTheDocument()
    })

    expect(screen.getByLabelText('Send message')).toBeInTheDocument()
  })

  it('shows AI not configured when AI is disabled', async () => {
    mockGetAIStatus.mockResolvedValue({ enabled: false })
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('AI not configured')).toBeInTheDocument()
    })
  })

  it('shows header elements when AI is enabled', async () => {
    mockGetAIStatus.mockResolvedValue({ enabled: true })
    renderComponent()

    await waitFor(() => {
      expect(screen.getByText('AI Assistant')).toBeInTheDocument()
    })

    expect(screen.getByText('New')).toBeInTheDocument()
  })
})
