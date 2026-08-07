// ManagedSecrets.tiles — the List | Tiles toggle and the tiles view itself
// (design-secret-sync-visual-pass, section 3 — "where is view types i
// asked"). Pins:
//
//  - the tiles view renders one card per addon plus one "Cluster
//    connections" card, built from the same buildRowGroups(filtered,
//    'addon') buckets the list view's Group by addon already uses;
//  - each card's status-mix bar renders one segment per state PRESENT,
//    worst-first (CHIP_ORDER order), never alphabetically or by count;
//  - clicking an addon card switches back to list, narrowed to that addon;
//    clicking the connections card switches back to list with the
//    dismissible "Cluster connections" pill active;
//  - the view choice persists across a reload — the URL wins over
//    localStorage when both are present, and localStorage is only the
//    fallback when the URL has no `view` param at all.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useSearchParams } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

const VIEW_STORAGE_KEY = 'sharko-secret-sync-view'

const adminAuth = {
  token: 'test-token',
  username: 'admin',
  role: 'admin',
  login: vi.fn(),
  logout: vi.fn(),
  isAuthenticated: true,
  isAdmin: true,
  loading: false,
  error: null,
}

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

const mockGetManagedSecrets = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => ({
  api: { getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args) },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  triggerSecretsReconcile: vi.fn(),
  checkAllAddonValuesSecrets: vi.fn(),
  reconcileCluster: vi.fn(),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: vi.fn(),
  syncAddonValuesSecret: vi.fn(),
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

function LocationProbe() {
  const [searchParams] = useSearchParams()
  return <div data-testid="location-probe">{searchParams.toString()}</div>
}

function renderPage(initialEntries: string[] = ['/secret-sync']) {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={initialEntries}>
        <LocationProbe />
        <ManagedSecrets />
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

// Demo-shaped: two clusters' connection secrets (one in_sync, one
// out_of_sync — the worst-case for the connections card's bar) plus three
// addons with a deliberate mix of states, so every card has something real
// to add up and the bar has more than one segment to order.
const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    { cluster: 'prod-eu', secret_namespace: 'argocd', secret_name: 'prod-eu', state: 'in_sync', source: 'git', self_heals: true },
    { cluster: 'staging-us', secret_namespace: 'argocd', secret_name: 'staging-us', state: 'out_of_sync', source: 'git', self_heals: true },
  ],
  addon_values_secrets: [
    {
      cluster: 'prod-eu',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'in_sync',
      source: 'AWS Secrets Manager',
      self_heals: true,
    },
    {
      cluster: 'staging-us',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'out_of_sync',
      source: 'AWS Secrets Manager',
      self_heals: true,
    },
    {
      cluster: 'prod-eu',
      addon: 'vault',
      secret_name: 'vault-unseal-keys',
      secret_namespace: 'vault',
      state: 'unknown',
      source: 'a Kubernetes Secret',
      self_heals: true,
    },
    {
      cluster: 'prod-eu',
      addon: 'grafana',
      secret_name: 'grafana-secret',
      secret_namespace: 'monitoring',
      state: 'in_sync',
      source: 'AWS Secrets Manager',
      self_heals: true,
    },
  ],
  engines: {
    cluster_connection: { wired: true, enabled: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
    addon_values: { wired: true, enabled: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
  },
  addon_values_secret_source: 'AWS Secrets Manager',
}

beforeEach(() => {
  vi.clearAllMocks()
  window.localStorage.clear()
  mockGetManagedSecrets.mockResolvedValue(response)
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
})

describe('the tiles view renders one card per addon plus the connections card', () => {
  it('shows a card for every addon in the fixture and one for cluster connections, none for individual rows', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    await user.click(screen.getByTestId('view-tiles'))

    const grid = await screen.findByTestId('secret-tiles')
    expect(within(grid).getByTestId('secret-tile-addon-datadog')).toBeInTheDocument()
    expect(within(grid).getByTestId('secret-tile-addon-vault')).toBeInTheDocument()
    expect(within(grid).getByTestId('secret-tile-addon-grafana')).toBeInTheDocument()
    expect(within(grid).getByTestId('secret-tile-__connections__')).toBeInTheDocument()
    // Exactly 4 cards — 3 addons + 1 connections bucket, never one per row.
    expect(within(grid).getAllByRole('button')).toHaveLength(4)

    // The connections card carries its own honest sublabel and the summary
    // line groupSummary() already produces for the list view's group
    // headers — no new wording invented for the card.
    const connectionsCard = within(grid).getByTestId('secret-tile-__connections__')
    expect(connectionsCard).toHaveTextContent('Cluster connections')
    expect(connectionsCard).toHaveTextContent('not an addon — one secret per cluster')
    expect(connectionsCard).toHaveTextContent('2 secrets')
    expect(connectionsCard).toHaveTextContent('1 out of sync')
    expect(connectionsCard).toHaveTextContent('1 synced')

    // The list view's own rows are gone while tiles is active — the table
    // itself doesn't render.
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()

    // Group by is hidden in tiles view — the cards ARE the addon grouping.
    expect(screen.queryByTestId('group-by-addon')).not.toBeInTheDocument()
  })

  it('the footer states the addon-unit count in tiles view, same wording the list view uses when grouped', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    await screen.findByTestId('secret-tiles')
    // 3 addons + the connections bucket = 4 "addons" units, 6 secrets total.
    expect(screen.getByTestId('secrets-summary')).toHaveTextContent('4 addons, 6 secrets')
  })
})

describe('the status-mix bar renders segments worst-first', () => {
  it('orders the datadog card\'s segments out_of_sync before in_sync, never alphabetically or by count', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    const datadogCard = await screen.findByTestId('secret-tile-addon-datadog')
    const bar = within(datadogCard).getByTestId('tile-bar-addon-datadog')
    const segments = within(bar).getAllByTestId(/^tile-bar-segment-/)
    const order = segments.map((s) => s.getAttribute('data-testid'))
    // datadog has one out_of_sync row and one in_sync row — CHIP_ORDER puts
    // out_of_sync well ahead of in_sync, so the worse segment renders
    // first regardless of which row arrived first in the fixture.
    expect(order).toEqual(['tile-bar-segment-addon-datadog-out_of_sync', 'tile-bar-segment-addon-datadog-in_sync'])
  })

  it('a card with only one state present renders exactly one full-width segment', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    const grafanaCard = await screen.findByTestId('secret-tile-addon-grafana')
    const bar = within(grafanaCard).getByTestId('tile-bar-addon-grafana')
    expect(within(bar).getAllByTestId(/^tile-bar-segment-/)).toHaveLength(1)
    expect(within(bar).getByTestId('tile-bar-segment-addon-grafana-in_sync')).toBeInTheDocument()
  })
})

describe('card click-through', () => {
  it('an addon card switches back to list, narrowed to that addon', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    await user.click(await screen.findByTestId('secret-tile-addon-datadog'))

    // Back to the list, narrowed — the addon filter select shows it, and
    // only datadog's two rows are on screen.
    expect(screen.getByTestId('view-list')).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('addon=datadog'))
    expect(screen.getByTestId('location-probe')).not.toHaveTextContent('view=')
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('addon-filter-select')).toHaveValue('datadog')
  })

  it('the connections card switches back to list with the dismissible "Cluster connections" pill active', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    await user.click(await screen.findByTestId('secret-tile-__connections__'))

    expect(screen.getByTestId('view-list')).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('kind=connection'))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-values-prod-eu-datadog')).not.toBeInTheDocument()

    // The pill is there, and dismissing it clears the filter.
    const pill = screen.getByTestId('kind-filter-pill')
    expect(pill).toHaveTextContent('Cluster connections')
    await user.click(pill)
    await waitFor(() => expect(screen.getByTestId('location-probe')).not.toHaveTextContent('kind='))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(6))
  })
})

describe('the view toggle persists — the URL wins over localStorage', () => {
  it('defaults to list, and clicking Tiles writes both the URL and localStorage', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    expect(screen.getByTestId('view-list')).toHaveAttribute('aria-pressed', 'true')

    await user.click(screen.getByTestId('view-tiles'))
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('view=tiles'))
    expect(window.localStorage.getItem(VIEW_STORAGE_KEY)).toBe('tiles')
  })

  it('reopens in tiles when localStorage says so and the URL has no view param', async () => {
    window.localStorage.setItem(VIEW_STORAGE_KEY, 'tiles')
    renderPage(['/secret-sync'])

    await waitFor(() => expect(screen.getByTestId('secret-tiles')).toBeInTheDocument())
    expect(screen.getByTestId('view-tiles')).toHaveAttribute('aria-pressed', 'true')
  })

  it('the URL wins over localStorage when both are present', async () => {
    window.localStorage.setItem(VIEW_STORAGE_KEY, 'tiles')
    renderPage(['/secret-sync?view=list'])

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    expect(screen.getByTestId('view-list')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByTestId('secret-tiles')).not.toBeInTheDocument()
  })

  it('a shared ?view=tiles link opens straight into tiles even with no localStorage set', async () => {
    renderPage(['/secret-sync?view=tiles'])

    await waitFor(() => expect(screen.getByTestId('secret-tiles')).toBeInTheDocument())
    expect(screen.getByTestId('view-tiles')).toHaveAttribute('aria-pressed', 'true')
  })
})
