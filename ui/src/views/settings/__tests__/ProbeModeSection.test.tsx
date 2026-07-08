import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ProbeModeSection } from '@/views/settings/ProbeModeSection'

/*
 * V2-cleanup-85.4-frontend — Settings → Connectivity Probe.
 *
 *   1. loading -> renders both options with the current value selected
 *   2. selecting the other option PUTs the new value and shows it selected
 *   3. save failure reverts the optimistic selection and shows an error toast
 *   4. load failure shows an error + retry
 */

const getMock = vi.fn()
const setMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getProbeMode: () => getMock(),
    setProbeMode: (mode: string) => setMock(mode),
  },
}))

const showToastMock = vi.fn()
vi.mock('@/components/ToastNotification', () => ({
  showToast: (...args: unknown[]) => showToastMock(...args),
}))

describe('ProbeModeSection', () => {
  beforeEach(() => {
    getMock.mockReset()
    setMock.mockReset()
    showToastMock.mockReset()
  })

  it('renders both options and shows the current value selected', async () => {
    getMock.mockResolvedValue({ probe_mode: 'check-app' })
    render(<ProbeModeSection />)

    expect(screen.getByText(/Loading connectivity probe setting/i)).toBeInTheDocument()

    await waitFor(() =>
      expect(screen.getByText('Deploy a temporary check app (default)')).toBeInTheDocument(),
    )
    expect(screen.getByText('Run an API test — deploy nothing')).toBeInTheDocument()
    // Helper text for both options is always visible.
    expect(screen.getByText(/lands a tiny throwaway app/i)).toBeInTheDocument()
    expect(screen.getByText(/never deploys anything to your cluster/i)).toBeInTheDocument()

    const checkAppRadio = screen.getByRole('radio', { name: /Deploy a temporary check app/i })
    const apiTestRadio = screen.getByRole('radio', { name: /Run an API test/i })
    expect(checkAppRadio).toBeChecked()
    expect(apiTestRadio).not.toBeChecked()
  })

  it('PUTs the new value when the other option is selected, and shows a success toast', async () => {
    getMock.mockResolvedValue({ probe_mode: 'check-app' })
    setMock.mockResolvedValue({ probe_mode: 'api-test' })
    const user = userEvent.setup()
    render(<ProbeModeSection />)

    await waitFor(() => expect(screen.getByRole('radio', { name: /Run an API test/i })).toBeInTheDocument())

    await user.click(screen.getByRole('radio', { name: /Run an API test/i }))

    await waitFor(() => expect(setMock).toHaveBeenCalledWith('api-test'))
    await waitFor(() => expect(screen.getByRole('radio', { name: /Run an API test/i })).toBeChecked())
    expect(screen.getByRole('radio', { name: /Deploy a temporary check app/i })).not.toBeChecked()
    expect(showToastMock).toHaveBeenCalledWith('Connectivity probe setting saved', 'success')
  })

  it('reverts the selection and shows an error toast when the save fails', async () => {
    getMock.mockResolvedValue({ probe_mode: 'check-app' })
    setMock.mockRejectedValue(new Error('boom'))
    const user = userEvent.setup()
    render(<ProbeModeSection />)

    await waitFor(() => expect(screen.getByRole('radio', { name: /Run an API test/i })).toBeInTheDocument())

    await user.click(screen.getByRole('radio', { name: /Run an API test/i }))

    await waitFor(() => expect(setMock).toHaveBeenCalledWith('api-test'))
    await waitFor(() =>
      expect(screen.getByRole('radio', { name: /Deploy a temporary check app/i })).toBeChecked(),
    )
    expect(screen.getByRole('radio', { name: /Run an API test/i })).not.toBeChecked()
    expect(showToastMock).toHaveBeenCalledWith(
      expect.stringContaining('Failed to save connectivity probe setting'),
      'error',
    )
  })

  it('shows an error with retry when the initial load fails', async () => {
    getMock.mockRejectedValueOnce(new Error('network down')).mockResolvedValueOnce({ probe_mode: 'check-app' })
    const user = userEvent.setup()
    render(<ProbeModeSection />)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('network down')

    await user.click(screen.getByRole('button', { name: /Retry/i }))

    await waitFor(() =>
      expect(screen.getByText('Deploy a temporary check app (default)')).toBeInTheDocument(),
    )
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
