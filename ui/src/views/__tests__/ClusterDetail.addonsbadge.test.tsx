import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// Walk 2, finding 7: a cluster with no add-ons showed "Addons 1" in the side
// menu (it counted Sharko's own connectivity check) while the card on the same
// page said 0 All Addons. The menu number is now the card's number.

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
      addon_name: 'connectivity-check',
      git_configured: false,
      git_enabled: false,
      has_version_override: false,
      argocd_deployed: true,
      argocd_health_status: 'Healthy',
      status: 'healthy',
      issues: [],
    },
  ],
  total_healthy: 0,
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


describe('ClusterDetail — Addons number in the side menu', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
  });

  it('says 0 when the cluster has no add-ons, the same as the All Addons card', async () => {
    renderView();
    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    const nav = screen.getByRole('button', { name: /^Addons/ });
    expect(nav.textContent).toBe('Addons0');
    expect(nav.textContent).not.toBe('Addons1');
  });
});
