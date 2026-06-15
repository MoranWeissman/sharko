import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AddonCatalog } from '@/views/AddonCatalog'
import { AuthProvider } from '@/hooks/useAuth'
import { addAddon } from '@/services/api'

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

// Catalog fixture covers all 4 V126-3.1 (DESIGN-02) tile-badge states:
//
// - ingress-nginx → N=2, M=2 → "Running on 2 clusters"
// - cert-manager  → N=3, M=5 → "Running on 3/5 clusters"
// - addon-target-only → N=0, M=4 → "Not deployed yet"
// - addon-nowhere     → N=0, M=0 → "Not deployed anywhere"
vi.mock('@/services/api', () => ({
  // V2-cleanup-15 — the catalog "Register addon" dialog now sends auto_merge,
  // supports a dry-run preview, and branches on merged. addAddon is mocked
  // per-test; the repo/chart validation endpoints return happy-path shapes so
  // the form reaches a submittable state.
  addAddon: vi.fn(),
  isAddonAlreadyExistsError: () => false,
  api: {
    listRepoCharts: vi.fn().mockResolvedValue({
      valid: true,
      charts: ['my-chart'],
    }),
    validateCatalogChart: vi.fn().mockResolvedValue({
      valid: true,
      repo: 'https://helm.example.com',
      versions: [{ version: '1.2.3' }],
      latest_stable: '1.2.3',
      cached_at: new Date().toISOString(),
    }),
    getAddonCatalog: vi.fn().mockResolvedValue({
      addons: [
        {
          addon_name: 'ingress-nginx',
          chart: 'ingress-nginx',
          repo_url: 'https://kubernetes.github.io/ingress-nginx',
          namespace: 'ingress-nginx',
          version: '4.8.0',
          total_clusters: 10,
          enabled_clusters: 2,
          healthy_applications: 2,
          degraded_applications: 0,
          missing_applications: 0,
          // V126-3.1 (DESIGN-02): N==M, M>0 — "Running on 2 clusters"
          deployed_cluster_count: 2,
          total_target_cluster_count: 2,
          applications: [
            {
              cluster_name: 'cluster-1',
              cluster_environment: 'prod',
              enabled: true,
              configured_version: '4.8.0',
              deployed_version: '4.8.0',
              namespace: 'ingress-nginx',
              health_status: 'Healthy',
              status: 'healthy',
            },
            {
              cluster_name: 'cluster-2',
              cluster_environment: 'dev',
              enabled: true,
              configured_version: '4.8.0',
              deployed_version: '4.8.0',
              namespace: 'ingress-nginx',
              health_status: 'Healthy',
              status: 'healthy',
            },
            {
              cluster_name: 'cluster-disabled',
              enabled: false,
              status: 'disabled',
            },
          ],
        },
        {
          addon_name: 'cert-manager',
          chart: 'cert-manager',
          repo_url: 'https://charts.jetstack.io',
          namespace: 'cert-manager',
          version: '1.13.0',
          total_clusters: 10,
          enabled_clusters: 5,
          healthy_applications: 3,
          degraded_applications: 0,
          missing_applications: 0,
          // V126-3.1 (DESIGN-02): 0 < N < M — "Running on 3/5 clusters"
          deployed_cluster_count: 3,
          total_target_cluster_count: 5,
          applications: [],
        },
        {
          addon_name: 'addon-target-only',
          chart: 'chart-target',
          repo_url: 'https://example.com/charts',
          namespace: 'target',
          version: '1.0.0',
          total_clusters: 10,
          enabled_clusters: 4,
          healthy_applications: 0,
          degraded_applications: 0,
          missing_applications: 4,
          // V126-3.1 (DESIGN-02): N=0, M>0 — "Not deployed yet"
          deployed_cluster_count: 0,
          total_target_cluster_count: 4,
          applications: [],
        },
        {
          addon_name: 'addon-nowhere',
          chart: 'chart-nowhere',
          repo_url: 'https://example.com/charts',
          namespace: 'nowhere',
          version: '1.0.0',
          total_clusters: 10,
          enabled_clusters: 0,
          healthy_applications: 0,
          degraded_applications: 0,
          missing_applications: 0,
          // V126-3.1 (DESIGN-02): M=0 — "Not deployed anywhere"
          deployed_cluster_count: 0,
          total_target_cluster_count: 0,
          applications: [],
        },
      ],
      total_addons: 4,
      total_clusters: 10,
      addons_only_in_git: 2,
    }),
  },
}))

function renderCatalog() {
  return render(
    <MemoryRouter>
      <AddonCatalog />
    </MemoryRouter>,
  )
}

describe('AddonCatalog', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders loading state initially', () => {
    renderCatalog()
    expect(screen.getByText('Loading addon catalog...')).toBeInTheDocument()
  })

  it('renders catalog data after loading', async () => {
    renderCatalog()

    await waitFor(() => {
      // Heading was renamed in v1.21 when the page gained the Marketplace tab.
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })

    // Summary stat cards — now clickable filters
    expect(screen.getAllByText('All Addons').length).toBeGreaterThanOrEqual(1)
    // Fixture has 4 addons.
    expect(screen.getAllByText('4').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Healthy').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Unhealthy').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Catalog Only').length).toBeGreaterThanOrEqual(1)

    // Addon cards
    expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    expect(screen.getByText('cert-manager')).toBeInTheDocument()
  })

  it('renders addon list with data', async () => {
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    })

    // Default is list view — check addon names are in the table
    expect(screen.getByText('cert-manager')).toBeInTheDocument()
  })

  it('renders search input', async () => {
    renderCatalog()

    await waitFor(() => {
      // Heading was renamed in v1.21 when the page gained the Marketplace tab.
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })

    expect(
      screen.getByPlaceholderText('Search addons by name, chart, or namespace...'),
    ).toBeInTheDocument()
  })

  it('renders filter and sort controls', async () => {
    renderCatalog()

    await waitFor(() => {
      // Heading was renamed in v1.21 when the page gained the Marketplace tab.
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })

    // Filter options — "All Addons" appears in both stat card and dropdown
    expect(screen.getAllByText('All Addons').length).toBeGreaterThanOrEqual(1)

    // Page size
    expect(screen.getByText('15 per page')).toBeInTheDocument()
  })
})

/**
 * V126-3.1 (DESIGN-02): the tile-level DeploymentBadge replaces the historical
 * "Installed" / "Catalog Only" headline with one of four state-specific copies
 * driven by (deployed_cluster_count, total_target_cluster_count). The four
 * states are tested via the catalog fixture above which covers them all:
 *
 *  - ingress-nginx       (N=2, M=2) → "Running on 2 clusters"
 *  - cert-manager        (N=3, M=5) → "Running on 3/5 clusters"
 *  - addon-target-only   (N=0, M=4) → "Not deployed yet"
 *  - addon-nowhere       (N=0, M=0) → "Not deployed anywhere"
 *
 * The grid view renders DeploymentBadge per tile. We switch to grid mode
 * before asserting (default is list).
 */
describe('AddonCatalog — DeploymentBadge (V126-3.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  async function renderInGridView() {
    renderCatalog()
    // Default view is grid (see useState<'grid' | 'list'>('grid')), so the
    // DeploymentBadge components render immediately after the catalog data
    // resolves.
    await waitFor(() => {
      expect(screen.getAllByTestId('addon-deployment-badge').length).toBeGreaterThan(0)
    })
  }

  it('renders "Running on N clusters" when N == M', async () => {
    await renderInGridView()
    expect(screen.getByText('Running on 2 clusters')).toBeInTheDocument()
  })

  it('renders "Running on N/M clusters" when 0 < N < M', async () => {
    await renderInGridView()
    expect(screen.getByText('Running on 3/5 clusters')).toBeInTheDocument()
  })

  it('renders "Not deployed yet" when N == 0 and M > 0', async () => {
    await renderInGridView()
    expect(screen.getByText('Not deployed yet')).toBeInTheDocument()
  })

  it('renders "Not deployed anywhere" when M == 0', async () => {
    await renderInGridView()
    expect(screen.getByText('Not deployed anywhere')).toBeInTheDocument()
  })
})

/**
 * V2-cleanup-36: DeploymentBadge new states — sync_failing (red) and
 * deploying (blue). These states are checked first in the priority chain
 * so they surface before the running-count logic.
 */
describe('AddonCatalog — DeploymentBadge V2-cleanup-36 states', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders "Sync failing" badge (red) when any enabled application has status sync_failing', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [
        {
          addon_name: 'keda',
          chart: 'keda',
          repo_url: 'https://kedacore.github.io/charts',
          namespace: 'keda',
          version: '2.13.0',
          total_clusters: 1,
          enabled_clusters: 1,
          healthy_applications: 0,
          degraded_applications: 1,
          missing_applications: 0,
          // keda incident: Running + SyncFailed → sync_failing
          deployed_cluster_count: 0,
          total_target_cluster_count: 1,
          applications: [
            {
              cluster_name: 'prod',
              enabled: true,
              configured_version: '2.13.0',
              status: 'sync_failing',
            },
          ],
        },
      ],
      total_addons: 1,
      total_clusters: 1,
      addons_only_in_git: 0,
    })

    renderCatalog()
    await waitFor(() =>
      expect(screen.getAllByTestId('addon-deployment-badge').length).toBeGreaterThan(0),
    )
    expect(screen.getByText('Sync failing')).toBeInTheDocument()
  })

  it('renders "Deploying…" badge (blue) when deployed=0, target>0, and any enabled app is deploying', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [
        {
          addon_name: 'velero',
          chart: 'velero',
          repo_url: 'https://vmware-tanzu.github.io/helm-charts',
          namespace: 'velero',
          version: '5.1.0',
          total_clusters: 1,
          enabled_clusters: 1,
          healthy_applications: 0,
          degraded_applications: 0,
          missing_applications: 0,
          // Active first rollout — op Running, no failures yet
          deployed_cluster_count: 0,
          total_target_cluster_count: 1,
          applications: [
            {
              cluster_name: 'dev',
              enabled: true,
              configured_version: '5.1.0',
              status: 'deploying',
            },
          ],
        },
      ],
      total_addons: 1,
      total_clusters: 1,
      addons_only_in_git: 0,
    })

    renderCatalog()
    await waitFor(() =>
      expect(screen.getAllByTestId('addon-deployment-badge').length).toBeGreaterThan(0),
    )
    expect(screen.getByText('Deploying…')).toBeInTheDocument()
  })

  it('sync_failing takes priority over the running-count logic', async () => {
    // N=1 (one cluster is healthy) but another has sync_failing —
    // badge should show "Sync failing", not "Running on 1/2 clusters".
    const { api } = await import('@/services/api')
    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [
        {
          addon_name: 'mixed-addon',
          chart: 'mixed',
          repo_url: 'https://example.com',
          namespace: 'mixed',
          version: '1.0.0',
          total_clusters: 2,
          enabled_clusters: 2,
          healthy_applications: 1,
          degraded_applications: 1,
          missing_applications: 0,
          deployed_cluster_count: 1,
          total_target_cluster_count: 2,
          applications: [
            { cluster_name: 'prod', enabled: true, configured_version: '1.0.0', status: 'healthy' },
            { cluster_name: 'staging', enabled: true, configured_version: '1.0.0', status: 'sync_failing' },
          ],
        },
      ],
      total_addons: 1,
      total_clusters: 2,
      addons_only_in_git: 0,
    })

    renderCatalog()
    await waitFor(() =>
      expect(screen.getAllByTestId('addon-deployment-badge').length).toBeGreaterThan(0),
    )
    // sync_failing wins over "Running on 1/2 clusters"
    expect(screen.getByText('Sync failing')).toBeInTheDocument()
    expect(screen.queryByText(/Running on/)).not.toBeInTheDocument()
  })
})

/**
 * V126-3.1 (DESIGN-02): the historical tab value `'installed'` was renamed
 * to `'catalog'`. This regression test asserts:
 *
 *  1. The new tab is labelled "Catalog" and is rendered/selected by default.
 *  2. The URL ?tab=catalog convention works (default state has no ?tab= so
 *     the absence of the param is the canonical default state).
 *  3. Loading the stale `?tab=installed` URL does NOT crash — it is
 *     normalised to the default tab (stripped) by a one-shot redirect.
 */
describe('AddonCatalog — Catalog tab rename (V126-3.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the tab labelled "Catalog" as default-selected', async () => {
    render(
      <MemoryRouter initialEntries={['/addons']}>
        <AddonCatalog />
      </MemoryRouter>,
    )
    const catalogTab = await screen.findByRole('tab', { name: /catalog/i })
    expect(catalogTab).toBeInTheDocument()
    expect(catalogTab).toHaveAttribute('aria-selected', 'true')
    // The legacy "Installed" tab name is gone.
    expect(screen.queryByRole('tab', { name: /^installed$/i })).not.toBeInTheDocument()
  })

  it('does not crash when given the stale ?tab=installed URL', async () => {
    render(
      <MemoryRouter initialEntries={['/addons?tab=installed']}>
        <AddonCatalog />
      </MemoryRouter>,
    )
    // Renders the catalog tab as the active selection (the stale value is
    // normalised — not respected).
    const catalogTab = await screen.findByRole('tab', { name: /catalog/i })
    expect(catalogTab).toHaveAttribute('aria-selected', 'true')
  })
})

/**
 * V2-cleanup-15.1 — the catalog "Register addon" dialog reaches parity with
 * the Marketplace add-addon flow (#397):
 *   - an admin-gated auto-merge toggle whose value is sent on addAddon
 *   - a dry-run Preview step that renders DryRunResult.files_to_write
 *   - an HONEST merged-vs-open outcome: a merged PR refreshes the catalog;
 *     an open PR does NOT (the addon isn't in git yet) and surfaces the
 *     clickable PR via pr_url instead.
 */
describe('AddonCatalog — add-addon parity flow (V2-cleanup-15.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sessionStorage.clear()
    sessionStorage.setItem('sharko-auth-token', 'test-token')
    sessionStorage.setItem('sharko-auth-user', 'tester')
    sessionStorage.setItem('sharko-auth-role', 'admin')
  })

  async function renderAndOpenDialog() {
    render(
      <MemoryRouter initialEntries={['/addons']}>
        <AuthProvider>
          <AddonCatalog />
        </AuthProvider>
      </MemoryRouter>,
    )
    // Wait for the catalog to finish loading, then open the dialog.
    const addBtn = await screen.findByRole('button', { name: /add addon/i })
    fireEvent.click(addBtn)
    return await screen.findByRole('dialog')
  }

  // Fill the form so it reaches a submittable state: name, repo URL (which
  // fires the debounced validation → marks repo valid + offers charts), chart,
  // and version (auto-selected from latest_stable once chart validates).
  async function fillSubmittableForm(dialog: HTMLElement) {
    const byId = (id: string) =>
      dialog.querySelector(`#${id}`) as HTMLInputElement
    fireEvent.change(byId('add-addon-name'), {
      target: { value: 'my-addon' },
    })
    fireEvent.change(byId('add-addon-repo'), {
      target: { value: 'https://helm.example.com' },
    })
    // Repo validation is debounced; wait for the chart input to enable.
    const chartInput = byId('add-addon-chart')
    await waitFor(() => expect(chartInput).toBeEnabled())
    fireEvent.change(chartInput, { target: { value: 'my-chart' } })
    // Version auto-selects latest_stable (1.2.3) once the chart validates.
    await waitFor(() =>
      expect(
        within(dialog).getByRole('button', { name: /register addon/i }),
      ).toBeEnabled(),
    )
  }

  // V2-cleanup-40: per-flow auto-merge toggle removed. The global GitOps
  // setting governs; no auto_merge is sent on the addAddon call.
  it('does NOT render the auto-merge toggle and does NOT send auto_merge', async () => {
    vi.mocked(addAddon).mockResolvedValue({
      pr_id: 7,
      pr_url: 'https://gh/pr/7',
      merged: false,
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    // The toggle must be gone.
    expect(
      within(dialog).queryByLabelText(/merge pr automatically/i),
    ).not.toBeInTheDocument()

    // Shows the global-setting hint text.
    expect(
      within(dialog).getByText(/global GitOps setting/i),
    ).toBeInTheDocument()

    fireEvent.click(
      within(dialog).getByRole('button', { name: /register addon/i }),
    )

    await waitFor(() => expect(addAddon).toHaveBeenCalled())
    const arg = vi.mocked(addAddon).mock.calls[0][0]
    // auto_merge must NOT be present on the call.
    expect(arg.auto_merge).toBeUndefined()
    expect(arg.dry_run).toBe(false)
  })

  it('previews the files that would be written (dry-run) without opening a PR', async () => {
    vi.mocked(addAddon).mockResolvedValue({
      dry_run: {
        pr_title: 'sharko: add addon my-addon',
        files_to_write: [
          { path: 'configuration/addons-catalog.yaml', action: 'update' },
          { path: 'configuration/addons-global-values/my-addon.yaml', action: 'create' },
        ],
      },
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    fireEvent.click(within(dialog).getByRole('button', { name: /preview/i }))

    await waitFor(() =>
      expect(
        within(dialog).getByText(
          'configuration/addons-global-values/my-addon.yaml',
        ),
      ).toBeInTheDocument(),
    )
    expect(
      within(dialog).getByText('configuration/addons-catalog.yaml'),
    ).toBeInTheDocument()
    // The preview call set dry_run:true and did NOT open a PR.
    expect(vi.mocked(addAddon).mock.calls[0][0].dry_run).toBe(true)
  })

  it('merged===true refreshes the catalog and confirms it was added', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(addAddon).mockResolvedValue({
      pr_id: 8,
      pr_url: 'https://gh/pr/8',
      merged: true,
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    const catalogCallsBefore = vi.mocked(api.getAddonCatalog).mock.calls.length
    fireEvent.click(
      within(dialog).getByRole('button', { name: /register addon/i }),
    )

    // Merged → catalog refetched + "added to your catalog" toast.
    await waitFor(() =>
      expect(vi.mocked(api.getAddonCatalog).mock.calls.length).toBeGreaterThan(
        catalogCallsBefore,
      ),
    )
    expect(
      await screen.findByText(/added to your catalog/i),
    ).toBeInTheDocument()
  })

  it('merged===false does NOT refresh the catalog and shows the clickable PR', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(addAddon).mockResolvedValue({
      pr_id: 9,
      pr_url: 'https://gh/pr/9',
      merged: false,
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    const catalogCallsBefore = vi.mocked(api.getAddonCatalog).mock.calls.length
    fireEvent.click(
      within(dialog).getByRole('button', { name: /register addon/i }),
    )

    // Open PR → lifecycle progress window with a clickable PR link to pr_url.
    const prLink = await within(dialog).findByRole('link', {
      name: /view pr #9 on github/i,
    })
    expect(prLink).toHaveAttribute('href', 'https://gh/pr/9')
    // The honest "open for review" copy — NOT presented as cataloged.
    // V2-cleanup-40: PRLifecycleProgress shows the openLabel.
    await waitFor(() => {
      expect(
        within(dialog).getByText(/PR open for review/i),
      ).toBeInTheDocument()
    })
    // The catalog was NOT refetched while the PR is still open.
    expect(vi.mocked(api.getAddonCatalog).mock.calls.length).toBe(
      catalogCallsBefore,
    )
  })
})
