// ManagedSecrets.credentialdrift — W3-3 AC9 + W3-5 discoverability.
//
// The fleet page notices credential drift by itself; repair stays a human's
// click. The background loop (already built server-side, #830) puts three
// new optional fields on a connection row — credential_check,
// credential_check_detail, credential_checked_at. This suite pins:
//
//  - a small badge next to the existing status mark, connection rows only.
//
//    B5 (2026-08-19) narrowed it to TWO states, because four states read off
//    credential_check — a field with no relationship to the canonical answer
//    beside it — is exactly how a green "Synced" ended up next to "Not
//    compared" on the same row:
//      'drifted' -> "Credential drift" (the fixed server sentence on the
//        badge's title); it names WHICH domain drifted, which the headline
//        deliberately does not;
//      verification_scope 'partial' -> "Credential not compared", read off
//        the CANONICAL field the headline itself is derived from, so the two
//        cannot contradict each other;
//      'check_failed' -> NO badge. The headline already says "Unknown —
//        check failed"; a badge repeating it is noise.
//  - 'clear' or absent renders NO badge — a quiet healthy row stays quiet;
//  - values rows never render the badge, regardless of what's on the row;
//  - the "Cluster connections" tile gains a secondary line counting
//    'drifted' rows, ONLY when the count is > 0 — a zero-drift estate sees
//    no change;
//  - W3-5: one help link to the published architecture doc, exact text and
//    href.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
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

const mockGetManagedSecrets = vi.fn()
const mockGetClusterComparison = vi.fn()
const mockGetConnectionComparison = vi.fn()
const mockGetConnectionSecretResource = vi.fn()
const mockGetAddonValuesSecretResource = vi.fn()
const mockFetchAuditLog = vi.fn()

import { withCanonicalConnectionRows } from './connectionRowCanonical'
import { CONNECTION_SENTENCES } from '@/generated/connection-sentences'

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
    getConnectionComparison: (...args: unknown[]) => mockGetConnectionComparison(...args),
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
  triggerSecretsReconcile: vi.fn(),
  checkAllAddonValuesSecrets: vi.fn(),
  reconcileCluster: vi.fn(),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: vi.fn(),
  syncAddonValuesSecret: vi.fn(),
  fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
}))

function renderPage() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={['/secret-sync']}>
        <Routes>
          <Route path="/secret-sync" element={<ManagedSecrets />} />
          <Route path="/secret-sync/:rowKey" element={<SecretDetailPage />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

// The exact fixed sentence the server ships for a drifted connection
// (epic-connection-repair-step4.md, Story W3-3, AC4). Pinned by exact
// words — a paraphrase here would hide a real regression behind a passing
// test.
const DRIFTED_SENTENCE =
  CONNECTION_SENTENCES.credentialDriftNotice

const CREDENTIAL_DRIFT_BADGE_LABEL = 'Credential drift'
const NOT_COMPARED_BADGE_LABEL = 'Credential not compared'

const NOT_COMPARED_SENTENCE_FIXTURE =
  CONNECTION_SENTENCES.limitReasonInlineKubeconfig
const CHECK_FAILED_SENTENCE_FIXTURE = "Sharko could not reach the configured credentials source to check this connection's details."

function baseResponse(overrides: Partial<ManagedSecretsResponse> = {}): ManagedSecretsResponse {
  return {
    cluster_connection_secrets: [
      { cluster: 'prod-eu', secret_namespace: 'argocd', secret_name: 'prod-eu', state: 'in_sync', source: 'git', self_heals: true },
      { cluster: 'staging-us', secret_namespace: 'argocd', secret_name: 'staging-us', state: 'in_sync', source: 'git', self_heals: true },
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
    ],
    engines: {
      cluster_connection: { wired: true, enabled: true, interval_seconds: 30, last_run: '2026-08-05T00:00:00Z' },
      addon_values: { wired: true, enabled: true, interval_seconds: 300, last_run: '2026-08-04T23:55:00Z' },
    },
    addon_values_secret_source: 'AWS Secrets Manager',
    ...overrides,
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  mockFetchAuditLog.mockResolvedValue({ entries: [] })
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
    differences: [],
    not_checked: [],
    checked_field_count: 10,
    repair_available: false,
    repair_scope: 'none',
    values_never_returned: true,
  })
  mockGetConnectionSecretResource.mockResolvedValue({
    kind: 'Secret',
    api_version: 'v1',
    name: 'prod-eu',
    namespace: 'argocd',
    secret_type: 'Opaque',
    created_at: '2026-07-01T00:00:00Z',
    labels: [],
    annotations: [],
    data_keys: [],
    read_from: 'cluster "prod-eu", namespace "argocd"',
    values_blanked: true,
  })
  mockGetAddonValuesSecretResource.mockResolvedValue({
    kind: 'Secret',
    api_version: 'v1',
    name: 'datadog-secrets',
    namespace: 'datadog',
    secret_type: 'Opaque',
    created_at: '2026-07-01T00:00:00Z',
    labels: [],
    annotations: [],
    data_keys: [],
    read_from: 'cluster "prod-eu", namespace "datadog"',
    values_blanked: true,
  })
})

describe('credential-check row badge (W3-3 AC9)', () => {
  it('renders "Credential drift" for a drifted connection, carrying the exact server sentence as its title', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      baseResponse({
        cluster_connection_secrets: [
          {
            cluster: 'prod-eu',
            secret_namespace: 'argocd',
            secret_name: 'prod-eu',
            state: 'in_sync',
            source: 'git',
            self_heals: true,
            credential_check: 'drifted',
            credential_check_detail: DRIFTED_SENTENCE,
            credential_checked_at: '2026-08-16T00:00:00Z',
          },
        ],
      }),
    )
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-prod-eu')
    const badge = within(row).getByTestId('credential-check-badge')
    expect(badge).toHaveTextContent(CREDENTIAL_DRIFT_BADGE_LABEL)
    expect(badge).toHaveAttribute('title', DRIFTED_SENTENCE)
    expect(badge).toHaveAttribute('data-credential-check', 'drifted')
  })

  // B13 item 7. This test used to assert the muted "Credential not compared"
  // chip. The chip is deleted: it sat in the same table cell as the server
  // headline, which on exactly these rows already says the same thing, so
  // the cell said one fact twice — once from the server and once invented in
  // the browser. What is pinned now is that no chip comes back, and that the
  // row says the fact exactly once.
  it('renders NO chip for a partial verification — the server headline is the only place the fact is said', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      baseResponse({
        cluster_connection_secrets: [
          {
            cluster: 'prod-eu',
            secret_namespace: 'argocd',
            secret_name: 'prod-eu',
            // The canonical answer for a clean EKS connection: unknown at
            // partial verification, with the headline carrying the fact.
            state: 'unknown',
            sync_state: 'unknown',
            management_mode: 'sharko_managed',
            managed_scope: 'full_connection',
            verification_scope: 'partial',
            approval_required: false,
            headline: CONNECTION_SENTENCES.headlineConfigurationMatchesEKS,
            health: 'connected',
            source: 'git',
            self_heals: true,
            credential_check: 'not_compared',
            credential_check_detail: NOT_COMPARED_SENTENCE_FIXTURE,
            credential_checked_at: '2026-08-16T00:00:00Z',
          },
        ],
      }),
    )
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-prod-eu')
    expect(within(row).queryByTestId('credential-check-badge')).toBeNull()

    // Exactly once, counted. "Is the sentence present" passes at one copy
    // and at four; only a count fails when a second copy returns.
    const occurrences = (haystack: string, needle: string) => haystack.split(needle).length - 1
    expect(occurrences(
        (row.textContent ?? '').toLowerCase(),
        CONNECTION_SENTENCES.qualifierCredentialNotCompared.toLowerCase(),
      )).toBe(1)
    // The deleted chip's own words never appear at all.
    expect(occurrences((row.textContent ?? '').toLowerCase(), NOT_COMPARED_BADGE_LABEL.toLowerCase())).toBe(0)
    // And the tail the two wordings share is said once, not twice.
    expect(occurrences((row.textContent ?? '').toLowerCase(), 'not compared')).toBe(1)
  })

  it('renders NO badge when the check itself failed — the headline already says "Unknown — check failed"', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      baseResponse({
        cluster_connection_secrets: [
          {
            cluster: 'prod-eu',
            secret_namespace: 'argocd',
            secret_name: 'prod-eu',
            state: 'unknown',
            sync_state: 'unknown',
            management_mode: 'sharko_managed',
            managed_scope: 'full_connection',
            verification_scope: 'none',
            approval_required: false,
            headline: CONNECTION_SENTENCES.headlineUnknownCheckFailed,
            health: 'connected',
            source: 'git',
            self_heals: true,
            credential_check: 'check_failed',
            credential_check_detail: CHECK_FAILED_SENTENCE_FIXTURE,
            credential_checked_at: '2026-08-16T00:00:00Z',
          },
        ],
      }),
    )
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-prod-eu')
    expect(within(row).queryByTestId('credential-check-badge')).not.toBeInTheDocument()
    expect(within(row).getByTestId('status-mark')).toHaveTextContent(CONNECTION_SENTENCES.headlineUnknownCheckFailed)
  })

  it('renders no badge for credential_check "clear" — a quiet healthy row stays quiet', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      baseResponse({
        cluster_connection_secrets: [
          {
            cluster: 'prod-eu',
            secret_namespace: 'argocd',
            secret_name: 'prod-eu',
            state: 'in_sync',
            source: 'git',
            self_heals: true,
            credential_check: 'clear',
            credential_checked_at: '2026-08-16T00:00:00Z',
          },
        ],
      }),
    )
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-prod-eu')
    expect(within(row).queryByTestId('credential-check-badge')).not.toBeInTheDocument()
  })

  it('renders no badge when credential_check is absent — a server that predates the loop, or before its first pass', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse())
    renderPage()

    const row = await screen.findByTestId('secret-row-connection-prod-eu')
    expect(within(row).queryByTestId('credential-check-badge')).not.toBeInTheDocument()
  })

  // W3 review fix (FIX 5): the old version of this test put
  // credential_check on the CONNECTION row's fixture and then checked a
  // DIFFERENT (values) row — trivially true, since that values row's own
  // object never carried the field at all. It never actually proved the
  // kind guard. This version puts the credential_check-shaped fields on the
  // VALUES row's own fixture object (AddonValuesSecretRow has no such
  // field, hence the cast) so the assertion is only true if the rendering
  // code really does gate on row.kind === 'connection', not merely on
  // whether the field happens to be present.
  it('never renders the badge on a VALUES row, even when that row\'s own fixture carries a credential_check-shaped field', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      baseResponse({
        addon_values_secrets: [
          {
            cluster: 'prod-eu',
            addon: 'datadog',
            secret_name: 'datadog-secrets',
            secret_namespace: 'datadog',
            state: 'in_sync',
            source: 'AWS Secrets Manager',
            self_heals: true,
            credential_check: 'drifted',
            credential_check_detail: DRIFTED_SENTENCE,
          } as ManagedSecretsResponse['addon_values_secrets'][number],
        ],
      }),
    )
    renderPage()

    await screen.findByTestId('secret-row-connection-prod-eu')
    const valuesRow = screen.getByTestId('secret-row-values-prod-eu-datadog')
    expect(within(valuesRow).queryByTestId('credential-check-badge')).not.toBeInTheDocument()
  })
})

describe('"Cluster connections" tile — drifted count (W3-3 AC9)', () => {
  it('shows the exact "N with credential drift" line when at least one connection is drifted', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      baseResponse({
        cluster_connection_secrets: [
          { cluster: 'prod-eu', secret_namespace: 'argocd', secret_name: 'prod-eu', state: 'in_sync', source: 'git', self_heals: true, credential_check: 'drifted', credential_check_detail: DRIFTED_SENTENCE },
          { cluster: 'staging-us', secret_namespace: 'argocd', secret_name: 'staging-us', state: 'in_sync', source: 'git', self_heals: true, credential_check: 'drifted', credential_check_detail: DRIFTED_SENTENCE },
          { cluster: 'dev-ap', secret_namespace: 'argocd', secret_name: 'dev-ap', state: 'in_sync', source: 'git', self_heals: true, credential_check: 'clear' },
        ],
      }),
    )
    renderPage()

    await screen.findByTestId('secret-row-connection-prod-eu')
    const line = await screen.findByTestId('engine-credential-drift-count')
    expect(line).toHaveTextContent('2 with credential drift')
  })

  it('shows no drift line at all when the count is zero — a zero-drift estate sees no change', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse())
    renderPage()

    await screen.findByTestId('secret-row-connection-prod-eu')
    expect(screen.queryByTestId('engine-credential-drift-count')).not.toBeInTheDocument()
  })

  it('shows no drift line when every connection is "clear"', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      baseResponse({
        cluster_connection_secrets: [
          { cluster: 'prod-eu', secret_namespace: 'argocd', secret_name: 'prod-eu', state: 'in_sync', source: 'git', self_heals: true, credential_check: 'clear' },
        ],
      }),
    )
    renderPage()

    await screen.findByTestId('secret-row-connection-prod-eu')
    expect(screen.queryByTestId('engine-credential-drift-count')).not.toBeInTheDocument()
  })
})

describe('Secrets architecture doc link (W3-5 discoverability)', () => {
  it('renders "How Sharko manages secrets" linking to the published architecture page', async () => {
    mockGetManagedSecrets.mockResolvedValue(baseResponse())
    renderPage()

    await waitFor(() => expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument())

    const link = screen.getByTestId('secrets-architecture-doc-link')
    expect(link).toHaveTextContent('How Sharko manages secrets')
    // /en/latest/ is the readthedocs prefix the published site serves under —
    // the unprefixed form hard-404s (composed-review blocker 1).
    expect(link).toHaveAttribute('href', 'https://sharko.readthedocs.io/en/latest/architecture/engine-and-secret-sync/')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })
})
