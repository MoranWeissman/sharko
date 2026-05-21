import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AddonCatalog } from '@/views/AddonCatalog'

// Catalog fixture covers all 4 V126-3.1 (DESIGN-02) tile-badge states:
//
// - ingress-nginx → N=2, M=2 → "Running on 2 clusters"
// - cert-manager  → N=3, M=5 → "Running on 3/5 clusters"
// - addon-target-only → N=0, M=4 → "Not deployed yet"
// - addon-nowhere     → N=0, M=0 → "Not deployed anywhere"
vi.mock('@/services/api', () => ({
  api: {
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
