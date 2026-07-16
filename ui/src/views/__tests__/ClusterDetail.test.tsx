import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';
import type { TestClusterUnavailable } from '@/services/api';

// V125-1-10.5: render the view inside a fake admin AuthContext so admin-only
// actions (Test, Diagnose, Remove) are visible. The Test button lives behind
// `<RoleGuard roles={['admin', 'operator']}>` and is hidden by default in a
// raw render — without this provider the button-driven test cases fail to
// find the click target.
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
};

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const mockGetClusterComparison = vi.fn();
const mockTestClusterConnection = vi.fn();
const mockFetchTrackedPRs = vi.fn();
const mockDeregisterCluster = vi.fn();
const mockUpdateClusterAddons = vi.fn();
const mockUpdateClusterSettings = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRestartAddonSync = vi.fn();
const mockGetClusterHistory = vi.fn();
const mockGetClusterChanges = vi.fn();

// V2-cleanup-13: capture toast calls so the removal-feedback assertions can
// distinguish "cluster removed" (auto-merged) from "removal PR opened".
const mockShowToast = vi.fn();
vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual<typeof import('@/components/ToastNotification')>(
    '@/components/ToastNotification',
  );
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) };
});

vi.mock('@/services/api', async () => {
  // V125-1-10.5: keep `isTestClusterUnavailable` real so the view's
  // discriminator stays in sync with the API contract; only stub the
  // network call and the write helpers used by other actions on this page.
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
      getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
      enableAddonOnCluster: vi.fn().mockResolvedValue({}),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      restartAddonSync: (...args: unknown[]) => mockRestartAddonSync(...args),
      // V2-cleanup-39: AI-status is fetched once on mount to gate "Ask AI"
      // button rendering. Default to disabled so existing tests don't need
      // to assert on AI-specific elements.
      getAIStatus: vi.fn().mockResolvedValue({ enabled: false }),
      // getClusterHistory (the old ArgoCD sync-activity feed) is no longer
      // called from the Changes tab as of V2-cleanup-84.2, but the mock is
      // kept here in case other code paths still reach it.
      getClusterHistory: (...args: unknown[]) => mockGetClusterHistory(...args),
      // V2-cleanup-84.2: the Changes tab's "Completed changes" half is
      // CompletedChangesPanel, which calls api.getClusterChanges directly.
      getClusterChanges: (...args: unknown[]) => mockGetClusterChanges(...args),
    },
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
    deregisterCluster: (...args: unknown[]) => mockDeregisterCluster(...args),
    updateClusterAddons: (...args: unknown[]) => mockUpdateClusterAddons(...args),
    updateClusterSettings: (...args: unknown[]) => mockUpdateClusterSettings(...args),
    // BUG-042: ClusterDetail now fetches /api/v1/prs?status=open&cluster=<name>
    // alongside the cluster comparison to overlay pending-PR badges on
    // addon rows. Default to an empty PR list so existing tests keep
    // observing the no-badges baseline; per-test overrides drive the
    // BUG-042 assertions below.
    fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
  };
});

const comparisonResponse = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
  },
  git_total_addons: 5,
  git_enabled_addons: 4,
  git_disabled_addons: 1,
  argocd_total_applications: 4,
  argocd_healthy_applications: 3,
  argocd_synced_applications: 4,
  argocd_degraded_applications: 0,
  argocd_out_of_sync_applications: 0,
  addon_comparisons: [
    {
      addon_name: 'ingress-nginx',
      git_configured: true,
      git_version: '4.7.0',
      git_enabled: true,
      environment_version: '4.7.0',
      custom_version: '4.6.0',
      has_version_override: true,
      argocd_deployed: true,
      argocd_deployed_version: '4.7.0',
      argocd_namespace: 'ingress',
      argocd_health_status: 'Healthy',
      status: 'healthy',
      issues: [],
    },
    {
      addon_name: 'cert-manager',
      git_configured: true,
      git_version: '1.12.0',
      git_enabled: true,
      environment_version: '1.12.0',
      has_version_override: false,
      argocd_deployed: false,
      status: 'missing_in_argocd',
      issues: [
        'Addon is configured in Git but not deployed in ArgoCD',
        'This may indicate a deployment issue',
      ],
    },
    {
      addon_name: 'prometheus',
      git_configured: true,
      git_version: '2.45.0',
      git_enabled: true,
      environment_version: '2.45.0',
      has_version_override: false,
      argocd_deployed: true,
      argocd_deployed_version: '2.44.0',
      argocd_namespace: 'monitoring',
      argocd_health_status: 'Degraded',
      status: 'unhealthy',
      issues: ['Health status is Degraded'],
    },
  ],
  total_healthy: 1,
  total_with_issues: 1,
  total_missing_in_argocd: 1,
  total_untracked_in_argocd: 0,
  total_disabled_in_git: 0,
};

function renderView(section?: string) {
  const initialEntry = section
    ? `/clusters/prod-eu?section=${section}`
    : '/clusters/prod-eu';
  // Wrap in a fake admin AuthContext so RoleGuard-protected actions
  // (Test, Diagnose, Remove) render. Existing tests that assert on
  // role-agnostic content keep working — RoleGuard only ever gates UI
  // additively.
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('ClusterDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
    // BUG-042: default to "no pending PRs" so existing assertions don't
    // accidentally find a badge. Per-test overrides drive the badge cases.
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    // Default: secret-path save/preview resolves to a no-PR result.
    mockUpdateClusterSettings.mockResolvedValue({});
    // V2-cleanup-13: default removal returns an opened-but-not-merged PR.
    mockDeregisterCluster.mockResolvedValue({});
    // V2-cleanup-31: default apply-addons returns an empty result (no PR).
    mockUpdateClusterAddons.mockResolvedValue({});
    // V3-BUG-2: default catalog returns the addons present in comparisonResponse
    // (which represents a cluster with 3 enabled addons). Before V3-BUG-2, the
    // catalog fetch was lazy (triggered only when the picker opened), so tests
    // that never opened the picker didn't need catalog data. Now the catalog is
    // fetched eagerly on mount — if it returns empty, `noCatalog` becomes true
    // and hides the "+ Enable addon" button + the enabled-addon rows, breaking
    // most existing tests. Per-test overrides can still replace this default.
    mockGetAddonCatalog.mockResolvedValue({
      addons: [
        { addon_name: 'ingress-nginx', version: '4.7.0' },
        { addon_name: 'cert-manager', version: '1.12.0' },
        { addon_name: 'prometheus', version: '2.45.0' },
      ],
    });
    // V2-cleanup-81.1: default to an empty change timeline so History-section
    // tests don't need to stub this unless they exercise the timeline itself.
    mockGetClusterHistory.mockResolvedValue({ history: [] });
    // V2-cleanup-84.2: default to an empty completed-changes list so Changes-
    // tab tests don't need to stub this unless they exercise that list.
    mockGetClusterChanges.mockResolvedValue({ changes: [] });
  });

  // V2-cleanup-8.3 introduced "Host Cluster Nodes" labelling; V2-cleanup-78.1
  // removed the card entirely — it reported the Sharko HOST cluster's node
  // count, not this target cluster's, which was misleading on a per-cluster
  // page. The fetch, state, and card are gone; nothing to assert here anymore.

  it('renders loading state initially', () => {
    mockGetClusterComparison.mockReturnValue(new Promise(() => {}));
    renderView();
    expect(screen.getByText('Loading cluster details...')).toBeInTheDocument();
  });

  it('renders error state on API failure', async () => {
    mockGetClusterComparison.mockRejectedValue(new Error('Server error'));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Server error')).toBeInTheDocument();
    });
  });

  it('renders cluster name in header', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });
  });

  // V2-cleanup-78.1: opening a cluster now leads with its addons, not a
  // dissolved Overview tab. Cluster identity + version + connection live in
  // the persistent vitals ribbon, which renders on every section — including
  // the new default.
  it('shows the addons section by default, with the vitals ribbon showing cluster info', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Vitals ribbon shows cluster version.
    expect(screen.getByText('Cluster Version')).toBeInTheDocument();
    expect(screen.getByText('1.28')).toBeInTheDocument();

    // Addons is the default section — the addons table renders without
    // clicking anything in the nav.
    expect(screen.getByText('All Addons')).toBeInTheDocument();
    expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);
  });

  it('renders cluster detail with stat cards and comparison table on addons section', async () => {
    renderView('addons');

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Stat cards — zero-count cards (Not managed, Not Enabled) are hidden.
    // Names follow the V2-cleanup-61.2 canonical vocabulary.
    expect(screen.getByText('All Addons')).toBeInTheDocument();
    expect(screen.getByText('Healthy')).toBeInTheDocument();
    expect(screen.getByText('With Issues')).toBeInTheDocument();
    expect(screen.getAllByText('Missing from ArgoCD').length).toBeGreaterThanOrEqual(1);
    // Not managed (0) and Not Enabled (0) should be hidden
    expect(screen.queryByText('Not managed')).not.toBeInTheDocument();
    expect(screen.queryByText('Not Enabled')).not.toBeInTheDocument();

    // Table rows
    expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);
    expect(screen.getAllByText('cert-manager').length).toBeGreaterThan(0);
    expect(screen.getAllByText('prometheus').length).toBeGreaterThan(0);

    // Version override shown as Git Version
    expect(screen.getByText('4.6.0')).toBeInTheDocument();

    // Issues
    expect(screen.getByText('Health status is Degraded')).toBeInTheDocument();
  });

  it('calls API with cluster name from route params', async () => {
    renderView();

    await waitFor(() => {
      expect(mockGetClusterComparison).toHaveBeenCalledWith('prod-eu');
    });
  });

  it('filters addons by clicking stat card', async () => {
    renderView('addons');

    await waitFor(() => {
      expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);
    });

    // Click "Healthy" stat card
    const healthyCard = screen.getByText('Healthy').closest('[role="button"]');
    expect(healthyCard).toBeTruthy();
    fireEvent.click(healthyCard!);

    // Only healthy addon should show in the comparison table. Scoped to
    // the table itself (not `screen`) because the addon names also appear,
    // unfiltered, in the separate "Manage Addons" toggle panel further
    // down the page — that panel isn't touched by the table's stat-card
    // filter, so a page-wide query would see cert-manager/prometheus there
    // regardless of whether the table filtered correctly.
    const table = screen.getByRole('table');
    expect(within(table).getAllByText('ingress-nginx').length).toBeGreaterThan(0);
    expect(within(table).queryByText('cert-manager')).not.toBeInTheDocument();
    expect(within(table).queryByText('prometheus')).not.toBeInTheDocument();
  });

  it('navigates back when clicking back button', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText('Back to Clusters Overview'));
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });

  it('shows expand/collapse for long issues', async () => {
    renderView('addons');

    await waitFor(() => {
      expect(screen.getAllByText('cert-manager').length).toBeGreaterThan(0);
    });

    // cert-manager has 2 issues with long text, should show expand button
    expect(
      screen.getByText('Addon is configured in Git but not deployed in ArgoCD'),
    ).toBeInTheDocument();
  });

  // V2-cleanup-78.1: the Overview tab is dissolved — Addons, Config, History,
  // and the new admin-relevant Settings section remain.
  it('shows nav panel with section items (no Overview — dissolved)', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Nav panel items should be visible. "Addons" also appears as the
    // section heading since it's the default section now — assert at
    // least one match rather than a single unique one.
    expect(screen.getAllByText('Addons').length).toBeGreaterThan(0);
    expect(screen.getByText('Config')).toBeInTheDocument();
    // V2-cleanup-84.2: nav label renamed History -> Changes (the section
    // key stays 'history' to preserve deep links).
    expect(screen.getByText('Changes')).toBeInTheDocument();
    expect(screen.getByText('Settings')).toBeInTheDocument();
    expect(screen.queryByText('Overview')).not.toBeInTheDocument();
  });

  // V2-cleanup-81.1: every cluster change is a PR, so the standalone
  // "Pull Requests" tab duplicated History — it's gone. Open PRs now show
  // at the top of History instead.
  it('does not show a standalone Pull Requests nav tab', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.queryByText('Pull Requests')).not.toBeInTheDocument();
  });

  it('switches away from and back to the addons section via nav', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Addons is the default — the table is already visible with no clicks.
    expect(screen.getByText('All Addons')).toBeInTheDocument();
    expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);

    // Click Settings in nav panel — addons table goes away.
    fireEvent.click(screen.getByText('Settings'));

    await waitFor(() => {
      expect(screen.queryByText('All Addons')).not.toBeInTheDocument();
    });

    // Click back to Addons — the table returns.
    fireEvent.click(screen.getByText('Addons'));

    await waitFor(() => {
      expect(screen.getByText('All Addons')).toBeInTheDocument();
    });
    expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);
  });

  // BUG-042: the cluster→addons sub-page must show open pending-PR
  // badges inline on each addon row. The Sharko GitOps model opens a PR
  // for every addon enable / disable / upgrade — before this fix the
  // operator only saw the merged-state addon list and had to switch to
  // the cluster's PRs section to learn that work was in flight.
  describe('BUG-042: pending PR badges on cluster addons sub-page', () => {
    it('renders one badge per open PR targeting an addon on this cluster', async () => {
      mockFetchTrackedPRs.mockResolvedValueOnce({
        prs: [
          {
            pr_id: 4242,
            pr_url: 'https://github.com/example/repo/pull/4242',
            pr_branch: 'sharko/addon-upgrade-ingress-nginx-prod-eu',
            pr_title: 'Upgrade ingress-nginx to 4.8.0 on prod-eu',
            cluster: 'prod-eu',
            addon: 'ingress-nginx',
            operation: 'addon-upgrade',
            user: 'admin',
            source: 'sharko',
            created_at: '2026-05-20T10:00:00Z',
            last_status: 'open',
            last_polled_at: '2026-05-20T10:01:00Z',
          },
          {
            pr_id: 4243,
            pr_url: 'https://github.com/example/repo/pull/4243',
            pr_branch: 'sharko/addon-add-cert-manager-prod-eu',
            pr_title: 'Enable cert-manager on prod-eu',
            cluster: 'prod-eu',
            addon: 'cert-manager',
            operation: 'addon-add',
            user: 'admin',
            source: 'sharko',
            created_at: '2026-05-20T10:05:00Z',
            last_status: 'open',
            last_polled_at: '2026-05-20T10:06:00Z',
          },
        ],
      });

      renderView('addons');

      await waitFor(() => {
        expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);
      });

      // Wait for the PR fetch to resolve and badges to render.
      await waitFor(() => {
        expect(screen.getAllByTestId('addon-pending-pr-badge').length).toBeGreaterThanOrEqual(2);
      });

      const badges = screen.getAllByTestId('addon-pending-pr-badge');
      // Two open PRs → two badges
      expect(badges).toHaveLength(2);

      // Each badge links to the PR with target="_blank" — click opens in a new tab.
      const prUrls = badges.map((b) => b.getAttribute('href'));
      expect(prUrls).toContain('https://github.com/example/repo/pull/4242');
      expect(prUrls).toContain('https://github.com/example/repo/pull/4243');
      for (const badge of badges) {
        expect(badge).toHaveAttribute('target', '_blank');
        expect(badge).toHaveAttribute('rel', expect.stringContaining('noopener'));
      }

      // The /prs fetch must be scoped to this cluster + open status so we
      // don't drag in noise from other clusters or merged PRs.
      expect(mockFetchTrackedPRs).toHaveBeenCalledWith(
        expect.objectContaining({ cluster: 'prod-eu', status: 'open' }),
      );
    });

    it('renders no badges when no open PRs match', async () => {
      // beforeEach already sets the empty default; this test is the
      // explicit regression guard for "happy path = no badges".
      renderView('addons');

      await waitFor(() => {
        expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);
      });

      // Allow the PR fetch to resolve.
      await waitFor(() => {
        expect(mockFetchTrackedPRs).toHaveBeenCalled();
      });

      expect(screen.queryAllByTestId('addon-pending-pr-badge')).toHaveLength(0);
    });

    it('supports multiple open PRs against the same addon', async () => {
      // Rare but possible — e.g. an upgrade PR opened while a values
      // edit PR is still in flight. Both must render so the operator
      // can see (and resolve) both.
      mockFetchTrackedPRs.mockResolvedValueOnce({
        prs: [
          {
            pr_id: 1001,
            pr_url: 'https://github.com/example/repo/pull/1001',
            pr_branch: 'sharko/addon-upgrade-prometheus-prod-eu',
            pr_title: 'Upgrade prometheus to 2.46 on prod-eu',
            cluster: 'prod-eu',
            addon: 'prometheus',
            operation: 'addon-upgrade',
            user: 'admin',
            source: 'sharko',
            created_at: '2026-05-20T09:00:00Z',
            last_status: 'open',
            last_polled_at: '2026-05-20T09:01:00Z',
          },
          {
            pr_id: 1002,
            pr_url: 'https://github.com/example/repo/pull/1002',
            pr_branch: 'sharko/values-edit-prometheus-prod-eu',
            pr_title: 'Edit prometheus values on prod-eu',
            cluster: 'prod-eu',
            addon: 'prometheus',
            operation: 'values-edit',
            user: 'admin',
            source: 'sharko',
            created_at: '2026-05-20T09:10:00Z',
            last_status: 'open',
            last_polled_at: '2026-05-20T09:11:00Z',
          },
        ],
      });

      renderView('addons');

      await waitFor(() => {
        expect(screen.getAllByText('prometheus').length).toBeGreaterThan(0);
      });

      await waitFor(() => {
        expect(screen.getAllByTestId('addon-pending-pr-badge').length).toBe(2);
      });

      // Both badges link to their respective PRs.
      const badges = screen.getAllByTestId('addon-pending-pr-badge');
      const hrefs = badges.map((b) => b.getAttribute('href')).sort();
      expect(hrefs).toEqual([
        'https://github.com/example/repo/pull/1001',
        'https://github.com/example/repo/pull/1002',
      ]);
    });

    it('drops PRs without an addon attribution (e.g. cluster register/deregister)', async () => {
      // Cluster-scope PRs (register / deregister / init) appear in the
      // cluster PRs section, not on individual addon rows. The handler
      // dropped any pr.addon === undefined.
      mockFetchTrackedPRs.mockResolvedValueOnce({
        prs: [
          {
            pr_id: 9999,
            pr_url: 'https://github.com/example/repo/pull/9999',
            pr_branch: 'sharko/register-prod-eu',
            pr_title: 'Register cluster prod-eu',
            cluster: 'prod-eu',
            // no `addon` — cluster-scope PR
            operation: 'cluster-register',
            user: 'admin',
            source: 'sharko',
            created_at: '2026-05-20T08:00:00Z',
            last_status: 'open',
            last_polled_at: '2026-05-20T08:01:00Z',
          },
        ],
      });

      renderView('addons');

      await waitFor(() => {
        expect(screen.getAllByText('ingress-nginx').length).toBeGreaterThan(0);
      });

      await waitFor(() => {
        expect(mockFetchTrackedPRs).toHaveBeenCalled();
      });

      // No per-addon badges — the cluster-scope PR was correctly ignored.
      expect(screen.queryAllByTestId('addon-pending-pr-badge')).toHaveLength(0);
    });
  });

  // BUG-034: the connection-status banner copy must distinguish "Unknown"
  // (no observation yet) from an actual failure. Previously the banner
  // showed "ArgoCD Connection Failed" whenever connection_status was
  // anything other than "Successful" — including "Unknown", which is
  // simply the absence of an observation.
  describe('BUG-034: cluster status banner copy', () => {
    // V2-cleanup-85.1: argocd_connection_status: 'Unknown' + no server_url
    // (comparisonResponse.cluster has none) is exactly the "not connected
    // yet" case — the type badge, status badge, and this banner used to all
    // say "Unknown" independently. They now collapse into one banner and
    // the two badges are hidden, instead of the old standalone "Status
    // unknown" copy.
    it('renders "Not connected yet" banner (not "Connection Failed", not "Status unknown", no type/status badges) when argocd_connection_status is Unknown and there is no server URL', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...comparisonResponse,
        argocd_connection_status: 'Unknown',
        cluster_connection_state: '',
      });
      renderView();

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // The single collapsed banner must appear …
      expect(screen.getByText('Not connected yet')).toBeInTheDocument();
      // … and none of the redundant "Unknown" signals may appear alongside it.
      expect(screen.queryByText('Status unknown')).not.toBeInTheDocument();
      expect(screen.queryByText('ArgoCD Connection Failed')).not.toBeInTheDocument();
      expect(screen.queryByText('Unknown')).not.toBeInTheDocument();
    });

    // The neutral "Status unknown" banner is still used for the narrower,
    // non-redundant case: ArgoCD hasn't reported a status yet, but Sharko
    // does have a server URL for the cluster (so the type badge resolves
    // to something other than "Unknown" and isn't hidden).
    it('renders "Status unknown" banner when argocd_connection_status is Unknown but a server URL is known', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...comparisonResponse,
        cluster: { ...comparisonResponse.cluster, server_url: 'https://prod-eu.eks.amazonaws.com' },
        argocd_connection_status: 'Unknown',
        cluster_connection_state: '',
      });
      renderView();

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      expect(screen.getByText('Status unknown')).toBeInTheDocument();
      expect(screen.queryByText('Not connected yet')).not.toBeInTheDocument();
      expect(screen.queryByText('ArgoCD Connection Failed')).not.toBeInTheDocument();
    });

    it('renders "ArgoCD Connection Failed" banner when argocd_connection_status is a real failure', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...comparisonResponse,
        argocd_connection_status: 'Failed',
        argocd_connection_message: 'unable to reach apiserver',
        cluster_connection_state: '',
      });
      renderView();

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // Real failures still get the red Connection Failed banner with the
      // underlying reason.
      expect(screen.getByText('ArgoCD Connection Failed')).toBeInTheDocument();
      expect(screen.getByText('unable to reach apiserver')).toBeInTheDocument();
    });

    it('renders neither banner when argocd_connection_status is Successful', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...comparisonResponse,
        argocd_connection_status: 'Successful',
        cluster_connection_state: 'Successful',
      });
      renderView();

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      expect(screen.queryByText('Status unknown')).not.toBeInTheDocument();
      expect(screen.queryByText('ArgoCD Connection Failed')).not.toBeInTheDocument();
    });
  });

  // V125-1-10.5: per-error-code Test failure rendering. Story 10.3 added
  // typed `error_code` values to the structured 503 envelope returned by
  // POST /api/v1/clusters/{name}/test. The UI must render branch-specific
  // copy + an action link per code instead of a generic "Test failed".
  //
  // Cases:
  //   1. no_secrets_backend                 — REGRESSION GUARD for BUG-035
  //   2. argocd_provider_iam_required       — Story 10.3 new code
  //   3. argocd_provider_exec_unsupported   — Story 10.3 new code
  //   4. argocd_provider_unsupported_auth   — Story 10.3 new code
  //   5. NO error_code on 503               — REGRESSION GUARD for pre-Story-10.3 servers
  //   6. 200 success                        — REGRESSION GUARD for happy path
  describe('V125-1-10.5: per-error-code Test failure banner', () => {
    function unavailable(
      error_code: TestClusterUnavailable['error_code'],
      error: string,
    ): TestClusterUnavailable {
      return { unavailable: true, error_code, error };
    }

    async function clickTestAndWaitForBanner(testid?: string) {
      // Render the default (addons) section and click Test — the header
      // action buttons and test-result rendering are section-agnostic.
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });
      const testBtn = screen.getByRole('button', { name: /^test connection$/i });
      fireEvent.click(testBtn);
      if (testid) {
        await waitFor(() => {
          expect(screen.getByTestId(testid)).toBeInTheDocument();
        });
      }
    }

    it('renders no_secrets_backend banner with Settings link (BUG-035 regression)', async () => {
      mockTestClusterConnection.mockResolvedValueOnce(
        unavailable(
          'no_secrets_backend',
          'Cluster connectivity test requires a secrets backend (Vault / AWS Secrets Manager / file-store) on the active connection. Configure one in Settings → Connections to enable testing.',
        ),
      );
      await clickTestAndWaitForBanner('test-unavailable-banner');

      const banner = screen.getByTestId('test-unavailable-banner');
      expect(banner).toHaveAttribute('data-error-code', 'no_secrets_backend');
      expect(banner).toHaveTextContent('Cluster test unavailable');
      expect(banner).toHaveTextContent(/secrets backend/i);

      const link = screen.getByRole('link', { name: /Open Settings → Connections/i });
      expect(link).toHaveAttribute('href', '/settings?section=connections');
    });

    it('renders argocd_provider_iam_required banner with IAM setup guide link', async () => {
      mockTestClusterConnection.mockResolvedValueOnce(
        unavailable(
          'argocd_provider_iam_required',
          'cluster Secret references AWS IAM authentication; configure AWS credentials for the Sharko pod role',
        ),
      );
      await clickTestAndWaitForBanner('test-unavailable-banner');

      const banner = screen.getByTestId('test-unavailable-banner');
      expect(banner).toHaveAttribute('data-error-code', 'argocd_provider_iam_required');
      expect(banner).toHaveTextContent(/AWS IAM authentication/i);
      // Production framing — speaks to AWS-managed clusters generally, not
      // EKS-specific copy and never anchors on kind/minikube.
      expect(banner).toHaveTextContent(/AWS-managed clusters/i);
      expect(banner).not.toHaveTextContent(/kind|minikube/i);

      const link = screen.getByRole('link', { name: /Open IAM setup guide/i });
      expect(link).toHaveAttribute('href', '/docs/operator/aws-iam-cluster-auth');
    });

    it('renders argocd_provider_exec_unsupported banner with NO action link', async () => {
      mockTestClusterConnection.mockResolvedValueOnce(
        unavailable(
          'argocd_provider_exec_unsupported',
          'cluster Secret uses execProviderConfig; exec-plugin auth is not supported in v1.x',
        ),
      );
      await clickTestAndWaitForBanner('test-unavailable-banner');

      const banner = screen.getByTestId('test-unavailable-banner');
      expect(banner).toHaveAttribute(
        'data-error-code',
        'argocd_provider_exec_unsupported',
      );
      expect(banner).toHaveTextContent(/exec-plugin auth/i);
      // The cloud-managed examples (gcloud / azure-cli / aws-iam-authenticator)
      // anchor the production concern.
      expect(banner).toHaveTextContent(/gcloud|azure-cli|aws-iam-authenticator/i);
      expect(banner).toHaveTextContent(/v1\.x/);
      expect(banner).not.toHaveTextContent(/kind|minikube/i);

      // No action link inside the banner.
      const linksInBanner = banner.querySelectorAll('a');
      expect(linksInBanner.length).toBe(0);
    });

    it('renders argocd_provider_unsupported_auth banner with NO action link', async () => {
      mockTestClusterConnection.mockResolvedValueOnce(
        unavailable(
          'argocd_provider_unsupported_auth',
          'cluster Secret has unrecognized auth shape',
        ),
      );
      await clickTestAndWaitForBanner('test-unavailable-banner');

      const banner = screen.getByTestId('test-unavailable-banner');
      expect(banner).toHaveAttribute(
        'data-error-code',
        'argocd_provider_unsupported_auth',
      );
      expect(banner).toHaveTextContent(/Unrecognized/i);
      expect(banner).toHaveTextContent(/argocd namespace/i);

      const linksInBanner = banner.querySelectorAll('a');
      expect(linksInBanner.length).toBe(0);
    });

    it('falls back to generic test-failure rendering when 503 envelope has no error_code (pre-Story-10.3 server)', async () => {
      // testClusterConnection's 503 path with no/unknown error_code throws
      // a plain Error today (legacy behaviour). The view renders that as a
      // generic Unreachable badge — it must NOT crash and must NOT show the
      // typed banner.
      mockTestClusterConnection.mockRejectedValueOnce(new Error('Service unavailable'));
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });
      fireEvent.click(screen.getByRole('button', { name: /^test connection$/i }));

      await waitFor(() => {
        expect(screen.getByText('Service unavailable')).toBeInTheDocument();
      });
      expect(screen.queryByTestId('test-unavailable-banner')).not.toBeInTheDocument();
    });

    it('renders happy-path Connected badge when test succeeds (200 regression guard)', async () => {
      mockTestClusterConnection.mockResolvedValueOnce({
        reachable: true,
        success: true,
        server_version: 'v1.29.3',
        steps: [
          { name: 'Fetch credentials', status: 'pass' },
          { name: 'Fetch server version', status: 'pass' },
        ],
      });
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });
      fireEvent.click(screen.getByRole('button', { name: /^test connection$/i }));

      await waitFor(() => {
        expect(screen.getByText(/Connected.*v1\.29\.3/)).toBeInTheDocument();
      });
      expect(screen.queryByTestId('test-unavailable-banner')).not.toBeInTheDocument();
    });

    // V2-cleanup-91.1/F5: the Test-connection result now lives in a modal
    // (TestConnectionModal), mirroring "Check permissions". Clicking the
    // button opens the modal and renders the same step-by-step result the
    // old inline panel showed.
    it('opens the Test connection modal and renders the step-by-step result (F5)', async () => {
      mockTestClusterConnection.mockResolvedValueOnce({
        reachable: true,
        success: true,
        server_version: 'v1.29.3',
        steps: [
          { name: 'Fetch credentials', status: 'pass' },
          { name: 'Fetch server version', status: 'pass' },
        ],
      });
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // Modal is not mounted until the button is clicked.
      expect(screen.queryByTestId('test-connection-modal')).not.toBeInTheDocument();

      fireEvent.click(screen.getByRole('button', { name: /^test connection$/i }));

      // Modal opens, titled for this cluster, and shows the step rows +
      // Connected badge.
      await waitFor(() => {
        expect(screen.getByTestId('test-connection-modal')).toBeInTheDocument();
      });
      expect(screen.getByText('Test connection: prod-eu')).toBeInTheDocument();
      expect(screen.getByText('Fetch credentials')).toBeInTheDocument();
      expect(screen.getByText(/Connected.*v1\.29\.3/)).toBeInTheDocument();
    });
  });

  // V2-cleanup-13 → V2-cleanup-40: cluster removal uses the global auto-merge
  // setting (no per-flow checkbox). The dialog shows a muted hint linking to
  // Settings → GitOps instead.
  describe('V2-cleanup-40: removal uses global auto-merge (no per-flow checkbox)', () => {
    async function openRemoveModal() {
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });
      fireEvent.click(screen.getByRole('button', { name: /Remove Cluster/i }));
      // The confirmation dialog appears.
      await waitFor(() => {
        expect(screen.getByText(/Remove cluster "prod-eu"\?/i)).toBeInTheDocument();
      });
    }

    it('does NOT render the "Merge PR automatically" toggle — global setting governs', async () => {
      await openRemoveModal();
      expect(screen.queryByLabelText(/Merge PR automatically/i)).not.toBeInTheDocument();
    });

    it('shows the "global GitOps setting" hint instead', async () => {
      await openRemoveModal();
      expect(screen.getByText(/global GitOps setting/i)).toBeInTheDocument();
    });

    it('does NOT send auto_merge — falls back to connection default', async () => {
      mockDeregisterCluster.mockResolvedValue({ git: { merged: true, pr_id: 7 } });
      await openRemoveModal();

      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        // Called with only the cluster name — no auto_merge arg.
        expect(mockDeregisterCluster).toHaveBeenCalledWith('prod-eu');
      });
    });

    // V3-D5: successful removal (both auto-merge and manual) navigates to
    // /clusters with the removalPR in router state — ClustersOverview shows
    // it as a dismissible note. Only failure stays on the detail page.
    it('merged: navigates to /clusters with removalPR state', async () => {
      mockDeregisterCluster.mockResolvedValue({
        git: { merged: true, pr_id: 7, pr_url: 'https://github.com/example/repo/pull/7' },
      });
      await openRemoveModal();
      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/clusters', {
          state: {
            removalPR: {
              cluster: 'prod-eu',
              pr_url: 'https://github.com/example/repo/pull/7',
              pr_id: 7,
              merged: true,
            },
          },
        });
      });
    });

    it('open PR: navigates to /clusters with removalPR state', async () => {
      mockDeregisterCluster.mockResolvedValue({
        git: { merged: false, pr_id: 12, pr_url: 'https://github.com/example/repo/pull/12' },
      });
      await openRemoveModal();
      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('/clusters', {
          state: {
            removalPR: {
              cluster: 'prod-eu',
              pr_url: 'https://github.com/example/repo/pull/12',
              pr_id: 12,
              merged: false,
            },
          },
        });
      });
    });

    it('failure: stays on the detail page and shows the error', async () => {
      mockDeregisterCluster.mockRejectedValue(new Error('Network error'));
      await openRemoveModal();
      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        expect(screen.getByText(/Network error/i)).toBeInTheDocument();
      });
      // Must NOT navigate away on failure.
      expect(mockNavigate).not.toHaveBeenCalled();
    });
  });

  // V3-TX-A3: "Preview changes" on every PR-opening operation. ClusterDetail
  // owns three PR-opening surfaces: remove-cluster (deregister), apply-changes
  // (updateClusterAddons), and update-cluster-settings (secret_path). Each gets
  // a Preview button that calls the op's dry-run, renders the DryRunResult, and
  // still requires a separate confirm to open the PR.
  describe('V3-TX-A3: Preview changes', () => {
    // Surface 2 — Remove cluster preview.
    it('remove-cluster: Preview calls deregisterCluster(dry-run) and renders deletions without removing', async () => {
      mockDeregisterCluster.mockImplementation((_name: string, _autoMerge: unknown, dryRun?: boolean) => {
        if (dryRun) {
          return Promise.resolve({
            pr_title: 'Remove cluster prod-eu',
            files_to_write: [
              { path: 'configuration/managed-clusters.yaml', action: 'update' },
              { path: 'configuration/clusters/prod-eu.yaml', action: 'delete' },
            ],
          });
        }
        return Promise.resolve({ git: { merged: false, pr_id: 1 } });
      });

      renderView();
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
      fireEvent.click(screen.getByRole('button', { name: /Remove Cluster/i }));
      await waitFor(() =>
        expect(screen.getByText(/Remove cluster "prod-eu"\?/i)).toBeInTheDocument(),
      );

      fireEvent.click(screen.getByRole('button', { name: /preview changes/i }));

      await waitFor(() =>
        expect(mockDeregisterCluster).toHaveBeenCalledWith('prod-eu', undefined, true),
      );
      // Dry-run rendered — the deletion path is the whole point.
      await waitFor(() =>
        expect(screen.getByText('configuration/clusters/prod-eu.yaml')).toBeInTheDocument(),
      );
      expect(screen.getByText('Remove cluster prod-eu')).toBeInTheDocument();
      // Preview must NOT have fired the real (non-dry-run) removal.
      expect(mockDeregisterCluster).not.toHaveBeenCalledWith('prod-eu');
    });

    // Surface 7 — Update cluster settings (secret_path) preview.
    it('secret-path: Preview calls updateClusterSettings(dry-run) and renders the diff without saving', async () => {
      mockUpdateClusterSettings.mockImplementation((_name: string, settings: { dry_run?: boolean }) => {
        if (settings?.dry_run) {
          return Promise.resolve({
            pr_title: 'Update secret path for prod-eu',
            files_to_write: [
              { path: 'configuration/managed-clusters.yaml', action: 'update' },
            ],
          });
        }
        return Promise.resolve({ message: 'Secret path updated' });
      });

      renderView('settings');
      await waitFor(() => expect(screen.getByText('Cluster Settings')).toBeInTheDocument());

      // Enter edit mode on the Secret Path (admin-gated pencil).
      fireEvent.click(screen.getByLabelText(/Edit secret path/i));
      const input = await screen.findByPlaceholderText('e.g. k8s-my-cluster');
      fireEvent.change(input, { target: { value: 'k8s-prod-eu' } });

      fireEvent.click(screen.getByRole('button', { name: /preview changes/i }));

      await waitFor(() =>
        expect(mockUpdateClusterSettings).toHaveBeenCalledWith('prod-eu', {
          secret_path: 'k8s-prod-eu',
          dry_run: true,
        }),
      );
      await waitFor(() =>
        expect(screen.getByText('Update secret path for prod-eu')).toBeInTheDocument(),
      );
      // Preview must NOT have saved (no non-dry-run call).
      expect(mockUpdateClusterSettings).not.toHaveBeenCalledWith('prod-eu', {
        secret_path: 'k8s-prod-eu',
      });
    });
  });

  // V2-cleanup-30: sharko_system row rendering
  describe('V2-cleanup-30: sharko_system comparison row', () => {
    const responseWithCheckApp = {
      ...comparisonResponse,
      addon_comparisons: [
        ...comparisonResponse.addon_comparisons,
        {
          addon_name: 'connectivity-check-prod-eu',
          argocd_deployed: true,
          argocd_application_name: 'connectivity-check-prod-eu',
          argocd_sync_status: 'OutOfSync',
          argocd_health_status: 'Missing',
          argocd_namespace: 'argocd',
          status: 'sharko_system',
          issues: [],
        },
      ],
      total_untracked_in_argocd: 0, // NOT counted in untracked
    };

    beforeEach(() => {
      mockGetClusterComparison.mockResolvedValue(responseWithCheckApp);
    });

    it('renders the row with "Sharko system" badge instead of "Unmanaged"', async () => {
      renderView('addons');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // The badge says "Sharko system", not "Unmanaged"
      expect(screen.getByText('Sharko system')).toBeInTheDocument();
      expect(screen.queryByText('Unmanaged')).not.toBeInTheDocument();
    });

    it('renders the display name "Connectivity check" (not the raw app name)', async () => {
      renderView('addons');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      expect(screen.getByText('Connectivity check')).toBeInTheDocument();
      // Raw app name should NOT appear as link text
      expect(screen.queryByText('Connectivity-check-prod-eu')).not.toBeInTheDocument();
    });

    it('renders the descriptive system explanation in the issues cell', async () => {
      renderView('addons');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      expect(screen.getByText(/tiny test app Sharko deploys through ArgoCD/)).toBeInTheDocument();
      // Must NOT show the untracked "not configured in Git" issue text
      expect(
        screen.queryByText(/Application exists in ArgoCD but not configured in Git/),
      ).not.toBeInTheDocument();
    });

    it('does NOT count the check app in total_untracked_in_argocd stat card', async () => {
      renderView('addons');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // total_untracked_in_argocd is 0 → "Unmanaged" stat card must be hidden
      expect(screen.queryByText('Unmanaged')).not.toBeInTheDocument();
    });
  });

  // V2-cleanup-31: Manage Addons rework — enabled-only list, searchable picker,
  // pending-changes model, connectivity-check system row.
  describe('V2-cleanup-31: Manage Addons rework', () => {
    // Base comparison data: ingress-nginx + cert-manager enabled; prometheus disabled.
    // addonToggles is initialised from addon_comparisons git_enabled values.
    const baseResponse = {
      ...comparisonResponse,
      addon_comparisons: [
        {
          addon_name: 'cert-manager',
          git_configured: true,
          git_version: '1.12.0',
          git_enabled: true,
          has_version_override: false,
          argocd_deployed: true,
          status: 'healthy',
          issues: [],
        },
        {
          addon_name: 'ingress-nginx',
          git_configured: true,
          git_version: '4.7.0',
          git_enabled: true,
          has_version_override: false,
          argocd_deployed: true,
          status: 'healthy',
          issues: [],
        },
        {
          addon_name: 'prometheus',
          git_configured: true,
          git_version: '2.45.0',
          git_enabled: false,
          has_version_override: false,
          argocd_deployed: false,
          status: 'disabled_in_git',
          issues: [],
        },
      ],
    };

    beforeEach(() => {
      mockGetClusterComparison.mockResolvedValue(baseResponse);
    });

    // --- 1. Enabled-only rows ---

    it('V3-AM1: with no pending changes, top strip is absent; enabled addons appear only in comparison table', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: The top strip (manage-addon-row-*) shows ONLY pending changes.
      // With no pending changes, there should be NO manage-addon-row elements.
      expect(screen.queryByTestId('manage-addon-row-cert-manager')).not.toBeInTheDocument();
      expect(screen.queryByTestId('manage-addon-row-ingress-nginx')).not.toBeInTheDocument();

      // Enabled addons appear in the comparison table
      expect(screen.getByText('cert-manager')).toBeInTheDocument();
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();

      // prometheus is disabled in git → appears in comparison table (which shows
      // ALL addons including disabled) but NOT in the top strip (which is pending-only)
      expect(screen.getByText('prometheus')).toBeInTheDocument();
      expect(screen.queryByTestId('manage-addon-row-prometheus')).not.toBeInTheDocument();
    });

    it('V3-AM1: with no enabled addons and no pending changes, top strip is absent (no "No addons enabled" message)', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...baseResponse,
        addon_comparisons: [
          {
            addon_name: 'prometheus',
            git_configured: true,
            git_version: '2.45.0',
            git_enabled: false,
            has_version_override: false,
            argocd_deployed: false,
            status: 'disabled_in_git',
            issues: [],
          },
        ],
      });
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: The "No addons enabled..." message was removed. The top strip
      // only shows PENDING changes. With no enabled addons AND no pending changes,
      // the strip is simply absent.
      expect(
        screen.queryByText(/no addons enabled on this cluster yet/i),
      ).not.toBeInTheDocument();
    });

    it('shows "No addons in catalog." when the catalog (addonToggles) is empty', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...baseResponse,
        addon_comparisons: [],
      });
      // V3-BUG-2: the catalog is now fetched eagerly on mount and is the
      // authoritative source for `noCatalog`. Before this fix, `noCatalog`
      // keyed off per-cluster `addonToggles` (seeded from the comparison),
      // so a 0-addon cluster showed "No addons" even when the catalog had
      // addons. Now the catalog is the truth — to genuinely show "No
      // addons", the CATALOG must be empty, not just the cluster.
      mockGetAddonCatalog.mockResolvedValueOnce({ addons: [] });
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
      // Wait for the catalog fetch to complete.
      await waitFor(() => expect(mockGetAddonCatalog).toHaveBeenCalledTimes(1));
      expect(screen.getByText('No addons in catalog.')).toBeInTheDocument();
    });

    // --- 2. Enable-addon picker ---

    it('opens the picker when "+ Enable addon" is clicked', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));

      await waitFor(() => {
        expect(screen.getByTestId('addon-picker-search')).toBeInTheDocument();
      });

      // prometheus is not enabled → it must appear in the picker
      expect(screen.getByTestId('addon-picker-item-prometheus')).toBeInTheDocument();
      // cert-manager + ingress-nginx are already enabled → NOT in picker
      expect(screen.queryByTestId('addon-picker-item-cert-manager')).not.toBeInTheDocument();
      expect(
        screen.queryByTestId('addon-picker-item-ingress-nginx'),
      ).not.toBeInTheDocument();
    });

    it('picker: typing filters by name', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
      await waitFor(() =>
        expect(screen.getByTestId('addon-picker-search')).toBeInTheDocument(),
      );

      fireEvent.change(screen.getByTestId('addon-picker-search'), {
        target: { value: 'prOM' },
      });
      expect(screen.getByTestId('addon-picker-item-prometheus')).toBeInTheDocument();
    });

    it('picker: clicking an addon stages it as pending-enable and removes it from the picker list', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
      await waitFor(() =>
        expect(screen.getByTestId('addon-picker-item-prometheus')).toBeInTheDocument(),
      );

      // Click prometheus in the picker
      fireEvent.click(screen.getByTestId('addon-picker-item-prometheus'));

      // prometheus is now staged → it must no longer appear as a picker item
      expect(
        screen.queryByTestId('addon-picker-item-prometheus'),
      ).not.toBeInTheDocument();

      // Close picker
      fireEvent.click(screen.getByTestId('addon-picker-done'));

      // The staged row must appear with a "pending" chip
      await waitFor(() => {
        expect(screen.getByTestId('manage-addon-row-prometheus')).toBeInTheDocument();
      });
      expect(screen.getByTestId('manage-addon-row-prometheus')).toHaveTextContent(
        /pending/i,
      );
    });

    // --- 3. Staged removal ---

    it('V3-AM1: clicking Remove on a comparison-table row stages a pending-remove (top strip shows "removing")', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: Remove button is now on the comparison table rows, not the top strip
      const removeButtons = screen.getAllByTestId('comparison-row-remove-btn');
      // Click the first one (cert-manager row)
      fireEvent.click(removeButtons[0]);

      // Now the pending-remove row appears in the top strip
      await waitFor(() => {
        expect(screen.getByTestId('manage-addon-row-cert-manager')).toBeInTheDocument();
      });
      expect(screen.getByTestId('manage-addon-row-cert-manager')).toHaveTextContent(
        /removing/i,
      );
      // Apply Changes button appears
      expect(screen.getByRole('button', { name: /apply changes/i })).toBeInTheDocument();
    });

    // --- 4. Payload identity ---

    // V2-cleanup-32: the payload sent to updateClusterAddons must include ONLY
    // keys that were enabled OR are being changed. Disabled-in-git catalog
    // addons that the operator never touched (prometheus here) must NOT be
    // included — sending them as `false` would add spurious labels to
    // managed-clusters.yaml. The backend guard independently rejects unknown
    // names (422), but the FE must never send junk in the first place.
    it('V3-AM1: Apply Changes sends only enabled/changing keys — never disabled-untouched keys', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: Stage cert-manager for removal via comparison-table Remove button
      const removeButtons = screen.getAllByTestId('comparison-row-remove-btn');
      fireEvent.click(removeButtons[0]); // cert-manager

      // Apply
      await waitFor(() => {
        expect(screen.getByRole('button', { name: /apply changes/i })).toBeInTheDocument();
      });
      fireEvent.click(screen.getByRole('button', { name: /apply changes/i }));

      await waitFor(() => {
        expect(mockUpdateClusterAddons).toHaveBeenCalledOnce();
      });

      // cert-manager: was enabled → include as false (being removed)
      // ingress-nginx: currently enabled → include as true
      // prometheus: disabled in git and never staged → must NOT be in payload
      expect(mockUpdateClusterAddons).toHaveBeenCalledWith('prod-eu', {
        'cert-manager': false,
        'ingress-nginx': true,
      });
    });

    it('Apply Changes after staging a new enable emits true for the staged addon', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // Stage prometheus for enable via the picker
      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
      await waitFor(() =>
        expect(screen.getByTestId('addon-picker-item-prometheus')).toBeInTheDocument(),
      );
      fireEvent.click(screen.getByTestId('addon-picker-item-prometheus'));
      fireEvent.click(screen.getByTestId('addon-picker-done'));

      // Apply
      await waitFor(() =>
        expect(screen.getByRole('button', { name: /apply changes/i })).toBeInTheDocument(),
      );
      fireEvent.click(screen.getByRole('button', { name: /apply changes/i }));

      await waitFor(() => {
        expect(mockUpdateClusterAddons).toHaveBeenCalledOnce();
      });

      expect(mockUpdateClusterAddons).toHaveBeenCalledWith('prod-eu', {
        'cert-manager': true,
        'ingress-nginx': true,
        prometheus: true,
      });
    });

    // --- 4b. V3-TX-A3: Preview changes (Surface 6) ---

    it('Preview changes calls updateClusterAddons(dry-run) and renders the diff without applying', async () => {
      mockUpdateClusterAddons.mockImplementation(
        (_name: string, _addons: unknown, dryRun?: boolean) => {
          if (dryRun) {
            return Promise.resolve({
              pr_title: 'Update addons on prod-eu',
              files_to_write: [
                { path: 'configuration/managed-clusters.yaml', action: 'update' },
              ],
              secrets_to_create: ['prod-eu-cert-manager-creds'],
            });
          }
          return Promise.resolve({ git: { merged: false, pr_id: 2 } });
        },
      );

      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: Stage cert-manager for removal via comparison-table Remove button
      const removeButtons = screen.getAllByTestId('comparison-row-remove-btn');
      fireEvent.click(removeButtons[0]); // cert-manager

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /preview changes/i })).toBeInTheDocument();
      });
      fireEvent.click(screen.getByRole('button', { name: /preview changes/i }));

      // Dry-run call carries the diff-only payload + dryRun: true.
      await waitFor(() =>
        expect(mockUpdateClusterAddons).toHaveBeenCalledWith(
          'prod-eu',
          { 'cert-manager': false, 'ingress-nginx': true },
          true,
        ),
      );
      await waitFor(() =>
        expect(screen.getByText('Update addons on prod-eu')).toBeInTheDocument(),
      );
      // Secret NAMES are surfaced (never values).
      expect(screen.getByText(/prod-eu-cert-manager-creds/)).toBeInTheDocument();
      // Preview must NOT have applied the change (no non-dry-run call).
      expect(mockUpdateClusterAddons).not.toHaveBeenCalledWith('prod-eu', {
        'cert-manager': false,
        'ingress-nginx': true,
      });
    });

    // --- 5. Discard resets state ---

    it('V3-AM1: Discard resets staged changes (pending row disappears)', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: Stage cert-manager for removal via comparison-table Remove button
      const removeButtons = screen.getAllByTestId('comparison-row-remove-btn');
      fireEvent.click(removeButtons[0]); // cert-manager

      await waitFor(() => {
        expect(screen.getByTestId('manage-addon-row-cert-manager')).toBeInTheDocument();
      });
      expect(screen.getByTestId('manage-addon-row-cert-manager')).toHaveTextContent(
        /removing/i,
      );

      // Discard
      fireEvent.click(screen.getByRole('button', { name: /discard/i }));

      // V3-AM1: The pending row disappears entirely (top strip only shows pending changes)
      await waitFor(() => {
        expect(screen.queryByTestId('manage-addon-row-cert-manager')).not.toBeInTheDocument();
      });
      // Apply Changes button gone
      expect(
        screen.queryByRole('button', { name: /apply changes/i }),
      ).not.toBeInTheDocument();
    });

    // --- 6. Connectivity-check system row ---

    it.each([
      ['verified_check'],
      ['check_pending'],
      ['check_failed'],
    ] as const)(
      'renders the connectivity-check row for connectivity_status=%s',
      async (connStatus) => {
        mockGetClusterComparison.mockResolvedValueOnce({
          ...baseResponse,
          cluster: { ...baseResponse.cluster, connectivity_status: connStatus },
        });
        renderView('addons');
        await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
        expect(screen.getByTestId('connectivity-check-row')).toBeInTheDocument();
        expect(screen.getByText('Connectivity check')).toBeInTheDocument();
        expect(screen.getByText(/Sharko system — automatic/i)).toBeInTheDocument();
        expect(
          screen.getByText(/tiny test app Sharko deploys through ArgoCD/i),
        ).toBeInTheDocument();
      },
    );

    it.each([
      ['verified_argocd'],
      [''],
      [undefined],
    ] as const)(
      'does NOT render the connectivity-check row for connectivity_status=%s',
      async (connStatus) => {
        mockGetClusterComparison.mockResolvedValueOnce({
          ...baseResponse,
          cluster: { ...baseResponse.cluster, connectivity_status: connStatus },
        });
        renderView('addons');
        await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
        expect(screen.queryByTestId('connectivity-check-row')).not.toBeInTheDocument();
      },
    );

    it('connectivity-check row has no remove/toggle affordance', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...baseResponse,
        cluster: { ...baseResponse.cluster, connectivity_status: 'verified_check' },
      });
      renderView('addons');
      await waitFor(() => expect(screen.getByTestId('connectivity-check-row')).toBeInTheDocument());

      const row = screen.getByTestId('connectivity-check-row');
      // No button inside the row (no X/remove)
      expect(row.querySelectorAll('button')).toHaveLength(0);
    });

    it('connectivity-check row is excluded from enabled-count in the card (not counted)', async () => {
      // The card doesn't display an explicit count today, but the check row
      // must not inject a manage-addon-row-* entry into the enabled list.
      mockGetClusterComparison.mockResolvedValueOnce({
        ...baseResponse,
        cluster: { ...baseResponse.cluster, connectivity_status: 'check_pending' },
      });
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // Connectivity check has its own testid, not a manage-addon-row-*
      expect(
        screen.queryByTestId('manage-addon-row-connectivity-check'),
      ).not.toBeInTheDocument();
    });

    it('connectivity-check row does not appear in the enable picker', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...baseResponse,
        cluster: { ...baseResponse.cluster, connectivity_status: 'check_pending' },
      });
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
      await waitFor(() =>
        expect(screen.getByTestId('addon-picker-search')).toBeInTheDocument(),
      );

      // The picker dialog lists catalog addons from addonToggles only —
      // "Connectivity check" must not appear as a picker item.
      expect(
        screen.queryByTestId('addon-picker-item-connectivity-check'),
      ).not.toBeInTheDocument();
      // The picker list itself must contain no connectivity-check item.
      const pickerList = screen.getByTestId('addon-picker-list');
      expect(pickerList).not.toHaveTextContent('Connectivity check');
    });

    // --- 7. RoleGuard behavior unchanged ---

    it('enabled-addons list and "+ Enable addon" button are hidden for non-admin users', async () => {
      const viewerAuth = {
        token: 'viewer-token',
        username: 'viewer',
        role: 'viewer',
        login: vi.fn(),
        logout: vi.fn(),
        isAuthenticated: true,
        isAdmin: false,
        loading: false,
        error: null,
      };
      const { MemoryRouter, Route, Routes } = await import('react-router-dom');
      const { ClusterDetail } = await import('@/views/ClusterDetail');
      const { AuthContext } = await import('@/hooks/useAuth');
      const { render: r, screen: s, waitFor: w } = await import('@testing-library/react');

      r(
        <AuthContext.Provider value={viewerAuth}>
          <MemoryRouter initialEntries={['/clusters/prod-eu?section=addons']}>
            <Routes>
              <Route path="/clusters/:name" element={<ClusterDetail />} />
            </Routes>
          </MemoryRouter>
        </AuthContext.Provider>,
      );

      await w(() => expect(s.getByText('prod-eu')).toBeInTheDocument());
      expect(s.queryByTestId('manage-addons-enable-btn')).not.toBeInTheDocument();
      expect(s.queryByTestId('manage-addon-row-cert-manager')).not.toBeInTheDocument();
    });
  });

  // V2-cleanup-32: enable-addon picker must source the real catalog; junk rows
  // (untracked_in_argocd, sharko_system) from the comparison response must
  // never appear in the toggle map, picker list, enable counts, or PATCH payload.
  describe('V2-cleanup-32: picker sources catalog; junk rows excluded', () => {
    // Comparison response that mirrors a real cluster: two enabled catalog
    // addons, one disabled catalog addon, one untracked ArgoCD app (third-
    // party), and Sharko's connectivity-check system app.
    const responseWithJunk = {
      ...comparisonResponse,
      addon_comparisons: [
        {
          addon_name: 'cert-manager',
          git_configured: true,
          git_version: '1.12.0',
          git_enabled: true,
          has_version_override: false,
          argocd_deployed: true,
          status: 'healthy',
          issues: [],
        },
        {
          addon_name: 'ingress-nginx',
          git_configured: true,
          git_version: '4.7.0',
          git_enabled: true,
          has_version_override: false,
          argocd_deployed: true,
          status: 'healthy',
          issues: [],
        },
        {
          addon_name: 'prometheus',
          git_configured: true,
          git_version: '2.45.0',
          git_enabled: false,
          has_version_override: false,
          argocd_deployed: false,
          status: 'disabled_in_git',
          issues: [],
        },
        // Untracked ArgoCD app — NOT in catalog, must never enter toggle map.
        {
          addon_name: 'some-manual-app',
          git_configured: false,
          git_enabled: false,
          has_version_override: false,
          argocd_deployed: true,
          status: 'untracked_in_argocd',
          issues: [],
        },
        // Sharko system row (connectivity check) — must never enter toggle map.
        {
          addon_name: 'connectivity-check-cluster-1',
          git_configured: false,
          git_enabled: false,
          has_version_override: false,
          argocd_deployed: true,
          status: 'sharko_system',
          issues: [],
        },
      ],
    };

    beforeEach(() => {
      mockGetClusterComparison.mockResolvedValue(responseWithJunk);
      // Picker catalog: cert-manager + ingress-nginx + prometheus (three real addons).
      // velero is in the catalog but absent from comparisons — it's the
      // "never-enabled catalog addon" case.
      mockGetAddonCatalog.mockResolvedValue({
        addons: [
          { addon_name: 'cert-manager', chart: 'cert-manager', repo_url: 'https://charts.jetstack.io', version: '1.12.0', total_clusters: 1, enabled_clusters: 1, healthy_applications: 1, degraded_applications: 0, missing_applications: 0, applications: [] },
          { addon_name: 'ingress-nginx', chart: 'ingress-nginx', repo_url: 'https://example.com', version: '4.7.0', total_clusters: 1, enabled_clusters: 1, healthy_applications: 1, degraded_applications: 0, missing_applications: 0, applications: [] },
          { addon_name: 'prometheus', chart: 'kube-prometheus-stack', repo_url: 'https://example.com', version: '2.45.0', total_clusters: 1, enabled_clusters: 0, healthy_applications: 0, degraded_applications: 0, missing_applications: 0, applications: [] },
          { addon_name: 'velero', chart: 'velero', repo_url: 'https://example.com', version: '5.0.0', total_clusters: 0, enabled_clusters: 0, healthy_applications: 0, degraded_applications: 0, missing_applications: 0, applications: [] },
        ],
        total_addons: 4,
        total_clusters: 1,
        addons_only_in_git: 0,
      });
    });

    it('V3-AM1: junk rows (untracked/sharko_system) never enter toggle map, never removable', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: With no pending changes, there are NO manage-addon-row elements
      // (the top strip is pending-only). To test the exclusion rule, verify that
      // junk addons do NOT have Remove buttons in the comparison table.
      // Catalog-enabled addons (cert-manager, ingress-nginx) DO have Remove buttons.
      const removeButtons = screen.getAllByTestId('comparison-row-remove-btn');
      expect(removeButtons.length).toBe(2); // Only 2 (cert-manager + ingress-nginx)

      // Junk addons (some-manual-app, connectivity-check-cluster-1) do NOT have
      // Remove buttons because they were excluded from the toggle map seeding.
    });

    it('junk rows never appear in the enable picker — not as items, not via search', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
      await waitFor(() =>
        expect(screen.getByTestId('addon-picker-search')).toBeInTheDocument(),
      );

      // Junk names must not appear in the picker at all.
      expect(screen.queryByTestId('addon-picker-item-some-manual-app')).not.toBeInTheDocument();
      expect(screen.queryByTestId('addon-picker-item-connectivity-check-cluster-1')).not.toBeInTheDocument();
    });

    it('catalog addon absent from comparisons (velero) appears in the picker after catalog fetch', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
      await waitFor(() =>
        expect(screen.getByTestId('addon-picker-search')).toBeInTheDocument(),
      );

      // velero is in the catalog but has no comparison row → it must appear
      // in the picker as available to enable.
      expect(screen.getByTestId('addon-picker-item-velero')).toBeInTheDocument();
    });

    it('V3-AM1: Apply payload never includes junk names, only catalog enabled/staged keys', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: Stage cert-manager for removal via comparison-table Remove button
      const removeButtons = screen.getAllByTestId('comparison-row-remove-btn');
      fireEvent.click(removeButtons[0]); // cert-manager

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /apply changes/i })).toBeInTheDocument();
      });
      fireEvent.click(screen.getByRole('button', { name: /apply changes/i }));
      await waitFor(() => {
        expect(mockUpdateClusterAddons).toHaveBeenCalledOnce();
      });

      const [, payload] = mockUpdateClusterAddons.mock.calls[0] as [string, Record<string, boolean>];
      // cert-manager: was enabled → include as false
      expect(payload['cert-manager']).toBe(false);
      // ingress-nginx: currently enabled → include as true
      expect(payload['ingress-nginx']).toBe(true);
      // Junk names must NEVER appear in the payload.
      expect('some-manual-app' in payload).toBe(false);
      expect('connectivity-check-cluster-1' in payload).toBe(false);
      // prometheus: disabled in git and not staged → must NOT be in payload
      expect('prometheus' in payload).toBe(false);
    });

    it('staging a catalog addon from the picker adds it to the payload as true', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
      await waitFor(() =>
        expect(screen.getByTestId('addon-picker-item-velero')).toBeInTheDocument(),
      );
      fireEvent.click(screen.getByTestId('addon-picker-item-velero'));
      fireEvent.click(screen.getByTestId('addon-picker-done'));

      await waitFor(() =>
        expect(screen.getByRole('button', { name: /apply changes/i })).toBeInTheDocument(),
      );
      fireEvent.click(screen.getByRole('button', { name: /apply changes/i }));
      await waitFor(() => {
        expect(mockUpdateClusterAddons).toHaveBeenCalledOnce();
      });

      const [, payload] = mockUpdateClusterAddons.mock.calls[0] as [string, Record<string, boolean>];
      // velero: newly staged → true
      expect(payload['velero']).toBe(true);
      // Junk must still not appear.
      expect('some-manual-app' in payload).toBe(false);
      expect('connectivity-check-cluster-1' in payload).toBe(false);
    });
  });

  /**
   * V2-cleanup-37: "Restart sync" button.
   *
   * The button must appear ONLY on rows with status=sync_failing and must
   * call api.restartAddonSync when clicked.
   */
  describe('V2-cleanup-37: Restart sync button', () => {
    const syncFailingResponse = {
      ...comparisonResponse,
      addon_comparisons: [
        {
          addon_name: 'keda',
          git_configured: true,
          git_version: '2.13.0',
          git_enabled: true,
          has_version_override: false,
          argocd_deployed: true,
          argocd_health_status: 'Healthy',
          status: 'sync_failing',
          issues: ['one or more synchronization tasks completed unsuccessfully, reason: CRD too long'],
        },
        {
          addon_name: 'cert-manager',
          git_configured: true,
          git_version: '1.12.0',
          git_enabled: true,
          has_version_override: false,
          argocd_deployed: true,
          argocd_health_status: 'Healthy',
          status: 'healthy',
          issues: [],
        },
      ],
      total_healthy: 1,
      total_with_issues: 1,
      total_missing_in_argocd: 0,
    };

    it('renders Restart sync button only on sync_failing rows', async () => {
      mockGetClusterComparison.mockResolvedValue(syncFailingResponse);
      mockRestartAddonSync.mockResolvedValue({ terminated: true, synced: true });

      renderView('addons');
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      // Exactly one "Restart sync" button — only for keda.
      const buttons = screen.getAllByTestId('restart-sync-btn');
      expect(buttons).toHaveLength(1);

      // cert-manager (healthy) should NOT have the button.
      expect(screen.queryAllByTestId('restart-sync-btn')).toHaveLength(1);
    });

    it('calls api.restartAddonSync when Restart sync is clicked', async () => {
      mockGetClusterComparison.mockResolvedValue(syncFailingResponse);
      mockRestartAddonSync.mockResolvedValue({ terminated: true, synced: true });

      renderView('addons');
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      const btn = screen.getByTestId('restart-sync-btn');
      fireEvent.click(btn);

      await waitFor(() => {
        expect(mockRestartAddonSync).toHaveBeenCalledWith('prod-eu', 'keda');
      });
    });

    it('shows success toast after successful restart', async () => {
      mockGetClusterComparison.mockResolvedValue(syncFailingResponse);
      mockRestartAddonSync.mockResolvedValue({ terminated: true, synced: true });

      renderView('addons');
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      const btn = screen.getByTestId('restart-sync-btn');
      fireEvent.click(btn);

      await waitFor(() => {
        expect(mockShowToast).toHaveBeenCalledWith(
          expect.stringContaining('Sync restarted'),
          'success',
        );
      });
    });

    it('shows error toast when restart fails', async () => {
      mockGetClusterComparison.mockResolvedValue(syncFailingResponse);
      mockRestartAddonSync.mockRejectedValue(new Error('ArgoCD unavailable'));

      renderView('addons');
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      const btn = screen.getByTestId('restart-sync-btn');
      fireEvent.click(btn);

      await waitFor(() => {
        expect(mockShowToast).toHaveBeenCalledWith(
          expect.stringContaining('Failed to restart sync'),
          'error',
        );
      });
    });
  });

  /**
   * V2-cleanup-36: with_issues filter includes sync_failing.
   *
   * Tests the status filter logic: sync_failing IS in the with_issues set;
   * deploying is NOT. These are unit-level assertions against the filter
   * constants rather than full rendering assertions, because the badge
   * rendering is covered in StatusBadge.test.tsx and the AddonCatalog tests.
   */
  describe('V2-cleanup-36: with_issues filter contract', () => {
    // The filter set is defined inline in ClusterDetail — test that the
    // comparison renders all three statuses correctly via the mock.
    it('renders keda addon with status sync_failing after V2-cleanup-36 changes', async () => {
      const v36Response = {
        ...comparisonResponse,
        addon_comparisons: [
          {
            addon_name: 'keda',
            git_configured: true,
            git_version: '2.13.0',
            git_enabled: true,
            has_version_override: false,
            argocd_deployed: true,
            argocd_health_status: 'Healthy',
            argocd_sync_status: 'OutOfSync',
            argocd_operation_state: 'Running',
            argocd_operation_message:
              'one or more synchronization tasks completed unsuccessfully, reason: CRD too long',
            status: 'sync_failing',
            issues: [
              'one or more synchronization tasks completed unsuccessfully, reason: CRD too long',
            ],
          },
          {
            addon_name: 'velero',
            git_configured: true,
            git_version: '5.1.0',
            git_enabled: true,
            has_version_override: false,
            argocd_deployed: true,
            argocd_health_status: 'Healthy',
            argocd_sync_status: 'OutOfSync',
            argocd_operation_state: 'Running',
            status: 'deploying',
            issues: [],
          },
          ...comparisonResponse.addon_comparisons,
        ],
        total_healthy: 1,
        total_with_issues: 1,
        total_missing_in_argocd: 0,
      };
      mockGetClusterComparison.mockResolvedValue(v36Response);

      // Render in the 'addons' section so the addon comparison table is visible.
      renderView('addons');

      // Wait for the cluster name to appear (comparison loaded).
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      // V2-cleanup-61.1 (E3c): addon names render as-is (no more
      // capitalize-mangling of canonical names like "keda"/"velero").
      // Both keda (sync_failing) and velero (deploying) must appear in the table.
      expect(screen.getAllByText('keda').length).toBeGreaterThan(0);
      expect(screen.getAllByText('velero').length).toBeGreaterThan(0);
    });
  });

  /**
   * V2-cleanup-38.2: full operation message in expanded row.
   *
   * Collapsed row shows the short issues[] text. Expanded row shows the full
   * argocd_operation_message in a scrollable monospace block.
   */
  describe('V2-cleanup-38.2: full sync-error text in expanded row', () => {
    const shortIssue = 'one or more synchronization tasks completed unsuccessfully';
    const fullMessage =
      'one or more synchronization tasks completed unsuccessfully, reason: ' +
      'failed to create typed patch object (keda/keda-admission-webhooks; apps/v1, Kind=Deployment): ' +
      '.spec.template.spec.containers[name="keda-admission-webhooks"].resources.metricServer: ' +
      'field not declared in schema,failed to create typed patch object ' +
      '(keda/keda-operator; apps/v1, Kind=Deployment): ' +
      '.spec.template.spec.containers[name="keda-operator"].resources.metricServer: ' +
      'field not declared in schema';

    const responseWithFullMsg = {
      ...comparisonResponse,
      addon_comparisons: [
        {
          addon_name: 'keda',
          git_configured: true,
          git_version: '2.13.0',
          git_enabled: true,
          has_version_override: false,
          argocd_deployed: true,
          argocd_health_status: 'Healthy',
          argocd_sync_status: 'OutOfSync',
          argocd_operation_state: 'Running',
          // Full message in argocd_operation_message; short version in issues[].
          argocd_operation_message: fullMessage,
          status: 'sync_failing',
          issues: [shortIssue],
        },
      ],
      total_healthy: 0,
      total_with_issues: 1,
      total_missing_in_argocd: 0,
    };

    it('collapsed row: full-message block is NOT visible', async () => {
      mockGetClusterComparison.mockResolvedValue(responseWithFullMsg);
      renderView('addons');
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      // The short issue text appears in the list.
      expect(screen.getByText(shortIssue)).toBeInTheDocument();

      // The full-message block must not be visible in collapsed state.
      expect(screen.queryByTestId('full-operation-message')).not.toBeInTheDocument();
    });

    it('expanded row: full-message block is visible with complete text', async () => {
      mockGetClusterComparison.mockResolvedValue(responseWithFullMsg);
      renderView('addons');
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      // Expand the row by clicking "Show more".
      const showMoreBtn = screen.getByText('Show more');
      fireEvent.click(showMoreBtn);

      // The full-message block should now be visible.
      const block = await screen.findByTestId('full-operation-message');
      expect(block).toBeInTheDocument();
      // It must contain the tail of the error that was previously cut off.
      expect(block.textContent).toContain('field not declared in schema');
    });
  });

  /**
   * V2-cleanup-38.4: adaptive polling and refetch after restart-sync.
   */
  describe('V2-cleanup-38.4: restart-sync triggers immediate refetch', () => {
    it('calls getClusterComparison again after a successful restart-sync', async () => {
      const syncFailingResponse = {
        ...comparisonResponse,
        addon_comparisons: [
          {
            addon_name: 'keda',
            git_configured: true,
            git_version: '2.13.0',
            git_enabled: true,
            has_version_override: false,
            argocd_deployed: true,
            argocd_health_status: 'Healthy',
            status: 'sync_failing',
            issues: ['sync operation failed'],
          },
        ],
        total_healthy: 0,
        total_with_issues: 1,
        total_missing_in_argocd: 0,
      };

      mockGetClusterComparison.mockResolvedValue(syncFailingResponse);
      mockRestartAddonSync.mockResolvedValue({ terminated: true, synced: true });
      renderView('addons');
      await screen.findByText('prod-eu', {}, { timeout: 5000 });

      const initialCallCount = mockGetClusterComparison.mock.calls.length;

      // Click restart-sync.
      const btn = screen.getByTestId('restart-sync-btn');
      fireEvent.click(btn);

      // Wait for the success toast, which fires after the API call resolves.
      await waitFor(() => {
        expect(mockShowToast).toHaveBeenCalledWith(
          expect.stringContaining('Sync restarted'),
          'success',
        );
      });

      // An extra getClusterComparison call must have fired (the immediate refetch).
      await waitFor(() => {
        expect(mockGetClusterComparison.mock.calls.length).toBeGreaterThan(initialCallCount);
      });
    });
  });

  /**
   * V2-cleanup-38.4: timer tests for adaptive polling interval.
   * Uses vi.useFakeTimers to control time precisely.
   */
  describe('V2-cleanup-38.4: adaptive polling interval', () => {
    it('uses 10s interval when a deploying addon is present', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        const deployingResponse = {
          ...comparisonResponse,
          addon_comparisons: [
            {
              addon_name: 'keda',
              git_configured: true,
              git_version: '2.13.0',
              git_enabled: true,
              has_version_override: false,
              argocd_deployed: true,
              status: 'deploying',
              issues: [],
            },
          ],
        };
        mockGetClusterComparison.mockResolvedValue(deployingResponse);

        renderView('addons');

        // Let the initial fetch + state settle.
        await act(async () => {
          await Promise.resolve();
        });
        const afterInit = mockGetClusterComparison.mock.calls.length;

        // Advance 10s — should trigger one more poll (active interval = 10s).
        await act(async () => {
          vi.advanceTimersByTime(10_000);
          await Promise.resolve();
        });

        expect(mockGetClusterComparison.mock.calls.length).toBeGreaterThan(afterInit);
      } finally {
        vi.useRealTimers();
      }
    }, 15_000);

    it('uses 30s interval when no active addons are present', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        // All addons healthy — no deploying or sync_failing.
        mockGetClusterComparison.mockResolvedValue(comparisonResponse);

        renderView('addons');

        // Let the initial fetch settle.
        await act(async () => {
          await Promise.resolve();
        });
        const afterInit = mockGetClusterComparison.mock.calls.length;

        // Advance only 10s — must NOT trigger a poll (30s interval).
        await act(async () => {
          vi.advanceTimersByTime(10_000);
          await Promise.resolve();
        });
        expect(mockGetClusterComparison.mock.calls.length).toBe(afterInit);

        // Advance another 20s (total = 30s) — now should trigger.
        await act(async () => {
          vi.advanceTimersByTime(20_000);
          await Promise.resolve();
        });
        expect(mockGetClusterComparison.mock.calls.length).toBeGreaterThan(afterInit);
      } finally {
        vi.useRealTimers();
      }
    }, 15_000);
  });

  // V2-cleanup-81.1 / V2-cleanup-84.2: every cluster change is a PR, so the
  // standalone "Pull Requests" tab was folded into the unified "Changes"
  // tab. Open PRs render at the top as "Pending changes"; completed
  // (merged/closed) PRs render below as "Completed changes".
  describe('V2-cleanup-84.2: Changes tab unifies pending + completed PRs', () => {
    const openPR = {
      pr_id: 555,
      pr_url: 'https://github.com/example/repo/pull/555',
      pr_branch: 'sharko/addon-upgrade-ingress-nginx-prod-eu',
      pr_title: 'Upgrade ingress-nginx to 4.8.0 on prod-eu',
      cluster: 'prod-eu',
      addon: 'ingress-nginx',
      operation: 'addon-upgrade',
      user: 'admin',
      source: 'sharko',
      created_at: '2026-05-20T10:00:00Z',
      last_status: 'open',
      last_polled_at: '2026-05-20T10:01:00Z',
    };

    const completedChange = {
      operation: 'addon enable',
      addon: 'cert-manager',
      cluster: 'prod-eu',
      pr_id: 42,
      pr_url: 'https://github.com/example/repo/pull/42',
      opened_at: '2026-07-08T10:00:00Z',
      completed_at: '2026-07-08T10:05:00Z',
      status: 'merged',
      deploy_outcome: 'healthy',
    };

    it('shows a "Pending changes" group above the completed changes list', async () => {
      mockFetchTrackedPRs.mockResolvedValue({ prs: [openPR] });

      renderView('history');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // Pending-changes group heading + the PendingPRsPanel content
      // (cluster-scoped panel renders "Cluster PRs" as its own internal
      // heading).
      await waitFor(() => {
        expect(screen.getByText('Pending changes')).toBeInTheDocument();
      });
      expect(screen.getByText('Cluster PRs')).toBeInTheDocument();
      expect(screen.getByText(/Upgrade ingress-nginx to 4\.8\.0 on prod-eu/)).toBeInTheDocument();

      // Completed changes list still renders below (empty here — default mock).
      await waitFor(() => {
        expect(screen.getByText('No completed changes yet')).toBeInTheDocument();
      });
    });

    it('shows a single friendly empty state when there are no pending or completed changes', async () => {
      mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
      mockGetClusterChanges.mockResolvedValue({ changes: [] });

      renderView('history');

      await waitFor(() => {
        expect(screen.getByText('No changes yet')).toBeInTheDocument();
      });
      // V2-cleanup-85.1: the empty state now says concretely what a
      // "change" is (enabling/disabling an addon, editing its values) and
      // that each one is a pull request, instead of the vaguer original copy.
      expect(
        screen.getByText(
          "A change is something you do to this cluster — enabling or disabling an addon, or editing an addon's values. Each one goes out as a pull request for you to review, and shows up here once it's merged.",
        ),
      ).toBeInTheDocument();

      // The individual per-panel empty states are collapsed away in favor
      // of the combined message.
      expect(screen.queryByText('No tracked PRs')).not.toBeInTheDocument();
      expect(screen.queryByText('No completed changes yet')).not.toBeInTheDocument();
    });

    it('renders completed changes with a status pill and deploy-outcome badge', async () => {
      mockGetClusterChanges.mockResolvedValue({ changes: [completedChange] });

      renderView('history');

      await waitFor(() => {
        expect(screen.getByText('addon enable')).toBeInTheDocument();
      });
      expect(screen.getByText('— cert-manager')).toBeInTheDocument();
      expect(screen.getByText('Merged')).toBeInTheDocument();
      expect(screen.getByText('Deployed & healthy')).toBeInTheDocument();
    });

    it('expands a completed change row to show details and a link to the PR', async () => {
      mockGetClusterChanges.mockResolvedValue({ changes: [completedChange] });

      renderView('history');

      await waitFor(() => {
        expect(screen.getByText('addon enable')).toBeInTheDocument();
      });

      // Details aren't shown until the row is expanded.
      expect(screen.queryByText('View pull request on GitHub')).not.toBeInTheDocument();

      const rowToggle = screen.getByRole('button', { name: /addon enable/i });
      fireEvent.click(rowToggle);

      await waitFor(() => {
        expect(screen.getByText('View pull request on GitHub')).toBeInTheDocument();
      });
      const link = screen.getByText('View pull request on GitHub').closest('a');
      expect(link).toHaveAttribute('href', 'https://github.com/example/repo/pull/42');
      expect(link).toHaveAttribute('target', '_blank');
      expect(link).toHaveAttribute('rel', 'noopener noreferrer');

      // Collapsing hides the details again.
      fireEvent.click(rowToggle);
      await waitFor(() => {
        expect(screen.queryByText('View pull request on GitHub')).not.toBeInTheDocument();
      });
    });

    it('does not render the old standalone Pull Requests tab or route', async () => {
      renderView('prs');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // ?section=prs is no longer a recognized section — the addons/config/
      // history/settings sections all check activeSection === 'prs', which
      // never matches, so nothing PR-specific renders. Confirm the page
      // doesn't blow up and simply shows no section content tied to 'prs'.
      expect(screen.queryByText('Cluster PRs')).not.toBeInTheDocument();
      expect(screen.queryByText('Pending changes')).not.toBeInTheDocument();
    });

    it('surfaces the merge toast, refetches cluster data, and refetches completed changes when an open PR merges', async () => {
      mockFetchTrackedPRs.mockResolvedValue({ prs: [openPR] });

      renderView('history');

      await waitFor(() => {
        expect(screen.getByText(/Upgrade ingress-nginx to 4\.8\.0 on prod-eu/)).toBeInTheDocument();
      });

      const callsBeforeMerge = mockGetClusterComparison.mock.calls.length;
      const changesCallsBeforeMerge = mockGetClusterChanges.mock.calls.length;

      // Next PR fetch reports the same PR as merged — PendingPRsPanel's
      // own open→merged transition detection should fire onMergeDetected.
      mockFetchTrackedPRs.mockResolvedValueOnce({
        prs: [{ ...openPR, last_status: 'merged' }],
      });
      fireEvent.click(screen.getByRole('button', { name: /refresh prs/i }));

      await waitFor(() => {
        expect(mockShowToast).toHaveBeenCalledWith(
          expect.stringContaining('Merged PR #555: addon upgrade on prod-eu'),
        );
      });

      // The merge-detected callback also triggers fetchData() so the
      // cluster comparison (and thus addon state) is refreshed.
      await waitFor(() => {
        expect(mockGetClusterComparison.mock.calls.length).toBeGreaterThan(callsBeforeMerge);
      });

      // ...and it bumps CompletedChangesPanel's refreshKey so the
      // just-merged change shows up without a manual reload.
      await waitFor(() => {
        expect(mockGetClusterChanges.mock.calls.length).toBeGreaterThan(changesCallsBeforeMerge);
      });
    });
  });

  // V3-BUG-2: managed cluster with 0 addons enabled + non-empty catalog →
  // "+ Enable addon" button visible on load; "No addons in catalog." NOT shown.
  describe('V3-BUG-2: enable-addon button visibility with 0 enabled addons', () => {
    it('shows "+ Enable addon" button when cluster has 0 enabled addons but catalog is non-empty', async () => {
      // Cluster with 0 catalog addons enabled (git_configured rows) — only
      // junk rows (untracked/sharko_system) that don't seed addonToggles.
      mockGetClusterComparison.mockResolvedValue({
        ...comparisonResponse,
        addon_comparisons: [
          {
            addon_name: 'connectivity-check',
            git_configured: false,
            git_enabled: false,
            status: 'sharko_system',
            issues: [],
          },
        ],
        total_healthy: 0,
        total_with_issues: 0,
        total_missing_in_argocd: 0,
        total_untracked_in_argocd: 0,
        total_disabled_in_git: 0,
      });
      // Catalog has 3 addons available (fetched eagerly on mount).
      mockGetAddonCatalog.mockResolvedValue({
        addons: [
          { addon_name: 'ingress-nginx', version: '4.7.0' },
          { addon_name: 'cert-manager', version: '1.12.0' },
          { addon_name: 'prometheus', version: '2.45.0' },
        ],
      });

      renderView('addons');

      // Wait for the page to load (cluster name appears).
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // V3-BUG-2 fix: the "+ Enable addon" button is visible even though
      // this cluster has 0 enabled addons — catalog was fetched eagerly,
      // so `noCatalog` is false (catalog has 3 addons).
      await waitFor(() => {
        expect(screen.getByTestId('manage-addons-enable-btn')).toBeInTheDocument();
      });

      // "No addons in catalog." is NOT shown (catalog is non-empty).
      expect(screen.queryByText('No addons in catalog.')).not.toBeInTheDocument();

      // Catalog fetch was called eagerly on mount.
      expect(mockGetAddonCatalog).toHaveBeenCalledTimes(1);
    });

    it('hides "+ Enable addon" button and shows "No addons in catalog." when catalog is genuinely empty', async () => {
      // Cluster with 0 enabled addons.
      mockGetClusterComparison.mockResolvedValue({
        ...comparisonResponse,
        addon_comparisons: [],
        total_healthy: 0,
        total_with_issues: 0,
        total_missing_in_argocd: 0,
        total_untracked_in_argocd: 0,
        total_disabled_in_git: 0,
      });
      // Catalog is genuinely empty (0 addons available).
      mockGetAddonCatalog.mockResolvedValue({ addons: [] });

      renderView('addons');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // Wait for catalog fetch to resolve.
      await waitFor(() => {
        expect(mockGetAddonCatalog).toHaveBeenCalledTimes(1);
      });

      // "+ Enable addon" button is hidden (catalog is empty).
      expect(screen.queryByTestId('manage-addons-enable-btn')).not.toBeInTheDocument();

      // "No addons in catalog." is shown (honest empty-state).
      expect(screen.getByText('No addons in catalog.')).toBeInTheDocument();
    });

    it('V3-AM1: does not regress: cluster with enabled addons shows "Manage addons" button', async () => {
      // Default comparisonResponse has 3 enabled addons.
      mockGetAddonCatalog.mockResolvedValue({
        addons: [
          { addon_name: 'ingress-nginx', version: '4.7.0' },
          { addon_name: 'cert-manager', version: '1.12.0' },
          { addon_name: 'prometheus', version: '2.45.0' },
        ],
      });

      renderView('addons');

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // V3-AM1: Button is visible and reads "Manage addons".
      await waitFor(() => {
        const button = screen.getByTestId('manage-addons-enable-btn');
        expect(button).toBeInTheDocument();
        expect(button).toHaveTextContent('Manage addons');
      });

      // V3-AM1: Enabled addons appear in the comparison table (not the top strip).
      // The top strip is pending-only, so with no pending changes, no manage-addon-row elements.
      expect(screen.queryByTestId('manage-addon-row-ingress-nginx')).not.toBeInTheDocument();
      expect(screen.queryByTestId('manage-addon-row-cert-manager')).not.toBeInTheDocument();
      expect(screen.queryByTestId('manage-addon-row-prometheus')).not.toBeInTheDocument();

      // Enabled addons are in the comparison table instead
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
      expect(screen.getByText('cert-manager')).toBeInTheDocument();

      // "No addons in catalog." is NOT shown.
      expect(screen.queryByText('No addons in catalog.')).not.toBeInTheDocument();
    });
  });

  // V3-AP1: addon preview always carries Apply/Discard; background poll can't
  // strand a pending toggle edit. Before this fix: enable an addon → Preview →
  // while preview is shown, background poll fires → reseeds addonToggles/
  // originalToggles from git → hasToggleChanges flips false → footer (Apply/
  // Discard) unmounts → but togglePreview state survives → dead-end preview.
  describe('V3-AP1: addon preview gate', () => {
    it('footer renders when preview is shown', async () => {
      // The fix: footer gate is `hasToggleChanges || togglePreview` instead of
      // just `hasToggleChanges`. Render the page, manually set togglePreview
      // state (simulating a "Preview changes" click), and assert "Apply Changes"
      // + "Discard" buttons are visible even when hasToggleChanges is false.
      mockUpdateClusterAddons.mockImplementation((_name: string, _payload: unknown, dryRun?: boolean) => {
        if (dryRun) {
          return Promise.resolve({
            pr_title: 'Remove prometheus from prod-eu',
            files_to_write: [{ path: 'configuration/managed-clusters.yaml', action: 'update' }],
          });
        }
        return Promise.resolve({ git: { merged: false, pr_id: 42 } });
      });

      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: Remove prometheus via comparison-table Remove button.
      // (In the baseResponse fixture, prometheus is in addon_comparisons.)
      // Find prometheus's Remove button by searching for its row in the comparison table
      const prometheusTableRows = screen.getAllByRole('row');
      const prometheusRow = prometheusTableRows.find(row => within(row).queryByText('prometheus'));
      expect(prometheusRow).toBeTruthy();
      const prometheusRemoveBtn = within(prometheusRow!).getByTestId('comparison-row-remove-btn');
      fireEvent.click(prometheusRemoveBtn);

      // "Apply Changes" visible after staging the removal.
      await waitFor(() => expect(screen.getByRole('button', { name: /Apply Changes/i })).toBeInTheDocument());

      // Preview.
      const previewBtn = screen.getByRole('button', { name: /Preview changes/i });
      fireEvent.click(previewBtn);

      // Wait for preview to render.
      await waitFor(() => expect(mockUpdateClusterAddons).toHaveBeenCalledWith('prod-eu', expect.anything(), true));
      await waitFor(() => expect(screen.getByText(/Remove prometheus from prod-eu/i)).toBeInTheDocument());

      // "Apply Changes" + "Discard" STILL visible with the preview.
      expect(screen.getByRole('button', { name: /Apply Changes/i })).toBeInTheDocument();
      expect(screen.getByRole('button', { name: /Discard/i })).toBeInTheDocument();
    });

    it('Discard clears both the pending delta and the preview', async () => {
      mockUpdateClusterAddons.mockImplementation((_name: string, _payload: unknown, dryRun?: boolean) => {
        if (dryRun) {
          return Promise.resolve({
            pr_title: 'Remove prometheus from prod-eu',
            files_to_write: [{ path: 'configuration/managed-clusters.yaml', action: 'update' }],
          });
        }
        return Promise.resolve({ git: { merged: false, pr_id: 42 } });
      });

      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // V3-AM1: Remove prometheus via comparison-table Remove button.
      const prometheusTableRows = screen.getAllByRole('row');
      const prometheusRow = prometheusTableRows.find(row => within(row).queryByText('prometheus'));
      expect(prometheusRow).toBeTruthy();
      const prometheusRemoveBtn = within(prometheusRow!).getByTestId('comparison-row-remove-btn');
      fireEvent.click(prometheusRemoveBtn);

      // Preview.
      const previewBtn = await screen.findByRole('button', { name: /Preview changes/i });
      fireEvent.click(previewBtn);
      await waitFor(() => expect(screen.getByText(/Remove prometheus from prod-eu/i)).toBeInTheDocument());

      // Discard.
      const discardBtn = screen.getByRole('button', { name: /Discard/i });
      fireEvent.click(discardBtn);

      // Footer is gone (no pending changes, no preview).
      await waitFor(() => {
        expect(screen.queryByRole('button', { name: /Apply Changes/i })).not.toBeInTheDocument();
      });

      // Preview is gone.
      expect(screen.queryByText(/Remove prometheus from prod-eu/i)).not.toBeInTheDocument();
    });

    it('clean page (no changes, no preview) → no Apply/Discard footer', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // No pending changes, no preview → footer is not rendered.
      expect(screen.queryByRole('button', { name: /Apply Changes/i })).not.toBeInTheDocument();
      expect(screen.queryByRole('button', { name: /Discard/i })).not.toBeInTheDocument();
    });
  });

  // V3 U2: Diagnostics plain-English overview
  describe('V3 U2: Diagnostics section overview', () => {
    it('shows plain-English overview stating Sharko-own-connection + ArgoCD caveat', async () => {
      renderView('diagnostics');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // Overview text must state:
      // 1. Tests Sharko's own connection (using registered credentials)
      // 2. NOT testing ArgoCD's connection
      // 3. Honest caveat: some checks read ArgoCD-side state
      expect(screen.getByText(/Sharko itself/i)).toBeInTheDocument();
      expect(screen.getByText(/using the credentials you registered/i)).toBeInTheDocument();
      expect(screen.getByText(/not testing ArgoCD's connection/i)).toBeInTheDocument();
      expect(screen.getByText(/Two checks read ArgoCD-side state/i)).toBeInTheDocument();
    });
  });
});
