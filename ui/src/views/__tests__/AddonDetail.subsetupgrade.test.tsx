import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'
import { AuthContext } from '@/hooks/useAuth'
import { api, upgradeAddon, upgradeAddonClusters } from '@/services/api'

// The Upgrade tab's per-cluster part, after pre-release walk 4:
//
//  - "Upgrade chosen clusters only" lists EVERY cluster that has the addon
//    switched on (it used to appear only when two or more clusters differed
//    from the catalog, so there was no way to move one cluster ahead);
//  - a cluster on a NEWER version than the catalog is "Ahead of catalog" —
//    never "behind", and never offered a "Downgrade to <catalog>" button in
//    its row. Going back stays possible in the panel, behind typed DOWNGRADE;
//  - what the server says an upgrade leaves on the old chart's values is
//    shown after the preview and after the real upgrade.
//
// Still pinned from the v4-wave2 review: per-row direction badges, the amber
// warning, the typed-DOWNGRADE gate, the single-click pure upgrade, and the
// admin-only guard.

const mockUpgradeAddonClusters = vi.mocked(upgradeAddonClusters)
const mockUpgradeAddon = vi.mocked(upgradeAddon)

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

const PANEL_TITLE = 'Upgrade chosen clusters only'
const PANEL_TEXT =
  'Pick clusters and a version. One pull request moves only those clusters. The catalog version stays 4.8.0.'

describe('AddonDetail — Upgrade tab, per-cluster part (pre-release walk 4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('is hidden entirely from a non-admin', async () => {
    renderUpgradeTab(viewerCtx)
    await waitFor(() => expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1))
    expect(screen.queryByText(PANEL_TITLE)).not.toBeInTheDocument()
  })

  it('says what it does in one plain sentence, and never calls a cluster "behind"', async () => {
    renderUpgradeTab(adminCtx)
    await screen.findByText(PANEL_TITLE)
    expect(screen.getByText(PANEL_TEXT)).toBeInTheDocument()

    // The old wording is gone.
    expect(screen.queryByText('Upgrade multiple clusters at once')).not.toBeInTheDocument()
    expect(screen.queryByText(/clusters are behind/)).not.toBeInTheDocument()
    expect(screen.queryByText(/clusters are on a different version/)).not.toBeInTheDocument()
    expect(screen.queryByText('Select all outdated')).not.toBeInTheDocument()

    expect(screen.getByTestId('subset-upgrade-badge-staging-cluster-1')).toHaveTextContent('Downgrade')
    expect(screen.getByTestId('subset-upgrade-badge-prod-cluster-1')).toHaveTextContent('Upgrade')
    expect(screen.getByTestId('subset-upgrade-badge-dev-cluster-1')).toHaveTextContent('Upgrade')
  })

  it('a cluster ahead of the catalog is "Ahead of catalog" in its row, with no Downgrade button', async () => {
    renderUpgradeTab(adminCtx)
    await screen.findByText(PANEL_TITLE)

    expect(screen.getByTestId('ahead-badge-staging-cluster-1')).toHaveTextContent('Ahead of catalog')
    // The row used to carry a red "Downgrade" badge and a "Downgrade to
    // 4.8.0" button. Following them undid the upgrade.
    expect(screen.queryByRole('button', { name: /Downgrade to/ })).not.toBeInTheDocument()
    // The clusters that really are older get the one-click upgrade.
    expect(screen.getAllByRole('button', { name: 'Upgrade to 4.8.0' })).toHaveLength(2)
    expect(screen.queryByTestId('ahead-badge-prod-cluster-1')).not.toBeInTheDocument()
  })

  it('the row upgrade of an older cluster calls the upgrade for that one cluster and shows what stays old', async () => {
    const warning =
      'values/global/ingress-nginx.yaml is shared by every cluster and still sets the old chart\'s value for: image.tag (4.7.0, new chart default 4.8.0).'
    mockUpgradeAddon.mockResolvedValueOnce({ pr_url: 'https://example.com/pull/9', pr_id: 9, merged: false, warnings: [warning] })
    renderUpgradeTab(adminCtx)
    await screen.findByText(PANEL_TITLE)

    fireEvent.click(screen.getAllByRole('button', { name: 'Upgrade to 4.8.0' })[0])
    fireEvent.click(await screen.findByRole('button', { name: 'Yes' }))

    await waitFor(() =>
      expect(mockUpgradeAddon).toHaveBeenCalledWith('ingress-nginx', { version: '4.8.0', cluster: 'prod-cluster-1' }),
    )
    expect(await screen.findByText('Upgrade pull request opened')).toBeInTheDocument()
    expect(screen.queryByText('Upgrade applied')).not.toBeInTheDocument()
    expect(screen.queryByText('Upgrade initiated')).not.toBeInTheDocument()
    expect(await screen.findByText(warning)).toBeInTheDocument()
  })

  it('shows the version that really runs next to the version Git asks for, when they differ', async () => {
    vi.mocked(api.getAddonDetail).mockResolvedValueOnce({
      addon: {
        addon_name: 'ingress-nginx', chart: 'ingress-nginx', repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx', version: '4.8.0', total_clusters: 1, enabled_clusters: 1,
        healthy_applications: 1, degraded_applications: 0, missing_applications: 0,
        applications: [{
          cluster_name: 'prod-cluster-1', cluster_environment: 'prod', enabled: true,
          configured_version: '4.8.0', deployed_version: '4.7.0', // merged, ArgoCD has not synced yet
          namespace: 'ingress-nginx', health_status: 'Healthy', status: 'healthy',
        }],
      },
    } as never)
    renderUpgradeTab(adminCtx)
    await screen.findByText(PANEL_TITLE)

    expect(screen.getByTestId('running-version-prod-cluster-1')).toHaveTextContent('running 4.7.0')
    // Git already asks for the catalog version, so there is nothing to
    // upgrade — even though the old version still runs for a moment.
    expect(screen.queryByRole('button', { name: /Upgrade to 4.8.0/ })).not.toBeInTheDocument()
    expect(screen.getByText('✓ Current')).toBeInTheDocument()
  })

  it('is there when every cluster is on the catalog version, so one cluster can be moved ahead', async () => {
    vi.mocked(api.getAddonDetail).mockResolvedValueOnce({
      addon: {
        addon_name: 'ingress-nginx', chart: 'ingress-nginx', repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx', version: '4.8.0', total_clusters: 1, enabled_clusters: 1,
        healthy_applications: 1, degraded_applications: 0, missing_applications: 0,
        applications: [{
          cluster_name: 'prod-cluster-1', cluster_environment: 'prod', enabled: true,
          configured_version: '4.8.0', deployed_version: '4.8.0',
          namespace: 'ingress-nginx', health_status: 'Healthy', status: 'healthy',
        }],
      },
    } as never)
    mockUpgradeAddonClusters.mockResolvedValue({ dry_run: { pr_title: 'x', files_to_write: [] } } as never)
    renderUpgradeTab(adminCtx)
    await screen.findByText(PANEL_TITLE)

    expect(screen.getByTestId('subset-upgrade-badge-prod-cluster-1')).toHaveTextContent('Already on this version')
    fireEvent.change(screen.getByLabelText('Target version'), { target: { value: '4.9.0' } })
    expect(screen.getByTestId('subset-upgrade-badge-prod-cluster-1')).toHaveTextContent('Upgrade')

    fireEvent.click(screen.getByRole('checkbox'))
    fireEvent.click(screen.getByRole('button', { name: /Preview 1 change/ }))
    await waitFor(() =>
      expect(mockUpgradeAddonClusters).toHaveBeenCalledWith('ingress-nginx', {
        clusters: ['prod-cluster-1'], version: '4.9.0', dry_run: true,
      }),
    )
  })

  it('a pure-upgrade selection applies with a single click — no typed confirmation gate', async () => {
    const warning = 'values/global/ingress-nginx.yaml is shared by every cluster and still sets the old chart\'s value for: image.tag.'
    mockUpgradeAddonClusters.mockImplementation((_name, data) => {
      if (data.dry_run) {
        return Promise.resolve({ dry_run: { pr_title: 'bump ingress-nginx', files_to_write: [] }, warnings: [warning] } as never)
      }
      return Promise.resolve({ pr_url: 'https://example.com/pull/1', pr_id: 1, merged: false, warnings: [warning] })
    })
    renderUpgradeTab(adminCtx)
    await screen.findByText(PANEL_TITLE)

    // Select only the two OLDER clusters (prod-cluster-1, dev-cluster-1) —
    // checkbox order follows the applications fixture order. The ahead
    // cluster (staging-cluster-1, index 2) is deliberately left unchecked.
    const checkboxes = screen.getAllByRole('checkbox')
    fireEvent.click(checkboxes[0])
    fireEvent.click(checkboxes[1])

    fireEvent.click(screen.getByRole('button', { name: /Preview 2 changes/ }))
    await waitFor(() => expect(mockUpgradeAddonClusters).toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ dry_run: true }),
    ))
    // What the upgrade leaves on the old chart's value is said on the
    // preview, before anything is opened.
    expect(await screen.findByText(warning)).toBeInTheDocument()

    fireEvent.click(await screen.findByRole('button', { name: /Upgrade 2 clusters/ }))

    // Applied immediately — no DOWNGRADE confirmation dialog appeared.
    expect(screen.queryByText('Confirm Downgrade')).not.toBeInTheDocument()
    await waitFor(() => expect(mockUpgradeAddonClusters).toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ yes: true, clusters: expect.arrayContaining(['prod-cluster-1', 'dev-cluster-1']) }),
    ))
    // ...and again next to the pull request.
    expect(await screen.findByText(warning)).toBeInTheDocument()
    expect(screen.queryByText(/cluster\(s\) upgraded to/)).not.toBeInTheDocument()
  })

  it('applying a selection that includes an ahead cluster requires typing DOWNGRADE before it goes through', async () => {
    mockUpgradeAddonClusters.mockImplementation((_name, data) => {
      if (data.dry_run) {
        return Promise.resolve({ dry_run: { pr_title: 'bump ingress-nginx', files_to_write: [] } } as never)
      }
      return Promise.resolve({ pr_url: 'https://example.com/pull/2', pr_id: 2, merged: false })
    })
    renderUpgradeTab(adminCtx)
    await screen.findByText(PANEL_TITLE)

    // "Select all" pulls in the ahead cluster too.
    fireEvent.click(screen.getByText('Select all'))

    // The amber warning must be visible before any apply is possible.
    expect(
      screen.getByText(/Some selected clusters are ahead of .* Applying will move them backward\./),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Preview 3 changes/ }))
    await waitFor(() => expect(mockUpgradeAddonClusters).toHaveBeenCalledWith(
      'ingress-nginx',
      expect.objectContaining({ dry_run: true }),
    ))

    // The button does not say "Upgrade" when some clusters go down.
    expect(screen.queryByRole('button', { name: /Upgrade 3 clusters/ })).not.toBeInTheDocument()
    fireEvent.click(await screen.findByRole('button', { name: /Change 3 clusters/ }))

    // The typed-confirmation modal gates the apply — the real (non-dry-run)
    // call must NOT have fired yet.
    expect(await screen.findByText('Downgrade 3 clusters?')).toBeInTheDocument()
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
