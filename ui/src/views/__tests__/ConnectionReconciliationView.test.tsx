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
//    heading and body, "Needs attention", "does not match what Sharko
//    intends";
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
} from '@/views/ConnectionReconciliationView'
import { RECENT_ACTIVITY_LABEL, ACTIVITY_EMPTY_SENTENCE, NO_CHANGES_MADE } from '@/views/connectionActivity'

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
  condCompDrift: 'At least one compared field does not match what Sharko intends.',
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
 * each matrix row. The old page's REPEATED verdict sentences are banned by
 * their exact text (the server's single itemized comparison condition — "At
 * least one compared field does not match what Sharko intends." — is a
 * different, Story-1-pinned sentence and appears at most once).
 */
function expectNoBannedWording() {
  expect(screen.queryByText(/How Sharko manages this connection/)).toBeNull()
  expect(screen.queryByText(/Git controls the addon labels/)).toBeNull()
  expect(screen.queryByText(/Your configured credentials source controls how ArgoCD connects/)).toBeNull()
  expect(screen.queryByText(/Needs attention/)).toBeNull()
  expect(screen.queryByText(/Sharko will fix this on the next pass/)).toBeNull()
  expect(screen.queryByText(/This connection does not match what Sharko intends\./)).toBeNull()
  expect(screen.queryByText(/This connection matches what Sharko intends/)).toBeNull()
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
        sync: { state: 'unknown', verification_scope: 'partial', approval_required: false, reason: S.eksLimit, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'unknown', verification_scope: 'none', approval_required: false, reason: S.checkFail, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'blocked', verification_scope: 'none', approval_required: false, reason: S.modeForeign, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'none', approval_required: false, reason: S.missingDurable, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'none', approval_required: false, reason: S.missingLegacy, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'unknown', verification_scope: 'partial', approval_required: false, reason: S.modeLegacy, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'synced', verification_scope: 'full', approval_required: false, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'unknown', verification_scope: 'none', approval_required: false },
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
        sync: { state: 'unknown', verification_scope: 'none', approval_required: false, reason: S.checkFail, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
  it('carries the exact honest label, human titles, actor, door, outcome — and "No changes made" on read-only lifecycle events', async () => {
    renderView(makeView(), 'admin', [
      auditEntry('cluster_connection_repair'),
      auditEntry('connection_credential_drift_detected', { user: 'sharko', source: '' }),
      auditEntry('cluster_registered'),
    ])
    await waitForView()
    expect(screen.getByTestId('recon-activity-label').textContent).toBe(RECENT_ACTIVITY_LABEL)
    expect(screen.getByTestId('recon-activity-entry-0').textContent).toContain('Connection repaired')
    const drift = screen.getByTestId('recon-activity-entry-1')
    expect(drift.textContent).toContain('Credential drift noticed')
    expect(drift.textContent).toContain('Background reconciler')
    expect(drift.textContent).toContain(NO_CHANGES_MADE)
    expect(screen.getByTestId('recon-activity-entry-2').textContent).toContain('Cluster registered')
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
