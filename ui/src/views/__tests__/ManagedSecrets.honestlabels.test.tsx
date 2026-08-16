// ManagedSecrets.honestlabels.test.tsx — the honest-labels epic (HL-1/HL-2/HL-4).
//
// HL-1: the cluster connection action calls resyncClusterLabels, which
// re-applies ONLY Sharko's own addon label keys — it does not rebuild
// config, server or name. So on connection rows the action reads
// "Sync addon labels" (renamed from "Re-apply addon labels" by the Round 3
// walkthrough ruling, 2026-08-16), while addon Secrets keep "Sync" (their
// value really is delivered from a backend).
//
// Round 3 ruling (2026-08-16) supersedes the old blanket "the word 'Repair'
// renders nowhere" rule below: the product owner has since named the real
// repair action "Repair connection", and it does render on these pages when
// repair is available. The describe block below is rescoped accordingly —
// see its own comment.
//
// HL-2: on the canonical subpages (/secrets/connections, /secrets/addons)
// the route already says which kind shows — a leftover ?kind= from an old
// link is ignored and removed from the URL. Before this fix,
// /secrets/connections?kind=values rendered an empty list because the
// pre-split filter argued with the route.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
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
  },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
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

// Both rows are broken on purpose: a drifted row is the one that renders
// the action button, the promise note under the conclusion, and the
// "Waiting for …" sentence — the places the old wording lived.
const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    {
      cluster: 'prod-eu',
      secret_namespace: 'argocd',
      secret_name: 'prod-eu',
      state: 'out_of_sync',
      source: 'git',
      last_checked: '2026-08-05T00:00:00Z',
      self_heals: false,
    },
  ],
  addon_values_secrets: [
    {
      cluster: 'prod-eu',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'out_of_sync',
      source: 'AWS Secrets Manager',
      last_checked: '2026-08-05T00:00:00Z',
      self_heals: false,
    },
  ],
  orphaned_secrets: [],
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

// Renders the live URL's search string so a test can assert what the page
// did to it — MemoryRouter has no address bar to look at.
function LocationProbe() {
  const location = useLocation()
  return <span data-testid="location-search">{location.search}</span>
}

function renderApp(initialEntries: string[]) {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={initialEntries}>
        <LocationProbe />
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

describe('HL-1 — the connection action is named for what it does', () => {
  it('the connection detail page action reads "Sync addon labels", and its confirm carries the same name', async () => {
    const user = userEvent.setup()
    renderApp(['/secrets/connections/prod-eu'])

    const syncButton = await screen.findByTestId('detail-sync')
    expect(syncButton).toHaveTextContent('Sync addon labels')
    expect(syncButton).not.toHaveTextContent(/^Sync$/)

    // The confirm box: real name in the title, real name on the button,
    // and a description that only promises the labels. Round 3
    // (2026-08-16) made the confirm button's own text "Sync addon labels"
    // too — the SAME words as the trigger button, which stays mounted
    // behind the dialog — so the confirm button is found scoped to the
    // dialog, not by a page-wide role query.
    await user.click(syncButton)
    expect(await screen.findByText('Sync addon labels on "prod-eu"?')).toBeInTheDocument()
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('button', { name: /^Sync addon labels$/ })).toBeInTheDocument()
    expect(screen.queryByText(/Sync cluster/)).not.toBeInTheDocument()
  })

  it('the addon Secret detail page action still reads exactly "Sync" — untouched', async () => {
    renderApp(['/secrets/addons/prod-eu/datadog'])

    const syncButton = await screen.findByTestId('detail-sync')
    expect(syncButton).toHaveTextContent('Sync')
    expect(syncButton).not.toHaveTextContent('Re-apply')
  })

  it('the connection row menu action reads "Sync addon labels" while the addon row menu keeps "Sync"', async () => {
    const user = userEvent.setup()
    const conn = renderApp(['/secrets/connections'])
    await screen.findByTestId('secret-row-connection-prod-eu')

    await user.click(screen.getByRole('button', { name: 'Actions for prod-eu' }))
    expect(await screen.findByRole('menuitem', { name: /Sync addon labels/ })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /^Sync$/ })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    conn.unmount()

    renderApp(['/secrets/addons'])
    await screen.findByTestId('secret-row-values-prod-eu-datadog')
    await user.click(screen.getByRole('button', { name: 'Actions for prod-eu / datadog' }))
    const items = await screen.findAllByRole('menuitem')
    const labels = items.map((i) => i.textContent ?? '')
    expect(labels.some((t) => t.includes('Sync'))).toBe(true)
    expect(labels.some((t) => t.includes('Re-apply'))).toBe(false)
    // Round 3 (2026-08-16): the addon row's plain "Sync" menu item must
    // never read as the connection-only "Sync addon labels" either.
    expect(labels.some((t) => t.includes('addon labels'))).toBe(false)
  })

  it('no connection sentence promises the cluster copy will match Git, and the addon promise still names its store', async () => {
    const conn = renderApp(['/secrets/connections/prod-eu'])
    const note = await screen.findByTestId('detail-repair-note')
    expect(note).toHaveTextContent("Sync addon labels puts git's addon labels back on this secret.")
    expect(note).not.toHaveTextContent('match Git')
    // The "who fixes this" sentence points at the renamed action.
    expect(screen.getByTestId('detail-self-heals')).toHaveTextContent('Waiting for Sync addon labels.')
    conn.unmount()

    renderApp(['/secrets/addons/prod-eu/datadog'])
    const addonNote = await screen.findByTestId('detail-repair-note')
    expect(addonNote).toHaveTextContent('Sync will update the cluster copy to match AWS Secrets Manager.')
  })
})

// Round 3 ruling (2026-08-16) supersedes the blanket "Repair renders
// nowhere" rule this block used to enforce: the product owner has since
// named the real connection-repair action "Repair connection", and it DOES
// render on the connection detail page once repair_available is true (see
// ManagedSecrets.panel.test.tsx's "Connection repair" describe for that
// coverage). This file's own mock always returns repair_available: false,
// so what's still true and still worth pinning here is narrower: the
// addon-labels action never says "Repair" (it's a labels-only action, never
// a repair), and while repair is genuinely unavailable, no "Repair" text
// leaks onto the page.
describe('HL-1 / HL-4 — the addon-labels action never claims to "Repair", and no Repair text renders while repair is unavailable', () => {
  it('never renders "Repair" on the connections inventory, the addons inventory, or the connection detail page (repair_available: false)', async () => {
    const conn = renderApp(['/secrets/connections'])
    await screen.findByTestId('secret-row-connection-prod-eu')
    expect(screen.queryByText(/Repair/)).not.toBeInTheDocument()
    conn.unmount()

    const addons = renderApp(['/secrets/addons'])
    await screen.findByTestId('secret-row-values-prod-eu-datadog')
    expect(screen.queryByText(/Repair/)).not.toBeInTheDocument()
    addons.unmount()

    // The detail page of a BROKEN connection row — the place a "Repair"
    // label would be most tempting to put, and the sync (addon-labels)
    // action itself must never say it either.
    renderApp(['/secrets/connections/prod-eu'])
    const syncButton = await screen.findByTestId('detail-sync')
    expect(syncButton).not.toHaveTextContent(/Repair/)
    expect(screen.queryByText(/Repair/)).not.toBeInTheDocument()
  })
})

describe('HL-2 — the canonical routes drop the old ?kind= filter', () => {
  it('/secrets/connections?kind=values shows the connection rows, not an empty list, and removes the param from the URL', async () => {
    renderApp(['/secrets/connections?kind=values'])

    // Before the fix this rendered an empty list: the route scoped rows to
    // connections, then the leftover filter threw them all away.
    expect(await screen.findByTestId('secret-row-connection-prod-eu')).toBeInTheDocument()

    // And the losing param is gone from the URL.
    await waitFor(() => expect(screen.getByTestId('location-search')).not.toHaveTextContent('kind='))
  })

  it('other params survive the ?kind= removal', async () => {
    renderApp(['/secrets/addons?kind=connection&state=out_of_sync'])

    expect(await screen.findByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('location-search')).not.toHaveTextContent('kind='))
    expect(screen.getByTestId('location-search')).toHaveTextContent('state=out_of_sync')
  })
})
