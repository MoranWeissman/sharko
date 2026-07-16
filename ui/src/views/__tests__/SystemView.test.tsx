// V2-cleanup-57.3: System page phase 1 tests.
//
// Covers: the four arrows rendering each status state (healthy / degraded /
// unknown), the ArgoCD tested-range badge (in-range / out-of-range / unknown
// version), and that every element links to the existing page where you'd
// act (read-only contract).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import SystemView, {
  GIT_CONN_ALERT_TITLE,
  aggregateStatuses,
  deriveArgoClusterStatus,
  deriveArgoRepoArrow,
  deriveSharkoClusterLabel,
  deriveSharkoClusterStatus,
  deriveSharkoRepoArrow,
  parseMajorMinor,
  testedRangeLabel,
  versionOutsideTestedRange,
} from '@/views/SystemView'
import type { Cluster } from '@/services/models'

const mockGetSystemCapabilities = vi.fn()

vi.mock('@/services/api', () => ({
  api: {
    getRepoStatus: vi.fn(),
    getClusters: vi.fn(),
    getNotifications: vi.fn(),
    getObservability: vi.fn(),
  },
  getSystemCapabilities: (...args: unknown[]) => mockGetSystemCapabilities(...args),
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
  repo?: { initialized: boolean; bootstrap_synced: boolean; reason?: string }
  clusters?: Cluster[]
  notifications?: { id: string; type: string; title: string; description: string; timestamp: string; read: boolean }[]
  argocdVersion?: string
  capabilities?: { aws: { detected: boolean; method: string; identity_arn?: string }; hub_platform: string } | null
}

function mockAll({
  repo = { initialized: true, bootstrap_synced: true },
  clusters = [],
  notifications = [],
  argocdVersion = 'v3.2.2',
  capabilities = { aws: { detected: false, method: 'none' }, hub_platform: 'unknown' },
}: MockAllOpts = {}) {
  mockedApi.getRepoStatus.mockResolvedValue(repo)
  mockedApi.getClusters.mockResolvedValue({ clusters })
  mockedApi.getNotifications.mockResolvedValue({ notifications, unread_count: 0 })
  mockedApi.getObservability.mockResolvedValue(obsWithVersion(argocdVersion))
  mockGetSystemCapabilities.mockResolvedValue(capabilities)
}

function renderPage() {
  return render(
    <MemoryRouter>
      <SystemView />
    </MemoryRouter>,
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
      screen.getByText('ArgoCD is syncing the repo — the bootstrap application is healthy.'),
    ).toBeInTheDocument()
    // Cluster arrows aggregate
    expect(screen.getAllByText('2 of 2 healthy')).toHaveLength(2)
    // Detected version shown, no warning badge (v3.2 is in range)
    expect(screen.getByText('ArgoCD v3.2.2 detected')).toBeInTheDocument()
    expect(screen.queryByTestId('argocd-version-badge')).not.toBeInTheDocument()
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
    // Both cluster arrows aggregate to 0 of 1 healthy
    expect(screen.getAllByText('0 of 1 healthy')).toHaveLength(2)
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

  it('shows the calm warning badge when the detected minor is outside the tested range', async () => {
    mockAll({ argocdVersion: 'v9.9.1' })
    renderPage()

    await waitFor(() => expect(screen.getByText('ArgoCD v9.9.1 detected')).toBeInTheDocument())
    const badge = screen.getByTestId('argocd-version-badge')
    expect(badge.textContent).toContain('ArgoCD v9.9 detected — Sharko is tested with')
  })

  it('shows no badge when the ArgoCD version is unknown', async () => {
    mockedApi.getRepoStatus.mockResolvedValue({ initialized: true, bootstrap_synced: true })
    mockedApi.getClusters.mockResolvedValue({ clusters: [] })
    mockedApi.getNotifications.mockResolvedValue({ notifications: [], unread_count: 0 })
    mockedApi.getObservability.mockRejectedValue(new Error('boom'))
    mockGetSystemCapabilities.mockResolvedValue({ aws: { detected: false, method: 'none' }, hub_platform: 'unknown' })
    renderPage()

    await waitFor(() => expect(screen.getByText('ArgoCD version unknown')).toBeInTheDocument())
    expect(screen.queryByTestId('argocd-version-badge')).not.toBeInTheDocument()
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
    expect(screen.getAllByText('2 of 2 healthy')).toHaveLength(2)

    // Expand the Sharko → Clusters list and confirm the honest label split
    // (scoped to each row — the repo arrows above also render a default
    // "Healthy" pill, so we check per-cluster-row text, not page-wide).
    fireEvent.click(screen.getByRole('button', { name: /Per-cluster status \(Sharko → cluster\)/ }))
    const prod1Row = screen.getByText('prod-1').closest('li')
    const prod2Row = screen.getByText('prod-2').closest('li')
    expect(prod1Row).not.toBeNull()
    expect(prod2Row).not.toBeNull()
    expect(prod1Row!.textContent).toContain('Healthy')
    expect(prod2Row!.textContent).toContain('Reachable')
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

    const clustersLinks = screen.getAllByRole('link', { name: /Open the Clusters page/ })
    expect(clustersLinks).toHaveLength(2)
    clustersLinks.forEach((l) => expect(l).toHaveAttribute('href', '/clusters'))

    // Expanding a per-cluster list exposes a link to the cluster detail page
    fireEvent.click(screen.getByRole('button', { name: /Per-cluster status \(Sharko → cluster\)/ }))
    const clusterLink = screen.getByRole('link', { name: 'prod-1' })
    expect(clusterLink).toHaveAttribute('href', '/clusters/prod-1')
  })
})

// ─────────────────────────────────────────────────────────────────────────────
// V2-cleanup-89.2 — the full identity explainer moved here from the
// Register Cluster dialog's Layer 1 (which now shows only a one-line
// summary — see ClustersOverview.identity.test.tsx). This section pins
// that the System page fetches capabilities and renders the full panel:
// detected ARN, method, and the expandable "how it works" explainer.
// ─────────────────────────────────────────────────────────────────────────────

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
