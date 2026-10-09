// ManagedSecrets.areas.test.tsx — Secrets-area rename (SN-3/SN-4/SN-7).
//
// The Secrets area is two real subpages rendered by the same inventory
// implementation, scoped by the `area` prop the routes pass:
//
//   /secrets/connections → only cluster connection rows, its own title,
//     its own description, only the cluster-connection engine.
//   /secrets/addons → only addon rows (leftover "orphaned" rows fold in
//     as before), its own title/description, only the addon-values engine.
//
// Plus the subnav between them (real links, aria-current, wrapped in
// <nav aria-label="Secrets">), the two detail routes loaded directly,
// Back/Forward between subpages, and the "no retired names" sweep.
//
// The pre-split unified behavior is pinned by the untouched existing
// suites (ManagedSecrets.test.tsx and friends), which mount the component
// without `area` — that is the proof shared functionality did not change.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { SecretDetailPage } from '@/views/SecretDetailPage'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

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

const mockShowToast = vi.fn()
import { withCanonicalConnectionRows } from './connectionRowCanonical'
import { CONNECTION_SENTENCES } from '@/generated/connection-sentences'

vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) }
})

const mockGetManagedSecrets = vi.fn()
const mockCheckAllAddonValuesSecrets = vi.fn()
const mockReconcileCluster = vi.fn()
const mockResyncClusterLabels = vi.fn()
const mockRefreshAddonValuesSecret = vi.fn()
const mockSyncAddonValuesSecret = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockFetchAuditLog = vi.fn()
const mockDeleteOrphanedSecret = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
    getConnectionComparison: () => Promise.resolve({ cluster: "test-cluster", status: "synced", scope: "full", ownership_mode: "sharko_managed", checked_at: "2026-08-13T12:00:00Z", branch: "main", differences: [], not_checked: [], checked_field_count: 10, repair_available: false, repair_scope: "none", values_never_returned: true }),
    getConnectionReconciliation: () => Promise.resolve({
      cluster: 'prod-eu',
      management_mode: 'sharko_managed',
      managed_scope: 'full_connection',
      mode_statement: CONNECTION_SENTENCES.modeStatementSharkoManaged,
      definition: { file: 'configuration/managed-clusters.yaml', branch: 'main', desired_revision: 'abcdef1234567890abcdef1234567890abcdef12', credential_source_type: 'secret-kubeconfig' },
      sync: { state: 'synced', verification_scope: 'full', approval_required: false, checked_at: '2026-08-13T12:00:00Z' },
      health: { state: 'connected' },
      conditions: [
        { id: 'git_definition', status: 'ok', detail: CONNECTION_SENTENCES.condGitDefinitionOK },
        { id: 'argocd_connection', status: 'ok', detail: CONNECTION_SENTENCES.condArgoCDConnected },
      ],
      drift: { connection_configuration: [], credential_material: [], addon_labels: [], not_checked: [] },
      plan: { action: 'none', action_scopes: [] },
      values_never_returned: true,
    }),
  },
  // TakeoverDialog's own imports — inert here.
  takeoverPreflight: vi.fn(),
  takeoverCluster: vi.fn(),
  dropLegacyLabels: vi.fn(),
  getManagedSecrets: async (...args: unknown[]) =>
    // B5: every fixture in this file goes through the canonical mapping, so
    // its connection rows carry what a real server now sends (sync_state,
    // verification_scope, headline, health, ...). A fixture that states any
    // of those itself is left untouched — see connectionRowCanonical.ts.
    withCanonicalConnectionRows(await mockGetManagedSecrets(...args)),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  checkAllAddonValuesSecrets: (...args: unknown[]) => mockCheckAllAddonValuesSecrets(...args),
  reconcileCluster: (...args: unknown[]) => mockReconcileCluster(...args),
  resyncClusterLabels: (...args: unknown[]) => mockResyncClusterLabels(...args),
  refreshAddonValuesSecret: (...args: unknown[]) => mockRefreshAddonValuesSecret(...args),
  syncAddonValuesSecret: (...args: unknown[]) => mockSyncAddonValuesSecret(...args),
  deleteOrphanedSecret: (...args: unknown[]) => mockDeleteOrphanedSecret(...args),
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    {
      cluster: 'prod-eu',
      secret_namespace: 'argocd',
      secret_name: 'prod-eu',
      state: 'in_sync',
      source: 'git',
      last_checked: '2026-08-05T00:00:00Z',
      self_heals: true,
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
  labels: [{ key: 'app.kubernetes.io/managed-by', value: 'sharko' }],
  annotations: [],
  data_keys: [{ key: 'api-key', value: '••••••••' }],
  read_from: 'cluster "prod-eu", namespace "datadog"',
  values_blanked: true,
}

// A helper the Back/Forward test drives — MemoryRouter has no browser
// chrome, so the history moves are real navigate(-1)/navigate(1) calls,
// which is exactly what the browser buttons do.
function HistoryProbe() {
  const navigate = useNavigate()
  return (
    <div>
      <button type="button" onClick={() => navigate(-1)} data-testid="history-back">
        back
      </button>
      <button type="button" onClick={() => navigate(1)} data-testid="history-forward">
        forward
      </button>
    </div>
  )
}

function renderApp(initialEntries: string[]) {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={initialEntries}>
        <HistoryProbe />
        <Routes>
          <Route path="/secrets/connections" element={<ManagedSecrets area="connections" />} />
          <Route path="/secrets/connections/:cluster" element={<SecretDetailPage />} />
          <Route path="/secrets/addons" element={<ManagedSecrets area="addons" />} />
          <Route path="/secrets/addons/:cluster/:addon" element={<SecretDetailPage />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetManagedSecrets.mockResolvedValue(response)
  mockGetClusterComparison.mockResolvedValue({ cluster: { name: 'prod-eu', labels: {}, last_reconcile: null } })
  mockGetConnectionSecretResource.mockResolvedValue({ ...blankedResource, name: 'prod-eu', namespace: 'argocd' })
  mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
  window.sessionStorage.clear()
})

describe('the two inventories are separate subpages (SN-3)', () => {
  it('Cluster connections: its own title and description, only connection rows, only its own engine', async () => {
    renderApp(['/secrets/connections'])

    expect(await screen.findByRole('heading', { level: 1, name: 'Cluster connections' })).toBeInTheDocument()
    // B5: the product owner's exact replacement subtitle. The old sentence
    // described the mechanism; the locked model is that Git defines the
    // connection and Sharko maintains the resulting Secret.
    expect(screen.getByText('Git-defined cluster connections Sharko maintains for Argo CD.')).toBeInTheDocument()
    expect(screen.queryByText('Secrets Sharko uses to register clusters with Argo CD.')).not.toBeInTheDocument()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    // No addon rows and no leftover rows on this subpage.
    expect(screen.queryByTestId('secret-row-values-prod-eu-datadog')).not.toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')).not.toBeInTheDocument()
    // Only its own engine in the strip.
    expect(screen.queryByText('Addon values')).not.toBeInTheDocument()
    // The honest footer counts only this subpage's rows.
    expect(screen.getByTestId('secrets-summary')).toHaveTextContent('1 secret')
  })

  it('Addon secrets: its own title and description, addon rows plus leftover rows, only its own engine', async () => {
    renderApp(['/secrets/addons'])

    expect(await screen.findByRole('heading', { level: 1, name: 'Addon secrets' })).toBeInTheDocument()
    expect(
      screen.getByText('Secrets Sharko delivers from configured backends to addons on remote clusters.'),
    ).toBeInTheDocument()

    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    // Leftover ("orphaned") rows belong here, exactly as before the split.
    expect(screen.getByTestId('secret-row-orphaned-staging-us-external-secrets-eso-creds')).toBeInTheDocument()
    // No connection rows on this subpage.
    expect(screen.queryByTestId('secret-row-connection-prod-eu')).not.toBeInTheDocument()
    // Only its own engine in the strip: the only "Cluster connections"
    // text left on this page is the subnav link, not an engine label.
    expect(screen.getByText('Addon values')).toBeInTheDocument()
    const connMentions = screen.getAllByText('Cluster connections')
    expect(connMentions).toHaveLength(1)
    expect(connMentions[0].closest('a')).toHaveAttribute('href', '/secrets/connections')
    expect(screen.getByTestId('secrets-summary')).toHaveTextContent('2 secrets')
  })

  it('never renders the retired names as headings or labels on either subpage', async () => {
    const first = renderApp(['/secrets/connections'])
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    expect(screen.queryByText(/Secret Sync/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Managed Secrets/)).not.toBeInTheDocument()
    first.unmount()

    renderApp(['/secrets/addons'])
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    expect(screen.queryByText(/Secret Sync/)).not.toBeInTheDocument()
    expect(screen.queryByText(/Managed Secrets/)).not.toBeInTheDocument()
  })
})

describe('the subnav between the subpages (SN-3)', () => {
  it('is real links inside <nav aria-label="Secrets">, with aria-current on the current one — not a tablist', async () => {
    renderApp(['/secrets/connections'])
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    const nav = screen.getByRole('navigation', { name: 'Secrets' })
    const connLink = within(nav).getByRole('link', { name: 'Cluster connections' })
    const addonLink = within(nav).getByRole('link', { name: 'Addon secrets' })
    expect(connLink).toHaveAttribute('href', '/secrets/connections')
    expect(addonLink).toHaveAttribute('href', '/secrets/addons')
    expect(connLink).toHaveAttribute('aria-current', 'page')
    expect(addonLink).not.toHaveAttribute('aria-current')
    // Links, not tabs: no ARIA tablist semantics anywhere in the subnav.
    expect(within(nav).queryByRole('tablist')).not.toBeInTheDocument()
    expect(within(nav).queryByRole('tab')).not.toBeInTheDocument()
    // 390px rule: the strip wraps inside itself rather than widening the
    // page (the real no-sideways-scroll measurement runs in a browser —
    // this pins the structure that makes it hold).
    expect(nav.className).toContain('flex-wrap')
  })

  it('clicking the other subnav item switches inventories, and Back/Forward walk between the two URLs', async () => {
    const user = userEvent.setup()
    renderApp(['/secrets/connections'])
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    await user.click(screen.getByRole('link', { name: 'Addon secrets' }))
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    expect(screen.getByRole('heading', { level: 1, name: 'Addon secrets' })).toBeInTheDocument()

    // Browser Back → Cluster connections again.
    await user.click(screen.getByTestId('history-back'))
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    expect(screen.getByRole('heading', { level: 1, name: 'Cluster connections' })).toBeInTheDocument()

    // Browser Forward → Addon secrets again.
    await user.click(screen.getByTestId('history-forward'))
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    expect(screen.getByRole('heading', { level: 1, name: 'Addon secrets' })).toBeInTheDocument()
  })

  it('does not offer "group by addon" on Cluster connections — nothing there is an addon', async () => {
    renderApp(['/secrets/connections'])
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())
    expect(screen.queryByTestId('group-by-addon')).not.toBeInTheDocument()
    expect(screen.getByTestId('group-by-cluster')).toBeInTheDocument()

    // Addon secrets keeps all three grouping choices.
    const addons = renderApp(['/secrets/addons'])
    await waitFor(() => expect(addons.getByTestId('group-by-addon')).toBeInTheDocument())
  })
})

describe('the detail routes load directly (SN-4)', () => {
  it('/secrets/connections/:cluster renders that connection Secret with "Back to Cluster connections"', async () => {
    renderApp(['/secrets/connections/prod-eu'])

    expect(await screen.findByRole('heading', { name: 'prod-eu connection' })).toBeInTheDocument()
    expect(screen.getByTestId('secret-detail-back')).toHaveTextContent('Back to Cluster connections')
    expect(screen.getByTestId('secret-detail-back')).toHaveAttribute('href', '/secrets/connections')
  })

  it('/secrets/addons/:cluster/:addon renders that addon Secret with "Back to Addon secrets"', async () => {
    renderApp(['/secrets/addons/prod-eu/datadog'])

    expect(await screen.findByRole('heading', { name: 'datadog values on prod-eu' })).toBeInTheDocument()
    expect(screen.getByTestId('secret-detail-back')).toHaveTextContent('Back to Addon secrets')
    expect(screen.getByTestId('secret-detail-back')).toHaveAttribute('href', '/secrets/addons')
  })

  it('a leftover Secret resolves by its namespace/name segment on the addons detail route', async () => {
    renderApp(['/secrets/addons/staging-us/external-secrets%2Feso-creds'])

    // The leftover row exists (not the not-found state), under the addons
    // back link.
    await waitFor(() =>
      expect(screen.queryByText("This secret isn't in the current list")).not.toBeInTheDocument(),
    )
    expect(await screen.findByTestId('secret-detail-container')).toBeInTheDocument()
    expect(screen.getByTestId('secret-detail-back')).toHaveTextContent('Back to Addon secrets')
  })

  it('an unknown cluster stays a calm not-found state with a way back — never a crash', async () => {
    renderApp(['/secrets/connections/no-such-cluster'])

    expect(await screen.findByText("This secret isn't in the current list")).toBeInTheDocument()
    expect(screen.getByTestId('secret-detail-not-found-back')).toHaveTextContent('Back to Cluster connections')
  })

  it('an unknown addon on a real cluster is a calm not-found state too', async () => {
    renderApp(['/secrets/addons/prod-eu/no-such-addon'])

    expect(await screen.findByText("This secret isn't in the current list")).toBeInTheDocument()
    expect(screen.getByTestId('secret-detail-not-found-back')).toHaveTextContent('Back to Addon secrets')
  })

  it('opening a row from an inventory navigates to its detail route and Back restores the list query string', async () => {
    const user = userEvent.setup()
    renderApp(['/secrets/addons?state=in_sync'])
    const row = await screen.findByTestId('secret-row-values-prod-eu-datadog')

    await user.click(row)
    expect(await screen.findByRole('heading', { name: 'datadog values on prod-eu' })).toBeInTheDocument()
    // The back link carries the list's own query string home.
    expect(screen.getByTestId('secret-detail-back')).toHaveAttribute('href', '/secrets/addons?state=in_sync')
  })
})
