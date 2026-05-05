import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const mockGetClusters = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
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
    mockGetClusters.mockResolvedValue(clustersResponse);
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
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });

    // Stat cards
    expect(screen.getByText('All Clusters')).toBeInTheDocument();
    expect(screen.getAllByText('Connected').length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText('Failed').length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText('Not Deployed')).toBeInTheDocument();
    expect(screen.getByText('Unmanaged')).toBeInTheDocument();

    // Stat values - total = total_in_git + not_in_git = 3
    // Use getAllByText because '3' may appear in both the stat card and a count badge
    expect(screen.getAllByText('3').length).toBeGreaterThanOrEqual(1);

    // Table rows
    expect(screen.getByText('prod-eu')).toBeInTheDocument();
    expect(screen.getByText('staging-us')).toBeInTheDocument();
    expect(screen.getByText('in-cluster')).toBeInTheDocument();
  });

  it('filters clusters by name search', async () => {
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

  it('toggles status filter on stat card click', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Click the "Failed" stat card to filter - find the one inside a stat card (role=button)
    const failedCards = screen.getAllByText('Failed');
    const failedStatCard = failedCards
      .map((el) => el.closest('[role="button"]'))
      .find(Boolean);
    expect(failedStatCard).toBeTruthy();
    fireEvent.click(failedStatCard!);

    // Only the failed cluster should remain
    expect(screen.queryByText('prod-eu')).not.toBeInTheDocument();
    expect(screen.getByText('staging-us')).toBeInTheDocument();
  });
});
