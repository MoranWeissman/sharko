import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AddonCatalog } from '@/views/AddonCatalog'
import { AuthProvider } from '@/hooks/useAuth'
import { addAddon, api } from '@/services/api'

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
    // V2-cleanup-61.3 (A2/B2): the Marketplace tab is now reachable from
    // both the primary "Browse Marketplace" CTA and the empty-catalog
    // state, so MarketplaceTab/MarketplaceBrowseTab's data call needs a
    // fixture too.
    listCuratedCatalog: vi.fn().mockResolvedValue({ addons: [] }),
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
    // V2-cleanup-61.2 (D1): the benign stat is "Not deployed yet"; the
    // ambiguous "Catalog Only" wording is retired.
    expect(screen.getAllByText('Not deployed yet').length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText('Catalog Only')).not.toBeInTheDocument()

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

// V2-cleanup-61.3 (A2): the Marketplace is the recommended path for adding
// an addon — it must get the prominent, primary CTA. The manual chart-URL
// dialog is the advanced/secondary path now, demoted but still reachable.
describe('AddonCatalog — Marketplace front door (V2-cleanup-61.3, A2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows "Browse Marketplace" as the primary CTA and switches to the Marketplace tab', async () => {
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })

    const marketplaceBtn = screen.getByRole('button', { name: /browse marketplace/i })
    fireEvent.click(marketplaceBtn)

    // Switching tabs re-renders the page header with the Marketplace copy.
    await waitFor(() => {
      expect(
        screen.getByText(/discover approved addons/i),
      ).toBeInTheDocument()
    })
  })
})

// V2-cleanup-61.3 (B2): the empty catalog state used to be a dead end that
// never mentioned the Marketplace.
describe('AddonCatalog — empty catalog points to the Marketplace (V2-cleanup-61.3, B2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows a "Browse the Marketplace" affordance when the catalog has no addons', async () => {
    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [],
      total_addons: 0,
      total_clusters: 0,
      addons_only_in_git: 0,
    })
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })

    expect(screen.getByText(/catalog is empty/i)).toBeInTheDocument()
    const btn = screen.getByRole('button', { name: /browse the marketplace/i })
    fireEvent.click(btn)

    await waitFor(() => {
      expect(
        screen.getByText(/discover approved addons/i),
      ).toBeInTheDocument()
    })
  })
})

/**
 * V126-3.1 (DESIGN-02) + V2-cleanup-61.2 vocabulary: the tile-level
 * DeploymentBadge renders one state-specific copy driven by
 * (deployed_cluster_count, total_target_cluster_count). The four states
 * are tested via the catalog fixture above which covers them all:
 *
 *  - ingress-nginx       (N=2, M=2) → "Running on 2 clusters"
 *  - cert-manager        (N=3, M=5) → "Running on 3/5 clusters"
 *  - addon-target-only   (N=0, M=4) → "Waiting to deploy"   (amber)
 *  - addon-nowhere       (N=0, M=0) → "Not deployed yet"    (neutral, benign)
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

  // LW-13: "Waiting to deploy" amber state removed from catalog tile when
  // deployed==0 && target>0 (deploy-progress is a fleet concern, not a catalog
  // concern). The badge shows "Not deployed yet" instead; the coverage count
  // below is the signal.
  it('does NOT render "Waiting to deploy" on the catalog tile when N == 0 and M > 0', async () => {
    await renderInGridView()
    expect(screen.queryByText('Waiting to deploy')).not.toBeInTheDocument()
    // addon-target-only fixture: deployed=0, target=4 → shows "Not deployed yet"
    const badges = screen
      .getAllByTestId('addon-deployment-badge')
      .filter((b) => b.textContent === 'Not deployed yet')
    // Should have more than one "Not deployed yet" badge now (addon-nowhere +
    // addon-target-only both show the neutral badge).
    expect(badges.length).toBeGreaterThan(1)
  })

  it('renders the benign "Not deployed yet" badge when M == 0 (enabled nowhere)', async () => {
    await renderInGridView()
    const badges = screen
      .getAllByTestId('addon-deployment-badge')
      .filter((b) => b.textContent === 'Not deployed yet')
    // LW-13: both addon-nowhere (M=0) AND addon-target-only (deployed=0, M=4)
    // now show "Not deployed yet" because we dropped the amber "Waiting to deploy".
    expect(badges).toHaveLength(2)
  })

  // LW-16: grid card MUST show version (previously only list + expanded table had it)
  it('grid card shows addon version in the header', async () => {
    await renderInGridView()
    // ingress-nginx fixture has version 4.8.0
    expect(screen.getByText(/Version: 4\.8\.0/)).toBeInTheDocument()
    // cert-manager fixture has version 1.13.0
    expect(screen.getByText(/Version: 1\.13\.0/)).toBeInTheDocument()
  })

  // LW-15: coverage count wording consistent across grid and list
  it('grid card shows "Installed on N/M clusters" coverage count', async () => {
    await renderInGridView()
    // cert-manager fixture: deployed=3, target=5
    expect(screen.getByText(/Installed on 3\/5 clusters/)).toBeInTheDocument()
  })

  // LW-14: per-cluster health removed from catalog tile
  it('grid card does NOT render per-cluster health bar or health chips', async () => {
    await renderInGridView()
    // No "healthy" text in the health progress bar (the bar is removed)
    expect(screen.queryByText(/\/.*healthy/)).not.toBeInTheDocument()
    // No StatusChip labels ("Healthy", "Degraded", "Missing from ArgoCD")
    // in the tile body (these chips were removed from the tile)
    const cards = screen.getAllByText('ingress-nginx').map(el => el.closest('div.group'))
    const firstCard = cards[0]
    if (firstCard) {
      // The card should NOT contain the per-cluster health chips that were
      // previously at lines 313-315. The expanded detail table (line 366) may
      // still show health, but the unexpanded tile does not.
      expect(firstCard.textContent).not.toMatch(/Healthy.*Degraded/)
    }
  })
})

/**
 * V2-cleanup-61.2 (finding D1): "Catalog Only" used to mean BOTH the benign
 * "enabled on no cluster yet" state AND the problem "enabled but missing
 * from ArgoCD" state. The two now have distinct names — and for any single
 * addon the two names never render together, because they describe
 * mutually exclusive situations.
 */
describe('AddonCatalog — D1 vocabulary split (V2-cleanup-61.2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  async function renderInGridView() {
    renderCatalog()
    await waitFor(() => {
      expect(screen.getAllByTestId('addon-deployment-badge').length).toBeGreaterThan(0)
    })
  }

  function cardOf(addonName: string): HTMLElement {
    const card = screen.getByText(addonName).closest('div.group') as HTMLElement
    expect(card).toBeTruthy()
    return card
  }

  // LW-14: per-cluster health chips removed from the catalog tile, so these
  // labels no longer render on the tile at all. The list-view column header
  // at line 480 was reworded per LW-12, but the card body chips are gone.
  it('an addon enabled nowhere shows "Not deployed yet" badge and no health chips', async () => {
    await renderInGridView()
    const card = cardOf('addon-nowhere') // enabled_clusters=0, missing=0
    expect(within(card).getByText('Not deployed yet')).toBeInTheDocument()
    // LW-14: StatusChips removed from tile, so no "Missing from ArgoCD" chip.
    expect(within(card).queryByText(/Missing from ArgoCD/)).not.toBeInTheDocument()
  })

  it('an addon with apps missing from ArgoCD does NOT show health chips on the tile', async () => {
    await renderInGridView()
    const card = cardOf('addon-target-only') // enabled_clusters=4, missing=4
    // LW-14: the per-cluster health StatusChips are removed from the tile,
    // so "Missing from ArgoCD" (or its LW-12 rewording) doesn't render here.
    expect(within(card).queryByText(/Missing from ArgoCD/)).not.toBeInTheDocument()
    expect(within(card).queryByText(/Enabled but not created in ArgoCD/)).not.toBeInTheDocument()
  })

  it('the retired "Catalog Only" spelling never renders anywhere on the page', async () => {
    await renderInGridView()
    expect(screen.queryByText(/Catalog Only/i)).not.toBeInTheDocument()
  })

  // LW-12: "Missing from ArgoCD" column header reworded to be unambiguous
  it('list view shows "Enabled but not created in ArgoCD" column header (LW-12)', async () => {
    renderCatalog()
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })
    // Switch to list view to see the table header
    const listViewBtn = screen.getByRole('button', { name: /list view/i })
    fireEvent.click(listViewBtn)
    await waitFor(() => {
      expect(screen.getByText(/Enabled but not created in ArgoCD/)).toBeInTheDocument()
    })
  })

  // LW-15: list view coverage text matches grid ("Installed on N/M clusters")
  it('list view shows "Installed on N/M clusters" in the Deployed column', async () => {
    renderCatalog()
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })
    // Switch to list view
    const listViewBtn = screen.getByRole('button', { name: /list view/i })
    fireEvent.click(listViewBtn)
    await waitFor(() => {
      // cert-manager fixture: deployed=3, target=5
      expect(screen.getByText(/Installed on 3\/5 clusters/)).toBeInTheDocument()
    })
  })
})

/**
 * V2-cleanup-61.2 handover: the Dashboard's "addons with drift" button
 * deep-links to /addons?drift=true (via the /version-matrix redirect that
 * 61.1 taught to preserve the query). The catalog must consume the param
 * and land pre-filtered on drifted addons.
 */
describe('AddonCatalog — ?drift=true deep-link (V2-cleanup-61.2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('lands filtered to addons with version drift when ?drift=true is present', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [
        {
          addon_name: 'drifted-addon',
          chart: 'drifted-chart',
          repo_url: 'https://example.com/charts',
          namespace: 'drift',
          version: '2.0.0',
          total_clusters: 1,
          enabled_clusters: 1,
          healthy_applications: 1,
          degraded_applications: 0,
          missing_applications: 0,
          deployed_cluster_count: 1,
          total_target_cluster_count: 1,
          applications: [
            {
              cluster_name: 'prod',
              enabled: true,
              configured_version: '2.0.0',
              deployed_version: '1.9.0', // ≠ catalog version → drift
              status: 'healthy',
            },
          ],
        },
        {
          addon_name: 'steady-addon',
          chart: 'steady-chart',
          repo_url: 'https://example.com/charts',
          namespace: 'steady',
          version: '1.0.0',
          total_clusters: 1,
          enabled_clusters: 1,
          healthy_applications: 1,
          degraded_applications: 0,
          missing_applications: 0,
          deployed_cluster_count: 1,
          total_target_cluster_count: 1,
          applications: [
            {
              cluster_name: 'prod',
              enabled: true,
              configured_version: '1.0.0',
              deployed_version: '1.0.0',
              status: 'healthy',
            },
          ],
        },
      ],
      total_addons: 2,
      total_clusters: 1,
      addons_only_in_git: 0,
    })

    render(
      <MemoryRouter initialEntries={['/addons?drift=true']}>
        <AddonCatalog />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByText('drifted-addon')).toBeInTheDocument()
    })
    // The non-drifted addon is filtered out.
    expect(screen.queryByText('steady-addon')).not.toBeInTheDocument()
    // The filter dropdown reflects the drift filter.
    expect(
      (screen.getByDisplayValue('With version drift') as HTMLSelectElement).value,
    ).toBe('drifted')
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

  // V2-cleanup-66.1 — a merged PR used to close the dialog instantly (a toast
  // was the only signal). Now the dialog STAYS OPEN showing the lifecycle
  // window's terminal "Merged" state with an explicit "View addon" button —
  // the catalog still refreshes in the background so it's ready when the
  // user chooses to leave.
  it('merged===true keeps the dialog open, refreshes the catalog in the background, and offers View addon', async () => {
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

    // Merged → catalog refetched in the background (no instant close).
    await waitFor(() =>
      expect(vi.mocked(api.getAddonCatalog).mock.calls.length).toBeGreaterThan(
        catalogCallsBefore,
      ),
    )
    // The dialog is still open, with at least one "added to your catalog"
    // confirmation visible (the toast and/or the lifecycle banner).
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(
      screen.getAllByText(/added to your catalog/i).length,
    ).toBeGreaterThan(0)

    // Clicking "View addon" navigates and closes the dialog — the user
    // decides when to leave, it isn't automatic.
    fireEvent.click(
      within(dialog).getByRole('button', { name: /view addon/i }),
    )
    expect(mockNavigate).toHaveBeenCalledWith('/addons/my-addon')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(),
    )
  })

  it('merged===true — "Add another" resets the form but keeps the dialog open', async () => {
    vi.mocked(addAddon).mockResolvedValue({
      pr_id: 8,
      pr_url: 'https://gh/pr/8',
      merged: true,
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    fireEvent.click(
      within(dialog).getByRole('button', { name: /register addon/i }),
    )
    await screen.findByRole('button', { name: /add another/i })

    fireEvent.click(screen.getByRole('button', { name: /add another/i }))

    // Dialog stays open, form is back to its empty/submittable-again state.
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(
      (dialog.querySelector('#add-addon-name') as HTMLInputElement).value,
    ).toBe('')
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

    // Terminal state offers "Track on Dashboard" instead of an automatic
    // jump (V2-cleanup-66.1). Clicking it navigates and closes the dialog.
    fireEvent.click(
      within(dialog).getByRole('button', { name: /track on dashboard/i }),
    )
    expect(mockNavigate).toHaveBeenCalledWith('/dashboard?prs_state=pending')
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument(),
    )
  })
})
