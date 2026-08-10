// V2-cleanup-57.3: System page phase 1 tests.
//
// Covers: the four arrows rendering each status state (healthy / degraded /
// unknown), the ArgoCD tested-range badge (in-range / out-of-range / unknown
// version), and that every element links to the existing page where you'd
// act (read-only contract).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import SystemView, {
  GIT_CONN_ALERT_TITLE,
  aggregateStatuses,
  clusterStatusParts,
  deriveArgoClusterStatus,
  deriveArgoRepoArrow,
  deriveSharkoClusterLabel,
  deriveSharkoClusterStatus,
  deriveSharkoRepoArrow,
  parseMajorMinor,
  summarizeClusterStatuses,
  testedRangeLabel,
  versionOutsideTestedRange,
} from '@/views/SystemView'
import type { Cluster } from '@/services/models'
import type { RepoStatusReason } from '@/services/api'

const mockGetSystemCapabilities = vi.fn()
// ManagedSecretsSummaryLine (S1 — rendered at the bottom of SystemView,
// the full tables now live on their own page at /secrets) fetches on its
// own mount via getManagedSecrets — mock it here too so every existing
// SystemView test (which never cared about managed secrets) still gets a
// resolved, empty-by-default response instead of an unmocked vi.fn()
// returning undefined and throwing inside the effect.
// clearAllMocks() (see beforeEach below) clears call history but NOT a
// mock's resolvedValue implementation — so this default, set once here,
// keeps every test that never touches managed-secrets working even if it
// doesn't call mockAll() (e.g. the tests below that mock each api call by
// hand). mockAll() overwrites it for tests that care.
const mockGetManagedSecrets = vi.fn().mockResolvedValue({
  cluster_connection_secrets: [],
  addon_values_secrets: [],
  engines: {
    cluster_connection: { wired: false },
    addon_values: { wired: false },
  },
})
const mockTriggerSecretsReconcile = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getRepoStatus: vi.fn(),
    getClusters: vi.fn(),
    getNotifications: vi.fn(),
    getObservability: vi.fn(),
    // WQ-3 — home-cluster identity card reads (moved here from Dashboard).
    getHomeCluster: vi.fn(),
    health: vi.fn(),
    getFleetStatus: vi.fn(),
  },
  getSystemCapabilities: (...args: unknown[]) => mockGetSystemCapabilities(...args),
  getManagedSecrets: (...args: unknown[]) => mockGetManagedSecrets(...args),
  triggerSecretsReconcile: (...args: unknown[]) => mockTriggerSecretsReconcile(...args),
}))

import { api } from '@/services/api'

const mockedApi = vi.mocked(api, { partial: true })

function obsWithVersion(version?: string) {
  return {
    control_plane: {
      argocd_version: version ?? '',
      helm_version: 'v3.14.0',
      kubectl_version: 'v1.29.0',
      total_apps: 0,
      total_clusters: 0,
      configured_clusters: 0,
      configured_clusters_available: true,
      connected_clusters: 0,
      total_appsets: 0,
      health_summary: {},
    },
    recent_syncs: [],
    addon_health: [],
    addon_groups: [],
    resource_alerts: [],
  }
}

interface MockAllOpts {
  repo?: { initialized: boolean; bootstrap_synced: boolean; reason?: RepoStatusReason }
  clusters?: Cluster[]
  notifications?: { id: string; type: string; title: string; description: string; timestamp: string; read: boolean }[]
  argocdVersion?: string
  capabilities?: { aws: { detected: boolean; method: string; identity_arn?: string }; hub_platform: string } | null
  // WQ-3 — home-cluster identity card reads. Defaulted to "unavailable"
  // (the honest degrade state) so existing tests that don't care about the
  // card keep passing unchanged.
  homeCluster?: { available: boolean; message?: string; kubernetes_version?: string; node_count?: number; nodes_ready?: number }
  sharkoVersion?: string
  uptime?: string
}

function mockAll({
  repo = { initialized: true, bootstrap_synced: true },
  clusters = [],
  notifications = [],
  argocdVersion = 'v3.2.2',
  capabilities = { aws: { detected: false, method: 'none' }, hub_platform: 'unknown' },
  homeCluster = { available: false, message: 'only available when running in-cluster' },
  sharkoVersion,
  uptime,
}: MockAllOpts = {}) {
  mockedApi.getRepoStatus.mockResolvedValue(repo)
  mockedApi.getClusters.mockResolvedValue({ clusters })
  mockedApi.getNotifications.mockResolvedValue({ notifications, unread_count: 0 })
  mockedApi.getObservability.mockResolvedValue(obsWithVersion(argocdVersion))
  mockGetSystemCapabilities.mockResolvedValue(capabilities)
  mockedApi.getHomeCluster.mockResolvedValue(homeCluster)
  mockedApi.health.mockResolvedValue(
    (sharkoVersion ? { status: 'healthy', version: sharkoVersion } : null) as never,
  )
  mockedApi.getFleetStatus.mockResolvedValue(
    (uptime ? { server_version: sharkoVersion ?? '', uptime } : null) as never,
  )
  mockGetManagedSecrets.mockResolvedValue({
    cluster_connection_secrets: [],
    addon_values_secrets: [],
    engines: {
      cluster_connection: { wired: false },
      addon_values: { wired: false },
    },
  })
}

function renderPage() {
  return render(
    <MemoryRouter>
      <SystemView />
    </MemoryRouter>,
  )
}

// The cluster status line's bucket phrases ("2 healthy", "1 with issues")
// are each colored in their own nested <span>, so the full sentence is no
// longer one text node — screen.getByText's default matcher only looks at
// an element's own direct text, not its descendants'. Match on the link's
// full textContent instead, which does read through the nested spans.
function getClusterStatusLines(fullText: string) {
  return screen.getAllByText(
    (_, element) => element?.tagName === 'A' && element.textContent === fullText,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

// ─────────────────────────────────────────────────────────────────────────────
// Pure derivations — each arrow reaches each status state
// ─────────────────────────────────────────────────────────────────────────────

describe('deriveSharkoRepoArrow', () => {
  it('is unknown with no repo status', () => {
    expect(deriveSharkoRepoArrow(null).status).toBe('unknown')
  })
  it('is degraded when no connection is configured', () => {
    expect(deriveSharkoRepoArrow({ initialized: false, reason: 'no_connection' }).status).toBe('degraded')
  })
  it('is degraded on a connection error', () => {
    expect(deriveSharkoRepoArrow({ initialized: false, reason: 'connection_error' }).status).toBe('degraded')
    expect(deriveSharkoRepoArrow({ initialized: false, reason: 'error' }).status).toBe('degraded')
  })
  it('is healthy when the repo is initialized', () => {
    expect(deriveSharkoRepoArrow({ initialized: true, bootstrap_synced: true }).status).toBe('healthy')
  })
  it('is healthy (reachable) when not bootstrapped yet', () => {
    const v = deriveSharkoRepoArrow({ initialized: false, reason: 'not_bootstrapped' })
    expect(v.status).toBe('healthy')
    expect(v.detail).toMatch(/hasn't been initialized/)
  })
})

describe('deriveArgoRepoArrow', () => {
  it('is unknown with no repo status', () => {
    expect(deriveArgoRepoArrow(null).status).toBe('unknown')
  })
  it('is unknown when the repo is not initialized', () => {
    expect(deriveArgoRepoArrow({ initialized: false, reason: 'not_bootstrapped' }).status).toBe('unknown')
  })
  it('is healthy when the bootstrap app is synced', () => {
    expect(deriveArgoRepoArrow({ initialized: true, bootstrap_synced: true }).status).toBe('healthy')
  })
  it('is degraded when ArgoCD cannot reach the repo', () => {
    const v = deriveArgoRepoArrow({ initialized: true, bootstrap_synced: false, reason: 'bootstrap_unreachable' })
    expect(v.status).toBe('degraded')
    expect(v.detail).toMatch(/can't reach the repo/)
  })
  it('is degraded when the bootstrap app is degraded', () => {
    expect(
      deriveArgoRepoArrow({ initialized: true, bootstrap_synced: false, reason: 'bootstrap_degraded' }).status,
    ).toBe('degraded')
  })
  // Error review package 1 — these two reasons mean Sharko never got a
  // usable answer from ArgoCD at all, distinct from a genuinely degraded
  // engine app. The System page must not assert the engine app is broken
  // when it never got that far. Review findings r1, L13: both are
  // "couldn't check", so both map to the SAME status ('unknown') — a
  // rejected token is not evidence of degradation any more than an
  // unreachable ArgoCD is.
  it('is unknown (not degraded) when ArgoCD rejected Sharko\'s token — a couldn\'t-check state', () => {
    const v = deriveArgoRepoArrow({ initialized: true, bootstrap_synced: false, reason: 'argocd_auth_failed' })
    expect(v.status).toBe('unknown')
    expect(v.detail).toMatch(/rejected Sharko's token/)
  })
  it('is unknown when Sharko could not reach ArgoCD at all', () => {
    const v = deriveArgoRepoArrow({ initialized: true, bootstrap_synced: false, reason: 'argocd_unreachable' })
    expect(v.status).toBe('unknown')
    expect(v.detail).toMatch(/couldn't reach ArgoCD/)
  })
  // H1 (review findings r1) — a 403 means the token is valid but lacks
  // permission. Sharko never got to check the engine app, so this is the
  // same couldn't-check bucket, not a claim the app is degraded.
  it('is unknown when ArgoCD refused Sharko\'s token permission (403)', () => {
    const v = deriveArgoRepoArrow({ initialized: true, bootstrap_synced: false, reason: 'argocd_forbidden' })
    expect(v.status).toBe('unknown')
    expect(v.detail).toMatch(/refused Sharko's token permission/)
  })
})

describe('deriveSharkoClusterStatus', () => {
  const base: Cluster = { name: 'c1', labels: {} }
  it('is degraded when the last test failed', () => {
    expect(deriveSharkoClusterStatus({ ...base, test_failing: true, sharko_status: 'Connected' })).toBe('degraded')
  })
  it('is degraded when unreachable', () => {
    expect(deriveSharkoClusterStatus({ ...base, sharko_status: 'Unreachable' })).toBe('degraded')
  })
  it('is healthy for Connected / Verified / Operational', () => {
    expect(deriveSharkoClusterStatus({ ...base, sharko_status: 'Connected' })).toBe('healthy')
    expect(deriveSharkoClusterStatus({ ...base, sharko_status: 'Verified' })).toBe('healthy')
    expect(deriveSharkoClusterStatus({ ...base, sharko_status: 'Operational' })).toBe('healthy')
  })
  it('is unknown otherwise', () => {
    expect(deriveSharkoClusterStatus(base)).toBe('unknown')
    expect(deriveSharkoClusterStatus({ ...base, sharko_status: 'Unknown' })).toBe('unknown')
  })

  // V2-cleanup-85.4: the auto-derived verdict — no manual Test click
  // required — must count a reachable/healthy cluster even when
  // sharko_status was never set (the exact bug this story fixes).
  it('is healthy when derived_health_status is "healthy", with no manual test ever run', () => {
    expect(deriveSharkoClusterStatus({ ...base, derived_health_status: 'healthy' })).toBe('healthy')
  })
  it('is healthy when derived_health_status is "reachable", with no manual test ever run', () => {
    expect(deriveSharkoClusterStatus({ ...base, derived_health_status: 'reachable' })).toBe('healthy')
  })
  it('stays unknown when derived_health_status is "unknown"', () => {
    expect(deriveSharkoClusterStatus({ ...base, derived_health_status: 'unknown' })).toBe('unknown')
  })
  it('a failed manual test still wins over a stale healthy derivation', () => {
    expect(
      deriveSharkoClusterStatus({ ...base, derived_health_status: 'healthy', test_failing: true }),
    ).toBe('degraded')
  })
})

describe('deriveSharkoClusterLabel', () => {
  const base: Cluster = { name: 'c1', labels: {} }
  it('labels "Healthy" when an addon is actually up', () => {
    expect(deriveSharkoClusterLabel({ ...base, derived_health_status: 'healthy' })).toBe('Healthy')
  })
  it('labels "Reachable" — honest distinction — when Sharko can reach it but no addon is up yet', () => {
    expect(deriveSharkoClusterLabel({ ...base, derived_health_status: 'reachable' })).toBe('Reachable')
  })
  it('falls back to the default pill label for the legacy manual-status-only path', () => {
    expect(deriveSharkoClusterLabel({ ...base, sharko_status: 'Connected' })).toBeUndefined()
  })
  it('is undefined for degraded/unknown clusters (no override needed)', () => {
    expect(deriveSharkoClusterLabel({ ...base, test_failing: true })).toBeUndefined()
    expect(deriveSharkoClusterLabel(base)).toBeUndefined()
  })
})

describe('deriveArgoClusterStatus', () => {
  const base: Cluster = { name: 'c1', labels: {} }
  it('is healthy when ArgoCD reports Successful', () => {
    expect(deriveArgoClusterStatus({ ...base, connection_status: 'Successful' })).toBe('healthy')
  })
  it('is healthy when the connectivity check verified it', () => {
    expect(deriveArgoClusterStatus({ ...base, connectivity_status: 'verified_check' })).toBe('healthy')
    expect(deriveArgoClusterStatus({ ...base, connectivity_status: 'verified_argocd' })).toBe('healthy')
  })
  it('is degraded when the check failed or the connection failed', () => {
    expect(deriveArgoClusterStatus({ ...base, connectivity_status: 'check_failed' })).toBe('degraded')
    expect(deriveArgoClusterStatus({ ...base, connection_status: 'Failed' })).toBe('degraded')
  })
  it('is unknown otherwise (Unknown status, pending check)', () => {
    expect(deriveArgoClusterStatus({ ...base, connection_status: 'Unknown' })).toBe('unknown')
    expect(deriveArgoClusterStatus({ ...base, connectivity_status: 'check_pending' })).toBe('unknown')
  })
})

describe('aggregateStatuses', () => {
  it('handles the empty fleet', () => {
    expect(aggregateStatuses([])).toEqual({ status: 'unknown', label: 'No clusters yet' })
  })
  it('is healthy when everything is healthy', () => {
    expect(aggregateStatuses(['healthy', 'healthy', 'healthy'])).toEqual({
      status: 'healthy',
      label: '3 of 3 healthy',
    })
  })
  it('is degraded when anything is degraded', () => {
    expect(aggregateStatuses(['healthy', 'degraded'])).toEqual({ status: 'degraded', label: '1 of 2 healthy' })
  })
  it('is unknown when nothing is broken but not everything is verified', () => {
    expect(aggregateStatuses(['healthy', 'unknown'])).toEqual({ status: 'unknown', label: '1 of 2 healthy' })
  })
})

// S2 (walk day 4) — the honest one-line summary that replaced the
// expandable per-cluster list under each cluster arrow.
describe('summarizeClusterStatuses', () => {
  it('counts all three buckets but only mentions the non-zero ones', () => {
    expect(
      summarizeClusterStatuses(['healthy', 'healthy', 'healthy', 'healthy', 'healthy', 'healthy', 'healthy', 'degraded']),
    ).toBe('8 managed clusters — 7 healthy, 1 with issues')
  })
  it('says "1 managed cluster" (singular) for a single cluster', () => {
    expect(summarizeClusterStatuses(['healthy'])).toBe('1 managed cluster — 1 healthy')
  })
  it('mentions unknown when that is the only non-zero bucket', () => {
    expect(summarizeClusterStatuses(['unknown'])).toBe('1 managed cluster — 1 unknown')
  })
  it('mentions every non-zero bucket when all three are present', () => {
    expect(summarizeClusterStatuses(['healthy', 'degraded', 'unknown'])).toBe(
      '3 managed clusters — 1 healthy, 1 with issues, 1 unknown',
    )
  })
  it('handles zero clusters', () => {
    expect(summarizeClusterStatuses([])).toBe('0 managed clusters')
  })
})

// The structured version behind the on-screen colored line: same buckets,
// tagged with a tone so the UI can color healthy/degraded/unknown
// differently while the sentence stays plain text.
describe('clusterStatusParts', () => {
  it('tags each non-zero bucket with its tone, in order', () => {
    expect(clusterStatusParts(['healthy', 'degraded', 'unknown'])).toEqual({
      total: 3,
      clusterWord: 'managed clusters',
      parts: [
        { text: '1 healthy', tone: 'healthy' },
        { text: '1 with issues', tone: 'degraded' },
        { text: '1 unknown', tone: 'unknown' },
      ],
    })
  })
  it('omits zero buckets', () => {
    expect(clusterStatusParts(['healthy', 'healthy'])).toEqual({
      total: 2,
      clusterWord: 'managed clusters',
      parts: [{ text: '2 healthy', tone: 'healthy' }],
    })
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Version badge logic — dumb and safe, minor-version comparison only
// ─────────────────────────────────────────────────────────────────────────────

describe('parseMajorMinor', () => {
  it('parses v-prefixed and bare versions (build metadata ignored)', () => {
    expect(parseMajorMinor('v3.2.2')).toEqual({ major: 3, minor: 2 })
    expect(parseMajorMinor('3.2')).toEqual({ major: 3, minor: 2 })
    expect(parseMajorMinor('v3.2.2+abc123')).toEqual({ major: 3, minor: 2 })
  })
  it('returns null for garbage / missing input', () => {
    expect(parseMajorMinor(undefined)).toBeNull()
    expect(parseMajorMinor('')).toBeNull()
    expect(parseMajorMinor('stable')).toBeNull()
  })
})

describe('versionOutsideTestedRange', () => {
  const range = { tested_min: 'v3.1', tested_max: 'v3.2' }
  it('is false inside the range (inclusive)', () => {
    expect(versionOutsideTestedRange('v3.1.9', range)).toBe(false)
    expect(versionOutsideTestedRange('v3.2.0', range)).toBe(false)
  })
  it('is true below min and above max', () => {
    expect(versionOutsideTestedRange('v3.0.1', range)).toBe(true)
    expect(versionOutsideTestedRange('v3.3.0', range)).toBe(true)
    expect(versionOutsideTestedRange('v4.0.0', range)).toBe(true)
    expect(versionOutsideTestedRange('v2.9.0', range)).toBe(true)
  })
  it('never fires for unknown or unparseable versions', () => {
    expect(versionOutsideTestedRange(undefined, range)).toBe(false)
    expect(versionOutsideTestedRange('weird', range)).toBe(false)
    expect(versionOutsideTestedRange('v3.3.0', { tested_min: 'junk', tested_max: 'junk' })).toBe(false)
  })
})

describe('testedRangeLabel', () => {
  it('collapses an equal min/max to one version', () => {
    expect(testedRangeLabel({ tested_min: 'v3.2', tested_max: 'v3.2' })).toBe('v3.2')
  })
  it('renders a range when min and max differ', () => {
    expect(testedRangeLabel({ tested_min: 'v3.1', tested_max: 'v3.2' })).toBe('v3.1–v3.2')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// Component rendering
// ─────────────────────────────────────────────────────────────────────────────

describe('SystemView', () => {
  it('renders all four arrows healthy when everything is fine', async () => {
    mockAll({
      repo: { initialized: true, bootstrap_synced: true },
      clusters: [
        { name: 'prod-1', labels: {}, connection_status: 'Successful', sharko_status: 'Connected' },
        { name: 'prod-2', labels: {}, connection_status: 'Successful', sharko_status: 'Verified' },
      ],
      argocdVersion: 'v3.2.2',
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())

    // Repo arrows healthy
    expect(screen.getByText('Sharko can read and write the repo.')).toBeInTheDocument()
    expect(
      screen.getByText('ArgoCD is syncing the repo — the engine app is healthy.'),
    ).toBeInTheDocument()
    // Cluster arrow pills say a plain "Healthy" — the count itself is said
    // once, by the summary line below, not duplicated in the pill too
    // (maintainer's live finding: the card used to say the same count twice).
    expect(screen.queryByText('2 of 2 healthy')).not.toBeInTheDocument()
    const clusterLines = getClusterStatusLines('2 managed clusters — 2 healthy')
    expect(clusterLines).toHaveLength(2)
    clusterLines.forEach((line) => {
      expect(within(line).getByText('2 healthy').className).toContain('text-green-700')
    })
    // Detected version shown once, no "outside the tested range" warning
    // (v3.2 is in range)
    expect(screen.getByText('ArgoCD v3.2.2 detected')).toBeInTheDocument()
    expect(screen.queryByText(/outside the tested range/)).not.toBeInTheDocument()
  })

  it('shows where it is broken — degraded arrows + bell alert detail', async () => {
    mockAll({
      repo: { initialized: false, bootstrap_synced: false, reason: 'connection_error' },
      clusters: [
        { name: 'prod-1', labels: {}, connection_status: 'Failed', sharko_status: 'Unreachable', test_failing: true },
      ],
      notifications: [
        {
          id: 'n1',
          type: 'connection',
          title: GIT_CONN_ALERT_TITLE,
          description: 'Sharko uses this Git connection for every commit and pull request, and right now it can\'t reach it.',
          timestamp: new Date().toISOString(),
          read: false,
        },
      ],
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())

    expect(
      screen.getByText("Sharko can't reach the Git repo right now (network, TLS, or auth problem)."),
    ).toBeInTheDocument()
    // The matching bell alert's description is surfaced on the arrow
    expect(screen.getByText(/right now it can't reach it/)).toBeInTheDocument()
    // ArgoCD→repo can't be assessed on an uninitialized repo
    expect(
      screen.getByText("Can't assess until the repo is set up — ArgoCD has nothing to sync yet."),
    ).toBeInTheDocument()
    // The pill no longer says "0 of 1 healthy" — the summary line below
    // says the count once, with the "with issues" bucket colored red.
    expect(screen.queryByText('0 of 1 healthy')).not.toBeInTheDocument()
    const clusterLines = getClusterStatusLines('1 managed cluster — 1 with issues')
    expect(clusterLines).toHaveLength(2)
    clusterLines.forEach((line) => {
      expect(within(line).getByText('1 with issues').className).toContain('text-red-700')
    })
  })

  it('renders unknown states calmly (no clusters, unreported statuses)', async () => {
    mockAll({
      repo: { initialized: true, bootstrap_synced: false, reason: 'bootstrap_unreachable' },
      clusters: [],
      argocdVersion: 'v3.2.2',
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    expect(screen.getAllByText('No clusters yet')).toHaveLength(2)
    expect(screen.getByText(/ArgoCD can't reach the repo/)).toBeInTheDocument()
  })

  it('says the ArgoCD version once, with an amber warning, when it is outside the tested range', async () => {
    mockAll({ argocdVersion: 'v9.9.1' })
    renderPage()

    // The full detected version, said exactly once — no separate near-duplicate line.
    await waitFor(() => expect(screen.getByText('ArgoCD v9.9.1 detected')).toBeInTheDocument())
    expect(screen.getAllByText('ArgoCD v9.9.1 detected')).toHaveLength(1)

    const versionLine = screen.getByTestId('argocd-version-line')
    expect(versionLine.className).toContain('text-amber-700')
    expect(screen.getByText(/Sharko is tested with .* — outside the tested range/)).toBeInTheDocument()
  })

  it('shows no warning when the ArgoCD version is unknown', async () => {
    mockedApi.getRepoStatus.mockResolvedValue({ initialized: true, bootstrap_synced: true })
    mockedApi.getClusters.mockResolvedValue({ clusters: [] })
    mockedApi.getNotifications.mockResolvedValue({ notifications: [], unread_count: 0 })
    mockedApi.getObservability.mockRejectedValue(new Error('boom'))
    mockGetSystemCapabilities.mockResolvedValue({ aws: { detected: false, method: 'none' }, hub_platform: 'unknown' })
    mockedApi.getHomeCluster.mockResolvedValue({ available: false, message: 'only available when running in-cluster' })
    mockedApi.health.mockResolvedValue(null as never)
    mockedApi.getFleetStatus.mockResolvedValue(null as never)
    renderPage()

    await waitFor(() => expect(screen.getByText('ArgoCD version unknown')).toBeInTheDocument())
    expect(screen.queryByText(/outside the tested range/)).not.toBeInTheDocument()
  })

  it('counts a cluster as healthy via derived_health_status alone — no Test click required (V2-cleanup-85.4)', async () => {
    mockAll({
      clusters: [
        // Never manually tested (no sharko_status at all) — this is
        // exactly the cluster that used to read "unknown" and drag the
        // System page tally down to "0 of 2 healthy" incorrectly.
        { name: 'prod-1', labels: {}, connection_status: 'Successful', derived_health_status: 'healthy' },
        { name: 'prod-2', labels: {}, connection_status: 'Successful', derived_health_status: 'reachable' },
      ],
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    expect(screen.queryByText('2 of 2 healthy')).not.toBeInTheDocument()

    // S2 (walk day 4): the per-cluster expandable list is gone — both
    // cluster arrows now show one honest summary line each, computed with
    // the same derive function the arrow's own aggregate uses. It's the
    // only place the count appears (the pill above just says "Healthy").
    const clusterLines = getClusterStatusLines('2 managed clusters — 2 healthy')
    expect(clusterLines).toHaveLength(2)
    clusterLines.forEach((line) => {
      expect(within(line).getByText('2 healthy').className).toContain('text-green-700')
    })
  })

  it('colors all three buckets on the summary line when a card has healthy, degraded, and unknown clusters at once', async () => {
    mockAll({
      clusters: [
        { name: 'prod-1', labels: {}, connection_status: 'Successful', sharko_status: 'Connected' },
        { name: 'prod-2', labels: {}, connection_status: 'Failed', sharko_status: 'Unreachable', test_failing: true },
        { name: 'prod-3', labels: {} },
      ],
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())

    const clusterLines = getClusterStatusLines('3 managed clusters — 1 healthy, 1 with issues, 1 unknown')
    expect(clusterLines).toHaveLength(2)
    clusterLines.forEach((line) => {
      expect(within(line).getByText('1 healthy').className).toContain('text-green-700')
      expect(within(line).getByText('1 with issues').className).toContain('text-red-700')
      expect(within(line).getByText('1 unknown').className).toContain('text-gray-700')
    })
  })

  it('links every arrow to the page where you would act (read-only page)', async () => {
    mockAll({
      clusters: [{ name: 'prod-1', labels: {}, connection_status: 'Successful', sharko_status: 'Connected' }],
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())

    const settingsLinks = screen.getAllByRole('link', { name: /Check in Settings/ })
    expect(settingsLinks).toHaveLength(2)
    settingsLinks.forEach((l) => expect(l).toHaveAttribute('href', '/settings?section=connections'))

    const clustersLinks = screen.getAllByRole('link', { name: /Open the Managed Clusters page/ })
    expect(clustersLinks).toHaveLength(2)
    clustersLinks.forEach((l) => expect(l).toHaveAttribute('href', '/clusters'))

    // S2 — the one-line cluster status summary under each arrow also links
    // to /clusters (no more per-cluster deep link on this page: the
    // maintainer-approved simplification points both arrows at the same
    // Managed Clusters page instead of expanding a list here). This
    // cluster is healthy on both the Sharko and ArgoCD arrows, so the
    // identical summary line renders twice.
    const summaryLines = getClusterStatusLines('1 managed cluster — 1 healthy')
    expect(summaryLines).toHaveLength(2)
    summaryLines.forEach((el) => expect(el.closest('a')).toHaveAttribute('href', '/clusters'))
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// V2-cleanup-89.2 — the full identity explainer moved here from the
// Register Cluster dialog's Layer 1 (which now shows only a one-line
// summary — see ClustersOverview.identity.test.tsx). This section pins
// that the System page fetches capabilities and renders the full panel:
// detected ARN, method, and the expandable "how it works" explainer.
// ─────────────────────────────────────────────────────────────────────────────

// S1 — the secrets tables moved off this page into their own area (the
// Secrets area: /secrets/connections + /secrets/addons — Secrets-area
// rename; the old /secret-sync URL still works as a redirect). This page
// keeps ONE quiet summary line with a link, never the two tables the old
// ManagedSecretsSection rendered here.
describe('SystemView — secrets summary line (S1)', () => {
  it('shows the one-line summary and a link into the Secrets area, never a table', async () => {
    mockAll()
    mockGetManagedSecrets.mockResolvedValue({
      cluster_connection_secrets: [{ cluster: 'prod-eu', state: 'in_sync' }],
      addon_values_secrets: [],
      engines: { cluster_connection: { wired: true }, addon_values: { wired: false } },
    })
    renderPage()

    await waitFor(() =>
      expect(screen.getByText(/Sharko manages 1 secret — all in sync\./)).toBeInTheDocument(),
    )
    expect(screen.getByRole('link', { name: 'View Secrets' })).toHaveAttribute('href', '/secrets/connections')
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})

describe('SystemView — Sharko identity section (V2-cleanup-89.2)', () => {
  it('shows the detected ARN and method, and the setup-guide docs link', async () => {
    mockAll({
      capabilities: {
        aws: { detected: true, method: 'pod-identity', identity_arn: 'arn:aws:iam::123456789012:role/sharko-hub' },
        hub_platform: 'eks',
      },
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    await waitFor(() => {
      expect(screen.getByTestId('identity-detected')).toBeInTheDocument()
    })
    expect(screen.getByText(/Sharko is running with an AWS identity/)).toBeInTheDocument()
    expect(screen.getByText('arn:aws:iam::123456789012:role/sharko-hub')).toBeInTheDocument()
    expect(screen.getByText(/\(pod-identity\)/)).toBeInTheDocument()
  })

  it('shows "no identity detected" copy with the setup-guide link when Sharko has none', async () => {
    mockAll({ capabilities: { aws: { detected: false, method: 'none' }, hub_platform: 'unknown' } })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    await waitFor(() => {
      expect(screen.getByTestId('identity-not-detected')).toBeInTheDocument()
    })
    // S5 (walk day 4): neutral-first main line — detection is of
    // credentials, not cluster type, so this never claims the cluster IS
    // or ISN'T EKS. The old copy led with "for EKS clusters", which read
    // as an AWS assumption on a non-EKS playground (e.g. kind).
    expect(
      screen.getByText(
        "Sharko isn't using a cloud identity — it connects to clusters with the credentials you gave it.",
      ),
    ).toBeInTheDocument()
    expect(screen.getByText(/Running on EKS\?/)).toBeInTheDocument()
    const guideLink = screen.getByRole('link', { name: /see the setup guide/i })
    expect(guideLink).toHaveAttribute(
      'href',
      'https://sharko.readthedocs.io/en/latest/operator/eks-hub-and-spoke-identity/',
    )
  })

  it('the "How identity-based access works" panel expands with the plain-English explanation + docs link', async () => {
    mockAll({ capabilities: { aws: { detected: false, method: 'none' }, hub_platform: 'unknown' } })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    expect(screen.queryByTestId('identity-how-it-works')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /how identity-based access works/i }))

    await waitFor(() => {
      expect(screen.getByTestId('identity-how-it-works')).toBeInTheDocument()
    })
    expect(screen.getByText(/one IAM role on the hub cluster/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /read the full guide/i })).toHaveAttribute(
      'href',
      'https://sharko.readthedocs.io/en/latest/operator/eks-hub-and-spoke-identity/',
    )
  })

  it('falls back to the not-detected copy when the capabilities fetch fails, without blocking the rest of the page', async () => {
    mockAll()
    mockGetSystemCapabilities.mockRejectedValue(new Error('network error'))
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    await waitFor(() => {
      expect(screen.getByTestId('identity-not-detected')).toBeInTheDocument()
    })
    // The rest of the page still rendered fine.
    expect(screen.getByText('Sharko can read and write the repo.')).toBeInTheDocument()
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// WQ-3 (attention-move-badges) — the home-cluster identity card moved here
// from the Dashboard, placed next to the connection arrows. Full field
// rendering is HomeClusterCard's own contract (see its dedicated test
// file); this pins that SystemView wires the reads through and that there
// is now exactly ONE ArgoCD version source on the page.
// ─────────────────────────────────────────────────────────────────────────────
describe('SystemView — home-cluster identity card (WQ-3)', () => {
  it('renders the card with Sharko/ArgoCD/Kubernetes versions and uptime', async () => {
    mockAll({
      argocdVersion: 'v3.2.2',
      homeCluster: { available: true, kubernetes_version: 'v1.29.0', node_count: 3, nodes_ready: 3 },
      sharkoVersion: '4.2.0',
      uptime: '3h12m',
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    expect(await screen.findByText("Sharko's home cluster")).toBeInTheDocument()
    expect(screen.getByText('4.2.0')).toBeInTheDocument()
    // ONE ArgoCD version source (WQ-3): the same "v3.2.2" the tested-range
    // banner above shows also appears in the card — never a contradicting
    // second value.
    expect(screen.getByText('ArgoCD v3.2.2 detected')).toBeInTheDocument()
    expect(screen.getByText('v3.2.2')).toBeInTheDocument()
    expect(screen.getByText('v1.29.0')).toBeInTheDocument()
    expect(screen.getByText('all nodes ready')).toBeInTheDocument()
    expect(screen.getByText(/up 3h12m/)).toBeInTheDocument()
  })

  it('degrades every field to "—" independently when the home-cluster probe is unavailable', async () => {
    mockAll({
      // Empty (not undefined) — the mockAll default-parameter destructuring
      // would otherwise substitute back its own 'v3.2.2' default.
      argocdVersion: '',
      homeCluster: { available: false, message: 'only available when running in-cluster' },
    })
    renderPage()

    await waitFor(() => expect(screen.getByText('System')).toBeInTheDocument())
    expect(await screen.findByText("Sharko's home cluster")).toBeInTheDocument()
    expect(screen.getByText('only available when running in-cluster')).toBeInTheDocument()
    // Sharko version, ArgoCD version, Kubernetes version, Nodes — all "—".
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(4)
  })

  it('the ArgoCD version story never contradicts itself — "unknown" up top means "—" in the card too', async () => {
    mockAll({ argocdVersion: '', homeCluster: { available: true, kubernetes_version: 'v1.29.0', node_count: 1, nodes_ready: 1 } })
    renderPage()

    await waitFor(() => expect(screen.getByText('ArgoCD version unknown')).toBeInTheDocument())
    // The card's ArgoCD version cell degrades to "—" in lockstep — it is
    // never fed a version the top banner doesn't also know about.
    const label = await screen.findByText('ArgoCD version')
    expect(label.nextSibling?.textContent).toBe('—')
  })
})
