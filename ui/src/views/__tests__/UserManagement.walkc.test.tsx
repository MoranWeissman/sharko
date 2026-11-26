import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { UserManagement } from '@/views/UserManagement'

/*
 * Walk 5 S6: after creating a user the page said "They must change it on
 * first login". Sharko does not force that, so the sentence was not true.
 * L4: a key that was never used showed "Jan 1, 1". L8: the operator line of
 * the role legend said "all except user management", which is too much.
 */
const listUsersMock = vi.fn()
const createUserMock = vi.fn()
const deleteUserMock = vi.fn()
const resetPasswordMock = vi.fn()
const listTokensMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    listUsers: () => listUsersMock(),
    createUser: (...a: unknown[]) => createUserMock(...a),
    deleteUser: (...a: unknown[]) => deleteUserMock(...a),
    resetPassword: (...a: unknown[]) => resetPasswordMock(...a),
    updateUser: vi.fn(),
  },
  listTokens: () => listTokensMock(),
  createToken: vi.fn(),
  revokeToken: vi.fn(),
  renewToken: vi.fn(),
}))

vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => ({ isAdmin: true, username: 'root-admin', role: 'admin' }),
}))

describe('UserManagement — sentences that are true', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    listUsersMock.mockResolvedValue([{ username: 'bob', enabled: true, role: 'viewer' }])
    listTokensMock.mockResolvedValue([
      {
        name: 'never-used', role: 'viewer', status: 'active',
        created_at: '2026-11-20T10:00:00Z', expires_at: '2027-02-18T10:00:00Z',
        last_used_at: '0001-01-01T00:00:00Z', expired: false, expiring_soon: false,
      },
    ])
  })

  it('the role legend does not promise an operator everything', async () => {
    render(<UserManagement />)
    await waitFor(() => expect(screen.getByText('bob')).toBeInTheDocument())
    expect(
      screen.getByText(
        '— register and adopt clusters, turn addons on and off, add addons to the catalog. Cannot remove things, change settings or manage users',
      ),
    ).toBeInTheDocument()
    expect(screen.queryByText(/all except user management/)).not.toBeInTheDocument()
  })

  it('a key that was never used says Never used', async () => {
    render(<UserManagement />)
    await waitFor(() => expect(screen.getByText('never-used')).toBeInTheDocument())
    expect(screen.getByText('Never used')).toBeInTheDocument()
    expect(screen.queryByText(/Jan 1, 1$/)).not.toBeInTheDocument()
  })

  it('the temporary password note says what really happens', async () => {
    resetPasswordMock.mockResolvedValue({ username: 'bob', temp_password: 'TempPass1234' })
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const user = userEvent.setup()
    render(<UserManagement />)
    await waitFor(() => expect(screen.getByText('bob')).toBeInTheDocument())

    await user.click(screen.getByTitle(/Reset password/i))
    expect(confirmSpy).toHaveBeenCalledWith(
      'Reset password for bob? They are logged out everywhere and need the new temporary password to get back in.',
    )
    expect(
      await screen.findByText('Share this with the user. They should change it after they log in, on the Account page.'),
    ).toBeInTheDocument()
    expect(screen.queryByText(/must change it on first login/)).not.toBeInTheDocument()
    confirmSpy.mockRestore()
  })

  it('deleting a user says what it ends', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const user = userEvent.setup()
    render(<UserManagement />)
    await waitFor(() => expect(screen.getByText('bob')).toBeInTheDocument())
    await user.click(screen.getByTitle(/Delete user/i))
    expect(confirmSpy).toHaveBeenCalledWith(
      'Delete user "bob"? This cannot be undone. They are logged out right away and the API keys they created stop working.',
    )
    expect(deleteUserMock).not.toHaveBeenCalled()
    confirmSpy.mockRestore()
  })
})
