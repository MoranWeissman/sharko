// ManagedSecrets.tiles — secret tiles v2 (2026-08-08): one BOX per SECRET,
// ArgoCD's own applications-grid mental model ("in argocd each resource or
// application are represented as a box, and when u click on it you get all
// the stats"). Supersedes the addon-summary-card tiles from the earlier
// visual pass. Pins:
//
//  - the tiles view renders one box per SECRET, never one box per addon;
//  - group-by "addon" gives each addon a heading (label + groupSummary()
//    line + the worst-first status-mix bar), same as list view's group
//    parents, with each addon's secrets as boxes underneath;
//  - group-by "None" gives a flat grid of boxes, no headings;
//  - clicking a box opens the SAME detail panel a list row opens — same
//    test-id, same content, no navigation, no filter change;
//  - filters/search/status chips narrow the boxes exactly as they narrow
//    list rows (same underlying rows array feeds both views);
//  - a long secret name truncates with the full name in the hover title;
//  - the view choice persists across a reload — the URL wins over
//    localStorage when both are present, and localStorage is only the
//    fallback when the URL has no `view` param at all.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation, useSearchParams } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { SecretDetailPage } from '@/views/SecretDetailPage'
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

// SSF-9: real react-router-dom, no useNavigate mock — a box click now
// navigates to its own full page (SecretDetailPage) instead of opening a
// drawer in place. See renderPage.

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
  const location = useLocation()
  const [searchParams] = useSearchParams()
  return (
    <div data-testid="location-probe" data-pathname={location.pathname}>
      {searchParams.toString()}
    </div>
  )
}

function renderPage(initialEntries: string[] = ['/secret-sync']) {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={initialEntries}>
        <LocationProbe />
        <Routes>
          <Route path="/secret-sync" element={<ManagedSecrets />} />
          <Route path="/secret-sync/:rowKey" element={<SecretDetailPage />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

// Demo-shaped: two clusters' connection secrets (one in_sync, one
// out_of_sync — the worst-case for the connections group's bar) plus three
// addons with a deliberate mix of states, so every group has something
// real to add up and the bar has more than one segment to order. One
// secret name is deliberately long to exercise the truncate/hover-title
// rule.
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
      secret_name: 'kube-prometheus-stack-grafana-admin-credentials-extra-long',
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
  // A connection-secret box's panel fires the same getClusterComparison
  // diff fetch a connection list row's panel already does — give it a
  // resolved value so opening a connection box in the click-through tests
  // below doesn't hang on an unmocked call.
  mockGetClusterComparison.mockResolvedValue({
    cluster: { name: 'prod-eu', labels: {}, last_reconcile: { time: '2026-08-05T00:00:00Z', outcome: 'succeeded' } },
  })
})

describe('group-by "None" — a flat grid of boxes, one per secret', () => {
  it('renders one box per secret and no section headings', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    await user.click(screen.getByTestId('view-tiles'))

    const grid = await screen.findByTestId('secret-tiles')
    // 2 connection secrets + 4 addon-values secrets = 6 boxes, never one
    // per addon.
    expect(within(grid).getAllByRole('button')).toHaveLength(6)
    expect(within(grid).getByTestId('secret-box-connection-prod-eu')).toBeInTheDocument()
    expect(within(grid).getByTestId('secret-box-values-prod-eu-datadog')).toBeInTheDocument()
    expect(within(grid).getByTestId('secret-box-values-staging-us-datadog')).toBeInTheDocument()

    // No section headings in the flat grid.
    expect(screen.queryByTestId('tile-section-addon-datadog')).not.toBeInTheDocument()
    expect(screen.queryByTestId('tile-section-__connections__')).not.toBeInTheDocument()

    // The list view's own rows are gone while tiles is active.
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()

    // Group by is now shared with list view — visible and defaulted to
    // Flat list, matching the flat grid on screen.
    expect(screen.getByTestId('group-by-none')).toHaveAttribute('aria-pressed', 'true')
  })

  it('the footer states a plain secret count, not a grouped count, while flat', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    await screen.findByTestId('secret-tiles')
    expect(screen.getByTestId('secrets-summary')).toHaveTextContent('6 secrets')
  })
})

describe('group-by "addon" — a heading per addon, boxes underneath', () => {
  it('each addon gets a heading with the summary line and status-mix bar, secrets as boxes underneath', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))
    await user.click(screen.getByTestId('group-by-addon'))

    const grid = await screen.findByTestId('secret-tiles')
    const datadogSection = within(grid).getByTestId('tile-section-addon-datadog')
    expect(datadogSection).toHaveTextContent('datadog')
    expect(datadogSection).toHaveTextContent('2 secrets')
    expect(datadogSection).toHaveTextContent('1 out of sync')
    expect(datadogSection).toHaveTextContent('1 synced')
    expect(within(datadogSection).getByTestId('secret-box-values-prod-eu-datadog')).toBeInTheDocument()
    expect(within(datadogSection).getByTestId('secret-box-values-staging-us-datadog')).toBeInTheDocument()

    const connectionsSection = within(grid).getByTestId('tile-section-__connections__')
    expect(connectionsSection).toHaveTextContent('Cluster connections')
    expect(connectionsSection).toHaveTextContent('2 secrets')
    expect(within(connectionsSection).getByTestId('secret-box-connection-prod-eu')).toBeInTheDocument()
    expect(within(connectionsSection).getByTestId('secret-box-connection-staging-us')).toBeInTheDocument()
  })

  it('the datadog heading\'s status-mix bar orders segments out_of_sync before in_sync, never alphabetically or by count', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))
    await user.click(screen.getByTestId('group-by-addon'))

    const datadogSection = await screen.findByTestId('tile-section-addon-datadog')
    const bar = within(datadogSection).getByTestId('tile-bar-addon-datadog')
    const segments = within(bar).getAllByTestId(/^tile-bar-segment-/)
    const order = segments.map((s) => s.getAttribute('data-testid'))
    expect(order).toEqual(['tile-bar-segment-addon-datadog-out_of_sync', 'tile-bar-segment-addon-datadog-in_sync'])
  })

  it('the footer states the addon-unit count in tiles view, same wording the list view uses when grouped', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))
    await user.click(screen.getByTestId('group-by-addon'))

    await screen.findByTestId('secret-tiles')
    // 3 addons + the connections bucket = 4 "addons" units, 6 secrets total.
    expect(screen.getByTestId('secrets-summary')).toHaveTextContent('4 addons, 6 secrets')
  })

  it('switching back to list view keeps the same group-by choice and headings', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))
    await user.click(screen.getByTestId('group-by-addon'))
    await screen.findByTestId('secret-tiles')

    await user.click(screen.getByTestId('view-list'))
    expect(screen.getByTestId('group-by-addon')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('secret-group-addon-datadog')).toBeInTheDocument()
  })
})

const blankedResource = {
  kind: 'Secret',
  api_version: 'v1',
  name: 'datadog-secrets',
  namespace: 'datadog',
  secret_type: 'Opaque',
  created_at: '2026-07-01T00:00:00Z',
  labels: [],
  annotations: [],
  data_keys: [],
  read_from: 'cluster "prod-eu", namespace "datadog"',
  values_blanked: true,
}

describe('clicking a box opens that secret\'s own page (SSF-9) — no filter change on the way there', () => {
  it('navigates to the exact secret\'s page — no addon/kind filter added along the way', async () => {
    const user = userEvent.setup()
    mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    const box = await screen.findByTestId('secret-box-values-prod-eu-datadog')
    await user.click(box)

    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveAttribute('data-pathname', '/secret-sync/values-prod-eu-datadog'))
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(panel).toBeInTheDocument()
    expect(panel).toHaveTextContent('datadog-secrets')
    // The click opened that secret's own page — it never touched the
    // list's own addon/kind filters on the way there (openRowDetail reads
    // the query string, it doesn't mutate it).
    expect(screen.getByTestId('location-probe')).not.toHaveTextContent('addon=')
    expect(screen.getByTestId('location-probe')).not.toHaveTextContent('kind=')
  })

  it('opens the same panel a connection-secret box stands for', async () => {
    const user = userEvent.setup()
    mockGetConnectionSecretResource.mockResolvedValue({ ...blankedResource, name: 'prod-eu', namespace: 'argocd' })
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    const box = await screen.findByTestId('secret-box-connection-prod-eu')
    await user.click(box)

    const panel = await screen.findByTestId('secret-detail-panel')
    expect(panel).toHaveTextContent('prod-eu')
  })
})

describe('filters and search narrow the boxes exactly as they narrow list rows', () => {
  it('a status chip narrows the flat grid to matching boxes only', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))
    await screen.findByTestId('secret-tiles')

    await user.click(screen.getByTestId('filter-chip-out_of_sync'))

    const grid = await screen.findByTestId('secret-tiles')
    expect(within(grid).getAllByRole('button')).toHaveLength(2)
    expect(within(grid).getByTestId('secret-box-connection-staging-us')).toBeInTheDocument()
    expect(within(grid).getByTestId('secret-box-values-staging-us-datadog')).toBeInTheDocument()
  })

  it('search text narrows the boxes the same way it narrows list rows', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))
    await screen.findByTestId('secret-tiles')

    await user.type(screen.getByPlaceholderText(/search by cluster/i), 'vault')

    const grid = await screen.findByTestId('secret-tiles')
    expect(within(grid).getAllByRole('button')).toHaveLength(1)
    expect(within(grid).getByTestId('secret-box-values-prod-eu-vault')).toBeInTheDocument()
  })
})

describe('a long secret name truncates with the full name in the hover title', () => {
  it('the box carries the full name in a title attribute', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    await user.click(screen.getByTestId('view-tiles'))

    const box = await screen.findByTestId('secret-box-values-prod-eu-grafana')
    const nameEl = within(box).getByTitle('kube-prometheus-stack-grafana-admin-credentials-extra-long')
    expect(nameEl).toHaveClass('truncate')
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
