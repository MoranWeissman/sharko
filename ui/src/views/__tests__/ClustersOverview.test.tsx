import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { StrictMode } from 'react';
import { ClustersOverview } from '@/views/ClustersOverview';

const mockNavigate = vi.fn();
const mockLocationState: { state?: Record<string, unknown> } = {};
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ state: mockLocationState.state }),
  };
});

const mockGetClusters = vi.fn();
const mockHealth = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    // BUG-041: ClustersOverview now fetches /api/v1/health on mount to read
    // the cluster_test_available capability flag. Default the mock to "true"
    // so existing tests keep observing the Test button enabled and do not
    // need to be rewritten.
    health: (...args: unknown[]) => mockHealth(...args),
    // V2-cleanup-89.6 kill switch — not under test here, keep the default.
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
}));

const clustersResponse = {
  clusters: [
    {
      name: 'prod-eu',
      labels: { env: 'prod', region: 'eu' },
      server_version: '1.28',
      connection_status: 'connected',
    },
    {
      name: 'staging-us',
      labels: { env: 'staging' },
      server_version: '1.27',
      connection_status: 'failed',
    },
    {
      name: 'in-cluster',
      labels: {},
      server_version: '1.28',
      connection_status: 'connected',
    },
  ],
  health_stats: {
    total_in_git: 2,
    connected: 2,
    failed: 1,
    missing_from_argocd: 0,
    not_in_git: 1,
  },
};

// V2-cleanup-61.3 (B3): the stat-card row + advanced filter bar are now
// hidden below 5 total clusters. `clustersResponse` above has only 3 —
// tests that specifically exercise the stat cards / filter bar need a
// fixture at or above the collapse threshold.
const clustersResponseAtThreshold = {
  clusters: [
    ...clustersResponse.clusters,
    {
      name: 'qa-cluster',
      labels: { env: 'qa' },
      server_version: '1.28',
      connection_status: 'connected',
    },
    {
      name: 'dev-cluster',
      labels: { env: 'dev' },
      server_version: '1.28',
      connection_status: 'connected',
    },
  ],
  health_stats: {
    total_in_git: 4,
    connected: 4,
    failed: 1,
    missing_from_argocd: 0,
    not_in_git: 1,
  },
};

function renderView() {
  return render(
    <MemoryRouter>
      <ClustersOverview />
    </MemoryRouter>,
  );
}

describe('ClustersOverview', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockLocationState.state = undefined;
    mockGetClusters.mockResolvedValue(clustersResponse);
    mockHealth.mockResolvedValue({
      status: 'healthy',
      version: 'test',
      cluster_test_available: true,
    });
  });

  it('renders loading state initially', () => {
    mockGetClusters.mockReturnValue(new Promise(() => {})); // never resolves
    renderView();
    expect(screen.getByText('Loading clusters...')).toBeInTheDocument();
  });

  it('renders error state on API failure', async () => {
    mockGetClusters.mockRejectedValue(new Error('Network error'));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Network error')).toBeInTheDocument();
    });
  });

  it('renders Try Again button on error and re-fetches when clicked (V124-2.3)', async () => {
    // First call fails, second call (the retry) succeeds.
    mockGetClusters.mockRejectedValueOnce(new Error('Boom'));
    mockGetClusters.mockResolvedValueOnce(clustersResponse);
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Boom')).toBeInTheDocument();
    });

    const retryBtn = screen.getByRole('button', { name: /try again/i });
    fireEvent.click(retryBtn);

    await waitFor(() => {
      // Successful retry surfaces the cluster list.
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });
    // The error message must be gone — not silently lingering.
    expect(screen.queryByText('Boom')).not.toBeInTheDocument();
    expect(mockGetClusters).toHaveBeenCalledTimes(2);
  });

  it('keeps prior data on screen when a background refresh fails (V124-2.3)', async () => {
    // Initial load succeeds.
    mockGetClusters.mockResolvedValueOnce(clustersResponse);
    // Background refresh (Refresh button click) fails.
    mockGetClusters.mockRejectedValueOnce(new Error('Transient 5xx'));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Trigger a background refresh via the Refresh button (same code path as
    // the 30s auto-refresh tick). The failed refresh must NOT wipe the
    // visible cluster list — that was the V124-2.3 blank-out bug.
    fireEvent.click(screen.getByTitle('Refresh'));

    await waitFor(() => {
      expect(mockGetClusters).toHaveBeenCalledTimes(2);
    });

    // Prior good data still on screen — no blank state, no ErrorState
    // takeover that would wipe the cluster table.
    expect(screen.getByText('prod-eu')).toBeInTheDocument();
    expect(screen.queryByText('Transient 5xx')).not.toBeInTheDocument();
  });

  it('renders clusters data with stat cards and table', async () => {
    // Stat cards only render at/above the 5-cluster collapse threshold
    // (V2-cleanup-61.3, B3) — use the at-threshold fixture.
    mockGetClusters.mockResolvedValue(clustersResponseAtThreshold);
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });

    // Stat cards — canonical connection vocabulary (V2-cleanup-61.2, D2):
    // Connected / Disconnected / Connecting / Not managed.
    expect(screen.getByText('All Clusters')).toBeInTheDocument();
    expect(screen.getAllByText('Connected').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Disconnected').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Connecting')).toBeInTheDocument();
    // "Not managed" appears in both the stat card and the legend.
    expect(screen.getAllByText('Not managed').length).toBeGreaterThanOrEqual(1);
    // The old competing names are gone.
    expect(screen.queryByText('Failed')).not.toBeInTheDocument();
    expect(screen.queryByText('Not Deployed')).not.toBeInTheDocument();
    expect(screen.queryByText('Unmanaged')).not.toBeInTheDocument();

    // Stat values - total = total_in_git + not_in_git = 5
    // Use getAllByText because '5' may appear in both the stat card and a count badge
    expect(screen.getAllByText('5').length).toBeGreaterThanOrEqual(1);

    // Table rows
    expect(screen.getByText('prod-eu')).toBeInTheDocument();
    expect(screen.getByText('staging-us')).toBeInTheDocument();
    expect(screen.getByText('in-cluster')).toBeInTheDocument();
  });

  it('filters clusters by name search', async () => {
    // The name-search input lives in the filter bar, hidden below the
    // 5-cluster collapse threshold (V2-cleanup-61.3, B3).
    mockGetClusters.mockResolvedValue(clustersResponseAtThreshold);
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    const searchInput = screen.getByPlaceholderText('Filter by name...');
    fireEvent.change(searchInput, { target: { value: 'prod' } });

    expect(screen.getByText('prod-eu')).toBeInTheDocument();
    expect(screen.queryByText('staging-us')).not.toBeInTheDocument();
  });

  it('navigates to cluster detail on row click', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText('prod-eu'));
    expect(mockNavigate).toHaveBeenCalledWith('/clusters/prod-eu');
  });

  it('does not navigate when clicking in-cluster row', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('in-cluster')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText('in-cluster'));
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('failed background refresh stays clean in StrictMode (V124-3.1)', async () => {
    // Regression guard for the React anti-pattern in fetchData's catch block.
    //
    // The pre-fix code called setError + setHealthStats from inside a
    //   setAllClusters((prev) => { ... return prev; })
    // updater. React 18+ StrictMode invokes setState updaters TWICE in dev to
    // surface impurity. With side effects inside the updater, this meant
    // setError + setHealthStats were dispatched twice per failed refresh and
    // would emit "Cannot update a component while rendering a different
    // component" / impure-updater warnings in dev.
    //
    // The fix moves the conditional state writes outside any updater. In
    // StrictMode the failed background refresh:
    //   1. Produces no React warnings about impure updaters or nested setState
    //   2. Leaves prior data on screen (no duplicated DOM, no blank state)
    //
    // A regression — re-introducing setError inside an updater — would
    // either trip a console warning (caught here) or render anomalously.
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    try {
      // Initial load(s) succeed so we have prior data on screen. StrictMode
      // double-mounts the fetch effect, so the initial mount may invoke
      // getClusters twice — both must resolve. We swap to a rejecting impl
      // only after the initial render is committed and we have prior data
      // visible (so the failed refresh hits the prior-data branch).
      mockGetClusters.mockResolvedValue(clustersResponse);

      render(
        <StrictMode>
          <MemoryRouter>
            <ClustersOverview />
          </MemoryRouter>
        </StrictMode>,
      );

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      // Now flip the mock so the next call (the background refresh) fails.
      mockGetClusters.mockReset();
      mockGetClusters.mockRejectedValue(new Error('Strict refresh fail'));

      // Reset the error spy after the initial mount so we only capture
      // warnings from the explicit background refresh below.
      errorSpy.mockClear();
      const callsBeforeRefresh = mockGetClusters.mock.calls.length;

      fireEvent.click(screen.getByTitle('Refresh'));
      await waitFor(() => {
        expect(mockGetClusters.mock.calls.length).toBe(callsBeforeRefresh + 1);
      });

      // 1. No React warnings from the failed refresh. The previous anti-pattern
      //    (state updates inside a setState updater) is exactly the kind of
      //    thing React surfaces in dev, and we explicitly want zero such
      //    warnings on the catch path.
      const reactWarnings = errorSpy.mock.calls.filter((args) => {
        const first = args[0];
        if (typeof first !== 'string') return false;
        return (
          first.includes('Warning:') ||
          first.includes('Cannot update a component') ||
          first.includes('act(')
        );
      });
      expect(reactWarnings).toEqual([]);

      // 2. Prior data still rendered exactly once (no duplicated rows from a
      //    re-invoked impure updater); error message NOT surfaced because we
      //    have prior data.
      expect(screen.getAllByText('prod-eu').length).toBe(1);
      expect(screen.queryByText('Strict refresh fail')).not.toBeInTheDocument();
    } finally {
      errorSpy.mockRestore();
    }
  });

  // BUG-040: the Dashboard's "N disconnected cluster(s)" link navigates to
  // /clusters?status=disconnected. The Clusters page MUST resolve that
  // deep-link to the same set of clusters the headline count covers — any
  // managed cluster that ArgoCD is not currently reporting as "Successful"
  // / "Connected" (failed + missing + unknown). Previously this routed to
  // a 'failed' filter which showed 0 rows whenever the disconnected
  // cluster's status was actually `missing` (not yet bootstrapped) or
  // `unknown` (ArgoCD has no record yet). That mismatch read as "count
  // says 1, list shows 0" which is the user-facing symptom of BUG-040.
  it('?status=disconnected filter resolves to all non-connected managed clusters (BUG-040)', async () => {
    const mixedDisconnected = {
      clusters: [
        {
          name: 'prod-eu',
          labels: { env: 'prod' },
          server_version: '1.28',
          connection_status: 'connected',
          managed: true,
        },
        {
          name: 'failing-cluster',
          labels: { env: 'staging' },
          server_version: '1.27',
          connection_status: 'failed',
          managed: true,
        },
        {
          name: 'missing-cluster',
          labels: { env: 'dev' },
          server_version: '1.28',
          connection_status: 'missing',
          managed: true,
        },
        {
          name: 'unknown-cluster',
          labels: { env: 'lab' },
          server_version: '1.28',
          connection_status: 'unknown',
          managed: true,
        },
        {
          name: 'discovered-cluster',
          labels: {},
          server_version: '1.28',
          connection_status: 'not_in_git',
          managed: false,
        },
      ],
      health_stats: {
        total_in_git: 4,
        connected: 1,
        failed: 1,
        missing_from_argocd: 1,
        not_in_git: 1,
      },
    };
    mockGetClusters.mockResolvedValue(mixedDisconnected);

    render(
      <MemoryRouter initialEntries={["/clusters?status=disconnected"]}>
        <ClustersOverview />
      </MemoryRouter>,
    );

    // Wait for the row of any of the 3 disconnected clusters to show.
    await waitFor(() => {
      expect(screen.getByText('failing-cluster')).toBeInTheDocument();
    });

    // All 3 disconnected managed clusters must appear under the deep-link.
    expect(screen.getByText('failing-cluster')).toBeInTheDocument();
    expect(screen.getByText('missing-cluster')).toBeInTheDocument();
    expect(screen.getByText('unknown-cluster')).toBeInTheDocument();

    // Connected and discovered/unmanaged clusters must NOT appear.
    expect(screen.queryByText('prod-eu')).not.toBeInTheDocument();
    expect(screen.queryByText('discovered-cluster')).not.toBeInTheDocument();
  });

  // BUG-041: Test button on each cluster row must be disabled (with a
  // tooltip pointing at Settings → Connections) when /api/v1/health
  // reports cluster_test_available=false. That happens whenever no
  // secrets backend (Vault / AWS Secrets Manager / file-store /
  // ArgoCDProvider auto-default) is configured on the active connection
  // — typically the `--demo` dev path. Previously the button was always
  // enabled, the user clicked it, and the test endpoint returned 503 +
  // error_code=no_secrets_backend — confusing UX.
  it('disables Test button when health reports cluster_test_available=false (BUG-041)', async () => {
    mockHealth.mockResolvedValue({
      status: 'healthy',
      version: 'test',
      cluster_test_available: false,
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Wait for the /health fetch to resolve and the gate to flip.
    await waitFor(() => {
      const testButtons = screen.getAllByRole('button', { name: /no secrets backend/i });
      expect(testButtons.length).toBeGreaterThanOrEqual(1);
    });

    const testButtons = screen.getAllByRole('button', { name: /no secrets backend/i });
    for (const btn of testButtons) {
      expect(btn).toBeDisabled();
      // The aria-label / title both contain the explanatory tooltip copy.
      const tooltipSource = btn.getAttribute('title') ?? btn.getAttribute('aria-label') ?? '';
      expect(tooltipSource).toMatch(/secrets backend/i);
      expect(tooltipSource).toMatch(/Settings\s*→\s*Connections/);
    }
  });

  // BUG-041 (paired): the default-enabled path must remain enabled when
  // /health reports cluster_test_available=true so existing flows work.
  it('keeps Test button enabled when cluster_test_available=true (BUG-041)', async () => {
    // Default beforeEach mock already returns true; just confirm.
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Allow /health fetch to resolve.
    await waitFor(() => {
      expect(mockHealth).toHaveBeenCalled();
    });

    // After health resolves with true, no Test connection button is disabled
    // by the gate. (it may still be disabled while testing=true, but no test
    // is in flight.)
    const testButtons = screen.getAllByRole('button', { name: /^Test connection$/ });
    expect(testButtons.length).toBeGreaterThanOrEqual(1);
    for (const btn of testButtons) {
      expect(btn).not.toBeDisabled();
    }
  });

  it('toggles status filter on stat card click', async () => {
    // Stat cards only render at/above the 5-cluster collapse threshold
    // (V2-cleanup-61.3, B3).
    mockGetClusters.mockResolvedValue(clustersResponseAtThreshold);
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Click the "Disconnected" stat card to filter - find the one inside a
    // stat card (role=button); the composite row pill also says
    // "Disconnected" but is a plain <button> without the role attribute.
    const disconnectedCards = screen.getAllByText('Disconnected');
    const disconnectedStatCard = disconnectedCards
      .map((el) => el.closest('[role="button"]'))
      .find(Boolean);
    expect(disconnectedStatCard).toBeTruthy();
    fireEvent.click(disconnectedStatCard!);

    // Only the failed cluster should remain
    expect(screen.queryByText('prod-eu')).not.toBeInTheDocument();
    expect(screen.getByText('staging-us')).toBeInTheDocument();
  });

  // V3-D5: cluster removal PR note carried via router state after a successful
  // removal — shows as a dismissible banner, clears state so refresh drops it.
  describe('V3-D5: removal PR note from router state', () => {
    beforeEach(() => {
      mockGetClusters.mockResolvedValue(clustersResponse);
      mockHealth.mockResolvedValue({ cluster_test_available: true });
    });

    it('renders dismissible note when removalPR state is present', async () => {
      mockLocationState.state = {
        removalPR: {
          cluster: 'prod-eu',
          pr_url: 'https://github.com/example/repo/pull/42',
          pr_id: 42,
          merged: false,
        },
      };

      renderView();

      await waitFor(() => {
        expect(screen.getByText(/Removal PR opened for "prod-eu"/i)).toBeInTheDocument();
        expect(screen.getByRole('link', { name: /View PR #42 on GitHub/i })).toHaveAttribute(
          'href',
          'https://github.com/example/repo/pull/42',
        );
      });
    });

    it('clears router state after reading removalPR', async () => {
      mockLocationState.state = {
        removalPR: {
          cluster: 'prod-eu',
          pr_url: 'https://github.com/example/repo/pull/42',
          pr_id: 42,
          merged: false,
        },
      };

      renderView();

      await waitFor(() => {
        expect(mockNavigate).toHaveBeenCalledWith('.', { replace: true, state: {} });
      });
    });

    it('dismissing the note hides it', async () => {
      mockLocationState.state = {
        removalPR: {
          cluster: 'prod-eu',
          pr_url: 'https://github.com/example/repo/pull/42',
          pr_id: 42,
          merged: false,
        },
      };

      renderView();

      await waitFor(() => {
        expect(screen.getByText(/Removal PR opened for "prod-eu"/i)).toBeInTheDocument();
      });

      const dismissButton = screen.getByLabelText('Dismiss');
      fireEvent.click(dismissButton);

      await waitFor(() => {
        expect(screen.queryByText(/Removal PR opened for "prod-eu"/i)).not.toBeInTheDocument();
      });
    });

    it('does not render note when no removalPR state', async () => {
      mockLocationState.state = {};
      renderView();

      await waitFor(() => {
        expect(screen.getByText('prod-eu')).toBeInTheDocument();
      });

      expect(screen.queryByText(/Removal PR opened/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/removed/i)).not.toBeInTheDocument();
    });

    it('shows merged message when merged=true', async () => {
      mockLocationState.state = {
        removalPR: {
          cluster: 'prod-eu',
          pr_url: 'https://github.com/example/repo/pull/42',
          pr_id: 42,
          merged: true,
        },
      };

      renderView();

      await waitFor(() => {
        expect(screen.getByText(/Cluster "prod-eu" removed/i)).toBeInTheDocument();
      });
    });
  });
});
