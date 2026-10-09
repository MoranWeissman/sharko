import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'
import { AuthContext } from '@/hooks/useAuth'

// v4.0.4 U4 (demo video): on a v4 repo the addon page's Remove button called
// the v3 door DELETE /addons/{name} and got 409 "operation not supported on
// a v4 repo". On a v4 repo it must use DELETE /catalog/addons/{name}.
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

const mockRemoveAddon = vi.fn()
const mockRemoveV4 = vi.fn()

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api')
  return {
  ApiError: actual.ApiError,
  catalogRemoveErrorMessage: actual.catalogRemoveErrorMessage,
  removeAddonFromCatalogV4: (...args: unknown[]) => mockRemoveV4(...args),
  getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
  upgradeAddon: vi.fn().mockResolvedValue({ pr_url: '' }),
  configureAddon: vi.fn().mockResolvedValue({}),
  removeAddon: (...args: unknown[]) => mockRemoveAddon(...args),
  api: {
    // v4.0.4 U4: a v4 repo.
    getRepoStatus: vi.fn().mockResolvedValue({ initialized: true, bootstrap_synced: true, format: 'v4' }),
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getAddonValues: vi.fn().mockRejectedValue(new Error('not found')),
    getMe: vi.fn().mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true }),
    getAddonValuesSchema: vi.fn().mockResolvedValue({ addon_name: 'ingress-nginx', current_values: '', schema: null }),
    getAIConfig: vi.fn().mockResolvedValue({ current_provider: 'none', available_providers: [], annotate_on_seed: false }),
    getAddonDetail: vi.fn().mockResolvedValue({
      addon: {
        addon_name: 'ingress-nginx',
        chart: 'ingress-nginx',
        repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx',
        version: '4.8.0',
        total_clusters: 1,
        enabled_clusters: 1,
        healthy_applications: 1,
        degraded_applications: 0,
        missing_applications: 0,
        applications: [],
      },
    }),
  },
}
})

const adminCtx = {
  token: 't',
  username: 'tester',
  role: 'admin',
  login: vi.fn(),
  logout: vi.fn(),
  isAuthenticated: true,
  isAdmin: true,
  loading: false,
  error: null,
}

function renderDetail() {
  return render(
    <AuthContext.Provider value={adminCtx}>
      <MemoryRouter initialEntries={['/addons/ingress-nginx']}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

async function openRemoveModalAndConfirm() {
  // The header Remove button (admin-gated) opens the type-to-confirm modal.
  const removeButtons = await screen.findAllByRole('button', { name: /^Remove$/i })
  fireEvent.click(removeButtons[0])
  // Type the addon name to satisfy the type-to-confirm gate.
  const input = await screen.findByPlaceholderText('ingress-nginx')
  fireEvent.change(input, { target: { value: 'ingress-nginx' } })
  // The modal footer's Remove button is now enabled.
  const confirmButtons = screen.getAllByRole('button', { name: /^Remove$/i })
  fireEvent.click(confirmButtons[confirmButtons.length - 1])
}

describe('AddonDetail — remove on a v4 repo uses the v4 catalog door (v4.0.4 U4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  async function renderAndWaitForV4() {
    const { api } = await import('@/services/api')
    renderDetail()
    await waitFor(() =>
      expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1),
    )
    await waitFor(() => expect(api.getRepoStatus).toHaveBeenCalled())
    // let the repo-status promise settle
    await Promise.resolve()
  }

  it('confirming Remove calls the v4 catalog door, never the v3 one, and shows the PR', async () => {
    mockRemoveV4.mockResolvedValue({ pr_url: 'http://gitea.local/o/r/pulls/12', pr_id: 12, merged: false })
    await renderAndWaitForV4()
    await openRemoveModalAndConfirm()
    await waitFor(() => expect(mockRemoveV4).toHaveBeenCalledWith('ingress-nginx'))
    expect(mockRemoveAddon).not.toHaveBeenCalled()
    expect(await screen.findByRole('link', { name: /^View PR #12$/i })).toHaveAttribute(
      'href',
      'http://gitea.local/o/r/pulls/12',
    )
    expect(mockNavigate).not.toHaveBeenCalledWith('/addons')
  })

  it('Preview changes uses the v4 dry run', async () => {
    mockRemoveV4.mockResolvedValue({
      pr_title: 'Remove ingress-nginx from the catalog',
      files_to_write: [{ path: 'catalog.yaml', action: 'update' }],
    })
    await renderAndWaitForV4()
    const removeButtons = await screen.findAllByRole('button', { name: /^Remove$/i })
    fireEvent.click(removeButtons[0])
    fireEvent.click(await screen.findByRole('button', { name: /preview changes/i }))
    await waitFor(() => expect(mockRemoveV4).toHaveBeenCalledWith('ingress-nginx', true))
    expect(mockRemoveAddon).not.toHaveBeenCalled()
  })

  it('an addon still switched on shows which clusters to switch it off on, in plain words', async () => {
    const { ApiError } = await import('@/services/api')
    mockRemoveV4.mockRejectedValue(
      new ApiError(
        409,
        {
          error:
            'ingress-nginx is still enabled on spoke-eu — switch it off there first (DELETE /api/v1/v4/clusters/{cluster}/addons/ingress-nginx on each), then delete it from the catalog',
          code: 'addon_enabled_on_clusters',
          addon: 'ingress-nginx',
          clusters: ['spoke-eu'],
        },
        'Conflict',
      ),
    )
    await renderAndWaitForV4()
    await openRemoveModalAndConfirm()
    expect(
      await screen.findByText(
        'ingress-nginx is still on for spoke-eu. Switch it off on that cluster first, then remove it from the catalog.',
      ),
    ).toBeInTheDocument()
    expect(screen.queryByText(/DELETE \/api/)).not.toBeInTheDocument()
    expect(screen.queryByText(/operation not supported on a v4 repo/)).not.toBeInTheDocument()
  })
})
