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
const mockFetchAuditLog = vi.fn()

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
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

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

// Two clusters, two addons, and a deliberate mix of states so a group
// header has something real to add up.
const response: ManagedSecretsResponse = {
  cluster_connection_secrets: [
    { cluster: 'prod-eu', secret_namespace: 'argocd', secret_name: 'prod-eu', state: 'in_sync', source: 'git', self_heals: true },
    { cluster: 'staging-us', secret_namespace: 'argocd', secret_name: 'staging-us', state: 'out_of_sync', source: 'git', self_heals: true },
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
      cluster: 'prod-eu',
      addon: 'vault',
      secret_name: 'vault-unseal-keys',
      secret_namespace: 'vault',
      state: 'unknown',
      source: 'a Kubernetes Secret',
      self_heals: true,
    },
    // G4: a fully in-sync addon — the one group in this fixture with NO
    // issues, so its default-open behaviour differs from every other group
    // here.
    {
      cluster: 'prod-eu',
      addon: 'grafana',
      secret_name: 'grafana-secret',
      secret_namespace: 'monitoring',
      state: 'in_sync',
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
    { key: 'api-key', value: '••••••••', path: 'secrets/datadog/api-key' },
    { key: 'app-key', value: '••••••••', path: 'secrets/datadog/app-key' },
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
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
})

describe('ManagedSecrets — the backend type is a per-row fact (G1)', () => {
  it('two rows with different backends say different things, in the SOURCE column', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    expect(
      within(screen.getByTestId('secret-row-values-prod-eu-datadog')).getByTestId('cell-source'),
    ).toHaveTextContent('AWS Secrets Manager')
    expect(
      within(screen.getByTestId('secret-row-values-prod-eu-vault')).getByTestId('cell-source'),
    ).toHaveTextContent('a Kubernetes Secret')
    expect(
      within(screen.getByTestId('secret-row-connection-prod-eu')).getByTestId('cell-source'),
    ).toHaveTextContent('git')
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

    // datadog has an out-of-sync row (G4: has issues), so it opens itself —
    // both of its clusters are already on screen.
    expect(await screen.findByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument()
    // vault ALSO has an issue (its one row is never-checked) so it opens
    // itself too — its row is on screen, just not under datadog's parent.
    // grafana is the one group with no issues, so IT stays collapsed.
    expect(screen.getByTestId('secret-row-values-prod-eu-vault')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-values-prod-eu-grafana')).not.toBeInTheDocument()
  })

  it('grouped by cluster puts both kinds of secret for that cluster under one parent', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())

    await user.click(screen.getByTestId('group-by-cluster'))
    // Both clusters have an issue in this fixture (prod-eu's vault row is
    // never-checked, staging-us is out-of-sync) so BOTH groups open
    // themselves by default (G4) — this test is about the right rows
    // bucketing under the right parent, not the collapse state.
    await screen.findByTestId('secret-group-cluster-prod-eu')
    expect(screen.getByTestId('secret-group-cluster-staging-us')).toBeInTheDocument()

    expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-values-prod-eu-vault')).toBeInTheDocument()

    // Nothing leaked between the two groups — every row that comes after
    // ONE group's header, up to the NEXT group header (or the end), names
    // that same group's cluster. Groups are worst-first now (G4), so this
    // doesn't assume which cluster's group renders first.
    const order = screen
      .getAllByTestId(/^secret-(group-cluster-|row-)/)
      .map((el) => el.getAttribute('data-testid')!)
    const headerIdxs = order
      .map((id, i) => (id.startsWith('secret-group-cluster-') ? i : -1))
      .filter((i) => i >= 0)
    expect(headerIdxs).toHaveLength(2)
    const [firstIdx, secondIdx] = headerIdxs
    const firstCluster = order[firstIdx].replace('secret-group-cluster-', '')
    const secondCluster = order[secondIdx].replace('secret-group-cluster-', '')
    expect(order.slice(firstIdx + 1, secondIdx).every((id) => id.includes(firstCluster))).toBe(true)
    expect(order.slice(secondIdx + 1).every((id) => id.includes(secondCluster))).toBe(true)
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
    renderPage(['/secret-sync?group=addon'])
    expect(await screen.findByTestId('secret-group-addon-datadog')).toBeInTheDocument()
    expect(screen.getByTestId('group-by-addon')).toHaveAttribute('aria-pressed', 'true')
  })
})

describe('ManagedSecrets — groups with issues open themselves (G4)', () => {
  it('a group with an issue starts open; a group with none starts collapsed', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    await user.click(screen.getByTestId('group-by-addon'))

    // datadog has an out-of-sync row — opens itself.
    expect(await screen.findByTestId('secret-group-addon-datadog')).toHaveAttribute('aria-expanded', 'true')
    // vault has only a not-checked-yet row — still an issue (it isn't
    // in_sync) — opens itself too.
    expect(screen.getByTestId('secret-group-addon-vault')).toHaveAttribute('aria-expanded', 'true')
    // grafana is fully in_sync — no issues — starts collapsed.
    expect(screen.getByTestId('secret-group-addon-grafana')).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('secret-row-values-prod-eu-grafana')).not.toBeInTheDocument()
  })

  it("a click on the group header always wins over the computed default, in either direction", async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    await user.click(screen.getByTestId('group-by-addon'))

    // Collapse an auto-open group.
    const datadog = await screen.findByTestId('secret-group-addon-datadog')
    expect(datadog).toHaveAttribute('aria-expanded', 'true')
    await user.click(datadog)
    expect(datadog).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByTestId('secret-row-values-prod-eu-datadog')).not.toBeInTheDocument()

    // Open a collapsed, issue-free group.
    const grafana = screen.getByTestId('secret-group-addon-grafana')
    expect(grafana).toHaveAttribute('aria-expanded', 'false')
    await user.click(grafana)
    expect(grafana).toHaveAttribute('aria-expanded', 'true')
    expect(await screen.findByTestId('secret-row-values-prod-eu-grafana')).toBeInTheDocument()
  })

  it('in addon view, the not-an-addon connections bucket always sorts last', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    await user.click(screen.getByTestId('group-by-addon'))

    await screen.findByTestId('secret-group-addon-datadog')
    const headers = screen
      .getAllByTestId(/^secret-group-/)
      .filter((el) => !(el.getAttribute('data-testid') ?? '').startsWith('secret-group-summary-'))
    const order = headers.map((h) => h.getAttribute('data-testid'))
    expect(order[order.length - 1]).toBe('secret-group-__connections__')
  })

  it('groups sort worst-first, then by name', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    await user.click(screen.getByTestId('group-by-addon'))

    await screen.findByTestId('secret-group-addon-datadog')
    const headers = screen
      .getAllByTestId(/^secret-group-/)
      .filter((el) => !(el.getAttribute('data-testid') ?? '').startsWith('secret-group-summary-'))
    const order = headers.map((h) => h.getAttribute('data-testid'))
    // datadog (worst row: out_of_sync) beats vault (worst row: unknown),
    // grafana (all in_sync — the best) sorts after both, and the
    // connections bucket is forced last regardless of its own state.
    expect(order).toEqual([
      'secret-group-addon-datadog',
      'secret-group-addon-vault',
      'secret-group-addon-grafana',
      'secret-group-__connections__',
    ])
  })
})

describe('ManagedSecrets — group headers state plain sums only (G3)', () => {
  it('adds up the real per-row states, and says nothing else', async () => {
    const user = userEvent.setup()
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    await user.click(screen.getByTestId('group-by-cluster'))

    // prod-eu: connection in sync, datadog in sync, grafana in sync, vault not checked yet.
    const prodSummary = await screen.findByTestId('secret-group-summary-cluster-prod-eu')
    expect(prodSummary).toHaveTextContent('4 secrets · 1 not checked yet · 3 in sync')

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

    // P3-F2: the key list is its own section under the two diff cards, no
    // longer buried inside the live card.
    const keys = within(await screen.findByTestId('detail-key-table')).getByTestId('resource-data-keys')
    expect(within(keys).getByText('api-key')).toBeInTheDocument()
    expect(within(keys).getByText('app-key')).toBeInTheDocument()
    expect(within(keys).getAllByText('••••••••')).toHaveLength(2)

    // P2-C2: each key shows the store pointer it comes from — a location,
    // never a value.
    expect(within(keys).getByText('← secrets/datadog/api-key')).toBeInTheDocument()
    expect(within(keys).getByText('← secrets/datadog/app-key')).toBeInTheDocument()

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
