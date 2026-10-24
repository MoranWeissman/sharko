import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview, AVAILABLE_CHECK_FAILED, availableFailureKind } from '@/views/ClustersOverview';
import { ApiError } from '@/services/api';
import { AuthContext } from '@/hooks/useAuth';

// EP3 round S (S27, S29, S36): the Clusters page and the list of clusters
// ArgoCD already knows. Each test fails without its fix.

const adminAuth = {
  token: 't', username: 'admin', role: 'admin', login: vi.fn(), logout: vi.fn(),
  isAuthenticated: true, isAdmin: true, loading: false, error: null,
};

const mockGetClusters = vi.fn();
const mockGetAvailable = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    testClusterConnection: vi.fn(),
    adoptClusters: vi.fn(),
    api: {
      getClusters: (...a: unknown[]) => mockGetClusters(...a),
      getAddonCatalog: () => Promise.resolve({ addons: [] }),
      health: () => Promise.resolve({ status: 'healthy', version: 't', cluster_test_available: true }),
      getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
      getAvailableClusters: (...a: unknown[]) => mockGetAvailable(...a),
    },
  };
});

const managed = {
  name: 'sharko-managed', labels: {}, managed: true,
  connection_status: 'connected', server_version: '1.29',
};
const fiveManaged = [1, 2, 3, 4, 5].map((n) => ({ ...managed, name: `sharko-${n}` }));
const fiveStats = { total_in_git: 5, connected: 5, failed: 0, missing_from_argocd: 0, not_in_git: 0 };

function response(extra: Partial<Record<string, unknown>> = {}) {
  return {
    clusters: [managed],
    health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    pending_registrations: [],
    orphan_registrations: [],
    ...extra,
  };
}

function renderView() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter>
        <ClustersOverview />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

const hint = () => screen.queryByTestId('argocd-known-hint');
const failedLine = () => screen.queryByTestId('argocd-known-failed');

async function openAddClusterAsUser() {
  fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
  await screen.findByText('Register New Cluster');
  fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'user' } });
}

const NO_GIT =
  'Sharko has no usable Git connection. Open Settings and check the active connection: the Git provider, the repository it points at, and the access token.';
const NO_ARGOCD =
  'Sharko has no usable ArgoCD connection. Open Settings and check the active connection: the ArgoCD server address and the ArgoCD token.';

describe('ClustersOverview - round S', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetAvailable.mockResolvedValue([]);
  });

  // S27
  it('a 403 from /clusters/available (a viewer) says nothing', async () => {
    mockGetClusters.mockResolvedValue(response());
    mockGetAvailable.mockRejectedValue(new ApiError(403, { error: 'forbidden' }, 'forbidden'));
    renderView();
    await screen.findByText('sharko-managed');
    await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
    expect(failedLine()).toBeNull();
    expect(screen.queryByText(AVAILABLE_CHECK_FAILED)).toBeNull();
  });

  it('no usable Git connection says nothing about refreshing', async () => {
    mockGetClusters.mockResolvedValue(response());
    mockGetAvailable.mockRejectedValue(new ApiError(502, { error: NO_GIT }, NO_GIT));
    renderView();
    await screen.findByText('sharko-managed');
    await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
    expect(failedLine()).toBeNull();
  });

  it('a real failure (ArgoCD not answering) still says it could not check', async () => {
    mockGetClusters.mockResolvedValue(response());
    mockGetAvailable.mockRejectedValue(new ApiError(502, { error: 'Sharko could not read ArgoCD' }, 'x'));
    renderView();
    expect(await screen.findByTestId('argocd-known-failed')).toHaveTextContent(AVAILABLE_CHECK_FAILED);
  });

  it('sorts each failure the right way', () => {
    expect(availableFailureKind(new ApiError(403, { error: 'no' }, 'no'))).toBe('skipped');
    expect(availableFailureKind(new ApiError(401, { error: 'no' }, 'no'))).toBe('skipped');
    expect(availableFailureKind(new ApiError(502, { error: NO_GIT }, NO_GIT))).toBe('skipped');
    expect(availableFailureKind(new ApiError(503, { error: NO_ARGOCD }, NO_ARGOCD))).toBe('skipped');
    expect(availableFailureKind(new ApiError(502, { error: 'upstream' }, 'upstream'))).toBe('failed');
    expect(availableFailureKind(new ApiError(500, { error: 'boom' }, 'boom'))).toBe('failed');
    expect(availableFailureKind(new TypeError('Failed to fetch'))).toBe('failed');
  });

  // S29
  it('the page search does not hide clusters from the Add Cluster picker, its count or its one-cluster pick', async () => {
    mockGetClusters.mockResolvedValue(response({ clusters: fiveManaged, health_stats: fiveStats }));
    mockGetAvailable.mockResolvedValue([{ name: 'stage-1', server_url: 'https://stage-1:6443' }]);
    renderView();
    await waitFor(() => expect(hint()).not.toBeNull());
    fireEvent.change(screen.getByPlaceholderText('Search clusters by name...'), { target: { value: 'sharko' } });
    // The hint follows the page filter.
    await waitFor(() => expect(hint()).toBeNull());
    await openAddClusterAsUser();
    expect(
      await screen.findByText(
        "ArgoCD already knows about 1 cluster Sharko doesn't manage yet. Pick one to adopt it instead of typing its coordinates by hand.",
      ),
    ).toBeInTheDocument();
    expect(await screen.findByRole('button', { name: 'Adopt stage-1' })).not.toBeDisabled();
    expect(screen.queryByTestId('discovered-empty')).toBeNull();
  });

  it('the page status filter does not hide a cluster /clusters lists from the picker', async () => {
    mockGetClusters.mockResolvedValue(response({
      clusters: [...fiveManaged, { name: 'argo-known', labels: {}, managed: false, connection_status: 'not_in_git', server_url: 'https://k:6443' }],
      health_stats: { ...fiveStats, not_in_git: 1 },
    }));
    renderView();
    await waitFor(() => expect(hint()).toHaveTextContent('1 cluster'));
    fireEvent.change(document.getElementById('managed-status-filter')!, { target: { value: 'connected' } });
    await waitFor(() => expect(hint()).toBeNull());
    await openAddClusterAsUser();
    expect(await screen.findByRole('button', { name: 'Adopt argo-known' })).not.toBeDisabled();
  });

  // S36
  it('a failed check never says "no other clusters there to adopt"', async () => {
    mockGetClusters.mockResolvedValue(response());
    mockGetAvailable.mockRejectedValue(new Error('network down'));
    renderView();
    await screen.findByTestId('argocd-known-failed');
    await openAddClusterAsUser();
    expect(await screen.findByTestId('discovered-check-failed')).toHaveTextContent(AVAILABLE_CHECK_FAILED);
    expect(screen.queryByTestId('discovered-empty')).toBeNull();
    expect(screen.queryByText('Sharko checked ArgoCD — no other clusters there to adopt.')).toBeNull();
  });

  it('a check that was not allowed never says "Sharko checked ArgoCD" either', async () => {
    mockGetClusters.mockResolvedValue(response());
    mockGetAvailable.mockRejectedValue(new ApiError(502, { error: NO_GIT }, NO_GIT));
    renderView();
    await screen.findByText('sharko-managed');
    await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
    await openAddClusterAsUser();
    await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
    expect(screen.queryByTestId('discovered-empty')).toBeNull();
  });

  it('a check that worked and found nothing says so', async () => {
    mockGetClusters.mockResolvedValue(response());
    mockGetAvailable.mockResolvedValue([]);
    renderView();
    await screen.findByText('sharko-managed');
    await openAddClusterAsUser();
    expect(await screen.findByTestId('discovered-empty')).toHaveTextContent(
      'Sharko checked ArgoCD — no other clusters there to adopt.',
    );
  });
});
