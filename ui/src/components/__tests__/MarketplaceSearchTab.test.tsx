import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { MarketplaceSearchTab } from '@/components/MarketplaceSearchTab'

// Mocks for api + showToast — Search tab calls api.searchCatalog,
// api.reprobeArtifactHub, api.getRemoteCatalogPackage, api.getMe (via modal),
// api.getAddonCatalog (via modal pre-flight).

const searchMock = vi.fn()
const reprobeMock = vi.fn()
const getRemoteMock = vi.fn()
const getCatalogMock = vi.fn().mockResolvedValue({ addons: [] })
const getMeMock = vi.fn().mockResolvedValue({ has_github_token: true })
const listVersionsMock = vi.fn().mockResolvedValue({
  addon: '',
  chart: '',
  repo: '',
  versions: [],
  cached_at: '',
})

vi.mock('@/services/api', () => ({
  api: {
    searchCatalog: (...args: unknown[]) => searchMock(...args),
    reprobeArtifactHub: () => reprobeMock(),
    getRemoteCatalogPackage: (...args: unknown[]) => getRemoteMock(...args),
    listCuratedCatalogVersions: (...args: unknown[]) => listVersionsMock(...args),
    getAddonCatalog: () => getCatalogMock(),
    getMe: () => getMeMock(),
  },
  addAddon: vi.fn(),
  isAddonAlreadyExistsError: () => false,
}))

vi.mock('@/components/ToastNotification', () => ({
  showToast: vi.fn(),
}))

function renderTab() {
  return render(
    <MemoryRouter>
      <MarketplaceSearchTab />
    </MemoryRouter>,
  )
}

describe('MarketplaceSearchTab', () => {
  beforeEach(() => {
    searchMock.mockReset()
    reprobeMock.mockReset()
    getRemoteMock.mockReset()
  })

  it('renders the empty state on first load', () => {
    renderTab()
    expect(
      screen.getByText(/Search any chart on ArtifactHub/i),
    ).toBeInTheDocument()
    // Auto-focused search input
    expect(screen.getByLabelText(/Search addons by name/i)).toHaveFocus()
  })

  it('debounces and calls searchCatalog with the typed query', async () => {
    searchMock.mockResolvedValue({
      query: 'prom',
      curated: [],
      artifacthub: [],
    })
    renderTab()
    fireEvent.change(screen.getByLabelText(/Search addons by name/i), {
      target: { value: 'prom' },
    })
    await waitFor(() => expect(searchMock).toHaveBeenCalledWith('prom', 20), {
      timeout: 1000,
    })
  })

  it('renders curated and ArtifactHub sections with results', async () => {
    searchMock.mockResolvedValue({
      query: 'prometheus',
      curated: [
        {
          name: 'prometheus',
          description: 'curated prometheus',
          chart: 'prometheus',
          repo: 'https://prometheus-community.github.io/helm-charts',
          default_namespace: 'monitoring',
          default_sync_wave: 0,
          maintainers: ['team'],
          license: 'Apache-2.0',
          category: 'observability',
          curated_by: ['cncf-graduated'],
        },
      ],
      artifacthub: [
        {
          package_id: 'pkg-1',
          name: 'prometheus-community-stack',
          description: 'kube-prometheus stack',
          stars: 5000,
          repository: {
            kind: 0,
            name: 'prometheus-community',
            display_name: 'Prometheus Community',
            verified_publisher: true,
          },
        },
      ],
    })
    renderTab()
    fireEvent.change(screen.getByLabelText(/Search addons by name/i), {
      target: { value: 'prometheus' },
    })
    await waitFor(() => {
      expect(screen.getByText(/Curated by Sharko/i)).toBeInTheDocument()
      expect(screen.getByText(/From ArtifactHub/i)).toBeInTheDocument()
      expect(screen.getByText(/Verified/i)).toBeInTheDocument()
    })
  })

  it('shows the unreachable banner when artifacthub_error is set', async () => {
    searchMock.mockResolvedValue({
      query: 'prom',
      curated: [],
      artifacthub: [],
      artifacthub_error: 'rate_limited',
    })
    renderTab()
    fireEvent.change(screen.getByLabelText(/Search addons by name/i), {
      target: { value: 'prom' },
    })
    await waitFor(() => {
      expect(
        screen.getByText(/ArtifactHub unreachable/i),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: /Retry connectivity/i }),
      ).toBeInTheDocument()
    })
  })

  it('clicking Retry calls reprobeArtifactHub', async () => {
    searchMock.mockResolvedValue({
      query: 'prom',
      curated: [],
      artifacthub: [],
      artifacthub_error: 'timeout',
    })
    reprobeMock.mockResolvedValue({
      reachable: true,
      probed_at: '2026-04-17T00:00:00Z',
    })
    renderTab()
    fireEvent.change(screen.getByLabelText(/Search addons by name/i), {
      target: { value: 'prom' },
    })
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /Retry connectivity/i })).toBeInTheDocument(),
    )
    fireEvent.click(screen.getByRole('button', { name: /Retry connectivity/i }))
    await waitFor(() => expect(reprobeMock).toHaveBeenCalled())
  })
})
