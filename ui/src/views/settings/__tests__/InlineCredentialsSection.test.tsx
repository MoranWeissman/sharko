import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { InlineCredentialsSection } from '@/views/settings/InlineCredentialsSection'

/*
 * V2-cleanup-89.6 — Settings → Inline Credentials.
 *
 *   1. loading -> renders the toggle reflecting the current value
 *   2. toggling flips the value, PUTs it, and shows a success toast
 *   3. toggle failure reverts the optimistic flip and shows an error toast
 *   4. load failure shows an error + retry
 */

const getMock = vi.fn()
const setMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getAllowInlineCredentials: () => getMock(),
    setAllowInlineCredentials: (allow: boolean) => setMock(allow),
  },
}))

const showToastMock = vi.fn()
vi.mock('@/components/ToastNotification', () => ({
  showToast: (...args: unknown[]) => showToastMock(...args),
}))

describe('InlineCredentialsSection', () => {
  beforeEach(() => {
    getMock.mockReset()
    setMock.mockReset()
    showToastMock.mockReset()
  })

  it('renders the toggle "on" (Allowed) when the setting is true', async () => {
    getMock.mockResolvedValue({ allow_inline_credentials: true })
    render(<InlineCredentialsSection />)

    expect(screen.getByText(/Loading inline credentials setting/i)).toBeInTheDocument()

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Allow pasting credentials/i })).toBeInTheDocument(),
    )
    const toggle = screen.getByRole('switch', { name: /Allow pasting credentials/i })
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText('Allowed')).toBeInTheDocument()
    expect(screen.getByText(/Operators can paste a kubeconfig/i)).toBeInTheDocument()
  })

  it('renders the toggle "off" (Forbidden) when the setting is false', async () => {
    getMock.mockResolvedValue({ allow_inline_credentials: false })
    render(<InlineCredentialsSection />)

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Allow pasting credentials/i })).toBeInTheDocument(),
    )
    const toggle = screen.getByRole('switch', { name: /Allow pasting credentials/i })
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByText('Forbidden')).toBeInTheDocument()
    expect(screen.getByText(/option is hidden/i)).toBeInTheDocument()
  })

  it('PUTs the flipped value when the toggle is clicked, and shows a success toast', async () => {
    getMock.mockResolvedValue({ allow_inline_credentials: true })
    setMock.mockResolvedValue({ allow_inline_credentials: false })
    const user = userEvent.setup()
    render(<InlineCredentialsSection />)

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Allow pasting credentials/i })).toBeInTheDocument(),
    )

    await user.click(screen.getByRole('switch', { name: /Allow pasting credentials/i }))

    await waitFor(() => expect(setMock).toHaveBeenCalledWith(false))
    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Allow pasting credentials/i })).toHaveAttribute(
        'aria-checked',
        'false',
      ),
    )
    expect(showToastMock).toHaveBeenCalledWith('Inline credentials setting saved', 'success')
  })

  it('reverts the toggle and shows an error toast when the save fails', async () => {
    getMock.mockResolvedValue({ allow_inline_credentials: true })
    setMock.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    render(<InlineCredentialsSection />)

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Allow pasting credentials/i })).toBeInTheDocument(),
    )

    await user.click(screen.getByRole('switch', { name: /Allow pasting credentials/i }))

    await waitFor(() => expect(setMock).toHaveBeenCalledWith(false))
    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Allow pasting credentials/i })).toHaveAttribute(
        'aria-checked',
        'true',
      ),
    )
    expect(showToastMock).toHaveBeenCalledWith(
      expect.stringContaining('Failed to save inline credentials setting'),
      'error',
    )
  })

  it('shows an error with retry when the initial load fails', async () => {
    getMock
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce({ allow_inline_credentials: true })
    const user = userEvent.setup()
    render(<InlineCredentialsSection />)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('network down')

    await user.click(screen.getByRole('button', { name: /Retry/i }))

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Allow pasting credentials/i })).toBeInTheDocument(),
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
