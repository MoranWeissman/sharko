// ManagedSecrets — the /secrets page rebuilt to look like ArgoCD's own
// Application list (maintainer's adopted design review, "shaped like
// ArgoCD but painted like the opposite of ArgoCD"). Pins:
//
//  - S3 honesty lock: every row states, in visible text, what it's
//    compared against ("cluster connection · follows git" /
//    "addon values · follows <the real backend name>") — never
//    tooltip-only, never the generic misleading "the vault".
//  - the default sort is worst-first, never alphabetical.
//  - B1: filter-chip counts follow the search box, not the chip filter
//    itself (selecting a chip must not zero out the other chips' counts).
//  - B2: the "Showing X–Y of Z" footer is honest — never "N of N shown"
//    while a page of N-minus-something rows is on screen — and says when
//    a filter has narrowed the total.
//  - B3: the active chip filter, the search text, and the selected row
//    all live in the URL and survive a reload.
//  - H2: the status word is plain dark ink — colour lives on the status
//    DOT only.
//  - H3: the "not checked yet" mark is a hollow ring, a different SHAPE
//    from the filled "in sync" dot, not just a different colour.
//  - a row click opens the detail panel with the right content; the
//    values-secret Diff makes no network call, ever; a row-menu info hint
//    appears only next to a genuinely disabled action.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useSearchParams } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

// Refresh/Sync are role-gated (admin/operator only) — provide an admin
// auth context so RoleGuard renders them; every test here cares about
// those controls existing, not about the role gate itself (that's covered
// by RoleGuard's own tests and the server-side authz tests).
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
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

const mockGetManagedSecrets = vi.fn()
const mockTriggerSecretsReconcile = vi.fn()
const mockReconcileCluster = vi.fn()
const mockResyncClusterLabels = vi.fn()
const mockRefreshAddonValuesSecret = vi.fn()
const mockSyncAddonValuesSecret = vi.fn()
const mockGetClusterComparison = vi.fn()
// G4 — the detail panel now also reads the LIVE Secret. These two are
// mocked here so opening a panel in this suite makes exactly one such
// call and never a real one; the blanking itself is the server's job and
// is pinned server-side.
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
  },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  triggerSecretsReconcile: (...args: unknown[]) => mockTriggerSecretsReconcile(...args),
  reconcileCluster: (...args: unknown[]) => mockReconcileCluster(...args),
  resyncClusterLabels: (...args: unknown[]) => mockResyncClusterLabels(...args),
  refreshAddonValuesSecret: (...args: unknown[]) => mockRefreshAddonValuesSecret(...args),
  syncAddonValuesSecret: (...args: unknown[]) => mockSyncAddonValuesSecret(...args),
}))

// Renders the current URL's search string into a testid'd node — B3 pins
// need to see the URL actually change, not just the component's own state.
function LocationProbe() {
  const [searchParams] = useSearchParams()
  return <div data-testid="location-probe">{searchParams.toString()}</div>
}

function renderPage(initialEntries: string[] = ['/secrets']) {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={initialEntries}>
        <LocationProbe />
        <ManagedSecrets />
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

// prod-eu rows are in_sync, staging-us rows are out_of_sync — the fixture
// this suite uses for both the source-line and worst-first-sort pins.
const baseResponse: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    {
      cluster: 'prod-eu',
      secret_namespace: 'argocd',
      secret_name: 'prod-eu',
      state: 'in_sync',
      source: 'git',
      last_checked: '2026-08-05T00:00:00Z',
      last_repaired: '2026-08-04T23:00:00Z',
      last_repaired_detail: 'secret created',
    },
    {
      cluster: 'staging-us',
      state: 'out_of_sync',
      source: 'git',
    },
  ],
  addon_values_secrets: [
    {
      cluster: 'prod-eu',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'in_sync',
      source: 'AWS Secrets Manager',
      last_checked: '2026-08-05T00:00:00Z',
      last_repaired: '2026-08-04T22:00:00Z',
      last_repaired_detail: 'secret created',
    },
    {
      cluster: 'staging-us',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'out_of_sync',
      source: 'AWS Secrets Manager',
      last_check_error: "Sharko couldn't fetch this secret's value from the vault.",
    },
  ],
  engines: {
    cluster_connection: { wired: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
    addon_values: { wired: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
  },
  addon_values_secret_source: 'AWS Secrets Manager',
}

const blankedResource = {
  kind: 'Secret',
  api_version: 'v1',
  name: 'datadog-secrets',
  namespace: 'datadog',
  secret_type: 'Opaque',
  created_at: '2026-07-01T00:00:00Z',
  labels: [{ key: 'app.kubernetes.io/managed-by', value: 'sharko' }],
  annotations: [],
  data_keys: [
    { key: 'api-key', value: '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022' },
    { key: 'app-key', value: '\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022' },
  ],
  read_from: 'cluster "prod-eu", namespace "datadog"',
  values_blanked: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetConnectionSecretResource.mockResolvedValue({ ...blankedResource, name: 'prod-eu', namespace: 'argocd' })
  mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
})

describe('ManagedSecrets', () => {
  it('states its source in visible text on every row — connection rows read "cluster connection · follows git", values rows read "addon values · follows <the real backend>" (S3 honesty lock)', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    const connRow1 = screen.getByTestId('secret-row-connection-prod-eu')
    const connRow2 = screen.getByTestId('secret-row-connection-staging-us')
    expect(within(connRow1).getByText('cluster connection · follows git')).toBeInTheDocument()
    expect(within(connRow2).getByText('cluster connection · follows git')).toBeInTheDocument()

    const valuesRow1 = screen.getByTestId('secret-row-values-prod-eu-datadog')
    const valuesRow2 = screen.getByTestId('secret-row-values-staging-us-datadog')
    expect(within(valuesRow1).getByText('addon values · follows AWS Secrets Manager')).toBeInTheDocument()
    expect(within(valuesRow2).getByText('addon values · follows AWS Secrets Manager')).toBeInTheDocument()

    // Never the generic, misleading "the vault" wording — the server's
    // real backend name is what ships.
    expect(screen.queryByText(/the vault/)).not.toBeInTheDocument()
  })

  // G1: the backend a row follows is now a PER-ROW fact (row.source), so
  // this fixture sets it on the row. The page-level
  // addon_values_secret_source is set to match only because the "Refresh
  // all" hint still reads it — the row itself no longer does.
  it('falls back to the generic lowercase "secrets store" when the server has no real backend name to give', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      addon_values_secrets: baseResponse.addon_values_secrets.map((r) => ({ ...r, source: 'secrets store' })),
      addon_values_secret_source: 'secrets store',
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    expect(
      within(screen.getByTestId('secret-row-values-prod-eu-datadog')).getByText('addon values · follows secrets store'),
    ).toBeInTheDocument()
  })

  it('sorts worst-first by default — out-of-sync rows come before in-sync rows, never alphabetically (S3)', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    const rows = screen.getAllByTestId(/^secret-row-/)
    const order = rows.map((r) => r.getAttribute('data-testid'))
    const outOfSyncIdx = order.map((id, i) => (id?.includes('staging-us') ? i : -1)).filter((i) => i >= 0)
    const inSyncIdx = order.map((id, i) => (id?.includes('prod-eu') ? i : -1)).filter((i) => i >= 0)

    // Both out-of-sync rows (staging-us) sort ahead of both in-sync rows
    // (prod-eu) — alphabetically "prod-eu" would come first, so this only
    // passes if the sort is genuinely state-priority-based.
    expect(Math.max(...outOfSyncIdx)).toBeLessThan(Math.min(...inSyncIdx))
  })

  it('filter chips show real per-state counts (plus an All chip) and filter the table when clicked (S4)', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('filter-chip-in_sync')).toBeInTheDocument())

    expect(screen.getByTestId('filter-chip-all')).toHaveTextContent('All4')
    expect(screen.getByTestId('filter-chip-in_sync')).toHaveTextContent('In sync2')
    expect(screen.getByTestId('filter-chip-out_of_sync')).toHaveTextContent('Out of sync2')
    expect(screen.getByTestId('filter-chip-missing')).toHaveTextContent('Missing0')
    expect(screen.getByTestId('filter-chip-unknown')).toHaveTextContent('Not checked yet0')

    // All 4 rows visible before filtering.
    expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(4)

    fireEvent.click(screen.getByTestId('filter-chip-out_of_sync'))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()
    expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument()

    // Clicking the same chip again clears the filter back to "All".
    fireEvent.click(screen.getByTestId('filter-chip-out_of_sync'))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(4))
  })

  it('B1: chip counts follow the search box, and selecting a chip never zeroes the OTHER chips (they stay computed off the search, not the chip filter)', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('filter-chip-in_sync')).toBeInTheDocument())

    // Narrow to "prod" — only the two prod-eu (in_sync) rows match.
    const search = screen.getByPlaceholderText('Search by cluster, addon, or secret name...')
    fireEvent.change(search, { target: { value: 'prod' } })

    await waitFor(() => expect(screen.getByTestId('filter-chip-in_sync')).toHaveTextContent('In sync2'))
    expect(screen.getByTestId('filter-chip-out_of_sync')).toHaveTextContent('Out of sync0')
    expect(screen.getByTestId('filter-chip-all')).toHaveTextContent('All2')

    // Now also select the in_sync chip — the OUT OF SYNC chip must still
    // read the same search-scoped count (0), not zero-from-being-unselected
    // and not the pre-search count either. This is the exact bug: counts
    // were memoized on unifiedRows only, never the search text.
    fireEvent.click(screen.getByTestId('filter-chip-in_sync'))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('filter-chip-out_of_sync')).toHaveTextContent('Out of sync0')
    expect(screen.getByTestId('filter-chip-in_sync')).toHaveTextContent('In sync2')
  })

  it('B2: the footer says "Showing X–Y of Z" honestly — never "N of N shown" with a partial page on screen — and states when a filter narrowed the total', async () => {
    const manyRows = Array.from({ length: 25 }, (_, i) => ({
      cluster: `cluster-${String(i).padStart(2, '0')}`,
      state: 'in_sync',
      last_checked: '2026-08-05T00:00:00Z',
    }))
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: manyRows,
      addon_values_secrets: [],
      engines: {
        cluster_connection: { wired: true, interval_seconds: 30 },
        addon_values: { wired: false },
      },
      addon_values_secret_source: 'secrets store',
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-cluster-00')).toBeInTheDocument())
    // Default page size is 20 of 25 total — must be an honest range, never "25 of 25".
    expect(screen.getByTestId('pagination-summary')).toHaveTextContent('Showing 1–20 of 25')
    expect(screen.getByTestId('pagination-summary')).not.toHaveTextContent('25 of 25')

    // Filtering narrows the total — the summary says so explicitly.
    const search = screen.getByPlaceholderText('Search by cluster, addon, or secret name...')
    fireEvent.change(search, { target: { value: 'cluster-0' } })
    await waitFor(() => expect(screen.getByTestId('pagination-summary')).toHaveTextContent('filtered from 25'))
  })

  it('B3: the active chip filter and the search text are written to the URL and restored from it on load', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('filter-chip-out_of_sync')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('filter-chip-out_of_sync'))
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('state=out_of_sync'))

    const search = screen.getByPlaceholderText('Search by cluster, addon, or secret name...')
    fireEvent.change(search, { target: { value: 'staging' } })
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('q=staging'))

    // A fresh render seeded with that exact URL comes up already filtered —
    // this is the "survives a reload" half of the pin.
    renderPage(['/secrets?state=out_of_sync&q=staging'])
    await waitFor(() => expect(screen.getAllByTestId('filter-chip-out_of_sync').length).toBeGreaterThan(0))
    const chips = screen.getAllByTestId('filter-chip-out_of_sync')
    expect(chips[chips.length - 1]).toHaveAttribute('aria-pressed', 'true')
    const searches = screen.getAllByPlaceholderText('Search by cluster, addon, or secret name...')
    expect(searches[searches.length - 1]).toHaveValue('staging')
  })

  it('B3: the selected row is written to the URL and a reload with that URL opens the same row', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockGetClusterComparison.mockResolvedValue({ cluster: { name: 'prod-eu', labels: {}, last_reconcile: null } })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-prod-eu'))
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('row=connection-prod-eu'))

    renderPage(['/secrets?row=connection-prod-eu'])
    const panel = await screen.findAllByTestId('secret-detail-panel')
    expect(panel.length).toBeGreaterThan(0)
  })

  it('clicking the connection-engine\'s red error names the cluster, carries a time, and filters the table to that cluster on click', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      engines: {
        ...baseResponse.engines,
        cluster_connection: {
          ...baseResponse.engines.cluster_connection,
          last_error: "Sharko couldn't reach this cluster's Kubernetes API.",
          last_error_cluster: 'staging-us',
          last_error_at: '2026-08-05T00:10:00Z',
        },
      },
    })
    renderPage()

    const errorEl = await screen.findByTestId('engine-error-connection')
    expect(errorEl).toHaveTextContent('staging-us')
    expect(errorEl).toHaveTextContent("Sharko couldn't reach this cluster's Kubernetes API.")

    fireEvent.click(errorEl)
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()
  })

  it('H2: the status word is plain dark ink for every state — colour lives on the status dot only', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getAllByTestId('status-mark').length).toBeGreaterThan(0))
    for (const mark of screen.getAllByTestId('status-mark')) {
      // The word's own className never carries a status colour (green/
      // amber/red) — those live on the nested <StatusDot> only.
      expect(mark.className).not.toMatch(/text-(green|amber|red)-\d/)
    }
  })

  it('H3: the "not checked yet" mark is a hollow ring — a different SHAPE from the filled in-sync dot, not just a different colour', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [
        { cluster: 'prod-eu', state: 'in_sync' },
        { cluster: 'staging-us', state: 'unknown' },
      ],
      addon_values_secrets: [],
      engines: { cluster_connection: { wired: true, interval_seconds: 30 }, addon_values: { wired: false } },
      addon_values_secret_source: 'secrets store',
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    // Scoped to each ROW's own dot — the filter chips above also render
    // StatusDot for their own glyphs, which isn't what this pin is about.
    const inSyncDot = within(screen.getByTestId('secret-row-connection-prod-eu')).getByTestId('status-dot')
    const unknownDot = within(screen.getByTestId('secret-row-connection-staging-us')).getByTestId('status-dot')
    expect(inSyncDot).toHaveAttribute('data-hollow', 'false')
    expect(unknownDot).toHaveAttribute('data-hollow', 'true')
  })

  it('clicking a connection-secret row opens the detail panel with identity, purpose, source, state, and a Diff that fetches labels only', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockGetClusterComparison.mockResolvedValue({
      cluster: {
        name: 'staging-us',
        labels: {},
        last_reconcile: {
          time: '2026-08-05T00:00:00Z',
          outcome: 'succeeded',
          label_drift: { added: ['datadog'], removed: [], changed: [] },
        },
      },
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-staging-us'))

    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByText(/Connects/)).toBeInTheDocument()
    expect(within(panel).getByText('Compared against git.')).toBeInTheDocument()

    await waitFor(() => expect(mockGetClusterComparison).toHaveBeenCalledWith('staging-us'))
    await waitFor(() => expect(within(panel).getByText(/Missing 1 addon label/)).toBeInTheDocument())
    expect(within(panel).getByText('datadog')).toBeInTheDocument()
  })

  it('the values-secret Diff never makes a network call — it restates the row state, the real source, and the S8 check-failure sentence', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-values-staging-us-datadog'))

    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByText(/Carries values for addon/)).toBeInTheDocument()
    expect(within(panel).getByText('Compared against AWS Secrets Manager — git only holds a pointer to it.')).toBeInTheDocument()
    expect(within(panel).getByText(/Does not match its source right now/)).toBeInTheDocument()

    // S8: the mapped canned sentence shows, plainly labeled as a failed
    // check — never implying the state itself is the report of drift.
    expect(within(panel).getByTestId('last-check-error')).toHaveTextContent(
      "The last check failed: Sharko couldn't fetch this secret's value from the vault.",
    )

    // Never a live fetch for a values-secret Diff — no content to leak.
    expect(mockGetClusterComparison).not.toHaveBeenCalled()

    // A different (in-sync) values row shows the matching-source sentence,
    // still with zero network calls.
    fireEvent.click(screen.getByTestId('secret-row-values-prod-eu-datadog'))
    await waitFor(() => expect(within(panel).getByText('Matches its source.')).toBeInTheDocument())
    expect(mockGetClusterComparison).not.toHaveBeenCalled()
  })

  it('shows an info hint next to Sync ONLY when it is genuinely disabled, with the correct accessible label (S7.1)', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    // prod-eu is in_sync — Sync is disabled and carries a hint.
    await user.click(screen.getByRole('button', { name: 'Actions for prod-eu' }))
    const syncItemDisabled = await screen.findByRole('menuitem', { name: /Sync/ })
    expect(within(syncItemDisabled).getByLabelText('Why is Sync unavailable?')).toBeInTheDocument()
    // Refresh is always enabled here — it must carry NO hint at all.
    const refreshItem = screen.getByRole('menuitem', { name: /Refresh/ })
    expect(within(refreshItem).queryByLabelText(/Why is Refresh unavailable\?/)).not.toBeInTheDocument()
    await user.keyboard('{Escape}')

    // staging-us is out_of_sync — Sync is enabled and must carry NO hint.
    await user.click(screen.getByRole('button', { name: 'Actions for staging-us' }))
    const syncItemEnabled = await screen.findByRole('menuitem', { name: /Sync/ })
    expect(within(syncItemEnabled).queryByLabelText(/Why is Sync unavailable\?/)).not.toBeInTheDocument()
  })

  it('search narrows the table across both secret kinds', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    const search = screen.getByPlaceholderText('Search by cluster, addon, or secret name...')
    fireEvent.change(search, { target: { value: 'staging' } })

    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()
    expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument()
  })

  it('paginates a long combined list', async () => {
    const manyRows = Array.from({ length: 25 }, (_, i) => ({
      cluster: `cluster-${String(i).padStart(2, '0')}`,
      state: 'in_sync',
      last_checked: '2026-08-05T00:00:00Z',
    }))
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: manyRows,
      addon_values_secrets: [],
      engines: {
        cluster_connection: { wired: true, interval_seconds: 30 },
        addon_values: { wired: false },
      },
      addon_values_secret_source: 'secrets store',
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-cluster-00')).toBeInTheDocument())
    // Default page size is 20 — row 24 (cluster-24) is on page 2.
    expect(screen.queryByTestId('secret-row-connection-cluster-24')).not.toBeInTheDocument()

    fireEvent.click(screen.getAllByRole('button', { name: 'Next' })[0])

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-cluster-24')).toBeInTheDocument())
    expect(screen.queryByTestId('secret-row-connection-cluster-00')).not.toBeInTheDocument()
  })

  it('the detail panel has a link to the cluster/addon page that navigates there', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-prod-eu'))

    const link = await screen.findByTestId('detail-view-page-link')
    fireEvent.click(link)
    expect(mockNavigate).toHaveBeenCalledWith('/clusters/prod-eu')

    fireEvent.click(screen.getByTestId('secret-row-values-prod-eu-datadog'))
    const link2 = await screen.findByTestId('detail-view-page-link')
    fireEvent.click(link2)
    expect(mockNavigate).toHaveBeenCalledWith('/addons/datadog')
  })

  it('reports an engine as not running, rather than fabricating cadence info, when it is not wired', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [],
      addon_values_secrets: [],
      engines: {
        cluster_connection: { wired: false },
        addon_values: { wired: false },
      },
      addon_values_secret_source: 'secrets store',
    })
    renderPage()

    await waitFor(() => expect(screen.getAllByText('Not running on this server.')).toHaveLength(2))
  })

  it('Refresh all triggers both engines when connection secrets exist', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockTriggerSecretsReconcile.mockResolvedValue({ status: 'ok' })
    mockReconcileCluster.mockResolvedValue({ status: 'ok', message: 'triggered' })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('refresh-all')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('refresh-all'))

    await waitFor(() => expect(mockTriggerSecretsReconcile).toHaveBeenCalled())
    // Any real cluster name works — the endpoint triggers the whole
    // reconciler regardless of which cluster name is in the path.
    expect(mockReconcileCluster).toHaveBeenCalledWith('prod-eu')
  })
})
