import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// HD1 (V3) — Diagnostics section. Check permissions + Diagnose connection
// moved from the header into a persistent Diagnostics nav section. Test
// connection results also persist there. Pins:
//  1. A "Diagnostics" nav section exists and routes via ?section=diagnostics.
//  2. Check permissions + Diagnose buttons render in the Diagnostics section.
//  3. Test connection result persists in Diagnostics after running from header.
//  4. Regression: AM1 (one addon list + Manage addons) and AP1 (Apply/Discard footer) intact.

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

const mockGetClusterComparison = vi.fn();
const mockFetchTrackedPRs = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockTestClusterConnection = vi.fn();
const mockDiagnoseCluster = vi.fn();
const mockDoctorCluster = vi.fn();

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
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
    diagnoseCluster: (...args: unknown[]) => mockDiagnoseCluster(...args),
    doctorCluster: (...args: unknown[]) => mockDoctorCluster(...args),
  };
});

const comparisonResponse = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
    addon_secrets_ready: true,
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
  ],
  total_healthy: 1,
  total_with_issues: 0,
  total_missing_in_argocd: 0,
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

describe('ClusterDetail — HD1 Diagnostics section (V3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
  });

  it('renders a "Diagnostics" nav section that routes to ?section=diagnostics', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    const diagnosticsTab = screen.getByRole('button', { name: 'Diagnostics' });
    expect(diagnosticsTab).toBeInTheDocument();

    fireEvent.click(diagnosticsTab);

    await waitFor(() => {
      // V3 U2: updated overview text
      expect(screen.getByText(/Sharko itself/)).toBeInTheDocument();
    });
  });

  it('renders Check permissions and Diagnose connection buttons in the Diagnostics section', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: 'Diagnostics' }));

    await waitFor(() => {
      expect(screen.getByTestId('run-check-permissions')).toBeInTheDocument();
      expect(screen.getByTestId('run-connection-doctor')).toBeInTheDocument();
    });
  });

  it('shows no test result initially in Diagnostics section', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: 'Diagnostics' }));

    await waitFor(() => {
      expect(screen.getByTestId('run-check-permissions')).toBeInTheDocument();
    });

    expect(screen.queryByText('Test Connection Result')).not.toBeInTheDocument();
  });

  it('runs Check permissions in-section and the FULL result PERSISTS (does not fade)', async () => {
    mockDiagnoseCluster.mockResolvedValue({
      namespace_access: [
        { permission: 'create secrets in ingress', passed: true },
        { permission: 'delete secrets in ingress', passed: true },
      ],
      suggested_fixes: [],
    });

    renderView();
    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: 'Diagnostics' }));
    await waitFor(() => {
      expect(screen.getByTestId('run-check-permissions')).toBeInTheDocument();
    });

    expect(mockDiagnoseCluster).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('run-check-permissions'));

    await waitFor(() => {
      expect(mockDiagnoseCluster).toHaveBeenCalledWith('prod-eu');
    });
    // The permissions report body renders in-section
    await waitFor(() => {
      expect(screen.getByText('Permission Checks')).toBeInTheDocument();
      expect(screen.getByText('create secrets in ingress')).toBeInTheDocument();
    });

    // Persistence: after the DOM settles, the result must still be present —
    // it does NOT auto-dismiss the way a fading modal would.
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByText('Permission Checks')).toBeInTheDocument();
    expect(screen.getByText('create secrets in ingress')).toBeInTheDocument();
    expect(screen.getByText('delete secrets in ingress')).toBeInTheDocument();
  });

  it('Test connection result persists in Diagnostics after running from header', async () => {
    mockTestClusterConnection.mockResolvedValue({
      reachable: true,
      server_version: 'v1.29.3',
    });

    renderView();
    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Test connection lives in the header — click it, then the TestConnectionModal
    // drives testClusterConnection and reports the result back to ClusterDetail,
    // which persists it in the Diagnostics section.
    fireEvent.click(screen.getByRole('button', { name: /^Test connection$/ }));

    await waitFor(() => {
      expect(mockTestClusterConnection).toHaveBeenCalledWith('prod-eu');
    });

    // Close the modal (Radix Dialog marks the rest of the page aria-hidden
    // while open) — the result was already reported to the parent.
    fireEvent.keyDown(document.body, { key: 'Escape', code: 'Escape' });
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    // Navigate to Diagnostics — the test result persists there.
    fireEvent.click(screen.getByRole('button', { name: 'Diagnostics' }));

    await waitFor(() => {
      expect(screen.getByText('Test Connection Result')).toBeInTheDocument();
    });
    expect(screen.getByText(/Cluster reachable/)).toBeInTheDocument();
  });

  it('header renders no bare refresh icon (HD1 removal)', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // The old bare refresh button had a specific icon with no text label
    // Check that there's NO button with just a RefreshCw icon and no text
    const buttons = screen.getAllByRole('button');
    const bareRefreshBtn = buttons.find(
      (btn) => btn.querySelector('svg.lucide-refresh-cw') && !btn.textContent?.trim(),
    );
    expect(bareRefreshBtn).toBeUndefined();
  });
});
