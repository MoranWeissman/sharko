import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
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
const mockGetNodeInfo = vi.fn();
const mockDeregisterCluster = vi.fn();
const mockUpdateClusterAddons = vi.fn();
const mockGetAddonCatalog = vi.fn();

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
      getNodeInfo: (...args: unknown[]) => mockGetNodeInfo(...args),
      enableAddonOnCluster: vi.fn().mockResolvedValue({}),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
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
    // Default to "no node info" so the host-node card is hidden in the
    // baseline; the V2-cleanup-8.3 test below overrides this.
    mockGetNodeInfo.mockResolvedValue(null);
    // V2-cleanup-13: default removal returns an opened-but-not-merged PR.
    mockDeregisterCluster.mockResolvedValue({});
    // V2-cleanup-31: default apply-addons returns an empty result (no PR).
    mockUpdateClusterAddons.mockResolvedValue({});
    // V2-cleanup-32: default catalog returns empty (most tests don't exercise
    // the picker's catalog fetch; per-test overrides in the 32 suite set up
    // real catalog data).
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
  });

  // V2-cleanup-8.3: the node-count card always reflects the Sharko HOST
  // cluster's nodes (the API only lists in-cluster nodes), yet it renders on
  // every target's detail page. The old "Nodes Ready" label made a green host
  // count look like the target's health, contradicting an Unreachable target.
  // The card is now labelled "Host Cluster Nodes" with clarifying subtext.
  describe('V2-cleanup-8.3: host-node card labelling', () => {
    it('labels the node card "Host Cluster Nodes" (not "Nodes Ready") with clarifying subtext', async () => {
      mockGetNodeInfo.mockResolvedValue({ ready: 1, total: 1, not_ready: 0 });
      renderView();

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      await waitFor(() => {
        expect(screen.getByText('Host Cluster Nodes')).toBeInTheDocument();
      });
      // The misleading old label must be gone.
      expect(screen.queryByText('Nodes Ready')).not.toBeInTheDocument();
      // Clarifying subtext distinguishes host from target.
      expect(screen.getByText(/Sharko's own cluster, not this target/i)).toBeInTheDocument();
      // The count itself still renders.
      expect(screen.getByText('1 / 1')).toBeInTheDocument();
    });
  });

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

  it('shows overview section by default with cluster info', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Overview section shows cluster version and connection info
    expect(screen.getByText('Cluster Version')).toBeInTheDocument();
    expect(screen.getByText('1.28')).toBeInTheDocument();
  });

  it('renders cluster detail with stat cards and comparison table on addons section', async () => {
    renderView('addons');

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Stat cards — zero-count cards (Unmanaged, Not Enabled) are hidden
    expect(screen.getByText('All Addons')).toBeInTheDocument();
    expect(screen.getByText('Healthy')).toBeInTheDocument();
    expect(screen.getByText('With Issues')).toBeInTheDocument();
    expect(screen.getAllByText('Not Deployed').length).toBeGreaterThanOrEqual(1);
    // Unmanaged (0) and Not Enabled (0) should be hidden
    expect(screen.queryByText('Unmanaged')).not.toBeInTheDocument();
    expect(screen.queryByText('Not Enabled')).not.toBeInTheDocument();

    // Table rows
    expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
    expect(screen.getByText('Cert-manager')).toBeInTheDocument();
    expect(screen.getByText('Prometheus')).toBeInTheDocument();

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
      expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
    });

    // Click "Healthy" stat card
    const healthyCard = screen.getByText('Healthy').closest('[role="button"]');
    expect(healthyCard).toBeTruthy();
    fireEvent.click(healthyCard!);

    // Only healthy addon should show
    expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
    expect(screen.queryByText('Cert-manager')).not.toBeInTheDocument();
    expect(screen.queryByText('Prometheus')).not.toBeInTheDocument();
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
      expect(screen.getByText('Cert-manager')).toBeInTheDocument();
    });

    // Cert-manager has 2 issues with long text, should show expand button
    expect(
      screen.getByText('Addon is configured in Git but not deployed in ArgoCD'),
    ).toBeInTheDocument();
  });

  it('shows nav panel with section items', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Nav panel items should be visible
    expect(screen.getByText('Overview')).toBeInTheDocument();
    expect(screen.getByText('Addons')).toBeInTheDocument();
    expect(screen.getByText('Config')).toBeInTheDocument();
  });

  it('switches to addons section when clicking Addons in nav', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Initially on overview — cluster info cards visible, not addons table
    expect(screen.queryByText('All Addons')).not.toBeInTheDocument();

    // Click Addons in nav panel
    fireEvent.click(screen.getByText('Addons'));

    await waitFor(() => {
      expect(screen.getByText('All Addons')).toBeInTheDocument();
    });

    expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
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
        expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
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
        expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
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
        expect(screen.getByText('Prometheus')).toBeInTheDocument();
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
        expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
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
      // Switch to Overview (default) and click Test.
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });
      const testBtn = screen.getByRole('button', { name: /^test$/i });
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
      fireEvent.click(screen.getByRole('button', { name: /^test$/i }));

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
      fireEvent.click(screen.getByRole('button', { name: /^test$/i }));

      await waitFor(() => {
        expect(screen.getByText(/Connected.*v1\.29\.3/)).toBeInTheDocument();
      });
      expect(screen.queryByTestId('test-unavailable-banner')).not.toBeInTheDocument();
    });
  });

  // V2-cleanup-13: cluster removal now offers the same auto-merge vs
  // manual-PR choice as init/register, and the success feedback tells the
  // user whether the cluster was removed (PR merged) or a PR was opened for
  // review (cluster stays listed until it merges).
  describe('V2-cleanup-13: removal auto-merge choice + feedback', () => {
    async function openRemoveModal() {
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });
      fireEvent.click(screen.getByRole('button', { name: /Remove Cluster/i }));
      // The confirmation dialog and its auto-merge toggle should appear.
      await waitFor(() => {
        expect(screen.getByText(/Remove cluster "prod-eu"\?/i)).toBeInTheDocument();
      });
    }

    it('renders the "Merge PR automatically" toggle in the remove dialog', async () => {
      await openRemoveModal();
      expect(screen.getByLabelText(/Merge PR automatically/i)).toBeInTheDocument();
    });

    it('auto-merge ON: sends auto_merge=true, toasts "removed", navigates away', async () => {
      mockDeregisterCluster.mockResolvedValue({ git: { merged: true, pr_id: 7 } });
      await openRemoveModal();

      fireEvent.click(screen.getByLabelText(/Merge PR automatically/i));
      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        expect(mockDeregisterCluster).toHaveBeenCalledWith('prod-eu', true);
      });
      await waitFor(() => {
        expect(mockShowToast).toHaveBeenCalledWith(
          expect.stringMatching(/removed/i),
          'success',
        );
      });
      expect(mockNavigate).toHaveBeenCalledWith('/clusters');
    });

    it('auto-merge OFF: sends auto_merge=false, toasts "PR opened", stays on page', async () => {
      mockDeregisterCluster.mockResolvedValue({
        git: { merged: false, pr_id: 12, pr_url: 'https://github.com/example/repo/pull/12' },
      });
      await openRemoveModal();

      // Leave the toggle unchecked (default false), confirm removal.
      fireEvent.click(screen.getByRole('button', { name: /^Remove$/i }));

      await waitFor(() => {
        expect(mockDeregisterCluster).toHaveBeenCalledWith('prod-eu', false);
      });
      await waitFor(() => {
        // V2-cleanup-24: the PR id/url is now passed as a clickable link
        // object on the toast instead of being baked into the message string.
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
});
