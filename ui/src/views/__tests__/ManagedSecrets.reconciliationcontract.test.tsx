// ManagedSecrets.reconciliationcontract — Story B5's browser half.
//
// THE DEFECT THIS SUITE EXISTS FOR. The fleet list showed `spoke-us` as a
// green **Synced** while the same connection's own page said **Verification
// incomplete**, and a "Not compared" chip sat right beside the green word.
// That connection is a legacy pasted-credential one: its credential was
// never compared at all. The list was answering from the cluster
// reconciler's addon-label-only record — a second vocabulary — while the
// page answered from the real comparison.
//
// The product owner's ruling, verbatim: "The fleet and detail page must
// derive their state from the same canonical reconciliation semantics. Do
// not maintain a second legacy vocabulary or duplicate status derivation in
// the browser."
//
// So every fixture below is a REAL server answer — the canonical fields the
// server now computes once and both surfaces render. What this suite pins,
// across all four management modes:
//
//   - a row's status WORD is the server's `headline`, verbatim;
//   - `synced` is never rendered and never COUNTED when the verification is
//     not full — checked separately, because a green word and a wrong chip
//     count are two different lies;
//   - a legacy-inline row is neutral, never green;
//   - a self-managed row with full label verification says "Addon labels
//     synced" and offers no manual label-sync door (ruling a);
//   - a foreign-owned row says "Managed elsewhere"/"Blocked" and offers no
//     Sharko repair;
//   - the Health column answers ArgoCD's own question, independently;
//   - and the fail-closed guard: a response that claims synced at a scope
//     that cannot support it is refused by the browser too.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import {
  ManagedSecrets,
  rowStatus,
  connectionRowLabel,
  HEALTH_COLUMN_WORDS,
  healthColumnWord,
  syncGateFor,
  syncConfirmDescription,
  type UnifiedRow,
} from '@/views/ManagedSecrets'
import {
  SYNC_ADDON_LABELS_NOTHING_TO_APPLY,
  SYNC_ADDON_LABELS_HINT,
  SHARKO_REAPPLIES_ADDON_LABELS,
  syncAddonLabelsConfirmText,
} from '@/components/ClusterActionHints'
import { AuthContext } from '@/hooks/useAuth'
import type { ConnectionSecretRow, ManagedSecretsResponse } from '@/services/models'
import { CONNECTION_SENTENCES } from '@/generated/connection-sentences'

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

vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual('@/components/ToastNotification')
  return { ...actual, showToast: vi.fn() }
})

vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: vi.fn(),
    getConnectionComparison: vi.fn(),
    getConnectionReconciliation: vi.fn(),
  },
  takeoverPreflight: vi.fn(),
  takeoverCluster: vi.fn(),
  dropLegacyLabels: vi.fn(),
  // NOTE: deliberately NOT wrapped in withCanonicalConnectionRows. Every
  // fixture here states its canonical fields itself, because this is the
  // suite that pins what those fields do.
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  getConnectionSecretResource: vi.fn(),
  getAddonValuesSecretResource: vi.fn(),
  checkAllAddonValuesSecrets: vi.fn(),
  reconcileCluster: vi.fn(),
  resyncClusterLabels: vi.fn(),
  refreshAddonValuesSecret: vi.fn(),
  syncAddonValuesSecret: vi.fn(),
  deleteOrphanedSecret: vi.fn(),
  fetchAuditLog: vi.fn().mockResolvedValue({ entries: [] }),
}))

// ─────────────────────────────────────────────────────────────────────────────
// The four management modes, as the server really answers them.
// ─────────────────────────────────────────────────────────────────────────────

/** Matrix row 1 — clean secret-kubeconfig. The ONE row that may read green. */
const sharkoManagedClean: ConnectionSecretRow = {
  cluster: 'prod-eu',
  secret_namespace: 'argocd',
  secret_name: 'prod-eu',
  state: 'in_sync',
  management_mode: 'sharko_managed',
  managed_scope: 'full_connection',
  sync_state: 'synced',
  verification_scope: 'full',
  approval_required: false,
  headline: CONNECTION_SENTENCES.headlineConnectionSynced,
  health: 'connected',
  source: 'git',
  self_heals: true,
  last_checked: '2026-08-19T00:00:00Z',
}

/** Matrix row 2 — clean EKS. Everything comparable matched; the credential could not be compared. */
const sharkoManagedEKS: ConnectionSecretRow = {
  cluster: 'spoke-eks',
  secret_namespace: 'argocd',
  secret_name: 'spoke-eks',
  state: 'unknown',
  management_mode: 'sharko_managed',
  managed_scope: 'full_connection',
  sync_state: 'unknown',
  verification_scope: 'partial',
  approval_required: false,
  headline: CONNECTION_SENTENCES.headlineConfigurationMatchesEKS,
  health: 'connected',
  source: 'git',
  self_heals: true,
  last_checked: '2026-08-19T00:00:00Z',
}

/** Matrix row 12 — legacy inline, present and clean. THE ROW THAT USED TO LIE. */
const legacyInlineClean: ConnectionSecretRow = {
  cluster: 'spoke-us',
  secret_namespace: 'argocd',
  secret_name: 'spoke-us',
  state: 'unknown',
  management_mode: 'legacy_inline',
  managed_scope: 'addon_labels',
  sync_state: 'unknown',
  verification_scope: 'partial',
  approval_required: false,
  headline: CONNECTION_SENTENCES.headlineVerificationIncomplete,
  qualifier:
    CONNECTION_SENTENCES.qualifierLegacyInline,
  health: 'connected',
  source: 'git',
  self_heals: true,
  last_checked: '2026-08-19T00:00:00Z',
}

/** Matrix row 13 — self-managed, every owned addon label compared and matching. */
const selfManagedClean: ConnectionSecretRow = {
  cluster: 'guest-1',
  secret_namespace: 'argocd',
  secret_name: 'guest-1',
  state: 'in_sync',
  management_mode: 'self_managed',
  managed_scope: 'addon_labels',
  sync_state: 'synced',
  verification_scope: 'full',
  approval_required: false,
  headline: CONNECTION_SENTENCES.headlineAddonLabelsSynced,
  qualifier: CONNECTION_SENTENCES.qualifierSelfManaged,
  health: 'connected',
  source: 'git',
  self_heals: true,
  last_checked: '2026-08-19T00:00:00Z',
}

/** Matrix row 14 — self-managed with drifted labels. Ruling (a): it converges by itself. */
const selfManagedDrifted: ConnectionSecretRow = {
  ...selfManagedClean,
  cluster: 'guest-2',
  secret_name: 'guest-2',
  state: 'out_of_sync',
  sync_state: 'out_of_sync',
  headline: CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync,
}

/**
 * (B13 item 3) The row the matrix had no entry for: self-managed, and the
 * Secret is not there. Health `unknown` is the FOURTH word — there is no
 * Secret for ArgoCD to probe, so "Not checked" would promise a check that is
 * never coming.
 */
const selfManagedMissingSecret: ConnectionSecretRow = {
  ...selfManagedClean,
  cluster: 'guest-nosecret',
  secret_name: 'guest-nosecret',
  state: 'missing',
  sync_state: 'blocked',
  verification_scope: 'none',
  headline: CONNECTION_SENTENCES.headlineConnectionSecretMissing,
  health: 'unknown',
}

/** Matrix row 9 — foreign ownership. */
const foreignOwned: ConnectionSecretRow = {
  cluster: 'other-tool',
  secret_namespace: 'argocd',
  secret_name: 'other-tool',
  state: 'foreign',
  management_mode: 'foreign_owned',
  managed_scope: 'none',
  sync_state: 'blocked',
  verification_scope: 'none',
  approval_required: false,
  headline: CONNECTION_SENTENCES.headlineBlocked,
  health: 'connected',
  source: 'git',
  self_heals: false,
  last_checked: '2026-08-19T00:00:00Z',
}

function response(rows: ConnectionSecretRow[]): ManagedSecretsResponse {
  return {
    cluster_connection_secrets: rows,
    addon_values_secrets: [],
    orphaned_secrets: [],
    engines: {
      cluster_connection: { enabled: true, wired: true, interval_seconds: 30, last_run: '2026-08-19T00:00:00Z' },
      addon_values: { enabled: true, wired: true, interval_seconds: 300, last_run: '2026-08-19T00:00:00Z' },
    },
    addon_values_secret_source: 'AWS Secrets Manager',
  }
}

function renderConnections() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={['/secrets/connections']}>
        <Routes>
          <Route path="/secrets/connections" element={<ManagedSecrets area="connections" />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  )
}

/** The Synced chip's own count, as the reader sees it. */
function syncedChipCount(): number {
  const chip = screen.getByTestId('filter-chip-in_sync')
  const digits = chip.textContent?.match(/\d+/)
  return digits ? Number(digits[0]) : -1
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ─────────────────────────────────────────────────────────────────────────────
// The word — the server's, verbatim, in every mode
// ─────────────────────────────────────────────────────────────────────────────

describe('a connection row renders the SERVER\'s headline, in every management mode', () => {
  const cases: [string, ConnectionSecretRow, string][] = [
    ['sharko-managed, clean', sharkoManagedClean, CONNECTION_SENTENCES.headlineConnectionSynced],
    ['sharko-managed, EKS', sharkoManagedEKS, CONNECTION_SENTENCES.headlineConfigurationMatchesEKS],
    ['legacy inline, clean', legacyInlineClean, CONNECTION_SENTENCES.headlineVerificationIncomplete],
    ['self-managed, clean', selfManagedClean, CONNECTION_SENTENCES.headlineAddonLabelsSynced],
    ['self-managed, drifted', selfManagedDrifted, CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync],
    ['foreign-owned', foreignOwned, CONNECTION_SENTENCES.headlineBlocked],
  ]

  for (const [what, row, expected] of cases) {
    it(`${what} → "${expected}"`, async () => {
      mockGetManagedSecrets.mockResolvedValue(response([row]))
      renderConnections()
      const tableRow = await screen.findByTestId(`secret-row-connection-${row.cluster}`)
      expect(within(tableRow).getByTestId('status-mark')).toHaveTextContent(expected)
    })
  }
})

// ─────────────────────────────────────────────────────────────────────────────
// synced is never rendered AND never counted without full verification
// ─────────────────────────────────────────────────────────────────────────────

describe('the synchronization invariant holds on the fleet, in both the word and the count', () => {
  it('EKS partial verification is not rendered as synced and is not counted under Synced', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([sharkoManagedEKS]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-eks')
    const mark = within(row).getByTestId('status-mark')
    expect(mark).not.toHaveTextContent(/^Synced$/)
    expect(mark).not.toHaveTextContent(CONNECTION_SENTENCES.headlineConnectionSynced)
    expect(mark).toHaveAttribute('data-status', 'unknown')
    expect(syncedChipCount()).toBe(0)
  })

  it('THE ORIGINAL DEFECT: a clean legacy-inline row is not rendered as synced and is not counted under Synced', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([legacyInlineClean]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-us')
    const mark = within(row).getByTestId('status-mark')
    expect(mark).toHaveTextContent(CONNECTION_SENTENCES.headlineVerificationIncomplete)
    expect(mark).not.toHaveTextContent(/Synced/)
    // Neutral styling — never the green synchronized dot.
    expect(mark).toHaveAttribute('data-status', 'unknown')
    expect(within(mark).getByTestId('status-dot')).toHaveAttribute('data-hollow', 'true')
    expect(syncedChipCount()).toBe(0)
  })

  // B13 item 7 — this test used to assert the browser-invented chip that has
  // now been deleted. It asserts the opposite property, which is the one that
  // was never checked: the row says the fact ONCE.
  //
  // WHY IT COUNTS INSTEAD OF MATCHING. The old shape of this check ("the
  // sentence is present") passes whether the row says it once or four times,
  // which is exactly how the repeat survived. Counting occurrences is the
  // only assertion that fails when a second copy comes back.
  it('the row states the not-compared fact exactly once — the server headline, with no browser chip repeating it', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([sharkoManagedEKS]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-eks')

    // The server headline for this row already carries the fact.
    expect((sharkoManagedEKS.headline ?? '').toLowerCase()).toContain(
      CONNECTION_SENTENCES.qualifierCredentialNotCompared.toLowerCase(),
    )

    const occurrences = (haystack: string, needle: string) => haystack.split(needle).length - 1
    expect(occurrences(
        (row.textContent ?? '').toLowerCase(),
        CONNECTION_SENTENCES.qualifierCredentialNotCompared.toLowerCase(),
      )).toBe(1)
    // The tail both wordings share. The deleted chip read "Credential not
    // compared" and the headline reads "credential content not compared", so
    // matching on the common tail catches the repeat whichever words come
    // back — and it is a COUNT, so one copy passes and two do not.
    expect(occurrences((row.textContent ?? '').toLowerCase(), 'not compared')).toBe(1)

    // And the chip itself is gone, on every row shape that used to grow one.
    expect(within(row).queryByTestId('credential-check-badge')).toBeNull()
  })

  it('the legacy-inline row grows no verification chip either — the headline is the only word about scope', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([legacyInlineClean]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-us')
    expect(within(row).queryByTestId('credential-check-badge')).toBeNull()
    expect(within(row).getByTestId('status-mark')).toHaveTextContent(CONNECTION_SENTENCES.headlineVerificationIncomplete)
  })

  it('the Synced chip counts ONLY the rows that really are synced at full verification', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      response([
        sharkoManagedClean,
        sharkoManagedEKS,
        legacyInlineClean,
        selfManagedClean,
        selfManagedDrifted,
        foreignOwned,
        // The shape the OLD server sent for spoke-us: the legacy word
        // in_sync on a connection whose credential was never compared. It is
        // in this count deliberately — the count is where the lie was least
        // visible.
        { ...legacyInlineClean, cluster: 'spoke-us-old', secret_name: 'spoke-us-old', state: 'in_sync', sync_state: 'synced', headline: CONNECTION_SENTENCES.headlineConnectionSynced },
      ]),
    )
    renderConnections()
    await screen.findByTestId('secret-row-connection-prod-eu')
    // prod-eu (full connection) and guest-1 (full of its owned addon labels).
    // Nothing else, and above all not the old-shaped row.
    expect(syncedChipCount()).toBe(2)
  })

  it('the Synced FILTER shows only those same rows', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(
      response([sharkoManagedClean, sharkoManagedEKS, legacyInlineClean, selfManagedClean, foreignOwned]),
    )
    renderConnections()
    await screen.findByTestId('secret-row-connection-prod-eu')
    await user.click(screen.getByTestId('filter-chip-in_sync'))
    await waitFor(() => expect(screen.queryByTestId('secret-row-connection-spoke-us')).not.toBeInTheDocument())
    expect(screen.getByTestId('secret-row-connection-prod-eu')).toBeInTheDocument()
    expect(screen.getByTestId('secret-row-connection-guest-1')).toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-connection-spoke-eks')).not.toBeInTheDocument()
    expect(screen.queryByTestId('secret-row-connection-other-tool')).not.toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The fail-closed guard (break tests 1 and 2 bite here)
// ─────────────────────────────────────────────────────────────────────────────

describe('the browser refuses a synced claim the verification cannot support', () => {
  /** A response the fixed server cannot produce — the shape the defect had. */
  const impossible: ConnectionSecretRow = {
    ...legacyInlineClean,
    state: 'in_sync',
    sync_state: 'synced',
    verification_scope: 'partial',
    headline: CONNECTION_SENTENCES.headlineConnectionSynced,
  }

  it('renders it as "Not checked yet", never the synced word it was handed', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([impossible]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-us')
    const mark = within(row).getByTestId('status-mark')
    expect(mark).toHaveTextContent(CONNECTION_SENTENCES.headlineNotCheckedYet)
    expect(mark).not.toHaveTextContent(CONNECTION_SENTENCES.headlineConnectionSynced)
    expect(mark).toHaveAttribute('data-status', 'unknown')
  })

  it('does not count it under Synced either', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([impossible]))
    renderConnections()
    await screen.findByTestId('secret-row-connection-spoke-us')
    expect(syncedChipCount()).toBe(0)
  })

  it('a row with no canonical answer at all reads "Not checked yet", never the old label-only Synced', () => {
    const preB5 = { kind: 'connection', key: 'connection-old', cluster: 'old', state: 'in_sync', sourceLabel: 'git', selfHeals: true } as UnifiedRow
    expect(rowStatus(preB5)).toBe('unknown')
    expect(connectionRowLabel(preB5)).toBe(CONNECTION_SENTENCES.headlineNotCheckedYet)
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Health — the second, independent question
// ─────────────────────────────────────────────────────────────────────────────

describe('the fleet answers ArgoCD health separately from the Git state', () => {
  it('replaces "Compared with" with a Health column on the connections subpage', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([legacyInlineClean]))
    renderConnections()
    await screen.findByTestId('secret-row-connection-spoke-us')
    expect(screen.getByRole('columnheader', { name: /Health/ })).toBeInTheDocument()
    expect(screen.queryByRole('columnheader', { name: /Compared with/ })).not.toBeInTheDocument()
  })

  it('shows Connected BESIDE "Verification incomplete" — correct, not a contradiction', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([legacyInlineClean]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-us')
    expect(within(row).getByTestId('status-mark')).toHaveTextContent(CONNECTION_SENTENCES.headlineVerificationIncomplete)
    expect(within(row).getByTestId('cell-health')).toHaveTextContent('Connected')
  })

  // B13 item 3 — this used to read "never invents a fourth". There now IS a
  // fourth, sent by the server, and the fleet renders it: `unknown`, for a
  // connection whose Secret does not exist. It is not a synonym for
  // not_checked, which promises a probe is on its way.
  it('renders each of the four health words the server can send', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      response([
        { ...sharkoManagedClean, cluster: 'c-connected', secret_name: 'c-connected', health: 'connected' },
        { ...sharkoManagedClean, cluster: 'c-unavailable', secret_name: 'c-unavailable', health: 'unavailable' },
        { ...sharkoManagedClean, cluster: 'c-notchecked', secret_name: 'c-notchecked', health: 'not_checked' },
        { ...selfManagedMissingSecret },
      ]),
    )
    renderConnections()
    await screen.findByTestId('secret-row-connection-c-connected')
    expect(within(screen.getByTestId('secret-row-connection-c-connected')).getByTestId('cell-health')).toHaveTextContent('Connected')
    expect(within(screen.getByTestId('secret-row-connection-c-unavailable')).getByTestId('cell-health')).toHaveTextContent('Unavailable')
    expect(within(screen.getByTestId('secret-row-connection-c-notchecked')).getByTestId('cell-health')).toHaveTextContent('Not checked')
    const unknownCell = within(screen.getByTestId('secret-row-connection-guest-nosecret')).getByTestId('cell-health')
    expect(unknownCell).toHaveTextContent('Unknown')
    // The specific failure this guards: falling through to the wrong word.
    expect(unknownCell.textContent).not.toContain('Not checked')
    expect(unknownCell.textContent?.trim()).not.toBe('')
  })

  // The mapping itself, at the unit level — this is where a missing case
  // shows up as a wrong word or a blank, and neither is acceptable.
  it('the health word mapping handles every value, an absent one, and a word it has never seen', () => {
    expect(HEALTH_COLUMN_WORDS.connected).toBe('Connected')
    expect(HEALTH_COLUMN_WORDS.unavailable).toBe('Unavailable')
    expect(HEALTH_COLUMN_WORDS.not_checked).toBe('Not checked')
    expect(HEALTH_COLUMN_WORDS.unknown).toBe('Unknown')
    expect(healthColumnWord('unknown')).toBe('Unknown')
    // No field at all: the server said nothing, and "Not checked" is honest.
    expect(healthColumnWord(undefined)).toBe('Not checked')
    expect(healthColumnWord('')).toBe('Not checked')
    // A newer server's word: "Unknown", never the specific claim that a
    // probe is coming, and never blank.
    expect(healthColumnWord('some_future_word')).toBe('Unknown')
    expect(healthColumnWord('some_future_word')).not.toBe('Not checked')
    expect(healthColumnWord('some_future_word')).not.toBe('')
  })

  it('an unknown-health row sorts with the no-answer rows, never with the working ones', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(
      response([
        { ...sharkoManagedClean, cluster: 'c-connected', secret_name: 'c-connected', health: 'connected' },
        { ...selfManagedMissingSecret },
        { ...sharkoManagedClean, cluster: 'c-unavailable', secret_name: 'c-unavailable', health: 'unavailable' },
      ]),
    )
    renderConnections()
    await screen.findByTestId('secret-row-connection-c-connected')
    await user.click(screen.getByTestId('sort-health'))
    const order = screen.getAllByTestId(/^secret-row-connection-/).map((r) => r.getAttribute('data-testid'))
    // Worst first: unavailable, then the no-answer row, then connected.
    expect(order[0]).toBe('secret-row-connection-c-unavailable')
    expect(order[1]).toBe('secret-row-connection-guest-nosecret')
    expect(order[2]).toBe('secret-row-connection-c-connected')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// B13 item 9 — one sentence for one button in one situation
//
// The cluster detail page offers the same "Sync addon labels" button and used
// to explain the nothing-to-do case differently. Both surfaces now read the
// SAME exported constant, and both pin it by exact text — this half here, the
// other half in ClusterDetail.resync.test.tsx. Two pins on one constant is
// what stops them drifting apart again.
// ─────────────────────────────────────────────────────────────────────────────

describe('B13 item 9 — the nothing-to-apply sentence is shared with the cluster page', () => {
  it('the fleet row\'s disabled reason is the shared sentence, character for character', () => {
    const inSync: UnifiedRow = {
      kind: 'connection',
      key: 'connection-prod-eu',
      cluster: 'prod-eu',
      state: 'in_sync',
      sourceLabel: 'git',
      managementMode: 'sharko_managed',
      managedScope: 'full_connection',
      syncState: 'synced',
      verificationScope: 'full',
      headline: CONNECTION_SENTENCES.headlineConnectionSynced,
      selfHeals: true,
    }
    expect(syncGateFor(inSync)).toEqual({
      disabled: true,
      reason: SYNC_ADDON_LABELS_NOTHING_TO_APPLY,
    })
    expect(SYNC_ADDON_LABELS_NOTHING_TO_APPLY).toBe(
      'Nothing to apply — this connection already matches the Git-defined connection.',
    )
    // The cluster page's old wording is banned on this surface too.
    expect(SYNC_ADDON_LABELS_NOTHING_TO_APPLY).not.toContain('this secret already matches git')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// One wording for one write — the other two pairs
//
// "Sync addon labels" writes the same thing from both pages, and both pages
// described that write in their own words twice over: once as the button's
// hint, once in the confirm box read right before the write. Both are shared
// constants now, and both are pinned by exact text on BOTH surfaces — this
// half here, the other half in ClusterDetail.resync.test.tsx.
// ─────────────────────────────────────────────────────────────────────────────

describe('the Sync addon labels wording is shared with the cluster page', () => {
  const driftedConnection: UnifiedRow = {
    kind: 'connection',
    key: 'connection-prod-eu',
    cluster: 'prod-eu',
    state: 'out_of_sync',
    sourceLabel: 'git',
    managementMode: 'sharko_managed',
    managedScope: 'full_connection',
    syncState: 'out_of_sync',
    verificationScope: 'full',
    headline: CONNECTION_SENTENCES.headlineOutOfSync,
    selfHeals: false,
  }

  it("the button's hint is the shared sentence, character for character", () => {
    expect(SYNC_ADDON_LABELS_HINT).toBe(
      'Puts the addon labels Git defines back on this connection. Nothing else on it changes.',
    )
    // Both wordings this replaced are banned.
    expect(SYNC_ADDON_LABELS_HINT).not.toContain('puts git\'s addon labels back on this secret')
    expect(SYNC_ADDON_LABELS_HINT).not.toContain("Applies git's addon labels to this cluster's secret")
  })

  it('the confirm box is the shared sentence, character for character', () => {
    expect(syncConfirmDescription(driftedConnection)).toBe(
      'This writes to cluster "prod-eu" now. No pull request. It puts back the addon labels Git defines, and nothing else. The self-heal setting doesn\'t change.',
    )
    expect(syncConfirmDescription(driftedConnection)).toBe(syncAddonLabelsConfirmText('prod-eu'))
    // Both wordings this replaced are banned.
    expect(syncConfirmDescription(driftedConnection)).not.toContain(
      "It copies git's addon labels onto the cluster's ArgoCD secret",
    )
    expect(syncConfirmDescription(driftedConnection)).not.toContain('one time; the self-heal setting is not changed')
  })

  it('an addon-values row keeps its own confirm sentence — a different action against a different source', () => {
    const valuesRow: UnifiedRow = {
      kind: 'values',
      key: 'values-prod-eu-grafana',
      cluster: 'prod-eu',
      addon: 'grafana',
      state: 'out_of_sync',
      sourceLabel: 'AWS Secrets Manager',
      selfHeals: false,
    }
    expect(syncConfirmDescription(valuesRow)).toBe(
      'This writes the secret on cluster "prod-eu" now. No pull request. It pushes the current value from AWS Secrets Manager onto the cluster. If the secret doesn\'t exist yet, this creates it.',
    )
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The browser writes no promise about what Sharko will do
//
// A self-managed connection's drifted addon labels converge on their own, and
// the fleet row's greyed-out Sync door has to say so. It used to say so in
// the BROWSER'S words — "Sharko re-applies the addon labels git declares on
// the next pass. Nothing to do here." — while the server said the identical
// fact differently in planAutomaticLabelSync
// (internal/api/connection_reconciliation.go).
//
// The fleet response carries no plan field for a connection row, so there is
// nothing on the wire to render here. The sentence is a shared constant
// instead, identical to the server's literal and pinned by an exact-string
// test on BOTH sides: this one, and the server's own
// TestConnectionReconciliation_NewSentencesExact.
// ─────────────────────────────────────────────────────────────────────────────

describe('the self-managed converge sentence is the SERVER\'S sentence', () => {
  const selfManagedDriftedRow: UnifiedRow = {
    kind: 'connection',
    key: 'connection-guest-2',
    cluster: 'guest-2',
    state: 'out_of_sync',
    sourceLabel: 'git',
    managementMode: 'self_managed',
    managedScope: 'addon_labels',
    syncState: 'out_of_sync',
    verificationScope: 'full',
    headline: CONNECTION_SENTENCES.headlineAddonLabelsOutOfSync,
    selfHeals: true,
  }

  it('is the server\'s literal, character for character', () => {
    // TEMPORARY — hand-copied server text, awaiting the generated import.
    //
    // The value below is a hand-copy of planAutomaticLabelSync in
    // internal/api/connection_reconciliation.go. Read it out of that file
    // with a tool if you ever change it.
    //
    // What this test used to do: it asserted SHARKO_REAPPLIES_ADDON_LABELS
    // equalled the literal written directly above it in
    // ClusterActionHints.ts — a constant compared with itself. It could not
    // fail, so it never noticed that the browser's sentence and the server's
    // sentence had become two different sentences. It now carries the
    // server's real text, so the two sides can actually disagree.
    expect(SHARKO_REAPPLIES_ADDON_LABELS).toBe(
      CONNECTION_SENTENCES.planAutomaticLabelSync,
    )
  })

  it('is what the greyed-out Sync door says, with no browser clause appended', () => {
    expect(syncGateFor(selfManagedDriftedRow)).toEqual({
      disabled: true,
      reason: SHARKO_REAPPLIES_ADDON_LABELS,
    })
    // The two browser-authored versions this replaced, banned by fragment.
    expect(SHARKO_REAPPLIES_ADDON_LABELS).not.toContain('Nothing to do here')
    expect(SHARKO_REAPPLIES_ADDON_LABELS).not.toContain('on the next pass')
    expect(SHARKO_REAPPLIES_ADDON_LABELS).not.toContain('nobody has to ask for it')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// B13 item 6 — the page says why no row has a check
// ─────────────────────────────────────────────────────────────────────────────

describe('B13 item 6 — why every row reads "Not checked yet"', () => {
  // The server's own sentence for an out-of-cluster server, verbatim.
  const NOT_SCHEDULED_REASON =
    CONNECTION_SENTENCES.checkLoopNotScheduled

  it('renders the SERVER\'S sentence, word for word, when checks are not running', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...response([sharkoManagedClean]),
      background_connection_checks: { running: false, reason: NOT_SCHEDULED_REASON, interval_seconds: 0 },
    })
    renderConnections()
    const notice = await screen.findByTestId('background-checks-notice')
    // Exact text — the browser renders the server's sentence and composes
    // nothing of its own. A summary here would be a guess at which of the
    // several reasons applies.
    expect(notice.textContent).toBe(NOT_SCHEDULED_REASON)
  })

  it('renders a DIFFERENT server reason unchanged too — the browser is not selecting from a table of its own', async () => {
    const NO_ARGOCD_REASON =
      CONNECTION_SENTENCES.checkLoopNoArgoCD
    mockGetManagedSecrets.mockResolvedValue({
      ...response([sharkoManagedClean]),
      background_connection_checks: { running: false, reason: NO_ARGOCD_REASON, interval_seconds: 900 },
    })
    renderConnections()
    const notice = await screen.findByTestId('background-checks-notice')
    expect(notice.textContent).toBe(NO_ARGOCD_REASON)
  })

  it('shows nothing when the checks ARE running', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...response([sharkoManagedClean]),
      background_connection_checks: { running: true, interval_seconds: 900, last_attempt: '2026-08-19T00:00:00Z' },
    })
    renderConnections()
    await screen.findByTestId('secret-row-connection-prod-eu')
    expect(screen.queryByTestId('background-checks-notice')).toBeNull()
  })

  it('shows nothing on an older server that does not send the field — silence, never a guessed reason', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([sharkoManagedClean]))
    renderConnections()
    await screen.findByTestId('secret-row-connection-prod-eu')
    expect(screen.queryByTestId('background-checks-notice')).toBeNull()
  })

  it('the per-row Check now action is still there — the explanation did not replace the door', async () => {
    mockGetManagedSecrets.mockResolvedValue({
      ...response([sharkoManagedClean]),
      background_connection_checks: { running: false, reason: NOT_SCHEDULED_REASON, interval_seconds: 0 },
    })
    renderConnections()
    await screen.findByTestId('background-checks-notice')
    const menu = await rowActionMenu('prod-eu')
    expect(within(menu).getByText('Check now')).toBeTruthy()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The actions each mode does and does not offer
// ─────────────────────────────────────────────────────────────────────────────

async function rowActionMenu(cluster: string) {
  const user = userEvent.setup()
  const row = await screen.findByTestId(`secret-row-connection-${cluster}`)
  await user.click(within(row).getByRole('button', { name: /Actions for/ }))
  return screen.findByRole('menu')
}

describe('a mode is never offered a door it does not have', () => {
  it('RULING (a): self-managed label drift offers NO manual label sync — the reconciler does it every pass', async () => {
    const user = userEvent.setup()
    mockGetManagedSecrets.mockResolvedValue(response([selfManagedDrifted]))
    renderConnections()
    const menu = await rowActionMenu('guest-2')
    const sync = within(menu).getByRole('menuitem', { name: /Sync addon labels/ })
    expect(sync).toHaveAttribute('aria-disabled', 'true')

    // The greyed-out door has to SAY why, and the sentence it says is the
    // SERVER'S. This assertion used to stop at aria-disabled and never
    // looked at the text at all — which is how the browser's own
    // paraphrase ("… on the next pass. Nothing to do here.") survived
    // beside the server's real sentence for the identical fact.
    await user.click(within(menu).getByRole('button', { name: 'Why is Sync addon labels unavailable?' }))
    expect(await screen.findByText(SHARKO_REAPPLIES_ADDON_LABELS)).toBeInTheDocument()
    // The browser-written version is banned on this surface.
    expect(screen.queryByText(/Nothing to do here/)).not.toBeInTheDocument()
  })

  it('foreign-owned reads "Managed elsewhere" in the legacy vocabulary and offers no Sharko write', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([foreignOwned]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-other-tool')
    // The row's own word is the server's headline; the underlying state is
    // the boundary word the chips and sort already know.
    expect(within(row).getByTestId('status-mark')).toHaveTextContent(CONNECTION_SENTENCES.headlineBlocked)
    expect(within(row).getByTestId('status-mark')).toHaveAttribute('data-status', 'foreign')
    const menu = await rowActionMenu('other-tool')
    const sync = within(menu).getByRole('menuitem', { name: /Sync addon labels/ })
    expect(sync).toHaveAttribute('aria-disabled', 'true')
  })

  it('a synced connection offers nothing to apply', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([sharkoManagedClean]))
    renderConnections()
    const menu = await rowActionMenu('prod-eu')
    expect(within(menu).getByRole('menuitem', { name: /Sync addon labels/ })).toHaveAttribute('aria-disabled', 'true')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// The page's own words
// ─────────────────────────────────────────────────────────────────────────────

describe('the subtitle names the locked model', () => {
  it('is exactly the ruled sentence, and the old one is gone', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([sharkoManagedClean]))
    renderConnections()
    await screen.findByTestId('secret-row-connection-prod-eu')
    expect(screen.getByText('Git-defined cluster connections Sharko maintains for Argo CD.')).toBeInTheDocument()
    expect(screen.queryByText('Secrets Sharko uses to register clusters with Argo CD.')).not.toBeInTheDocument()
  })
})
