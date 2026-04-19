import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { MarketplaceTab } from '@/components/MarketplaceTab'
import type { CatalogEntry } from '@/services/models'

// Three fixtures spanning every filter axis we care about.
const fixtures: CatalogEntry[] = [
  {
    name: 'cert-manager',
    description: 'TLS lifecycle manager.',
    chart: 'cert-manager',
    repo: 'https://charts.jetstack.io',
    default_namespace: 'cert-manager',
    default_sync_wave: 1,
    maintainers: ['jetstack'],
    license: 'Apache-2.0',
    category: 'security',
    curated_by: ['cncf-graduated', 'aws-eks-blueprints'],
    security_score: 8.2,
    security_tier: 'Strong',
    security_score_updated: '2026-04-15',
    github_stars: 12500,
  },
  {
    name: 'grafana',
    description: 'Visualisation dashboards.',
    chart: 'grafana',
    repo: 'https://grafana.github.io/helm-charts',
    default_namespace: 'monitoring',
    default_sync_wave: 2,
    maintainers: ['grafana'],
    license: 'AGPL-3.0',
    category: 'observability',
    curated_by: ['cncf-incubating'],
    security_score: 'unknown',
    security_tier: '',
  },
  {
    name: 'argo-cd',
    description: 'GitOps continuous delivery.',
    chart: 'argo-cd',
    repo: 'https://argoproj.github.io/argo-helm',
    default_namespace: 'argocd',
    default_sync_wave: 0,
    maintainers: ['argoproj'],
    license: 'Apache-2.0',
    category: 'gitops',
    curated_by: ['cncf-graduated'],
    security_score: 6.0,
    security_tier: 'Moderate',
    github_stars: 17000,
  },
]

const listMock = vi.fn().mockResolvedValue({ addons: fixtures, total: fixtures.length })
const listVersionsMock = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    listCuratedCatalog: () => listMock(),
    listCuratedCatalogVersions: (...args: unknown[]) => listVersionsMock(...args),
  },
}))

function renderTab(initialEntries: string[] = ['/']) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <MarketplaceTab />
    </MemoryRouter>,
  )
}

describe('MarketplaceTab', () => {
  beforeEach(() => {
    listMock.mockClear()
    listVersionsMock.mockReset()
  })

  it('renders all curated entries on first load', async () => {
    renderTab()
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: /Configure grafana/i }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: /Configure argo-cd/i }),
      ).toBeInTheDocument()
    })
    // Filter sidebar should expose the OpenSSF tier radio group.
    expect(
      screen.getByRole('group', { name: /OpenSSF Scorecard/i }),
    ).toBeInTheDocument()
  })

  it('filters by category multi-select', async () => {
    renderTab()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument(),
    )

    fireEvent.click(screen.getByLabelText(/^security$/i))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: /Configure grafana/i }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: /Configure argo-cd/i }),
      ).not.toBeInTheDocument()
    })
  })

  it('filters by curated_by with AND semantics', async () => {
    renderTab()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument(),
    )

    // Scope to the filter sidebar so we don't collide with curated_by chips
    // rendered on each card's aria-label.
    const filtersAside = screen.getByRole('complementary', {
      name: /Marketplace filters/i,
    })
    fireEvent.click(within(filtersAside).getByLabelText(/cncf-graduated/i))
    fireEvent.click(within(filtersAside).getByLabelText(/aws-eks-blueprints/i))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument()
      // argo-cd carries cncf-graduated but NOT aws-eks-blueprints, so AND drops it.
      expect(
        screen.queryByRole('button', { name: /Configure argo-cd/i }),
      ).not.toBeInTheDocument()
    })
  })

  it('filters by OpenSSF tier (Strong)', async () => {
    renderTab()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument(),
    )

    fireEvent.click(screen.getByLabelText(/Strong \(/i))
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: /Configure argo-cd/i }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: /Configure grafana/i }),
      ).not.toBeInTheDocument()
    })
  })

  it('reads filters from the URL query string', async () => {
    renderTab(['/?mp_cat=security'])
    await waitFor(() => {
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: /Configure grafana/i }),
      ).not.toBeInTheDocument()
    })
  })

  it('opens the Configure modal when a card is activated', async () => {
    listVersionsMock.mockResolvedValue({
      addon: 'cert-manager',
      chart: 'cert-manager',
      repo: 'https://charts.jetstack.io',
      versions: [
        { version: '1.20.0', prerelease: false },
        { version: '1.19.0', prerelease: false },
      ],
      latest_stable: '1.20.0',
      cached_at: '2026-04-17T00:00:00Z',
    })
    renderTab()
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: /Configure cert-manager/i }),
      ).toBeInTheDocument(),
    )

    fireEvent.click(screen.getByRole('button', { name: /Configure cert-manager/i }))
    await waitFor(() =>
      expect(screen.getByRole('dialog')).toBeInTheDocument(),
    )
    // Name field pre-filled.
    expect(screen.getByLabelText(/Display name/i)).toHaveValue('cert-manager')
    expect(screen.getByLabelText(/Namespace/i)).toHaveValue('cert-manager')
    // Versions endpoint was called.
    await waitFor(() => expect(listVersionsMock).toHaveBeenCalledWith('cert-manager'))
  })
})
