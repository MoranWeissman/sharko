import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-89.4 — "Last sync" line + "Sync now" button on the cluster
// detail page. Before this, a failed reconcile for a cluster's ArgoCD
// secret was server-log-only; ArgoCD shows a failed apply, Sharko showed
// nothing. Pins:
//  1. "Sync now" renders next to Test connection / Run connection doctor.
//  2. Clicking it calls POST /clusters/{name}/reconcile for THIS cluster.
//  3. The "Last sync" line renders relative time + outcome.
//  4. A failed outcome renders the plain-English message.

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
const mockReconcileCluster = vi.fn();

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
    reconcileCluster: (...args: unknown[]) => mockReconcileCluster(...args),
  };
});

function baseComparisonResponse(lastReconcile?: {
  time: string;
  outcome: 'succeeded' | 'failed' | 'skipped';
  message?: string;
}) {
  return {
    cluster: {
      name: 'prod-eu',
      labels: { env: 'prod' },
      server_version: '1.28',
      connection_status: 'connected',
      addon_secrets_ready: true,
      ...(lastReconcile ? { last_reconcile: lastReconcile } : {}),
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

describe('ClusterDetail — last sync + sync now (V2-cleanup-89.4)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockReconcileCluster.mockResolvedValue({ status: 'accepted', message: 'reconcile triggered for cluster prod-eu' });
  });

  it('renders "Sync now" next to Test connection and Run connection doctor', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: /^Test connection$/ })).toBeInTheDocument();
    expect(screen.getByTestId('run-connection-doctor')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Sync now$/ })).toBeInTheDocument();
  });

  it('clicking "Sync now" triggers a reconcile for this cluster', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(mockReconcileCluster).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: /^Sync now$/ }));

    await waitFor(() => {
      expect(mockReconcileCluster).toHaveBeenCalledWith('prod-eu');
    });
  });

  it('does not render a "Last sync" line when last_reconcile is absent', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.queryByText(/^Last sync:/)).not.toBeInTheDocument();
  });

  it('renders a succeeded "Last sync" line with a relative time', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({ time: new Date(Date.now() - 5 * 60 * 1000).toISOString(), outcome: 'succeeded' }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText(/^Last sync:/)).toBeInTheDocument();
    });
    expect(screen.getByText(/Last sync:.*succeeded/)).toBeInTheDocument();
  });

  it('renders a failed outcome with its plain-English message', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        time: new Date(Date.now() - 2 * 60 * 1000).toISOString(),
        outcome: 'failed',
        message: "Sharko couldn't fetch this cluster's credentials from the secrets backend: simulated vault outage",
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText(/^Last sync:/)).toBeInTheDocument();
    });
    // The "— failed" outcome text renders inside its own (red-styled) span,
    // so it's checked as a separate node rather than concatenated with the
    // "Last sync:" text — Testing Library only matches a node's own direct
    // text children, not nested elements' text.
    expect(screen.getByText(/—\s*failed/)).toBeInTheDocument();
    expect(
      screen.getByText("Sharko couldn't fetch this cluster's credentials from the secrets backend: simulated vault outage"),
    ).toBeInTheDocument();
  });
});
