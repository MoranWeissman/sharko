import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, WRONG_CURRENT_PASSWORD_MESSAGE } from '@/services/api'

/*
 * Walk 5 L3: the server answers a wrong current password with a 401. The
 * shared POST helper treats every 401 as "your login ended", so a typing
 * mistake threw the user out to the login screen with no message.
 */
describe('api.updatePassword', () => {
  const reload = vi.fn()
  beforeEach(() => {
    localStorage.setItem('sharko-auth-token', 'live-token')
    Object.defineProperty(window, 'location', { value: { ...window.location, reload }, writable: true })
    reload.mockClear()
  })
  afterEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it('pins the sentence', () => {
    expect(WRONG_CURRENT_PASSWORD_MESSAGE).toBe('The current password is wrong. Nothing was changed.')
  })

  it('a wrong current password is an error on the form, not a log out', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'current password is incorrect' }), { status: 401 }),
    )
    await expect(api.updatePassword('wrong', 'a-brand-new-long-password')).rejects.toThrow(
      'The current password is wrong. Nothing was changed.',
    )
    expect(localStorage.getItem('sharko-auth-token')).toBe('live-token')
    expect(reload).not.toHaveBeenCalled()
  })

  it('any other 401 still means the login ended', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }),
    )
    await expect(api.updatePassword('x', 'a-brand-new-long-password')).rejects.toThrow('Session expired')
    expect(localStorage.getItem('sharko-auth-token')).toBeNull()
    expect(reload).toHaveBeenCalled()
  })

  it('sends the two passwords to the right place', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ status: 'password updated' }), { status: 200 }),
    )
    await expect(api.updatePassword('old', 'a-brand-new-long-password')).resolves.toEqual({ status: 'password updated' })
    const [url, init] = fetchMock.mock.calls[0]
    expect(String(url)).toContain('/auth/update-password')
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({
      current_password: 'old',
      new_password: 'a-brand-new-long-password',
    })
  })
})
