import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'
import { getAddonPRs, api } from '@/services/api'
import { AuthContext } from '@/hooks/useAuth'

vi.mock('@/services/api', () => ({
  getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
  upgradeAddon: vi.fn().mockResolvedValue({ pr_url: '' }),
  api: {
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getAddonValues: vi.fn().mockRejectedValue(new Error('not found')),
    // v1.20 — values editor / attribution lookups
    getMe: vi.fn().mockResolvedValue({ username: 'tester', role: 'admin', has_github_token: true }),
    getAddonValuesSchema: vi.fn().mockResolvedValue({ addon_name: 'ingress-nginx', current_values: '', schema: null }),
    // V121-7.4: AI config probe used by AddonDetail to render the
    // "AI not configured" banner / annotate-now button conditionally.
    getAIConfig: vi.fn().mockResolvedValue({ current_provider: 'none', available_providers: [], annotate_on_seed: false }),
    // V2-cleanup-72.1 — the Upgrade tab's child components fetch these on
    // mount; stub them so tests that visit/click the Upgrade tab don't blow
    // up on an undefined mock method.
    getUpgradeRecommendations: vi.fn().mockResolvedValue({ current_version: '4.8.0' }),
    getUpgradeVersions: vi.fn().mockResolvedValue({ versions: [] }),
    getAddonDetail: vi.fn().mockResolvedValue({
      addon: {
        addon_name: 'ingress-nginx',
        chart: 'ingress-nginx',
        repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx',
        version: '4.8.0',
        total_clusters: 10,
        enabled_clusters: 8,
        healthy_applications: 6,
        degraded_applications: 1,
        missing_applications: 1,
        applications: [
          {
            cluster_name: 'prod-cluster-1',
            cluster_environment: 'prod',
            enabled: true,
            configured_version: '4.8.0',
            deployed_version: '4.8.0',
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
            application_name: 'ingress-nginx-prod-1',
          },
          {
            cluster_name: 'dev-cluster-1',
            cluster_environment: 'dev',
            enabled: true,
            configured_version: '4.7.0',
            deployed_version: '4.7.0',
            namespace: 'ingress-nginx',
            health_status: 'Degraded',
            status: 'degraded',
          },
          {
            cluster_name: 'staging-cluster-1',
            cluster_environment: 'staging',
            enabled: true,
            configured_version: '4.8.0',
            deployed_version: '4.8.0',
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
          },
          {
            cluster_name: 'disabled-cluster',
            enabled: false,
            status: 'disabled',
          },
        ],
      },
    }),
  },
}))

function renderDetail() {
  return render(
    <MemoryRouter initialEntries={['/addons/ingress-nginx']}>
      <Routes>
        <Route path="/addons/:name" element={<AddonDetail />} />
      </Routes>
    </MemoryRouter>,
  )
}

function renderDetailClustersTab() {
  return render(
    <MemoryRouter initialEntries={['/addons/ingress-nginx?section=clusters']}>
      <Routes>
        <Route path="/addons/:name" element={<AddonDetail />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('AddonDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders loading state initially', () => {
    renderDetail()
    expect(screen.getByText('Loading addon details...')).toBeInTheDocument()
  })

  it('renders addon details after loading', async () => {
    renderDetail()

    await waitFor(() => {
      // 'ingress-nginx' now appears in both the page header and the info card
      expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1)
    })

    // Stat cards — plain-English labels (V2-cleanup-71.1)
    expect(screen.getByText('Clusters enabled')).toBeInTheDocument()
    expect(screen.getByText('8 / 10')).toBeInTheDocument()
    expect(screen.getByText('Running fine')).toBeInTheDocument()
    expect(screen.getByText('6 (75%)')).toBeInTheDocument()
    // The problem stat is now named "Having problems" (subtitle carries the
    // "deployed but unhealthy" detail).
    expect(screen.getByText('Having problems')).toBeInTheDocument()
    // V2-cleanup-71.1: the problem stat is now named "Missing" (subtitle
    // carries the ArgoCD detail), not "Missing from ArgoCD" and NOT
    // "Not deployed yet" — that term is reserved for a different state.
    expect(screen.getByText('Missing')).toBeInTheDocument()
    expect(screen.getByText('Turned off')).toBeInTheDocument()
  })

  it('renders cluster applications table', async () => {
    renderDetailClustersTab()

    await waitFor(() => {
      expect(screen.getByText('Cluster Applications')).toBeInTheDocument()
    })

    // Cluster names in table
    expect(screen.getByText('prod-cluster-1')).toBeInTheDocument()
    expect(screen.getByText('dev-cluster-1')).toBeInTheDocument()
    expect(screen.getByText('staging-cluster-1')).toBeInTheDocument()
  })

  it('renders environment versions', async () => {
    // Environment versions are shown in the overview section (default)
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Environment Versions')).toBeInTheDocument()
    })

    // env names appear in env versions card
    expect(screen.getAllByText('prod').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('dev').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('staging').length).toBeGreaterThanOrEqual(1)
  })

  it('renders disabled clusters section', async () => {
    renderDetailClustersTab()

    await waitFor(() => {
      expect(screen.getByText('Disabled on 1 Clusters')).toBeInTheDocument()
    })

    expect(screen.getByText('disabled-cluster')).toBeInTheDocument()
  })

  it('renders filter controls', async () => {
    renderDetailClustersTab()

    await waitFor(() => {
      expect(screen.getByText('Filter Applications')).toBeInTheDocument()
    })

    expect(
      screen.getByPlaceholderText('Search clusters, environments, or apps...'),
    ).toBeInTheDocument()
    expect(screen.getByText('All Environments')).toBeInTheDocument()
    expect(screen.getByText('All Status')).toBeInTheDocument()
    expect(screen.getByText('All Health')).toBeInTheDocument()
  })

  it('renders overall health progress bar', async () => {
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Overall Health')).toBeInTheDocument()
    })

    expect(
      screen.getByText('6 of 8 applications are healthy (75%)'),
    ).toBeInTheDocument()
  })
})

// V2-cleanup-15.2 — the pending-PR banner must label the PR by its KIND.
// An "Add addon" PR was previously mislabelled "Upgrade in progress" because
// the banner copy was hard-coded to the upgrade wording. These tests drive
// the banner with each PR kind and assert the headline.
describe('AddonDetail — pending-PR banner copy (V2-cleanup-15.2)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('labels an open "Add addon" PR as "Add addon — PR open" (not an Upgrade)', async () => {
    vi.mocked(getAddonPRs).mockResolvedValue({
      prs: [
        {
          pr_id: 42,
          pr_url: 'https://gh/pr/42',
          pr_title: 'sharko: add addon ingress-nginx',
          last_status: 'open',
        },
      ],
    } as never)
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Add addon — PR open')).toBeInTheDocument()
    })
    // The old mislabel must be gone for an Add PR.
    expect(screen.queryByText('Upgrade in progress')).not.toBeInTheDocument()
  })

  it('labels a merged "Add addon" PR as "Addon added"', async () => {
    vi.mocked(getAddonPRs).mockResolvedValue({
      prs: [
        {
          pr_id: 43,
          pr_url: 'https://gh/pr/43',
          pr_title: 'sharko: add addon ingress-nginx',
          last_status: 'merged',
        },
      ],
    } as never)
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Addon added')).toBeInTheDocument()
    })
    expect(screen.queryByText('Upgrade merged')).not.toBeInTheDocument()
  })

  it('still labels an upgrade PR as "Upgrade in progress"', async () => {
    vi.mocked(getAddonPRs).mockResolvedValue({
      prs: [
        {
          pr_id: 44,
          pr_url: 'https://gh/pr/44',
          pr_title: 'sharko: upgrade ingress-nginx to 4.9.0',
          last_status: 'open',
        },
      ],
    } as never)
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Upgrade in progress')).toBeInTheDocument()
    })
    expect(screen.queryByText('Add addon — PR open')).not.toBeInTheDocument()
  })
})

// V2-cleanup-72.1 — rename "Deployment Options" → "ApplicationSet", drop the
// duplicate "Raw default values" peek and the duplicate header Upgrade
// button, and tuck the raw ArgoCD knobs under a collapsed Advanced fold.
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

function renderDetailCatalogTab() {
  return render(
    <MemoryRouter initialEntries={['/addons/ingress-nginx?section=catalog']}>
      <Routes>
        <Route path="/addons/:name" element={<AddonDetail />} />
      </Routes>
    </MemoryRouter>,
  )
}

function renderDetailAsAdmin(initialEntry = '/addons/ingress-nginx') {
  return render(
    <AuthContext.Provider value={adminCtx}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

describe('AddonDetail — tab renamed "ApplicationSet" (V2-cleanup-72.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders the tab as "ApplicationSet", not "Deployment Options"', async () => {
    renderDetail()

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'ApplicationSet' })).toBeInTheDocument()
    })
    expect(screen.queryByText('Deployment Options')).not.toBeInTheDocument()
  })

  it('shows the "ApplicationSet" heading with a plain-English subtitle on the tab', async () => {
    renderDetailCatalogTab()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ApplicationSet' })).toBeInTheDocument()
    })
    expect(
      screen.getByText(/How ArgoCD deploys this addon across your clusters/i),
    ).toBeInTheDocument()
  })

  it('does not render the removed "Raw default values" peek (Values tab owns Helm values)', async () => {
    renderDetailCatalogTab()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ApplicationSet' })).toBeInTheDocument()
    })
    expect(screen.queryByText('Raw default values (read-only)')).not.toBeInTheDocument()
  })

  it('rewords the Self-Heal description in plain English', async () => {
    renderDetailCatalogTab()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ApplicationSet' })).toBeInTheDocument()
    })
    expect(
      screen.getByText(/ArgoCD undoes it and restores what.s in Git/i),
    ).toBeInTheDocument()
  })
})

describe('AddonDetail — Advanced fold tucks the raw ArgoCD knobs away (V2-cleanup-72.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('collapses Sync Options / Ignore Differences / Additional Sources under "Advanced — passed straight to ArgoCD" by default', async () => {
    renderDetailCatalogTab()

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ApplicationSet' })).toBeInTheDocument()
    })

    const summary = screen.getByText('Advanced — passed straight to ArgoCD')
    const details = summary.closest('details')
    expect(details).not.toBeNull()
    // Collapsed by default (read-only viewer, not editing).
    expect(details).not.toHaveAttribute('open')

    const scoped = within(details as HTMLElement)
    expect(scoped.getByText('Sync Options')).toBeInTheDocument()
    expect(scoped.getByText('Ignore Differences')).toBeInTheDocument()
    expect(scoped.getByText('Additional Sources')).toBeInTheDocument()
    // Self-Heal stays outside the fold, always visible.
    expect(scoped.queryByText('Self-Heal')).not.toBeInTheDocument()
    expect(screen.getByText('Self-Heal')).toBeInTheDocument()

    // Each advanced field links out to the matching ArgoCD docs page.
    const learnMoreLinks = scoped.getAllByRole('link', { name: 'Learn more' })
    expect(learnMoreLinks).toHaveLength(3)
    const hrefs = learnMoreLinks.map((l) => l.getAttribute('href'))
    expect(hrefs).toContain('https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/')
    expect(hrefs).toContain('https://argo-cd.readthedocs.io/en/stable/user-guide/diffing/')
    expect(hrefs).toContain('https://argo-cd.readthedocs.io/en/stable/user-guide/multiple_sources/')
    learnMoreLinks.forEach((l) => {
      expect(l).toHaveAttribute('target', '_blank')
      expect(l).toHaveAttribute('rel', 'noopener noreferrer')
    })
  })

  it('opens the Advanced fold automatically while editing', async () => {
    renderDetailAsAdmin('/addons/ingress-nginx?section=catalog')

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: 'ApplicationSet' })).toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /^Edit$/i }))

    const summary = screen.getByText('Advanced — passed straight to ArgoCD')
    const details = summary.closest('details')
    expect(details).toHaveAttribute('open')
  })
})

describe('AddonDetail — header no longer duplicates the Upgrade tab (V2-cleanup-72.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('has exactly one "Upgrade" control — the tab, not a header button', async () => {
    renderDetailAsAdmin()

    await waitFor(() => {
      expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThanOrEqual(1)
    })

    const upgradeControls = screen.getAllByRole('button', { name: /^Upgrade$/i })
    expect(upgradeControls).toHaveLength(1)
    // Refresh and Remove are still there.
    expect(screen.getByTitle('Refresh')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^Remove$/i })).toBeInTheDocument()
  })

  it('the Upgrade tab still opens the Upgrade section (flow untouched by the header-button removal)', async () => {
    renderDetailAsAdmin('/addons/ingress-nginx?section=upgrade')

    await waitFor(() => {
      expect(screen.getByText('Current Catalog Version')).toBeInTheDocument()
    })
  })
})

// V2-cleanup-71.1 — the problem/off stat cards (Having problems / Missing /
// Turned off) hide themselves when their count is zero, so an empty addon
// shows a calm two-card view instead of a wall of zeros.
describe('AddonDetail — hide problem/off stat cards when zero (V2-cleanup-71.1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getAddonPRs).mockResolvedValue({ prs: [] } as never)
  })

  it('hides "Having problems", "Missing", and "Turned off" when all their counts are zero', async () => {
    vi.mocked(api.getAddonDetail).mockResolvedValueOnce({
      addon: {
        addon_name: 'ingress-nginx',
        chart: 'ingress-nginx',
        repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx',
        version: '4.8.0',
        total_clusters: 2,
        enabled_clusters: 2,
        healthy_applications: 2,
        degraded_applications: 0,
        missing_applications: 0,
        applications: [
          {
            cluster_name: 'prod-cluster-1',
            cluster_environment: 'prod',
            enabled: true,
            configured_version: '4.8.0',
            deployed_version: '4.8.0',
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
            application_name: 'ingress-nginx-prod-1',
          },
          {
            cluster_name: 'staging-cluster-1',
            cluster_environment: 'staging',
            enabled: true,
            configured_version: '4.8.0',
            deployed_version: '4.8.0',
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
          },
        ],
      },
    } as never)

    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Clusters enabled')).toBeInTheDocument()
    })

    // Always-shown cards are present.
    expect(screen.getByText('Clusters enabled')).toBeInTheDocument()
    expect(screen.getByText('Running fine')).toBeInTheDocument()

    // Zero-count cards are absent, not just zeroed-out.
    expect(screen.queryByText('Having problems')).not.toBeInTheDocument()
    expect(screen.queryByText('Missing')).not.toBeInTheDocument()
    expect(screen.queryByText('Turned off')).not.toBeInTheDocument()
  })

  it('shows "Having problems" when degraded_applications is greater than zero', async () => {
    vi.mocked(api.getAddonDetail).mockResolvedValueOnce({
      addon: {
        addon_name: 'ingress-nginx',
        chart: 'ingress-nginx',
        repo_url: 'https://kubernetes.github.io/ingress-nginx',
        namespace: 'ingress-nginx',
        version: '4.8.0',
        total_clusters: 2,
        enabled_clusters: 2,
        healthy_applications: 1,
        degraded_applications: 1,
        missing_applications: 0,
        applications: [
          {
            cluster_name: 'prod-cluster-1',
            cluster_environment: 'prod',
            enabled: true,
            configured_version: '4.8.0',
            deployed_version: '4.8.0',
            namespace: 'ingress-nginx',
            health_status: 'Degraded',
            status: 'degraded',
          },
          {
            cluster_name: 'staging-cluster-1',
            cluster_environment: 'staging',
            enabled: true,
            configured_version: '4.8.0',
            deployed_version: '4.8.0',
            namespace: 'ingress-nginx',
            health_status: 'Healthy',
            status: 'healthy',
          },
        ],
      },
    } as never)

    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Having problems')).toBeInTheDocument()
    })

    expect(screen.queryByText('Missing')).not.toBeInTheDocument()
    expect(screen.queryByText('Turned off')).not.toBeInTheDocument()
  })
})
