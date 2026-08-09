// SecretDetailPage — SSF-9 (Secret Sync finish pass, stories 9+10). Pins
// specific to the page itself, on top of the detail CONTENT pins already
// covered by ManagedSecrets.panel.test.tsx / .redactedyaml.test.tsx /
// .orphaned.test.tsx (all of which now render this same page):
//
//  - direct load / browser refresh shows a plain loading state first, then
//    the row, fetched from the same store the list uses;
//  - an unknown/missing row key renders a plain "isn't in the current
//    list" state with a way back — never a crash;
//  - "Back to Secret Sync" carries the list's own query string, so its
//    filters/search/sort/group/view come back;
//  - the orphaned-row Delete flow works from the page and returns to the
//    list on success;
//  - `?row=` compat redirect (also pinned from the list side in
//    ManagedSecrets.test.tsx).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation, useSearchParams } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { SecretDetailPage } from '@/views/SecretDetailPage'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

const mockShowToast = vi.fn()
vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) }
})

const mockGetManagedSecrets = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockDeleteOrphanedSecret = vi.fn()
const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => ({
  api: { getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args) },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  deleteOrphanedSecret: (...args: unknown[]) => mockDeleteOrphanedSecret(...args),
  triggerSecretsReconcile: vi.fn(),
  checkAllAddonValuesSecrets: vi.fn(),
  reconcileCluster: vi.fn(),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: vi.fn(),
  syncAddonValuesSecret: vi.fn(),
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

function authFor(role: string) {
  return {
    token: 'test-token',
    username: role,
    role,
    login: vi.fn(),
    logout: vi.fn(),
    isAuthenticated: true,
    isAdmin: role === 'admin',
    loading: false,
    error: null,
  }
}

function LocationProbe() {
  const location = useLocation()
  const [searchParams] = useSearchParams()
  return (
    <div data-testid="location-probe" data-pathname={location.pathname}>
      {searchParams.toString()}
    </div>
  )
}

function renderApp(initialEntries: string[], role = 'operator') {
  return render(
    <AuthContext.Provider value={authFor(role)}>
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

const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    { cluster: 'prod-eu', secret_namespace: 'argocd', secret_name: 'prod-eu', state: 'in_sync', source: 'git', self_heals: true },
    { cluster: 'staging-us', secret_namespace: 'argocd', secret_name: 'staging-us', state: 'out_of_sync', source: 'git', self_heals: false },
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
  ],
  orphaned_secrets: [
    {
      cluster: 'staging-us',
      secret_name: 'eso-creds',
      secret_namespace: 'external-secrets',
      addon: 'eso',
      state: 'orphaned',
      source: 'AWS Secrets Manager',
      last_checked: '2026-08-05T00:00:00Z',
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
  labels: [],
  annotations: [],
  data_keys: [],
  read_from: 'cluster "prod-eu", namespace "datadog"',
  values_blanked: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetManagedSecrets.mockResolvedValue(response)
  mockGetClusterComparison.mockResolvedValue({ cluster: { name: 'prod-eu', labels: {}, last_reconcile: null } })
  mockGetConnectionSecretResource.mockResolvedValue({ ...blankedResource, name: 'prod-eu', namespace: 'argocd' })
  mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
})

describe('direct load and refresh', () => {
  it('shows a plain loading state before the shared fetch resolves, then the row', async () => {
    let resolveFetch!: (value: ManagedSecretsResponse) => void
    mockGetManagedSecrets.mockReturnValue(new Promise((resolve) => { resolveFetch = resolve }))
    renderApp(['/secret-sync/connection-prod-eu'])

    expect(screen.getByText('Loading secret...')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-detail-panel')).not.toBeInTheDocument()

    resolveFetch(response)
    expect(await screen.findByTestId('secret-detail-panel')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'prod-eu connection' })).toBeInTheDocument()
  })

  it('a direct load fetches from the same store the list uses — no new endpoint', async () => {
    renderApp(['/secret-sync/values-prod-eu-datadog'])
    await screen.findByTestId('secret-detail-panel')
    expect(mockGetManagedSecrets).toHaveBeenCalledTimes(1)
  })
})

describe('an unknown or missing row key', () => {
  it('renders a plain "not in the current list" state with a way back — never a crash', async () => {
    renderApp(['/secret-sync/values-does-not-exist-anywhere'])

    expect(await screen.findByText("This secret isn't in the current list")).toBeInTheDocument()
    expect(screen.queryByTestId('secret-detail-panel')).not.toBeInTheDocument()
    const back = screen.getByTestId('secret-detail-not-found-back')
    fireEvent.click(back)
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveAttribute('data-pathname', '/secret-sync'))
  })
})

describe('Back to Secret Sync restores the list exactly as it was', () => {
  it('carries search, status filter, and sort back to the list', async () => {
    // Navigate the way a real reader does: land on the list with a rich
    // query string, click a row, then use the Back link — proving the
    // ENTIRE query string round-trips, not just one param at a time.
    renderApp(['/secret-sync?q=datadog&state=in_sync&sort=name&dir=desc'])
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-values-prod-eu-datadog'))

    await screen.findByTestId('secret-detail-back')
    fireEvent.click(screen.getByTestId('secret-detail-back'))

    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveAttribute('data-pathname', '/secret-sync'))
    // The list is back with the exact same filters — proven by the search
    // box and the pressed chip reading the carried-back state, and the
    // sort header showing the carried-back column/direction.
    await waitFor(() =>
      expect(screen.getByPlaceholderText('Search by cluster, addon, secret name, or namespace...')).toHaveValue('datadog'),
    )
    expect(screen.getByTestId('filter-chip-in_sync')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('location-probe')).toHaveTextContent('sort=name')
    expect(screen.getByTestId('location-probe')).toHaveTextContent('dir=desc')
  })

  it('carries the group-by choice and List/Tiles view back to the list', async () => {
    // staging-us's connection secret is out_of_sync — grouped by addon, it
    // falls into the "not an addon" connections bucket, which has an issue
    // and auto-opens (G4), so the row is reachable without an extra click
    // to expand the group first.
    renderApp(['/secret-sync?group=addon&view=list'])
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-staging-us')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-connection-staging-us'))

    fireEvent.click(await screen.findByTestId('secret-detail-back'))

    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveAttribute('data-pathname', '/secret-sync'))
    expect(await screen.findByTestId('group-by-addon')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('view-list')).toHaveAttribute('aria-pressed', 'true')
  })

  it('a direct load with no listSearch (e.g. a shared link) still has a working Back link to the plain list', async () => {
    renderApp(['/secret-sync/connection-prod-eu'])
    const back = await screen.findByTestId('secret-detail-back')
    fireEvent.click(back)
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveAttribute('data-pathname', '/secret-sync'))
  })
})

describe('deleting an orphaned row from the page', () => {
  it('confirming Delete calls the API, toasts the result, and returns to the list', async () => {
    mockDeleteOrphanedSecret.mockResolvedValue({
      status: 'deleted',
      cluster: 'staging-us',
      namespace: 'external-secrets',
      name: 'eso-creds',
      message: 'Deleted secret "external-secrets/eso-creds" from cluster "staging-us".',
    })
    renderApp(['/secret-sync/orphaned-staging-us-external-secrets-eso-creds'])

    const panel = await screen.findByTestId('secret-detail-panel')
    fireEvent.click(within(panel).getByTestId('detail-delete'))
    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(mockDeleteOrphanedSecret).toHaveBeenCalledWith('staging-us', 'external-secrets', 'eso-creds'))
    await waitFor(() =>
      expect(mockShowToast).toHaveBeenCalledWith('Deleted secret "external-secrets/eso-creds" from cluster "staging-us".', 'success'),
    )
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveAttribute('data-pathname', '/secret-sync'))
  })

  it('cancel does nothing — no API call, the page stays put', async () => {
    renderApp(['/secret-sync/orphaned-staging-us-external-secrets-eso-creds'])

    const panel = await screen.findByTestId('secret-detail-panel')
    fireEvent.click(within(panel).getByTestId('detail-delete'))
    await screen.findByText(/This permanently deletes secret/)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await waitFor(() => expect(screen.queryByText(/This permanently deletes secret/)).not.toBeInTheDocument())
    expect(mockDeleteOrphanedSecret).not.toHaveBeenCalled()
    expect(screen.getByTestId('secret-detail-panel')).toBeInTheDocument()
  })
})
