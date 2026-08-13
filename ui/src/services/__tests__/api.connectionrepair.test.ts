import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { api } from '../api'

/**
 * R1-2 — what the browser actually SENDS for a connection repair.
 *
 * The panel test one layer up mocks api.repairConnection, so it proves the
 * commit on screen reaches the client call. It cannot see the request. That
 * blind spot is exactly where a real bug shipped: repairConnection called
 * fetchJSON (a GET-only helper that takes one argument) and handed it a
 * `{method: 'POST'}` options object, which fetchJSON silently dropped.
 * Every test stayed green; only the TypeScript compiler caught it, in CI,
 * on the image build.
 *
 * So these tests stub fetch and read the request that came out — the
 * method by name, the path, and the url-encoded reviewed_commit parameter.
 * Same shape as api.errorshape.test.ts: the write helper (postJSON) is not
 * exported, so it's driven through the exported api.repairConnection.
 */

const TOKEN_KEY = 'sharko-auth-token'

function okResponse(body: unknown): Response {
  return {
    status: 200,
    ok: true,
    statusText: 'OK',
    json: async () => body,
  } as Response
}

/** Minimal body — these tests are about the request, not the reply. */
const repairReply = {
  cluster: 'prod-eu',
  repaired: true,
  scope_applied: 'full_connection',
  message: 'Repaired',
}

describe('api.repairConnection — the request that actually goes out (R1-2)', () => {
  beforeEach(() => {
    localStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    localStorage.removeItem(TOKEN_KEY)
  })

  it('sends POST, spelled out — never a GET', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(okResponse(repairReply))

    await api.repairConnection('prod-eu', 'abc123')

    expect(fetchSpy).toHaveBeenCalledTimes(1)
    const [, init] = fetchSpy.mock.calls[0]
    // Asserted by name on purpose. A GET-shaped call is the bug that
    // shipped, so nothing here is allowed to infer the method.
    expect(init?.method).toBe('POST')
  })

  it('posts to /clusters/<name>/connection-repair with the reviewed commit in the query string', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(okResponse(repairReply))

    await api.repairConnection('prod-eu', 'abc123')

    const [url, init] = fetchSpy.mock.calls[0]
    expect(init?.method).toBe('POST')
    expect(url).toBe('/api/v1/clusters/prod-eu/connection-repair?reviewed_commit=abc123')
  })

  it('url-encodes a reviewed commit that needs encoding', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(okResponse(repairReply))

    // A value with characters that would change the meaning of the query
    // string if they went through raw: a slash, a space, a plus and an
    // equals sign.
    const awkward = 'refs/heads/fix a+b=c'
    await api.repairConnection('prod-eu', awkward)

    const [url, init] = fetchSpy.mock.calls[0]
    expect(init?.method).toBe('POST')
    expect(url).toBe(
      '/api/v1/clusters/prod-eu/connection-repair?reviewed_commit=refs%2Fheads%2Ffix%20a%2Bb%3Dc',
    )
    // Belt and braces: the raw value must not appear anywhere in the URL.
    expect(String(url)).not.toContain(awkward)
  })

  it('sends no body — the reviewed commit rides in the query string only', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(okResponse(repairReply))

    await api.repairConnection('prod-eu', 'abc123')

    const [, init] = fetchSpy.mock.calls[0]
    expect(init?.body).toBeUndefined()
  })
})
