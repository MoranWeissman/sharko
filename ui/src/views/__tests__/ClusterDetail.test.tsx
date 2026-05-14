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
      getNodeInfo: vi.fn().mockResolvedValue(null),
      enableAddonOnCluster: vi.fn().mockResolvedValue({}),
      getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
    },
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
    deregisterCluster: vi.fn().mockResolvedValue({}),
    updateClusterAddons: vi.fn().mockResolvedValue({}),
    updateClusterSettings: vi.fn().mockResolvedValue({}),
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
});
