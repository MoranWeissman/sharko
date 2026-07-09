import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-88.5 — "Run connection doctor" button on the cluster detail
// page, next to Test connection / Check permissions. The doctor's own
// check-rendering (all three statuses + fix line) is pinned in
// DoctorModal.test.tsx; this file pins the integration: the button exists
// in the right place, is wired to the right cluster name, and opens the
// modal which then renders real check data end-to-end.

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

describe('ClusterDetail — connection doctor button (V2-cleanup-88.5)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
  });

  it('renders "Run connection doctor" next to Test connection and Check permissions', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: /^Test connection$/ })).toBeInTheDocument();
    const doctorButton = screen.getByTestId('run-connection-doctor');
    expect(doctorButton).toBeInTheDocument();
    expect(doctorButton).toHaveTextContent('Run connection doctor');
  });

  it('opens the doctor modal for this cluster and renders real check data on click', async () => {
    mockDoctorCluster.mockResolvedValue({
      overall: 'pass',
      checks: [
        { id: 'connection-credentials', status: 'pass', detail: 'Sharko read connection credentials for cluster "prod-eu".' },
        { id: 'addon-secret-paths', status: 'pass', detail: 'All addon secret paths resolved.' },
        { id: 'assume-role', status: 'not-applicable', detail: 'No cross-account role is configured for this cluster.' },
        { id: 'cluster-access', status: 'pass', detail: 'Sharko created, read, and deleted a canary secret on the cluster.' },
      ],
    });

    renderView();
    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(mockDoctorCluster).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId('run-connection-doctor'));

    await waitFor(() => {
      expect(mockDoctorCluster).toHaveBeenCalledWith('prod-eu');
    });
    await waitFor(() => {
      expect(screen.getByText('Run connection doctor: prod-eu')).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.getByText('All checks passed — this cluster’s connection looks healthy.')).toBeInTheDocument();
    });
  });
});
