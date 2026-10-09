import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AIConfigSection } from '@/views/settings/AIConfigSection'

/*
 * error review package 2 walk finding — the Test-connection result box
 * picked its icon (CheckCircle vs XCircle) and color by running
 * `testResult.includes('correctly')` against the server's own copy. A
 * caught error message that happened to contain the word "correctly"
 * would have rendered as a false success. The fix keys the icon/color off
 * a real `ok: boolean` set from api.testAI()'s status field (success path)
 * or the catch branch (failure path) — never off message text.
 */

const getAIConfigMock = vi.fn()
const testAIMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getAIConfig: () => getAIConfigMock(),
    testAI: () => testAIMock(),
    saveAIConfig: vi.fn(),
  },
}))

const configuredAIConfig = {
  current_provider: 'gemini',
  available_providers: [
    { id: 'gemini', name: 'Gemini', configured: true, model: 'gemini-2.5-flash' },
  ],
  annotate_on_seed: true,
}

describe('AIConfigSection — Test result icon/color keyed on a real signal', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getAIConfigMock.mockResolvedValue(configuredAIConfig)
  })

  it('renders success (green, CheckCircle) when the server reports status ok', async () => {
    testAIMock.mockResolvedValue({ status: 'ok', response: 'pong' })
    const user = userEvent.setup()

    render(<AIConfigSection />)
    await waitFor(() => expect(screen.getByText('Using Gemini — gemini-2.5-flash')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Test' }))

    const box = await screen.findByText('AI is responding correctly')
    expect(box.closest('div')).toHaveClass('text-green-700')
  })

  it('renders failure (red, XCircle) even when the thrown message happens to contain "correctly"', async () => {
    // Adversarial case: the OLD implementation keyed off
    // testResult.includes('correctly'), so an error message that mentions
    // the word "correctly" would have been misrendered as success.
    testAIMock.mockRejectedValue(new Error('the model did not respond correctly'))
    const user = userEvent.setup()

    render(<AIConfigSection />)
    await waitFor(() => expect(screen.getByText('Using Gemini — gemini-2.5-flash')).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Test' }))

    const box = await screen.findByText('the model did not respond correctly')
    expect(box.closest('div')).toHaveClass('text-red-700')
    expect(box.closest('div')).not.toHaveClass('text-green-700')
  })
})
