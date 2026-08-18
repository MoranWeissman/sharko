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
import { ManagedSecrets, rowStatus, connectionRowLabel, type UnifiedRow } from '@/views/ManagedSecrets'
import { AuthContext } from '@/hooks/useAuth'
import type { ConnectionSecretRow, ManagedSecretsResponse } from '@/services/models'

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
  headline: 'Connection synced',
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
  headline: 'Configuration matches; credential content not compared',
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
  headline: 'Verification incomplete',
  qualifier:
    "The credential exists only in the live Secret. Sharko can check the connection's health and some non-sensitive fields, but it cannot verify or rebuild the complete connection from Git.",
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
  headline: 'Addon labels synced',
  qualifier: 'Connection data managed outside Sharko',
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
  headline: 'Addon labels out of sync',
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
  headline: 'Blocked',
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
    ['sharko-managed, clean', sharkoManagedClean, 'Connection synced'],
    ['sharko-managed, EKS', sharkoManagedEKS, 'Configuration matches; credential content not compared'],
    ['legacy inline, clean', legacyInlineClean, 'Verification incomplete'],
    ['self-managed, clean', selfManagedClean, 'Addon labels synced'],
    ['self-managed, drifted', selfManagedDrifted, 'Addon labels out of sync'],
    ['foreign-owned', foreignOwned, 'Blocked'],
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
    expect(mark).not.toHaveTextContent('Connection synced')
    expect(mark).toHaveAttribute('data-status', 'unknown')
    expect(syncedChipCount()).toBe(0)
  })

  it('THE ORIGINAL DEFECT: a clean legacy-inline row is not rendered as synced and is not counted under Synced', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([legacyInlineClean]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-us')
    const mark = within(row).getByTestId('status-mark')
    expect(mark).toHaveTextContent('Verification incomplete')
    expect(mark).not.toHaveTextContent(/Synced/)
    // Neutral styling — never the green synchronized dot.
    expect(mark).toHaveAttribute('data-status', 'unknown')
    expect(within(mark).getByTestId('status-dot')).toHaveAttribute('data-hollow', 'true')
    expect(syncedChipCount()).toBe(0)
  })

  it('the verification indicator on that row reads "Credential not compared", off the canonical scope', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([legacyInlineClean]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-us')
    const badge = within(row).getByTestId('credential-check-badge')
    expect(badge).toHaveTextContent('Credential not compared')
    expect(badge).toHaveAttribute('data-credential-check', 'not_compared')
    // Neutral, not amber — an honest scope limit is not damage.
    expect(badge.className).not.toMatch(/amber/)
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
        { ...legacyInlineClean, cluster: 'spoke-us-old', secret_name: 'spoke-us-old', state: 'in_sync', sync_state: 'synced', headline: 'Connection synced' },
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
    headline: 'Connection synced',
  }

  it('renders it as "Not checked yet", never the synced word it was handed', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([impossible]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-spoke-us')
    const mark = within(row).getByTestId('status-mark')
    expect(mark).toHaveTextContent('Not checked yet')
    expect(mark).not.toHaveTextContent('Connection synced')
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
    expect(connectionRowLabel(preB5)).toBe('Not checked yet')
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
    expect(within(row).getByTestId('status-mark')).toHaveTextContent('Verification incomplete')
    expect(within(row).getByTestId('cell-health')).toHaveTextContent('Connected')
  })

  it('renders each health word, and never invents a fourth', async () => {
    mockGetManagedSecrets.mockResolvedValue(
      response([
        { ...sharkoManagedClean, cluster: 'c-connected', secret_name: 'c-connected', health: 'connected' },
        { ...sharkoManagedClean, cluster: 'c-unavailable', secret_name: 'c-unavailable', health: 'unavailable' },
        { ...sharkoManagedClean, cluster: 'c-notchecked', secret_name: 'c-notchecked', health: 'not_checked' },
      ]),
    )
    renderConnections()
    await screen.findByTestId('secret-row-connection-c-connected')
    expect(within(screen.getByTestId('secret-row-connection-c-connected')).getByTestId('cell-health')).toHaveTextContent('Connected')
    expect(within(screen.getByTestId('secret-row-connection-c-unavailable')).getByTestId('cell-health')).toHaveTextContent('Unavailable')
    expect(within(screen.getByTestId('secret-row-connection-c-notchecked')).getByTestId('cell-health')).toHaveTextContent('Not checked')
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
    mockGetManagedSecrets.mockResolvedValue(response([selfManagedDrifted]))
    renderConnections()
    const menu = await rowActionMenu('guest-2')
    const sync = within(menu).getByRole('menuitem', { name: /Sync addon labels/ })
    expect(sync).toHaveAttribute('aria-disabled', 'true')
  })

  it('foreign-owned reads "Managed elsewhere" in the legacy vocabulary and offers no Sharko write', async () => {
    mockGetManagedSecrets.mockResolvedValue(response([foreignOwned]))
    renderConnections()
    const row = await screen.findByTestId('secret-row-connection-other-tool')
    // The row's own word is the server's headline; the underlying state is
    // the boundary word the chips and sort already know.
    expect(within(row).getByTestId('status-mark')).toHaveTextContent('Blocked')
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
