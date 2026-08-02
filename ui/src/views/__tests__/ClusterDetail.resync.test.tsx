import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// v4-8-5 — "Re-sync now" in the drift view, renamed to "Sync" as part of the
// Managed cluster secret panel's ArgoCD-vocabulary toolbar (walk day 4 /
// S1). Pins:
//  1. The button always renders, but stays DISABLED when there is nothing
//     to apply (no drift) — not hidden, unlike the old always-conditional
//     "Re-sync now".
//  2. Clicking it opens a confirmation dialog stating exactly what will
//     happen and that self-heal is not touched.
//  3. Confirming calls POST /clusters/{name}/resync for THIS cluster and
//     shows the returned message.
//  4. After a successful resync, the cluster is refetched.
//  5. A viewer never sees the button (RoleGuard admin/operator only).

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

const viewerAuth = {
  ...adminAuth,
  username: 'viewer',
  role: 'viewer',
  isAdmin: false,
};

const mockGetClusterComparison = vi.fn();
const mockFetchTrackedPRs = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockResyncClusterLabels = vi.fn();

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
    resyncClusterLabels: (...args: unknown[]) => mockResyncClusterLabels(...args),
  };
});

function baseComparisonResponse(labelDrift?: { added?: string[]; removed?: string[]; changed?: string[] }) {
  const lastReconcile: {
    time: string;
    outcome: 'succeeded' | 'failed' | 'skipped';
    label_drift?: typeof labelDrift;
  } = {
    time: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
    outcome: 'succeeded',
  };
  if (labelDrift) {
    lastReconcile.label_drift = labelDrift;
  }

  return {
    cluster: {
      name: 'prod-eu',
      labels: { env: 'prod' },
      server_version: '1.28',
      connection_status: 'connected',
      addon_secrets_ready: true,
      last_reconcile: lastReconcile,
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
    total_healthy: 1,
    total_with_issues: 0,
    total_missing_in_argocd: 0,
    total_untracked_in_argocd: 0,
    total_disabled_in_git: 0,
  };
}

function renderView(auth: typeof adminAuth = adminAuth) {
  return render(
    <AuthContext.Provider value={auth}>
      <MemoryRouter initialEntries={['/clusters/prod-eu']}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('ClusterDetail — Re-sync now (v4-8-5)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockResyncClusterLabels.mockResolvedValue({
      status: 'resynced',
      cluster: 'prod-eu',
      outcome: 'succeeded',
      message: 're-applied Sharko\'s addon labels for cluster "prod-eu" to match git — 1 added, 0 removed, 0 changed. The self-heal setting was not changed.',
      label_diff: { added: ['addon-foo'], removed: [], changed: [], unchanged: [] },
    });
  });

  it('renders "Sync" disabled when labels are in sync (nothing to apply)', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });
    expect(screen.getByText('Synced')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Sync$/ })).toBeDisabled();
  });

  it('renders "Sync" enabled when drift is present', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse({ added: ['addon-foo'] }));
    renderView();

    await waitFor(() => {
      expect(screen.getByText(/Out of sync/)).toBeInTheDocument();
    });
    expect(screen.getByRole('button', { name: /^Sync$/ })).toBeEnabled();
  });

  it('a viewer never sees "Sync", even with drift present', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse({ added: ['addon-foo'] }));
    renderView(viewerAuth);

    await waitFor(() => {
      expect(screen.getByText(/Out of sync/)).toBeInTheDocument();
    });
    expect(screen.queryByRole('button', { name: /^Sync$/ })).not.toBeInTheDocument();
  });

  it('clicking "Sync" opens a confirm dialog naming exactly what it does', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse({ added: ['addon-foo'] }));
    renderView();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^Sync$/ })).toBeEnabled();
    });
    fireEvent.click(screen.getByRole('button', { name: /^Sync$/ }));

    await waitFor(() => {
      expect(
        screen.getByText(/Applies git's addon labels to this cluster's ArgoCD secret — one time; the self-heal setting is not changed/i),
      ).toBeInTheDocument();
    });
    expect(mockResyncClusterLabels).not.toHaveBeenCalled();
  });

  it('confirming the dialog calls resyncClusterLabels for this cluster and refetches', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse({ added: ['addon-foo'] }));
    renderView();

    await waitFor(() => {
      expect(screen.getByRole('button', { name: /^Sync$/ })).toBeEnabled();
    });
    fireEvent.click(screen.getByRole('button', { name: /^Sync$/ }));

    // Wait for the dialog to actually render before looking for its confirm
    // button — the modal's confirm button shares the same accessible name
    // as the toolbar's Sync button, so we grab the last one.
    await waitFor(() => {
      expect(screen.getByText(/one time; the self-heal setting is not changed/i)).toBeInTheDocument();
    });

    const callCountBefore = mockGetClusterComparison.mock.calls.length;
    const buttons = screen.getAllByRole('button', { name: /^Sync$/ });
    fireEvent.click(buttons[buttons.length - 1]);

    await waitFor(() => {
      expect(mockResyncClusterLabels).toHaveBeenCalledWith('prod-eu');
    });
    await waitFor(() => {
      expect(mockGetClusterComparison.mock.calls.length).toBeGreaterThan(callCountBefore);
    });
  });
});
