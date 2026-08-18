// ManagedSecrets.redactedyaml — SSF-5 (Secret Sync finish pass). What this
// suite holds down:
//
//  - the tab renders the SAME live read the Overview tab already fires —
//    no second request;
//  - every data value in it is the server's own fixed mask, never
//    anything derived from a real value;
//  - a visible sentence states Sharko only shows the safe fields it reads;
//  - the words "exact live manifest" never appear;
//  - there is no reveal control and no PER-KEY copy control — only one
//    copy control for the whole block;
//  - a row the panel never fires a live read for (missing, orphaned) shows
//    a calm sentence instead of a fabricated document;
//  - a viewer without operator/admin access sees the same access sentence
//    the Overview tab's live card already shows, not the real fields.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { SecretDetailPage } from '@/views/SecretDetailPage'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

// SSF-9: real react-router-dom, no useNavigate mock — a row click now
// navigates to its own full page (SecretDetailPage) instead of opening a
// drawer in place. See renderPage.

import { withCanonicalConnectionRows } from './connectionRowCanonical'

vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: vi.fn() }
})

const mockGetManagedSecrets = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
    getConnectionComparison: () => Promise.resolve({ cluster: "test-cluster", status: "synced", scope: "full", ownership_mode: "sharko_managed", checked_at: "2026-08-13T12:00:00Z", branch: "main", differences: [], not_checked: [], checked_field_count: 10, repair_available: false, repair_scope: "none", values_never_returned: true }),
    getConnectionReconciliation: () => Promise.resolve({
      cluster: 'prod-eu',
      management_mode: 'sharko_managed',
      managed_scope: 'full_connection',
      mode_statement: 'Git defines the connection. Sharko resolves its credential references and maintains the resulting ArgoCD Secret.',
      definition: { file: 'configuration/managed-clusters.yaml', branch: 'main', desired_revision: 'abcdef1234567890abcdef1234567890abcdef12', credential_source_type: 'secret-kubeconfig' },
      sync: { state: 'synced', verification_scope: 'full', approval_required: false, checked_at: '2026-08-13T12:00:00Z' },
      health: { state: 'connected' },
      conditions: [
        { id: 'git_definition', status: 'ok', detail: 'The connection definition was read from git.' },
        { id: 'argocd_connection', status: 'ok', detail: 'ArgoCD reports this connection as working.' },
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

function renderPage(role = 'operator', initialEntries: string[] = ['/secret-sync']) {
  return render(
    <AuthContext.Provider value={authFor(role)}>
      <MemoryRouter initialEntries={initialEntries}>
        <Routes>
          <Route path="/secret-sync" element={<ManagedSecrets />} />
          <Route path="/secret-sync/:rowKey" element={<SecretDetailPage />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [],
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
      cluster: 'spoke-asia',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'missing',
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

// A "real value" a broken implementation might leak — never expected to
// appear anywhere in the rendered YAML text.
const REAL_LOOKING_VALUE = 'sk-live-not-a-real-secret-but-should-never-render'

const blankedResource = {
  kind: 'Secret',
  api_version: 'v1',
  name: 'datadog-secrets',
  namespace: 'datadog',
  secret_type: 'Opaque',
  created_at: '2026-07-01T00:00:00Z',
  labels: [{ key: 'app.kubernetes.io/managed-by', value: 'sharko' }],
  annotations: [
    { key: 'sharko.dev/source', value: 'AWS Secrets Manager' },
    { key: 'kubectl.kubernetes.io/last-applied-configuration', value: '••••••••', blanked: true },
  ],
  data_keys: [
    { key: 'api-key', value: '••••••••', path: 'secrets/datadog/api-key', present: true },
    { key: 'app-key', value: '••••••••', path: 'secrets/datadog/app-key', present: false },
  ],
  read_from: 'cluster "prod-eu", namespace "datadog"',
  values_blanked: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetManagedSecrets.mockResolvedValue(response)
  mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
  mockGetConnectionSecretResource.mockResolvedValue(blankedResource)
  mockGetClusterComparison.mockResolvedValue({ cluster: { name: 'prod-eu', labels: {}, last_reconcile: null } })
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
})

async function openRowOnYamlTab(key: string) {
  await waitFor(() => expect(screen.getByTestId(`secret-row-${key}`)).toBeInTheDocument())
  fireEvent.click(screen.getByTestId(`secret-row-${key}`))
  const panel = await screen.findByTestId('secret-detail-panel')
  fireEvent.click(within(panel).getByTestId('detail-tab-yaml'))
  return panel
}

describe('SSF-5 — Redacted YAML', () => {
  it('renders the SAME live read the Overview tab already fires — no second request', async () => {
    renderPage()
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    await waitFor(() => expect(within(panel).getByTestId('detail-yaml-content')).toBeInTheDocument())
    // One call total for this row, whether Overview or the YAML tab reads it.
    expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1)
  })

  it('never renders a real value — every data key is the fixed mask', async () => {
    renderPage()
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    const content = await within(panel).findByTestId('detail-yaml-content')
    expect(content).toHaveTextContent('api-key: ••••••••')
    expect(content).toHaveTextContent('app-key: ••••••••')
    expect(content.textContent).not.toContain(REAL_LOOKING_VALUE)
  })

  it('states plainly that only the safe fields Sharko reads are shown', async () => {
    renderPage()
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    const scope = within(panel).getByTestId('detail-yaml-scope')
    expect(scope.textContent).toMatch(/only.*fields Sharko reads/i)
  })

  it('never calls this the exact live manifest', async () => {
    renderPage()
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    await waitFor(() => expect(within(panel).getByTestId('detail-yaml-content')).toBeInTheDocument())
    expect(panel.textContent).not.toMatch(/exact live manifest/i)
  })

  it('has no reveal control and no per-key copy control — one copy control for the whole block only', async () => {
    renderPage()
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    await waitFor(() => expect(within(panel).getByTestId('detail-yaml-content')).toBeInTheDocument())
    expect(within(panel).queryAllByText(/reveal/i)).toHaveLength(0)
    // Exactly one copy affordance in the whole panel while on the YAML tab.
    expect(within(panel).getAllByTestId('detail-yaml-copy')).toHaveLength(1)
  })

  it('copies the whole redacted block, never a per-key value', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    renderPage()
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    await waitFor(() => expect(within(panel).getByTestId('detail-yaml-content')).toBeInTheDocument())
    fireEvent.click(within(panel).getByTestId('detail-yaml-copy'))
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    const copied = writeText.mock.calls[0][0] as string
    expect(copied).toContain('kind: Secret')
    expect(copied).toContain('api-key: ••••••••')
    expect(copied).not.toContain(REAL_LOOKING_VALUE)
  })

  it('shows a calm sentence, not a fabricated document, for a row the panel never reads live (missing)', async () => {
    renderPage()
    const panel = await openRowOnYamlTab('values-spoke-asia-datadog')
    expect(within(panel).queryByTestId('detail-yaml-content')).not.toBeInTheDocument()
    expect(within(panel).getByText('Nothing is there — this secret has not been created yet.')).toBeInTheDocument()
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
  })

  it('gates the tab behind the same operator/admin access the Overview live card uses', async () => {
    renderPage('viewer')
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    expect(within(panel).getByTestId('yaml-needs-operator')).toBeInTheDocument()
    expect(within(panel).queryByTestId('detail-yaml-content')).not.toBeInTheDocument()
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
  })

  // SSF-9: a different row is its own page/mount now, so "the tab choice
  // doesn't carry over" is proven by loading a SECOND row's page fresh
  // (rather than clicking within a still-open drawer) and finding it back
  // on Overview, same as the first row was before its own tab was clicked.
  it('a different row\'s page opens on the Overview tab, never carrying over the previous row\'s YAML choice', async () => {
    const firstRender = renderPage('operator', ['/secret-sync/values-prod-eu-datadog'])
    const firstPanel = await screen.findByTestId('secret-detail-panel')
    fireEvent.click(within(firstPanel).getByTestId('detail-tab-yaml'))
    await waitFor(() => expect(within(firstPanel).getByTestId('detail-yaml-content')).toBeInTheDocument())
    firstRender.unmount()

    renderPage('operator', ['/secret-sync/values-spoke-asia-datadog'])
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('detail-tab-overview')).toHaveAttribute('aria-pressed', 'true')
    expect(within(panel).queryByTestId('detail-yaml-content')).not.toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// SSF-8 — the tab is named "YAML" (was "Redacted YAML"), and states plainly
// up front that values are hidden.
// ─────────────────────────────────────────────────────────────────────────────

describe('SSF-8 — the YAML tab', () => {
  it('labels the tab button "YAML", never "Redacted YAML"', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    fireEvent.click(screen.getByTestId('secret-row-values-prod-eu-datadog'))
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('detail-tab-yaml')).toHaveTextContent('YAML')
    expect(within(panel).queryByText('Redacted YAML')).not.toBeInTheDocument()
  })

  it('states "Secret values are hidden." at the top of the YAML view', async () => {
    renderPage()
    const panel = await openRowOnYamlTab('values-prod-eu-datadog')
    expect(within(panel).getByTestId('detail-yaml-hidden')).toHaveTextContent('Secret values are hidden.')
  })
})
