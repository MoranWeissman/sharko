import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AddonCatalog } from '@/views/AddonCatalog'

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
          enabled_clusters: 8,
          healthy_applications: 7,
          degraded_applications: 1,
          missing_applications: 0,
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
              health_status: 'Degraded',
              status: 'degraded',
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
          healthy_applications: 5,
          degraded_applications: 0,
          missing_applications: 0,
          applications: [],
        },
      ],
      total_addons: 2,
      total_clusters: 10,
      addons_only_in_git: 1,
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
      expect(screen.getByText('Add-ons Catalog')).toBeInTheDocument()
    })

    // Summary stat cards — now clickable filters
    expect(screen.getAllByText('All Addons').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('2')).toBeInTheDocument()
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
      expect(screen.getByText('Add-ons Catalog')).toBeInTheDocument()
    })

    expect(
      screen.getByPlaceholderText('Search addons by name, chart, or namespace...'),
    ).toBeInTheDocument()
  })

  it('renders filter and sort controls', async () => {
    renderCatalog()

    await waitFor(() => {
      expect(screen.getByText('Add-ons Catalog')).toBeInTheDocument()
    })

    // Filter options — "All Addons" appears in both stat card and dropdown
    expect(screen.getAllByText('All Addons').length).toBeGreaterThanOrEqual(1)

    // Page size
    expect(screen.getByText('15 per page')).toBeInTheDocument()
  })
})
