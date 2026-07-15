import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import {
  removeAddon,
  configureAddon,
  deregisterCluster,
  updateClusterAddons,
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

// The REAL nested envelope shape the backend returns
const mockDryRunResult: DryRunResult = {
  pr_title: 'Test PR Title',
  effective_addons: ['addon1', 'addon2'],
  files_to_write: [
    { path: 'clusters/prod/addons.yaml', action: 'update' },
    { path: 'addons/nginx/values.yaml', action: 'create' },
  ],
  secrets_to_create: ['secret1'],
}

// Backend returns the DryRunResult nested under .dry_run key
function mockNestedDryRunEnvelope(outerFields: Record<string, unknown> = {}): unknown {
  return {
    ...outerFields,
    dry_run: mockDryRunResult,
  }
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
      mockResponse(200, mockNestedDryRunEnvelope({ status: 'success' })),
    )

    const result = await removeAddon('test-addon', true)

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/addons/test-addon?confirm=true')
    expect(init.method).toBe('DELETE')
    expect(init.body).toBeDefined()
    const body = JSON.parse(init.body as string)
    expect(body.dry_run).toBe(true)
    // Assert the function returns the UNWRAPPED DryRunResult
    expect(result.pr_title).toBe('Test PR Title')
    expect(result.files_to_write?.length).toBeGreaterThan(0)
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
      mockResponse(200, mockNestedDryRunEnvelope({ status: 'success' })),
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
    // Assert the function returns the UNWRAPPED DryRunResult
    expect(result.pr_title).toBe('Test PR Title')
    expect(result.files_to_write?.length).toBeGreaterThan(0)
  })

  it('deregisterCluster sends dry_run in DELETE body when dryRun=true', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, mockNestedDryRunEnvelope({ name: 'prod-cluster', status: 'success', cleanup: 'all' })),
    )

    const result = await deregisterCluster('prod-cluster', undefined, true)

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/clusters/prod-cluster')
    expect(init.method).toBe('DELETE')
    const body = JSON.parse(init.body as string)
    expect(body.yes).toBe(true)
    expect(body.dry_run).toBe(true)
    // Assert the function returns the UNWRAPPED DryRunResult
    expect(result.pr_title).toBe('Test PR Title')
    expect(result.files_to_write?.length).toBeGreaterThan(0)
  })

  it('unwrapGlobalValues sends dry_run as query param when dryRun=true', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, mockNestedDryRunEnvelope({ attribution_warning: null, files: [] })),
    )

    const result = await api.unwrapGlobalValues('test-addon', true)

    expect(fetchSpy).toHaveBeenCalledOnce()
    const [url, init] = fetchSpy.mock.calls[0] as [string, RequestInit]
    expect(url).toContain('/addons/unwrap-globals')
    expect(url).toContain('addon=test-addon')
    expect(url).toContain('dry_run=true')
    expect(init.method).toBe('POST')
    // Assert the function returns the UNWRAPPED DryRunResult
    expect(result.pr_title).toBe('Test PR Title')
    expect(result.files_to_write?.length).toBeGreaterThan(0)
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

  // ─── REGRESSION TESTS (V3-HF1) ─────────────────────────────────────────────
  // These tests pin the REAL nested-envelope wire contract so a mock can never
  // hide nesting bugs again. Each test feeds a NESTED {dry_run:{...}} response
  // and asserts the function returns the UNWRAPPED DryRunResult.

  it('[REGRESSION] deregisterCluster unwraps nested .dry_run envelope', async () => {
    const nestedEnvelope = {
      name: 'manual-spoke',
      status: 'success',
      cleanup: 'all',
      dry_run: {
        effective_addons: null,
        files_to_write: [
          { path: 'configuration/managed-clusters.yaml', action: 'update' },
          { path: 'configuration/addons-clusters-values/manual-spoke.yaml', action: 'delete' },
        ],
        pr_title: 'sharko: remove cluster manual-spoke',
        secrets_to_create: [],
      },
    }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, nestedEnvelope),
    )

    const result = await deregisterCluster('manual-spoke', undefined, true)

    // Assert the function returned the INNER object (has pr_title directly), NOT the envelope
    expect(result.pr_title).toBe('sharko: remove cluster manual-spoke')
    expect(result.files_to_write?.length).toBe(2)
    expect(result.files_to_write?.[1].action).toBe('delete')
  })

  it('[REGRESSION] updateClusterAddons unwraps nested .dry_run envelope', async () => {
    const nestedEnvelope = {
      cluster: { name: 'prod', addons: { podinfo: true } },
      status: 'success',
      dry_run: {
        pr_title: 'sharko: update prod cluster addons',
        files_to_write: [
          { path: 'configuration/managed-clusters.yaml', action: 'update' },
        ],
      },
    }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, nestedEnvelope),
    )

    const result = await updateClusterAddons('prod', { podinfo: true }, true)

    // Assert unwrapped
    expect(result.pr_title).toBe('sharko: update prod cluster addons')
    expect(result.files_to_write?.length).toBeGreaterThan(0)
  })

  it('[REGRESSION] setAddonValues unwraps nested .dry_run envelope', async () => {
    const nestedEnvelope = {
      status: 'success',
      dry_run: {
        pr_title: 'sharko: update nginx addon values',
        files_to_write: [
          { path: 'addons/nginx/values.yaml', action: 'update' },
        ],
      },
    }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, nestedEnvelope),
    )

    const result = await api.setAddonValues('nginx', 'replicaCount: 2\n', true)

    // Assert unwrapped
    expect(result.pr_title).toBe('sharko: update nginx addon values')
    expect(result.files_to_write?.length).toBeGreaterThan(0)
  })

  it('[REGRESSION] unwrapGlobalValues unwraps nested .dry_run envelope', async () => {
    const nestedEnvelope = {
      attribution_warning: null,
      files: [],
      dry_run: {
        pr_title: 'sharko: unwrap legacy nginx values',
        files_to_write: [
          { path: 'addons/nginx/values.yaml', action: 'delete' },
        ],
      },
    }
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      mockResponse(200, nestedEnvelope),
    )

    const result = await api.unwrapGlobalValues('nginx', true)

    // Assert unwrapped
    expect(result.pr_title).toBe('sharko: unwrap legacy nginx values')
    expect(result.files_to_write?.length).toBeGreaterThan(0)
  })
})
