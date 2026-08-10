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
// The two cards
// ─────────────────────────────────────────────────────────────────────────────

describe('the two cards', () => {
  it('the left card paints from row data alone — the git file and commit are there before the label-drift fetch resolves', async () => {
    // SSF-12: a connection row's comparison reads getClusterComparison
    // (diffData), never the live secret read — so THAT is the fetch that
    // must stay pending for this to prove anything.
    mockGetClusterComparison.mockReturnValue(new Promise(() => {}))
    renderPage()
    const panel = await openRow('connection-prod-eu')
    // SSF-8: prod-eu is in_sync (a match) — the comparison box opens behind
    // "View comparison" instead of showing automatically.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

    const intent = within(panel).getByTestId('diff-intent-card')
    expect(within(intent).getByText('configuration/managed-clusters.yaml')).toBeInTheDocument()
    expect(within(intent).getByText('abcdef1')).toBeInTheDocument()

    // ...while the right card is still waiting on the label-drift fetch.
    expect(within(panel).getByTestId('diff-live-card')).toHaveTextContent('Loading…')
  })

  it('a connection row with no compared commit says so instead of showing a blank card', async () => {
    renderPage()
    // no-commit is 'unknown' (could_not_look, not a match) — the comparison
    // box shows automatically, no click needed.
    const panel = await openRow('connection-no-commit')
    expect(within(panel).getByTestId('diff-intent-card')).toHaveTextContent("Sharko hasn't compared this secret against git yet.")
  })

  it('the left card of a values row names the real store and points at the key list', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    // SSF-8: this row is in_sync (a match) — open the comparison first.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    const intent = within(panel).getByTestId('diff-intent-card')
    expect(intent).toHaveTextContent('The values come from AWS Secrets Manager.')
    expect(intent).toHaveTextContent('Git holds a pointer to where each value lives, never the value itself.')
  })

  // SSF-12 honesty rule: a connection row's ONLY comparable field is the
  // addon-label drift git vs cluster — never a value, and never the
  // resource facts (type, labels, annotations) that moved to Resource
  // details below.
  it('a connection row\'s comparison shows the addon-label drift, never a value', async () => {
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
    const liveCard = await within(panel).findByTestId('diff-live-card')
    const drift = await within(liveCard).findByTestId('comparison-label-drift')
    expect(drift).toHaveTextContent('datadog')
    expect(drift).toHaveTextContent('old-addon')
    expect(drift).toHaveTextContent('Missing')
    expect(drift).toHaveTextContent('Present')
  })

  // SSF-12 honesty rule: a values row's ONLY comparable field is key
  // presence — expected keys vs which of them the server saw. Never a
  // value; the mask is fixed-length so it leaks no length either.
  it('a values row\'s comparison shows key presence, and its Resource details show the live labels/annotations with the server blanks', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    // SSF-8: this row is in_sync (a match) — open the comparison first.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    const liveCard = await within(panel).findByTestId('diff-live-card')

    const presence = await within(liveCard).findByTestId('comparison-key-presence')
    expect(presence).toHaveTextContent('api-key')
    expect(presence).toHaveTextContent('app-key')
    expect(presence).toHaveTextContent('Expected — present on the cluster')
    expect(presence).toHaveTextContent('Expected — not on the cluster')
    // Never a value, and never the resource facts that moved to Resource
    // details — type/labels/annotations don't belong in the comparison.
    expect(liveCard).not.toHaveTextContent('type Opaque')

    // The moved facts live in the collapsed Resource details section now.
    const resourceDetails = within(panel).getByTestId('detail-resource-disclosure')
    await waitFor(() => expect(within(resourceDetails).getByTestId('detail-resource-type')).toHaveTextContent('Opaque'))
    const annotations = within(resourceDetails).getByTestId('resource-annotations')
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
    // SSF-8: this row is in_sync (a match) — open the comparison first; the
    // toggle itself is not role-gated, only the live half inside it is.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))

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
    // SSF-8: this row is in_sync (a match) — open the comparison first.
    fireEvent.click(within(panel).getByTestId('view-comparison-toggle'))
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1))

    const before = mockGetManagedSecrets.mock.calls.length
    // Refresh re-reads the list, which hands the panel a brand new row
    // object for the same row. The live card must not blink.
    fireEvent.click(within(panel).getByTestId('detail-refresh'))
    await waitFor(() => expect(mockGetManagedSecrets.mock.calls.length).toBeGreaterThan(before))

    expect(mockGetAddonValuesSecretResource).toHaveBeenCalledTimes(1)
    // The live card's content survived untouched — proven by its
    // key-presence comparison content (SSF-12: a values row's comparison
    // pane shows key presence, not the resource facts that moved to
    // Resource details).
    expect(within(panel).getByTestId('diff-live-card')).toHaveTextContent('api-key')
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
    // The live card's content survived untouched — proven by its
    // key-presence comparison content (SSF-12: a values row's comparison
    // pane shows key presence, not the resource facts that moved to
    // Resource details).
    expect(within(panel).getByTestId('diff-live-card')).toHaveTextContent('api-key')
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
    // SSF-4/SSF-9: the identity prints once now, in the page's own title —
    // not repeated inside detail-resource-header.
    expect(panel).toHaveTextContent('datadog/datadog-secrets')
  })

  it('Space also opens a row\'s own page', async () => {
    renderPage()
    await waitFor(() => expect(screen.getByTestId('secret-row-values-staging-us-datadog')).toBeInTheDocument())
    fireEvent.keyDown(screen.getByTestId('secret-row-values-staging-us-datadog'), { key: ' ' })
    await waitFor(() => expect(mockGetAddonValuesSecretResource).toHaveBeenLastCalledWith('staging-us', 'datadog'))
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The header
// ─────────────────────────────────────────────────────────────────────────────

describe('the resource header', () => {
  it('names the kind and cluster in the header, and shows the live age once the read lands, in Resource details', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    const header = within(panel).getByTestId('detail-resource-header')

    // SSF-4/SSF-12: the identity (name) is stated once — in the page's own
    // title, not repeated in this row — so it's checked at the panel
    // level; kind/cluster still live in detail-resource-header. The age is
    // a live-read fact and moved into the Resource details field grid.
    expect(panel).toHaveTextContent('datadog/datadog-secrets')
    expect(header).toHaveTextContent('Secret')
    expect(header).toHaveTextContent('on prod-eu')
    await waitFor(() => expect(within(panel).getByTestId('detail-resource-created')).toBeInTheDocument())
  })

  it('shows no age at all when the live read never lands — never an invented one', async () => {
    mockGetAddonValuesSecretResource.mockReturnValue(new Promise(() => {}))
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    expect(within(panel).queryByTestId('detail-resource-created')).not.toBeInTheDocument()
  })

  // SSF-4 — the identity used to print once in the sheet's own title and
  // again in Zone A; it now prints exactly once.
  it('states the secret identity exactly once, not twice', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    const matches = (panel.textContent?.match(/datadog\/datadog-secrets/g) ?? []).length
    expect(matches).toBe(1)
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

describe('SSF-8 — comparison on demand', () => {
  it('a matching row shows the one-line result and NOT the two-column comparison, until "View comparison" reveals it', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog') // in_sync -> match
    // The one-line result is up front...
    expect(within(panel).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy matches AWS Secrets Manager. No action is needed.')
    // ...but the two-column box is not rendered until asked for.
    expect(within(panel).queryByTestId('diff-intent-card')).not.toBeInTheDocument()
    expect(within(panel).queryByTestId('diff-live-card')).not.toBeInTheDocument()
    const toggle = within(panel).getByTestId('view-comparison-toggle')
    expect(toggle).toHaveTextContent('View comparison')

    fireEvent.click(toggle)

    expect(within(panel).getByTestId('diff-intent-card')).toBeInTheDocument()
    await waitFor(() => expect(within(panel).getByTestId('diff-live-card')).toBeInTheDocument())
    expect(within(panel).queryByTestId('view-comparison-toggle')).not.toBeInTheDocument()
  })

  it('a differing row shows the two-column comparison straight away, no "View comparison" control at all', async () => {
    renderPage()
    const panel = await openRow('values-staging-us-datadog') // out_of_sync -> differ
    expect(within(panel).getByTestId('diff-intent-card')).toBeInTheDocument()
    await waitFor(() => expect(within(panel).getByTestId('diff-live-card')).toBeInTheDocument())
    expect(within(panel).queryByTestId('view-comparison-toggle')).not.toBeInTheDocument()
  })

  it('a foreign row (a boundary, not a match) shows the two-column comparison straight away too', async () => {
    renderPage()
    const panel = await openRow('values-byo-cluster-datadog') // foreign
    expect(within(panel).getByTestId('diff-intent-card')).toBeInTheDocument()
    expect(within(panel).queryByTestId('view-comparison-toggle')).not.toBeInTheDocument()
  })

  // SSF-9: a different row is its own page/mount now, so "the reveal
  // doesn't carry over" is proven by rendering a SECOND matching row's page
  // fresh (rather than clicking within a still-open drawer) and finding it
  // collapsed, same as the first row was before its own toggle was clicked.
  it('a second matching row\'s page opens collapsed — the "View comparison" reveal never carries over between rows', async () => {
    const firstRender = renderPage('operator', ['/secret-sync/values-prod-eu-datadog'])
    const first = await screen.findByTestId('secret-detail-panel') // match
    fireEvent.click(within(first).getByTestId('view-comparison-toggle'))
    expect(within(first).getByTestId('diff-intent-card')).toBeInTheDocument()
    firstRender.unmount()

    renderPage('operator', ['/secret-sync/connection-prod-eu']) // also a match
    const second = await screen.findByTestId('secret-detail-panel')
    expect(within(second).getByTestId('view-comparison-toggle')).toBeInTheDocument()
    expect(within(second).queryByTestId('diff-intent-card')).not.toBeInTheDocument()
  })
})

describe('SSF-8 — disclosure sections are closed by default', () => {
  it('keeps Resource details, Keys, and Recent activity collapsed until opened', async () => {
    renderPage()
    const panel = await openRow('values-prod-eu-datadog')
    await waitFor(() => expect(within(panel).getByTestId('detail-resource-disclosure')).toBeInTheDocument())

    const resourceDetails = within(panel).getByTestId('detail-resource-disclosure')
    const keys = within(panel).getByTestId('detail-keys-disclosure')
    const activity = within(panel).getByTestId('detail-activity-disclosure')
    expect(resourceDetails).not.toHaveAttribute('open')
    expect(keys).not.toHaveAttribute('open')
    expect(activity).not.toHaveAttribute('open')

    // SSF-12: "Sharko's record" is renamed "Recent activity" everywhere in
    // this path.
    expect(within(activity).getByText('Recent activity')).toBeInTheDocument()
    expect(within(activity).queryByText("Sharko's record")).not.toBeInTheDocument()

    // The namespace/name identity still exists — it just lives in the
    // collapsed Resource details section now, not the sheet's own title.
    expect(within(resourceDetails).getByTestId('detail-identity')).toHaveTextContent('datadog/datadog-secrets')
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

  it('a broken connection row says "Needs attention" and explains what Sync will do — never "Git" softened, never a values source', async () => {
    renderPage()
    const panel = await openRow('connection-drifted-eu') // out_of_sync -> differ
    const conclusion = within(panel).getByTestId('detail-health-conclusion')
    expect(within(conclusion).getByTestId('detail-conclusion-label')).toHaveTextContent('Needs attention')
    expect(within(conclusion).getByTestId('diff-verdict')).toHaveTextContent('The cluster copy does not match Git.')
    expect(within(conclusion).getByTestId('detail-repair-note')).toHaveTextContent('Sync will update the cluster copy to match Git.')
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
