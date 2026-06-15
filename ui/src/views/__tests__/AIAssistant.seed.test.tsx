/**
 * V2-cleanup-42 — AIAssistant seed-based initialisation tests
 *
 * Four scenarios:
 * 1. A seed fires even when prior messages exist (the messages.length===0 regression is gone).
 * 2. Two seeds with the same message but different nonces both fire api.agentChat.
 * 3. A manual open (no seed / nonce) does NOT call api.agentChat and does NOT wipe existing messages.
 * 4. An empty res.response renders a visible fallback bubble instead of a blank one.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AIAssistant } from '@/views/AIAssistant'
import type { AIAssistantSeed } from '@/views/AIAssistant'

// jsdom doesn't implement scrollIntoView — mock it globally.
window.HTMLElement.prototype.scrollIntoView = vi.fn()

// --- Mocks ---

const mockAgentChat = vi.fn()
const mockAgentReset = vi.fn()
const mockGetAIStatus = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getAIStatus: (...args: unknown[]) => mockGetAIStatus(...args),
    agentChat: (...args: unknown[]) => mockAgentChat(...args),
    agentReset: (...args: unknown[]) => mockAgentReset(...args),
  },
}))

// Stable randomUUID increments so nonces are predictable
let uuidCounter = 0
vi.stubGlobal('crypto', {
  ...globalThis.crypto,
  randomUUID: () => `uuid-${++uuidCounter}`,
})

// --- Helpers ---

function renderWithSeed(seed?: AIAssistantSeed) {
  return render(
    <MemoryRouter>
      <AIAssistant embedded initialMessageSeed={seed} />
    </MemoryRouter>,
  )
}

// --- Tests ---

describe('AIAssistant — seed-based initialisation (V2-cleanup-42)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    uuidCounter = 0
    mockGetAIStatus.mockResolvedValue({ enabled: true })
    mockAgentReset.mockResolvedValue({ status: 'ok' })
  })

  // -----------------------------------------------------------------------
  // 1. Seed fires even when prior messages exist
  // -----------------------------------------------------------------------
  it('sends the seeded message even when prior conversation messages exist', async () => {
    mockAgentChat.mockResolvedValue({ response: 'Got it.' })

    const { rerender } = renderWithSeed({ message: 'first question', nonce: 'nonce-A' })

    // Wait for AI status to resolve and first seed to fire
    await waitFor(() => {
      expect(mockAgentChat).toHaveBeenCalledTimes(1)
    })
    expect(mockAgentChat).toHaveBeenCalledWith(
      expect.any(String),
      'first question',
      undefined,
    )

    // Wait for the assistant bubble to appear
    await waitFor(() => {
      expect(screen.getByText('Got it.')).toBeInTheDocument()
    })

    mockAgentChat.mockClear()
    mockAgentChat.mockResolvedValue({ response: 'Second answer.' })

    // Now dispatch a NEW seed — different nonce. The prior messages exist.
    await act(async () => {
      rerender(
        <MemoryRouter>
          <AIAssistant
            embedded
            initialMessageSeed={{ message: 'second question', nonce: 'nonce-B' }}
          />
        </MemoryRouter>,
      )
    })

    // The new seed must fire, regardless of prior messages
    await waitFor(() => {
      expect(mockAgentChat).toHaveBeenCalledTimes(1)
    })
    expect(mockAgentChat).toHaveBeenCalledWith(
      expect.any(String),
      'second question',
      undefined,
    )
  })

  // -----------------------------------------------------------------------
  // 2. Same message, two different nonces → fires twice
  // -----------------------------------------------------------------------
  it('fires api.agentChat twice for the same message string when nonces differ', async () => {
    mockAgentChat.mockResolvedValue({ response: 'Answer.' })

    const { rerender } = renderWithSeed({ message: 'error X', nonce: 'nonce-1' })

    await waitFor(() => {
      expect(mockAgentChat).toHaveBeenCalledTimes(1)
    })
    expect(mockAgentChat).toHaveBeenCalledWith(expect.any(String), 'error X', undefined)

    mockAgentChat.mockClear()
    mockAgentChat.mockResolvedValue({ response: 'Answer again.' })

    await act(async () => {
      rerender(
        <MemoryRouter>
          <AIAssistant
            embedded
            initialMessageSeed={{ message: 'error X', nonce: 'nonce-2' }}
          />
        </MemoryRouter>,
      )
    })

    await waitFor(() => {
      expect(mockAgentChat).toHaveBeenCalledTimes(1)
    })
    expect(mockAgentChat).toHaveBeenCalledWith(expect.any(String), 'error X', undefined)
  })

  // -----------------------------------------------------------------------
  // 3. Manual open (no seed) must NOT call api.agentChat and must NOT wipe messages
  // -----------------------------------------------------------------------
  it('does not call api.agentChat and preserves messages when no seed is provided', async () => {
    mockAgentChat.mockResolvedValue({ response: 'Hello.' })

    // Render with a seed first so we have messages
    const { rerender } = renderWithSeed({ message: 'initial', nonce: 'nonce-X' })

    await waitFor(() => {
      expect(screen.getByText('Hello.')).toBeInTheDocument()
    })

    mockAgentChat.mockClear()

    // Rerender without a seed (simulates FloatingAssistant / manual open)
    await act(async () => {
      rerender(
        <MemoryRouter>
          <AIAssistant embedded initialMessageSeed={undefined} />
        </MemoryRouter>,
      )
    })

    // Wait a tick to confirm no erroneous effect fired
    await new Promise((r) => setTimeout(r, 80))

    expect(mockAgentChat).not.toHaveBeenCalled()

    // Prior message bubble is still visible (conversation not wiped)
    expect(screen.getByText('Hello.')).toBeInTheDocument()
  })

  // -----------------------------------------------------------------------
  // 4. Empty res.response → visible fallback bubble (not blank)
  // -----------------------------------------------------------------------
  it('renders a visible fallback bubble when res.response is empty', async () => {
    mockAgentChat.mockResolvedValue({ response: '' })

    renderWithSeed({ message: 'what is wrong?', nonce: 'nonce-empty' })

    await waitFor(() => {
      expect(mockAgentChat).toHaveBeenCalledTimes(1)
    })

    // A non-blank fallback bubble must appear instead of a blank bubble
    await waitFor(() => {
      expect(
        screen.getByText(/couldn't generate a response/i),
      ).toBeInTheDocument()
    })
  })
})
