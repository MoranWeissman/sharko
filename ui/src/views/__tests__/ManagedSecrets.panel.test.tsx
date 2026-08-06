// ManagedSecrets.panel — P3-F2, the detail panel rebuilt around the
// resource. What this suite holds down:
//
//  - the five edge sentences, one per real row state, each reachable;
//  - the LEFT card paints from row data alone — no request needed;
//  - a viewer sees a calm sentence about access, never a permission
//    error, and fires NO live read at all;
//  - a row already known missing fires no read either (a doomed call that
//    could only come back 404);
//  - a failed read offers Retry, and Retry actually re-reads;
//  - a list re-read behind an open panel never reloads its live card;
//  - a row opens from the keyboard;
//  - there is NO per-key match/differ verdict anywhere, and must not be.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

const mockNavigate = vi.fn()
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom')
  return { ...actual, useNavigate: () => mockNavigate }
})

const mockShowToast = vi.fn()
vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) }
})

const mockGetManagedSecrets = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockReconcileCluster = vi.fn()
const mockRefreshAddonValuesSecret = vi.fn()

vi.mock('@/services/api', () => ({
  api: { getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args) },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  triggerSecretsReconcile: vi.fn(),
  checkAllAddonValuesSecrets: vi.fn(),
  reconcileCluster: (...args: unknown[]) => mockReconcileCluster(...args),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: (...args: unknown[]) => mockRefreshAddonValuesSecret(...args),
  syncAddonValuesSecret: vi.fn(),
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
      <MemoryRouter initialEntries={['/secrets']}>
        <ManagedSecrets />
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

// One values row per state, so every edge sentence has a row to reach it
// from, plus the two connection rows the intent card needs.
const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    {
      cluster: 'prod-eu',
      secret_namespace: 'argocd',
      secret_name: 'prod-eu',
      state: 'in_sync',
      source: 'git',
      self_heals: true,
      compared_revision: 'abcdef1234567890abcdef1234567890abcdef12',
      compared_path: 'configuration/managed-clusters.yaml',
    },
    {
      cluster: 'no-commit',
      secret_namespace: 'argocd',
      secret_name: 'no-commit',
      state: 'unknown',
      source: 'git',
      self_heals: false,
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
      cluster: 'spoke-asia',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'missing',
      source: 'AWS Secrets Manager',
      self_heals: true,
    },
    {
      cluster: 'byo-cluster',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'foreign',
      source: 'AWS Secrets Manager',
      self_heals: false,
    },
    {
      cluster: 'flaky-eu',
      addon: 'datadog',
      secret_name: 'datadog-secrets',
      secret_namespace: 'datadog',
      state: 'unknown',
      source: 'AWS Secrets Manager',
      self_heals: true,
      last_check_error: "Sharko couldn't connect to this cluster.",
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
  mockGetConnectionSecretResource.mockResolvedValue({ ...blankedResource, name: 'prod-eu', namespace: 'argocd' })
  mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
  mockGetClusterComparison.mockResolvedValue({
    cluster: { name: 'prod-eu', labels: {}, last_reconcile: { time: '2026-08-05T00:00:00Z', outcome: 'succeeded' } },
  })
})

async function openRow(key: string) {
  await waitFor(() => expect(screen.getByTestId(`secret-row-${key}`)).toBeInTheDocument())
  fireEvent.click(screen.getByTestId(`secret-row-${key}`))
  return screen.findByTestId('secret-detail-panel')
}

// ─────────────────────────────────────────────────────────────────────────────
// The five edge sentences
// ─────────────────────────────────────────────────────────────────────────────

describe('the diff verdict — five sentences, one per state', () => {
  it('says "these match" for an in-sync row', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('These match — the cluster has what the source says.'),
    )
  })

  it('says "these differ" for an out-of-sync row', async () => {
    renderPage()
    const panel = await openRow('values-staging-us-datadog')
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
        "These differ — Sync writes the source's version onto the cluster.",
      ),
    )
  })

  it('says "never created" for a missing row, and fires NO read for it', async () => {
    renderPage()
    const panel = await openRow('values-spoke-asia-datadog')
    expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
      'This secret was never created on the cluster — Sync creates it.',
    )
    // The doomed read: the row already knows the secret is not there, so
    // asking the cluster could only ever come back 404.
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
    expect(within(panel).getByTestId('resource-not-there')).toHaveTextContent('Nothing is there')
  })

  it('says "someone else created this" for a foreign row', async () => {
    renderPage()
    const panel = await openRow('values-byo-cluster-datadog')
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
        'Someone else created this secret — Sharko will not touch it.',
      ),
    )
  })

  it('says "could not look" for an unknown row — the last check never finished', async () => {
    renderPage()
    const panel = await openRow('values-flaky-eu-datadog')
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('Sharko could not look at the cluster just now.'),
    )
    // The reason lives on its own line, as a pre-written sentence.
    expect(within(panel).getByTestId('last-check-error')).toHaveTextContent("Sharko couldn't connect to this cluster.")
  })

  it('says "could not look" when the live read itself fails, with a Retry that re-reads', async () => {
    mockGetAddonValuesSecretResource.mockRejectedValue(new Error('Sharko couldn\'t connect to cluster "prod-eu".'))
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')

    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('Sharko could not look at the cluster just now.'),
    )
    expect(within(panel).getByTestId('resource-error')).toHaveTextContent("Sharko couldn't connect to cluster")
    expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1)

    mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
    fireEvent.click(within(panel).getByTestId('resource-retry'))
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(2))
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('These match — the cluster has what the source says.'),
    )
  })

  it('a live read that comes back 404 says "never created", not "could not look"', async () => {
    const notFound = Object.assign(new Error('This secret does not exist on cluster "prod-eu" right now.'), { status: 404 })
    mockGetAddonValuesSecretResource.mockRejectedValue(notFound)
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')

    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
        'This secret was never created on the cluster — Sync creates it.',
      ),
    )
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The two cards
// ─────────────────────────────────────────────────────────────────────────────

describe('the two cards', () => {
  it('the left card paints from row data alone — the git file and commit are there before any read resolves', async () => {
    // A read that never settles: the left card must still be complete.
    mockGetConnectionSecretResource.mockReturnValue(new Promise(() => {}))
    renderPage()
    const panel = await openRow('connection-prod-eu')

    const intent = within(panel).getByTestId('diff-intent-card')
    expect(within(intent).getByText('configuration/managed-clusters.yaml')).toBeInTheDocument()
    expect(within(intent).getByText('abcdef1')).toBeInTheDocument()

    // ...while the right card is still waiting.
    expect(within(panel).getByTestId('diff-live-card')).toHaveTextContent('Reading it from the cluster')
  })

  it('a connection row with no compared commit says so instead of showing a blank card', async () => {
    renderPage()
    const panel = await openRow('connection-no-commit')
    expect(within(panel).getByTestId('diff-intent-card')).toHaveTextContent("Sharko hasn't compared this secret against git yet.")
  })

  it('the left card of a values row names the real store and points at the key list', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    const intent = within(panel).getByTestId('diff-intent-card')
    expect(intent).toHaveTextContent('The values come from AWS Secrets Manager.')
    expect(intent).toHaveTextContent('Git holds a pointer to where each value lives, never the value itself.')
  })

  it('the right card shows the live object with the server blanks, and never a value', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    const liveCard = await within(panel).findByTestId('diff-live-card')

    await waitFor(() => expect(liveCard).toHaveTextContent('datadog/datadog-secrets'))
    expect(liveCard).toHaveTextContent('type Opaque')
    // The allow-listed provenance annotation shows as written; the
    // self-copying one shows as the server's mask.
    const annotations = within(liveCard).getByTestId('resource-annotations')
    expect(annotations).toHaveTextContent('sharko.dev/source')
    expect(annotations).toHaveTextContent('AWS Secrets Manager')
    expect(annotations).toHaveTextContent('kubectl.kubernetes.io/last-applied-configuration')
    expect(within(annotations).getByText('••••••••')).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The key table
// ─────────────────────────────────────────────────────────────────────────────

describe('the key table', () => {
  it('lists each key, where its value comes from, and whether the cluster has it', async () => {
    renderPage()
    await openRow('values-prod-eu-datadog')

    const table = await screen.findByTestId('detail-key-table')
    expect(within(table).getByText('api-key')).toBeInTheDocument()
    expect(within(table).getByText('← secrets/datadog/api-key')).toBeInTheDocument()
    // present: false — declared by the addon's definition, not on the
    // cluster. This is the ONLY per-key verdict there is.
    expect(within(table).getByText('not on the cluster')).toBeInTheDocument()
  })

  it('never prints a per-key match or differ verdict — the engines compare whole secrets', async () => {
    renderPage()
    await openRow('values-prod-eu-datadog')

    const table = await screen.findByTestId('detail-key-table')
    const text = table.textContent ?? ''
    expect(text).not.toMatch(/matches/i)
    expect(text).not.toMatch(/differs/i)
    expect(text).not.toMatch(/in sync|out of sync/i)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Who is allowed to look
// ─────────────────────────────────────────────────────────────────────────────

describe('the role gate on the live half', () => {
  it('a viewer sees a calm sentence about access — never a permission error — and fires no read', async () => {
    renderPage('viewer')
    const panel = await openRow('values-prod-eu-datadog')

    expect(within(panel).getByTestId('live-needs-operator')).toHaveTextContent(
      'Reading the live secret needs operator access.',
    )
    expect(within(panel).queryByTestId('resource-error')).not.toBeInTheDocument()
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
    expect(mockGetConnectionSecretResource).not.toHaveBeenCalled()

    // The left card is still fully there — a viewer loses the live read,
    // not the whole panel.
    expect(within(panel).getByTestId('diff-intent-card')).toHaveTextContent('The values come from AWS Secrets Manager.')
    // And the key table, which is live-read data, is simply absent.
    expect(screen.queryByTestId('detail-key-table')).not.toBeInTheDocument()
  })

  it('an operator gets the live card', async () => {
    renderPage('operator')
    await openRow('values-prod-eu-datadog')
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))
    expect(screen.queryByTestId('live-needs-operator')).not.toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The panel keeps its own state
// ─────────────────────────────────────────────────────────────────────────────

describe('the open panel is independent of the list', () => {
  it('a list re-read behind an open panel never re-reads the live secret', async () => {
    mockRefreshAddonValuesSecret.mockResolvedValue({ message: 'checked' })
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))

    const before = mockGetManagedSecrets.mock.calls.length
    // Refresh re-reads the list, which hands the panel a brand new row
    // object for the same row. The live card must not blink.
    fireEvent.click(within(panel).getByTestId('detail-refresh'))
    await waitFor(() => expect(mockGetManagedSecrets.mock.calls.length).toBeGreaterThan(before))

    expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1)
    expect(within(panel).getByTestId('diff-live-card')).toHaveTextContent('datadog/datadog-secrets')
  })

  it('opening a DIFFERENT row does read that row', async () => {
    renderPage()
    await openRow('values-prod-eu-datadog')
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))

    fireEvent.click(screen.getByTestId('secret-row-values-staging-us-datadog'))
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(2))
    expect(mockGetAddonValuesSecretResource).toHaveBeenLastCalledWith('staging-us', 'datadog')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Keyboard
// ─────────────────────────────────────────────────────────────────────────────

describe('rows are reachable from the keyboard', () => {
  it('a row is focusable, announced as a button, and opens on Enter and on Space', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    const row = screen.getByTestId('secret-row-values-prod-eu-datadog')

    expect(row).toHaveAttribute('tabindex', '0')
    expect(row).toHaveAttribute('role', 'button')
    expect(row).toHaveAttribute('aria-label', 'Open datadog/datadog-secrets')

    fireEvent.keyDown(row, { key: 'Enter' })
    const panel = await screen.findByTestId('secret-detail-panel')
    expect(within(panel).getByTestId('detail-resource-header')).toHaveTextContent('datadog/datadog-secrets')

    fireEvent.keyDown(screen.getByTestId('secret-row-values-staging-us-datadog'), { key: ' ' })
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenLastCalledWith('staging-us', 'datadog'))
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The header
// ─────────────────────────────────────────────────────────────────────────────

describe('the resource header', () => {
  it('names the kind, the secret, its cluster, and its age once the read lands', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    const header = within(panel).getByTestId('detail-resource-header')

    expect(header).toHaveTextContent('Secret')
    expect(header).toHaveTextContent('datadog/datadog-secrets')
    expect(header).toHaveTextContent('on prod-eu')
    await waitFor(() => expect(header).toHaveTextContent('created'))
  })

  it('shows no age at all when the live read never lands — never an invented one', async () => {
    mockGetAddonValuesSecretResource.mockReturnValue(new Promise(() => {}))
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    expect(within(panel).getByTestId('detail-resource-header')).not.toHaveTextContent('created')
  })
})
