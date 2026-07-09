import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';

// V2-cleanup-61.3 (B3): below the locked 5-cluster threshold, the Clusters
// page hides the 5 health stat cards and the full advanced-filter bar
// (name search, version/connection dropdowns, view toggle), and turns the
// always-visible status legend into an on-demand "Status legend"
// toggle. At >= 5 clusters everything reappears automatically. The
// register/add-cluster button stays visible regardless of cluster count.

const mockGetClusters = vi.fn();
const mockHealth = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    health: (...args: unknown[]) => mockHealth(...args),
  },
}));

function makeClusters(count: number) {
  return {
    clusters: Array.from({ length: count }, (_, i) => ({
      name: `cluster-${i + 1}`,
      labels: {},
      server_version: '1.28',
      connection_status: 'connected',
    })),
    health_stats: {
      total_in_git: count,
      connected: count,
      failed: 0,
      missing_from_argocd: 0,
      not_in_git: 0,
    },
  };
}

function renderView() {
  return render(
    <MemoryRouter>
      <ClustersOverview />
    </MemoryRouter>,
  );
}

describe('ClustersOverview — control collapse below 5 clusters (V2-cleanup-61.3, B3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockHealth.mockResolvedValue({
      status: 'healthy',
      version: 'test',
      cluster_test_available: true,
    });
  });

  it('hides the stat-card row and filter bar with 1 cluster', async () => {
    mockGetClusters.mockResolvedValue(makeClusters(1));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('cluster-1')).toBeInTheDocument();
    });

    expect(screen.queryByText('All Clusters')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('Filter by name...')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /ArgoCD Connection/ })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Grid view')).not.toBeInTheDocument();

    // Legend is on-demand instead of permanently visible.
    expect(screen.queryByText('Cluster Status:')).not.toBeInTheDocument();
    const legendToggle = screen.getByRole('button', { name: /status legend/i });
    expect(legendToggle).toBeInTheDocument();
    fireEvent.click(legendToggle);
    expect(screen.getByText('Cluster Status:')).toBeInTheDocument();
  });

  it('shows the stat-card row, filter bar, and automatic legend with 5 clusters', async () => {
    mockGetClusters.mockResolvedValue(makeClusters(5));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('cluster-1')).toBeInTheDocument();
    });

    expect(screen.getByText('All Clusters')).toBeInTheDocument();
    expect(screen.getByPlaceholderText('Filter by name...')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /ArgoCD Connection/ })).toBeInTheDocument();
    expect(screen.getByLabelText('Grid view')).toBeInTheDocument();

    // Legend renders automatically — no on-demand toggle.
    expect(screen.getByText('Cluster Status:')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /status legend/i })).not.toBeInTheDocument();
  });

  it('keeps the stat-card row and filter bar hidden with 4 clusters (just under threshold)', async () => {
    mockGetClusters.mockResolvedValue(makeClusters(4));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('cluster-1')).toBeInTheDocument();
    });

    expect(screen.queryByText('All Clusters')).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText('Filter by name...')).not.toBeInTheDocument();
  });
});
