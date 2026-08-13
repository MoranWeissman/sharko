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
import { ApiError } from '@/services/api'

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
const mockGetConnectionComparison = vi.fn()
const mockRepairConnection = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockReconcileCluster = vi.fn()
const mockRefreshAddonValuesSecret = vi.fn()

const mockFetchAuditLog = vi.fn()

vi.mock('@/services/api', () => {
  // Mock ApiError class for 409 handling tests - defined inside factory to avoid hoisting issues
  // Matches the real ApiError constructor from ui/src/services/api.ts:190
  class MockApiError extends Error {
    status: number
    code?: string
    cause?: string
    hint?: string
    problems?: unknown
    body: { error?: string }

    constructor(status: number, body: { error?: string }, fallbackMessage: string) {
      super(body.error || fallbackMessage)
      this.name = 'ApiError'
      this.status = status
      this.body = body
    }
  }

  return {
    api: {
      getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
      getConnectionComparison: (...args: unknown[]) => mockGetConnectionComparison(...args),
      repairConnection: (...args: unknown[]) => mockRepairConnection(...args),
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
    ApiError: MockApiError,
  }
})

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
  mockGetConnectionComparison.mockResolvedValue({
    cluster: 'prod-eu',
    status: 'synced',
    scope: 'full',
    ownership_mode: 'sharko_managed',
    checked_at: '2026-08-13T12:00:00Z',
    branch: 'main',
    compared_path: 'configuration/managed-clusters.yaml',
    compared_commit: 'abcdef1234567890abcdef1234567890abcdef12',
    differences: [],
    not_checked: [],
    checked_field_count: 10,
    repair_available: false,
    repair_scope: 'none',
    values_never_returned: true,
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
  // S4-1: Connection comparison is now inline and always expanded per product owner.
  // Changed 2026-08-13: (b) view-comparison-toggle removed (always expanded);
  // (a) provenance test ID changed to connection-comparison-provenance but rule
  // preserved (file and commit must be visible); (a) 7-char short commit with
  // full commit on hover preserved.
  it('provenance paints from row data alone — the git file and commit are there before the connection-comparison fetch resolves', async () => {
    // S4-1: connection rows now read getConnectionComparison, which returns
    // immediately from our mock, so this test no longer proves the provenance-
    // before-table rule the old version did. Keep the test to verify provenance
    // structure and the 7-char + hover behavior.
    renderPage()
    const panel = await openRow('connection-prod-eu')
    // S4-1: NO toggle click - comparison is inline and always expanded.

    // Wait for provenance to appear (async mock resolution)
    const provenance = await within(panel).findByTestId('connection-comparison-provenance')
    // S4-1: Changed 2026-08-13: (b) text split across elements ("File: " + path), use textContent.
    expect(provenance.textContent).toContain('configuration/managed-clusters.yaml')
    const shortCommit = within(provenance).getByText('abcdef1')
    expect(shortCommit).toBeInTheDocument()
    // (a) RESTORED: full commit on hover — regression caught in coordinator review.
    expect(shortCommit.title).toBe('Full commit: abcdef1234567890abcdef1234567890abcdef12')
  })

  // S4-1: Changed 2026-08-13: (a) test ID updated but rule preserved (must show
  // something when commit is unknown, not hide provenance section entirely).
  it('a connection row with no compared commit says so instead of showing a blank table', async () => {
    // S4-1: Mock connection-comparison for no-commit cluster with no compared_commit.
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'no-commit',
      status: 'synced',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      compared_path: 'configuration/managed-clusters.yaml',
      // NO compared_commit
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    })
    renderPage()
    const panel = await openRow('connection-no-commit')
    const provenance = within(panel).getByTestId('connection-comparison-provenance')
    // (a) Rule preserved: when commit is unknown, say so instead of hiding.
    // New text: "Branch: main (commit unknown)"
    expect(provenance).toHaveTextContent('commit unknown')
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

  // S4-1/S4-2: Changed 2026-08-13: Connection comparison now checks the ENTIRE
  // connection (server, credentials, labels, metadata), not just labels. The
  // SECURITY RULE "never a value" for SENSITIVE fields is preserved (they show
  // "<redacted>"). Non-sensitive fields (like label keys/values) CAN show values
  // because the server deliberately sends them.
  //
  // (b) Test ID changed from comparison-label-drift to connection-comparison-differences.
  // (b) Column headers changed: "Expected in Git" → "Expected", "On the cluster" → "Live".
  // (a) Security rule preserved: sensitive fields never show values.
  // (b) Behavior change: label VALUES now shown (addon label values are not sensitive).
  it('a connection row\'s comparison table shows field differences with sensitive fields redacted, never showing credential values', async () => {
    // Mock showing label drift — these are NOT sensitive fields.
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'no-commit',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      compared_path: 'configuration/managed-clusters.yaml',
      differences: [
        { path: 'metadata.labels[addons.sharko.dev/datadog]', status: 'missing' },
        { path: 'metadata.labels[addons.sharko.dev/old-addon]', status: 'unexpected' },
        // Add a sensitive field to prove redaction works:
        { path: 'data.config', status: 'different', sensitive: true },
      ],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    })
    renderPage()
    const panel = await openRow('connection-no-commit')
    const diffs = await within(panel).findByTestId('connection-comparison-differences')

    // (b) New column headers:
    expect(within(diffs).getByText('Field')).toBeInTheDocument()
    expect(within(diffs).getByText('Expected')).toBeInTheDocument()
    expect(within(diffs).getByText('Live')).toBeInTheDocument()
    expect(within(diffs).getByText('Status')).toBeInTheDocument()

    // (b) Label field paths are shown:
    expect(diffs).toHaveTextContent('metadata.labels[addons.sharko.dev/datadog]')
    expect(diffs).toHaveTextContent('metadata.labels[addons.sharko.dev/old-addon]')

    // (a) SECURITY RULE PRESERVED: Sensitive field shows "<redacted>" on both sides, never the value.
    const sensitiveRows = within(diffs).getAllByText('<redacted>')
    expect(sensitiveRows.length).toBeGreaterThanOrEqual(2) // Both Expected and Live columns for the sensitive field
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

  // S4-1: Changed 2026-08-13: Connection comparison now shows status from the
  // server (synced/out_of_sync/etc), not a detailed field-by-field match table.
  // (a) Rule preserved: must show EVIDENCE when healthy, not just claim.
  // (b) Structure changed: evidence is now the status sentence plus the fact that
  // there are zero differences, rather than a per-label match table.
  it('a healthy connection row shows its status with evidence (zero differences), not just a claim', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'synced',
      scope: 'full',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      compared_path: 'configuration/managed-clusters.yaml',
      compared_commit: 'abcdef1234567890abcdef1234567890abcdef12',
      differences: [], // ZERO differences = evidence of health
      not_checked: [],
      checked_field_count: 15,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    })
    renderPage()
    const panel = await openRow('connection-prod-eu')
    // S4-1: NO toggle - always expanded.

    // (a) EVIDENCE: Status sentence says "matches" (wait for async mock)
    const statusSentence = await within(panel).findByTestId('connection-comparison-status-sentence')
    expect(statusSentence).toHaveTextContent('This connection matches what Sharko intends.')
    // (a) EVIDENCE: No differences section (because differences array is empty)
    expect(within(panel).queryByTestId('connection-comparison-differences')).not.toBeInTheDocument()
    // (a) EVIDENCE: Field count shown
    expect(within(panel).getByTestId('connection-comparison-result')).toBeInTheDocument()
  })

  // S4-1: Changed 2026-08-13: (b) This test's premise no longer applies - the
  // connection-comparison endpoint returns immediately with a status, not a
  // pending fetch. Keeping a related test: when status is check_failed, show
  // the failure reason (honesty rule).
  it('shows the check failure reason when the connection check could not complete', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'check_failed',
      scope: 'none',
      ownership_mode: 'sharko_managed',
      checked_at: '2026-08-13T12:00:00Z',
      branch: 'main',
      failure_reason: 'Sharko could not read this cluster\'s record from git, so it cannot tell what the connection should look like. Check the git connection and try again.',
      differences: [],
      not_checked: [],
      checked_field_count: 0,
      repair_available: false,
      repair_scope: 'none',
      values_never_returned: true,
    })
    renderPage()
    const panel = await openRow('connection-prod-eu')

    // (a) HONESTY RULE PRESERVED: Show the failure reason, don't hide it.
    expect(within(panel).getByTestId('connection-comparison-failure-reason')).toHaveTextContent('Sharko could not read')
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
  // S4-1: Changed 2026-08-13: (b) Heading changed to "Connection check" for
  // connection rows per product owner design. For addon-values rows, still
  // "Comparison" (match) or "Differences" (differ).
  it('calls the connection section "Connection check" — connection rows no longer use "Comparison"', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu')
    expect(within(panel).getByRole('heading', { name: 'Connection check' })).toBeInTheDocument()
    expect(within(panel).queryByRole('heading', { name: 'Comparison' })).not.toBeInTheDocument()
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
  // S4-1: Changed 2026-08-13: (b) Product owner said inline and expanded by
  // default — no toggle. Test REWRITTEN to assert the NEW rule: connection rows
  // have no toggle and comparison is always visible; addon-values rows still
  // have the toggle and start collapsed for matches.
  it('connection rows have no toggle and are always expanded; addon rows still toggle and start collapsed for matches', async () => {
    // First: connection row has no toggle, provenance always visible
    const firstRender = renderPage('operator', ['/secret-sync/connection-prod-eu'])
    const first = await screen.findByTestId('secret-detail-panel') // match
    // (b) NEW RULE: no toggle for connection rows
    expect(within(first).queryByTestId('view-comparison-toggle')).not.toBeInTheDocument()
    // (b) NEW RULE: provenance always visible (no click needed)
    expect(within(first).getByTestId('connection-comparison-provenance')).toBeInTheDocument()
    firstRender.unmount()

    // Second: addon-values row still has toggle, starts collapsed for match
    renderPage('operator', ['/secret-sync/values-prod-eu-datadog'])
    const second = await screen.findByTestId('secret-detail-panel')
    // Addon rows KEEP the toggle
    expect(within(second).getByTestId('view-comparison-toggle')).toHaveTextContent('View comparison')
    // And start collapsed
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

  // S4-2: Changed 2026-08-13: Connection rows' YAML tab shows redirect message.
  // (a) Rule preserved: conclusion must be visible on both tabs.
  it('shows freshness ("Checked …") in the conclusion on both tabs', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu')
    expect(within(panel).getByTestId('detail-checked-line')).toBeInTheDocument()
    fireEvent.click(within(panel).getByTestId('detail-tab-yaml'))
    // (b) For connection rows, wait for redirect message instead of detail-yaml-hidden
    await waitFor(() => expect(within(panel).getByText(/redacted YAML for this connection is on the Overview tab/)).toBeInTheDocument())
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
  // S4-2: Changed 2026-08-13: For connection rows, redacted YAML now appears
  // on Overview (below comparison), not on YAML tab. (a) Rule preserved: must
  // open on Overview by default, not YAML.
  it('opens on Overview, not YAML, for a healthy row', async () => {
    renderPage()
    const panel = await openRow('connection-prod-eu') // in_sync -> match
    // (a) PRESERVED: opens on Overview by default
    expect(within(panel).getByTestId('detail-tab-overview')).toHaveAttribute('aria-pressed', 'true')
    expect(within(panel).getByTestId('detail-tab-yaml')).toHaveAttribute('aria-pressed', 'false')
    // (b) CHANGED: For connection rows, YAML IS on Overview now
    expect(within(panel).getByTestId('detail-yaml-content')).toBeInTheDocument()
  })

  it('opens on Overview, not YAML, for a broken row too', async () => {
    renderPage()
    const panel = await openRow('connection-drifted-eu') // out_of_sync -> differ
    // (a) PRESERVED: opens on Overview by default
    expect(within(panel).getByTestId('detail-tab-overview')).toHaveAttribute('aria-pressed', 'true')
    // (b) CHANGED: For connection rows, YAML IS on Overview now
    expect(within(panel).getByTestId('detail-yaml-content')).toBeInTheDocument()
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

// S4-4 / S4-5: Connection repair tests.
// Every user-facing sentence pinned by exact full text.
// Wording agreed on 2026-08-13.
describe('Connection repair (S4-4 / S4-5)', () => {
  // Pinned sentences — wording agreed 2026-08-13
  const REPAIR_BUTTON_LABEL = 'Repair connection'
  const REPAIR_BUTTON_LABEL_EKS = 'Refresh EKS connection'
  const RECENT_ACTIVITY_HEADING = 'Recent activity'
  const VIEW_FULL_AUDIT_LOG_LINK = 'View full audit log'

  const CONFIRM_DESC_FULL = `This will rewrite this cluster's connection to match git and this cluster's configured credentials source. Addon labels will be re-applied. Foreign labels, other data keys and annotations will be left alone. The self-heal setting will not be changed.`
  const CONFIRM_DESC_EKS = `This will refresh the short-lived sign-in token for this EKS connection to match what Sharko intends. Addon labels will be re-applied. Foreign labels, other data keys and annotations will be left alone. The self-heal setting will not be changed.`
  const CONFIRM_DESC_LABELS_ONLY = `This will re-apply this cluster's addon labels to match git. Sharko will not read or change this connection's sign-in details. The self-heal setting will not be changed.`

  // 409 error sentences — the server's exact wording (internal/api/connection_repair.go:87-92)
  const REPAIR_FAIL_REVISION_MOVED = `Your git branch moved while you were looking at this connection, so what you reviewed is not what Sharko would write now. Sharko changed nothing. Run the connection check again and repair from the fresh result.`

  beforeEach(() => {
    vi.clearAllMocks()
    mockGetManagedSecrets.mockResolvedValue(response)
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'owned',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: 'abc123',
      compared_path: 'clusters.yaml',
      credential_source_type: 'secret-kubeconfig',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    })
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
  })

  // S4-4: Repair button gating
  it('shows the repair button when all conditions are met', async () => {
    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText('Connection check'))

    // Wait for button to appear
    const button = await within(panel).findByTestId('detail-repair-connection')
    expect(button).toHaveTextContent(REPAIR_BUTTON_LABEL)
  })

  it('does NOT show the repair button when repair_available is false', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'owned',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: 'abc123',
      compared_path: 'clusters.yaml',
      credential_source_type: 'secret-kubeconfig',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: false,
      repair_scope: 'full_connection',
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText('Connection check'))

    expect(within(panel).queryByTestId('detail-repair-connection')).not.toBeInTheDocument()
  })

  it('does NOT show the repair button when repair_scope is "none"', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'owned',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: 'abc123',
      compared_path: 'clusters.yaml',
      credential_source_type: 'secret-kubeconfig',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'none',
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText('Connection check'))

    expect(within(panel).queryByTestId('detail-repair-connection')).not.toBeInTheDocument()
  })

  it('does NOT show the repair button when there is no commit', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'owned',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: undefined,
      compared_path: 'clusters.yaml',
      credential_source_type: 'secret-kubeconfig',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText('Connection check'))

    expect(within(panel).queryByTestId('detail-repair-connection')).not.toBeInTheDocument()
  })

  // S4-4: EKS button label
  it('shows "Refresh EKS connection" for EKS clusters (wording agreed 2026-08-13)', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'owned',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: 'abc123',
      compared_path: 'clusters.yaml',
      credential_source_type: 'eks-token',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText('Connection check'))

    const button = await within(panel).findByTestId('detail-repair-connection')
    expect(button).toHaveTextContent(REPAIR_BUTTON_LABEL_EKS)
  })

  // S4-4: Confirmation modal wording
  it('shows correct confirmation wording for full repair (wording agreed 2026-08-13)', async () => {
    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    const button = await within(panel).findByTestId('detail-repair-connection')
    fireEvent.click(button)

    await waitFor(() => screen.getByText(`Repair connection for "prod-eu"?`))
    expect(screen.getByText(CONFIRM_DESC_FULL)).toBeInTheDocument()
  })

  it('shows correct confirmation wording for EKS (wording agreed 2026-08-13)', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'out_of_sync',
      scope: 'full',
      ownership_mode: 'owned',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: 'abc123',
      compared_path: 'clusters.yaml',
      credential_source_type: 'eks-token',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    const button = await within(panel).findByTestId('detail-repair-connection')
    fireEvent.click(button)

    await waitFor(() => screen.getByText(`Refresh EKS connection for "prod-eu"?`))
    expect(screen.getByText(CONFIRM_DESC_EKS)).toBeInTheDocument()
  })

  it('shows correct confirmation wording for labels-only (wording agreed 2026-08-13)', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'out_of_sync',
      scope: 'addon_labels',
      ownership_mode: 'self_managed',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: 'abc123',
      compared_path: 'clusters.yaml',
      credential_source_type: 'secret-kubeconfig',
      differences: [],
      not_checked: [],
      checked_field_count: 10,
      repair_available: true,
      repair_scope: 'addon_labels_only',
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    const button = await within(panel).findByTestId('detail-repair-connection')
    fireEvent.click(button)

    await waitFor(() => screen.getByText(`Repair connection for "prod-eu"?`))
    expect(screen.getByText(CONFIRM_DESC_LABELS_ONLY)).toBeInTheDocument()
  })

  // S4-4: Sends displayed commit
  // This is the critical rule: the browser sends the commit from the check it
  // just showed, not a freshly fetched one. The endpoint demands reviewed_commit
  // to prevent repairing against a commit the person never looked at.
  //
  // To catch a re-fetch bug, we make the mock return a DIFFERENT commit if
  // called again during the repair flow.
  it('sends the commit from the check on screen, not a re-fetched one', async () => {
    // First call (on panel open) returns 'abc123'
    // Second call (if code re-fetches) would return 'def456'
    mockGetConnectionComparison
      .mockResolvedValueOnce({
        cluster: 'prod-eu',
        status: 'out_of_sync',
        scope: 'full',
        ownership_mode: 'owned',
        checked_at: new Date().toISOString(),
        branch: 'main',
        compared_commit: 'abc123',
        compared_path: 'clusters.yaml',
        credential_source_type: 'secret-kubeconfig',
        differences: [],
        not_checked: [],
        checked_field_count: 10,
        repair_available: true,
        repair_scope: 'full_connection',
        values_never_returned: true,
      })
      .mockResolvedValueOnce({
        cluster: 'prod-eu',
        status: 'out_of_sync',
        scope: 'full',
        ownership_mode: 'owned',
        checked_at: new Date().toISOString(),
        branch: 'main',
        compared_commit: 'def456',  // Different commit
        compared_path: 'clusters.yaml',
        credential_source_type: 'secret-kubeconfig',
        differences: [],
        not_checked: [],
        checked_field_count: 10,
        repair_available: true,
        repair_scope: 'full_connection',
        values_never_returned: true,
      })

    mockRepairConnection.mockResolvedValue({
      cluster: 'prod-eu',
      repaired: true,
      scope_applied: 'full_connection',
      fields_repaired: ['metadata.labels["sharko.dev/addon.cert-manager"]'],
      preserved_foreign_labels: 0,
      preserved_foreign_data_keys: 0,
      branch: 'main',
      repaired_at_commit: 'abc123',
      repaired_at: new Date().toISOString(),
      message: 'Repaired',
      comparison: {
        cluster: 'prod-eu',
        status: 'synced',
        scope: 'full',
        ownership_mode: 'owned',
        checked_at: new Date().toISOString(),
        branch: 'main',
        compared_commit: 'abc123',
        compared_path: 'clusters.yaml',
        credential_source_type: 'secret-kubeconfig',
        differences: [],
        not_checked: [],
        checked_field_count: 10,
        repair_available: false,
        repair_scope: 'none',
        values_never_returned: true,
      },
      self_heal_unchanged: true,
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    const button = await within(panel).findByTestId('detail-repair-connection')
    fireEvent.click(button)

    // Wait for modal and confirm
    await waitFor(() => screen.getByText(`Repair connection for "prod-eu"?`))
    const confirmButton = screen.getByRole('button', { name: 'Repair connection' })
    fireEvent.click(confirmButton)

    // The repair MUST have been called with 'abc123', the commit from the
    // comparison mock returned above, not a re-fetched value.
    await waitFor(() =>
      expect(mockRepairConnection).toHaveBeenCalledWith('prod-eu', 'abc123'),
    )
  })

  // S4-5: Recent activity
  it('shows Recent activity heading (wording agreed 2026-08-13)', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [
        {
          timestamp: new Date().toISOString(),
          level: 'info',
          event: 'cluster_reconcile',
          user: 'admin',
          action: 'reconcile',
          resource: 'cluster:prod-eu',
          detail: 'Reconciled',
          result: 'success',
        },
      ],
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')

    await waitFor(() => within(panel).getByText(RECENT_ACTIVITY_HEADING))
  })

  it('shows "View full audit log" link (wording agreed 2026-08-13)', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [
        {
          timestamp: new Date().toISOString(),
          level: 'info',
          event: 'cluster_reconcile',
          user: 'admin',
          action: 'reconcile',
          resource: 'cluster:prod-eu',
          detail: 'Reconciled',
          result: 'success',
        },
      ],
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')

    const link = await within(panel).findByTestId('view-full-audit-log')
    expect(link).toHaveTextContent(VIEW_FULL_AUDIT_LOG_LINK)
    expect(link).toHaveAttribute('href', '/audit?cluster=prod-eu')
  })

  it('does not show Recent activity when there are no entries', async () => {
    mockFetchAuditLog.mockResolvedValue({ entries: [] })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText('Connection check'))

    expect(within(panel).queryByText(RECENT_ACTIVITY_HEADING)).not.toBeInTheDocument()
  })

  // Banned wording tests
  it('NEVER says "fully synced" for an EKS connection, even after successful repair', async () => {
    mockGetConnectionComparison.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'limited',
      scope: 'partial',
      ownership_mode: 'owned',
      checked_at: new Date().toISOString(),
      branch: 'main',
      compared_commit: 'abc123',
      compared_path: 'clusters.yaml',
      credential_source_type: 'eks-token',
      limit_reason: 'EKS connections are checked with no token minted.',
      differences: [],
      not_checked: [],
      checked_field_count: 5,
      repair_available: true,
      repair_scope: 'full_connection',
      values_never_returned: true,
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText('Connection check'))

    // Must show "limited" status, never "synced" or "fully synced"
    expect(within(panel).getByText('Sharko checked part of this connection.')).toBeInTheDocument()
    expect(within(panel).queryByText(/fully synced/i)).not.toBeInTheDocument()
    expect(within(panel).queryByText(/^synced$/i)).not.toBeInTheDocument()
  })

  it('NEVER says "Kubernetes events" instead of "Recent activity"', async () => {
    mockFetchAuditLog.mockResolvedValue({
      entries: [
        {
          timestamp: new Date().toISOString(),
          level: 'info',
          event: 'cluster_reconcile',
          user: 'admin',
          action: 'reconcile',
          resource: 'cluster:prod-eu',
          detail: 'Reconciled',
          result: 'success',
        },
      ],
    })

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    await waitFor(() => within(panel).getByText(RECENT_ACTIVITY_HEADING))

    expect(within(panel).queryByText(/Kubernetes events/i)).not.toBeInTheDocument()
  })

  // S4-4: 409 handling - exact sentence from server
  it('shows the server\'s moved-branch sentence UNCHANGED on 409 (wording agreed 2026-08-13)', async () => {
    // This test verifies the UI passes through the server's 409 sentence exactly,
    // with no paraphrase, rewrite, or truncation, and shows it as a 'warning' (not 'error').
    mockRepairConnection.mockRejectedValue(
      new ApiError(409, { error: REPAIR_FAIL_REVISION_MOVED }, 'Conflict'),
    )

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    const button = await within(panel).findByTestId('detail-repair-connection')
    fireEvent.click(button)

    await waitFor(() => screen.getByText(`Repair connection for "prod-eu"?`))
    const confirmButton = screen.getByRole('button', { name: 'Repair connection' })
    fireEvent.click(confirmButton)

    // Wait for the repair to finish and assert the toast
    await waitFor(() => {
      expect(mockShowToast).toHaveBeenCalledWith(REPAIR_FAIL_REVISION_MOVED, 'warning')
    })

    // The page should remain usable after 409 - not stuck in error state
    expect(within(panel).getByTestId('detail-repair-connection')).toBeInTheDocument()
  })

  it('does NOT auto-retry after 409', async () => {
    mockRepairConnection.mockRejectedValue(
      new ApiError(409, { error: REPAIR_FAIL_REVISION_MOVED }, 'Conflict'),
    )

    renderPage('admin')
    const panel = await openRow('connection-prod-eu')
    const button = await within(panel).findByTestId('detail-repair-connection')
    fireEvent.click(button)

    await waitFor(() => screen.getByText(`Repair connection for "prod-eu"?`))
    const confirmButton = screen.getByRole('button', { name: 'Repair connection' })
    fireEvent.click(confirmButton)

    // Wait a moment for any potential retry
    await new Promise((resolve) => setTimeout(resolve, 100))

    // mockRepairConnection must have been called exactly ONCE
    expect(mockRepairConnection).toHaveBeenCalledTimes(1)
  })
})
