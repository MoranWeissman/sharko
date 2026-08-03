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
//
// S1 (maintainer's 50-cluster walk, day 7): on top of that server-side
// fetch, the managed clusters list now paginates client-side too (default
// page size 20). The tests below that used to assert every fetched
// cluster was simultaneously ON SCREEN now drive the page-size selector /
// Next button first — the fetch-layer behaviour they exist to pin
// (everything really got fetched, "Load more" fires the right call) is
// unchanged.

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

function makeCluster(name: string, overrides: Record<string, unknown> = {}) {
  return {
    name,
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
    ...overrides,
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

describe('ClustersOverview server-side fetch (S4)', () => {
  it('fetches every cluster from a 50-cluster response with no "load more" control, reachable via client paging', async () => {
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
    // Nothing was cut server-side — no "load more" and no second fetch.
    expect(screen.queryByTestId('clusters-load-more')).not.toBeInTheDocument();
    expect(mockGetClustersPage).not.toHaveBeenCalled();

    // All 50 are actually in memory — bump the page size to 100 (client
    // pagination default is 20) to bring every one of them onto one page.
    fireEvent.click(screen.getByRole('button', { name: 'Show 100 per page' }));
    for (const name of ['cluster-1', 'cluster-25', 'cluster-50']) {
      expect(screen.getByText(name)).toBeInTheDocument();
    }
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
    // 12 clusters fit in one 20-per-page client page — page controls
    // stay hidden (nothing to page through).
    expect(screen.getByText('cluster-12')).toBeInTheDocument();
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

    fireEvent.click(loadMoreBtn);

    await waitFor(() => {
      expect(mockGetClustersPage).toHaveBeenCalledWith(2, 100);
    });
    // All 120 now loaded (fetch-side), matching the confirmed total ->
    // "load more" control disappears.
    await waitFor(() => {
      expect(screen.queryByTestId('clusters-load-more')).not.toBeInTheDocument();
    });

    // extra-1 is in memory now, but client pagination (default 20/page)
    // still gates what's on screen — bump the page size to see it.
    fireEvent.click(screen.getByRole('button', { name: 'Show 100 per page' }));
    expect(screen.getByText('extra-1')).toBeInTheDocument();
  });
});

describe('ClustersOverview client-side pagination + sort (S1)', () => {
  it('pages the managed clusters list at the 20-per-page default, and Next reaches later clusters', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: makeClusters(50),
      health_stats: { total_in_git: 50, connected: 50, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    renderView();

    await waitFor(() => expect(screen.getByText('cluster-1')).toBeInTheDocument());

    // Default sort is Name A→Z; cluster-1..cluster-9 sort after
    // cluster-10..cluster-19 lexicographically, so just assert on the page
    // count / navigation mechanics rather than exact membership.
    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument();
    expect(screen.queryByText('cluster-50')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => {
      expect(screen.getByText('Page 2 of 3')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => {
      expect(screen.getByText('Page 3 of 3')).toBeInTheDocument();
    });
    // Next is disabled on the last page.
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled();
  });

  it('switching page size recomputes total pages and resets to page 1', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: makeClusters(50),
      health_stats: { total_in_git: 50, connected: 50, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    renderView();

    await waitFor(() => expect(screen.getByText('cluster-1')).toBeInTheDocument());
    expect(screen.getByText('Page 1 of 3')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => expect(screen.getByText('Page 2 of 3')).toBeInTheDocument());

    // Switching page size to 5 must both recompute total pages AND reset
    // back to page 1 — staying on "page 2" against a 5-per-page split
    // would show the wrong slice.
    fireEvent.click(screen.getByRole('button', { name: 'Show 5 per page' }));
    await waitFor(() => {
      expect(screen.getByText('Page 1 of 10')).toBeInTheDocument();
    });
  });

  it('resets to page 1 when the name search narrows the result set', async () => {
    // 30 clusters named cluster-1..30, plus one "special-one" that alone
    // matches a later search.
    mockGetClusters.mockResolvedValue({
      clusters: [...makeClusters(30), makeCluster('special-one')],
      health_stats: { total_in_git: 31, connected: 31, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    renderView();

    await waitFor(() => expect(screen.getByText('cluster-1')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await waitFor(() => {
      expect(screen.getByText(/Page 2 of/)).toBeInTheDocument();
    });

    const searchInput = screen.getByPlaceholderText('Search clusters by name...');
    fireEvent.change(searchInput, { target: { value: 'special-one' } });

    await waitFor(() => {
      expect(screen.getByText('special-one')).toBeInTheDocument();
    });
    // One matching cluster fits on one page — pagination controls
    // (which only render above 1 page) are gone, confirming the reset.
    expect(screen.queryByText(/Page \d+ of \d+/)).not.toBeInTheDocument();
  });

  it('sorts Name A→Z by default and Name Z→A when selected', async () => {
    // The sort dropdown lives in the advanced filter bar, which only
    // renders at/above the 5-cluster collapse threshold (V2-cleanup-61.3)
    // — pad with 2 filler clusters excluded from the assertions below.
    mockGetClusters.mockResolvedValue({
      clusters: [
        makeCluster('zeta'),
        makeCluster('alpha'),
        makeCluster('mid'),
        makeCluster('filler-1'),
        makeCluster('filler-2'),
      ],
      health_stats: { total_in_git: 5, connected: 5, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    renderView();

    await waitFor(() => expect(screen.getByText('zeta')).toBeInTheDocument());

    const rowOrder = () =>
      Array.from(document.querySelectorAll('tbody tr'))
        .map((tr) => tr.querySelector('td')?.textContent?.trim())
        .filter((t): t is string => Boolean(t) && ['zeta', 'alpha', 'mid'].includes(t as string));

    expect(rowOrder()).toEqual(['alpha', 'mid', 'zeta']);

    fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'name-desc' } });
    await waitFor(() => {
      expect(rowOrder()).toEqual(['zeta', 'mid', 'alpha']);
    });
  });

  it('"Problems first" sorts failed/missing clusters ahead of healthy ones, stable within each group', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: [
        makeCluster('healthy-a', { connection_status: 'connected' }),
        makeCluster('broken-a', { connection_status: 'failed' }),
        makeCluster('healthy-b', { connection_status: 'connected' }),
        makeCluster('broken-b', { connection_status: 'missing' }),
        makeCluster('healthy-c', { connection_status: 'connected' }),
      ],
      health_stats: { total_in_git: 5, connected: 3, failed: 1, missing_from_argocd: 1, not_in_git: 0 },
    });
    renderView();

    await waitFor(() => expect(screen.getByText('healthy-a')).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'problems-first' } });

    const rowOrder = () =>
      Array.from(document.querySelectorAll('tbody tr'))
        .map((tr) => tr.querySelector('td')?.textContent?.trim())
        .filter((t): t is string => typeof t === 'string' && t.includes('-'));

    await waitFor(() => {
      // Problem clusters (broken-a, broken-b) first, in their original
      // relative order; healthy clusters after, also in original order.
      expect(rowOrder()).toEqual(['broken-a', 'broken-b', 'healthy-a', 'healthy-b', 'healthy-c']);
    });
  });

  it('"Kubernetes version" sorts newest server_version first, missing versions last', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: [
        makeCluster('old', { server_version: '1.24' }),
        makeCluster('no-version', { server_version: undefined }),
        makeCluster('newest', { server_version: '1.30' }),
        makeCluster('mid', { server_version: '1.28.5' }),
        makeCluster('filler-1'),
      ],
      health_stats: { total_in_git: 5, connected: 5, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    renderView();

    await waitFor(() => expect(screen.getByText('old')).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'k8s-version' } });

    const rowOrder = () =>
      Array.from(document.querySelectorAll('tbody tr'))
        .map((tr) => tr.querySelector('td')?.textContent?.trim())
        .filter((t): t is string => Boolean(t) && ['old', 'no-version', 'newest', 'mid'].includes(t as string));

    await waitFor(() => {
      expect(rowOrder()).toEqual(['newest', 'mid', 'old', 'no-version']);
    });
  });

  it('"Most addons" sorts by enabled-label count descending', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: [
        makeCluster('few', { labels: { a: 'enabled' } }),
        makeCluster('none', { labels: { a: 'disabled' } }),
        makeCluster('many', { labels: { a: 'enabled', b: 'enabled', c: 'enabled' } }),
        makeCluster('filler-1'),
        makeCluster('filler-2'),
      ],
      health_stats: { total_in_git: 5, connected: 5, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    renderView();

    await waitFor(() => expect(screen.getByText('few')).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText('Sort'), { target: { value: 'most-addons' } });

    const rowOrder = () =>
      Array.from(document.querySelectorAll('tbody tr'))
        .map((tr) => tr.querySelector('td')?.textContent?.trim())
        .filter((t): t is string => Boolean(t) && ['few', 'none', 'many'].includes(t as string));

    await waitFor(() => {
      expect(rowOrder()).toEqual(['many', 'few', 'none']);
    });
  });
});
