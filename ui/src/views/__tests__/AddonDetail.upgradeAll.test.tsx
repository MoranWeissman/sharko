import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail, isPreReleaseVersion } from '@/views/AddonDetail'
import { AuthContext } from '@/hooks/useAuth'
import { api, upgradeAddon } from '@/services/api'

// The main Upgrade button, after pre-release walk 4:
//
//  - Analyze, "Upgrade to X", "Yes, upgrade" ended in a red "operation not
//    supported on a v4 repo". It now opens the all-clusters upgrade pull
//    request, and the screen says "on all clusters";
//  - a version the chart repository does not have is refused by the server
//    and the server's sentence is what the person reads;
//  - Analyze showed a red "Conflicts" box for the chart's own image tag;
//  - a beta at the top of the list was labelled LATEST;
//  - typing "zzz" into the version search emptied the list with no word.

const mockUpgradeAddon = vi.mocked(upgradeAddon)

// jsdom has no scrollIntoView; the page scrolls to the analysis when it opens.
Element.prototype.scrollIntoView = vi.fn()

vi.mock('@/services/api', () => ({
  getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
  upgradeAddon: vi.fn(),
  upgradeAddonClusters: vi.fn(),
  configureAddon: vi.fn().mockResolvedValue({}),
  removeAddon: vi.fn(),
  api: {
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getAddonValues: vi.fn().mockRejectedValue(new Error('not found')),
    getMe: vi.fn().mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true }),
    getAddonValuesSchema: vi.fn().mockResolvedValue({ addon_name: 'podinfo', current_values: '', schema: null }),
    getAIConfig: vi.fn().mockResolvedValue({ current_provider: 'none', available_providers: [], annotate_on_seed: false }),
    getUpgradeRecommendations: vi.fn().mockResolvedValue({ current_version: '6.7.1' }),
    getUpgradeVersions: vi.fn().mockResolvedValue({
      versions: [{ version: '7.0.0-beta.1' }, { version: '6.8.0' }, { version: '6.7.1' }],
    }),
    checkUpgrade: vi.fn(),
    getAddonDetail: vi.fn().mockResolvedValue({
      addon: {
        addon_name: 'podinfo',
        chart: 'podinfo',
        repo_url: 'https://stefanprodan.github.io/podinfo',
        namespace: 'podinfo',
        version: '6.7.1',
        total_clusters: 1,
        enabled_clusters: 1,
        healthy_applications: 1,
        degraded_applications: 0,
        missing_applications: 0,
        applications: [
          {
            cluster_name: 'spoke-1',
            cluster_environment: 'prod',
            enabled: true,
            configured_version: '6.7.1',
            deployed_version: '6.7.1',
            namespace: 'podinfo',
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

function renderUpgradeTab() {
  return render(
    <AuthContext.Provider value={adminCtx}>
      <MemoryRouter initialEntries={['/addons/podinfo?section=upgrade']}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

const analysis = {
  addon_name: 'podinfo',
  chart: 'podinfo',
  current_version: '6.7.1',
  target_version: '6.8.0',
  total_changes: 1,
  added: [],
  removed: [],
  changed: [{ path: 'image.tag', type: 'changed', old_value: '6.7.1', new_value: '6.8.0' }],
  conflicts: [],
  follows_default: [{ path: 'image.tag', type: 'changed', old_value: '6.7.1', new_value: '6.8.0' }],
}

async function analyze(version: string) {
  const row = (await screen.findByText(version)).closest('div.rounded-lg') as HTMLElement
  const btn = Array.from(row.querySelectorAll('button')).find((b) => b.textContent === 'Analyze')
  if (!btn) throw new Error(`no Analyze button in the row of ${version}`)
  fireEvent.click(btn)
}

describe('AddonDetail — the main Upgrade button (pre-release walk 4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(api.checkUpgrade).mockResolvedValue(analysis as never)
  })

  it('Analyze, Upgrade, Yes: opens the upgrade for ALL clusters and says so', async () => {
    mockUpgradeAddon.mockResolvedValueOnce({ pr_url: 'https://example.com/pull/7', pr_id: 7, merged: false })
    renderUpgradeTab()
    await analyze('6.8.0')

    fireEvent.click(await screen.findByRole('button', { name: /Upgrade to 6\.8\.0/ }))
    expect(await screen.findByText('Upgrade podinfo to 6.8.0 on all clusters?')).toBeInTheDocument()
    expect(
      screen.getByText('This opens one pull request. Every cluster that has podinfo switched on moves to 6.8.0 once it merges. A cluster that is already on a newer version stays there.'),
    ).toBeInTheDocument()
    // The old text did not say which clusters.
    expect(screen.queryByText('Upgrade podinfo to 6.8.0?')).not.toBeInTheDocument()
    expect(screen.queryByText('This will create a pull request with the version change.')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Yes, upgrade' }))

    // One call, no cluster named: that is the all-clusters upgrade.
    await waitFor(() =>
      expect(mockUpgradeAddon).toHaveBeenCalledWith('podinfo', { version: '6.8.0', cluster: undefined }),
    )
    expect(
      await screen.findByText('PR open for review — podinfo moves to 6.8.0 on all clusters once it merges'),
    ).toBeInTheDocument()
    expect(screen.queryByText(/not supported on a v4 repo/)).not.toBeInTheDocument()
    expect(screen.queryByText(/podinfo upgraded to 6\.8\.0/)).not.toBeInTheDocument()
  })

  it('shows what the server says the upgrade left on the old chart value', async () => {
    const warning =
      "values/global/podinfo.yaml still sets the old chart's value for: args (- --level=info, new chart default - --level=warn). Sharko did not change these. They stay as they are after the upgrade until you edit that file."
    mockUpgradeAddon.mockResolvedValueOnce({ pr_url: 'https://example.com/pull/8', pr_id: 8, merged: false, warnings: [warning] })
    renderUpgradeTab()
    await analyze('6.8.0')
    fireEvent.click(await screen.findByRole('button', { name: /Upgrade to 6\.8\.0/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Yes, upgrade' }))

    expect(await screen.findByText('Check after this upgrade')).toBeInTheDocument()
    expect(screen.getByText(warning)).toBeInTheDocument()
  })

  it('a version the chart repository does not have: the server sentence is shown and nothing claims success', async () => {
    const sentence =
      'Version 99.0.0 of podinfo was not found in its chart repository (https://stefanprodan.github.io/podinfo). Nothing was changed. Pick a version that the repository lists.'
    mockUpgradeAddon.mockRejectedValueOnce(new Error(sentence))
    vi.mocked(api.checkUpgrade).mockResolvedValueOnce({ ...analysis, target_version: '99.0.0', follows_default: [] } as never)
    renderUpgradeTab()

    fireEvent.change(await screen.findByPlaceholderText(/Jump to version/), { target: { value: '99.0.0' } })
    fireEvent.click(await screen.findByRole('button', { name: 'Analyze 99.0.0' }))
    fireEvent.click(await screen.findByRole('button', { name: /Upgrade to 99\.0\.0/ }))
    fireEvent.click(await screen.findByRole('button', { name: 'Yes, upgrade' }))

    expect(await screen.findByText(sentence)).toBeInTheDocument()
    expect(screen.queryByText(/PR open for review/)).not.toBeInTheDocument()
    expect(screen.queryByText(/PR merged/)).not.toBeInTheDocument()
  })

  it('settings that follow the chart are listed apart, not as red conflicts', async () => {
    renderUpgradeTab()
    await analyze('6.8.0')

    const box = await screen.findByTestId('follows-default')
    expect(box).toHaveTextContent('1 setting in the global values file move to the new chart defaults')
    expect(box).toHaveTextContent('Nobody changed these, so an upgrade for all clusters updates them in the same pull request.')
    expect(box).toHaveTextContent('image.tag')
    expect(screen.queryByText(/1 Conflict/)).not.toBeInTheDocument()
    expect(screen.queryByText('Conflicts Found')).not.toBeInTheDocument()
  })

  it('a real conflict says what it means', async () => {
    vi.mocked(api.checkUpgrade).mockResolvedValueOnce({
      ...analysis,
      follows_default: [],
      conflicts: [{ path: 'replicaCount', configured_value: '5', old_default: '1', new_default: '2', source: 'global' }],
    } as never)
    renderUpgradeTab()
    await analyze('6.8.0')

    expect(await screen.findByText('1 Conflict')).toBeInTheDocument()
    expect(
      screen.getByText('A values file sets these, and the chart changed its own value for them. Your value stays after the upgrade. Check that it still fits the new chart.'),
    ).toBeInTheDocument()
  })

  it('LATEST goes on the newest released version, and a beta is marked as a pre-release', async () => {
    renderUpgradeTab()
    const betaRow = (await screen.findByText('7.0.0-beta.1')).closest('div.rounded-lg') as HTMLElement
    const stableRow = screen.getByText('6.8.0').closest('div.rounded-lg') as HTMLElement

    expect(betaRow).toHaveTextContent('Pre-release')
    expect(betaRow).not.toHaveTextContent('LATEST')
    expect(stableRow).toHaveTextContent('LATEST')
    expect(stableRow).not.toHaveTextContent('Pre-release')
    expect(screen.getAllByText('LATEST')).toHaveLength(1)
  })

  it('a search that matches nothing says so', async () => {
    renderUpgradeTab()
    fireEvent.change(await screen.findByPlaceholderText(/Jump to version/), { target: { value: 'zzz' } })
    expect(await screen.findByText('No version matches "zzz".')).toBeInTheDocument()
  })
})

describe('isPreReleaseVersion', () => {
  it('knows a pre-release from a release', () => {
    expect(isPreReleaseVersion('3.0.0-beta.6')).toBe(true)
    expect(isPreReleaseVersion('v2.0.0-rc.1')).toBe(true)
    expect(isPreReleaseVersion('2.2.21')).toBe(false)
    expect(isPreReleaseVersion('v1.2.3+build-5')).toBe(false)
  })
})
