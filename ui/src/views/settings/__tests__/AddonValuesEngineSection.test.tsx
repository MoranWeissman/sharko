import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AddonValuesEngineSection } from '@/views/settings/AddonValuesEngineSection'

/*
 * gitops-proud P4-I (D2) — Settings → Addon Values Engine, mirroring
 * InlineCredentialsSection's own test shape:
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
    getAddonValuesEngineEnabled: () => getMock(),
    setAddonValuesEngineEnabled: (enabled: boolean) => setMock(enabled),
  },
}))

const showToastMock = vi.fn()
vi.mock('@/components/ToastNotification', () => ({
  showToast: (...args: unknown[]) => showToastMock(...args),
}))

describe('AddonValuesEngineSection', () => {
  beforeEach(() => {
    getMock.mockReset()
    setMock.mockReset()
    showToastMock.mockReset()
  })

  it('renders the toggle "On" when the setting is true', async () => {
    getMock.mockResolvedValue({ addon_values_engine_enabled: true })
    render(<AddonValuesEngineSection />)

    expect(screen.getByText(/Loading addon values engine setting/i)).toBeInTheDocument()

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Apply addon secret values/i })).toBeInTheDocument(),
    )
    const toggle = screen.getByRole('switch', { name: /Apply addon secret values/i })
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText('On')).toBeInTheDocument()
    expect(screen.getByText(/checks and applies addon secret values/i)).toBeInTheDocument()
  })

  it('renders the toggle "Off" when the setting is false, and names Sharko\'s own job as unaffected', async () => {
    getMock.mockResolvedValue({ addon_values_engine_enabled: false })
    render(<AddonValuesEngineSection />)

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Apply addon secret values/i })).toBeInTheDocument(),
    )
    const toggle = screen.getByRole('switch', { name: /Apply addon secret values/i })
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByText('Off')).toBeInTheDocument()
    expect(screen.getByText(/not checking or applying addon secret values/i)).toBeInTheDocument()
    // The section's own description names the boundary: connection secrets
    // are Sharko's own job and this switch does not touch them.
    expect(screen.getByText(/Cluster connection secrets are Sharko's own job/i)).toBeInTheDocument()
  })

  it('PUTs the flipped value when the toggle is clicked, and shows a success toast', async () => {
    getMock.mockResolvedValue({ addon_values_engine_enabled: true })
    setMock.mockResolvedValue({ addon_values_engine_enabled: false })
    const user = userEvent.setup()
    render(<AddonValuesEngineSection />)

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Apply addon secret values/i })).toBeInTheDocument(),
    )

    await user.click(screen.getByRole('switch', { name: /Apply addon secret values/i }))

    await waitFor(() => expect(setMock).toHaveBeenCalledWith(false))
    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Apply addon secret values/i })).toHaveAttribute(
        'aria-checked',
        'false',
      ),
    )
    expect(showToastMock).toHaveBeenCalledWith('Addon values engine setting saved', 'success')
  })

  it('reverts the toggle and shows an error toast when the save fails', async () => {
    getMock.mockResolvedValue({ addon_values_engine_enabled: true })
    setMock.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    render(<AddonValuesEngineSection />)

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Apply addon secret values/i })).toBeInTheDocument(),
    )

    await user.click(screen.getByRole('switch', { name: /Apply addon secret values/i }))

    await waitFor(() => expect(setMock).toHaveBeenCalledWith(false))
    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Apply addon secret values/i })).toHaveAttribute(
        'aria-checked',
        'true',
      ),
    )
    expect(showToastMock).toHaveBeenCalledWith(
      expect.stringContaining('Failed to save addon values engine setting'),
      'error',
    )
  })

  it('shows an error with retry when the initial load fails', async () => {
    getMock
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce({ addon_values_engine_enabled: true })
    const user = userEvent.setup()
    render(<AddonValuesEngineSection />)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('network down')

    await user.click(screen.getByRole('button', { name: /Retry/i }))

    await waitFor(() =>
      expect(screen.getByRole('switch', { name: /Apply addon secret values/i })).toBeInTheDocument(),
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
