import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'
import { AuthContext } from '@/hooks/useAuth'

/*
 * V3-TX-A3 — Preview on every PR-opening operation. Surface 5: Configure Addon.
 *
 * The ApplicationSet (catalog) tab's Edit mode gets a "Preview changes" button
 * next to "Save (opens PR)". Clicking it calls configureAddon(name, {..., dry_run:true})
 * and renders the returned DryRunResult via the shared DryRunPreview WITHOUT
 * opening a PR — the real Save stays a separate, explicit action.
 */

const mockConfigureAddon = vi.fn()

vi.mock('@/services/api', () => ({
  getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
  upgradeAddon: vi.fn().mockResolvedValue({ pr_url: '' }),
  removeAddon: vi.fn().mockResolvedValue({}),
  configureAddon: (...args: unknown[]) => mockConfigureAddon(...args),
  api: {
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getAddonValues: vi.fn().mockRejectedValue(new Error('not found')),
    getMe: vi.fn().mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true }),
    getAddonValuesSchema: vi.fn().mockResolvedValue({ addon_name: 'ingress-nginx', current_values: '', schema: null }),
    getAIConfig: vi.fn().mockResolvedValue({ current_provider: 'none', available_providers: [], annotate_on_seed: false }),
    getUpgradeRecommendations: vi.fn().mockResolvedValue({ current_version: '4.8.0' }),
    getUpgradeVersions: vi.fn().mockResolvedValue({ versions: [] }),
    getAddonDetail: vi.fn().mockResolvedValue({
      addon: {
        addon_name: 'ingress-nginx',
        chart: 'ingress-nginx',
        repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx',
        version: '4.8.0',
        selfHeal: true,
        syncOptions: [],
        extraHelmValues: {},
        total_clusters: 1,
        enabled_clusters: 1,
        healthy_applications: 1,
        degraded_applications: 0,
        missing_applications: 0,
        applications: [],
      },
    }),
  },
}))

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

function renderCatalogTab() {
  return render(
    <AuthContext.Provider value={adminCtx}>
      <MemoryRouter initialEntries={['/addons/ingress-nginx?section=catalog']}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

describe('AddonDetail — Configure Addon preview (V3-TX-A3, Surface 5)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('Preview calls configureAddon(dry_run) and renders the diff without saving', async () => {
    mockConfigureAddon.mockResolvedValue({
      pr_title: 'Configure ingress-nginx',
      files_to_write: [
        { path: 'configuration/addons-catalog.yaml', action: 'update' },
      ],
    })

    renderCatalogTab()

    await waitFor(() =>
      expect(screen.getByRole('heading', { name: 'ApplicationSet' })).toBeInTheDocument(),
    )

    // Enter edit mode.
    fireEvent.click(screen.getByRole('button', { name: /^Edit$/i }))

    // Preview changes button is present alongside Save (opens PR).
    const previewBtn = await screen.findByRole('button', { name: /preview changes/i })
    fireEvent.click(previewBtn)

    // Dry-run call carries dry_run: true.
    await waitFor(() => {
      expect(mockConfigureAddon).toHaveBeenCalled()
      const [name, config] = mockConfigureAddon.mock.calls[0]
      expect(name).toBe('ingress-nginx')
      expect((config as { dry_run?: boolean }).dry_run).toBe(true)
    })

    // The returned diff renders via the shared DryRunPreview.
    await waitFor(() =>
      expect(screen.getByText('Configure ingress-nginx')).toBeInTheDocument(),
    )
    expect(screen.getByText('configuration/addons-catalog.yaml')).toBeInTheDocument()

    // Preview is a courtesy — the real (non-dry-run) save was NOT called.
    const nonDryRunCall = mockConfigureAddon.mock.calls.find(
      ([, config]) => !(config as { dry_run?: boolean }).dry_run,
    )
    expect(nonDryRunCall).toBeUndefined()
  })
})
