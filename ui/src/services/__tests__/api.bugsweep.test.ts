import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import {
  testClusterConnection,
  isTestClusterUnavailable,
  deregisterCluster,
  unadoptCluster,
  adoptClusters,
} from '../api'

/**
 * Integration-level tests for the V124 BUG-sweep fixes that live in the API
 * client layer. We mock `fetch` rather than the API helpers themselves so the
 * exact request body shape (which is what the backend depends on) is asserted.
 */

const TOKEN_KEY = 'sharko-auth-token'

function mockResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText: 'OK',
    json: async () => body,
  } as Response
}

describe('BUG-035: testClusterConnection structured 503 handling', () => {
  beforeEach(() => {
    sessionStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    sessionStorage.removeItem(TOKEN_KEY)
  })

  it('returns a typed "unavailable" result when backend returns 503 + error_code=no_secrets_backend', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(503, {
        error: 'Cluster connectivity test requires a secrets backend on the active connection. Configure one in Settings → Connections to enable testing.',
        error_code: 'no_secrets_backend',
        hint: 'configure a secrets backend on the active connection via Settings → Connections',
      }),
    )

    const result = await testClusterConnection('prod-eu')

    expect(fetchSpy).toHaveBeenCalledOnce()
    expect(isTestClusterUnavailable(result)).toBe(true)
    if (!isTestClusterUnavailable(result)) throw new Error('type narrowed wrong')
    expect(result.error_code).toBe('no_secrets_backend')
    expect(result.error).toMatch(/secrets backend/i)
    // BUG-035: the UI keys off `unavailable: true` so this contract must not
    // regress to throwing on 503.
    expect(result.unavailable).toBe(true)
  })

  it('throws for non-structured 503 responses (preserves error UX for other failures)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(503, { error: 'argocd unreachable' }),
    )

    await expect(testClusterConnection('prod-eu')).rejects.toThrow(/argocd unreachable/)
  })

  it('forwards a normal verify.Result body unchanged on 200', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, { reachable: true, success: true, server_version: 'v1.29.0' }),
    )

    const result = await testClusterConnection('prod-eu')
    expect(isTestClusterUnavailable(result)).toBe(false)
    expect((result as { reachable?: boolean }).reachable).toBe(true)
  })
})

describe('BUG-039: confirm dialogs send yes:true in request body', () => {
  beforeEach(() => {
    sessionStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    sessionStorage.removeItem(TOKEN_KEY)
  })

  it('deregisterCluster sends DELETE with body {"yes": true}', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, { status: 'success' }),
    )

    await deregisterCluster('prod-eu')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/clusters/prod-eu')
    expect(init.method).toBe('DELETE')
    // BUG-039: the backend handler rejects requests without `yes:true` with
    // HTTP 400 "confirmation required". The UI must always include the flag
    // in the body, not as a query parameter.
    expect(init.body).toBeDefined()
    const body = JSON.parse(init.body as string)
    expect(body.yes).toBe(true)
  })

  it('unadoptCluster posts to /unadopt with body {"yes": true}', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, { status: 'success', pr_url: 'https://example/pr/1' }),
    )

    await unadoptCluster('prod-eu')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    // BUG-039 root cause: the previous shape used DELETE /clusters/{name}
    // with ?unadopt=true, which routed to handleDeregisterCluster and 400'd
    // because the body lacked yes:true. Canonical handler is POST .../unadopt.
    expect(url).toContain('/clusters/prod-eu/unadopt')
    expect(init.method).toBe('POST')
    const body = JSON.parse(init.body as string)
    expect(body.yes).toBe(true)
  })

  it('adoptClusters does NOT send yes:true (AdoptClustersRequest has no Yes field)', async () => {
    // BUG-039 audit guard: keep this test honest about the asymmetry. The
    // backend AdoptClustersRequest does not require confirmation — adopt is
    // gated on per-cluster Stage1 verification, not a flag. If the backend
    // ever adds a Yes field, this test surfaces it so we update the UI.
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, { results: [] }),
    )

    await adoptClusters({ clusters: ['prod-eu'], auto_merge: true })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    const body = JSON.parse(init.body as string)
    expect(body.yes).toBeUndefined()
    expect(body.clusters).toEqual(['prod-eu'])
    expect(body.auto_merge).toBe(true)
  })
})
