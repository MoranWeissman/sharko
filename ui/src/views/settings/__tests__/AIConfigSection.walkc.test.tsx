import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AIConfigSection, KEY_IS_SAVED_HINT, ANNOTATE_SAVE_FAILED } from '@/views/settings/AIConfigSection'
import { AI_CONFIG_CHANGED_EVENT } from '@/lib/aiEvents'

/*
 * Walk 7 finding 1: one click on "Annotate values on generate" erased the
 * saved key and model. Finding 4: the assistant only showed up or went away
 * after a page reload.
 */
const getAIConfigMock = vi.fn()
const saveAIConfigMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getAIConfig: () => getAIConfigMock(),
    testAI: vi.fn(),
    testAIConfig: vi.fn(),
    saveAIConfig: (...args: unknown[]) => saveAIConfigMock(...args),
  },
}))

const saved = {
  current_provider: 'openai',
  available_providers: [
    { id: 'openai', name: 'OpenAI', configured: true, model: 'gpt-4o' },
    { id: 'claude', name: 'Claude (Anthropic)', configured: false, model: '' },
  ],
  annotate_on_seed: true,
  has_api_key: true,
}

describe('AIConfigSection — the switch, and the page after a save', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getAIConfigMock.mockResolvedValue(saved)
    saveAIConfigMock.mockResolvedValue({ status: 'ok' })
  })

  it('the switch sends only the provider and the switch, and reads the page again', async () => {
    getAIConfigMock
      .mockResolvedValueOnce(saved)
      .mockResolvedValue({ ...saved, annotate_on_seed: false })
    const user = userEvent.setup()
    render(<AIConfigSection />)
    await waitFor(() => expect(screen.getByText('Using OpenAI — gpt-4o')).toBeInTheDocument())

    const sw = screen.getByRole('switch')
    expect(sw).toHaveAttribute('aria-checked', 'true')
    await user.click(sw)

    await waitFor(() => expect(saveAIConfigMock).toHaveBeenCalledTimes(1))
    // Exactly these two parts. No empty key, no empty model: the server
    // keeps what is saved for every part that is left out.
    expect(saveAIConfigMock).toHaveBeenCalledWith({ provider: 'openai', annotate_on_seed: false })

    // The page shows the new state without a reload, and the header still
    // names the model.
    await waitFor(() => expect(screen.getByRole('switch')).toHaveAttribute('aria-checked', 'false'))
    expect(screen.getByText('Using OpenAI — gpt-4o')).toBeInTheDocument()
  })

  it('says so when the switch could not be saved', async () => {
    expect(ANNOTATE_SAVE_FAILED).toBe('The switch could not be saved. Try again.')
    saveAIConfigMock.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    render(<AIConfigSection />)
    await waitFor(() => expect(screen.getByRole('switch')).toBeInTheDocument())
    await user.click(screen.getByRole('switch'))
    expect(await screen.findByRole('alert')).toHaveTextContent('The switch could not be saved. Try again.')
  })

  it('tells the rest of the app when AI is switched off', async () => {
    const heard = vi.fn()
    window.addEventListener(AI_CONFIG_CHANGED_EVENT, heard)
    getAIConfigMock
      .mockResolvedValueOnce(saved)
      .mockResolvedValue({ current_provider: 'none', available_providers: [], annotate_on_seed: false })
    const user = userEvent.setup()
    render(<AIConfigSection />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Disable' })).toBeInTheDocument())

    await user.click(screen.getByRole('button', { name: 'Disable' }))

    await waitFor(() => expect(heard).toHaveBeenCalledTimes(1))
    expect(saveAIConfigMock).toHaveBeenCalledWith({ provider: 'none' })
    await waitFor(() => expect(screen.getByText('Not Configured')).toBeInTheDocument())
    window.removeEventListener(AI_CONFIG_CHANGED_EVENT, heard)
  })

  it('the edit form says a key is already saved', async () => {
    expect(KEY_IS_SAVED_HINT).toBe('A key is already saved. Leave this empty to keep it.')
    const user = userEvent.setup()
    render(<AIConfigSection />)
    await waitFor(() => expect(screen.getByRole('button', { name: /Edit/ })).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /Edit/ }))
    expect(screen.getByText('A key is already saved. Leave this empty to keep it.')).toBeInTheDocument()

    // Pick another provider: the hint is about the saved provider only.
    await user.selectOptions(screen.getAllByRole('combobox')[0], 'claude')
    expect(screen.queryByText('A key is already saved. Leave this empty to keep it.')).not.toBeInTheDocument()
  })
})
