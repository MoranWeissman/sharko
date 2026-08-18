// ConnectionReconciliationView — Story 2+3 of the connection-reconciliation
// epic. What this suite holds down:
//
//  - the 17 page-state matrix v3 rows: for each mocked API response, the
//    headline word, the qualifier, the offered action — and that no
//    UNOFFERED action's button exists (esp. no Repair on legacy_inline,
//    foreign, or withheld rows);
//  - the sync display language names its scope: bare "Synced" NEVER renders
//    for EKS, legacy_inline, or self_managed;
//  - the server's plan sentences render VERBATIM, and the page computes no
//    promise of its own — the old "Sharko will fix this on the next pass"
//    is banned;
//  - the replaced page's wording is banned by name: the teaching block
//    heading and body, "Needs attention", and (ruling b) the whole banned
//    fragment about what Sharko means to do — Git defines the connection;
//  - sensitive drift renders the fixed "<redacted>" on both sides;
//  - Recent activity shows lifecycle events with human titles only — a raw
//    event id (secret_resource_read included) never renders;
//  - the redacted YAML lives behind collapsed Technical evidence, never as
//    primary content;
//  - ruling 4: the initiating control shows Checking…/Repairing… while its
//    own request is in flight, and nothing persists it.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthContext } from '@/hooks/useAuth'
import type { AuditEntry, ConnectionReconciliation } from '@/services/models'
import {
  ConnectionReconciliationView,
  HEADLINE_CONNECTION_SYNCED,
  HEADLINE_ADDON_LABELS_SYNCED,
  HEADLINE_ADDON_LABELS_OUT_OF_SYNC,
  HEADLINE_OUT_OF_SYNC,
  HEADLINE_OUT_OF_SYNC_APPROVAL,
  HEADLINE_OUT_OF_SYNC_CANNOT_RESTORE,
  HEADLINE_BLOCKED,
  HEADLINE_VERIFICATION_INCOMPLETE,
  HEADLINE_EKS_PARTIAL,
  HEADLINE_UNKNOWN_CHECK_FAILED,
  HEADLINE_NOT_CHECKED_YET,
  QUALIFIER_LEGACY_INLINE,
  QUALIFIER_SELF_MANAGED,
  MIGRATE_CREDENTIALS_DOC_URL,
  RECONCILIATION_NEEDS_OPERATOR,
  PLAN_LABEL_AUTOMATIC,
  PLAN_LABEL_REQUIRES_APPROVAL,
  PLAN_LABEL_PRESERVED,
  PLAN_UNTOUCHED_SENTENCE,
} from '@/views/ConnectionReconciliationView'
import { RECENT_ACTIVITY_LABEL, ACTIVITY_EMPTY_SENTENCE, ACTIVITY_FETCH_LIMIT, NO_CHANGES_MADE } from '@/views/connectionActivity'

const mockShowToast = vi.fn()
vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) }
})

const mockGetConnectionReconciliation = vi.fn()
const mockRepairConnection = vi.fn()
const mockFetchAuditLog = vi.fn()
const mockTakeoverPreflight = vi.fn()

vi.mock('@/services/api', () => {
  class MockApiError extends Error {
    status: number
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
      getConnectionReconciliation: (...args: unknown[]) => mockGetConnectionReconciliation(...args),
      repairConnection: (...args: unknown[]) => mockRepairConnection(...args),
    },
    fetchAuditLog: (...args: unknown[]) => mockFetchAuditLog(...args),
    // TakeoverDialog's own imports — inert unless a test opens the dialog.
    takeoverPreflight: (...args: unknown[]) => mockTakeoverPreflight(...args),
    takeoverCluster: vi.fn(),
    dropLegacyLabels: vi.fn(),
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

const FULL_SHA = 'abcdef1234567890abcdef1234567890abcdef12'
const APPLIED_SHA = '9876543210fedcba9876543210fedcba98765432'

// The server's own fixed sentences (pinned server-side in
// internal/api/connection_reconciliation.go) — the page must render them
// VERBATIM, so the fixtures carry the real text.
const S = {
  modeSharko: 'Git defines the connection. Sharko resolves its credential references and maintains the resulting ArgoCD Secret.',
  modeSelf: 'Connection data is managed outside Sharko. Sharko reconciles only its addon-label keys from Git.',
  modeLegacy: "This connection's credential exists only in the live Secret and cannot be restored from Git.",
  modeForeign: 'Another tool owns this connection, so Sharko will not change it.',
  approvalRequired:
    'The live connection no longer matches what git defines. Sharko will not change connection details or credential material by itself — an admin reviews and applies the change through the repair action.',
  labelsOnly: 'Some addon labels on this connection do not match what git declares.',
  legacyDrift:
    'The live connection no longer matches what git defines. Its credential exists only in the live Secret, so Sharko cannot rebuild the connection from Git. Store a fresh credential in a supported credentials provider and move the cluster onto it.',
  missingDurable:
    'This cluster has no connection Secret right now. Sharko will create it from git and the configured credentials source on the reconciler\'s next pass.',
  missingLegacy:
    "This cluster's connection Secret is gone, and its credential existed only in that Secret — Sharko cannot restore it from Git. Store a fresh credential in a supported credentials provider and move the cluster onto it.",
  planAutoCreate: 'Sharko will create this connection Secret from git and the configured credentials source on the reconciler\'s next pass.',
  planAutoLabels: 'Sharko re-applies the addon labels git declares on the reconciler\'s next pass.',
  planApproval:
    'An admin reviews and applies this change through the guarded repair action. Sharko never changes connection details or credential material by itself.',
  eksLimit: 'The credentials source stores EKS cluster details, not a reusable sign-in credential, so credential content is not compared.',
  checkFail: "Sharko could not read this cluster's sign-in details from the configured credentials source.",
  condGitOK: 'The connection definition was read from git.',
  condCredOK: 'The credential reference resolves from the configured credentials source.',
  condOwnOK: 'Sharko owns this connection Secret.',
  condOwnGuest: 'This connection is maintained outside Sharko. Sharko manages only its addon-label keys.',
  condLiveOK: 'The live connection Secret was found.',
  condLiveMissing: 'This cluster has no live connection Secret.',
  condCompFull: 'Every field Sharko owns was compared.',
  condCompDrift: 'At least one compared field differs from the Git-defined connection.',
  condArgoOK: 'ArgoCD reports this connection as working.',
  condApproval: "Applying this change needs an admin's approval through the repair action.",
}

const okConditions = [
  { id: 'git_definition', status: 'ok', detail: S.condGitOK },
  { id: 'credential_reference', status: 'ok', detail: S.condCredOK },
  { id: 'ownership', status: 'ok', detail: S.condOwnOK },
  { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
  { id: 'comparison', status: 'ok', detail: S.condCompFull },
  { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
] as ConnectionReconciliation['conditions']

function makeView(overrides: Partial<ConnectionReconciliation> = {}): ConnectionReconciliation {
  return {
    cluster: 'spoke-eu',
    management_mode: 'sharko_managed',
    managed_scope: 'full_connection',
    mode_statement: S.modeSharko,
    definition: {
      file: 'managed-clusters.yaml',
      branch: 'main',
      desired_revision: FULL_SHA,
      applied_revision: APPLIED_SHA,
      credential_source_type: 'secret-kubeconfig',
    },
    sync: {
      state: 'synced',
      verification_scope: 'full',
      approval_required: false,
      // B5: headline and qualifier now arrive FROM THE SERVER — the page
      // renders them verbatim and selects nothing. Every fixture below
      // states the exact strings the canonical core would send for that
      // row, so a test that changes the state without changing the words
      // is asserting a response the server cannot produce.
      headline: HEADLINE_CONNECTION_SYNCED,
      checked_at: '2026-08-18T10:00:00Z',
      last_successful_application: '2026-08-18T09:00:00Z',
    },
    health: { state: 'connected' },
    conditions: okConditions,
    drift: { connection_configuration: [], credential_material: [], addon_labels: [], not_checked: [] },
    plan: { action: 'none', action_scopes: [] },
    values_never_returned: true,
    ...overrides,
  }
}

function renderView(view: ConnectionReconciliation | Error, role = 'admin', entries: AuditEntry[] = []) {
  if (view instanceof Error) {
    mockGetConnectionReconciliation.mockRejectedValue(view)
  } else {
    mockGetConnectionReconciliation.mockResolvedValue(view)
  }
  mockFetchAuditLog.mockResolvedValue({ entries })
  return render(
    <AuthContext.Provider value={authFor(role)}>
      <MemoryRouter>
        <ConnectionReconciliationView cluster="spoke-eu" onRequestSync={vi.fn()} onChanged={vi.fn()} />
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

async function waitForView() {
  await waitFor(() => expect(screen.getByTestId('recon-view')).toBeTruthy())
}

/**
 * Every render must be free of the replaced page's wording — checked after
 * each matrix row.
 *
 * RULING (b), 2026-08-19: the FRAGMENT "Sharko intends" is banned outright,
 * anywhere on the page. The old list banned two complete verdict SENTENCES,
 * which is precisely how the phrase survived in five Go files at once — a
 * fragment that travelled across files was invisible to every one of them.
 * The ruled replacement, where a full sentence is needed, is exactly:
 * "At least one compared field differs from the Git-defined connection."
 */
function expectNoBannedWording() {
  expect(screen.queryByText(/How Sharko manages this connection/)).toBeNull()
  expect(screen.queryByText(/Git controls the addon labels/)).toBeNull()
  expect(screen.queryByText(/Your configured credentials source controls how ArgoCD connects/)).toBeNull()
  expect(screen.queryByText(/Needs attention/)).toBeNull()
  expect(screen.queryByText(/Sharko will fix this on the next pass/)).toBeNull()
  expect(screen.queryByText(/This connection does not match what Sharko intends\./)).toBeNull()
  expect(screen.queryByText(/This connection matches what Sharko intends/)).toBeNull()
  // The bare fragment, over the whole rendered page — not one sentence in
  // one place.
  expect(screen.getByTestId('recon-view').textContent ?? '').not.toMatch(/sharko intends/i)
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ─────────────────────────────────────────────────────────────────────────────
// The 17 matrix rows
// ─────────────────────────────────────────────────────────────────────────────

describe('page-state matrix v3 — headline, qualifier, offered action per row', () => {
  it('row 1 — clean secret-kubeconfig: "Connection synced", compact conditions, no write action at all', async () => {
    renderView(makeView())
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_CONNECTION_SYNCED)
    expect(screen.queryByTestId('recon-sync-qualifier')).toBeNull()
    // Routine success renders compactly (ruling 5).
    expect(screen.getByTestId('recon-conditions-compact')).toBeTruthy()
    expect(screen.getByTestId('recon-conditions-compact').textContent).toContain('All 6 checks passed.')
    // A healthy state has NO repair action, no sync action, no plan section.
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByTestId('recon-action-sync')).toBeNull()
    expect(screen.queryByTestId('recon-plan')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    // Health renders beside the sync word, independent.
    expect(screen.getByTestId('recon-health').textContent).toContain('Connected')
    expectNoBannedWording()
  })

  it('row 2 — clean EKS: the partial headline, never any form of "Synced"; repair stays offered by policy', async () => {
    renderView(
      makeView({
        definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, credential_source_type: 'eks-token' },
        sync: { headline: HEADLINE_EKS_PARTIAL, state: 'unknown', verification_scope: 'partial', approval_required: false, reason: S.eksLimit, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'attention', detail: S.eksLimit },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
          { id: 'comparison', status: 'attention', detail: S.eksLimit },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        drift: { connection_configuration: [], credential_material: [], addon_labels: [], not_checked: [{ path: 'data.config', reason: S.eksLimit }] },
        plan: { action: 'repair_connection', action_scopes: ['metadata.labels', 'data.name', 'data.server', 'data.config'], reviewed_commit: FULL_SHA },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_EKS_PARTIAL)
    // Bare "Synced" (and "Connection synced") banned for EKS.
    expect(screen.queryByText(/^Synced$/)).toBeNull()
    expect(screen.queryByText(HEADLINE_CONNECTION_SYNCED)).toBeNull()
    // The EKS repair keeps its existing name.
    const repair = screen.getByTestId('recon-action-repair')
    expect(repair.textContent).toContain('Refresh EKS connection')
    // data.config sits in the not-checked table with its reason.
    expect(screen.getByTestId('recon-drift-notchecked').textContent).toContain('data.config')
    expectNoBannedWording()
  })

  it('row 3 — new git revision: "Out of sync — approval required", repair beside the approval condition, confirm shows revision + scopes, repair sends reviewed_commit', async () => {
    mockRepairConnection.mockResolvedValue({ message: 'Repaired.', comparison: {} })
    renderView(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC_APPROVAL, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'ok', detail: S.condCredOK },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
          { id: 'comparison', status: 'attention', detail: S.condCompDrift },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
          { id: 'approval', status: 'blocked', detail: S.condApproval },
        ],
        drift: {
          connection_configuration: [{ path: 'data.server', status: 'different', expected: 'https://new.example.com', live: 'https://old.example.com' }],
          credential_material: [],
          addon_labels: [],
          not_checked: [],
        },
        plan: {
          action: 'repair_connection',
          action_scopes: ['metadata.labels', 'data.name', 'data.server', 'data.config'],
          reviewed_commit: FULL_SHA,
          requires_approval: S.planApproval,
        },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_OUT_OF_SYNC_APPROVAL)
    // The reason and approval sentences arrive from the server, verbatim.
    expect(screen.getByTestId('recon-sync-reason').textContent).toBe(S.approvalRequired)
    expect(screen.getByTestId('recon-plan-approval').textContent).toBe(S.planApproval)
    // Contextual placement: the repair renders inside the approval condition card.
    const approvalCard = screen.getByTestId('recon-condition-approval')
    const repair = within(approvalCard).getByTestId('recon-action-repair')
    expect(repair.textContent).toContain('Repair connection')
    // The drift row shows the human label first, the raw path secondary.
    const config = screen.getByTestId('recon-drift-config')
    expect(config.textContent).toContain('API server address')
    expect(config.textContent).toContain('data.server')
    // Confirm shows the desired revision (short) and the exact scopes, then
    // the repair sends the reviewed commit — exactly as the old flow sent
    // compared_commit.
    fireEvent.click(repair)
    await waitFor(() => expect(screen.getByText(/Repair connection for "spoke-eu"\?/)).toBeTruthy())
    // The confirm shows the desired revision (short form) and the exact
    // non-sensitive scopes the action changes (ruling 3).
    expect(screen.getByText(new RegExp(`Desired revision: ${FULL_SHA.slice(0, 7)}`))).toBeTruthy()
    expect(screen.getByText(/It changes: metadata\.labels, data\.name, data\.server, data\.config\./)).toBeTruthy()
    const confirmButton = screen.getAllByRole('button').find((b) => b.textContent === 'Repair connection' && b !== repair)
    expect(confirmButton).toBeTruthy()
    fireEvent.click(confirmButton!)
    await waitFor(() => expect(mockRepairConnection).toHaveBeenCalledWith('spoke-eu', FULL_SHA))
    expectNoBannedWording()
  })

  it('row 4 — v4 addon-label drift: "Out of sync", the SERVER\'s automatic sentence verbatim, no buttons', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC, state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'ok', detail: S.condCredOK },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
          { id: 'comparison', status: 'attention', detail: S.condCompDrift },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        drift: {
          connection_configuration: [],
          credential_material: [],
          addon_labels: [{ path: 'metadata.labels[datadog]', status: 'missing', expected: 'enabled' }],
          not_checked: [],
        },
        plan: { action: 'none', action_scopes: [], automatic: S.planAutoLabels },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_OUT_OF_SYNC)
    expect(screen.getByTestId('recon-plan-automatic').textContent).toBe(S.planAutoLabels)
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByTestId('recon-action-sync')).toBeNull()
    // Human label for the drifted addon label.
    expect(screen.getByTestId('recon-drift-labels').textContent).toContain('Label "datadog"')
    expectNoBannedWording()
  })

  it('row 5 — slash-free label drift, self-heal off: the Sync addon labels door beside the addon-assignments group', async () => {
    const onRequestSync = vi.fn()
    mockGetConnectionReconciliation.mockResolvedValue(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC, state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
        drift: {
          connection_configuration: [],
          credential_material: [],
          addon_labels: [{ path: 'metadata.labels[team]', status: 'different', expected: 'a', live: 'b' }],
          not_checked: [],
        },
        plan: { action: 'sync_addon_labels', action_scopes: ['metadata.labels'] },
      }),
    )
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
    render(
      <AuthContext.Provider value={authFor('operator')}>
        <MemoryRouter>
          <ConnectionReconciliationView cluster="spoke-eu" onRequestSync={onRequestSync} onChanged={vi.fn()} />
        </MemoryRouter>
      </AuthContext.Provider>,
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_OUT_OF_SYNC)
    const labelsGroup = screen.getByTestId('recon-drift-labels').closest('div') as HTMLElement
    const sync = within(labelsGroup).getByTestId('recon-action-sync')
    expect(sync.textContent).toContain('Sync addon labels')
    fireEvent.click(sync)
    expect(onRequestSync).toHaveBeenCalled()
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expectNoBannedWording()
  })

  it('row 6 — credential rotation: sensitive drift renders the fixed <redacted> on BOTH sides, and never any value or length', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC_APPROVAL, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'ok', detail: S.condCredOK },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
          { id: 'comparison', status: 'attention', detail: S.condCompDrift },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
          { id: 'approval', status: 'blocked', detail: S.condApproval },
        ],
        drift: {
          connection_configuration: [],
          credential_material: [{ path: 'data.config', status: 'different', sensitive: true }],
          addon_labels: [],
          not_checked: [],
        },
        plan: {
          action: 'repair_connection',
          action_scopes: ['metadata.labels', 'data.name', 'data.server', 'data.config'],
          reviewed_commit: FULL_SHA,
          requires_approval: S.planApproval,
        },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_OUT_OF_SYNC_APPROVAL)
    const table = screen.getByTestId('recon-drift-credential')
    // Both sides are the SAME fixed text — no value, no length hint.
    expect(within(table).getAllByText('<redacted>')).toHaveLength(2)
    expect(table.textContent).toContain('Credential material')
    expect(table.textContent).toContain('data.config')
    // No automatic promise exists for credential drift.
    expect(screen.queryByTestId('recon-plan-automatic')).toBeNull()
    expectNoBannedWording()
  })

  it('row 7 — mixed drift: both groups listed; the label half may carry the automatic sentence, the credential half only the approval one', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC_APPROVAL, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
          { id: 'comparison', status: 'attention', detail: S.condCompDrift },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
          { id: 'approval', status: 'blocked', detail: S.condApproval },
        ],
        drift: {
          connection_configuration: [],
          credential_material: [{ path: 'data.config', status: 'different', sensitive: true }],
          addon_labels: [{ path: 'metadata.labels[datadog]', status: 'missing', expected: 'enabled' }],
          not_checked: [],
        },
        plan: {
          action: 'repair_connection',
          action_scopes: ['metadata.labels', 'data.name', 'data.server', 'data.config'],
          reviewed_commit: FULL_SHA,
          requires_approval: S.planApproval,
          automatic: S.planAutoLabels,
        },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-drift-credential')).toBeTruthy()
    expect(screen.getByTestId('recon-drift-labels')).toBeTruthy()
    expect(screen.getByTestId('recon-plan-automatic').textContent).toBe(S.planAutoLabels)
    expect(screen.getByTestId('recon-plan-approval').textContent).toBe(S.planApproval)
    expect(screen.getByTestId('recon-action-repair')).toBeTruthy()
    expectNoBannedWording()
  })

  it('row 8 — provider unavailable: "Unknown — check failed" with the server\'s failure sentence; no repair door', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_UNKNOWN_CHECK_FAILED, state: 'unknown', verification_scope: 'none', approval_required: false, reason: S.checkFail, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'blocked', detail: S.checkFail },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_UNKNOWN_CHECK_FAILED)
    expect(screen.getByTestId('recon-sync-reason').textContent).toBe(S.checkFail)
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 9 — foreign ownership: "Blocked", takeover is the only door, and no Repair text exists anywhere', async () => {
    renderView(
      makeView({
        management_mode: 'foreign_owned',
        managed_scope: 'none',
        mode_statement: S.modeForeign,
        sync: { headline: HEADLINE_BLOCKED, state: 'blocked', verification_scope: 'none', approval_required: false, reason: S.modeForeign, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'ownership', status: 'blocked', detail: S.modeForeign },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'take_over', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_BLOCKED)
    const ownershipCard = screen.getByTestId('recon-condition-ownership')
    expect(within(ownershipCard).getByTestId('recon-action-takeover').textContent).toContain('Take ownership')
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 10 — live Secret missing, durable source: "Out of sync" and the server\'s create-next-pass sentence verbatim', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC, state: 'out_of_sync', verification_scope: 'none', approval_required: false, reason: S.missingDurable, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'ok', detail: S.condCredOK },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'blocked', detail: S.condLiveMissing },
          { id: 'argocd_connection', status: 'attention', detail: 'ArgoCD cannot currently use this connection.' },
        ],
        health: { state: 'unavailable', message: 'connection refused' },
        plan: { action: 'none', action_scopes: [], automatic: S.planAutoCreate },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_OUT_OF_SYNC)
    expect(screen.getByTestId('recon-plan-automatic').textContent).toBe(S.planAutoCreate)
    expect(screen.getByTestId('recon-health').textContent).toContain('Unavailable')
    expectNoBannedWording()
  })

  it('row 11 — live Secret missing, legacy inline: the cannot-restore headline, migration only, NEVER a rebuild promise and NEVER a repair', async () => {
    renderView(
      makeView({
        management_mode: 'legacy_inline',
        managed_scope: 'addon_labels',
        mode_statement: S.modeLegacy,
        definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, credential_source_type: 'inline-kubeconfig' },
        sync: { headline: HEADLINE_OUT_OF_SYNC_CANNOT_RESTORE, qualifier: QUALIFIER_LEGACY_INLINE, state: 'out_of_sync', verification_scope: 'none', approval_required: false, reason: S.missingLegacy, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'blocked', detail: S.modeLegacy },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'blocked', detail: S.condLiveMissing },
          { id: 'argocd_connection', status: 'attention', detail: 'ArgoCD cannot currently use this connection.' },
        ],
        health: { state: 'unavailable', message: 'connection refused' },
        plan: { action: 'migrate_credentials', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_OUT_OF_SYNC_CANNOT_RESTORE)
    expect(screen.getByTestId('recon-sync-reason').textContent).toBe(S.missingLegacy)
    // The migration link is the only action, pointing at the documented path.
    const migrate = within(screen.getByTestId('recon-condition-credential_reference')).getByTestId('recon-action-migrate')
    expect(migrate.getAttribute('href')).toBe(MIGRATE_CREDENTIALS_DOC_URL)
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    // The break test: no invented reconstruction promise.
    expect(screen.queryByText(/next pass/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 12 — legacy inline, present and clean: "Verification incomplete" + the ruled qualifier, with Health: Connected CORRECTLY beside it', async () => {
    renderView(
      makeView({
        management_mode: 'legacy_inline',
        managed_scope: 'addon_labels',
        mode_statement: S.modeLegacy,
        definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, credential_source_type: 'inline-kubeconfig' },
        sync: { headline: HEADLINE_VERIFICATION_INCOMPLETE, qualifier: QUALIFIER_LEGACY_INLINE, state: 'unknown', verification_scope: 'partial', approval_required: false, reason: S.modeLegacy, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'blocked', detail: S.modeLegacy },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'migrate_credentials', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_VERIFICATION_INCOMPLETE)
    expect(screen.getByTestId('recon-sync-qualifier').textContent).toBe(QUALIFIER_LEGACY_INLINE)
    // Connected beside Verification incomplete is correct — never "fixed".
    expect(screen.getByTestId('recon-health').textContent).toContain('Connected')
    expect(screen.queryByText(/^Synced$/)).toBeNull()
    expect(screen.getByTestId('recon-action-migrate')).toBeTruthy()
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 13 — self-managed, labels match: "Addon labels synced", never bare "Synced", no repair door', async () => {
    renderView(
      makeView({
        management_mode: 'self_managed',
        managed_scope: 'addon_labels',
        mode_statement: S.modeSelf,
        sync: { headline: HEADLINE_ADDON_LABELS_SYNCED, qualifier: QUALIFIER_SELF_MANAGED, state: 'synced', verification_scope: 'full', approval_required: false, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'ownership', status: 'ok', detail: S.condOwnGuest },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_ADDON_LABELS_SYNCED)
    expect(screen.queryByText(/^Synced$/)).toBeNull()
    expect(screen.getByTestId('recon-sync-qualifier').textContent).toBe(QUALIFIER_SELF_MANAGED)
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 14 — self-managed, labels drifted: "Addon labels out of sync" and only the label-sync door', async () => {
    renderView(
      makeView({
        management_mode: 'self_managed',
        managed_scope: 'addon_labels',
        mode_statement: S.modeSelf,
        sync: { headline: HEADLINE_ADDON_LABELS_OUT_OF_SYNC, qualifier: QUALIFIER_SELF_MANAGED, state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'ownership', status: 'ok', detail: S.condOwnGuest },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        drift: {
          connection_configuration: [],
          credential_material: [],
          addon_labels: [{ path: 'metadata.labels[datadog]', status: 'missing', expected: 'enabled' }],
          not_checked: [],
        },
        plan: { action: 'sync_addon_labels', action_scopes: ['metadata.labels'] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_ADDON_LABELS_OUT_OF_SYNC)
    expect(screen.getByTestId('recon-action-sync')).toBeTruthy()
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 15 — never checked: "Not checked yet", the check button says "Check now", and no zero time renders as a real run', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_NOT_CHECKED_YET, state: 'unknown', verification_scope: 'none', approval_required: false },
        health: { state: 'not_checked' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'argocd_connection', status: 'attention', detail: 'ArgoCD has not checked this connection. ArgoCD only probes a cluster once an application is scheduled on it.' },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_NOT_CHECKED_YET)
    expect(screen.getByTestId('recon-check').textContent).toContain('Check now')
    expect(screen.getByTestId('recon-health').textContent).toContain('Not checked')
    // The break test: a Go zero time must never render as a real run.
    expect(screen.queryByText(/0001/)).toBeNull()
    expect(screen.getByTestId('recon-timestamps').textContent).toContain('Not checked yet')
    expectNoBannedWording()
  })

  it('row 16 — check failed: "Unknown — check failed", and the mode statement is NOT presented (F7)', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_UNKNOWN_CHECK_FAILED, state: 'unknown', verification_scope: 'none', approval_required: false, reason: S.checkFail, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'comparison', status: 'blocked', detail: S.checkFail },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_UNKNOWN_CHECK_FAILED)
    // F7: a mode derived before classification is cosmetic — not shown as
    // the page's authoritative model sentence on a failed check.
    expect(screen.queryByTestId('recon-mode-statement')).toBeNull()
    expect(screen.queryByText(S.modeSharko)).toBeNull()
    expectNoBannedWording()
  })

  it('row 17 — in flight: the INITIATING control shows Checking… while its request runs, and returns to Check again after', async () => {
    let resolveSecond: (v: ConnectionReconciliation) => void = () => {}
    mockGetConnectionReconciliation.mockResolvedValueOnce(makeView()).mockImplementationOnce(
      () => new Promise((resolve) => (resolveSecond = resolve)),
    )
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
    render(
      <AuthContext.Provider value={authFor('admin')}>
        <MemoryRouter>
          <ConnectionReconciliationView cluster="spoke-eu" onRequestSync={vi.fn()} onChanged={vi.fn()} />
        </MemoryRouter>
      </AuthContext.Provider>,
    )
    await waitForView()
    const check = screen.getByTestId('recon-check')
    expect(check.textContent).toContain('Check again')
    fireEvent.click(check)
    await waitFor(() => expect(screen.getByTestId('recon-check').textContent).toContain('Checking…'))
    // The word is request-local: it disappears the moment the request ends.
    resolveSecond(makeView())
    await waitFor(() => expect(screen.getByTestId('recon-check').textContent).toContain('Check again'))
    expect(screen.queryByText(/Checking…/)).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Role gates and error paths
// ─────────────────────────────────────────────────────────────────────────────

describe('role gates', () => {
  it('a viewer sees the calm access sentence and fires NO request at all', async () => {
    renderView(makeView(), 'viewer')
    expect(screen.getByTestId('recon-needs-operator').textContent).toBe(RECONCILIATION_NEEDS_OPERATOR)
    expect(mockGetConnectionReconciliation).not.toHaveBeenCalled()
    expect(mockFetchAuditLog).not.toHaveBeenCalled()
  })

  it('an operator never sees the repair button even when the plan offers it — the endpoint is admin-only', async () => {
    renderView(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC_APPROVAL, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'approval', status: 'blocked', detail: S.condApproval },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        drift: {
          connection_configuration: [{ path: 'data.server', status: 'different', expected: 'a', live: 'b' }],
          credential_material: [],
          addon_labels: [],
          not_checked: [],
        },
        plan: { action: 'repair_connection', action_scopes: ['data.server'], reviewed_commit: FULL_SHA, requires_approval: S.planApproval },
      }),
      'operator',
    )
    await waitForView()
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
  })

  it('an operator never sees the takeover button on a foreign row — the endpoint is admin-only; an admin does', async () => {
    const foreignView = makeView({
      management_mode: 'foreign_owned',
      managed_scope: 'none',
      mode_statement: S.modeForeign,
      sync: { headline: HEADLINE_BLOCKED, state: 'blocked', verification_scope: 'none', approval_required: false, reason: S.modeForeign, checked_at: '2026-08-18T10:00:00Z' },
      conditions: [
        { id: 'ownership', status: 'blocked', detail: S.modeForeign },
        { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
      ],
      plan: { action: 'take_over', action_scopes: [] },
    })
    const operatorRender = renderView(foreignView, 'operator')
    await waitForView()
    // Absent, never greyed out — same rule as the repair button.
    expect(screen.queryByTestId('recon-action-takeover')).toBeNull()
    expect(screen.queryByText('Take ownership')).toBeNull()
    operatorRender.unmount()

    renderView(foreignView, 'admin')
    await waitForView()
    expect(screen.getByTestId('recon-action-takeover')).toBeTruthy()
  })

  it('a failed reconciliation read shows the error with a Try again that re-fetches', async () => {
    mockGetConnectionReconciliation.mockRejectedValueOnce(new Error('The check failed.')).mockResolvedValueOnce(makeView())
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
    render(
      <AuthContext.Provider value={authFor('admin')}>
        <MemoryRouter>
          <ConnectionReconciliationView cluster="spoke-eu" onRequestSync={vi.fn()} onChanged={vi.fn()} />
        </MemoryRouter>
      </AuthContext.Provider>,
    )
    await waitFor(() => expect(screen.getByTestId('recon-error')).toBeTruthy())
    fireEvent.click(screen.getByTestId('recon-retry'))
    await waitForView()
  })

  it('shows the server\'s 409 sentence unchanged after a repair race, and does NOT auto-retry', async () => {
    const { ApiError } = await import('@/services/api')
    mockRepairConnection.mockRejectedValue(new (ApiError as unknown as new (s: number, b: { error?: string }, f: string) => Error)(409, { error: 'The branch moved. Nothing changed. Run the check again.' }, 'conflict'))
    renderView(
      makeView({
        sync: { headline: HEADLINE_OUT_OF_SYNC_APPROVAL, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'approval', status: 'blocked', detail: S.condApproval },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        drift: {
          connection_configuration: [{ path: 'data.server', status: 'different', expected: 'a', live: 'b' }],
          credential_material: [],
          addon_labels: [],
          not_checked: [],
        },
        plan: { action: 'repair_connection', action_scopes: ['data.server'], reviewed_commit: FULL_SHA, requires_approval: S.planApproval },
      }),
    )
    await waitForView()
    fireEvent.click(screen.getByTestId('recon-action-repair'))
    await waitFor(() => expect(screen.getByText(/Repair connection for "spoke-eu"\?/)).toBeTruthy())
    const repairButtons = screen.getAllByRole('button').filter((b) => b.textContent === 'Repair connection')
    fireEvent.click(repairButtons[repairButtons.length - 1])
    await waitFor(() =>
      expect(mockShowToast).toHaveBeenCalledWith('The branch moved. Nothing changed. Run the check again.', 'info'),
    )
    expect(mockRepairConnection).toHaveBeenCalledTimes(1)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Technical evidence — collapsed, never primary
// ─────────────────────────────────────────────────────────────────────────────

describe('technical evidence', () => {
  it('is collapsed by default — the YAML extra is NOT on the page until the toggle opens it', async () => {
    mockGetConnectionReconciliation.mockResolvedValue(makeView())
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
    render(
      <AuthContext.Provider value={authFor('admin')}>
        <MemoryRouter>
          <ConnectionReconciliationView
            cluster="spoke-eu"
            onRequestSync={vi.fn()}
            onChanged={vi.fn()}
            technicalEvidenceExtra={<div data-testid="yaml-inside-evidence">redacted yaml lives here</div>}
          />
        </MemoryRouter>
      </AuthContext.Provider>,
    )
    await waitForView()
    // Collapsed: no body, no YAML — the break test for "YAML as primary content".
    expect(screen.queryByTestId('recon-technical-evidence-body')).toBeNull()
    expect(screen.queryByTestId('yaml-inside-evidence')).toBeNull()
    fireEvent.click(screen.getByTestId('recon-technical-evidence-toggle'))
    expect(screen.getByTestId('recon-technical-evidence-body')).toBeTruthy()
    expect(screen.getByTestId('yaml-inside-evidence')).toBeTruthy()
    // The full SHAs live in here, not on the summary.
    expect(screen.getByTestId('recon-technical-evidence-body').textContent).toContain(FULL_SHA)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Recent activity (Story 3) — honest, human-titled, read-noise-free
// ─────────────────────────────────────────────────────────────────────────────

function auditEntry(event: string, overrides: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id: `id-${event}`,
    timestamp: '2026-08-18T09:00:00Z',
    level: 'info',
    event,
    user: 'admin',
    action: 'update',
    resource: 'cluster:spoke-eu',
    source: 'ui',
    result: 'success',
    duration_ms: 5,
    ...overrides,
  }
}

describe('Recent activity since Sharko started', () => {
  it('carries the exact honest label, human titles, actor, door, outcome — and "No changes made" ONLY where the entry says so (ruling f)', async () => {
    renderView(makeView(), 'admin', [
      // The ONE case where "No changes made" is true: a repair that ran and
      // deliberately wrote nothing.
      auditEntry('cluster_connection_repair', { changes: 'none' }),
      // A read-only check. It neither changed anything nor failed to, so it
      // says NOTHING about changes — this used to claim "No changes made"
      // from a static flag in the browser's own table.
      auditEntry('connection_credential_drift_detected', { user: 'sharko', source: '', level: 'warn', changes: 'not_applicable' }),
      auditEntry('cluster_registered', { changes: 'applied' }),
    ])
    await waitForView()
    expect(screen.getByTestId('recon-activity-label').textContent).toBe(RECENT_ACTIVITY_LABEL)
    const repair = screen.getByTestId('recon-activity-entry-0')
    expect(repair.textContent).toContain('Connection repaired')
    expect(repair.textContent).toContain(NO_CHANGES_MADE)
    const drift = screen.getByTestId('recon-activity-entry-1')
    expect(drift.textContent).toContain('Credential drift noticed')
    expect(drift.textContent).toContain('Background reconciler')
    // Successfully detecting drift is a SUCCESSFUL check with an attention
    // result — never a failure, and never a claim about changes.
    expect(drift.textContent).toContain('success')
    expect(drift.textContent).not.toContain(NO_CHANGES_MADE)
    const registered = screen.getByTestId('recon-activity-entry-2')
    expect(registered.textContent).toContain('Cluster registered')
    expect(registered.textContent).not.toContain(NO_CHANGES_MADE)
    // The separate audit-log link stays.
    const link = screen.getByTestId('recon-view-audit-log')
    expect(link.textContent).toBe('View audit log')
    expect(link.getAttribute('href')).toContain('/audit?cluster=spoke-eu')
  })

  it('NEVER renders a raw event id: reads and unmapped events are skipped entirely', async () => {
    renderView(makeView(), 'admin', [
      auditEntry('secret_resource_read'),
      auditEntry('cluster_connection_secret_check_triggered'),
      auditEntry('some_future_event_nobody_mapped'),
      auditEntry('cluster_adopted'),
    ])
    await waitForView()
    // The one mapped lifecycle event renders, with its human title.
    expect(screen.getByTestId('recon-activity-entry-0').textContent).toContain('Cluster adopted')
    expect(screen.queryByTestId('recon-activity-entry-1')).toBeNull()
    // The raw identifiers never appear anywhere on the page.
    expect(screen.queryByText(/secret_resource_read/)).toBeNull()
    expect(screen.queryByText(/cluster_connection_secret_check_triggered/)).toBeNull()
    expect(screen.queryByText(/some_future_event_nobody_mapped/)).toBeNull()
  })

  it('an estate with only read noise shows the quiet empty sentence, never an invented history', async () => {
    renderView(makeView(), 'admin', [auditEntry('secret_resource_read')])
    await waitForView()
    expect(screen.getByTestId('recon-activity-empty').textContent).toBe(ACTIVITY_EMPTY_SENTENCE)
  })

  it('asks for the ring-sized budget, so read noise cannot drown the lifecycle events out of the window (composed-review blocker 2)', async () => {
    // 60 reads NEWER than the 2 lifecycle entries: under the server's
    // 50-entry default, the lifecycle entries would never reach the client
    // and the feed would falsely say nothing was recorded — while both
    // events still sit in the 1000-entry ring.
    const noisy: AuditEntry[] = []
    for (let i = 0; i < 60; i++) {
      noisy.push(auditEntry('secret_resource_read', { id: `read-${i}`, timestamp: `2026-08-18T10:${String(i % 60).padStart(2, '0')}:00Z` }))
    }
    noisy.push(auditEntry('cluster_connection_repair', { timestamp: '2026-08-18T09:00:00Z' }))
    noisy.push(auditEntry('cluster_registered', { timestamp: '2026-08-18T08:00:00Z' }))
    renderView(makeView(), 'admin', noisy)
    await waitForView()
    // The fetch carries the ring-sized budget, not the server default.
    await waitFor(() => expect(mockFetchAuditLog).toHaveBeenCalledWith({ cluster: 'spoke-eu', limit: ACTIVITY_FETCH_LIMIT }))
    expect(ACTIVITY_FETCH_LIMIT).toBe(1000)
    // Both lifecycle titles render; the empty sentence does NOT.
    expect(await screen.findByText('Connection repaired')).toBeTruthy()
    expect(screen.getByText('Cluster registered')).toBeTruthy()
    expect(screen.queryByTestId('recon-activity-empty')).toBeNull()
    expect(screen.queryByText(ACTIVITY_EMPTY_SENTENCE)).toBeNull()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The migration link points at a page that actually resolves
// ─────────────────────────────────────────────────────────────────────────────

describe('the migration doc link', () => {
  it('carries the /en/latest/ readthedocs prefix — the unprefixed form hard-404s (composed-review blocker 1)', () => {
    expect(MIGRATE_CREDENTIALS_DOC_URL).toBe('https://sharko.readthedocs.io/en/latest/operator/migrate-inline-credentials/')
    expect(MIGRATE_CREDENTIALS_DOC_URL).toContain('/en/latest/')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Demo mode — the page renders a demo-shaped response like any other
// ─────────────────────────────────────────────────────────────────────────────

describe('demo mode', () => {
  it('renders a demo-shaped clean response without special-casing', async () => {
    renderView(
      makeView({
        cluster: 'demo-west',
        definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, applied_revision: FULL_SHA, credential_source_type: 'secret-kubeconfig' },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(HEADLINE_CONNECTION_SYNCED)
    expect(screen.getByTestId('recon-mode-statement').textContent).toBe(S.modeSharko)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// B6 — the condition hierarchy. Routine successes fold into ONE line; only
// what needs attention takes a card.
//
// The captured screens showed four routine successes — Git definition,
// credential reference, ownership, live Secret — each taking a full-width
// card, so the operator scrolled past all of them to reach the actual
// differences. That is the text wall the product owner rejected in the first
// place.
// ─────────────────────────────────────────────────────────────────────────────

/** A drifted connection: two conditions need attention, four are routine. */
function driftedWithRoutineSuccesses(): ConnectionReconciliation {
  return makeView({
    sync: {
      state: 'out_of_sync',
      verification_scope: 'full',
      approval_required: true,
      headline: HEADLINE_OUT_OF_SYNC_APPROVAL,
      reason: S.approvalRequired,
      checked_at: '2026-08-18T10:00:00Z',
    },
    conditions: [
      { id: 'git_definition', status: 'ok', detail: S.condGitOK },
      { id: 'credential_reference', status: 'ok', detail: S.condCredOK },
      { id: 'ownership', status: 'ok', detail: S.condOwnOK },
      { id: 'live_secret', status: 'ok', detail: S.condLiveOK },
      { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
      { id: 'comparison', status: 'attention', detail: S.condCompDrift },
      { id: 'approval', status: 'blocked', detail: S.condApproval },
    ],
    drift: {
      connection_configuration: [{ path: 'data.server', status: 'different', expected: 'https://new', live: 'https://old' }],
      credential_material: [],
      addon_labels: [],
      not_checked: [],
    },
    plan: {
      action: 'repair_connection',
      action_scopes: ['data.server'],
      reviewed_commit: FULL_SHA,
      requires_approval: S.planApproval,
    },
  })
}

describe('B6 — routine successes collapse, attention conditions stay expanded', () => {
  it('a drifted connection shows the attention conditions as cards and folds the routine successes into one line', async () => {
    renderView(driftedWithRoutineSuccesses())
    await waitForView()

    // The two that need attention are expanded, with their sentences visible.
    expect(screen.getByTestId('recon-condition-comparison')).toBeTruthy()
    expect(screen.getByTestId('recon-condition-comparison').textContent).toContain(S.condCompDrift)
    expect(screen.getByTestId('recon-condition-approval')).toBeTruthy()
    expect(screen.getByTestId('recon-condition-approval').textContent).toContain(S.condApproval)

    // The routine four are NOT cards — they are one compact summary.
    expect(screen.queryByTestId('recon-condition-git_definition')).toBeNull()
    expect(screen.queryByTestId('recon-condition-credential_reference')).toBeNull()
    expect(screen.queryByTestId('recon-condition-ownership')).toBeNull()
    expect(screen.queryByTestId('recon-condition-live_secret')).toBeNull()
    expect(screen.queryByTestId('recon-condition-argocd_connection')).toBeNull()
    const compact = screen.getByTestId('recon-conditions-compact')
    expect(compact.textContent).toContain('5 checks passed.')
    expect(compact.getAttribute('aria-expanded')).toBe('false')
    // And the routine sentences are genuinely off screen until asked for.
    expect(screen.queryByText(S.condGitOK)).toBeNull()
  })

  it('the routine evidence is one click away, and folds back', async () => {
    renderView(driftedWithRoutineSuccesses())
    await waitForView()
    fireEvent.click(screen.getByTestId('recon-conditions-compact'))
    expect(screen.getByTestId('recon-condition-git_definition').textContent).toContain(S.condGitOK)
    expect(screen.getByTestId('recon-condition-ownership')).toBeTruthy()
    fireEvent.click(screen.getByTestId('recon-conditions-collapse'))
    expect(screen.queryByTestId('recon-condition-git_definition')).toBeNull()
  })

  it('a healthy connection is ONE compact summary, not a stack of green cards', async () => {
    renderView(makeView())
    await waitForView()
    const compact = screen.getByTestId('recon-conditions-compact')
    expect(compact.textContent).toContain('All 6 checks passed.')
    for (const id of ['git_definition', 'credential_reference', 'ownership', 'live_secret', 'comparison', 'argocd_connection']) {
      expect(screen.queryByTestId(`recon-condition-${id}`)).toBeNull()
    }
  })

  it('the condition an offered action hangs off stays expanded even when it reads "ok"', async () => {
    // Clean EKS: repair is offered by policy and hangs off the comparison
    // condition. A button must never disappear into a collapsed summary.
    renderView(
      makeView({
        definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, credential_source_type: 'eks-token' },
        sync: {
          state: 'unknown',
          verification_scope: 'partial',
          approval_required: false,
          headline: HEADLINE_EKS_PARTIAL,
          reason: S.eksLimit,
          checked_at: '2026-08-18T10:00:00Z',
        },
        conditions: okConditions,
        plan: { action: 'repair_connection', action_scopes: ['data.config'], reviewed_commit: FULL_SHA },
      }),
    )
    await waitForView()
    const comparison = screen.getByTestId('recon-condition-comparison')
    expect(within(comparison).getByTestId('recon-action-repair')).toBeTruthy()
    // The other five still fold away.
    expect(screen.getByTestId('recon-conditions-compact').textContent).toContain('5 checks passed.')
  })

  it('the Approval condition is a policy gate — a lock, never a red failure icon', async () => {
    renderView(driftedWithRoutineSuccesses())
    await waitForView()
    const approval = screen.getByTestId('recon-condition-approval')
    // It arrives from the server as `blocked`, which everywhere else means a
    // red failure mark. Nothing is broken here — Sharko is waiting for a
    // person — so it renders amber with a lock.
    expect(approval.getAttribute('data-condition-status')).toBe('blocked')
    const icon = approval.querySelector('svg')
    expect(icon?.getAttribute('class') ?? '').toContain('amber')
    expect(icon?.getAttribute('class') ?? '').not.toContain('red')
    // The repair door stays attached to it.
    expect(within(approval).getByTestId('recon-action-repair')).toBeTruthy()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// B7 — the plan is scannable, and still the server's own sentences
// ─────────────────────────────────────────────────────────────────────────────

describe('B7 — the action plan renders under explicit labels', () => {
  it('labels each returned sentence Automatic / Requires approval / Preserved, without rewriting one word of it', async () => {
    renderView(
      makeView({
        sync: {
          state: 'out_of_sync',
          verification_scope: 'full',
          approval_required: true,
          headline: HEADLINE_OUT_OF_SYNC_APPROVAL,
          reason: S.approvalRequired,
          checked_at: '2026-08-18T10:00:00Z',
        },
        conditions: [
          { id: 'comparison', status: 'attention', detail: S.condCompDrift },
          { id: 'approval', status: 'blocked', detail: S.condApproval },
        ],
        drift: {
          connection_configuration: [],
          credential_material: [],
          addon_labels: [{ path: 'metadata.labels[datadog]', status: 'missing', expected: 'enabled' }],
          not_checked: [],
        },
        plan: {
          action: 'repair_connection',
          action_scopes: ['metadata.labels'],
          reviewed_commit: FULL_SHA,
          automatic: S.planAutoLabels,
          requires_approval: S.planApproval,
        },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-plan-automatic-label').textContent).toBe(PLAN_LABEL_AUTOMATIC)
    expect(screen.getByTestId('recon-plan-approval-label').textContent).toBe(PLAN_LABEL_REQUIRES_APPROVAL)
    expect(screen.getByTestId('recon-plan-preserved-label').textContent).toBe(PLAN_LABEL_PRESERVED)
    // VERBATIM — character for character, no merge and no paraphrase.
    expect(screen.getByTestId('recon-plan-automatic').textContent).toBe(S.planAutoLabels)
    expect(screen.getByTestId('recon-plan-approval').textContent).toBe(S.planApproval)
    expect(screen.getByTestId('recon-plan-untouched').textContent).toBe(PLAN_UNTOUCHED_SENTENCE)
  })

  it('RULING (a): self-managed label drift promises the automatic re-apply and offers NO manual door', async () => {
    const onRequestSync = vi.fn()
    mockGetConnectionReconciliation.mockResolvedValue(
      makeView({
        management_mode: 'self_managed',
        managed_scope: 'addon_labels',
        mode_statement: S.modeSelf,
        sync: {
          state: 'out_of_sync',
          verification_scope: 'full',
          approval_required: false,
          headline: HEADLINE_ADDON_LABELS_OUT_OF_SYNC,
          qualifier: QUALIFIER_SELF_MANAGED,
          reason: S.labelsOnly,
          checked_at: '2026-08-18T10:00:00Z',
        },
        conditions: [
          { id: 'ownership', status: 'ok', detail: S.condOwnGuest },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        drift: {
          connection_configuration: [],
          credential_material: [],
          addon_labels: [{ path: 'metadata.labels[datadog]', status: 'missing', expected: 'enabled' }],
          not_checked: [],
        },
        // The server's own plan for this row after ruling (a): a promise, no action.
        plan: { action: 'none', action_scopes: [], automatic: S.planAutoLabels },
      }),
    )
    mockFetchAuditLog.mockResolvedValue({ entries: [] })
    render(
      <AuthContext.Provider value={authFor('admin')}>
        <MemoryRouter>
          <ConnectionReconciliationView cluster="spoke-eu" onRequestSync={onRequestSync} onChanged={vi.fn()} />
        </MemoryRouter>
      </AuthContext.Provider>,
    )
    await waitForView()
    expect(screen.getByTestId('recon-plan-automatic').textContent).toBe(S.planAutoLabels)
    expect(screen.getByTestId('recon-plan-automatic-label').textContent).toBe(PLAN_LABEL_AUTOMATIC)
    // No manual action for work the reconciler performs on every pass.
    expect(screen.queryByTestId('recon-action-sync')).toBeNull()
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText('Sync addon labels')).toBeNull()
  })
})
