// ManagedSecrets — the /secrets page rebuilt as one dense resource list
// (S1-S8). Pins: the source column ("git" / "the vault") appears on every
// row and survives sort/filter; the default sort is worst-first, never
// alphabetical; filter chips show real counts and filter correctly; a row
// click opens the detail panel with the right content; the values-secret
// Diff makes no network call, ever; and a row-menu info hint appears only
// next to a genuinely disabled action.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
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

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
  },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  triggerSecretsReconcile: (...args: unknown[]) => mockTriggerSecretsReconcile(...args),
  reconcileCluster: (...args: unknown[]) => mockReconcileCluster(...args),
  resyncClusterLabels: (...args: unknown[]) => mockResyncClusterLabels(...args),
  refreshAddonValuesSecret: (...args: unknown[]) => mockRefreshAddonValuesSecret(...args),
  syncAddonValuesSecret: (...args: unknown[]) => mockSyncAddonValuesSecret(...args),
}))

function renderPage() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter>
        <ManagedSecrets />
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

// prod-eu rows are in_sync, staging-us rows are out_of_sync — the fixture
// this suite uses for both the source-column and worst-first-sort pins.
const baseResponse: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    {
      cluster: 'prod-eu',
      secret_namespace: 'argocd',
      secret_name: 'prod-eu',
      state: 'in_sync',
      last_checked: '2026-08-05T00:00:00Z',
      last_repaired: '2026-08-04T23:00:00Z',
      last_repaired_detail: 'secret created',
    },
    {
      cluster: 'staging-us',
      state: 'out_of_sync',
    },
  ],
  addon_values_secrets: [
    {
      cluster: 'prod-eu',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'in_sync',
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
      last_check_error: "Sharko couldn't fetch this secret's value from the vault.",
    },
  ],
  engines: {
    cluster_connection: { wired: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
    addon_values: { wired: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
  },
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('ManagedSecrets', () => {
  it('shows the source column on every row — git for connection secrets, the vault for addon-values secrets (S3 honesty lock)', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    const connRow1 = screen.getByTestId('secret-row-connection-prod-eu')
    const connRow2 = screen.getByTestId('secret-row-connection-staging-us')
    expect(within(connRow1).getByText('git')).toBeInTheDocument()
    expect(within(connRow2).getByText('git')).toBeInTheDocument()

    const valuesRow1 = screen.getByTestId('secret-row-values-prod-eu-datadog')
    const valuesRow2 = screen.getByTestId('secret-row-values-staging-us-datadog')
    expect(within(valuesRow1).getByText('the vault')).toBeInTheDocument()
    expect(within(valuesRow2).getByText('the vault')).toBeInTheDocument()

    // Never a generic phrase shared by both kinds — connection rows never
    // say "the vault" and values rows never say "git" in this column.
    expect(within(connRow1).queryByText('the vault')).not.toBeInTheDocument()
    expect(within(valuesRow1).queryByText('git')).not.toBeInTheDocument()
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

  it('filter chips show real per-state counts and filter the table when clicked (S4)', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('filter-chip-in_sync')).toBeInTheDocument())

    expect(screen.getByTestId('filter-chip-in_sync')).toHaveTextContent('In sync 2')
    expect(screen.getByTestId('filter-chip-out_of_sync')).toHaveTextContent('Out of sync 2')
    expect(screen.getByTestId('filter-chip-missing')).toHaveTextContent('Missing 0')
    expect(screen.getByTestId('filter-chip-unknown')).toHaveTextContent('Not checked yet 0')

    // All 4 rows visible before filtering.
    expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(4)

    fireEvent.click(screen.getByTestId('filter-chip-out_of_sync'))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()
    expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument()

    // Clicking the same chip again clears the filter.
    fireEvent.click(screen.getByTestId('filter-chip-out_of_sync'))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(4))
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

  it('the values-secret Diff never makes a network call — it restates the row state and shows the S8 check-failure sentence', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-values-staging-us-datadog'))

    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByText(/Carries values for addon/)).toBeInTheDocument()
    expect(within(panel).getByText('Compared against the vault — git only holds a pointer to it.')).toBeInTheDocument()
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
    })
    renderPage()

    await waitFor(() => expect(screen.getAllByText('· Not running on this server.')).toHaveLength(2))
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
