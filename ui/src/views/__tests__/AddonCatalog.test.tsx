import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AddonCatalog, ADDON_CATALOG_CACHE_KEY } from '@/views/AddonCatalog'
import { AuthProvider } from '@/hooks/useAuth'
import { api } from '@/services/api'
import { setCached } from '@/lib/viewCache'

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
// v4 walk-findings W2, item 5: /prs?status=open&operation=catalog-add,... is
// fetched via the standalone fetchTrackedPRs export, not the `api` object —
// mocked at module scope so per-test overrides can use vi.mocked(...).
const mockFetchTrackedPRs = vi.fn().mockResolvedValue({ prs: [] })

vi.mock('@/services/api', () => ({
  fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
  api: {
    // V2-cleanup-15 / v4 wave 2.5 review B-3 — the "Add your own chart"
    // dialog now posts to POST /api/v1/catalog/addons (addToCatalog), not
    // the legacy /addons endpoint (which 409s on a v4 repo). Mocked
    // per-test; the repo/chart validation endpoints return happy-path
    // shapes so the form reaches a submittable state.
    addToCatalog: vi.fn(),
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
    // v4 walk-findings W2, item 4: the optional "also enable on a cluster"
    // selector fetches managed clusters on mount.
    getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
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
    // Matrix view (VersionMatrixTable) fetches this on mount whenever the
    // view toggle — or a `?view=matrix` deep-link from the Dashboard's
    // Upgrades card — lands on it. Empty by default; overridden per test.
    getVersionMatrix: vi.fn().mockResolvedValue({ clusters: [], addons: [] }),
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

// v4 walk-findings W2, item 1: the redundant "Browse Marketplace" action-bar
// CTA was dropped — the always-visible Marketplace TAB does exactly the
// same thing, so it's the one door in now. The action bar must NOT render a
// second "Browse Marketplace" button, and clicking the tab still switches
// the page header to the Marketplace copy.
describe('AddonCatalog — Marketplace tab is the one door in (v4 walk-findings W2, item 1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('does not render a redundant "Browse Marketplace" action-bar button', async () => {
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })

    expect(
      screen.queryByRole('button', { name: /browse marketplace/i }),
    ).not.toBeInTheDocument()
  })

  it('switches to the Marketplace tab via the tab control', async () => {
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })

    const marketplaceTab = screen.getByRole('tab', { name: /marketplace/i })
    fireEvent.click(marketplaceTab)

    // Switching tabs re-renders the page header with the Marketplace copy.
    await waitFor(() => {
      expect(
        screen.getByText(/browse addons you could add to your catalog/i),
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
        screen.getByText(/browse addons you could add to your catalog/i),
      ).toBeInTheDocument()
    })
  })

  // v4 wave 2.5 (decision 3) — day zero is a valid, empty catalog with two
  // doors in: pick from the Marketplace, or add your own chart. Both are
  // admin-only (RoleGuard), so this test renders with an admin session.
  it('shows both empty-catalog doors ("Browse the Marketplace" + "Add your own chart") to an admin', async () => {
    // Auth session lives in localStorage — see ui/src/lib/authStorage.ts.
    localStorage.clear()
    localStorage.setItem('sharko-auth-token', 'test-token')
    localStorage.setItem('sharko-auth-user', 'tester')
    localStorage.setItem('sharko-auth-role', 'admin')

    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [],
      total_addons: 0,
      total_clusters: 0,
      addons_only_in_git: 0,
    })
    render(
      <MemoryRouter>
        <AuthProvider>
          <AddonCatalog />
        </AuthProvider>
      </MemoryRouter>,
    )

    const emptyState = await screen.findByTestId('catalog-empty-state')
    expect(
      within(emptyState).getByText(/nothing runs in your org that you didn.t put here/i),
    ).toBeInTheDocument()
    expect(
      within(emptyState).getByRole('button', { name: /browse the marketplace/i }),
    ).toBeInTheDocument()
    expect(
      within(emptyState).getByRole('button', { name: /add your own chart/i }),
    ).toBeInTheDocument()

    localStorage.clear()
  })

  // The locked two-surface sentence (design decision 8) should be visible
  // wherever Catalog and Marketplace are explained — the Addons page header.
  it('shows the locked Catalog-vs-Marketplace sentence in the page header', async () => {
    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [],
      total_addons: 0,
      total_clusters: 0,
      addons_only_in_git: 0,
    })
    renderCatalog()
    await waitFor(() => {
      expect(
        screen.getByText(/your clusters run only what.s enabled from the catalog/i),
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
 * Walk finding #1: the version matrix view is now linkable. The Dashboard's
 * Upgrades card deep-links to /addons?view=matrix (via the /version-matrix
 * redirect, same pattern as the ?drift=true deep-link above) so clicking it
 * lands directly on the matrix instead of the plain catalog grid.
 */
describe('AddonCatalog — ?view=matrix deep-link (walk finding #1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('lands on the version matrix view when ?view=matrix is present', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce({
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'cert-manager',
          catalog_version: '1.14.5',
          chart: 'cert-manager',
          cells: { 'prod-eu': { version: '1.14.5', health: 'healthy', drift_from_catalog: false } },
          newest_available: '1.14.5',
          last_checked: '2026-07-29T12:00:00Z',
        },
      ],
    })

    render(
      <MemoryRouter initialEntries={['/addons?view=matrix']}>
        <AddonCatalog />
      </MemoryRouter>,
    )

    // The matrix table rendered, not the grid/list view.
    await waitFor(() => {
      expect(screen.getAllByText('cert-manager').length).toBeGreaterThan(0)
    })
    expect(screen.getByText('prod-eu')).toBeInTheDocument()
    // The grid pagination copy ("Showing N addons") only renders outside
    // matrix mode.
    expect(screen.queryByText(/Showing \d+ addons?/)).not.toBeInTheDocument()
    // The matrix toggle button reflects the active view.
    expect(screen.getByRole('button', { name: /version matrix view/i }).className).toContain('bg-teal-600')
  })

  it('defaults to the grid view when ?view is absent', async () => {
    render(
      <MemoryRouter initialEntries={['/addons']}>
        <AddonCatalog />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    })
    expect(screen.getByRole('button', { name: /grid view/i }).className).toContain('bg-teal-600')
  })
})

/**
 * WQ-2: the Fleet Status Strip's Upgrades segment is now a bare clickable
 * number that deep-links to /addons?view=matrix&filter=outdated (same
 * pattern as ?view=matrix and ?drift=true above) so it lands directly on
 * the matrix, already filtered to just the outdated rows.
 */
describe('AddonCatalog — ?filter=outdated deep-link (WQ-2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  const twoRowMatrix = {
    clusters: ['prod-eu'],
    addons: [
      {
        addon_name: 'up-to-date-addon',
        catalog_version: '1.0.0',
        chart: 'up-to-date-addon',
        cells: { 'prod-eu': { version: '1.0.0', health: 'healthy', drift_from_catalog: false } },
        newest_available: '1.0.0',
        last_checked: '2026-07-29T12:00:00Z',
      },
      {
        addon_name: 'outdated-addon',
        catalog_version: '1.0.0',
        chart: 'outdated-addon',
        cells: { 'prod-eu': { version: '1.0.0', health: 'healthy', drift_from_catalog: false } },
        newest_available: '2.0.0',
        last_checked: '2026-07-29T12:00:00Z',
      },
    ],
  }

  it('lands on the matrix pre-filtered to outdated rows when ?view=matrix&filter=outdated is present', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce(twoRowMatrix)

    render(
      <MemoryRouter initialEntries={['/addons?view=matrix&filter=outdated']}>
        <AddonCatalog />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByText('outdated-addon')).toBeInTheDocument()
    })
    expect(screen.queryByText('up-to-date-addon')).not.toBeInTheDocument()
    expect(screen.getByTestId('matrix-outdated-chip')).toBeInTheDocument()
  })

  it('clearing the chip shows every row again and drops ?filter from the URL', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce(twoRowMatrix)

    render(
      <MemoryRouter initialEntries={['/addons?view=matrix&filter=outdated']}>
        <AddonCatalog />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByTestId('matrix-outdated-chip')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /clear the outdated filter/i }))

    await waitFor(() => {
      expect(screen.getByText('up-to-date-addon')).toBeInTheDocument()
    })
    expect(screen.queryByTestId('matrix-outdated-chip')).not.toBeInTheDocument()
  })

  it('matrix shows every row (no chip) when ?filter is absent', async () => {
    const { api } = await import('@/services/api')
    vi.mocked(api.getVersionMatrix).mockResolvedValueOnce(twoRowMatrix)

    render(
      <MemoryRouter initialEntries={['/addons?view=matrix']}>
        <AddonCatalog />
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(screen.getByText('up-to-date-addon')).toBeInTheDocument()
    })
    expect(screen.getByText('outdated-addon')).toBeInTheDocument()
    expect(screen.queryByTestId('matrix-outdated-chip')).not.toBeInTheDocument()
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
 * V2-cleanup-15.1 — the catalog "Add your own chart" dialog reaches parity
 * with the Marketplace add-addon flow (#397):
 *   - an admin-gated auto-merge toggle whose value is sent on the write call
 *   - a dry-run Preview step that renders DryRunResult.files_to_write
 *   - an HONEST merged-vs-open outcome: a merged PR refreshes the catalog;
 *     an open PR does NOT (the addon isn't in git yet) and surfaces the
 *     clickable PR via pr_url instead.
 *
 * v4 wave 2.5 review fix round, B-3 — this dialog now posts to
 * POST /api/v1/catalog/addons (addToCatalog), not the legacy POST /addons
 * (which 409s with code repo_layout on a v4 repo). The mocks below assert
 * the AddToCatalogRequest shape the real server reads.
 */
describe('AddonCatalog — add-addon parity flow (V2-cleanup-15.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    sessionStorage.clear()
    // Auth session lives in localStorage — see ui/src/lib/authStorage.ts.
    localStorage.clear()
    localStorage.setItem('sharko-auth-token', 'test-token')
    localStorage.setItem('sharko-auth-user', 'tester')
    localStorage.setItem('sharko-auth-role', 'admin')
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
        within(dialog).getByRole('button', { name: /add to catalog/i }),
      ).toBeEnabled(),
    )
  }

  // V2-cleanup-40: per-flow auto-merge toggle removed. The global GitOps
  // setting governs; no auto_merge is sent on the write call.
  it('does NOT render the auto-merge toggle and does NOT send auto_merge', async () => {
    vi.mocked(api.addToCatalog).mockResolvedValue({
      added: ['my-addon'],
      enabled: [],
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
      within(dialog).getByRole('button', { name: /add to catalog/i }),
    )

    await waitFor(() => expect(api.addToCatalog).toHaveBeenCalled())
    const arg = vi.mocked(api.addToCatalog).mock.calls[0][0]
    // auto_merge must NOT be present on the call.
    expect(arg.auto_merge).toBeUndefined()
    expect(arg.dry_run).toBe(false)
    expect(arg.addons).toEqual([
      expect.objectContaining({
        name: 'my-addon',
        from_marketplace: false,
        chart: 'my-chart',
        repo_url: 'https://helm.example.com',
      }),
    ])
  })

  it('previews the files that would be written (dry-run) without opening a PR', async () => {
    vi.mocked(api.addToCatalog).mockResolvedValue({
      added: [],
      enabled: [],
      dry_run: {
        pr_title: 'sharko: add my-addon to catalog',
        files_to_write: [
          { path: 'catalog.yaml', action: 'update' },
          { path: 'values/global/my-addon.yaml', action: 'create' },
        ],
      },
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    fireEvent.click(within(dialog).getByRole('button', { name: /preview/i }))

    await waitFor(() =>
      expect(
        within(dialog).getByText('values/global/my-addon.yaml'),
      ).toBeInTheDocument(),
    )
    expect(
      within(dialog).getByText('catalog.yaml'),
    ).toBeInTheDocument()
    // The preview call set dry_run:true and did NOT open a PR.
    expect(vi.mocked(api.addToCatalog).mock.calls[0][0].dry_run).toBe(true)
  })

  // V2-cleanup-66.1 — a merged PR used to close the dialog instantly (a toast
  // was the only signal). Now the dialog STAYS OPEN showing the lifecycle
  // window's terminal "Merged" state with an explicit "View addon" button —
  // the catalog still refreshes in the background so it's ready when the
  // user chooses to leave.
  it('merged===true keeps the dialog open, refreshes the catalog in the background, and offers View addon', async () => {
    vi.mocked(api.addToCatalog).mockResolvedValue({
      added: ['my-addon'],
      enabled: [],
      pr_id: 8,
      pr_url: 'https://gh/pr/8',
      merged: true,
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    const catalogCallsBefore = vi.mocked(api.getAddonCatalog).mock.calls.length
    fireEvent.click(
      within(dialog).getByRole('button', { name: /add to catalog/i }),
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
    vi.mocked(api.addToCatalog).mockResolvedValue({
      added: ['my-addon'],
      enabled: [],
      pr_id: 8,
      pr_url: 'https://gh/pr/8',
      merged: true,
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    fireEvent.click(
      within(dialog).getByRole('button', { name: /add to catalog/i }),
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
    vi.mocked(api.addToCatalog).mockResolvedValue({
      added: ['my-addon'],
      enabled: [],
      pr_id: 9,
      pr_url: 'https://gh/pr/9',
      merged: false,
    })
    const dialog = await renderAndOpenDialog()
    await fillSubmittableForm(dialog)

    const catalogCallsBefore = vi.mocked(api.getAddonCatalog).mock.calls.length
    fireEvent.click(
      within(dialog).getByRole('button', { name: /add to catalog/i }),
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

  // v4 wave 2.5 review fix round, item 10 — the empty-catalog "Add your own
  // chart" door and the admin "Add addon manually" trigger open the SAME
  // dialog and hit the SAME real endpoint; this exercises the empty-state
  // door specifically end to end.
  it('the empty-catalog "Add your own chart" door opens the dialog and posts to the real POST /catalog/addons', async () => {
    vi.mocked(api.getAddonCatalog).mockResolvedValueOnce({
      addons: [],
      total_addons: 0,
      total_clusters: 0,
      addons_only_in_git: 0,
    })
    vi.mocked(api.addToCatalog).mockResolvedValue({
      added: ['my-addon'],
      enabled: [],
      pr_id: 11,
      pr_url: 'https://gh/pr/11',
      merged: true,
    })

    render(
      <MemoryRouter initialEntries={['/addons']}>
        <AuthProvider>
          <AddonCatalog />
        </AuthProvider>
      </MemoryRouter>,
    )

    const emptyState = await screen.findByTestId('catalog-empty-state')
    fireEvent.click(
      within(emptyState).getByRole('button', { name: /add your own chart/i }),
    )
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(/add your own chart/i),
    ).toBeInTheDocument()

    await fillSubmittableForm(dialog)
    fireEvent.click(
      within(dialog).getByRole('button', { name: /add to catalog/i }),
    )

    await waitFor(() => expect(api.addToCatalog).toHaveBeenCalled())
    expect(vi.mocked(api.addToCatalog).mock.calls[0][0].addons[0]).toEqual(
      expect.objectContaining({ name: 'my-addon', from_marketplace: false }),
    )
  })
})

/**
 * Pending addons as ghost cards (walk finding) — a catalog-add PR that's
 * still open is invisible on GET /addons/catalog (merged-branch-only
 * read). Rather than a separate "Pending Addons" lane (the old design),
 * the maintainer's call was: render it INSIDE the normal grid/list, same
 * card shape as a real addon, but transparent with a subtle amber tint
 * and a "Pending" badge instead of "Not deployed yet" — and never link to
 * the addon detail route, which 404s until the PR merges.
 */
describe('AddonCatalog — pending addons render as ghost cards in the real grid', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  const pendingPR = {
    pr_id: 77,
    pr_url: 'https://gh/pr/77',
    pr_branch: 'sharko/catalog-add-loki',
    pr_title: 'sharko: add loki to catalog',
    addon: 'loki',
    operation: 'catalog-add',
    user: 'tester',
    source: 'ui',
    created_at: new Date().toISOString(),
    last_status: 'open',
    last_polled_at: new Date().toISOString(),
  }

  it('renders a pending ghost card in the grid: pending chip, tint/opacity, links to the PR, not to /addons/<name>', async () => {
    mockFetchTrackedPRs.mockResolvedValue({ prs: [pendingPR] })
    renderCatalog()

    const ghost = await screen.findByTestId('pending-addon-card')
    expect(within(ghost).getByText('loki')).toBeInTheDocument()
    expect(within(ghost).getByText('Pending')).toBeInTheDocument()

    // Same visual family as a real card (ring + rounded), but transparent
    // with a subtle amber tint — never the plain blue card background.
    expect(ghost.className).toContain('opacity-60')
    expect(ghost.className).toMatch(/bg-amber-50|bg-amber-950/)

    // Links straight to the PR, in a new tab — never to the addon detail
    // route (that page 404s until the PR merges).
    expect(ghost.tagName).toBe('A')
    expect(ghost).toHaveAttribute('href', 'https://gh/pr/77')
    expect(ghost).toHaveAttribute('target', '_blank')
    expect(ghost.getAttribute('href')).not.toContain('/addons/loki')
  })

  it('renders the pending ghost card in list view too', async () => {
    mockFetchTrackedPRs.mockResolvedValue({ prs: [pendingPR] })
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'Addons' })).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /list view/i }))

    const row = await screen.findByTestId('pending-addon-row')
    expect(within(row).getByText('loki')).toBeInTheDocument()
    expect(within(row).getByText('Pending')).toBeInTheDocument()
  })

  it('dedupes: an addon that is both pending AND already in the real catalog shows only the real card', async () => {
    // ingress-nginx is already in the getAddonCatalog fixture (see module
    // mock above) — a pending PR naming the SAME addon must not produce a
    // second, ghost duplicate.
    mockFetchTrackedPRs.mockResolvedValue({
      prs: [{ ...pendingPR, addon: 'ingress-nginx', pr_title: 'sharko: add ingress-nginx to catalog' }],
    })
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    })
    // Only one card names ingress-nginx (the real one) — no ghost twin.
    expect(screen.getAllByText('ingress-nginx')).toHaveLength(1)
    expect(screen.queryByTestId('pending-addon-card')).not.toBeInTheDocument()
  })

  // No silent swallows (walk finding): a failed pending-PR check used to
  // disappear into `.catch(() => setPendingAddonPRs([]))` with no visible
  // trace. It must now show a muted note and the grid must still render
  // normally (never crash, never block the real addons).
  it('a failed pending-PR check shows a muted note and still renders the real grid', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {})
    mockFetchTrackedPRs.mockRejectedValue(new Error('401 unauthorized'))
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    })
    expect(
      await screen.findByText(/couldn.t check for pending addons/i),
    ).toBeInTheDocument()
    expect(warnSpy).toHaveBeenCalled()
    expect(screen.queryByTestId('pending-addon-card')).not.toBeInTheDocument()
  })
})

// perf S2 — a same-session revisit paints from the last successful load
// instantly (no spinner), then quietly refreshes in the background.
describe('AddonCatalog stale-while-refresh (perf S2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders cached addons immediately on mount, then replaces them once the background refetch resolves', async () => {
    setCached(ADDON_CATALOG_CACHE_KEY, {
      addons: [
        {
          addon_name: 'stale-addon',
          chart: 'stale-addon',
          repo_url: 'https://example.com/charts',
          namespace: 'stale-addon',
          version: '1.0.0',
          total_clusters: 1,
          enabled_clusters: 0,
          healthy_applications: 0,
          degraded_applications: 0,
          missing_applications: 0,
          deployed_cluster_count: 0,
          total_target_cluster_count: 0,
          applications: [],
        },
      ],
    })

    // The background refetch resolves with the fixture's fresh 4-addon
    // response (module mock default, set up above).
    renderCatalog()

    // Instant paint from cache — no loading spinner, the stale addon's name
    // visible without waiting on any fetch.
    expect(screen.queryByText('Loading addon catalog...')).not.toBeInTheDocument()
    expect(screen.getByText('stale-addon')).toBeInTheDocument()

    // Background refresh lands and replaces the stale list with the fresh
    // fixture data.
    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    })
    expect(screen.queryByText('stale-addon')).not.toBeInTheDocument()
  })
})
