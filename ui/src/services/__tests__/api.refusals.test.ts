import { describe, it, expect, beforeEach, vi } from 'vitest'
import { api, testClusterConnection, ApiError } from '../api'

// Walk 5: a wrong CURRENT password logged the person out; a role refusal read
// as an internal action name. Walks 5/6/10: a refused or turned-away call must
// reach the page as a typed error, not a bare message.

const TOKEN_KEY = 'sharko-auth-token'

function res(status: number, body: unknown): Response {
  const r = {
    status,
    ok: status >= 200 && status < 300,
    statusText: 'x',
    json: async () => body,
    clone: () => res(status, body),
  }
  return r as unknown as Response
}

describe('api.updatePassword', () => {
  const reload = vi.fn()
  beforeEach(() => {
    localStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
    reload.mockReset()
    Object.defineProperty(window, 'location', { value: { ...window.location, reload }, writable: true })
  })

  it('a wrong current password stays logged in and says what is wrong', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(res(401, { error: 'current password is incorrect' }))
    await expect(api.updatePassword('bad', 'a-new-long-password')).rejects.toMatchObject({
      message: 'The current password is wrong. Nothing was changed.',
      status: 401,
    })
    expect(localStorage.getItem(TOKEN_KEY)).toBe('test-token')
    expect(reload).not.toHaveBeenCalled()
  })

  it('a 401 that means the login ended still logs out', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(res(401, { error: 'invalid or expired session' }))
    await expect(api.updatePassword('x', 'a-new-long-password')).rejects.toThrow('Session expired')
    expect(localStorage.getItem(TOKEN_KEY)).toBeNull()
    expect(reload).toHaveBeenCalled()
  })
})

describe('refusals keep their type', () => {
  beforeEach(() => {
    localStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  it('a 403 role refusal reads as "not allowed", never as the internal action text', () => {
    const e = new ApiError(403, { error: "action 'cluster.test' requires role 'operator', you have 'viewer'" }, 'Forbidden')
    expect(e.message).toBe('Your role is not allowed to do this. Ask an operator or an admin.')
    expect(e.message).not.toMatch(/cluster\.test|requires role/)
    expect(e.status).toBe(403)
  })

  it('other 403 sentences are left alone', () => {
    expect(new ApiError(403, { error: 'Resource not accessible' }, 'Forbidden').message).toBe('Resource not accessible')
  })

  it('the cluster test throws an ApiError carrying the status and code (so a page can tell 403 and 429 apart)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(res(429, { error: 'rate limit exceeded', code: 'sharko_rate_limit' }))
    const err = await testClusterConnection('c1').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(429)
    expect(err.code).toBe('sharko_rate_limit')
  })
})
