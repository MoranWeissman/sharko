import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// HD1 (V3) — "Sync now" button redesign + sync-status pill. Moved to
// primary action styling in the header. Pins:
//  1. "Sync now" renders as a prominent primary button next to Test connection.
//  2. A status pill shows the reconcile outcome (In sync / Sync failed / Reconciling… / Not synced yet).
//  3. Clicking Sync now calls POST /clusters/{name}/reconcile for THIS cluster.
//  4. The "Last sync" line still renders relative time + outcome (kept from V2-cleanup-89.4).

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

describe('ClusterDetail — sync now primary + status pill (HD1, V3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockReconcileCluster.mockResolvedValue({ status: 'accepted', message: 'reconcile triggered for cluster prod-eu' });
  });

  it('renders "Sync now" as a primary button next to Test connection', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByRole('button', { name: /^Test connection$/ })).toBeInTheDocument();
    const syncBtn = screen.getByRole('button', { name: /^Sync now$/ });
    expect(syncBtn).toBeInTheDocument();
    expect(syncBtn).toHaveClass('bg-teal-600'); // Primary button style
  });

  it('renders "Not synced yet" status pill when last_reconcile is absent', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText('Not synced yet')).toBeInTheDocument();
  });

  it('renders "In sync" status pill when last_reconcile outcome is succeeded', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({ time: new Date(Date.now() - 5 * 60 * 1000).toISOString(), outcome: 'succeeded' }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText('In sync')).toBeInTheDocument();
  });

  it('renders "Sync failed" status pill when last_reconcile outcome is failed', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        time: new Date(Date.now() - 2 * 60 * 1000).toISOString(),
        outcome: 'failed',
        message: 'Connection failed',
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText('Sync failed')).toBeInTheDocument();
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

  // V2-cleanup-90.4 (L1) — the single blind 1.5s refetch became a bounded
  // poll: up to 4 refetches, 2s apart, stopping early once
  // last_reconcile.time changes. The timer is tracked in a ref so it can
  // be cleared on unmount, and the spinner stays lit through the FIRST
  // refetch landing, not just until the 202 is accepted.
  describe('bounded sync-now poll (V2-cleanup-90.4)', () => {
    it('keeps the spinner lit until the first refetch lands, not just until the 202 is accepted', async () => {
      mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
      renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        const syncBtn = screen.getByRole('button', { name: /^Sync now$/ });
        fireEvent.click(syncBtn);

        await waitFor(() => {
          expect(mockReconcileCluster).toHaveBeenCalledWith('prod-eu');
        });
        // The 202 has been accepted (reconcileCluster resolved), but the
        // first scheduled refetch hasn't fired yet — the spinner must
        // still be lit.
        expect(syncBtn).toBeDisabled();

        // Advance to the first scheduled refetch (2s) and let it resolve.
        await act(async () => {
          vi.advanceTimersByTime(2000);
          await Promise.resolve();
        });

        await waitFor(() => {
          expect(syncBtn).not.toBeDisabled();
        });
      } finally {
        vi.useRealTimers();
      }
    });

    it('stops polling early once last_reconcile.time changes from the pre-click value', async () => {
      const initialTime = new Date(Date.now() - 5 * 60 * 1000).toISOString();
      mockGetClusterComparison.mockResolvedValue(
        baseComparisonResponse({ time: initialTime, outcome: 'succeeded' }),
      );
      renderView();
      await waitFor(() => {
        expect(screen.getByText(/^Last sync:/)).toBeInTheDocument();
      });

      vi.useFakeTimers({ shouldAdvanceTime: true });
      try {
        fireEvent.click(screen.getByRole('button', { name: /^Sync now$/ }));
        await waitFor(() => {
          expect(mockReconcileCluster).toHaveBeenCalledWith('prod-eu');
        });

        const callsBeforePoll = mockGetClusterComparison.mock.calls.length;

        // Every refetch from here on returns a changed time — only ONE
        // refetch should happen even though up to 4 are allowed.
        const newTime = new Date().toISOString();
        mockGetClusterComparison.mockResolvedValue(
          baseComparisonResponse({ time: newTime, outcome: 'succeeded' }),
        );

        // First scheduled refetch (2s) picks up the changed time and stops.
        await act(async () => {
          vi.advanceTimersByTime(2000);
          await Promise.resolve();
        });
        await waitFor(() => {
          expect(mockGetClusterComparison.mock.calls.length).toBe(callsBeforePoll + 1);
        });

        // Advance well past all 4 possible attempts (8s total from the
        // first poll) — no further refetches should fire.
        await act(async () => {
          vi.advanceTimersByTime(10_000);
          await Promise.resolve();
        });
        expect(mockGetClusterComparison.mock.calls.length).toBe(callsBeforePoll + 1);
      } finally {
        vi.useRealTimers();
      }
    });

    it('clears the pending refetch timer on unmount — no further refetch and no setState-after-unmount error', async () => {
      mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
      const { unmount } = renderView();
      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      vi.useFakeTimers({ shouldAdvanceTime: true });
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
      try {
        fireEvent.click(screen.getByRole('button', { name: /^Sync now$/ }));
        await waitFor(() => {
          expect(mockReconcileCluster).toHaveBeenCalledWith('prod-eu');
        });

        const callsBeforeUnmount = mockGetClusterComparison.mock.calls.length;
        unmount();

        // Advance well past the scheduled refetch — since the pending
        // timer was cleared on unmount, it must never fire.
        await act(async () => {
          vi.advanceTimersByTime(10_000);
          await Promise.resolve();
        });
        expect(mockGetClusterComparison.mock.calls.length).toBe(callsBeforeUnmount);
        expect(consoleError).not.toHaveBeenCalled();
      } finally {
        consoleError.mockRestore();
        vi.useRealTimers();
      }
    });
  });
});
