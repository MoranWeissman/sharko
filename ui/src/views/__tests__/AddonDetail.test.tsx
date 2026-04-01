import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { AddonDetail } from '@/views/AddonDetail'

vi.mock('@/services/api', () => ({
  api: {
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getAddonValues: vi.fn().mockRejectedValue(new Error('not found')),
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
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument()
    })

    // Stat cards
    expect(screen.getByText('Active Apps')).toBeInTheDocument()
    expect(screen.getByText('8 / 10')).toBeInTheDocument()
    // "Healthy" appears in stat card, status badges, and filter — use getAllByText
    expect(screen.getAllByText('Healthy').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('6 (75%)')).toBeInTheDocument()
    // "Degraded" also appears in stat card + status badge + filter
    expect(screen.getAllByText('Degraded').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Not Deployed')).toBeInTheDocument()
    expect(screen.getByText('Disabled in Git')).toBeInTheDocument()
  })

  it('renders cluster applications table', async () => {
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Cluster Applications')).toBeInTheDocument()
    })

    // Cluster names in table
    expect(screen.getByText('prod-cluster-1')).toBeInTheDocument()
    expect(screen.getByText('dev-cluster-1')).toBeInTheDocument()
    expect(screen.getByText('staging-cluster-1')).toBeInTheDocument()
  })

  it('renders environment versions', async () => {
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Environment Versions')).toBeInTheDocument()
    })

    // env names appear in both env versions card and filter dropdown, so use getAllByText
    expect(screen.getAllByText('prod').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('dev').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('staging').length).toBeGreaterThanOrEqual(1)
  })

  it('renders disabled clusters section', async () => {
    renderDetail()

    await waitFor(() => {
      expect(screen.getByText('Disabled on 1 Clusters')).toBeInTheDocument()
    })

    expect(screen.getByText('disabled-cluster')).toBeInTheDocument()
  })

  it('renders filter controls', async () => {
    renderDetail()

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
