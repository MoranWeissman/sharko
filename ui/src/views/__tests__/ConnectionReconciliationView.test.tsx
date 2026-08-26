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
//    promise of its own — any browser-authored "Sharko will fix this …" is
//    banned by fragment, whatever wording it wears;
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
  CHECK_NOW_LABEL,
  HEALTH_WORDS,
  healthWordFor,
  MIGRATE_CREDENTIALS_DOC_URL,
  RECONCILIATION_NEEDS_OPERATOR,
  PLAN_LABEL_AUTOMATIC,
  PLAN_LABEL_REQUIRES_APPROVAL,
  PLAN_LABEL_PRESERVED,
  PLAN_UNTOUCHED_SENTENCE,
  ACTION_TAKE_OWNERSHIP,
  actionTargetConditionId,
  routineChecksSummary,
} from '@/views/ConnectionReconciliationView'
import { CONNECTION_HEALTH_WORDS, connectionHealthWord } from '@/views/connectionHealthWords'
import { HEALTH_COLUMN_WORDS, healthColumnWord } from '@/views/ManagedSecrets'
import { RECENT_ACTIVITY_LABEL, ACTIVITY_EMPTY_SENTENCE, ACTIVITY_FETCH_LIMIT, NO_CHANGES_MADE } from '@/views/connectionActivity'
import { CONNECTION_SENTENCES } from '@/generated/connection-sentences'

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

// Short names for the server's sentences. Every value comes from the
// GENERATED contract (story P5); not one of them is typed here.
//
// What this block used to be, and why it had to change: every value was a
// hand-copy of a Go constant. Nothing checked the copy against the original —
// this block feeds the fixtures and the assertions read the same block back,
// so a fixture and its assertion agreed with each other even when both
// described a response the server cannot send. Five came to be stale or
// invented that way, and three were STILL stale at conversion time: they said
// "what git defines" and "what git declares" where the server says "Git".
// Every test in this file passed while asserting a sentence no server emits.
//
// Now the words arrive from ui/src/generated/connection-sentences.ts, which
// cmd/gen-connection-sentences writes by reading the Go catalog at runtime,
// and CI's "Connection Sentences Up To Date" job fails the PR if the file and
// the Go catalog disagree. Fixture and assertion still read the same value —
// that is correct and deliberate. The browser's job is to render what it was
// sent, so this suite proves the page renders it; the WORDS are pinned once,
// in Go, by the exact-text tests there. What changed is that there is no
// longer a second copy in this repository that can quietly say something
// else.
//
// The names below are this file's own shorthand, not part of any contract.
// To change a sentence, edit the Go constant and regenerate.
const S = {
  modeSharko: CONNECTION_SENTENCES.modeStatementSharkoManaged,
  modeSelf: CONNECTION_SENTENCES.modeStatementSelfManaged,
  modeLegacy: CONNECTION_SENTENCES.modeStatementLegacyInline,
  // modeStatementForeignOwned. The words changed and the identifier did not: "marked as managed by" is a
  // correctness word, because Sharko only sees an ownership marker on the
  // Secret — it never checks that the other tool really manages the
  // connection. This is the ONLY place the page states the boundary; there is
  // no ownership condition beside it and no limit sentence behind it.
  modeForeign: CONNECTION_SENTENCES.modeStatementForeignOwned,
  approvalRequired: CONNECTION_SENTENCES.reasonOutOfSyncApprovalRequired,
  labelsOnly: CONNECTION_SENTENCES.reasonOutOfSyncLabelsOnly,
  legacyDrift: CONNECTION_SENTENCES.reasonOutOfSyncLegacyInline,
  missingDurable:
    CONNECTION_SENTENCES.limitReasonSecretMissingDurable,
  missingLegacy:
    CONNECTION_SENTENCES.limitReasonSecretMissingLegacyInline,
  planAutoCreate: CONNECTION_SENTENCES.planAutomaticSecretCreate,
  // planAutomaticLabelSync. This one was never a server sentence at all
  // until P2 made it one: the old block header claimed it was, and it was
  // written here.
  planAutoLabels: CONNECTION_SENTENCES.planAutomaticLabelSync,
  planApproval:
    CONNECTION_SENTENCES.planRequiresApprovalSentence,
  eksLimit: CONNECTION_SENTENCES.condCredentialRefEKS,
  // failCredsUnavailable. The old value here was
  // credsafe.Message with its second half cut off, and credsafe cannot reach
  // this endpoint at all: none of connection_reconciliation.go,
  // connection_canonical.go or connection_comparison.go imports it.
  checkFail: CONNECTION_SENTENCES.failCredsUnavailable,
  // condCredentialRefUnread. This is the FACT sentence the
  // handler puts on the condition when the failure sentence is already the
  // sync reason: saidOnce (connection_reconciliation.go) refuses to print the
  // same sentence twice, so a fixture repeating checkFail in both places is a
  // response the server will not send.
  condCredUnread: CONNECTION_SENTENCES.condCredentialRefUnread,
  condGitOK: CONNECTION_SENTENCES.condGitDefinitionOK,
  // failBackendRead. This is the failure the
  // page now shows for a foreign-owned connection whose credentials backend
  // could not be read; it is NOT the same sentence as condCredUnread, which
  // is the fact on the condition row.
  failBackendRead:
    CONNECTION_SENTENCES.failBackendRead,
  condCredOK: CONNECTION_SENTENCES.condCredentialRefOK,
  condOwnOK: CONNECTION_SENTENCES.condOwnershipOK,
  condOwnGuest: CONNECTION_SENTENCES.condOwnershipGuest,
  condLiveOK: CONNECTION_SENTENCES.condLiveSecretFound,
  condLiveMissing: CONNECTION_SENTENCES.condLiveSecretMissing,
  condCompFull: CONNECTION_SENTENCES.condComparisonFull,
  condCompDrift: CONNECTION_SENTENCES.condComparisonDrift,
  condArgoOK: CONNECTION_SENTENCES.condArgoCDConnected,
  condApproval: CONNECTION_SENTENCES.condApprovalRequired,
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
      headline: CONNECTION_SENTENCES.headlineConnectionSynced,
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

// ─────────────────────────────────────────────────────────────────────────────
// The two foreign-owned responses the server can actually send.
//
// Both are shared, so a row test and the foreign-owned suite below can never
// describe two different servers — and so ONE edit to a fixture here fails
// every test that depends on the shape (which is exactly what break test 8
// checks).
//
// The rule that produced them: the page states the foreign-ownership boundary
// in ONE place, mode_statement. There is no ownership condition row beside it
// (the server omits the component rather than blanking it — a condition with
// empty text paints an empty paragraph) and no limit sentence behind it (the
// comparison sets limit_reason to empty on an ownership conflict, so
// sync.reason is absent).
// ─────────────────────────────────────────────────────────────────────────────

/**
 * TUPLE A — ordinary foreign ownership. The comparison's ownership exit:
 * nothing was compared, so the ArgoCD health fact is the only condition.
 */
function foreignOwnedOrdinary(): ConnectionReconciliation {
  return makeView({
    management_mode: 'foreign_owned',
    managed_scope: 'none',
    mode_statement: S.modeForeign,
    sync: {
      headline: CONNECTION_SENTENCES.headlineBlocked,
      state: 'blocked',
      verification_scope: 'none',
      approval_required: false,
      checked_at: '2026-08-18T10:00:00Z',
    },
    conditions: [{ id: 'argocd_connection', status: 'ok', detail: S.condArgoOK }],
    plan: { action: 'take_over', action_scopes: [] },
  })
}

/**
 * TUPLE B — foreign ownership AND the credentials backend could not be read.
 *
 * This response is NEW. The comparison's failure exit fires before its
 * ownership exit, so the answer is check_failed with a foreign mode — and the
 * page used to swallow the failed read entirely behind the ownership row,
 * which was the one fact the reader needed. The failed step is now named as
 * its own condition, and the failure sentence is the sync reason.
 */
function foreignOwnedBackendUnread(): ConnectionReconciliation {
  return makeView({
    management_mode: 'foreign_owned',
    managed_scope: 'none',
    mode_statement: S.modeForeign,
    sync: {
      headline: CONNECTION_SENTENCES.headlineUnknownCheckFailed,
      state: 'unknown',
      verification_scope: 'none',
      approval_required: false,
      reason: S.failBackendRead,
      checked_at: '2026-08-18T10:00:00Z',
    },
    conditions: [
      { id: 'git_definition', status: 'ok', detail: S.condGitOK },
      { id: 'credential_reference', status: 'blocked', detail: S.condCredUnread },
      { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
    ],
    plan: { action: 'take_over', action_scopes: [] },
  })
}

/**
 * Every condition card currently on screen, as ids — read off the DOM, not
 * off the fixture, so a card the page invents (or drops) shows up here.
 *
 * Routine successes fold into a compact summary, so this opens it first: an
 * `ok` condition the page should not have built would otherwise hide inside
 * the fold and every "it is not there" assertion would pass for the wrong
 * reason.
 */
function renderedConditionIds(): string[] {
  const compact = screen.queryByTestId('recon-conditions-compact')
  if (compact) fireEvent.click(compact)
  return Array.from(document.querySelectorAll('[data-testid^="recon-condition-"]'))
    .map((el) => el.getAttribute('data-testid') ?? '')
    .map((id) => id.replace('recon-condition-', ''))
    .sort()
}

/**
 * Fails if any condition card on screen carries no words — the blank-instead-
 * of-omit failure the ruling names. Call AFTER renderedConditionIds() so the
 * routine fold is already open.
 */
function expectNoEmptyConditionCards() {
  const cards = Array.from(document.querySelectorAll('[data-testid^="recon-condition-"]'))
  expect(cards.length).toBeGreaterThan(0)
  for (const card of cards) {
    const paragraphs = Array.from(card.querySelectorAll('p'))
    expect(paragraphs.length).toBeGreaterThan(0)
    for (const p of paragraphs) {
      expect((p.textContent ?? '').trim()).not.toBe('')
    }
  }
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
  // The revoked split-authority model (ruling 8) — banned in every variant:
  // the credentials source is a resolved reference, never a second source of
  // truth standing next to git.
  expect(screen.queryByText(/two authorities/i)).toBeNull()
  expect(screen.queryByText(/hybrid ownership/i)).toBeNull()
  expect(screen.queryByText(/hybrid connection/i)).toBeNull()
  expect(screen.queryByText(/authoritative for connection details/i)).toBeNull()
  expect(screen.queryByText(/authoritative for addon assignments/i)).toBeNull()
  expect(screen.queryByText(/safer alternative to editing with kubectl/i)).toBeNull()
  expect(screen.queryByText(/Needs attention/)).toBeNull()
  // The browser-authored self-heal promise, banned by FRAGMENT rather than by
  // one full wording. That sentence has now been reworded once (it dropped
  // "on the next pass"), and a guard naming only the retired text would have
  // stopped guarding anything the moment it changed.
  expect(screen.getByTestId('recon-view').textContent ?? '').not.toContain('Sharko will fix this')
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineConnectionSynced)
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
        sync: { headline: CONNECTION_SENTENCES.headlineConfigurationMatchesEKS, state: 'unknown', verification_scope: 'partial', approval_required: false, reason: S.eksLimit, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineConfigurationMatchesEKS)
    // Bare "Synced" (and "Connection synced") banned for EKS.
    expect(screen.queryByText(/^Synced$/)).toBeNull()
    expect(screen.queryByText(CONNECTION_SENTENCES.headlineConnectionSynced)).toBeNull()
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
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineOutOfSyncApproval)
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
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSync, state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineOutOfSync)
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
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSync, state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineOutOfSync)
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
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineOutOfSyncApproval)
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
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
        sync: { headline: CONNECTION_SENTENCES.headlineUnknownCheckFailed, state: 'unknown', verification_scope: 'none', approval_required: false, reason: S.checkFail, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'blocked', detail: S.condCredUnread },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineUnknownCheckFailed)
    expect(screen.getByTestId('recon-sync-reason').textContent).toBe(S.checkFail)
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 9 — foreign ownership: "Blocked", takeover is the only door, and no Repair text exists anywhere', async () => {
    // The fixture is the shared tuple A below, so this row and the
    // foreign-owned suite cannot describe two different servers.
    renderView(foreignOwnedOrdinary())
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineBlocked)
    // The takeover door now lives in the plan section: there is no ownership
    // condition for it to hang off any more.
    expect(screen.queryByTestId('recon-condition-ownership')).toBeNull()
    expect(within(screen.getByTestId('recon-plan-action')).getByTestId('recon-action-takeover').textContent).toContain('Take ownership')
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 10 — live Secret missing, durable source: "Out of sync" and the server\'s create-next-pass sentence verbatim', async () => {
    renderView(
      makeView({
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSync, state: 'out_of_sync', verification_scope: 'none', approval_required: false, reason: S.missingDurable, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'ok', detail: S.condCredOK },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'blocked', detail: S.condLiveMissing },
          { id: 'argocd_connection', status: 'attention', detail: CONNECTION_SENTENCES.condArgoCDUnavailable },
        ],
        health: { state: 'unavailable', message: 'connection refused' },
        plan: { action: 'none', action_scopes: [], automatic: S.planAutoCreate },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineOutOfSync)
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
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSyncCannotRestore, qualifier: CONNECTION_SENTENCES.qualifierLegacyInline, state: 'out_of_sync', verification_scope: 'none', approval_required: false, reason: S.missingLegacy, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'credential_reference', status: 'blocked', detail: S.modeLegacy },
          { id: 'ownership', status: 'ok', detail: S.condOwnOK },
          { id: 'live_secret', status: 'blocked', detail: S.condLiveMissing },
          { id: 'argocd_connection', status: 'attention', detail: CONNECTION_SENTENCES.condArgoCDUnavailable },
        ],
        health: { state: 'unavailable', message: 'connection refused' },
        plan: { action: 'migrate_credentials', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineOutOfSyncCannotRestore)
    expect(screen.getByTestId('recon-sync-reason').textContent).toBe(S.missingLegacy)
    // What this row must prove, positively and by element.
    //
    // This replaces `expect(screen.queryByText(/next pass/)).toBeNull()`,
    // which was free the moment the phrase moved: the fixture above never
    // supplied a sentence containing "next pass", so the check was looking
    // for text nobody had put on the page. After the server dropped the
    // phrase entirely it could never fail under any change.
    //
    // 1. the credential-reference condition rendered, blocked, carrying the
    //    server's legacy-inline sentence;
    const credCond = screen.getByTestId('recon-condition-credential_reference')
    expect(credCond.getAttribute('data-condition-status')).toBe('blocked')
    expect(credCond.textContent).toContain(S.modeLegacy)
    // 2. the migration action rendered inside it, pointing at the documented
    //    path — this is the offer that must never disappear;
    const migrate = within(credCond).getByTestId('recon-action-migrate')
    expect(migrate.getAttribute('href')).toBe(MIGRATE_CREDENTIALS_DOC_URL)
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    // 3. and NO automatic promise, under ANY wording. Asserted on the
    //    testids a promise would have to render into rather than on words,
    //    so a promise nobody has written yet still fails this. The plan
    //    block itself must be absent here: the server's plan carries no
    //    automatic sentence and no action scopes, so it describes no write.
    expect(screen.queryByTestId('recon-plan')).toBeNull()
    expect(screen.queryByTestId('recon-plan-automatic')).toBeNull()
    expect(screen.queryByTestId('recon-plan-automatic-label')).toBeNull()
    expectNoBannedWording()
  })

  it('row 12 — legacy inline, present and clean: "Verification incomplete" + the ruled qualifier, with Health: Connected CORRECTLY beside it', async () => {
    renderView(
      makeView({
        management_mode: 'legacy_inline',
        managed_scope: 'addon_labels',
        mode_statement: S.modeLegacy,
        definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, credential_source_type: 'inline-kubeconfig' },
        sync: { headline: CONNECTION_SENTENCES.headlineVerificationIncomplete, qualifier: CONNECTION_SENTENCES.qualifierLegacyInline, state: 'unknown', verification_scope: 'partial', approval_required: false, reason: S.modeLegacy, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineVerificationIncomplete)
    expect(screen.getByTestId('recon-sync-qualifier').textContent).toBe(CONNECTION_SENTENCES.qualifierLegacyInline)
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
        sync: { headline: CONNECTION_SENTENCES.headlineAddonLabelsSynced, qualifier: CONNECTION_SENTENCES.qualifierSelfManaged, state: 'synced', verification_scope: 'full', approval_required: false, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'ownership', status: 'ok', detail: S.condOwnGuest },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineAddonLabelsSynced)
    expect(screen.queryByText(/^Synced$/)).toBeNull()
    expect(screen.getByTestId('recon-sync-qualifier').textContent).toBe(CONNECTION_SENTENCES.qualifierSelfManaged)
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
        sync: { headline: CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync, qualifier: CONNECTION_SENTENCES.qualifierSelfManaged, state: 'out_of_sync', verification_scope: 'full', approval_required: false, reason: S.labelsOnly, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync)
    expect(screen.getByTestId('recon-action-sync')).toBeTruthy()
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByText(/Repair/)).toBeNull()
    expectNoBannedWording()
  })

  it('row 15 — never checked: "Not checked yet", the check button says "Check now", and no zero time renders as a real run', async () => {
    renderView(
      makeView({
        sync: { headline: CONNECTION_SENTENCES.headlineNotCheckedYet, state: 'unknown', verification_scope: 'none', approval_required: false },
        health: { state: 'not_checked' },
        conditions: [
          { id: 'git_definition', status: 'ok', detail: S.condGitOK },
          { id: 'argocd_connection', status: 'attention', detail: CONNECTION_SENTENCES.condArgoCDNotChecked },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineNotCheckedYet)
    expect(screen.getByTestId('recon-check').textContent).toContain('Check now')
    expect(screen.getByTestId('recon-health').textContent).toContain('Not checked')
    // The break test: a Go zero time must never render as a real run.
    expect(screen.queryByText(/0001/)).toBeNull()
    expect(screen.getByTestId('recon-timestamps').textContent).toContain(CONNECTION_SENTENCES.headlineNotCheckedYet)
    expectNoBannedWording()
  })

  it('row 16 — check failed: "Unknown — check failed", and the mode statement is NOT presented (F7)', async () => {
    renderView(
      makeView({
        sync: { headline: CONNECTION_SENTENCES.headlineUnknownCheckFailed, state: 'unknown', verification_scope: 'none', approval_required: false, reason: S.checkFail, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'comparison', status: 'blocked', detail: S.checkFail },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineUnknownCheckFailed)
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
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
    const foreignView = foreignOwnedOrdinary()
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

  // B13 item 6: the refusal button used to read "Try again" — a second name
  // for the identical action the page's own check button and the fleet row
  // menu both call "Check now".
  it('a failed reconciliation read shows the error with a Check now that re-fetches', async () => {
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
    const retry = screen.getByTestId('recon-retry')
    // Pinned by exact text, both directions — the old name is banned.
    expect(retry.textContent).toContain(CHECK_NOW_LABEL)
    expect(CHECK_NOW_LABEL).toBe('Check now')
    expect(retry.textContent).not.toContain('Try again')
    fireEvent.click(retry)
    await waitForView()
  })

  it('shows the server\'s 409 sentence unchanged after a repair race, and does NOT auto-retry', async () => {
    const { ApiError } = await import('@/services/api')
    mockRepairConnection.mockRejectedValue(new (ApiError as unknown as new (s: number, b: { error?: string }, f: string) => Error)(409, { error: 'The branch moved. Nothing changed. Run the check again.' }, 'conflict'))
    renderView(
      makeView({
        sync: { headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval, state: 'out_of_sync', verification_scope: 'full', approval_required: true, reason: S.approvalRequired, checked_at: '2026-08-18T10:00:00Z' },
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
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineConnectionSynced)
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
      headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval,
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

// ─────────────────────────────────────────────────────────────────────────────
// B13 item 3 — a self-managed connection whose Secret is not there
//
// The page used to render "Addon labels out of sync" here: a claim about
// labels on a Secret that does not exist, directly above the reason sentence
// saying the Secret has not been created and Sharko does not create it. The
// server now sends "Connection Secret missing", health `unknown` — a FOURTH
// word in an enum this browser knew as three — verification none, plan
// action none, and a live_secret condition.
// ─────────────────────────────────────────────────────────────────────────────

describe('B13 item 3 — a self-managed connection with no Secret', () => {
  // TEMPORARY — hand-copied server text, awaiting the generated import.
  //
  // REASON_SELF_MANAGED_MISSING used to hold a sentence that existed in no Go
  // file at all: "This cluster has no connection Secret, and this connection
  // is managed outside Sharko, so Sharko will not create one." It was written
  // here, asserted here, and nothing ever compared it to the server. The real
  // sentence for this row is connectioncompare.LimitReasonSecretMissingSelfManaged.
  const REASON_SELF_MANAGED_MISSING =
    CONNECTION_SENTENCES.limitReasonSecretMissingSelfManaged
  // api.condArgoCDNoConnection — hand-copied.
  const COND_ARGO_NO_CONNECTION = CONNECTION_SENTENCES.condArgoCDNoConnection
  // The other three real ArgoCD condition sentences, hand-copied from
  // api.condArgoCDNotChecked / condArgoCDUnavailable / condArgoCDConnected.
  // Four situations, four sentences, none a stand-in for another.
  const COND_ARGO_NOT_CHECKED =
    CONNECTION_SENTENCES.condArgoCDNotChecked
  const COND_ARGO_UNAVAILABLE = CONNECTION_SENTENCES.condArgoCDUnavailable
  const COND_ARGO_CONNECTED = CONNECTION_SENTENCES.condArgoCDConnected

  function missingSelfManaged(): ConnectionReconciliation {
    return makeView({
      management_mode: 'self_managed',
      managed_scope: 'addon_labels',
      mode_statement: S.modeSelf,
      sync: {
        state: 'blocked',
        verification_scope: 'none',
        approval_required: false,
        headline: CONNECTION_SENTENCES.headlineConnectionSecretMissing,
        qualifier: CONNECTION_SENTENCES.qualifierSelfManaged,
        reason: REASON_SELF_MANAGED_MISSING,
        checked_at: '2026-08-18T10:00:00Z',
      },
      health: { state: 'unknown' },
      conditions: [
        { id: 'ownership', status: 'ok', detail: S.condOwnGuest },
        { id: 'live_secret', status: 'blocked', detail: S.condLiveMissing },
        { id: 'argocd_connection', status: 'attention', detail: COND_ARGO_NO_CONNECTION },
      ] as ConnectionReconciliation['conditions'],
      plan: { action: 'none', action_scopes: [] },
    })
  }

  it('renders the exact ruled headline, and never the label claim it replaced', async () => {
    renderView(missingSelfManaged())
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineConnectionSecretMissing)
    // A second line used to sit here pinning the browser constant against the
    // literal 'Connection Secret missing'. It is gone (story P5): the WORDS
    // are pinned once, in Go, and a browser-side copy of them was the drift
    // channel this conversion closes. Re-adding a literal here would fail the
    // no-hand-typed-server-sentences guard.
    // The false headline this replaced must not appear anywhere on the page.
    expect(screen.queryByText(CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync)).toBeNull()
    expectNoBannedWording()
  })

  it('renders the FOURTH health word — never "Not checked", which would promise a probe that is not coming', async () => {
    renderView(missingSelfManaged())
    await waitForView()
    const health = screen.getByTestId('recon-health')
    expect(health.textContent).toContain('Unknown')
    expect(health.textContent).not.toContain('Not checked')
    // The mapping itself, both directions.
    expect(HEALTH_WORDS.unknown).toBe('Unknown')
    expect(healthWordFor('unknown')).toBe('Unknown')
    expect(healthWordFor('not_checked')).toBe('Not checked')
    // A word this browser has never heard of is "Unknown" — never the
    // specific claim "Not checked", and never blank.
    expect(healthWordFor('some_future_word')).toBe('Unknown')
    expect(healthWordFor('some_future_word')).not.toBe('')
    // No health field at all is still "Not checked" — that is a server
    // saying nothing, which is a different thing from a server saying
    // "there is no health to report".
    expect(healthWordFor(undefined)).toBe('Not checked')
  })

  it('states the missing Secret as a fact, and offers neither a create nor a repair action', async () => {
    renderView(missingSelfManaged())
    await waitForView()
    const liveSecret = screen.getByTestId('recon-condition-live_secret')
    expect(liveSecret.textContent).toContain(S.condLiveMissing)
    // No action of any kind — the server's plan offers none.
    expect(screen.queryByTestId('recon-action-repair')).toBeNull()
    expect(screen.queryByTestId('recon-action-sync')).toBeNull()
    expect(screen.queryByTestId('recon-action-takeover')).toBeNull()
    expect(screen.queryByTestId('recon-action-migrate')).toBeNull()
    // And no promise that a Secret is on its way. The create sentence
    // belongs to the modes Sharko really does create Secrets for.
    //
    // Asserted on the element, not on a phrase: the phrase guard here used to
    // read /will create/i, which the server's own rewording just retired —
    // it would have gone free without anything failing. The plan block is
    // where every promise this page can make renders, so its absence is the
    // check that survives a rewrite.
    expect(screen.queryByTestId('recon-plan')).toBeNull()
    expect(screen.queryByTestId('recon-plan-automatic')).toBeNull()
    expect(screen.queryByText(S.planAutoCreate)).toBeNull()
  })

  it('the ArgoCD condition says there is nothing to probe — never the never-probed or unreachable sentence', async () => {
    renderView(missingSelfManaged())
    await waitForView()
    // Asserted by IDENTITY, positives first.
    //
    // This replaces `not.toContain('Sharko could not ask ArgoCD')`, which was
    // dead twice over: the fixture set the condition detail to something else
    // entirely, so nothing on the page could ever have matched it; and the
    // constant behind that phrase (condArgoCDUnreachable) has since been
    // deleted from the server, so after the restack no handler can emit it
    // under any circumstance.
    const argo = screen.getByTestId('recon-condition-argocd_connection')
    expect(argo.getAttribute('data-condition-status')).toBe('attention')
    expect(argo.textContent).toContain(COND_ARGO_NO_CONNECTION)
    // And it is none of the other three real ArgoCD sentences. Each answers a
    // different question, and swapping one in here would be a wrong answer,
    // not a wording change.
    expect(argo.textContent).not.toContain(COND_ARGO_NOT_CHECKED)
    expect(argo.textContent).not.toContain(COND_ARGO_UNAVAILABLE)
    expect(argo.textContent).not.toContain(COND_ARGO_CONNECTED)
  })
})

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
          headline: CONNECTION_SENTENCES.headlineConfigurationMatchesEKS,
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
          headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval,
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

  /**
   * B7: "Do not rewrite the server's promises in the browser; only provide
   * visual structure around the returned fields."
   *
   * PLAN_UNTOUCHED_SENTENCE is composed in the browser, so showing it where
   * the server promised nothing is the browser authoring a promise — and on
   * a legacy-inline connection, one that is not even true: Sharko writes
   * nothing there, so there is no write whose scope needs bounding.
   */
  describe('the Preserved line only appears where the server said a write is coming', () => {
    it('a clean legacy-inline connection gets the migration link and NO preserved promise', async () => {
      renderView(
        makeView({
          management_mode: 'legacy_inline',
          managed_scope: 'addon_labels',
          mode_statement: S.modeLegacy,
          definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, credential_source_type: 'inline-kubeconfig' },
          sync: {
            state: 'unknown',
            verification_scope: 'partial',
            approval_required: false,
            headline: CONNECTION_SENTENCES.headlineVerificationIncomplete,
            qualifier: CONNECTION_SENTENCES.qualifierLegacyInline,
            reason: S.modeLegacy,
            checked_at: '2026-08-18T10:00:00Z',
          },
          // No credential_reference condition, so the migration link has no
          // condition to sit beside and lands in the PLAN section — which is
          // what makes the plan block render at all here. (With that
          // condition present the link renders beside it and the plan block
          // is absent, so a fixture that keeps it would pass this test
          // vacuously. It did, first time round.)
          conditions: [{ id: 'argocd_connection', status: 'ok', detail: S.condArgoOK }],
          // The server promises nothing: no automatic write, no scopes.
          plan: { action: 'migrate_credentials', action_scopes: [] },
        }),
      )
      await waitForView()
      // The plan block DOES render — it carries the migration link. Asserted
      // first so this test can never pass just because the block was absent.
      expect(screen.getByTestId('recon-plan')).toBeTruthy()
      expect(screen.getByTestId('recon-action-migrate')).toBeTruthy()
      // But nothing claims anything is preserved, because nothing is written.
      expect(screen.queryByTestId('recon-plan-untouched')).toBeNull()
      expect(screen.queryByTestId('recon-plan-preserved-label')).toBeNull()
      expect(screen.getByTestId('recon-view').textContent ?? '').not.toContain(PLAN_UNTOUCHED_SENTENCE)
    })

    it('a foreign-owned connection makes no preserved promise either — Sharko owns nothing on it', async () => {
      // Same reason as above: no ownership condition, so the takeover action
      // lands in the plan section and the block really renders.
      renderView(foreignOwnedOrdinary())
      await waitForView()
      expect(screen.getByTestId('recon-plan')).toBeTruthy()
      expect(screen.getByTestId('recon-plan-action')).toBeTruthy()
      expect(screen.queryByTestId('recon-plan-untouched')).toBeNull()
      expect(screen.getByTestId('recon-view').textContent ?? '').not.toContain(PLAN_UNTOUCHED_SENTENCE)
    })

    it('an automatic write DOES get it — that write really does leave everything else alone', async () => {
      renderView(
        makeView({
          sync: {
            state: 'out_of_sync',
            verification_scope: 'full',
            approval_required: false,
            headline: CONNECTION_SENTENCES.headlineOutOfSync,
            reason: S.labelsOnly,
            checked_at: '2026-08-18T10:00:00Z',
          },
          conditions: [{ id: 'comparison', status: 'attention', detail: S.condCompDrift }],
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
      expect(screen.getByTestId('recon-plan-untouched').textContent).toBe(PLAN_UNTOUCHED_SENTENCE)
    })

    it('an offered action that names its scopes DOES get it — the scopes are what it bounds', async () => {
      renderView(
        makeView({
          sync: {
            state: 'out_of_sync',
            verification_scope: 'full',
            approval_required: true,
            headline: CONNECTION_SENTENCES.headlineOutOfSyncApproval,
            reason: S.approvalRequired,
            checked_at: '2026-08-18T10:00:00Z',
          },
          conditions: [{ id: 'approval', status: 'blocked', detail: S.condApproval }],
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
      expect(screen.getByTestId('recon-plan-untouched').textContent).toBe(PLAN_UNTOUCHED_SENTENCE)
    })
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
          headline: CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync,
          qualifier: CONNECTION_SENTENCES.qualifierSelfManaged,
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

// ─────────────────────────────────────────────────────────────────────────────
// P6 — the foreign-owned state, browser side.
//
// The ruling: presentation structure must follow typed facts, never equality
// between human sentences. The foreign-owned ownership row was the case that
// exposed the old pattern — its de-duplication depended on two hand-written
// sentences in two Go packages staying byte-identical, and one character of
// drift would have silently doubled a sentence on the page with no test able
// to notice.
//
// So every assertion below reads TESTIDS and TYPED STATE. The two places a
// sentence is named at all are the mode statement itself (counted, never
// matched against another sentence) and the fixtures, which are re-read
// copies of the Go constants.
// ─────────────────────────────────────────────────────────────────────────────

describe('P6 — foreign-owned: the boundary is stated once, and the takeover door survives', () => {
  it('tuple A (ordinary foreign ownership) renders the boundary once, one condition, and the takeover in the plan section', async () => {
    renderView(foreignOwnedOrdinary())
    await waitForView()

    // 1. The boundary sentence, in its ONE place.
    expect(screen.getByTestId('recon-mode-statement').textContent).toBe(S.modeForeign)
    // And nowhere else on the page — not as a condition detail, not as the
    // sync reason. Counted, so a second copy fails whatever element it lands in.
    expect(screen.queryAllByText(S.modeForeign)).toHaveLength(1)

    // 2. The summary: blocked, nothing verified, and NO reason line — the
    //    comparison sends no limit sentence for an ownership conflict.
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineBlocked)
    expect(screen.queryByTestId('recon-sync-qualifier')).toBeNull()
    expect(screen.queryByTestId('recon-sync-reason')).toBeNull()

    // 3. Conditions: the ArgoCD health fact, and nothing else. BREAK TEST 8
    //    aims here — restoring an ownership condition to the fixture fails
    //    this line.
    expect(renderedConditionIds()).toEqual(['argocd_connection'])
    expectNoEmptyConditionCards()

    // 4. The plan section carries the takeover and promises nothing.
    expect(screen.getByTestId('recon-plan')).toBeTruthy()
    expect(screen.queryByTestId('recon-plan-automatic')).toBeNull()
    expect(screen.queryByTestId('recon-plan-approval')).toBeNull()
    expect(screen.queryByTestId('recon-plan-untouched')).toBeNull()
    expectNoBannedWording()
  })

  it('tuple B (foreign ownership AND the credentials backend could not be read) names the failed step instead of swallowing it', async () => {
    renderView(foreignOwnedBackendUnread())
    await waitForView()

    // The check did not finish, so the summary says so and carries the
    // backend-read failure as its reason.
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineUnknownCheckFailed)
    expect(screen.getByTestId('recon-sync-reason').textContent).toBe(S.failBackendRead)

    // The three facts the server now sends, and no ownership row among them.
    // BREAK TEST 8 aims here too.
    expect(renderedConditionIds()).toEqual(['argocd_connection', 'credential_reference', 'git_definition'])
    expectNoEmptyConditionCards()

    // The failed step is a BLOCKED condition, by typed status — not by the
    // words it happens to carry.
    const credRef = screen.getByTestId('recon-condition-credential_reference')
    expect(credRef.getAttribute('data-condition-status')).toBe('blocked')
    expect(credRef.textContent).toContain(S.condCredUnread)
    // Two different sentences: the row states the fact, the summary states
    // the failure. Neither repeats the other.
    expect(credRef.textContent).not.toContain(S.failBackendRead)

    // THE RULING: with a backend-read failure, the management statement shows
    // ONCE and the backend failure shows ONCE. The page used to suppress the
    // statement on every failed check, which left this reader with a "Take
    // ownership" button and nothing saying why. foreign_owned is a mode the
    // server can only reach by classifying, so it is stated here too.
    //
    // Counted, not merely present: the whole point of the ruling is "once".
    expect(screen.getByTestId('recon-mode-statement').textContent).toBe(S.modeForeign)
    expect(screen.queryAllByText(S.modeForeign)).toHaveLength(1)
    // And the failure sentence is still said exactly once, in the summary.
    expect(screen.queryAllByText(S.failBackendRead)).toHaveLength(1)
    expectNoBannedWording()
  })

  // ── BREAK TEST 5 ────────────────────────────────────────────────────────
  // Neither tuple builds a condition card with no words in it. The mutation:
  // make the browser construct a card from a condition with empty text.
  it.each([
    ['tuple A — ordinary', foreignOwnedOrdinary],
    ['tuple B — backend unread', foreignOwnedBackendUnread],
  ])('%s builds no ownership card and no empty condition card', async (_name, fixture) => {
    renderView(fixture())
    await waitForView()
    // Opens the routine fold first: an `ok` card the page invented would
    // otherwise hide inside it and every "not there" check would pass for
    // the wrong reason.
    expect(renderedConditionIds()).not.toContain('ownership')
    expect(screen.queryByTestId('recon-condition-ownership')).toBeNull()
    expectNoEmptyConditionCards()
  })

  // ── BREAK TEST 6 ────────────────────────────────────────────────────────
  // Both tuples keep the takeover door, and it relocated to the plan section
  // because no condition exists for it to hang off. The mutation: remove the
  // plan-section fallback.
  it.each([
    ['tuple A — ordinary', foreignOwnedOrdinary],
    ['tuple B — backend unread', foreignOwnedBackendUnread],
  ])('%s keeps the Take over action, once, in the plan section', async (_name, fixture) => {
    renderView(fixture())
    await waitForView()

    // It exists at all.
    const button = screen.getByTestId('recon-action-takeover')
    // It is inside the plan section.
    expect(within(screen.getByTestId('recon-plan-action')).getByTestId('recon-action-takeover')).toBe(button)
    // And beside no condition card.
    for (const card of Array.from(document.querySelectorAll('[data-testid^="recon-condition-"]'))) {
      expect(card.querySelector('[data-testid="recon-action-takeover"]')).toBeNull()
    }
    // Exactly one door, and no prose beside it repeating the instruction.
    expect(screen.getAllByTestId('recon-action-takeover')).toHaveLength(1)
    expect(screen.getAllByText(ACTION_TAKE_OWNERSHIP)).toHaveLength(1)
    const plan = screen.getByTestId('recon-plan')
    expect(within(plan).queryByTestId('recon-plan-automatic')).toBeNull()
    expect(within(plan).queryByTestId('recon-plan-approval')).toBeNull()
    expect(within(plan).queryByTestId('recon-plan-untouched')).toBeNull()
  })

  it('the browser picks the takeover’s home from the condition list, not from any sentence', async () => {
    // The typed lookup, exercised directly: with no ownership condition the
    // target is null, which is what sends the button to the plan section.
    expect(actionTargetConditionId(foreignOwnedOrdinary())).toBeNull()
    expect(actionTargetConditionId(foreignOwnedBackendUnread())).toBeNull()
  })

  // ── P7 ──────────────────────────────────────────────────────────────────
  // The management statement is stated on BOTH tuples, exactly once each.
  // The mutation this catches: putting the blanket check-failed suppression
  // back, which drops it from tuple B.
  it.each([
    ['tuple A — ordinary', foreignOwnedOrdinary],
    ['tuple B — backend unread', foreignOwnedBackendUnread],
  ])('%s states the management mode exactly once', async (_name, fixture) => {
    renderView(fixture())
    await waitForView()
    expect(screen.getAllByTestId('recon-mode-statement')).toHaveLength(1)
    expect(screen.getByTestId('recon-mode-statement').textContent).toBe(S.modeForeign)
    // Counted over the whole page: a second copy fails whatever element it
    // lands in, which is the mutation "render the statement twice".
    expect(screen.queryAllByText(S.modeForeign)).toHaveLength(1)
  })

  // ── P7 ──────────────────────────────────────────────────────────────────
  // The state-model principle: WHICH elements render follows typed facts, so
  // rewording the statement must move nothing. The mutation: change a word in
  // the mode statement — only a wording pin may fail, never a structure one.
  it.each([
    ['tuple A — ordinary', foreignOwnedOrdinary],
    ['tuple B — backend unread', foreignOwnedBackendUnread],
  ])('%s renders the same elements when the statement is worded differently', async (_name, fixture) => {
    // This sentence is deliberately NOT one of the server's — it is the
    // mutation. Replacing it with a generated constant would delete the test:
    // the point is that the page's STRUCTURE is indifferent to the words.
    const reworded = { ...fixture(), mode_statement: 'Another tool has this connection. Sharko leaves it alone.' }
    renderView(reworded)
    await waitForView()
    // Same element, same count, same conditions, same door — only the words
    // inside the statement differ.
    expect(screen.getAllByTestId('recon-mode-statement')).toHaveLength(1)
    expect(screen.getByTestId('recon-mode-statement').textContent).toBe(reworded.mode_statement)
    expect(renderedConditionIds()).toEqual(reworded.conditions.map((c) => c.id).sort())
    expect(screen.getByTestId('recon-action-takeover')).toBeTruthy()
  })

  // ── P7 ──────────────────────────────────────────────────────────────────
  // Dropping the ownership row left tuple A with exactly ONE routine
  // condition, and the counting template rendered "All 1 checks passed."
  // The mutation this catches: restoring the bare plural template.
  it('tuple A folds its single routine success into a sentence that reads like English', async () => {
    renderView(foreignOwnedOrdinary())
    await waitForView()
    const compact = screen.getByTestId('recon-conditions-compact')
    // toBe, not toContain: "All 1 checks passed." would satisfy a loose match.
    expect(compact.textContent).toBe('1 check passed.')
    // And the same sentence after the fold is opened — the two buttons used
    // to carry two copies of one expression.
    fireEvent.click(compact)
    expect(screen.getByTestId('recon-conditions-collapse').textContent).toBe('1 check passed.')
  })

  // ── P7 ──────────────────────────────────────────────────────────────────
  // The other half of the same fix: one routine success while something else
  // needs attention. Same sentence, and still not "1 checks passed."
  it('a single routine success beside a failed condition reads the same way', async () => {
    const view = foreignOwnedBackendUnread()
    renderView({
      ...view,
      conditions: [
        { id: 'credential_reference', status: 'blocked', detail: S.condCredUnread },
        { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
      ] as ConnectionReconciliation['conditions'],
    })
    await waitForView()
    expect(screen.getByTestId('recon-conditions-compact').textContent).toBe('1 check passed.')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// P7 — the F7 suppression still applies to the modes the server can DEFAULT
// to. Removing the exception's condition entirely (statement always shown)
// has to fail here.
// ─────────────────────────────────────────────────────────────────────────────

describe('P7 — a defaulted mode is still not stated on a failed check', () => {
  it('legacy_inline on a failed check shows no mode statement', async () => {
    renderView(
      makeView({
        management_mode: 'legacy_inline',
        managed_scope: 'addon_labels',
        mode_statement: S.modeLegacy,
        definition: { file: 'managed-clusters.yaml', branch: 'main', desired_revision: FULL_SHA, credential_source_type: 'inline-kubeconfig' },
        sync: { headline: CONNECTION_SENTENCES.headlineUnknownCheckFailed, state: 'unknown', verification_scope: 'none', approval_required: false, reason: S.checkFail, checked_at: '2026-08-18T10:00:00Z' },
        conditions: [
          { id: 'comparison', status: 'blocked', detail: S.checkFail },
          { id: 'argocd_connection', status: 'ok', detail: S.condArgoOK },
        ],
        plan: { action: 'none', action_scopes: [] },
      }),
    )
    await waitForView()
    expect(screen.queryByTestId('recon-mode-statement')).toBeNull()
    expect(screen.queryAllByText(S.modeLegacy)).toHaveLength(0)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// R2-5: the two surfaces cannot disagree, and the folded-successes line is
// total.
// ─────────────────────────────────────────────────────────────────────────────

describe('the synced word is refused at a scope that cannot support it — the same guard the fleet row has', () => {
  it('renders the not-checked headline, not the synced one the server sent', async () => {
    // A response the server should never send: it says synced while telling
    // us only part of the owned scope was compared. The fleet row has refused
    // this since B13; this page rendered the headline verbatim, so ONE
    // response put "Connection synced" here and "Not checked yet" in the list
    // for the same connection.
    renderView(
      makeView({
        sync: {
          state: 'synced',
          verification_scope: 'partial',
          approval_required: false,
          headline: CONNECTION_SENTENCES.headlineConnectionSynced,
          checked_at: '2026-08-18T10:00:00Z',
        },
      }),
    )
    await waitForView()
    const headline = screen.getByTestId('recon-sync-headline')
    expect(headline.textContent).toBe(CONNECTION_SENTENCES.headlineNotCheckedYet)
    expect(headline.textContent).not.toBe(CONNECTION_SENTENCES.headlineConnectionSynced)
  })

  it('says nothing at scope "none" either — no scope verified is no grounds for a synced word', async () => {
    renderView(
      makeView({
        sync: {
          state: 'synced',
          verification_scope: 'none',
          approval_required: false,
          headline: CONNECTION_SENTENCES.headlineConnectionSynced,
          checked_at: '2026-08-18T10:00:00Z',
        },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(CONNECTION_SENTENCES.headlineNotCheckedYet)
  })

  it('leaves a properly scoped synced answer alone — the guard only ever downgrades', async () => {
    renderView(makeView())
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(
      CONNECTION_SENTENCES.headlineConnectionSynced,
    )
  })

  it('leaves every other state alone, whatever its scope — this is not a second derivation', async () => {
    renderView(
      makeView({
        sync: {
          state: 'unknown',
          verification_scope: 'partial',
          approval_required: false,
          headline: CONNECTION_SENTENCES.headlineConfigurationMatchesEKS,
          checked_at: '2026-08-18T10:00:00Z',
        },
      }),
    )
    await waitForView()
    expect(screen.getByTestId('recon-sync-headline').textContent).toBe(
      CONNECTION_SENTENCES.headlineConfigurationMatchesEKS,
    )
  })
})

describe('the folded routine-successes line never claims a check that did not happen', () => {
  it('says nothing passed when nothing ran, instead of "All 0 checks passed."', () => {
    // The sibling of the "All 1 checks passed." defect. Both render sites sit
    // behind `routineConditions.length > 0` today, and that guard is one edit
    // away from not being there — which is exactly the distance the plural
    // noun was.
    expect(routineChecksSummary(0, true)).toBe('No routine checks.')
    expect(routineChecksSummary(0, true)).not.toContain('0 checks passed')
    expect(routineChecksSummary(0, false)).toBe('No routine checks.')
    // A negative count cannot arrive from an array length, but the function
    // is total rather than correct-if-called-correctly.
    expect(routineChecksSummary(-1, true)).toBe('No routine checks.')
  })

  it('still says the other three things exactly as it did', () => {
    expect(routineChecksSummary(1, true)).toBe('1 check passed.')
    expect(routineChecksSummary(1, false)).toBe('1 check passed.')
    expect(routineChecksSummary(6, true)).toBe('All 6 checks passed.')
    expect(routineChecksSummary(5, false)).toBe('5 checks passed.')
  })
})

describe('both surfaces read ONE health-word table', () => {
  it('this page and the fleet list are the same object, not two that agree', () => {
    // They used to be two Records that happened to hold the same four words,
    // held together by a comment in each file saying so. Object identity is
    // the assertion, because it is the only one that cannot be satisfied by
    // two copies staying accidentally in step.
    expect(HEALTH_WORDS).toBe(CONNECTION_HEALTH_WORDS)
    expect(HEALTH_COLUMN_WORDS).toBe(CONNECTION_HEALTH_WORDS)
    expect(healthWordFor).toBe(connectionHealthWord)
    expect(healthColumnWord).toBe(connectionHealthWord)
  })

  it('all four words are pinned, as written-out literals', () => {
    // Written out, not read off the constant: an assertion that reads the
    // same table the code reads compares a thing with itself.
    expect(CONNECTION_HEALTH_WORDS.connected).toBe('Connected')
    expect(CONNECTION_HEALTH_WORDS.unavailable).toBe('Unavailable')
    expect(CONNECTION_HEALTH_WORDS.not_checked).toBe('Not checked')
    expect(CONNECTION_HEALTH_WORDS.unknown).toBe('Unknown')
  })
})
