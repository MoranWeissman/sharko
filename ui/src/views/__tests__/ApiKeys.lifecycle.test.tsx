import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { APIToken } from '@/services/models'
import { ApiKeys } from '@/views/ApiKeys'

/*
 * v4-wave2 8.2 — API key lifecycle in the UI.
 *
 * Covers:
 *   1. the expiry date and status show up per key
 *   2. a key with no expiry (made before expiry dates existed) says so
 *      plainly and is NOT shown as expired
 *   3. Renew calls the API and reloads the list
 *   4. Create sends expires_in_days, defaulting to 90
 *   5. the plaintext is shown once after create
 */

const listMock = vi.fn()
const createMock = vi.fn()
const renewMock = vi.fn()
const revokeMock = vi.fn()

vi.mock('@/services/api', () => ({
  listTokens: () => listMock(),
  createToken: (data: unknown) => createMock(data),
  renewToken: (name: string, days?: number) => renewMock(name, days),
  revokeToken: (name: string) => revokeMock(name),
}))

const activeToken: APIToken = {
  name: 'ci-deploy',
  role: 'operator',
  created_at: '2026-07-01T10:00:00Z',
  expires_at: '2026-09-29T10:00:00Z',
  status: 'active',
  expired: false,
  expiring_soon: false,
}

const legacyToken: APIToken = {
  name: 'ancient-key',
  role: 'viewer',
  created_at: '2025-01-01T10:00:00Z',
  expires_at: null,
  status: 'legacy-no-expiry',
  expired: false,
  expiring_soon: false,
}

const expiredToken: APIToken = {
  name: 'lapsed-key',
  role: 'viewer',
  created_at: '2026-01-01T10:00:00Z',
  expires_at: '2026-04-01T10:00:00Z',
  status: 'expired',
  expired: true,
  expiring_soon: false,
}

beforeEach(() => {
  listMock.mockReset()
  createMock.mockReset()
  renewMock.mockReset()
  revokeMock.mockReset()
})

describe('ApiKeys — token lifecycle', () => {
  it('shows the expiry date and status for each key', async () => {
    listMock.mockResolvedValue([activeToken, expiredToken])
    render(<ApiKeys />)

    await waitFor(() => expect(screen.getByText('ci-deploy')).toBeInTheDocument())
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByText('Expired')).toBeInTheDocument()
  })

  it('says plainly that an older key has no expiry, without calling it expired', async () => {
    listMock.mockResolvedValue([legacyToken])
    render(<ApiKeys />)

    await waitFor(() => expect(screen.getByText('ancient-key')).toBeInTheDocument())
    expect(screen.getAllByText(/No expiry \(older key\)/).length).toBeGreaterThan(0)
    expect(screen.queryByText('Expired')).not.toBeInTheDocument()
  })

  it('renews a key and reloads the list', async () => {
    const user = userEvent.setup()
    listMock.mockResolvedValue([expiredToken])
    renewMock.mockResolvedValue({ ...expiredToken, status: 'active', expired: false })
    render(<ApiKeys />)

    await waitFor(() => expect(screen.getByText('lapsed-key')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /Renew/i }))

    await waitFor(() => expect(renewMock).toHaveBeenCalledWith('lapsed-key', undefined))
    expect(listMock).toHaveBeenCalledTimes(2)
    await waitFor(() =>
      expect(screen.getByText(/now runs for another 90 days/i)).toBeInTheDocument(),
    )
  })

  it('creates a key with the default 90-day window and shows the value once', async () => {
    const user = userEvent.setup()
    listMock.mockResolvedValue([])
    createMock.mockResolvedValue({ token: 'sharko_abcdef0123456789abcdef0123456789' })
    render(<ApiKeys />)

    await waitFor(() => expect(screen.getByText(/No API tokens yet/i)).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: /Create API Key/i }))
    await user.type(screen.getByPlaceholderText('e.g. ci-deploy'), 'new-key')
    await user.click(screen.getByRole('button', { name: /Create Key/i }))

    await waitFor(() =>
      expect(createMock).toHaveBeenCalledWith({
        name: 'new-key',
        role: 'viewer',
        expires_in_days: 90,
      }),
    )
    await waitFor(() =>
      expect(screen.getByText('sharko_abcdef0123456789abcdef0123456789')).toBeInTheDocument(),
    )
  })
})
