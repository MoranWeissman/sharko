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
//  - the page's own 30-second self-refresh (gitops-proud P4-I I2) is the
//    same list re-read, and holds the same guarantee, pausing while the
//    tab is hidden and resuming once it's visible again;
//  - a row opens from the keyboard;
//  - there is NO per-key match/differ verdict anywhere, and must not be.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { ManagedSecrets } from '@/views/ManagedSecrets'
import { SecretDetailPage } from '@/views/SecretDetailPage'
import { AuthContext } from '@/hooks/useAuth'
import type { ManagedSecretsResponse } from '@/services/models'

// SSF-9: react-router-dom is used for real here — a row click now
// navigates to its own full page (SecretDetailPage, at
// /secret-sync/<key>) rather than opening a drawer in place, so the test
// harness follows it there through the actual router. See renderPage.

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

const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
    getConnectionComparison: () => Promise.resolve({
      cluster: 'test-cluster',
      status: 'synced',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    }),
  },
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: (...args: unknown[]) => mockGetConnectionSecretResource(...args),
  getAddonValuesSecretResource: (...args: unknown[]) => mockGetAddonValuesSecretResource(...args),
  triggerSecretsReconcile: vi.fn(),
  checkAllAddonValuesSecrets: vi.fn(),
  reconcileCluster: (...args: unknown[]) => mockReconcileCluster(...args),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: (...args: unknown[]) => mockRefreshAddonValuesSecret(...args),
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
    {
      cluster: 'drifted-eu',
      secret_namespace: 'argocd',
      secret_name: 'drifted-eu',
      state: 'out_of_sync',
      source: 'git',
      self_heals: true,
      compared_revision: 'abcdef1234567890abcdef1234567890abcdef12',
      compared_path: 'configuration/managed-clusters.yaml',
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
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
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
  it('says "matches" for an in-sync row (SSF-8/SSF-12: a values row names its real source, never "Git")', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
        'The cluster copy matches AWS Secrets Manager. No action is needed.',
      ),
    )
  })

  it('says the copies do not match for an out-of-sync row, with the repair promise alongside it', async () => {
    renderPage()
    const panel = await openRow('values-staging-us-datadog')
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy does not match AWS Secrets Manager.'),
    )
    expect(within(panel).getByTestId('detail-repair-note')).toHaveTextContent(
      'Sync will update the cluster copy to match AWS Secrets Manager.',
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
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent(
        'The cluster copy matches AWS Secrets Manager. No action is needed.',
      ),
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
// SSF-14 item 3 — provenance above the comparison, a real safe-field table
// inside it. Provenance (comparison-provenance) is never one side of the
// table; the table itself pairs the same safe field per row kind.
// ─────────────────────────────────────────────────────────────────────────────

describe('the comparison: provenance above, a real field table inside', () => {
  it('provenance paints from row data alone — the git file and commit are there before the label-drift fetch resolves', async () => {
    // SSF-12: a connection row's comparison reads getClusterComparison
    // (diffData), never the live secret read — so THAT is the fetch that
    // must stay pending for this to prove anything.
    mockGetClusterComparison.mockReturnValue(new Promise(() => {}))
    renderPage()
    const panel = await openRow('connection-prod-eu')
    // SSF-8/SSF-14: prod-eu is in_sync (a match) — the comparison box opens
    // behind "View comparison" instead of showing automatically.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

    const provenance = within(panel).getByTestId('comparison-provenance')
    expect(within(provenance).getByText('configuration/managed-clusters.yaml')).toBeInTheDocument()
    expect(within(provenance).getByText('abcdef1')).toBeInTheDocument()

    // ...while the table itself is still waiting on the label-drift fetch.
    expect(within(panel).getByText('Loading…')).toBeInTheDocument()
  })

  it('a connection row with no compared commit says so instead of showing a blank table', async () => {
    renderPage()
    // no-commit is 'unknown' (could_not_look, not a match) — the comparison
    // box shows automatically, no click needed.
    const panel = await openRow('connection-no-commit')
    expect(within(panel).getByTestId('comparison-provenance')).toHaveTextContent("Sharko hasn't compared this secret against git yet.")
  })

  it('the provenance line of a values row names the real store, never Git', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    // SSF-8/SSF-14: this row is in_sync (a match) — open the comparison first.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    const provenance = within(panel).getByTestId('comparison-provenance')
    expect(provenance).toHaveTextContent('Compared with AWS Secrets Manager.')
    expect(provenance).toHaveTextContent('Git holds a pointer to where each value lives, never the value itself.')
  })

  // SSF-12/SSF-14 honesty rule: a connection row's ONLY comparable field is
  // the addon-label drift git vs cluster — never a value, and never the
  // resource facts (type, labels, annotations), which now live in the YAML
  // tab, the one remaining technical representation.
  it('a connection row\'s comparison table shows the addon-label drift as Field | Expected in Git | On the cluster | Result, never a value', async () => {
    mockGetClusterComparison.mockResolvedValue({
      cluster: {
        name: 'no-commit',
        labels: {},
        last_reconcile: { time: '2026-08-05T00:00:00Z', outcome: 'succeeded', label_drift: { added: ['datadog'], removed: ['old-addon'] } },
      },
    })
    renderPage()
    // no-commit's connection is 'unknown' -> could_not_look (not a match) — comparison auto-shows.
    const panel = await openRow('connection-no-commit')
    const drift = await within(panel).findByTestId('comparison-label-drift')
    expect(within(drift).getByText('Field')).toBeInTheDocument()
    expect(within(drift).getByText('Expected in Git')).toBeInTheDocument()
    expect(within(drift).getByText('On the cluster')).toBeInTheDocument()
    expect(within(drift).getByText('Result')).toBeInTheDocument()
    expect(drift).toHaveTextContent('datadog')
    expect(drift).toHaveTextContent('old-addon')
    expect(drift).toHaveTextContent('Missing on cluster')
    expect(drift).toHaveTextContent('Extra on cluster')
  })

  // SSF-12/SSF-14 honesty rule: a values row's ONLY comparable field is key
  // presence — expected keys vs which of them the server saw. Never a
  // value; the mask is fixed-length so it leaks no length either. The type/
  // labels/annotations that used to sit in a "Resource details" accordion
  // next to this table are gone from Overview entirely now — YAML is the
  // one place they still live (pinned in ManagedSecrets.redactedyaml.test.tsx).
  it('a values row\'s comparison table shows key presence as Key name | Expected | Present on cluster | Result, and no resource facts leak into it', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    // SSF-8/SSF-14: this row is in_sync (a match) — open the comparison first.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

    const presence = await within(panel).findByTestId('comparison-key-presence')
    expect(within(presence).getByText('Key name')).toBeInTheDocument()
    expect(within(presence).getByText('Present on cluster')).toBeInTheDocument()
    expect(presence).toHaveTextContent('api-key')
    expect(presence).toHaveTextContent('app-key')
    expect(presence).toHaveTextContent('Match')
    expect(presence).toHaveTextContent('Missing')
    // Never a value, and never a resource fact — type/labels/annotations
    // don't belong in the comparison, and the accordion they used to live
    // in (detail-resource-disclosure) is gone.
    expect(presence).not.toHaveTextContent('Opaque')
    expect(screen.queryByTestId('detail-resource-disclosure')).not.toBeInTheDocument()
  })

  // Walkthrough follow-up on SSF-14 item 3: opening the comparison on a
  // HEALTHY row used to show only the same claim the conclusion already
  // made ("Every addon label on the cluster matches what git expects.") —
  // no new evidence. It now renders the full field-by-field table, built
  // from data already fetched: git's per-cluster label map
  // (getClusterComparison's cluster.labels) vs the live connection
  // secret's own labels (getConnectionSecretResource). A disabled addon
  // never gets a row — it has no corresponding label on the cluster
  // Secret either way, so there is nothing to compare.
  it('a healthy connection row\'s opened comparison lists its matching addon labels as evidence, not just a repeated claim', async () => {
    mockGetClusterComparison.mockResolvedValue({
      cluster: {
        name: 'prod-eu',
        labels: { 'metrics-server': 'enabled', 'cert-manager': 'disabled' },
        last_reconcile: { time: '2026-08-05T00:00:00Z', outcome: 'succeeded' },
      },
    })
    mockGetConnectionSecretResource.mockResolvedValue({
      kind: 'Secret',
      api_version: 'v1',
      name: 'prod-eu',
      namespace: 'argocd',
      secret_type: 'Opaque',
      created_at: '2026-07-01T00:00:00Z',
      labels: [
        { key: 'app.kubernetes.io/managed-by', value: 'sharko' },
        { key: 'addons.sharko.dev/metrics-server', value: 'enabled' },
      ],
      annotations: [],
      data_keys: [],
      read_from: 'cluster "prod-eu"',
      values_blanked: true,
    })
    renderPage()
    const panel = await openRow('connection-prod-eu') // in_sync -> match
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

    const table = await within(panel).findByTestId('comparison-label-match')
    expect(within(table).getByText('Field')).toBeInTheDocument()
    expect(within(table).getByText('Expected in Git')).toBeInTheDocument()
    expect(within(table).getByText('On the cluster')).toBeInTheDocument()
    expect(within(table).getByText('addons.sharko.dev/metrics-server')).toBeInTheDocument()
    // Expected in Git and On the cluster both read "enabled" for this row.
    expect(within(table).getAllByText('enabled')).toHaveLength(2)
    expect(within(table).getByText('Match')).toBeInTheDocument()
    // The disabled addon never gets a row — no comparable cluster-side label.
    expect(within(table).queryByText(/cert-manager/)).not.toBeInTheDocument()
    // The old bare summary sentence no longer stands in for the table.
    expect(within(panel).queryByTestId('comparison-no-drift')).not.toBeInTheDocument()
  })

  it('falls back to the honest summary sentence for a healthy connection row when the live cluster labels are not provable yet', async () => {
    mockGetClusterComparison.mockResolvedValue({
      cluster: {
        name: 'prod-eu',
        labels: { 'metrics-server': 'enabled' },
        last_reconcile: { time: '2026-08-05T00:00:00Z', outcome: 'succeeded' },
      },
    })
    // The live read never settles — the cluster side can't be proven.
    mockGetConnectionSecretResource.mockReturnValue(new Promise(() => {}))
    renderPage()
    const panel = await openRow('connection-prod-eu') // in_sync -> match
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

    expect(await within(panel).findByTestId('comparison-no-drift')).toHaveTextContent(
      'Every addon label on the cluster matches what git expects.',
    )
    expect(within(panel).queryByTestId('comparison-label-match')).not.toBeInTheDocument()
  })

  // Walkthrough follow-up on SSF-14 item 3, values-row half: a healthy
  // values row's comparison already lists every expected key (present and
  // missing alike, via ValuesKeyComparison) — this pins that a row where
  // EVERY key is actually present reads "Match" for all of them, not just
  // a claim.
  it('a healthy addon-values row\'s opened comparison lists its keys as present, not just a summary sentence', async () => {
    mockGetAddonValuesSecretResource.mockResolvedValue({ ...blankedResource, data_keys: blankedResource.data_keys.map((k) => ({ ...k, present: true })) })
    renderPage()
    const panel = await openRow('values-prod-eu-datadog') // in_sync -> match
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

    const table = await within(panel).findByTestId('comparison-key-presence')
    expect(within(table).getByText('api-key')).toBeInTheDocument()
    expect(within(table).getByText('app-key')).toBeInTheDocument()
    expect(within(table).getAllByText('Match')).toHaveLength(2)
    expect(within(table).queryByText('Missing')).not.toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Who is allowed to look
// ─────────────────────────────────────────────────────────────────────────────

describe('the role gate on the live half', () => {
  it('a viewer sees a calm sentence about access — never a permission error — and fires no read', async () => {
    renderPage('viewer')
    const panel = await openRow('values-prod-eu-datadog')
    // SSF-8: this row is in_sync (a match) — open the comparison first; the
    // toggle itself is not role-gated, only the live half inside it is.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

    expect(within(panel).getByTestId('live-needs-operator')).toHaveTextContent(
      'Reading the live secret needs operator access.',
    )
    expect(within(panel).queryByTestId('resource-error')).not.toBeInTheDocument()
    expect(mockGetAddonValuesSecretResource).not.toHaveBeenCalled()
    expect(mockGetConnectionSecretResource).not.toHaveBeenCalled()

    // Provenance is still fully there — a viewer loses the live read, not
    // the whole panel.
    expect(within(panel).getByTestId('comparison-provenance')).toHaveTextContent('Compared with AWS Secrets Manager.')
    // And the key-presence table, which is live-read data, is simply absent.
    expect(within(panel).queryByTestId('comparison-key-presence')).not.toBeInTheDocument()
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
    // SSF-8: this row is in_sync (a match) — open the comparison first.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))

    const before = mockGetManagedSecrets.mock.calls.length
    // Refresh re-reads the list, which hands the panel a brand new row
    // object for the same row. The live card must not blink.
    fireEvent.click(within(panel).getByTestId('detail-refresh'))
    await waitFor(() => expect(mockGetManagedSecrets.mock.calls.length).toBeGreaterThan(before))

    expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1)
    // The comparison table's content survived untouched — proven by its
    // key-presence comparison content (SSF-12/SSF-14: a values row's
    // comparison table shows key presence, never a resource fact).
    expect(within(panel).getByTestId('comparison-key-presence')).toHaveTextContent('api-key')
  })

  // SSF-9: a different row is now a different PAGE (its own URL, its own
  // mount) rather than a still-open drawer swapping rows underneath
  // itself — this proves each page's read is scoped to its own row key,
  // never carrying over a stale fetch from whichever row a reader looked
  // at previously.
  it('a different row\'s page reads that row, not the previous one', async () => {
    const firstRender = renderPage('operator', ['/secret-sync/values-prod-eu-datadog'])
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))
    expect(mockGetAddonValuesSecretResource).toHaveBeenLastCalledWith('prod-eu', 'datadog')
    firstRender.unmount()

    renderPage('operator', ['/secret-sync/values-staging-us-datadog'])
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(2))
    expect(mockGetAddonValuesSecretResource).toHaveBeenLastCalledWith('staging-us', 'datadog')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// I2 (gitops-proud P4-I) — the 30-second self-refresh
// ─────────────────────────────────────────────────────────────────────────────

// jsdom's document.visibilityState is a getter with no setter — redefine it
// per test to flip the page between "visible" (the refresh runs) and
// "hidden" (it pauses), then fire the real event the page listens for.
function setDocumentVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  })
  document.dispatchEvent(new Event('visibilitychange'))
}

describe('the page keeps itself fresh every 30 seconds while visible', () => {
  afterEach(() => {
    setDocumentVisibility('visible')
    vi.useRealTimers()
  })

  it('re-reads the list on its own after 30 seconds, and the open panel is untouched — the same hard guarantee a manual re-read already has', async () => {
    // Fake timers go on BEFORE render — the 30s interval is armed in a
    // useEffect at mount, so it must be the fake clock that owns it from
    // the start (shouldAdvanceTime keeps real awaits/waitFor working).
    vi.useFakeTimers({ shouldAdvanceTime: true })

    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    // SSF-8: this row is in_sync (a match) — open the comparison first.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))

    const callsBefore = mockGetManagedSecrets.mock.calls.length
    await vi.advanceTimersByTimeAsync(30_000)

    await waitFor(() => expect(mockGetManagedSecrets.mock.calls.length).toBeGreaterThan(callsBefore))
    // The panel is still open on the same row, and its live card was NOT
    // re-fetched — the exact guarantee the "list re-read" test above pins
    // for a manual Refresh, now proven for the automatic 30s one too.
    expect(screen.getByTestId('secret-detail-panel')).toBeInTheDocument()
    expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1)
    // The comparison table's content survived untouched — proven by its
    // key-presence comparison content (SSF-12/SSF-14: a values row's
    // comparison table shows key presence, never a resource fact).
    expect(within(panel).getByTestId('comparison-key-presence')).toHaveTextContent('api-key')
  })

  it('does not re-read while the tab is hidden', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })

    renderPage()
    await waitFor(() => expect(mockGetManagedSecrets).toHaveBeenCalledTimes(1))

    setDocumentVisibility('hidden')
    await vi.advanceTimersByTimeAsync(60_000)

    expect(mockGetManagedSecrets).toHaveBeenCalledTimes(1)
  })

  it('resumes re-reading once the tab becomes visible again', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })

    renderPage()
    await waitFor(() => expect(mockGetManagedSecrets).toHaveBeenCalledTimes(1))

    setDocumentVisibility('hidden')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(mockGetManagedSecrets).toHaveBeenCalledTimes(1)

    setDocumentVisibility('visible')
    await vi.advanceTimersByTimeAsync(30_000)

    await waitFor(() => expect(mockGetManagedSecrets.mock.calls.length).toBeGreaterThan(1))
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Keyboard
// ─────────────────────────────────────────────────────────────────────────────

describe('rows are reachable from the keyboard', () => {
  it('a row is focusable, announced as a button, and opens its own page on Enter', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-prod-eu-datadog')).toBeInTheDocument())
    const row = screen.getByTestId('secret-row-values-prod-eu-datadog')

    expect(row).toHaveAttribute('tabindex', '0')
    expect(row).toHaveAttribute('role', 'button')
    expect(row).toHaveAttribute('aria-label', 'Open datadog/datadog-secrets')

    fireEvent.keyDown(row, { key: 'Enter' })
    const panel = await screen.findByTestId('secret-detail-panel')
    // SSF-4/SSF-9/SSF-14: the page's own title states what the row is in
    // plain words — the raw namespace/name identity now lives only on the
    // YAML tab (see "the resource identity" describe block below).
    expect(within(panel).getByRole('heading', { name: 'datadog values on prod-eu' })).toBeInTheDocument()
  })

  it('Space also opens a row\'s own page', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument())
    fireEvent.keyDown(screen.getByTestId('secret-row-values-staging-us-datadog'), { key: ' ' })
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenLastCalledWith('staging-us', 'datadog'))
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The resource identity — SSF-14 item 4 removed the "Resource details"
// accordion from Overview (type/age/labels/annotations/namespace-name);
// the YAML tab is the ONE place those facts still render.
// ─────────────────────────────────────────────────────────────────────────────

describe('the resource identity lives on the YAML tab only, not on Overview', () => {
  it('the raw namespace/name identity never appears on Overview — the plain-words title is what Overview says instead', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    expect(panel.textContent).not.toContain('datadog/datadog-secrets')
    expect(within(panel).getByRole('heading', { name: 'datadog values on prod-eu' })).toBeInTheDocument()
  })

  it('the identity appears on the YAML tab, exactly once', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    fireEvent.click(within(panel).getByTestId('detail-tab-yaml'))
    const content = await within(panel).findByTestId('detail-yaml-content')
    expect(content).toHaveTextContent('name: datadog-secrets')
    expect(content).toHaveTextContent('namespace: datadog')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// SSF-4 — Comparison naming, Check now, and the strong Sync button
// ─────────────────────────────────────────────────────────────────────────────

describe('SSF-12 — comparison heading, action naming, and Sync visibility', () => {
  it('calls the comparison "Comparison" when the row matches its source — row kind no longer decides this word', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu') // in_sync -> match
    expect(within(panel).getByRole('heading', { name: 'Comparison' })).toBeInTheDocument()
    expect(within(panel).queryByRole('heading', { name: 'Diff' })).not.toBeInTheDocument()
    expect(within(panel).queryByRole('heading', { name: 'Differences' })).not.toBeInTheDocument()
  })

  it('calls the comparison "Differences" when the row does not match — never "Diff", which would claim a git-only check', async () => {
    renderPage()
    const panel = await openRow('values-staging-us-datadog') // out_of_sync -> differ
    expect(within(panel).getByRole('heading', { name: 'Differences' })).toBeInTheDocument()
    expect(within(panel).queryByRole('heading', { name: 'Diff' })).not.toBeInTheDocument()
    expect(within(panel).queryByRole('heading', { name: 'Comparison' })).not.toBeInTheDocument()
  })

  it('labels the check button "Check now" before any check has run (testid unchanged)', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    expect(within(panel).getByTestId('detail-refresh')).toHaveTextContent('Check now')
    expect(within(panel).queryByText('Refresh')).not.toBeInTheDocument()
  })

  it('renders Sync as the strong teal action when there is real drift to push', async () => {
    renderPage()
    const panel = await openRow('values-staging-us-datadog') // out_of_sync
    const syncButton = within(panel).getByTestId('detail-sync')
    expect(syncButton).not.toBeDisabled()
    expect(syncButton.className).toMatch(/bg-teal-600/)
  })

  it('hides Sync entirely — no disabled button at all — when the row already matches its source', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog') // in_sync -> match
    expect(within(panel).queryByTestId('detail-sync')).not.toBeInTheDocument()
    // Check now stays — it's read-only and always useful.
    expect(within(panel).getByTestId('detail-refresh')).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// SSF-8 — drawer calm-down: title, comparison on demand, disclosure sections
// ─────────────────────────────────────────────────────────────────────────────

describe('SSF-8/SSF-9 — the page title says what the row is, in plain words', () => {
  it('titles a connection row "{cluster} connection"', async () => {
    renderPage()
    await openRow('connection-prod-eu')
    // SSF-9: the title moved from the drawer's own header onto the page,
    // above (not inside) the "secret-detail-panel" content div.
    expect(screen.getByRole('heading', { name: 'prod-eu connection' })).toBeInTheDocument()
  })

  it('titles a values row "{addon} values on {cluster}"', async () => {
    renderPage()
    await openRow('values-prod-eu-datadog')
    expect(screen.getByRole('heading', { name: 'datadog values on prod-eu' })).toBeInTheDocument()
  })
})

describe('SSF-8/SSF-14 — comparison on demand, and the toggle actually toggles', () => {
  it('a matching row shows the one-line result and NOT the comparison, until "View comparison" reveals it — and it can be hidden again', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog') // in_sync -> match
    // The one-line result is up front...
    expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy matches AWS Secrets Manager. No action is needed.')
    // ...but the comparison is not rendered until asked for.
    expect(within(panel).queryByTestId('comparison-provenance')).not.toBeInTheDocument()
    const toggle = within(panel).getByTestId('view-comparison-toggle')
    expect(toggle).toHaveTextContent('View comparison')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')

    fireEvent.click(toggle)

    expect(within(panel).getByTestId('comparison-provenance')).toBeInTheDocument()
    await waitFor(() => expect(within(panel).getByTestId('comparison-key-presence')).toBeInTheDocument())
    // SSF-14 item 2: the SAME toggle stays put — it never disappears — and
    // now reads "Hide comparison".
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveTextContent('Hide comparison')
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveAttribute('aria-expanded', 'true')

    // Clicking it again closes the comparison — no page refresh needed.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    expect(within(panel).queryByTestId('comparison-provenance')).not.toBeInTheDocument()
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveTextContent('View comparison')
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveAttribute('aria-expanded', 'false')
  })

  it('a differing row shows the comparison straight away, and its toggle (already "Hide comparison") can close it', async () => {
    renderPage()
    const panel = await openRow('values-staging-us-datadog') // out_of_sync -> differ
    expect(within(panel).getByTestId('comparison-provenance')).toBeInTheDocument()
    await waitFor(() => expect(within(panel).getByTestId('comparison-key-presence')).toBeInTheDocument())
    const toggle = within(panel).getByTestId('view-comparison-toggle')
    expect(toggle).toHaveTextContent('Hide comparison')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')

    fireEvent.click(toggle)
    expect(within(panel).queryByTestId('comparison-provenance')).not.toBeInTheDocument()
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveTextContent('View comparison')
  })

  it('a foreign row (a boundary, not a match) shows the comparison straight away too', async () => {
    renderPage()
    const panel = await openRow('values-byo-cluster-datadog') // foreign
    expect(within(panel).getByTestId('comparison-provenance')).toBeInTheDocument()
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveTextContent('Hide comparison')
  })

  // SSF-9: a different row is its own page/mount now, so "the reveal
  // doesn't carry over" is proven by rendering a SECOND matching row's page
  // fresh (rather than clicking within a still-open drawer) and finding it
  // collapsed, same as the first row was before its own toggle was clicked.
  it('a second matching row\'s page opens collapsed — the "View comparison" reveal never carries over between rows', async () => {
    const firstRender = renderPage('operator', ['/secret-sync/values-prod-eu-datadog'])
    const first = await screen.findByTestId('secret-detail-panel') // match
    fireEvent.click(within(first).getByTestId('view-comparison-toggle'))
    expect(within(first).getByTestId('comparison-provenance')).toBeInTheDocument()
    firstRender.unmount()

    renderPage('operator', ['/secret-sync/connection-prod-eu']) // also a match
    const second = await screen.findByTestId('secret-detail-panel')
    expect(within(second).getByTestId('view-comparison-toggle')).toHaveTextContent('View comparison')
    expect(within(second).queryByTestId('comparison-provenance')).not.toBeInTheDocument()
  })

  // SSF-14 item 2, first half: the reader's own open/closed choice survives
  // a Check again as long as the result doesn't flip from healthy to
  // broken.
  it('keeps the comparison OPEN across Check again when the row is still a match afterward', async () => {
    mockRefreshAddonValuesSecret.mockResolvedValue({ message: 'checked' })
    renderPage()
    const panel = await openRow('values-prod-eu-datadog') // in_sync -> match, starts closed
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    expect(within(panel).getByTestId('comparison-provenance')).toBeInTheDocument()

    fireEvent.click(within(panel).getByTestId('detail-refresh')) // Check again — response is still in_sync
    await waitFor(() => expect(mockRefreshAddonValuesSecret).toHaveBeenCalled())

    expect(within(panel).getByTestId('comparison-provenance')).toBeInTheDocument()
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveTextContent('Hide comparison')
  })

  // SSF-14 item 2, second half: the reader's own CLOSED choice on a healthy
  // row does not survive a Check again that turns up a real problem — the
  // comparison forces back open so a new problem is never hidden.
  it('forces the comparison OPEN across Check again when the result flips healthy -> broken', async () => {
    mockRefreshAddonValuesSecret.mockResolvedValue({ message: 'checked' })
    renderPage()
    const panel = await openRow('values-prod-eu-datadog') // in_sync -> match, starts closed
    expect(within(panel).queryByTestId('comparison-provenance')).not.toBeInTheDocument()

    // The next getManagedSecrets (fired by onChanged() after Check again)
    // reports the SAME row now out of sync.
    mockGetManagedSecrets.mockResolvedValueOnce({
      ...response,
      addon_values_secrets: response.addon_values_secrets.map((r) =>
        r.cluster === 'prod-eu' && r.addon === 'datadog' ? { ...r, state: 'out_of_sync' } : r,
      ),
    })

    fireEvent.click(within(panel).getByTestId('detail-refresh'))
    await waitFor(() =>
      expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy does not match AWS Secrets Manager.'),
    )

    expect(within(panel).getByTestId('comparison-provenance')).toBeInTheDocument()
    expect(within(panel).getByTestId('view-comparison-toggle')).toHaveTextContent('Hide comparison')
  })
})

describe('SSF-14 item 4 — Resource details and Keys are gone from Overview; SSF-14 item 6 — Recent activity is a link, not a list', () => {
  it('never renders the old Resource details / Keys / Recent activity accordions', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    await waitFor(() => expect(within(panel).getByTestId('detail-related-events-link')).toBeInTheDocument())

    expect(within(panel).queryByTestId('detail-resource-disclosure')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('detail-keys-disclosure')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('detail-activity-disclosure')).not.toBeInTheDocument()
    expect(within(panel).queryByText('Recent activity')).not.toBeInTheDocument()
    expect(within(panel).queryByText("Sharko's record")).not.toBeInTheDocument()
  })

  it('the "View related events" link points at the audit log, pre-filtered to this row\'s cluster', async () => {
    renderPage()
    const panel = await openRow('connection-drifted-eu')
    const link = await within(panel).findByTestId('detail-related-events-link')
    expect(link).toHaveTextContent('View related events')
    expect(link).toHaveAttribute('href', '/audit?cluster=drifted-eu')
  })

  it('a current failure still shows even with the activity list gone — removing it never hides an active problem', async () => {
    renderPage()
    const panel = await openRow('values-flaky-eu-datadog') // unknown, with a lastCheckError
    expect(within(panel).getByTestId('last-check-error')).toHaveTextContent("Sharko couldn't connect to this cluster.")
    expect(within(panel).getByTestId('detail-related-events-link')).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// SSF-12 — the ONE health conclusion
// ─────────────────────────────────────────────────────────────────────────────

describe('SSF-12 — the one health conclusion', () => {
  it('a healthy connection row says "In sync" with the exact source-named sentence, no repair note', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu') // in_sync -> match
    const conclusion = within(panel).getByTestId('detail-health-conclusion')
    expect(within(conclusion).getByTestId('detail-conclusion-label')).toHaveTextContent('In sync')
    expect(within(conclusion).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy matches Git. No action is needed.')
    expect(within(conclusion).queryByTestId('detail-repair-note')).not.toBeInTheDocument()
  })

  it('a healthy addon-values row names the real configured store, never "Git"', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog') // in_sync -> match
    const conclusion = within(panel).getByTestId('detail-health-conclusion')
    expect(within(conclusion).getByTestId('detail-conclusion-label')).toHaveTextContent('In sync')
    expect(within(conclusion).getByTestId('diff-verdict')).toHaveTextContent(
      'The cluster copy matches AWS Secrets Manager. No action is needed.',
    )
  })

  it('a broken connection row says "Needs attention" and promises only the labels — never that the copy will match Git (HL-1)', async () => {
    renderPage()
    const panel = await openRow('connection-drifted-eu') // out_of_sync -> differ
    const conclusion = within(panel).getByTestId('detail-health-conclusion')
    expect(within(conclusion).getByTestId('detail-conclusion-label')).toHaveTextContent('Needs attention')
    expect(within(conclusion).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy does not match Git.')
    // HL-1: the old sentence here ("Sync will update the cluster copy to
    // match Git.") was untrue — the action re-applies only Sharko's own
    // addon label keys. The note now promises exactly that.
    expect(within(conclusion).getByTestId('detail-repair-note')).toHaveTextContent(
      "Re-apply addon labels puts git's addon labels back on this secret. Nothing else on it changes.",
    )
  })

  it('a broken addon-values row says "Needs attention", names the real store, and promises what Sync repairs', async () => {
    renderPage()
    const panel = await openRow('values-staging-us-datadog') // out_of_sync -> differ
    const conclusion = within(panel).getByTestId('detail-health-conclusion')
    expect(within(conclusion).getByTestId('detail-conclusion-label')).toHaveTextContent('Needs attention')
    expect(within(conclusion).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy does not match AWS Secrets Manager.')
    expect(within(conclusion).getByTestId('detail-repair-note')).toHaveTextContent(
      'Sync will update the cluster copy to match AWS Secrets Manager.',
    )
  })

  it('shows freshness ("Checked …") in the conclusion on both tabs', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu')
    expect(within(panel).getByTestId('detail-checked-line')).toBeInTheDocument()
    fireEvent.click(within(panel).getByTestId('detail-tab-yaml'))
    await screen.findByTestId('detail-yaml-hidden')
    expect(within(panel).getByTestId('detail-checked-line')).toBeInTheDocument()
  })

  it('explains why Sync is unavailable for a foreign row instead of leaving an unexplained disabled button', async () => {
    const user = userEvent.setup()
    renderPage()
    const panel = await openRow('values-byo-cluster-datadog') // foreign
    const syncButton = within(panel).getByTestId('detail-sync')
    expect(syncButton).toBeDisabled()
    await user.click(within(panel).getByLabelText('Why is Sync unavailable?'))
    expect(await screen.findByText(/Someone else created this one/)).toBeInTheDocument()
  })

  it('the conclusion is an accessible status region a screen reader announces on change', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu')
    expect(within(panel).getByTestId('detail-health-conclusion')).toHaveAttribute('role', 'status')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// SSF-14 item 1 — Overview is, and stays, the default tab
// ─────────────────────────────────────────────────────────────────────────────

describe('SSF-14 item 1 — Overview is the default tab', () => {
  it('opens on Overview, not YAML, for a healthy row', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu') // in_sync -> match
    expect(within(panel).getByTestId('detail-tab-overview')).toHaveAttribute('aria-pressed', 'true')
    expect(within(panel).getByTestId('detail-tab-yaml')).toHaveAttribute('aria-pressed', 'false')
    expect(within(panel).queryByTestId('detail-yaml-content')).not.toBeInTheDocument()
  })

  it('opens on Overview, not YAML, for a broken row too', async () => {
    renderPage()
    const panel = await openRow('connection-drifted-eu') // out_of_sync -> differ
    expect(within(panel).getByTestId('detail-tab-overview')).toHaveAttribute('aria-pressed', 'true')
    expect(within(panel).queryByTestId('detail-yaml-content')).not.toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// SSF-14 item 7 — the disclosure control reads at interface size (14px),
// not the old 12px it rendered at (measured on cd1d76c4). Vitest/jsdom
// don't compute real font sizes, so this pins the Tailwind class instead —
// text-sm is 14px in this project's type scale (SSF-13 already established
// controls/nav/table/buttons/forms at text-sm), never text-xs (12px).
// ─────────────────────────────────────────────────────────────────────────────

describe('SSF-14 item 7 — the comparison toggle is 14px, not 12px', () => {
  it('the View comparison / Hide comparison button carries text-sm, never text-xs', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    const toggle = within(panel).getByTestId('view-comparison-toggle')
    expect(toggle.className).toMatch(/\btext-sm\b/)
    expect(toggle.className).not.toMatch(/\btext-xs\b/)
  })
})
