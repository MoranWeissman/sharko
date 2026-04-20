/**
 * Accessibility audit — v1.20 pages retrofit (Story V122-1, WCAG 2.1 AA).
 *
 * Sister file to `a11y.test.tsx`, which only covers the v1.21 Marketplace
 * pages. v1.21 shipped axe-core for new pages but explicitly deferred the
 * v1.20 pages to v1.22 (see header in `a11y.test.tsx`). This file closes
 * that gap for the five top-level v1.20 pages:
 *
 *   1. AddonDetail
 *   2. ClusterDetail
 *   3. Settings (with all sections)
 *   4. AuditViewer
 *   5. Connections (also reachable via Settings → Connection)
 *
 * jsdom limitations apply (color-contrast disabled — see a11y.test.tsx).
 * The suite fails on any serious or critical violation under the
 * wcag2a/wcag2aa/wcag21a/wcag21aa rule set.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import axe from 'axe-core'

import { AddonDetail } from '@/views/AddonDetail'
import { ClusterDetail } from '@/views/ClusterDetail'
import { Settings } from '@/views/Settings'
import { AuditViewer } from '@/views/AuditViewer'
import { Connections } from '@/views/Connections'

/* ------------------------------------------------------------------ */
/*  Mocks                                                              */
/* ------------------------------------------------------------------ */

const meResponse = { username: 'a11y-tester', role: 'admin', has_github_token: true }

const addonDetailResponse = {
  addon: {
    addon_name: 'cert-manager',
    chart: 'cert-manager',
    repo_url: 'https://charts.jetstack.io',
    namespace: 'cert-manager',
    version: '1.14.0',
    total_clusters: 3,
    enabled_clusters: 3,
    healthy_applications: 3,
    degraded_applications: 0,
    missing_applications: 0,
    applications: [
      {
        cluster_name: 'prod',
        cluster_environment: 'prod',
        enabled: true,
        configured_version: '1.14.0',
        deployed_version: '1.14.0',
        namespace: 'cert-manager',
        health_status: 'Healthy',
        status: 'healthy',
        application_name: 'cert-manager-prod',
      },
    ],
  },
}

const clusterComparisonResponse = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
  },
  git_total_addons: 1,
  git_enabled_addons: 1,
  git_disabled_addons: 0,
  argocd_total_applications: 1,
  argocd_healthy_applications: 1,
  argocd_synced_applications: 1,
  argocd_degraded_applications: 0,
  argocd_out_of_sync_applications: 0,
  addon_comparisons: [
    {
      addon_name: 'cert-manager',
      git_configured: true,
      git_version: '1.14.0',
      git_enabled: true,
      environment_version: '1.14.0',
      has_version_override: false,
      argocd_deployed: true,
      argocd_deployed_version: '1.14.0',
      argocd_namespace: 'cert-manager',
      argocd_health_status: 'Healthy',
      status: 'healthy',
      issues: [],
    },
  ],
  total_healthy: 1,
  total_with_issues: 0,
  total_missing_in_argocd: 0,
  total_untracked_in_argocd: 0,
  total_disabled_in_git: 0,
}

const auditEntries = [
  {
    id: '1',
    timestamp: '2026-04-20T12:00:00Z',
    level: 'info',
    event: 'cluster.test',
    user: 'admin',
    action: 'test',
    resource: 'prod-eu',
    source: 'ui',
    result: 'success',
    duration_ms: 245,
  },
]

const mockConnections = [
  {
    name: 'production',
    description: 'Production environment',
    git_provider: 'github',
    git_repo_identifier: 'my-org/k8s-addons',
    git_token_masked: 'ghp_****1234',
    argocd_server_url: 'https://argocd.prod.example.com',
    argocd_token_masked: 'argo****5678',
    argocd_namespace: 'argocd',
    is_default: true,
    is_active: true,
  },
]

vi.mock('@/hooks/useConnections', () => ({
  useConnections: () => ({
    connections: mockConnections,
    activeConnection: 'production',
    loading: false,
    error: null,
    refreshConnections: vi.fn(),
    setActiveConnection: vi.fn(),
  }),
}))

// AuthContext is consumed by RoleGuard via useContext directly, so the mock
// must export the context object as well as the hook — RoleGuard's "no
// context" branch returns `fallback ?? null` which would hide the buttons
// we need under the axe scan.
vi.mock('@/hooks/useAuth', async () => {
  const React = await import('react')
  const ctxValue = {
    token: 'test-token',
    username: 'a11y-tester',
    role: 'admin',
    isAuthenticated: true,
    isAdmin: true,
    loading: false,
    error: null,
    login: () => Promise.resolve(),
    logout: () => {},
  }
  const FakeAuthContext = React.createContext<unknown>(ctxValue)
  return {
    AuthContext: FakeAuthContext,
    useAuth: () => ctxValue,
    AuthProvider: ({ children }: { children: React.ReactNode }) =>
      React.createElement(FakeAuthContext.Provider, { value: ctxValue }, children),
  }
})

vi.mock('@/services/api', () => {
  const noopOk = () => Promise.resolve({ status: 'ok' })
  return {
    // Generic api object — every method returns a non-rejecting empty payload.
    api: {
      health: () => Promise.resolve({ status: 'ok', mode: 'in-cluster' }),
      getMe: () => Promise.resolve(meResponse),
      getConnections: () =>
        Promise.resolve({ connections: mockConnections, active_connection: 'production' }),
      createConnection: noopOk,
      updateConnection: noopOk,
      testCredentials: () =>
        Promise.resolve({ git: { status: 'ok', message: '' }, argocd: { status: 'ok', message: '' } }),
      testConnection: () =>
        Promise.resolve({ git: { status: 'ok' }, argocd: { status: 'ok' } }),
      getProviders: () => Promise.resolve({ configured_provider: null, available_types: [] }),
      getRepoStatus: () => Promise.resolve({ initialized: true }),
      getAddonDetail: () => Promise.resolve(addonDetailResponse),
      getAddonValues: () => Promise.reject(new Error('not found')),
      getAddonValuesSchema: () =>
        Promise.resolve({ addon_name: 'cert-manager', current_values: '', schema: null }),
      getAIConfig: () =>
        Promise.resolve({ current_provider: 'none', available_providers: [], annotate_on_seed: false }),
      getAIStatus: () => Promise.resolve({ enabled: false }),
      getAISummary: () => Promise.resolve({ summary: '' }),
      setAIProvider: () => Promise.resolve({ status: 'ok', provider: 'none' }),
      testAI: () => Promise.resolve({ status: 'ok' }),
      testAIConfig: () => Promise.resolve({ status: 'ok', response: 'pong' }),
      saveAIConfig: noopOk,
      getClusterComparison: () => Promise.resolve(clusterComparisonResponse),
      getNodeInfo: () => Promise.resolve(null),
      getClusterHistory: () => Promise.resolve({ history: [] }),
      enableAddonOnCluster: noopOk,
      getAddonCatalog: () => Promise.resolve({ addons: [] }),
      getUpgradeRecommendations: () => Promise.resolve({ cards: [], current_version: '1.14.0' }),
      getUpgradeVersions: () => Promise.resolve({ versions: [] }),
      // Newer endpoints used by Settings sub-sections
      getSecretsProviderConfig: () => Promise.resolve({ type: '', region: '' }),
      saveSecretsProviderConfig: noopOk,
      getGitOpsConfig: () =>
        Promise.resolve({
          base_branch: 'main',
          branch_prefix: 'sharko/',
          commit_prefix: 'sharko:',
          pr_auto_merge: false,
        }),
      saveGitOpsConfig: noopOk,
      // Catalog-y endpoints
      listCuratedCatalog: () => Promise.resolve({ addons: [], total: 0 }),
      reprobeArtifactHub: () => Promise.resolve({ reachable: true, probed_at: '' }),
    },
    // Top-level helpers
    initRepo: () => Promise.resolve({ status: 'initialized' }),
    fetchAuditLog: () => Promise.resolve({ entries: auditEntries }),
    createAuditStream: () => ({ onmessage: null, onerror: null, close: vi.fn() }),
    getAddonPRs: vi.fn().mockResolvedValue({ prs: [] }),
    upgradeAddon: vi.fn().mockResolvedValue({ pr_url: '' }),
    removeAddon: vi.fn().mockResolvedValue({}),
    configureAddon: vi.fn().mockResolvedValue({}),
    deregisterCluster: vi.fn().mockResolvedValue({}),
    updateClusterAddons: vi.fn().mockResolvedValue({}),
    updateClusterSettings: vi.fn().mockResolvedValue({}),
    testClusterConnection: vi.fn().mockResolvedValue({ reachable: true, server_version: 'v1.29.0' }),
    isAddonAlreadyExistsError: () => false,
  }
})

/* ------------------------------------------------------------------ */
/*  axe                                                                */
/* ------------------------------------------------------------------ */

const axeOpts: axe.RunOptions = {
  runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
  rules: {
    // jsdom can't compute layout colours reliably; we rely on shadcn defaults
    // (4.5:1 by construction) plus the manual sweep tracked in
    // docs/developer-guide/accessibility.md, same as a11y.test.tsx.
    'color-contrast': { enabled: false },
  },
}

async function expectNoSeriousViolations(container: HTMLElement, label: string) {
  const results = await axe.run(container, axeOpts)
  const blocking = results.violations.filter(
    (v) => v.impact === 'serious' || v.impact === 'critical',
  )
  if (blocking.length > 0) {
    const summary = blocking
      .map((v) => `${v.id} (${v.impact}) — ${v.help}: ${v.nodes.length} node(s)`)
      .join('\n')
    throw new Error(`${label}: ${blocking.length} blocking a11y violations\n${summary}`)
  }
  expect(blocking).toEqual([])
}

/* ------------------------------------------------------------------ */
/*  Tests                                                              */
/* ------------------------------------------------------------------ */

describe('v1.20 pages — WCAG 2.1 AA (axe-core retrofit, V122-1)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('AddonDetail has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter initialEntries={['/addons/cert-manager']}>
        <Routes>
          <Route path="/addons/:name" element={<AddonDetail />} />
        </Routes>
      </MemoryRouter>,
    )
    // Wait for the addon header (h1 with the addon name) — past the loading
    // spinner and any first-render placeholders.
    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: /cert-manager/i })).toBeInTheDocument()
    })
    await expectNoSeriousViolations(container, 'AddonDetail')
  })

  it('ClusterDetail has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter initialEntries={['/clusters/prod-eu']}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>,
    )
    // Cluster name renders in the page header (currently h2; V122-1 keeps it
    // as the page heading regardless of level).
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /prod-eu/i })).toBeInTheDocument()
    })
    await expectNoSeriousViolations(container, 'ClusterDetail')
  })

  it('Settings (default Connection section) has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter initialEntries={['/settings']}>
        <Routes>
          <Route path="/settings" element={<Settings />} />
        </Routes>
      </MemoryRouter>,
    )
    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: /^Settings$/ })).toBeInTheDocument()
    })
    await expectNoSeriousViolations(container, 'Settings')
  })

  it('AuditViewer has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter>
        <AuditViewer />
      </MemoryRouter>,
    )
    // V122-3 promoted the page heading to h1; assert that to defend the
    // heading hierarchy fix against future drift.
    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: /Audit Log/i })).toBeInTheDocument()
    })
    await expectNoSeriousViolations(container, 'AuditViewer')
  })

  it('Connections (standalone) has no serious violations', async () => {
    const { container } = render(
      <MemoryRouter>
        <Connections />
      </MemoryRouter>,
    )
    await waitFor(() => {
      expect(screen.getByRole('heading', { name: /^Settings$/ })).toBeInTheDocument()
    })
    await expectNoSeriousViolations(container, 'Connections')
  })
})
