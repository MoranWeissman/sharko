import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';

// S4: the server paginates GET /api/v1/clusters (default per_page=20, max
// 100) and the UI used to ignore that entirely — getClusters() passed no
// params and fetchJSON never read the X-Total-Count header, so at 50+
// clusters only the first 20 were ever visible with no indication anything
// was cut. These tests exercise the fix: api.getClusters() now requests
// per_page=100 (covering the 50-cluster demo preset in one call), and
// api.getClustersPage() is used for the rare overflow beyond 100.

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useLocation: () => ({ state: undefined }),
  };
});

const mockGetClusters = vi.fn();
const mockGetClustersPage = vi.fn();
const mockHealth = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getClustersPage: (...args: unknown[]) => mockGetClustersPage(...args),
    health: (...args: unknown[]) => mockHealth(...args),
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
}));

function makeCluster(name: string) {
  return {
    name,
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
  };
}

function makeClusters(count: number, prefix = 'cluster') {
  return Array.from({ length: count }, (_, i) => makeCluster(`${prefix}-${i + 1}`));
}

function renderView() {
  return render(
    <MemoryRouter>
      <ClustersOverview />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockHealth.mockResolvedValue({
    status: 'healthy',
    version: 'test',
    cluster_test_available: true,
  });
});

describe('ClustersOverview pagination (S4)', () => {
  it('shows every cluster from a 50-cluster response with no "load more" control', async () => {
    const clusters = makeClusters(50);
    mockGetClusters.mockResolvedValue({
      clusters,
      health_stats: {
        total_in_git: 50,
        connected: 50,
        failed: 0,
        missing_from_argocd: 0,
        not_in_git: 0,
      },
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('cluster-1')).toBeInTheDocument();
    });
    // Every one of the 50 clusters must be reachable — not silently
    // truncated the way the pre-fix default per_page=20 would.
    for (const name of ['cluster-1', 'cluster-25', 'cluster-50']) {
      expect(screen.getByText(name)).toBeInTheDocument();
    }
    expect(screen.queryByTestId('clusters-load-more')).not.toBeInTheDocument();
    // A single per_page=100 call is enough — no continuation page needed.
    expect(mockGetClustersPage).not.toHaveBeenCalled();
  });

  it('shows no "load more" control for a small (<=20) cluster list — today\'s look unchanged', async () => {
    const clusters = makeClusters(12);
    mockGetClusters.mockResolvedValue({
      clusters,
      health_stats: {
        total_in_git: 12,
        connected: 12,
        failed: 0,
        missing_from_argocd: 0,
        not_in_git: 0,
      },
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('cluster-1')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('clusters-load-more')).not.toBeInTheDocument();
    expect(mockGetClustersPage).not.toHaveBeenCalled();
  });

  it('offers "load more" when the first page comes back completely full, and loads the rest on click', async () => {
    // A full CLUSTERS_PAGE_SIZE (100) page is the signal there may be more
    // beyond what api.getClusters() alone can see.
    const firstPage = makeClusters(100, 'full');
    const secondPage = makeClusters(20, 'extra');
    mockGetClusters.mockResolvedValue({
      clusters: firstPage,
      health_stats: {
        total_in_git: 120,
        connected: 120,
        failed: 0,
        missing_from_argocd: 0,
        not_in_git: 0,
      },
    });
    mockGetClustersPage.mockResolvedValue({
      data: { clusters: secondPage },
      total: 120,
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('full-1')).toBeInTheDocument();
    });

    const loadMoreBtn = await screen.findByTestId('clusters-load-more');
    expect(loadMoreBtn).toBeInTheDocument();
    expect(screen.queryByText('extra-1')).not.toBeInTheDocument();

    fireEvent.click(loadMoreBtn);

    await waitFor(() => {
      expect(screen.getByText('extra-1')).toBeInTheDocument();
    });
    expect(mockGetClustersPage).toHaveBeenCalledWith(2, 100);
    // All 120 now loaded, matching the confirmed total -> control disappears.
    await waitFor(() => {
      expect(screen.queryByTestId('clusters-load-more')).not.toBeInTheDocument();
    });
  });
});
