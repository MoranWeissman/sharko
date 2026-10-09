import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { Login } from '@/views/Login'

const mockLogin = vi.fn()
vi.mock('@/hooks/useAuth', () => ({
  useAuth: () => ({
    login: mockLogin,
    error: null,
  }),
}))

describe('Login footer version', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('renders live server version in the footer when /api/v1/health succeeds', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ status: 'ok', version: '1.23.0-pre.0' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )

    render(<Login />)

    await waitFor(() => {
      expect(screen.getByText('Sharko v1.23.0-pre.0')).toBeInTheDocument()
    })
    expect(fetchMock).toHaveBeenCalledWith('/api/v1/health')
  })

  it('renders dash placeholder when /api/v1/health fails', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('network down'))

    render(<Login />)

    // While the request is in-flight (or after rejection) the footer must
    // never display a stale hard-coded version. Dash placeholder always.
    await waitFor(() => {
      expect(screen.getByText('Sharko v—')).toBeInTheDocument()
    })
    expect(screen.queryByText(/Sharko v1\.0\.0/)).not.toBeInTheDocument()
  })

  it('renders dash placeholder when /api/v1/health returns non-OK', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('server error', { status: 500 }),
    )

    render(<Login />)

    await waitFor(() => {
      expect(screen.getByText('Sharko v—')).toBeInTheDocument()
    })
  })

  it('login form still functions independently of version fetch', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ status: 'ok', version: '1.23.0-pre.0' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    mockLogin.mockResolvedValue(undefined)

    render(<Login />)

    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'admin' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'secret' } })
    fireEvent.click(screen.getByRole('button', { name: /sign in/i }))

    await waitFor(() => {
      expect(mockLogin).toHaveBeenCalledWith('admin', 'secret')
    })
  })
})
