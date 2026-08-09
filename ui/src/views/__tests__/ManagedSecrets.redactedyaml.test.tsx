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
import { MemoryRouter } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => vi.fn() }
})

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

function renderPage(role = 'operator') {
  return render(
    <AuthContext.Provider value={authFor(role)}>
      <MemoryRouter initialEntries={['/secret-sync']}>
        <ManagedSecrets />
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

  it('resets to the Overview tab when a different row is opened', async () => {
    renderPage()
    const firstPanel = await openRowOnYamlTab('values-prod-eu-datadog')
    await waitFor(() => expect(within(firstPanel).getByTestId('detail-yaml-content')).toBeInTheDocument())

    fireEvent.click(screen.getByTestId('secret-row-values-spoke-asia-datadog'))
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
