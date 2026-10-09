import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { removeAddonFromCatalogV4, catalogRemoveErrorMessage, ApiError } from '../api'

/**
 * v4.0.4 U4: the v4 way to remove an addon from the catalog is
 * DELETE /api/v1/catalog/addons/{name} — not the v3 DELETE /addons/{name},
 * which a v4 repo refuses with 409.
 */

function mockResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText: 'OK',
    json: async () => body,
  } as Response
}

describe('removeAddonFromCatalogV4', () => {
  beforeEach(() => {
    localStorage.setItem('sharko-auth-token', 'test-token')
    vi.restoreAllMocks()
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('confirms with yes: true on the v4 catalog door and returns the PR', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(mockResponse(200, { pr_url: 'http://g/pulls/3', pr_id: 3, merged: false }))
    const res = await removeAddonFromCatalogV4('metrics-server')
    const [url, init] = fetchSpy.mock.calls[0]
    expect(url).toBe('/api/v1/catalog/addons/metrics-server')
    expect(init?.method).toBe('DELETE')
    expect(JSON.parse(String(init?.body))).toEqual({ yes: true })
    expect(res.pr_id).toBe(3)
  })

  it('dry run sends dry_run: true and returns the nested preview', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(
        mockResponse(200, { merged: false, dry_run: { pr_title: 'Remove x', files_to_write: [] } }),
      )
    const res = await removeAddonFromCatalogV4('metrics-server', true)
    expect(JSON.parse(String(fetchSpy.mock.calls[0][1]?.body))).toEqual({ dry_run: true })
    expect(res.pr_title).toBe('Remove x')
  })

  it('a refusal throws an ApiError carrying the code', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      mockResponse(409, { error: 'x', code: 'addon_enabled_on_clusters', clusters: ['a', 'b'] }),
    )
    const err = await removeAddonFromCatalogV4('m').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(catalogRemoveErrorMessage('m', err)).toBe(
      'm is still on for a, b. Switch it off on those clusters first, then remove it from the catalog.',
    )
  })
})
