import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';
import { SHARKO_CONN_TOOLTIP } from '@/components/WhoseConnectionLabel';

// V2-cleanup-55.3 — whose-connection attribution on the cluster detail page.
// The Test flow is Sharko's own connection to the cluster (Sharko fetches the
// credentials from the secret backend and connects directly), distinct from
// ArgoCD's connection (already labelled by the "ArgoCD Connection Failed" /
// "Status unknown" banners). These tests pin the Sharko-side labels on the
// Test button and the step-by-step test results.

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

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
      getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
      getNodeInfo: vi.fn().mockResolvedValue(null),
      getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
      getAIStatus: vi.fn().mockResolvedValue({ enabled: false }),
    },
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
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

describe('ClusterDetail — whose-connection labels (V2-cleanup-55.3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
  });

  it('gives the Test connection button a Sharko → cluster tooltip', async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    const testBtn = screen.getByRole('button', { name: 'Test connection' });
    expect(testBtn).toHaveAttribute('title', SHARKO_CONN_TOOLTIP);
  });

  it('labels step-by-step test results as Sharko → cluster', async () => {
    mockTestClusterConnection.mockResolvedValue({
      reachable: true,
      server_version: 'v1.29.3',
      steps: [
        { name: 'Fetch credentials from secret backend', status: 'pass' },
        { name: 'Connect to cluster API', status: 'pass' },
      ],
    });
    renderView();
    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }));

    await waitFor(() => {
      expect(screen.getByText('Connection test results (Sharko → cluster):')).toBeInTheDocument();
    });
    expect(screen.getByText('Connection test results (Sharko → cluster):')).toHaveAttribute(
      'title',
      SHARKO_CONN_TOOLTIP,
    );
  });
});
