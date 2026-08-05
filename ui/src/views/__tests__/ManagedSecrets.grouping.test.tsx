// ManagedSecrets — "Group by" and the live-resource panel (the
// maintainer's ask: "datadog → cluster name → the secret name with
// namespace, backend type, synced/out of synced… and clicking on it
// should open the resource as it looks inside the cluster right now, read
// only"). Pins:
//
//  - G1: the backend a row follows comes off the ROW, not off one label
//    for the whole page — two rows with different backends read
//    differently.
//  - G2: Group by defaults to None (today's flat list); grouping by addon
//    gives addon → cluster → secret; grouping by cluster gives one
//    cluster's whole set, both kinds together; the choice is in the URL.
//  - G3: a group header states plain sums of the real per-row states and
//    nothing else — no rolled-up verdict, no percentage, no group-level
//    "last checked".
//  - G4: opening a row reads the live Secret once, shows the key names
//    with the server's blanks, and a failed read says so plainly and
//    shows no content at all.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, useSearchParams } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse, SecretResource } from '@/services/models'

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
  return { ...actual, useNavigate: () => mockNavigate }
})

const mockGetManagedSecrets = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()

vi.mock('@/services/api', () => ({
  api: { getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args) },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  triggerSecretsReconcile: vi.fn(),
  reconcileCluster: vi.fn(),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: vi.fn(),
  syncAddonValuesSecret: vi.fn(),
}))

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

// Two clusters, two addons, and a deliberate mix of states so a group
// header has something real to add up.
const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    { cluster: 'prod-eu', secret_namespace: 'argocd', secret_name: 'prod-eu', state: 'in_sync', source: 'git' },
    { cluster: 'staging-us', secret_namespace: 'argocd', secret_name: 'staging-us', state: 'out_of_sync', source: 'git' },
  ],
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
      state: 'out_of_sync',
      source: 'AWS Secrets Manager',
    },
    {
      cluster: 'prod-eu',
      addon: 'vault',
      secret_name: 'vault-unseal-keys',
      secret_namespace: 'vault',
      state: 'unknown',
      source: 'a Kubernetes Secret',
    },
  ],
  engines: {
    cluster_connection: { wired: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
    addon_values: { wired: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
  },
  addon_values_secret_source: 'AWS Secrets Manager',
}

const blankedResource: SecretResource = {
  kind: 'Secret',
  api_version: 'v1',
  name: 'datadog-secrets',
  namespace: 'datadog',
  secret_type: 'Opaque',
  created_at: '2026-07-01T00:00:00Z',
  labels: [{ key: 'app.kubernetes.io/managed-by', value: 'sharko' }],
  annotations: [{ key: 'kubectl.kubernetes.io/last-applied-configuration', value: '••••••••', blanked: true }],
  data_keys: [
    { key: 'api-key', value: '••••••••' },
    { key: 'app-key', value: '••••••••' },
  ],
  read_from: 'cluster "prod-eu", namespace "datadog"',
  values_blanked: true,
}

beforeEach(() => {
  vi.clearAllMocks()
  mockGetManagedSecrets.mockResolvedValue(response)
  mockGetClusterComparison.mockResolvedValue({ cluster: {} })
  mockGetConnectionSecretResource.mockResolvedValue({ ...blankedResource, name: 'prod-eu', namespace: 'argocd' })
  mockGetAddonValuesSecretResource.mockResolvedValue(blankedResource)
})

describe('ManagedSecrets — the backend type is a per-row fact (G1)', () => {
  it('two rows with different backends say different things', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    expect(
      within(screen.getByTestId('secret-row-values-prod-eu-datadog')).getByText('addon values · follows AWS Secrets Manager'),
    ).toBeInTheDocument()
    expect(
      within(screen.getByTestId('secret-row-values-prod-eu-vault')).getByText('addon values · follows a Kubernetes Secret'),
    ).toBeInTheDocument()
    expect(
      within(screen.getByTestId('secret-row-connection-prod-eu')).getByText('cluster connection · follows git'),
    ).toBeInTheDocument()
  })
})

describe('ManagedSecrets — Group by (G2)', () => {
  it('defaults to None: the flat list, every row on screen, no group parents', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    expect(screen.getByTestId('group-by-none')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-group-addon-datadog')).not.toBeInTheDocument()
  })

  it('grouped by addon gives the addon → cluster → secret shape: the addon is the parent, its clusters are the children', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    await user.click(screen.getByTestId('group-by-addon'))

    // Parents are the addons — plus the honest "these are not an addon"
    // bucket the connection secrets go in.
    const datadog = await screen.findByTestId('secret-group-addon-datadog')
    expect(within(datadog).getByText('datadog')).toBeInTheDocument()
    expect(screen.getByTestId('secret-group-addon-vault')).toBeInTheDocument()
    expect(screen.getByTestId('secret-group-__connections__')).toBeInTheDocument()

    // Children are hidden until the parent is opened.
    expect(screen.queryByTestId('secret-row-values-prod-eu-datadog')).not.toBeInTheDocument()

    await user.click(datadog)
    // Both of datadog's clusters, and nothing from another addon.
    expect(await screen.findByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-values-prod-eu-vault')).not.toBeInTheDocument()
  })

  it('grouped by cluster puts both kinds of secret for that cluster under one parent', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    await user.click(screen.getByTestId('group-by-cluster'))
    const prodEu = await screen.findByTestId('secret-group-cluster-prod-eu')
    await user.click(prodEu)

    expect(await screen.findByTestId('secret-row-connection-prod-eu')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-prod-eu-vault')).toBeInTheDocument()
    // Nothing from the other cluster leaked in.
    expect(screen.queryByTestId('secret-row-connection-staging-us')).not.toBeInTheDocument()
  })

  it('keeps the choice in the URL so a reload lands back on the same view', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    await user.click(screen.getByTestId('group-by-cluster'))
    await waitFor(() => expect(screen.getByTestId('location-probe')).toHaveTextContent('group=cluster'))

    // Back to None takes it out of the URL again rather than writing
    // group=none, so a plain /secrets link stays plain.
    await user.click(screen.getByTestId('group-by-none'))
    await waitFor(() => expect(screen.getByTestId('location-probe')).not.toHaveTextContent('group='))
  })

  it('comes up already grouped when the URL says so', async () => {
    renderPage(['/secrets?group=addon'])
    expect(await screen.findByTestId('secret-group-addon-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('group-by-addon')).toHaveAttribute('aria-pressed', 'true')
  })
})

describe('ManagedSecrets — group headers state plain sums only (G3)', () => {
  it('adds up the real per-row states, and says nothing else', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    await user.click(screen.getByTestId('group-by-cluster'))

    // prod-eu: connection in sync, datadog in sync, vault not checked yet.
    const prodSummary = await screen.findByTestId('secret-group-summary-cluster-prod-eu')
    expect(prodSummary).toHaveTextContent('3 secrets · 1 not checked yet · 2 in sync')

    // staging-us: connection out of sync, datadog out of sync.
    expect(screen.getByTestId('secret-group-summary-cluster-staging-us')).toHaveTextContent('2 secrets · 2 out of sync')
  })

  it('never rolls the rows up into a verdict, a percentage, or one "last checked" time', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    await user.click(screen.getByTestId('group-by-cluster'))

    const summary = await screen.findByTestId('secret-group-summary-cluster-prod-eu')
    const text = summary.textContent ?? ''
    expect(text).not.toMatch(/%/)
    expect(text).not.toMatch(/healthy|all good|ok\b/i)
    expect(text).not.toMatch(/ago|checked [0-9]/i)
  })
})

describe('ManagedSecrets — the live resource, read-only (G4)', () => {
  it('reads the Secret once when a row is opened and shows the key names with the server blanks', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    // Nothing is read while the list is on screen — this is a click-only
    // call, never a render-time or fanned-out one.
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
    expect(mockGetConnectionSecretResource).not.toHaveBeenCalled()

    await user.click(screen.getByTestId('secret-row-values-prod-eu-datadog'))

    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))
    expect(mockGetAddonValuesSecretResource).toHaveBeenCalledWith('prod-eu', 'datadog')

    const panel = await screen.findByTestId('detail-resource-panel')
    expect(within(panel).getByText('datadog/datadog-secrets')).toBeInTheDocument()
    expect(within(panel).getByText('type Opaque')).toBeInTheDocument()

    // Key names are listed; every value is the server's blank.
    const keys = within(panel).getByTestId('resource-data-keys')
    expect(within(keys).getByText('api-key')).toBeInTheDocument()
    expect(within(keys).getByText('app-key')).toBeInTheDocument()
    expect(within(keys).getAllByText('••••••••')).toHaveLength(2)

    // The label is shown in full — labels are not secret.
    expect(within(within(panel).getByTestId('resource-labels')).getByText('sharko')).toBeInTheDocument()
  })

  it('a failed read says so plainly and shows no resource content at all', async () => {
    const user = userEvent.setup()
    mockGetAddonValuesSecretResource.mockRejectedValue(new Error('Sharko couldn\'t connect to cluster "prod-eu".'))
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    await user.click(screen.getByTestId('secret-row-values-prod-eu-datadog'))

    const err = await screen.findByTestId('resource-error')
    expect(err).toHaveTextContent("Sharko couldn't connect to cluster")
    expect(screen.queryByTestId('resource-data-keys')).not.toBeInTheDocument()
    expect(screen.queryByTestId('resource-labels')).not.toBeInTheDocument()
  })

  it('a connection row reads the hub secret, not a remote one', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    await user.click(screen.getByTestId('secret-row-connection-prod-eu'))

    await waitFor(() => expect(mockGetConnectionSecretResource).toHaveBeenCalledWith('prod-eu'))
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
  })
})
