import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// ep4 round T, T2. When Sharko could not read the cluster's login, it never
// asked the cluster. The page must not say "Connection failed" or mark the
// cluster Unreachable. It says the connection was not tested, and shows the
// reason the server sent (for example, AWS turned down Sharko's login).

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
const mockTestConnection = vi.fn();
const mockTestProvider = vi.fn();

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
      // v4-wave2 8.1: the Diagnostics section now also probes global
      // connection health (git/ArgoCD/vault) via useConnectionHealth.
      // Default to healthy so existing diagnostics assertions are
      // unaffected; the dedicated connection-health test overrides these.
      testConnection: (...args: unknown[]) => mockTestConnection(...args),
      testProvider: (...args: unknown[]) => mockTestProvider(...args),
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

const notRead = "Sharko could not read this cluster's login, so it did not test the connection.";
const awsReason = 'AWS turned down the login Sharko uses for AWS.';

async function runTestAndOpenDiagnostics() {
  renderView();
  await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: /^Test connection$/ }));
  await waitFor(() => expect(mockTestClusterConnection).toHaveBeenCalledWith('prod-eu'));
  fireEvent.keyDown(document.body, { key: 'Escape', code: 'Escape' });
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: 'Diagnostics' }));
  await waitFor(() => expect(screen.getByText('Test Connection Result')).toBeInTheDocument());
}

describe('ClusterDetail — Test connection when the login could not be read', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockTestConnection.mockResolvedValue({ git: { status: 'ok' }, argocd: { status: 'ok' } });
    mockTestProvider.mockResolvedValue({ status: 'connected' });
  });

  it('says not tested with the reason, and never Connection failed or Unreachable', async () => {
    mockTestClusterConnection.mockResolvedValue({
      success: false,
      reachable: false,
      stage: 'credentials',
      error_code: 'ERR_AUTH',
      error_message: awsReason,
      duration_ms: 3,
    });
    await runTestAndOpenDiagnostics();

    expect(screen.getByTestId('test-login-not-read').textContent).toBe(notRead);
    expect(screen.getByText(awsReason)).toBeInTheDocument();
    expect(screen.queryByText('Connection failed')).not.toBeInTheDocument();
    expect(screen.queryByText('Cluster unreachable')).not.toBeInTheDocument();
    expect(screen.queryAllByText(/unreachable/i)).toHaveLength(0);
  });

  it('still says Connection failed and Unreachable when the cluster did not answer', async () => {
    mockTestClusterConnection.mockResolvedValue({
      success: false,
      reachable: false,
      stage: 'connectivity',
      error_message: 'Sharko could not reach the cluster.',
      duration_ms: 3,
    });
    await runTestAndOpenDiagnostics();

    expect(screen.getByText('Connection failed')).toBeInTheDocument();
    expect(screen.getByText('Cluster unreachable')).toBeInTheDocument();
    expect(screen.queryByTestId('test-login-not-read')).not.toBeInTheDocument();
  });
});
