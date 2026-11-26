import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthContext } from '@/hooks/useAuth'
import { UserInfo } from '@/views/UserInfo'

/*
 * Walk 5 S1: this page printed "Username: admin" and "Issuer: local" for
 * every user. It now shows who is logged in and their role.
 * Walk 5 L3: a wrong current password logged the user out with no message.
 */
const updatePasswordMock = vi.fn()
vi.mock('@/services/api', () => ({
  api: { updatePassword: (...args: unknown[]) => updatePasswordMock(...args) },
}))

function renderAs(username: string, role: string) {
  return render(
    <AuthContext.Provider
      value={{
        token: 't', username, role,
        login: vi.fn(), logout: vi.fn(),
        isAuthenticated: true, isAdmin: role === 'admin', loading: false, error: null,
      }}
    >
      <UserInfo />
    </AuthContext.Provider>,
  )
}

describe('UserInfo — shows the logged-in user', () => {
  beforeEach(() => vi.clearAllMocks())

  it('shows a viewer as that viewer, not as admin', () => {
    renderAs('walk5-viewer', 'viewer')
    expect(screen.getByTestId('user-info-username')).toHaveTextContent('walk5-viewer')
    expect(screen.getByTestId('user-info-role')).toHaveTextContent('viewer')
    expect(screen.getByText('Role:')).toBeInTheDocument()
    expect(screen.queryByText('admin')).not.toBeInTheDocument()
    expect(screen.queryByText(/Issuer/)).not.toBeInTheDocument()
    expect(screen.queryByText(/local/)).not.toBeInTheDocument()
  })

  it('shows an operator as an operator', () => {
    renderAs('olga', 'operator')
    expect(screen.getByTestId('user-info-username')).toHaveTextContent('olga')
    expect(screen.getByTestId('user-info-role')).toHaveTextContent('operator')
  })

  it('says what a password change does to other logins', async () => {
    updatePasswordMock.mockResolvedValue({ status: 'password updated' })
    const user = userEvent.setup()
    const { container } = renderAs('bob', 'viewer')
    const inputs = container.querySelectorAll('input[type="password"]')
    await user.type(inputs[0], 'the-old-password')
    await user.type(inputs[1], 'a-brand-new-long-password')
    await user.type(inputs[2], 'a-brand-new-long-password')
    await user.click(screen.getByRole('button', { name: 'Save Password' }))
    await waitFor(() =>
      expect(
        screen.getByText('Password updated. You stay logged in here. Every other place where you were logged in has been logged out.'),
      ).toBeInTheDocument(),
    )
    expect(screen.queryByText(/Use the new password on next login/)).not.toBeInTheDocument()
  })

  it('shows the message of a wrong current password and stays on the page', async () => {
    updatePasswordMock.mockRejectedValue(new Error('The current password is wrong. Nothing was changed.'))
    const user = userEvent.setup()
    const { container } = renderAs('bob', 'viewer')
    const inputs = container.querySelectorAll('input[type="password"]')
    await user.type(inputs[0], 'not-the-password')
    await user.type(inputs[1], 'a-brand-new-long-password')
    await user.type(inputs[2], 'a-brand-new-long-password')
    await user.click(screen.getByRole('button', { name: 'Save Password' }))
    await waitFor(() =>
      expect(screen.getByText('The current password is wrong. Nothing was changed.')).toBeInTheDocument(),
    )
  })
})
