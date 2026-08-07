// ManagedSecrets — the page now named "Secret Sync" (/secret-sync, with
// /secrets kept as a redirect alias — gitops-proud P4-I D1), rebuilt to
// look like ArgoCD's own Application list (maintainer's adopted design
// review, "shaped like ArgoCD but painted like the opposite of ArgoCD").
// Pins:
//
//  - S3 honesty lock: every row states, in visible text, what it's
//    compared against ("cluster connection · follows git" /
//    "addon values · follows <the real backend name>") — never
//    tooltip-only, never the generic misleading "the vault".
//  - the default sort is worst-first, never alphabetical.
//  - B1: filter-chip counts follow the search box, not the chip filter
//    itself (selecting a chip must not zero out the other chips' counts).
//  - B2: the footer states an honest count and says when a filter has
//    narrowed the total. (P4-I I2: paging is gone — the footer used to
//    read "Showing X–Y of Z", now it's a plain total; every filtered row
//    renders at once under a scrolling, sticky-header table instead.)
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

// P1-A A3 pins the exact toast wording, so the toast helper is mocked here
// rather than rendered — the sentence is what matters, not the widget.
const mockShowToast = vi.fn()
vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) }
})

const mockGetManagedSecrets = vi.fn()
const mockTriggerSecretsReconcile = vi.fn()
const mockCheckAllAddonValuesSecrets = vi.fn()
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
// H4-3: "Sharko's record" reads the existing audit log endpoint, scoped to
// the opened row — mocked here the same way every other API call in this
// suite is, defaulted to an empty list in beforeEach.
const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
  },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  triggerSecretsReconcile: (...args: unknown[]) => mockTriggerSecretsReconcile(...args),
  checkAllAddonValuesSecrets: (...args: unknown[]) => mockCheckAllAddonValuesSecrets(...args),
  reconcileCluster: (...args: unknown[]) => mockReconcileCluster(...args),
  resyncClusterLabels: (...args: unknown[]) => mockResyncClusterLabels(...args),
  refreshAddonValuesSecret: (...args: unknown[]) => mockRefreshAddonValuesSecret(...args),
  syncAddonValuesSecret: (...args: unknown[]) => mockSyncAddonValuesSecret(...args),
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

// Renders the current URL's search string into a testid'd node — B3 pins
// need to see the URL actually change, not just the component's own state.
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
      self_heals: true,
    },
    {
      cluster: 'staging-us',
      state: 'out_of_sync',
      source: 'git',
      self_heals: false,
      compared_revision: 'abcdef1234567890abcdef1234567890abcdef12',
      compared_path: 'configuration/managed-clusters.yaml',
      applied_revision: '00000000000000000000000000000000000000',
      drift_source: 'git',
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
      self_heals: true,
    },
    {
      cluster: 'staging-us',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'out_of_sync',
      source: 'AWS Secrets Manager',
      last_check_error: "Sharko couldn't fetch this secret's value from the vault.",
      self_heals: true,
    },
  ],
  engines: {
    cluster_connection: { wired: true, enabled: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
    addon_values: { wired: true, enabled: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
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
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
})

describe('ManagedSecrets', () => {
  it('states its source in visible text on every row, in the SOURCE column, as "checked against X" — connection rows read git, values rows read the real backend (S3 honesty lock, moved into the column by G1, worded by H3)', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    const connRow1 = screen.getByTestId('secret-row-connection-prod-eu')
    const connRow2 = screen.getByTestId('secret-row-connection-staging-us')
    expect(within(connRow1).getByTestId('cell-source')).toHaveTextContent('checked against git')
    expect(within(connRow2).getByTestId('cell-source')).toHaveTextContent('checked against git')

    const valuesRow1 = screen.getByTestId('secret-row-values-prod-eu-datadog')
    const valuesRow2 = screen.getByTestId('secret-row-values-staging-us-datadog')
    expect(within(valuesRow1).getByTestId('cell-source')).toHaveTextContent('checked against AWS Secrets Manager')
    expect(within(valuesRow2).getByTestId('cell-source')).toHaveTextContent('checked against AWS Secrets Manager')

    // Never the generic, misleading "the vault" wording — the server's
    // real backend name is what ships.
    expect(screen.queryByText(/the vault/)).not.toBeInTheDocument()
  })

  // H2 (gitops-proud P4-H): the row is one line now — the old grey KIND
  // subline under the identity is gone, and both kinds print their
  // cluster in its own column instead of connection rows leaving it blank.
  it('renders one line per row: no kind subline, and the cluster column is filled in for both kinds', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    expect(screen.queryByText('cluster connection')).not.toBeInTheDocument()
    expect(screen.queryByText('addon values')).not.toBeInTheDocument()

    const connRow1 = screen.getByTestId('secret-row-connection-prod-eu')
    const valuesRow1 = screen.getByTestId('secret-row-values-prod-eu-datadog')
    // Walk finding #140 split NAME out of the old combined identity cell, so
    // "prod-eu" now legitimately appears twice on the connection row (its
    // secret name AND its cluster) — check each column by its own testid.
    expect(within(connRow1).getByTestId('cell-cluster')).toHaveTextContent('prod-eu')
    expect(within(valuesRow1).getByTestId('cell-cluster')).toHaveTextContent('prod-eu')
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
      within(screen.getByTestId('secret-row-values-prod-eu-datadog')).getByTestId('cell-source'),
    ).toHaveTextContent('secrets store')
  })

  // G1 — the ADDON column: values rows show the addon, connection rows show
  // a plain dash (they aren't addon secrets).
  it('shows the addon in its own column — a real addon name on values rows, a plain dash on connection rows', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    expect(within(screen.getByTestId('secret-row-connection-prod-eu')).getByTestId('cell-addon')).toHaveTextContent('—')
    expect(within(screen.getByTestId('secret-row-values-prod-eu-datadog')).getByTestId('cell-addon')).toHaveTextContent('datadog')
  })

  // G1 — ADDON and SOURCE are sortable columns via their header buttons.
  it('sorts by the ADDON and SOURCE columns when their headers are clicked', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        { cluster: 'c1', addon: 'zeta', state: 'in_sync', source: 'AWS Secrets Manager' },
        { cluster: 'c2', addon: 'alpha', state: 'in_sync', source: 'a Kubernetes Secret' },
      ],
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-values-c1-zeta')).toBeInTheDocument())

    await user.click(screen.getByTestId('sort-addon'))
    await waitFor(() => {
      const order = screen.getAllByTestId(/^secret-row-/).map((r) => r.getAttribute('data-testid'))
      expect(order).toEqual(['secret-row-values-c2-alpha', 'secret-row-values-c1-zeta'])
    })

    await user.click(screen.getByTestId('sort-source'))
    await waitFor(() => {
      const order = screen.getAllByTestId(/^secret-row-/).map((r) => r.getAttribute('data-testid'))
      // Ascending by row.sourceLabel: "AWS Secrets Manager" (c1) sorts
      // before "a Kubernetes Secret" (c2) — 'A' (65) < 'a' (97) by char
      // code, exactly the same plain string compare compareRows uses.
      expect(order).toEqual(['secret-row-values-c1-zeta', 'secret-row-values-c2-alpha'])
    })
  })

  // ───────────────────────────────────────────────────────────────────────
  // Walk finding #140 — NAMESPACE becomes its own column, and the status
  // words go ArgoCD-professional.
  // ───────────────────────────────────────────────────────────────────────

  // Before this, NAME rendered "namespace/name" in one capped cell, so rows
  // sharing a namespace and differing only in name truncated exactly where
  // they differed. NAME now shows only the name; NAMESPACE is its own cell.
  it('splits NAME and NAMESPACE into their own columns, each carrying its full value on hover', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        {
          cluster: 'prod-eu',
          addon: 'eso',
          secret_name: 'eso-creds-primary',
          secret_namespace: 'external-secrets',
          state: 'in_sync',
          source: 'AWS Secrets Manager',
        },
        {
          cluster: 'staging-us',
          addon: 'eso',
          secret_name: 'eso-creds-replica',
          secret_namespace: 'external-secrets',
          state: 'in_sync',
          source: 'AWS Secrets Manager',
        },
      ],
    })
    renderPage()

    const row1 = await screen.findByTestId('secret-row-values-prod-eu-eso')
    const row2 = screen.getByTestId('secret-row-values-staging-us-eso')

    // NAME shows only the secret name — two rows that share a namespace and
    // would have truncated identically before still read apart.
    expect(within(row1).getByTestId('cell-name')).toHaveTextContent('eso-creds-primary')
    expect(within(row1).getByTestId('cell-name')).not.toHaveTextContent('external-secrets')
    expect(within(row2).getByTestId('cell-name')).toHaveTextContent('eso-creds-replica')

    // NAMESPACE is its own cell, present on both rows.
    expect(within(row1).getByTestId('cell-namespace')).toHaveTextContent('external-secrets')
    expect(within(row2).getByTestId('cell-namespace')).toHaveTextContent('external-secrets')

    // Full values sit in the title attribute for when either cell truncates
    // at a narrow width.
    expect(within(row1).getByTestId('cell-name')).toHaveAttribute('title', 'eso-creds-primary')
    expect(within(row1).getByTestId('cell-namespace')).toHaveAttribute('title', 'external-secrets')
  })

  // NAMESPACE sorts like every other column.
  it('sorts by the NAMESPACE column when its header is clicked', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        { cluster: 'c1', addon: 'a', secret_name: 's1', secret_namespace: 'zeta', state: 'in_sync', source: 'AWS Secrets Manager' },
        { cluster: 'c2', addon: 'b', secret_name: 's2', secret_namespace: 'alpha', state: 'in_sync', source: 'AWS Secrets Manager' },
      ],
    })
    renderPage()

    await screen.findByTestId('secret-row-values-c1-a')

    await user.click(screen.getByTestId('sort-namespace'))
    await waitFor(() => {
      const order = screen.getAllByTestId(/^secret-row-/).map((r) => r.getAttribute('data-testid'))
      expect(order).toEqual(['secret-row-values-c2-b', 'secret-row-values-c1-a'])
    })
  })

  // ArgoCD-professional status words (maintainer-locked): "In sync" →
  // "Synced", "Not on the cluster" → "Missing". Checked on the row chip, the
  // panel's chip, and confirmed gone from the page entirely — plus the
  // panel's plain-English sentence, which does not change.
  it('renders "Synced" and "Missing" everywhere a status word shows, with the old wording gone from the page', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        {
          cluster: 'prod-eu',
          addon: 'datadog',
          secret_name: 'datadog-secrets',
          secret_namespace: 'datadog',
          state: 'in_sync',
          source: 'AWS Secrets Manager',
        },
        {
          cluster: 'staging-us',
          addon: 'datadog',
          secret_name: 'datadog-secrets',
          secret_namespace: 'datadog',
          state: 'missing',
          source: 'AWS Secrets Manager',
        },
      ],
    })
    renderPage()

    const syncedRow = await screen.findByTestId('secret-row-values-prod-eu-datadog')
    const missingRow = screen.getByTestId('secret-row-values-staging-us-datadog')

    expect(within(syncedRow).getByTestId('status-mark')).toHaveTextContent('Synced')
    expect(within(missingRow).getByTestId('status-mark')).toHaveTextContent('Missing')

    expect(screen.queryByText('In sync')).not.toBeInTheDocument()
    expect(screen.queryByText('Not on the cluster')).not.toBeInTheDocument()

    // The panel's status chip reads the same word, and the plain-English
    // sentence underneath it is exactly what it was before this pass.
    fireEvent.click(missingRow)
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('status-mark')).toHaveTextContent('Missing')
    expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('This secret was never created on the cluster — Sync creates it.')
  })

  // G1 — ADDON and SOURCE are matched by the search box.
  it('search matches the ADDON and SOURCE columns, not just cluster/name', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    const search = screen.getByPlaceholderText('Search by cluster, addon, secret name, or namespace...')

    // "datadog" matches the addon on both values rows, no connection rows.
    fireEvent.change(search, { target: { value: 'datadog' } })
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument()

    // "AWS Secrets Manager" (the source) matches only the values rows, never the connection rows (source "git").
    fireEvent.change(search, { target: { value: 'aws secrets' } })
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()
  })

  // G1 — ADDON and SOURCE are filterable via their own selects.
  it('filters the table with the Addon and Source selects', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    // 4 rows total before any filter.
    expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(4)

    await user.selectOptions(screen.getByTestId('addon-filter-select'), 'datadog')
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByTestId('addon-filter-select'), '')
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(4))

    await user.selectOptions(screen.getByTestId('source-filter-select'), 'git')
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-values-prod-eu-datadog')).not.toBeInTheDocument()
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
    expect(screen.getByTestId('filter-chip-in_sync')).toHaveTextContent('Synced2')
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
    const search = screen.getByPlaceholderText('Search by cluster, addon, secret name, or namespace...')
    fireEvent.change(search, { target: { value: 'prod' } })

    await waitFor(() => expect(screen.getByTestId('filter-chip-in_sync')).toHaveTextContent('Synced2'))
    expect(screen.getByTestId('filter-chip-out_of_sync')).toHaveTextContent('Out of sync0')
    expect(screen.getByTestId('filter-chip-all')).toHaveTextContent('All2')

    // Now also select the in_sync chip — the OUT OF SYNC chip must still
    // read the same search-scoped count (0), not zero-from-being-unselected
    // and not the pre-search count either. This is the exact bug: counts
    // were memoized on unifiedRows only, never the search text.
    fireEvent.click(screen.getByTestId('filter-chip-in_sync'))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('filter-chip-out_of_sync')).toHaveTextContent('Out of sync0')
    expect(screen.getByTestId('filter-chip-in_sync')).toHaveTextContent('Synced2')
  })

  it('B2 (carried through I2 paging removal): the footer states an honest total, and says when a filter narrowed it', async () => {
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
    // I2: no paging — every row is on screen at once, so the footer states
    // the real total plainly, never a page range.
    expect(screen.getByTestId('secrets-summary')).toHaveTextContent('25 secrets')

    // Filtering narrows the total — the summary says so explicitly.
    const search = screen.getByPlaceholderText('Search by cluster, addon, secret name, or namespace...')
    fireEvent.change(search, { target: { value: 'cluster-0' } })
    await waitFor(() => expect(screen.getByTestId('secrets-summary')).toHaveTextContent('filtered from 25'))
  })

  it('B3: the active chip filter and the search text are written to the URL and restored from it on load', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('filter-chip-out_of_sync')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('filter-chip-out_of_sync'))
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('state=out_of_sync'))

    const search = screen.getByPlaceholderText('Search by cluster, addon, secret name, or namespace...')
    fireEvent.change(search, { target: { value: 'staging' } })
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('q=staging'))

    // A fresh render seeded with that exact URL comes up already filtered —
    // this is the "survives a reload" half of the pin.
    renderPage(['/secret-sync?state=out_of_sync&q=staging'])
    await waitFor(() => expect(screen.getAllByTestId('filter-chip-out_of_sync').length).toBeGreaterThan(0))
    const chips = screen.getAllByTestId('filter-chip-out_of_sync')
    expect(chips[chips.length - 1]).toHaveAttribute('aria-pressed', 'true')
    const searches = screen.getAllByPlaceholderText('Search by cluster, addon, secret name, or namespace...')
    expect(searches[searches.length - 1]).toHaveValue('staging')
  })

  it('B3: the selected row is written to the URL and a reload with that URL opens the same row', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockGetClusterComparison.mockResolvedValue({ cluster: { name: 'prod-eu', labels: {}, last_reconcile: null } })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-prod-eu'))
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('row=connection-prod-eu'))

    renderPage(['/secret-sync?row=connection-prod-eu'])
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

    // H3: the red line is now one short sentence naming the cluster and
    // what clicking it does — the raw server error text moved into the
    // info hint next to it, reachable by click, not printed inline.
    const errorEl = await screen.findByTestId('engine-error-connection')
    expect(errorEl).toHaveTextContent('staging-us')
    expect(errorEl).toHaveTextContent('click to see it')
    expect(errorEl).not.toHaveTextContent("Sharko couldn't reach this cluster's Kubernetes API.")

    fireEvent.click(within(errorEl).getByRole('button', { name: /A check failed on staging-us/ }))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()

    // The full error text is still there, one click away.
    fireEvent.click(within(errorEl).getByLabelText('What was the error?'))
    await waitFor(() => expect(screen.getByText("Sharko couldn't reach this cluster's Kubernetes API.")).toBeInTheDocument())
  })

  // P1-B B2: the addon-values engine's red line now carries a cluster and a
  // time exactly like the connection one, and clicks the same way —
  // EngineStat is generic over both engines, so this only needed the
  // backend to start sending the fields and the page to pass onErrorClick.
  it("clicking the addon-values engine's red error names the cluster, carries a time, and filters the table to that cluster on click", async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      engines: {
        ...baseResponse.engines,
        addon_values: {
          ...baseResponse.engines.addon_values,
          last_error: "Sharko couldn't connect to one of the clusters. Check that Sharko can reach that cluster, then click Refresh.",
          last_error_cluster: 'staging-us',
          last_error_at: '2026-08-05T00:10:00Z',
        },
      },
    })
    renderPage()

    const errorEl = await screen.findByTestId('engine-error-values')
    expect(errorEl).toHaveTextContent('staging-us')
    expect(errorEl).toHaveTextContent('click to see it')
    expect(errorEl).not.toHaveTextContent("Sharko couldn't connect to one of the clusters.")

    fireEvent.click(within(errorEl).getByRole('button', { name: /A check failed on staging-us/ }))
    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-values-prod-eu-datadog')).not.toBeInTheDocument()

    // The full error text is still there, one click away.
    fireEvent.click(within(errorEl).getByLabelText('What was the error?'))
    await waitFor(() =>
      expect(
        screen.getByText("Sharko couldn't connect to one of the clusters. Check that Sharko can reach that cluster, then click Refresh."),
      ).toBeInTheDocument(),
    )
  })

  // P1-B B1: a connection row whose last check FAILED is not the same
  // thing as a row that was never checked at all — but both are honestly
  // "Sharko doesn't know", so both render the same not-checked/unknown
  // family mark. The failure sentence is what tells them apart, in the
  // panel, exactly where a values row's check-failure sentence already
  // shows.
  it("a connection row whose last check failed renders the unknown-family mark (not out-of-sync) and shows the check-failure sentence in the panel", async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [
        ...baseResponse.cluster_connection_secrets,
        {
          cluster: 'spoke-asia',
          secret_namespace: 'argocd',
          secret_name: 'spoke-asia',
          state: 'unknown',
          source: 'git',
          last_checked: '2026-08-05T00:05:00Z',
          last_check_error: 'Sharko could not read git, so this check did not finish. Check that Sharko can reach your git host, then click Refresh.',
        },
      ],
    })
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-spoke-asia')
    const dot = within(row).getByTestId('status-dot')
    // Hollow/still — the same mark a genuinely never-checked row gets,
    // never the filled amber out_of_sync dot.
    expect(dot).toHaveAttribute('data-hollow', 'true')
    expect(dot).toHaveAttribute('data-status', 'unknown')
    expect(within(row).getByTestId('status-mark')).not.toHaveTextContent(/out of sync/i)

    fireEvent.click(row)
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('last-check-error')).toHaveTextContent(
      'The last check failed: Sharko could not read git, so this check did not finish. Check that Sharko can reach your git host, then click Refresh.',
    )
  })

  // P2-D D3: a connection row whose self-managed secret keeps getting
  // reverted shows a quiet warning in the panel, once the streak reaches 3.
  it('P2-D: a connection row with fight_count >= 3 shows the quiet fight warning in the panel', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [
        ...baseResponse.cluster_connection_secrets,
        {
          cluster: 'byo-cluster',
          secret_namespace: 'argocd',
          secret_name: 'byo-cluster',
          state: 'in_sync',
          source: 'git',
          last_checked: '2026-08-05T00:05:00Z',
          fight_count: 4,
        },
      ],
    })
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-byo-cluster')
    fireEvent.click(row)
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('fight-warning')).toHaveTextContent(
      'Something keeps changing this secret back — Sharko has re-applied it 4 times in a row.',
    )
  })

  // P2-D D3: a values row with a fight_count below 3 (or a connection row
  // below 3) must stay quiet — the warning is only for a real, sustained
  // pattern, never a single blip.
  it('P2-D: a connection row with fight_count below 3 shows no fight warning', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [
        ...baseResponse.cluster_connection_secrets,
        {
          cluster: 'byo-cluster',
          secret_namespace: 'argocd',
          secret_name: 'byo-cluster',
          state: 'in_sync',
          source: 'git',
          last_checked: '2026-08-05T00:05:00Z',
          fight_count: 2,
        },
      ],
    })
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-byo-cluster')
    fireEvent.click(row)
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).queryByTestId('fight-warning')).not.toBeInTheDocument()
  })

  // P2-D D3: the values-side counterpart — a secret whose last 3 checks
  // failed in a row shows its own quiet warning.
  it('P2-D: a values row with consecutive_failures >= 3 shows the quiet consecutive-failures warning in the panel', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      addon_values_secrets: [
        ...baseResponse.addon_values_secrets,
        {
          cluster: 'staging-us',
          addon: 'grafana',
          secret_name: 'grafana-secret',
          secret_namespace: 'monitoring',
          state: 'unknown',
          source: 'AWS Secrets Manager',
          last_checked: '2026-08-05T00:05:00Z',
          last_check_error: "Sharko couldn't connect to one of the clusters.",
          consecutive_failures: 3,
        },
      ],
    })
    renderPage()

    const row = await screen.findByTestId('secret-row-values-staging-us-grafana')
    fireEvent.click(row)
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('consecutive-failures-warning')).toHaveTextContent(
      'The last 3 checks of this secret failed in a row.',
    )
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
    expect(within(panel).getByText('Checked against git.')).toBeInTheDocument()

    await waitFor(() => expect(mockGetClusterComparison).toHaveBeenCalledWith('staging-us'))
    await waitFor(() => expect(within(panel).getByText(/Missing 1 addon label/)).toBeInTheDocument())
    expect(within(panel).getByText('datadog')).toBeInTheDocument()
  })

  // P2-C1/C3/C6: the out-of-sync connection row (staging-us in baseResponse)
  // carries a known compared revision, a compared path, an applied
  // revision that DISAGREES with it (drift blame "git"), and
  // self_heals: false — the panel must show all three facts, verbatim.
  it('the connection panel shows which commit it was compared against, the self-heal promise, and drift blame — verbatim', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockGetClusterComparison.mockResolvedValue({
      cluster: {
        name: 'staging-us',
        labels: {},
        last_reconcile: { time: '2026-08-05T00:00:00Z', outcome: 'succeeded', label_drift: { added: [], removed: [], changed: [] } },
      },
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-staging-us'))

    const panel = await screen.findByTestId('secret-detail-panel')

    // C1: short SHA (7 chars) + full path shown; full SHA on hover (title).
    const revisionLine = within(panel).getByTestId('detail-compared-revision')
    expect(revisionLine).toHaveTextContent('Checked against git abcdef1 · configuration/managed-clusters.yaml')
    expect(revisionLine.title).toBe('Full commit: abcdef1234567890abcdef1234567890abcdef12')

    // C3: self_heals: false on an out_of_sync row -> "Waiting for Sync."
    expect(within(panel).getByTestId('detail-self-heals')).toHaveTextContent('Waiting for Sync.')

    // C6: compared_revision != applied_revision -> "git moved" sentence.
    expect(within(panel).getByTestId('detail-drift-source')).toHaveTextContent(
      'Git moved — a newer commit changed what this secret should be.',
    )
  })

  // P2-C3: the in-sync row (prod-eu) must show neither the drift sentence
  // nor the self-heal promise — both are scoped to out_of_sync/missing rows.
  it('an in-sync connection row shows no self-heal promise and no drift blame', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-prod-eu'))

    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).queryByTestId('detail-self-heals')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('detail-drift-source')).not.toBeInTheDocument()
  })

  it('a values-secret row never fires the label-drift comparison — its verdict comes from the row state alone', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-values-staging-us-datadog'))

    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByText(/Carries values for addon/)).toBeInTheDocument()
    expect(within(panel).getByText('Checked against AWS Secrets Manager — git only holds a pointer to it.')).toBeInTheDocument()
    // P3-F2: the verdict is one of the five edge sentences.
    expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
      "These differ — Sync writes the source's version onto the cluster.",
    )

    // S8: the mapped canned sentence shows, plainly labeled as a failed
    // check — never implying the state itself is the report of drift.
    expect(within(panel).getByTestId('last-check-error')).toHaveTextContent(
      "The last check failed: Sharko couldn't fetch this secret's value from the vault.",
    )

    // Never a label-drift fetch for a values row — that comparison is a
    // connection-secret thing and a values secret's content must never
    // reach the browser.
    expect(mockGetClusterComparison).not.toHaveBeenCalled()

    // A different (in-sync) values row shows the matching verdict, still
    // with no label-drift call.
    fireEvent.click(screen.getByTestId('secret-row-values-prod-eu-datadog'))
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('These match — the cluster has what the source says.'),
    )
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

  // ───────────────────────────────────────────────────────────────────────
  // P1-A — a secret Sharko did not create
  // ───────────────────────────────────────────────────────────────────────

  it('renders a foreign row with its own status mark and word', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      addon_values_secrets: [
        {
          cluster: 'prod-eu',
          addon: 'eso',
          secret_name: 'eso-creds',
          secret_namespace: 'external-secrets',
          state: 'foreign',
          source: 'AWS Secrets Manager',
          last_checked: '2026-08-05T00:00:00Z',
        },
      ],
    })
    renderPage()

    const row = await screen.findByTestId('secret-row-values-prod-eu-eso')
    expect(within(row).getByText('Foreign')).toBeInTheDocument()
    const dot = within(row).getAllByTestId('status-dot')[0]
    expect(dot).toHaveAttribute('data-status', 'foreign')
    // Neither red nor amber — foreign is a boundary, not damage.
    expect(dot.className).not.toMatch(/red|amber/)
  })

  it('disables Sync on a foreign row with the exact sentence', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        {
          cluster: 'prod-eu',
          addon: 'eso',
          secret_name: 'eso-creds',
          secret_namespace: 'external-secrets',
          state: 'foreign',
          source: 'AWS Secrets Manager',
        },
      ],
    })
    renderPage()

    await screen.findByTestId('secret-row-values-prod-eu-eso')
    await user.click(screen.getByRole('button', { name: 'Actions for prod-eu / eso' }))
    const syncItem = await screen.findByRole('menuitem', { name: /Sync/ })
    expect(syncItem).toHaveAttribute('aria-disabled', 'true')
    await user.click(within(syncItem).getByLabelText('Why is Sync unavailable?'))
    await waitFor(() =>
      expect(screen.getByText('Someone else created this one — Sharko will not touch it.')).toBeInTheDocument(),
    )
  })

  // P1-B symmetry fix: a values row whose last check FAILED now renders
  // "unknown" (never out_of_sync — see the Go-side
  // TestAddonValuesSecretRowState_ErrorOutcomeIsUnknownNotOutOfSync). This
  // pins the UI-side consequence the coordinator asked to verify: Sync
  // must stay disabled for it, exactly like any other unknown row — a
  // failed check must never leave Sync looking like a safe action to take.
  it('disables Sync on a values row whose last check failed (state unknown) with the "click Refresh first" reason', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        {
          cluster: 'prod-eu',
          addon: 'eso',
          secret_name: 'eso-creds',
          secret_namespace: 'external-secrets',
          state: 'unknown',
          source: 'AWS Secrets Manager',
          last_check_error: "Sharko couldn't connect to this cluster.",
        },
      ],
    })
    renderPage()

    const row = await screen.findByTestId('secret-row-values-prod-eu-eso')
    const dot = within(row).getByTestId('status-dot')
    expect(dot).toHaveAttribute('data-hollow', 'true')
    expect(dot).toHaveAttribute('data-status', 'unknown')

    await user.click(screen.getByRole('button', { name: 'Actions for prod-eu / eso' }))
    const syncItem = await screen.findByRole('menuitem', { name: /Sync/ })
    expect(syncItem).toHaveAttribute('aria-disabled', 'true')
    await user.click(within(syncItem).getByLabelText('Why is Sync unavailable?'))
    await waitFor(() => expect(screen.getByText('Click Refresh first to check this secret.')).toBeInTheDocument())
    await user.keyboard('{Escape}')

    // And the panel shows the failure sentence, same as the connection-row
    // symmetry test above.
    fireEvent.click(row)
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('last-check-error')).toHaveTextContent(
      "The last check failed: Sharko couldn't connect to this cluster.",
    )
  })

  // G3 (gitops-proud P4-G): the rank table got more honest — missing
  // outranks out_of_sync now (nothing exists to Sync onto, a harder stop
  // than a wrong value Sync can overwrite), and foreign keeps its seat
  // right after the two real comparisons.
  it('sorts missing first, then out-of-sync, then foreign, then never-checked (G3 worst-first)', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        { cluster: 'c-unknown', addon: 'a', state: 'unknown', source: 'AWS Secrets Manager' },
        { cluster: 'c-foreign', addon: 'a', state: 'foreign', source: 'AWS Secrets Manager' },
        { cluster: 'c-missing', addon: 'a', state: 'missing', source: 'AWS Secrets Manager' },
        { cluster: 'c-drift', addon: 'a', state: 'out_of_sync', source: 'AWS Secrets Manager' },
      ],
    })
    renderPage()

    await screen.findByTestId('secret-row-values-c-drift-a')
    const order = screen.getAllByTestId(/^secret-row-/).map((r) => r.getAttribute('data-testid'))
    expect(order).toEqual([
      'secret-row-values-c-missing-a',
      'secret-row-values-c-drift-a',
      'secret-row-values-c-foreign-a',
      'secret-row-values-c-unknown-a',
    ])
  })

  // G3: both rows are the exact same "unknown" state word — the ONLY
  // difference between them is whether the last check actually failed
  // (last_check_error set) — and a failed check sorts ahead of a row
  // that's simply never been looked at.
  it('sorts a check-failed row ahead of a genuinely never-checked row — both "unknown" (G3)', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...baseResponse,
      cluster_connection_secrets: [],
      addon_values_secrets: [
        { cluster: 'c-not-checked', addon: 'a', state: 'unknown', source: 'AWS Secrets Manager' },
        {
          cluster: 'c-check-failed',
          addon: 'a',
          state: 'unknown',
          source: 'AWS Secrets Manager',
          last_check_error: "Sharko couldn't connect to this cluster.",
        },
      ],
    })
    renderPage()

    await screen.findByTestId('secret-row-values-c-not-checked-a')
    const order = screen.getAllByTestId(/^secret-row-/).map((r) => r.getAttribute('data-testid'))
    expect(order).toEqual(['secret-row-values-c-check-failed-a', 'secret-row-values-c-not-checked-a'])
  })

  // P1-A A3 — a connection row's Refresh checks every cluster, and the
  // toast says so instead of implying one cluster was singled out.
  it("a connection row's Refresh names the real blast radius in its toast", async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockReconcileCluster.mockResolvedValue({ status: 'accepted', message: 'checking' })
    renderPage()

    await screen.findByTestId('secret-row-connection-staging-us')
    await user.click(screen.getByRole('button', { name: 'Actions for staging-us' }))
    await user.click(await screen.findByRole('menuitem', { name: /Refresh/ }))

    await waitFor(() => expect(mockReconcileCluster).toHaveBeenCalledWith('staging-us'))
    await waitFor(() =>
      expect(mockShowToast).toHaveBeenCalledWith("Checking every cluster's connection secret now, staging-us included.", 'success'),
    )
  })

  it('search narrows the table across both secret kinds', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    const search = screen.getByPlaceholderText('Search by cluster, addon, secret name, or namespace...')
    fireEvent.change(search, { target: { value: 'staging' } })

    await waitFor(() => expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(2))
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()
    expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument()
  })

  it('I2: a long combined list has no pagination controls — every row renders at once under a scrolling, sticky-header table', async () => {
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
    // Every one of the 25 rows is in the DOM at once — no page 2 to click
    // Next into.
    expect(screen.getByTestId('secret-row-connection-cluster-24')).toBeInTheDocument()
    expect(screen.getAllByTestId(/^secret-row-/)).toHaveLength(25)

    // No page-size selector, no Prev/Next, no page-number buttons — the
    // shared PaginationControls/PageSizeSelector widgets are gone from
    // this page (they stay in use elsewhere — AddonCatalog, Clusters,
    // Observability, PullRequestsPanel).
    expect(screen.queryByText('Rows per page:')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Next' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Prev' })).not.toBeInTheDocument()

    // The table lives inside a scrolling frame with a sticky header, not a
    // plain overflow-hidden box.
    const header = screen.getByText('Status').closest('thead')
    expect(header).toHaveClass('sticky')
    // The scroll frame is an ANCESTOR div of the table (possibly not the
    // nearest one — the shared Table component wraps its own <table> in a
    // horizontal-scroll div first), so search up by class rather than
    // assuming a fixed nesting depth.
    const scrollFrame = header?.closest('div.overflow-y-auto')
    expect(scrollFrame).not.toBeNull()
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

  // I3 (gitops-proud P4-I, D2) — the values engine's off switch. `enabled`
  // false is a DIFFERENT state from `wired` false ("not running on this
  // server at all") — an admin turned a real, running engine off, and the
  // strip says so plainly, in the engine's own words, not a generic
  // "not running" line that would suggest something is broken.
  it('says the addon values engine is switched off when an admin has turned it off — and leaves the connection engine alone', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [],
      addon_values_secrets: [],
      engines: {
        cluster_connection: { wired: true, enabled: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
        addon_values: { wired: true, enabled: false, interval_seconds: 300, last_run: '2026-08-04T23:00:00Z' },
      },
      addon_values_secret_source: 'secrets store',
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('engine-off-values')).toHaveTextContent('Addon values engine is switched off.'))
    // The connection engine has no off switch — it still reports its
    // ordinary cadence sentence, never the off sentence.
    expect(screen.queryByTestId('engine-off-connection')).not.toBeInTheDocument()
    expect(screen.getByText(/Sharko last ran a check/)).toBeInTheDocument()
  })

  // P1-A A3 — "Check all now" (renamed from "Refresh all" in the H3 word
  // pass) checks both engines and writes to neither. The second assertion
  // is the one that matters: triggerSecretsReconcile runs the pass that
  // CREATES and ROTATES secrets, so a read-only check must never reach it.
  it('Check all now checks both engines and never fires the values write pass', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockCheckAllAddonValuesSecrets.mockResolvedValue({ status: 'ok' })
    mockReconcileCluster.mockResolvedValue({ status: 'ok', message: 'triggered' })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('refresh-all')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('refresh-all'))

    await waitFor(() => expect(mockCheckAllAddonValuesSecrets).toHaveBeenCalled())
    expect(mockTriggerSecretsReconcile).not.toHaveBeenCalled()
    // Any real cluster name works — the endpoint checks every cluster
    // regardless of which cluster name is in the path.
    expect(mockReconcileCluster).toHaveBeenCalledWith('prod-eu')
  })

  // P1-A A3 — the help text has to describe what the button actually does.
  it('Check all now says plainly that it only checks', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('refresh-all')).toBeInTheDocument())
    fireEvent.click(screen.getByLabelText('What does Check all now do?'))

    await waitFor(() =>
      expect(
        screen.getByText(
          "Asks Sharko to check every secret against its source — connection secrets against git, addon values against your secrets store. Checking only: nothing is written. The engines' own loops do the repairs.",
        ),
      ).toBeInTheDocument(),
    )
  })

  // G4: dark-mode "selected" stopped borrowing StatusMark's teal (which
  // reads as "in sync" next to a row that genuinely is) — it's the house
  // navy in both themes now, same as the pagination controls.
  it('never uses the status-teal colour for a selected state, even in dark mode (G4)', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('group-by-addon')).toBeInTheDocument())
    expect(screen.getByTestId('group-by-none').className).not.toMatch(/teal/)

    await user.click(screen.getByTestId('group-by-addon'))
    expect(screen.getByTestId('group-by-addon').className).not.toMatch(/teal/)

    await user.click(screen.getByTestId('filter-chip-in_sync'))
    expect(screen.getByTestId('filter-chip-in_sync').className).not.toMatch(/teal/)
  })

  // H3 (gitops-proud P4-H): the cadence sentence used to live ONLY in a
  // hover `title` attribute — unreachable by touch or keyboard. It's a
  // visible InfoHint now, next to "Sharko last ran a check…".
  it('the cadence sentence is a visible InfoHint, reachable by click — not hover-only', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    const hint = screen.getByLabelText('How often does Sharko check cluster connections?')
    fireEvent.click(hint)
    await waitFor(() =>
      expect(screen.getByText('Sharko re-checks it every 30 seconds, and right after each merge.')).toBeInTheDocument(),
    )
  })

  // H3: the Sync confirm box used to read like two log-line clauses
  // stitched together. It now opens with the one fact that matters most —
  // this writes, right now, on the real cluster, and there's no PR to
  // review first.
  it('the Sync confirm box writes now, names the real cluster, and says there is no pull request', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Actions for staging-us' }))
    await user.click(await screen.findByRole('menuitem', { name: /Sync/ }))

    await waitFor(() =>
      expect(
        screen.getByText(
          'This writes the secret on cluster "staging-us" now. No pull request. It copies git\'s addon labels onto the cluster\'s ArgoCD secret. The self-heal setting doesn\'t change.',
        ),
      ).toBeInTheDocument(),
    )
  })

  // H4-3: "Sharko's record" — a short list of this row's own recent
  // actions, scoped to its exact resource key so a different cluster's
  // history never leaks in.
  it('lists recent activity for the row under "Sharko\'s record", scoped to just that row', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    mockFetchAuditLog.mockResolvedValue({
      entries: [
        {
          id: '1',
          timestamp: '2026-08-05T00:00:00Z',
          level: 'info',
          event: 'secret.sync',
          user: 'admin',
          action: 'sync',
          resource: 'cluster:staging-us',
          source: 'api',
          result: 'success',
          duration_ms: 12,
          detail: 'Synced the connection secret',
        },
        {
          id: '2',
          timestamp: '2026-08-04T23:00:00Z',
          level: 'info',
          event: 'secret.reconcile',
          user: 'admin',
          action: 'reconcile',
          resource: 'cluster:staging-us',
          source: 'reconciler',
          result: 'success',
          duration_ms: 8,
          detail: 'Reconciled the connection labels',
        },
        {
          id: '3',
          timestamp: '2026-08-04T22:00:00Z',
          level: 'info',
          event: 'secret.sync',
          user: 'admin',
          action: 'sync',
          resource: 'cluster:prod-eu',
          source: 'api',
          result: 'success',
          duration_ms: 9,
          detail: 'Synced a different cluster entirely',
        },
      ],
    })
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-staging-us'))

    const panel = await screen.findByTestId('secret-detail-panel')
    await waitFor(() => expect(mockFetchAuditLog).toHaveBeenCalledWith({ cluster: 'staging-us', limit: 50 }))

    expect(within(panel).getByText("Sharko's record")).toBeInTheDocument()
    const list = await within(panel).findByTestId('detail-recent-activity')
    expect(within(list).getByText('Synced the connection secret')).toBeInTheDocument()
    expect(within(list).getByText('Reconciled the connection labels')).toBeInTheDocument()
    // Scoped to this row's own resource key — a different cluster's entry
    // (which the server's own substring-matching filter would have let
    // through) must not leak in.
    expect(within(list).queryByText('Synced a different cluster entirely')).not.toBeInTheDocument()
    // The honest scope note is visible, not buried.
    expect(within(panel).getByText(/since Sharko last started/)).toBeInTheDocument()
  })
})
