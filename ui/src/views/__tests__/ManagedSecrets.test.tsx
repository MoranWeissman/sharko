// ManagedSecrets — the /secrets page (S1-S3).
//
// Covers: rows render as the resource they are (real name/namespace,
// purpose, and source per kind), the two kinds name different sources
// (S3(a)), greyed actions carry a reason, a values-secret Diff never
// renders secret content (S3(b)) — only match/no-match wording — and
// search/filter/pagination still work.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

// Refresh/Sync are role-gated (admin/operator only) — provide an admin
// auth context so RoleGuard renders them; every test here cares about
// those buttons existing, not about the role gate itself (that's covered
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
  it('renders each row as the resource it is — real name/namespace, purpose, and its own source', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByText('argocd/prod-eu')).toBeInTheDocument())

    // Connection secret: identity, purpose, and source against git.
    expect(screen.getAllByText(/Connects/).length).toBe(2)
    expect(screen.getAllByText('Compared against git.').length).toBe(2)

    // Addon-values secret: identity, purpose, and source against the vault
    // — a DIFFERENT sentence than the connection row's, never the same
    // generic "out of sync" phrase for both kinds (S3(a)).
    expect(screen.getAllByText('datadog/datadog-secrets').length).toBe(2)
    expect(screen.getAllByText(/Carries values for addon/).length).toBe(2)
    expect(screen.getAllByText('Compared against the vault — git only holds a pointer to it.').length).toBe(2)
  })

  it('greys the connection Sync button with a reason when the secret already matches git', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('connection-sync-prod-eu')).toBeInTheDocument())

    // prod-eu is in_sync — Sync must be disabled with a reason attached
    // right next to it.
    const prodSyncBtn = screen.getByTestId('connection-sync-prod-eu')
    expect(prodSyncBtn).toBeDisabled()
    expect(within(prodSyncBtn.parentElement as HTMLElement).getByLabelText('Why is Sync unavailable?')).toBeInTheDocument()

    // staging-us is out_of_sync — Sync must be enabled.
    expect(screen.getByTestId('connection-sync-staging-us')).not.toBeDisabled()
  })

  it('greys the values Sync button with a reason when nothing needs pushing', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('values-sync-prod-eu-datadog')).toBeInTheDocument())

    expect(screen.getByTestId('values-sync-prod-eu-datadog')).toBeDisabled()
    expect(screen.getByTestId('values-sync-staging-us-datadog')).not.toBeDisabled()
  })

  it('a values-secret Diff never renders secret content — only match/no-match wording', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('values-diff-prod-eu-datadog')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('values-diff-prod-eu-datadog'))

    const panel = await screen.findByTestId('values-diff-panel-prod-eu-datadog')
    expect(within(panel).getByText('Matches its source.')).toBeInTheDocument()
    // Never a live fetch for a values-secret Diff — no content to leak.
    expect(mockGetClusterComparison).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('values-diff-staging-us-datadog'))
    const outOfSyncPanel = await screen.findByTestId('values-diff-panel-staging-us-datadog')
    expect(within(outOfSyncPanel).getByText(/Does not match its source right now/)).toBeInTheDocument()
  })

  it('a connection-secret Diff fetches and shows the label diff (no credentials, existing behavior)', async () => {
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

    await waitFor(() => expect(screen.getByTestId('connection-diff-staging-us')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('connection-diff-staging-us'))

    await waitFor(() => expect(mockGetClusterComparison).toHaveBeenCalledWith('staging-us'))
    const panel = await screen.findByTestId('connection-diff-panel-staging-us')
    expect(within(panel).getByText(/Missing 1 addon label/)).toBeInTheDocument()
    expect(within(panel).getByText('datadog')).toBeInTheDocument()
  })

  it('search narrows the connection-secrets section', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('connection-identity-prod-eu')).toBeInTheDocument())
    const search = screen.getByPlaceholderText('Search by cluster or secret name...')
    fireEvent.change(search, { target: { value: 'staging' } })

    expect(screen.queryByTestId('connection-identity-prod-eu')).not.toBeInTheDocument()
    expect(screen.getByTestId('connection-identity-staging-us')).toBeInTheDocument()
  })

  it('the state filter narrows the addon-values section', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('values-identity-prod-eu-datadog')).toBeInTheDocument())
    const stateFilters = screen.getAllByLabelText('State')
    // Second "State" select belongs to the addon-values section.
    fireEvent.change(stateFilters[1], { target: { value: 'out_of_sync' } })

    expect(screen.queryByTestId('values-diff-prod-eu-datadog')).not.toBeInTheDocument()
    expect(screen.getByTestId('values-diff-staging-us-datadog')).toBeInTheDocument()
  })

  it('paginates a long connection-secrets list', async () => {
    const manyRows = Array.from({ length: 12 }, (_, i) => ({
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

    await waitFor(() => expect(screen.getByTestId('connection-diff-cluster-00')).toBeInTheDocument())
    expect(screen.queryByTestId('connection-diff-cluster-11')).not.toBeInTheDocument()

    fireEvent.click(screen.getAllByRole('button', { name: 'Next' })[0])

    await waitFor(() => expect(screen.getByTestId('connection-diff-cluster-11')).toBeInTheDocument())
    expect(screen.queryByTestId('connection-diff-cluster-00')).not.toBeInTheDocument()
  })

  it('clicking a row navigates to its cluster/addon page', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse)
    renderPage()

    await waitFor(() => expect(screen.getByTestId('connection-identity-prod-eu')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('connection-identity-prod-eu'))
    expect(mockNavigate).toHaveBeenCalledWith('/clusters/prod-eu')

    fireEvent.click(screen.getByTestId('values-identity-prod-eu-datadog'))
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
})
