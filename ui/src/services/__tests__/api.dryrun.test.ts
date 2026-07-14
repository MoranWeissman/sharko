import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import {
  removeAddon,
  configureAddon,
  deregisterCluster,
  api,
} from '../api'
import type { DryRunResult } from '../models'

/**
 * Tests for V3-TX-A2: dry_run support on PR-opening API functions.
 * Verifies that each function passes dry_run correctly (body or query per
 * the backend contract) and types the return as DryRunResult when dry-run.
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

const mockDryRunResult: DryRunResult = {
  pr_title: 'Test PR Title',
  effective_addons: ['addon1', 'addon2'],
  files_to_write: [
    { path: 'clusters/prod/addons.yaml', action: 'update' },
    { path: 'addons/nginx/values.yaml', action: 'create' },
  ],
  secrets_to_create: ['secret1'],
}

describe('V3-TX-A2: dry_run param wiring', () => {
  beforeEach(() => {
    sessionStorage.setItem(TOKEN_KEY, 'test-token')
    vi.restoreAllMocks()
  })

  afterEach(() => {
    sessionStorage.removeItem(TOKEN_KEY)
  })

  it('removeAddon sends dry_run in DELETE body when dryRun=true', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, mockDryRunResult),
    )

    const result = await removeAddon('test-addon', true)

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/addons/test-addon?confirm=true')
    expect(init.method).toBe('DELETE')
    expect(init.body).toBeDefined()
    const body = JSON.parse(init.body as string)
    expect(body.dry_run).toBe(true)
    expect(result).toMatchObject(mockDryRunResult)
  })

  it('removeAddon does NOT send dry_run when dryRun=false (default)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, { pr_url: 'https://example/pr/1', status: 'success' }),
    )

    await removeAddon('test-addon')

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/addons/test-addon?confirm=true')
    expect(init.method).toBe('DELETE')
    // Normal DELETE has no body (deleteJSON helper doesn't send one)
    expect(init.body).toBeUndefined()
  })

  it('configureAddon sends dry_run in PATCH body when dry_run: true in config', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, mockDryRunResult),
    )

    const result = await configureAddon('test-addon', {
      version: '1.2.3',
      self_heal: true,
      dry_run: true,
    })

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/addons/test-addon')
    expect(init.method).toBe('PATCH')
    const body = JSON.parse(init.body as string)
    expect(body.dry_run).toBe(true)
    expect(body.version).toBe('1.2.3')
    expect(body.self_heal).toBe(true)
    expect(result).toMatchObject(mockDryRunResult)
  })

  it('deregisterCluster sends dry_run in DELETE body when dryRun=true', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, mockDryRunResult),
    )

    const result = await deregisterCluster('prod-cluster', undefined, true)

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/clusters/prod-cluster')
    expect(init.method).toBe('DELETE')
    const body = JSON.parse(init.body as string)
    expect(body.yes).toBe(true)
    expect(body.dry_run).toBe(true)
    expect(result).toMatchObject(mockDryRunResult)
  })

  it('unwrapGlobalValues sends dry_run as query param when dryRun=true', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, mockDryRunResult),
    )

    const result = await api.unwrapGlobalValues('test-addon', true)

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/addons/unwrap-globals')
    expect(url).toContain('addon=test-addon')
    expect(url).toContain('dry_run=true')
    expect(init.method).toBe('POST')
    expect(result).toMatchObject(mockDryRunResult)
  })

  it('DryRunResult includes FilePreview.action = delete for remove ops', () => {
    const dryRunWithDelete: DryRunResult = {
      pr_title: 'Remove addon',
      files_to_write: [
        { path: 'addons/nginx/values.yaml', action: 'delete' },
        { path: 'clusters/prod/addons.yaml', action: 'update' },
      ],
    }
    // Type-level check: 'delete' is a valid action
    expect(dryRunWithDelete.files_to_write?.[0].action).toBe('delete')
  })
})
