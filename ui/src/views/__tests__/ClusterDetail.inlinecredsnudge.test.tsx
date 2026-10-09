import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-89.6 — migrate nudge on ClusterDetail. When a cluster's
// managed-clusters.yaml entry records creds_source: inline-kubeconfig
// (mirrored onto the read model as Cluster.creds_source), Sharko shows a
// light nudge toward migrating to a secret-store pointer. Clusters
// registered any other way (or predating the field) show nothing.

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

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
      getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
      getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
      getAIStatus: vi.fn().mockResolvedValue({ enabled: false }),
      getClusterHistory: vi.fn().mockResolvedValue({ history: [] }),
      getClusterChanges: vi.fn().mockResolvedValue({ changes: [] }),
    },
    fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
  };
});

function baseComparisonResponse(credsSource?: string) {
  return {
    cluster: {
      name: 'prod-eu',
      labels: { env: 'prod' },
      server_version: '1.28',
      connection_status: 'connected',
      addon_secrets_ready: true,
      ...(credsSource ? { creds_source: credsSource } : {}),
    },
    git_total_addons: 1,
    git_enabled_addons: 1,
    git_disabled_addons: 0,
    argocd_total_applications: 1,
    argocd_healthy_applications: 1,
    argocd_synced_applications: 1,
    argocd_degraded_applications: 0,
    argocd_out_of_sync_applications: 0,
    addon_comparisons: [],
    total_healthy: 0,
    total_with_issues: 0,
    total_missing_in_argocd: 0,
    total_untracked_in_argocd: 0,
    total_disabled_in_git: 0,
  };
}

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

describe('ClusterDetail — inline-credentials migrate nudge (V2-cleanup-89.6)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
  });

  it('shows the migrate nudge when creds_source is inline-kubeconfig', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse('inline-kubeconfig'));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(
      screen.getByText(/Registered with pasted credentials — consider migrating to a secret-store pointer\./),
    ).toBeInTheDocument();
  });

  it('does not show the nudge for a secret-kubeconfig cluster', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse('secret-kubeconfig'));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.queryByText(/consider migrating to a secret-store pointer/)).not.toBeInTheDocument();
  });

  it('does not show the nudge when creds_source is absent (record predates the field)', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.queryByText(/consider migrating to a secret-store pointer/)).not.toBeInTheDocument();
  });
});
