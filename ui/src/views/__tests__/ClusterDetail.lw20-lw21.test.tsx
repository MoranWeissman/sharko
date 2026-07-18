import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// LW-20 + LW-21 test: addon table improvements + button rename.
// - Column order: Addon name before Status
// - Headers NOT all-caps (sentence case)
// - Version column labels: "Declared (Git)" and "Running (in cluster)"
// - Issues column: with issues → count+severity chip that opens popover; with none → "—"
// - Button label: "+ Enable addon" not "Manage addons"

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
const mockFetchTrackedPRs = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockShowToast = vi.fn();

vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual<typeof import('@/components/ToastNotification')>(
    '@/components/ToastNotification',
  );
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) };
});

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
      getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      getAIStatus: vi.fn().mockResolvedValue({ enabled: false }),
      getClusterHistory: vi.fn().mockResolvedValue({ history: [] }),
      getClusterChanges: vi.fn().mockResolvedValue({ changes: [] }),
    },
    fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
  };
});

const comparisonWithIssues = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
  },
  git_total_addons: 3,
  git_enabled_addons: 3,
  git_disabled_addons: 0,
  argocd_total_applications: 3,
  argocd_healthy_applications: 1,
  argocd_synced_applications: 2,
  argocd_degraded_applications: 1,
  argocd_out_of_sync_applications: 1,
  addon_comparisons: [
    {
      addon_name: 'ingress-nginx',
      git_configured: true,
      git_version: '4.7.0',
      git_enabled: true,
      environment_version: '4.7.0',
      has_version_override: false,
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
  total_with_issues: 2,
  total_missing_in_argocd: 1,
  total_untracked_in_argocd: 0,
  total_disabled_in_git: 0,
};

function renderView() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={['/clusters/prod-eu']}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('ClusterDetail — LW-20 + LW-21', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonWithIssues);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({
      addons: [
        { addon_name: 'ingress-nginx', version: '4.7.0' },
        { addon_name: 'cert-manager', version: '1.12.0' },
        { addon_name: 'prometheus', version: '2.45.0' },
      ],
    });
  });

  it('LW-20: addon table has name column before status column', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    const table = screen.getByRole('table');
    const headers = within(table).getAllByRole('columnheader');

    // First header should be "Addon name", second should be "Status"
    expect(headers[0]).toHaveTextContent('Addon name');
    expect(headers[1]).toHaveTextContent('Status');
  });

  it('LW-20: headers are sentence case, not all-caps', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    const table = screen.getByRole('table');
    const headers = within(table).getAllByRole('columnheader');

    // Headers should be sentence case (not "ADDON NAME", "STATUS", etc.)
    expect(headers[0]).toHaveTextContent('Addon name');
    expect(headers[1]).toHaveTextContent('Status');
    expect(headers[2]).toHaveTextContent('Declared (Git)');
    expect(headers[3]).toHaveTextContent('Running (in cluster)');
    expect(headers[4]).toHaveTextContent('Namespace');
    expect(headers[5]).toHaveTextContent('Issues');
  });

  it('LW-20: version column labels are "Declared (Git)" and "Running (in cluster)"', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // New labels should be present
    expect(screen.getByText('Declared (Git)')).toBeInTheDocument();
    expect(screen.getByText('Running (in cluster)')).toBeInTheDocument();

    // Old labels should NOT be present
    expect(screen.queryByText('Git Version')).not.toBeInTheDocument();
    expect(screen.queryByText('ArgoCD Version')).not.toBeInTheDocument();
  });

  it('LW-20: issues column shows "—" when no issues', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    const table = screen.getByRole('table');
    const rows = within(table).getAllByRole('row');

    // ingress-nginx row (no issues) should show "—" in the issues cell
    const ingressRow = rows.find(row => within(row).queryByText('ingress-nginx'));
    expect(ingressRow).toBeDefined();
    if (ingressRow) {
      const cells = within(ingressRow).getAllByRole('cell');
      // Issues column is the 6th column (index 5)
      expect(cells[5]).toHaveTextContent('—');
    }
  });

  it('LW-20: issues column shows count+severity chip when issues exist', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('cert-manager')).toBeInTheDocument();
    });

    // cert-manager has 2 issues → should show "2 errors" chip
    expect(screen.getByText('2 errors')).toBeInTheDocument();

    // prometheus has 1 issue → should show "1 error" chip
    expect(screen.getByText('1 error')).toBeInTheDocument();
  });

  it('LW-20: clicking issues chip opens popover with issue details', async () => {
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByText('cert-manager')).toBeInTheDocument();
    });

    // Click the "2 errors" chip for cert-manager
    const chip = screen.getByText('2 errors');
    await user.click(chip);

    // Popover should show both issues
    await waitFor(() => {
      expect(screen.getByText('Addon is configured in Git but not deployed in ArgoCD')).toBeInTheDocument();
      expect(screen.getByText('This may indicate a deployment issue')).toBeInTheDocument();
    });
  });

  it('LW-21: button is labeled "+ Enable addon" not "Manage addons"', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // New label should be present
    expect(screen.getByRole('button', { name: /enable addon/i })).toBeInTheDocument();

    // Old label should NOT be present
    expect(screen.queryByRole('button', { name: /manage addons/i })).not.toBeInTheDocument();
  });
});
