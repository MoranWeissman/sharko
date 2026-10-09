import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider } from '@/hooks/useAuth'
import { MarketplaceAddonDetail } from '@/components/MarketplaceAddonDetail'

// Regression coverage for the dead-Preview bug (maintainer's live walk):
// POST /api/v1/catalog/addons succeeds, but when the caller has no
// personal Git token, internal/api/tiered_git.go's withAttributionWarning
// wraps EVERY such response as {result: <payload>, attribution_warning}.
// MarketplaceAddonDetail's handlePreview used to read only `res.dry_run`
// (never `res.result?.dry_run`), so Preview silently did nothing.
//
// The sibling test file (MarketplaceAddonDetail.test.tsx) mocks the whole
// `@/services/api` module, including addToCatalog — that hides this bug
// completely, because the mock always returns the flat shape the UI
// expects. These tests instead keep the REAL addToCatalog (and the real
// postJSON/fetchJSON helpers it shares the unwrap with) and only mock
// `globalThis.fetch`, so the central unwrap in ui/src/services/api.ts is
// actually exercised against the real wire shape.

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

vi.mock('@/services/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/services/api')>()
  return {
    ...actual,
    // addToCatalog is intentionally left as the REAL implementation — see
    // file header. Every other call the component makes on mount is
    // stubbed so the action panel renders without extra network noise.
    fetchTrackedPRs: vi.fn().mockResolvedValue({ prs: [] }),
    api: {
      ...actual.api,
      getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
      getCuratedCatalogEntry: vi.fn().mockResolvedValue({
        name: 'prometheus',
        description: 'Monitoring',
        chart: 'kube-prometheus-stack',
        repo: 'https://prometheus-community.github.io/helm-charts',
        default_namespace: 'monitoring',
        maintainers: [],
        license: 'Apache-2.0',
        category: 'observability',
        curated_by: [],
      }),
      getCuratedCatalogReadme: vi.fn().mockResolvedValue({
        readme: '',
        source: 'artifacthub',
      }),
      listCuratedCatalogVersions: vi.fn().mockResolvedValue({
        addon: 'prometheus',
        chart: 'kube-prometheus-stack',
        repo: 'https://prometheus-community.github.io/helm-charts',
        versions: [{ version: '45.0.0' }],
        latest_stable: '45.0.0',
        cached_at: new Date().toISOString(),
      }),
      getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
      getMe: vi.fn().mockResolvedValue({ has_github_token: true }),
      listCatalogSources: vi.fn().mockResolvedValue([]),
    },
  }
})

function mockFetchResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    statusText: 'OK',
    json: async () => body,
  } as Response
}

/**
 * Routes globalThis.fetch by URL instead of call order — AuthProvider (and
 * possibly other mounted providers) fire their own real fetches (e.g.
 * /api/v1/health) before the component under test ever calls
 * addToCatalog, so a plain `mockResolvedValueOnce` gets consumed by the
 * wrong call and the real /catalog/addons request falls through
 * unmocked.
 */
function mockFetchByURL(handlers: Record<string, () => Response>) {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
    const url = typeof input === 'string' ? input : (input as Request).url
    for (const [match, respond] of Object.entries(handlers)) {
      if (url.includes(match)) return respond()
    }
    return mockFetchResponse(200, {})
  })
}

function renderDetail() {
  localStorage.setItem('sharko-auth-token', 'test-token')
  localStorage.setItem('sharko-auth-user', 'tester')
  localStorage.setItem('sharko-auth-role', 'admin')
  return render(
    <MemoryRouter>
      <AuthProvider>
        <MarketplaceAddonDetail addonName="prometheus" source="curated" onBack={() => {}} />
      </AuthProvider>
    </MemoryRouter>,
  )
}

async function waitForActionPanel() {
  await waitFor(() => {
    expect(screen.getByRole('button', { name: /add to catalog/i })).toBeEnabled()
  })
}

describe('MarketplaceAddonDetail — attribution envelope unwrap (walk finding, dead-Preview bug)', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    sessionStorage.clear()
    localStorage.clear()
  })

  it('unwraps an envelope-wrapped dry-run preview and renders the files it would write', async () => {
    // Real wire shape from withAttributionWarning: the AddToCatalogResult
    // (carrying `dry_run`) sits under `result`, not at the top level.
    mockFetchByURL({
      '/catalog/addons': () =>
        mockFetchResponse(200, {
          result: {
            added: [],
            enabled: [],
            dry_run: {
              pr_title: 'sharko: add prometheus to catalog',
              effective_addons: ['prometheus'],
              files_to_write: [
                { path: 'catalog.yaml', action: 'update' },
                { path: 'values/global/prometheus.yaml', action: 'create' },
              ],
              secrets_to_create: [],
            },
          },
          attribution_warning: 'no_per_user_pat',
        }),
    })

    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /preview/i }))

    // Before the fix, handlePreview read res.dry_run (undefined on the
    // wrapped shape) and silently did nothing — this would time out.
    await waitFor(() => {
      expect(screen.getByText('values/global/prometheus.yaml')).toBeInTheDocument()
    })
    expect(screen.getAllByText('catalog.yaml').length).toBeGreaterThan(0)
  })

  it('unwraps an envelope-wrapped submit — merged:true still renders the merged terminal state', async () => {
    // merged:true is buried under `result` here. Before the fix,
    // `res.merged ?? res.result?.merged ?? false` covered this at the one
    // call site it was patched at — this proves the now-simplified
    // `res.merged` alone (post-unwrap) still resolves correctly.
    mockFetchByURL({
      '/catalog/addons': () =>
        mockFetchResponse(201, {
          result: {
            added: ['prometheus'],
            enabled: [],
            pr_id: 11,
            pr_url: 'https://gh/pr/11',
            merged: true,
          },
          attribution_warning: 'no_per_user_pat',
        }),
    })

    renderDetail()
    await waitForActionPanel()

    fireEvent.click(screen.getByRole('button', { name: /add to catalog/i }))

    // Only reachable if wasMerged resolved to true, which requires
    // res.merged (unwrapped) to be true — the dead-envelope bug would
    // leave res.merged undefined and misroute this to the "opened" phase.
    const viewCatalogBtn = await screen.findByRole('button', { name: /view in catalog/i })
    expect(mockNavigate).not.toHaveBeenCalled()

    fireEvent.click(viewCatalogBtn)
    expect(mockNavigate).toHaveBeenCalledWith('/addons')
  })
})
