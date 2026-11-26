import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { AuthProvider, useAuth } from '@/hooks/useAuth'

/*
 * Walk 5: "Log out" only forgot the token in the browser. The server was
 * never told, so a copy of the token went on working for up to 24 hours.
 */
function Consumer() {
  const { logout, token } = useAuth()
  return (
    <div>
      <span data-testid="token">{token ?? 'none'}</span>
      <button data-testid="logout-btn" onClick={() => logout()}>logout</button>
    </div>
  )
}

describe('useAuth — log out tells the server', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
  })
  afterEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it('calls POST /api/v1/auth/logout with the token, then forgets it', async () => {
    localStorage.setItem('sharko-auth-token', 'live-token')
    localStorage.setItem('sharko-auth-user', 'bob')
    localStorage.setItem('sharko-auth-role', 'viewer')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )

    render(<AuthProvider><Consumer /></AuthProvider>)
    await waitFor(() => expect(screen.getByTestId('token').textContent).toBe('live-token'))

    fireEvent.click(screen.getByTestId('logout-btn'))
    await waitFor(() => expect(screen.getByTestId('token').textContent).toBe('none'))

    const logoutCalls = fetchMock.mock.calls.filter(([url]) => url === '/api/v1/auth/logout')
    expect(logoutCalls).toHaveLength(1)
    const init = logoutCalls[0][1] as RequestInit
    expect(init.method).toBe('POST')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer live-token')
    expect(localStorage.getItem('sharko-auth-token')).toBeNull()
  })

  it('still logs out in the browser when the server cannot be reached', async () => {
    localStorage.setItem('sharko-auth-token', 'live-token')
    vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
      if (url === '/api/v1/auth/logout') return Promise.reject(new Error('offline'))
      return Promise.resolve(new Response('{}', { status: 200 }))
    })

    render(<AuthProvider><Consumer /></AuthProvider>)
    await waitFor(() => expect(screen.getByTestId('token').textContent).toBe('live-token'))
    fireEvent.click(screen.getByTestId('logout-btn'))
    await waitFor(() => expect(screen.getByTestId('token').textContent).toBe('none'))
    expect(localStorage.getItem('sharko-auth-token')).toBeNull()
  })

  it('does not call the server when nobody is logged in', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 200 }))
    render(<AuthProvider><Consumer /></AuthProvider>)
    fireEvent.click(screen.getByTestId('logout-btn'))
    expect(fetchMock.mock.calls.filter(([url]) => url === '/api/v1/auth/logout')).toHaveLength(0)
  })
})
