import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import {
  testClusterConnection,
  isTestClusterUnavailable,
  deregisterCluster,
  unadoptCluster,
  adoptClusters,
  api,
  extractArgocdVersionString,
} from '../api'

/**
 * Integration-level tests for the V124 BUG-sweep fixes that live in the API
 * client layer. We mock `fetch` rather than the API helpers themselves so the
 * exact request body shape (which is what the backend depends on) is asserted.
 *
 * The auth token is seeded into localStorage (not sessionStorage) — the API
 * client reads it via `authStorage.getToken()`, which is backed by
 * localStorage so a session survives "open in new tab" (see
 * ui/src/lib/authStorage.ts).
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
    localStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    localStorage.removeItem(TOKEN_KEY)
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
    localStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    localStorage.removeItem(TOKEN_KEY)
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

describe('dashboard-crash: /config argocd.version arrives as ArgoCD\'s full version object, not a string', () => {
  beforeEach(() => {
    localStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    localStorage.removeItem(TOKEN_KEY)
  })

  // This is the real wire shape: internal/api/system.go's handleGetConfig
  // stores the map returned by internal/argocd/client.go's GetVersion
  // (ArgoCD's /api/version response) verbatim into argocd.version. It is
  // never a plain string. A UI that types this field as `string` and hands
  // it straight to JSX white-screens (React error #31: object as child).
  const realArgocdVersionPayload = {
    BuildDate: '2026-06-01T00:00:00Z',
    Compiler: 'gc',
    GitCommit: 'abc123',
    GitTag: 'v2.11.0',
    GitTreeState: 'clean',
    GoVersion: 'go1.22.0',
    HelmVersion: 'v3.14.0',
    JsonnetVersion: 'v0.20.0',
    KubectlVersion: 'v1.29.0',
    KustomizeVersion: 'v5.3.0',
    Platform: 'linux/amd64',
    Version: 'v2.11.0',
  }

  it('getConfig() extracts a plain version string from the real object payload', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, {
        repo_paths: { cluster_values: '', global_values: '', charts: '', bootstrap: '' },
        gitops: { pr_auto_merge: false, branch_prefix: '', commit_prefix: '', base_branch: '' },
        argocd: { connected: true, version: realArgocdVersionPayload },
      }),
    )

    const config = await api.getConfig()

    expect(config.argocd.connected).toBe(true)
    expect(config.argocd.version).toBe('v2.11.0')
    expect(typeof config.argocd.version).toBe('string')
  })

  it('getConfig() still accepts a plain string for back-compat', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, {
        repo_paths: { cluster_values: '', global_values: '', charts: '', bootstrap: '' },
        gitops: { pr_auto_merge: false, branch_prefix: '', commit_prefix: '', base_branch: '' },
        argocd: { connected: true, version: '2.11.0' },
      }),
    )

    const config = await api.getConfig()
    expect(config.argocd.version).toBe('2.11.0')
  })

  it('getConfig() returns undefined version when disconnected (no version key at all)', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, {
        repo_paths: { cluster_values: '', global_values: '', charts: '', bootstrap: '' },
        gitops: { pr_auto_merge: false, branch_prefix: '', commit_prefix: '', base_branch: '' },
        argocd: { connected: false },
      }),
    )

    const config = await api.getConfig()
    expect(config.argocd.connected).toBe(false)
    expect(config.argocd.version).toBeUndefined()
  })

  describe('extractArgocdVersionString', () => {
    it('pulls Version out of the real object shape', () => {
      expect(extractArgocdVersionString(realArgocdVersionPayload)).toBe('v2.11.0')
    })

    it('passes a plain string through unchanged', () => {
      expect(extractArgocdVersionString('2.11.0')).toBe('2.11.0')
    })

    it('returns undefined for an object with no Version key', () => {
      expect(extractArgocdVersionString({ BuildDate: '2026-06-01T00:00:00Z' })).toBeUndefined()
    })

    it('returns undefined for null/undefined', () => {
      expect(extractArgocdVersionString(undefined)).toBeUndefined()
      expect(extractArgocdVersionString(null)).toBeUndefined()
    })
  })
})

// Login-survives-new-tabs fix (walk finding): every write call through the
// API client goes through `authHeaders()`, which now reads the token via
// `authStorage.getToken()` (localStorage) instead of sessionStorage
// directly. FirstRunWizard's init/poll flow calls straight through this
// path (initRepo, getOperation, operationHeartbeat), so this pins the
// contract those calls depend on.
describe('login-survives-new-tabs: API client attaches Bearer token from localStorage', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
    vi.restoreAllMocks()
  })

  afterEach(() => {
    localStorage.clear()
  })

  it('attaches Authorization: Bearer <token> from localStorage on a write call', async () => {
    localStorage.setItem(TOKEN_KEY, 'new-tab-token')
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, { status: 'success' }),
    )

    await deregisterCluster('prod-eu')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer new-tab-token')
  })

  it('sends no Authorization header when neither storage has a token', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, { status: 'success' }),
    )

    await deregisterCluster('prod-eu')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect((init.headers as Record<string, string>).Authorization).toBeUndefined()
  })
})
