import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'
import { AuthContext } from '@/hooks/useAuth'
import { upgradeAddonClusters } from '@/services/api'

// SubsetUpgradePanel — wave 2 review fix: "silent mass downgrade labeled
// 'behind'". The old drift calc (deployedVersion !== catalogVersion)
// swept up clusters AHEAD of the catalog into the same "behind" bucket as
// clusters actually behind, so selecting one and clicking "Upgrade N
// clusters" could silently move it DOWN a version — with none of the
// typed-confirmation gate the single-cluster path (PerClusterUpgradeRow)
// requires for the same act. This file pins: per-row direction badges, the
// mixed-set copy, the amber warning, the typed-DOWNGRADE gate on apply,
// the pure-upgrade path staying a single click, and the admin-only guard.

const mockUpgradeAddonClusters = vi.mocked(upgradeAddonClusters)

vi.mock('@/services/api', () => ({
  getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
  upgradeAddon: vi.fn().mockResolvedValue({ pr_url: '' }),
  upgradeAddonClusters: vi.fn(),
  configureAddon: vi.fn().mockResolvedValue({}),
  removeAddon: vi.fn(),
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
        total_clusters: 3,
        enabled_clusters: 3,
        healthy_applications: 3,
        degraded_applications: 0,
        missing_applications: 0,
        applications: [
          {
            cluster_name: 'prod-cluster-1',
            cluster_environment: 'prod',
            enabled: true,
            configured_version: '4.7.0',
            deployed_version: '4.7.0', // behind
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
          },
          {
            cluster_name: 'dev-cluster-1',
            cluster_environment: 'dev',
            enabled: true,
            configured_version: '4.6.0',
            deployed_version: '4.6.0', // behind
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
          },
          {
            cluster_name: 'staging-cluster-1',
            cluster_environment: 'staging',
            enabled: true,
            configured_version: '4.9.0',
            deployed_version: '4.9.0', // AHEAD of the catalog (4.8.0)
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
          },
        ],
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

const viewerCtx = { ...adminCtx, role: 'viewer', username: 'viewer', isAdmin: false }

function renderUpgradeTab(ctx: typeof adminCtx) {
  return render(
    <AuthContext.Provider value={ctx}>
      <MemoryRouter initialEntries={['/addons/ingress-nginx?section=upgrade']}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

describe('AddonDetail — SubsetUpgradePanel direction honesty (v4-wave2 review)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('is hidden entirely from a non-admin', async () => {
    renderUpgradeTab(viewerCtx)
    await waitFor(() => expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1))
    expect(screen.queryByText('Upgrade multiple clusters at once')).not.toBeInTheDocument()
  })

  it('shows the mixed-set copy and a Downgrade badge on the ahead cluster, Upgrade badges on behind clusters', async () => {
    renderUpgradeTab(adminCtx)
    await screen.findByText('Upgrade multiple clusters at once')

    // Mixed set: 2 behind, 1 ahead — the neutral copy, not "are behind".
    expect(
      screen.getByText('3 of 3 clusters are on a different version. Pick which ones move together — one pull request, one small diff per cluster.'),
    ).toBeInTheDocument()

    expect(screen.getByTestId('subset-upgrade-badge-staging-cluster-1')).toHaveTextContent('Downgrade')
    expect(screen.getByTestId('subset-upgrade-badge-prod-cluster-1')).toHaveTextContent('Upgrade')
    expect(screen.getByTestId('subset-upgrade-badge-dev-cluster-1')).toHaveTextContent('Upgrade')
  })

  it('a pure-upgrade selection applies with a single click — no typed confirmation gate', async () => {
    mockUpgradeAddonClusters.mockImplementation((_name, data) => {
      if (data.dry_run) {
        return Promise.resolve({ dry_run: { pr_title: 'bump ingress-nginx', files_to_write: [] } })
      }
      return Promise.resolve({ pr_url: 'https://example.com/pull/1', pr_id: 1, merged: false })
    })
    renderUpgradeTab(adminCtx)
    await screen.findByText('Upgrade multiple clusters at once')

    // Select only the two BEHIND clusters (prod-cluster-1, dev-cluster-1) —
    // checkbox order follows the applications fixture order. The ahead
    // cluster (staging-cluster-1, index 2) is deliberately left unchecked.
    const checkboxes = screen.getAllByRole('checkbox')
    fireEvent.click(checkboxes[0])
    fireEvent.click(checkboxes[1])

    const previewBtn = screen.getByRole('button', { name: /Preview 2 changes/ })
    fireEvent.click(previewBtn)
    await waitFor(() => expect(mockUpgradeAddonClusters).toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ dry_run: true }),
    ))

    const applyBtn = await screen.findByRole('button', { name: /Upgrade 2 clusters/ })
    fireEvent.click(applyBtn)

    // Applied immediately — no DOWNGRADE confirmation dialog appeared.
    expect(screen.queryByText('Confirm Downgrade')).not.toBeInTheDocument()
    await waitFor(() => expect(mockUpgradeAddonClusters).toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ yes: true, clusters: expect.arrayContaining(['prod-cluster-1', 'dev-cluster-1']) }),
    ))
  })

  it('applying a selection that includes an ahead cluster requires typing DOWNGRADE before it goes through', async () => {
    mockUpgradeAddonClusters.mockImplementation((_name, data) => {
      if (data.dry_run) {
        return Promise.resolve({ dry_run: { pr_title: 'bump ingress-nginx', files_to_write: [] } })
      }
      return Promise.resolve({ pr_url: 'https://example.com/pull/2', pr_id: 2, merged: false })
    })
    renderUpgradeTab(adminCtx)
    await screen.findByText('Upgrade multiple clusters at once')

    // "Select all outdated" pulls in the ahead cluster too.
    fireEvent.click(screen.getByText('Select all outdated'))

    // The amber warning must be visible before any apply is possible.
    expect(
      screen.getByText(/Some selected clusters are ahead of .* Applying will move them backward\./),
    ).toBeInTheDocument()

    const previewBtn = screen.getByRole('button', { name: /Preview 3 changes/ })
    fireEvent.click(previewBtn)
    await waitFor(() => expect(mockUpgradeAddonClusters).toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ dry_run: true }),
    ))

    const applyBtn = await screen.findByRole('button', { name: /Upgrade 3 clusters/ })
    fireEvent.click(applyBtn)

    // The typed-confirmation modal gates the apply — the real (non-dry-run)
    // call must NOT have fired yet.
    const dialog = await screen.findByText('Downgrade 3 clusters?')
    expect(dialog).toBeInTheDocument()
    expect(mockUpgradeAddonClusters).not.toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ yes: true }),
    )

    // The confirm button stays disabled until "DOWNGRADE" is typed.
    const confirmBtn = screen.getByRole('button', { name: 'Confirm Downgrade' })
    expect(confirmBtn).toBeDisabled()

    fireEvent.change(screen.getByPlaceholderText('DOWNGRADE'), { target: { value: 'DOWNGRADE' } })
    expect(confirmBtn).not.toBeDisabled()
    fireEvent.click(confirmBtn)

    await waitFor(() => expect(mockUpgradeAddonClusters).toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ yes: true }),
    ))
  })
})
