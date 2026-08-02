import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// S2 (walk day 5 ride-along) — a manual "Refresh page" button in the page
// header, on top of the existing 30s auto-poll. Distinct from the
// "Refresh" button inside the Managed cluster secret panel (that one only
// re-checks the ArgoCD secret via reconcileCluster — see
// ClusterDetail.syncnow.test.tsx); this one re-fetches the whole
// comparison AND the cluster's tracked PRs, the same two calls fetchData
// already makes on every load and on the 30s poll. Pins:
//  1. A "Refresh page" button renders in the page header.
//  2. Clicking it re-fetches both getClusterComparison and fetchTrackedPRs.
//  3. The button disables itself (and shows a spinner) while the refetch
//     is in flight, then re-enables once it resolves.

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

function baseComparisonResponse() {
  return {
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

describe('ClusterDetail — page-header Refresh button (S2, walk day 5)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
  });

  it('renders a "Refresh page" button in the page header', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: /Refresh page/ })).toBeInTheDocument();
  });

  it('clicking "Refresh page" re-fetches the comparison and the tracked PRs', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    const initialComparisonCalls = mockGetClusterComparison.mock.calls.length;
    const initialPRCalls = mockFetchTrackedPRs.mock.calls.length;

    fireEvent.click(screen.getByRole('button', { name: /Refresh page/ }));

    await waitFor(() => {
      expect(mockGetClusterComparison.mock.calls.length).toBeGreaterThan(initialComparisonCalls);
    });
    expect(mockFetchTrackedPRs.mock.calls.length).toBeGreaterThan(initialPRCalls);
    expect(mockFetchTrackedPRs).toHaveBeenLastCalledWith({ status: 'open', cluster: 'prod-eu' });
  });

  it('disables the button while the refresh is in flight, then re-enables it', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    let resolveRefetch: (() => void) | undefined;
    mockGetClusterComparison.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveRefetch = () => resolve(baseComparisonResponse());
        }),
    );

    const button = screen.getByRole('button', { name: /Refresh page/ });
    fireEvent.click(button);

    await waitFor(() => {
      expect(button).toBeDisabled();
    });

    resolveRefetch?.();

    await waitFor(() => {
      expect(button).not.toBeDisabled();
    });
  });
});
