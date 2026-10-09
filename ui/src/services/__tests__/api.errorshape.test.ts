import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { ApiError, unadoptCluster } from '../api'

/**
 * error review package 2 — api.ts used to have six near-identical copies of
 * `const err = await res.json().catch(...); throw new Error(err.error ||
 * res.statusText)`, which dropped everything the server's error boundary
 * now sends beyond the headline: status, code, cause, hint, problems[].
 *
 * These tests exercise the shared throwApiError path via unadoptCluster
 * (which calls the unexported postJSON helper directly) rather than
 * fetchJSON/postJSON/etc. directly, since those aren't exported — the six
 * call sites are identical, so any one write helper proves the shared path.
 * (Not every exported cluster helper routes through the shared helpers —
 * deregisterCluster and deleteOrphanCluster build their own raw fetch calls
 * for response-shape reasons unrelated to this change, so they're
 * deliberately not used here.)
 */

const TOKEN_KEY = 'sharko-auth-token'

function mockResponse(status: number, body: unknown, statusText = 'Bad Request'): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText,
    json: async () => body,
  } as Response
}

function mockNonJSONResponse(status: number, statusText: string): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText,
    json: async (): Promise<unknown> => {
      throw new SyntaxError('Unexpected token in JSON')
    },
  } as Response
}

describe('api.ts error boundary — ApiError', () => {
  beforeEach(() => {
    localStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    localStorage.removeItem(TOKEN_KEY)
  })

  it('populates status/code/cause/hint/problems from a fully-shaped body, .message = headline', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(502, {
        error: 'Sharko could not verify the ArgoCD connection',
        cause: 'invalid ArgoCD token — check that the token is correct and not expired',
        hint: "check the ArgoCD token in Settings → Connections and replace it if it's expired.",
        code: 'ERR_AUTH',
      }),
    )

    let caught: unknown
    try {
      await unadoptCluster('prod-eu')
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(ApiError)
    const err = caught as ApiError
    expect(err.message).toBe('Sharko could not verify the ArgoCD connection')
    expect(err.status).toBe(502)
    expect(err.code).toBe('ERR_AUTH')
    expect(err.cause).toBe('invalid ArgoCD token — check that the token is correct and not expired')
    expect(err.hint).toContain('Settings')
    expect(err.body.error).toBe('Sharko could not verify the ArgoCD connection')
  })

  it('carries problems[] through untouched (writeCodedError-style refusals)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(422, {
        error: 'addon config is incomplete',
        code: 'incomplete_entry',
        problems: ['chart is required', 'namespace is required'],
      }),
    )

    let caught: unknown
    try {
      await unadoptCluster('prod-eu')
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(ApiError)
    const err = caught as ApiError
    expect(err.problems).toEqual(['chart is required', 'namespace is required'])
    expect(err.code).toBe('incomplete_entry')
  })

  it('behaves like today when the body has no cause/hint/code (fields simply undefined)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(400, { error: 'name is required' }),
    )

    let caught: unknown
    try {
      await unadoptCluster('prod-eu')
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(ApiError)
    const err = caught as ApiError
    expect(err.message).toBe('name is required')
    expect(err.cause).toBeUndefined()
    expect(err.hint).toBeUndefined()
    expect(err.code).toBeUndefined()
    // Existing ~90 render sites all do `err instanceof Error ? err.message
    // : '...'` — ApiError must satisfy that check.
    expect(err instanceof Error).toBe(true)
  })

  it('falls back to statusText when the body is not valid JSON', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockNonJSONResponse(503, 'Service Unavailable'),
    )

    let caught: unknown
    try {
      await unadoptCluster('prod-eu')
    } catch (e) {
      caught = e
    }

    expect(caught).toBeInstanceOf(ApiError)
    const err = caught as ApiError
    expect(err.message).toBe('Service Unavailable')
    expect(err.status).toBe(503)
    expect(err.cause).toBeUndefined()
  })
})
