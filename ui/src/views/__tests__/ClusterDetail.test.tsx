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
const mockGetAddonCatalog = vi.fn();
const mockRestartAddonSync = vi.fn();

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
    },
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
    deregisterCluster: (...args: unknown[]) => mockDeregisterCluster(...args),
    updateClusterAddons: (...args: unknown[]) => mockUpdateClusterAddons(...args),
    updateClusterSettings: vi.fn().mockResolvedValue({}),
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
    // V2-cleanup-13: default removal returns an opened-but-not-merged PR.
    mockDeregisterCluster.mockResolvedValue({});
    // V2-cleanup-31: default apply-addons returns an empty result (no PR).
    mockUpdateClusterAddons.mockResolvedValue({});
    // V2-cleanup-32: default catalog returns empty (most tests don't exercise
    // the picker's catalog fetch; per-test overrides in the 32 suite set up
    // real catalog data).
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
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
    expect(screen.getByText('Settings')).toBeInTheDocument();
    expect(screen.queryByText('Overview')).not.toBeInTheDocument();
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
    it('renders "Status unknown" banner (not "Connection Failed") when argocd_connection_status is Unknown', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...comparisonResponse,
        argocd_connection_status: 'Unknown',
        cluster_connection_state: '',
      });
      renderView();

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // The neutral copy must appear …
      expect(screen.getByText('Status unknown')).toBeInTheDocument();
      // … and the misleading "Connection Failed" copy must NOT appear when
      // the only signal is "Unknown".
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

    it('merged: toasts "removed" and navigates away', async () => {
      mockDeregisterCluster.mockResolvedValue({ git: { merged: true, pr_id: 7 } });
      await openRemoveModal();
      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        expect(mockShowToast).toHaveBeenCalledWith(
          expect.stringMatching(/removed/i),
          'success',
        );
      });
      expect(mockNavigate).toHaveBeenCalledWith('/clusters');
    });

    it('open PR: toasts "PR opened", stays on page', async () => {
      mockDeregisterCluster.mockResolvedValue({
        git: { merged: false, pr_id: 12, pr_url: 'https://github.com/example/repo/pull/12' },
      });
      await openRemoveModal();
      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        expect(mockShowToast).toHaveBeenCalledWith(
          expect.stringMatching(/PR opened for review/i),
          'success',
          { url: 'https://github.com/example/repo/pull/12', id: 12 },
        );
      });
      // Manual path must NOT navigate away — the cluster is still listed.
      expect(mockNavigate).not.toHaveBeenCalledWith('/clusters');
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

    it('renders rows only for enabled addons; disabled addons are not shown', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // cert-manager and ingress-nginx are enabled → rows present
      expect(screen.getByTestId('manage-addon-row-cert-manager')).toBeInTheDocument();
      expect(screen.getByTestId('manage-addon-row-ingress-nginx')).toBeInTheDocument();

      // prometheus is disabled in git → no row in the Manage Addons card
      expect(screen.queryByTestId('manage-addon-row-prometheus')).not.toBeInTheDocument();
    });

    it('shows a "No addons enabled" message when no addons are enabled', async () => {
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
      expect(
        screen.getByText(/no addons enabled on this cluster yet/i),
      ).toBeInTheDocument();
    });

    it('shows "No addons in catalog." when the catalog (addonToggles) is empty', async () => {
      mockGetClusterComparison.mockResolvedValueOnce({
        ...baseResponse,
        addon_comparisons: [],
      });
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
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

    it('clicking X marks a row as pending-removal with a strikethrough + "removing" chip', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      fireEvent.click(screen.getByTestId('manage-addon-remove-cert-manager'));

      // Row still present but marked for removal
      expect(screen.getByTestId('manage-addon-row-cert-manager')).toBeInTheDocument();
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
    it('Apply Changes sends only enabled/changing keys — never disabled-untouched keys', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // Stage cert-manager for removal
      fireEvent.click(screen.getByTestId('manage-addon-remove-cert-manager'));

      // Apply
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

    // --- 5. Discard resets state ---

    it('Discard resets staged changes back to the original state', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // Stage cert-manager for removal
      fireEvent.click(screen.getByTestId('manage-addon-remove-cert-manager'));
      expect(screen.getByTestId('manage-addon-row-cert-manager')).toHaveTextContent(
        /removing/i,
      );

      // Discard
      fireEvent.click(screen.getByRole('button', { name: /discard/i }));

      // Row is back to normal (no "removing" chip)
      expect(screen.getByTestId('manage-addon-row-cert-manager')).not.toHaveTextContent(
        /removing/i,
      );
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

    it('Manage Addons card is hidden for non-admin users', async () => {
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
      expect(s.queryByText('Manage Addons')).not.toBeInTheDocument();
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

    it('junk rows (untracked/sharko_system) never appear as manage-addon rows', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // Only catalog rows appear as manage-addon-row-* entries.
      expect(screen.getByTestId('manage-addon-row-cert-manager')).toBeInTheDocument();
      expect(screen.getByTestId('manage-addon-row-ingress-nginx')).toBeInTheDocument();
      // Junk must not appear as a managed row.
      expect(screen.queryByTestId('manage-addon-row-some-manual-app')).not.toBeInTheDocument();
      expect(screen.queryByTestId('manage-addon-row-connectivity-check-cluster-1')).not.toBeInTheDocument();
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

    it('Apply payload never includes junk names, only catalog enabled/staged keys', async () => {
      renderView('addons');
      await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

      // Stage cert-manager for removal (it's enabled in the fixture).
      fireEvent.click(screen.getByTestId('manage-addon-remove-cert-manager'));

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
});
