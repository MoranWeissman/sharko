import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// EP3 story T1b: the banners for un-adopt, remove and register must follow
// the real pull request after a later merge or close. They used to stay on
// "PR open" for ever.

const mockGetClusters = vi.fn();
const mockUnadoptCluster = vi.fn();
const mockRegisterCluster = vi.fn();
const mockRefreshPR = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: () => Promise.resolve({ addons: [] }),
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  testClusterConnection: vi.fn(),
  unadoptCluster: (...args: unknown[]) => mockUnadoptCluster(...args),
  deleteOrphanCluster: vi.fn(),
  isTestClusterUnavailable: vi.fn(() => false),
  getSystemCapabilities: () => Promise.resolve({}),
  refreshPR: (...args: unknown[]) => mockRefreshPR(...args),
}));

const adopted = {
  clusters: [
    { name: 'prod-eu', labels: { env: 'prod' }, server_version: '1.28', connection_status: 'connected', managed: true, adopted: true },
  ],
  health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
};

function renderView(state?: unknown) {
  localStorage.setItem('sharko-auth-token', 'test-token');
  localStorage.setItem('sharko-auth-user', 'tester');
  localStorage.setItem('sharko-auth-role', 'admin');
  return render(
    <MemoryRouter initialEntries={[{ pathname: '/clusters', state }]}>
      <AuthProvider>
        <ClustersOverview />
      </AuthProvider>
    </MemoryRouter>,
  );
}

const openBody = { pr_id: 9, status: 'open', last_status: 'open' };

describe('ClustersOverview: banners follow the real pull request (T1b)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    localStorage.clear();
    mockGetClusters.mockResolvedValue(adopted);
    mockRefreshPR.mockResolvedValue(openBody);
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  async function unadoptOpen() {
    mockUnadoptCluster.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'success',
      git: { pr_url: 'https://gitea/demo/r/pulls/9', pr_id: 9, merged: false },
    });
    renderView();
    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /un-adopt/i }));
    await waitFor(() => expect(screen.getByText('Un-adopt Cluster')).toBeInTheDocument());
    fireEvent.change(screen.getByPlaceholderText('prod-eu'), { target: { value: 'prod-eu' } });
    fireEvent.click(screen.getByRole('button', { name: /^un-adopt$/i }));
    await screen.findByText('PR open for review — cluster is un-adopted once it merges');
  }

  it('un-adopt: a later merge turns "PR open" into "PR merged"', async () => {
    await unadoptOpen();
    mockRefreshPR.mockResolvedValue({ ...openBody, status: 'merged', last_status: 'merged' });
    await act(() => vi.advanceTimersByTimeAsync(30_100));
    expect(await screen.findByText('PR merged — cluster un-adopted')).toBeInTheDocument();
    expect(screen.queryByText(/PR open for review/)).not.toBeInTheDocument();
  });

  it('un-adopt: a pull request closed without merging says so, and says the cluster is still managed', async () => {
    await unadoptOpen();
    mockRefreshPR.mockResolvedValue({ ...openBody, status: 'closed', last_status: 'closed' });
    await act(() => vi.advanceTimersByTimeAsync(30_100));
    expect(
      await screen.findByText('PR closed without merging — the cluster is still managed'),
    ).toBeInTheDocument();
    expect(screen.queryByText(/PR open for review/)).not.toBeInTheDocument();
  });

  it('un-adopt: a merged pull request Sharko is still finishing says it is finishing', async () => {
    await unadoptOpen();
    mockRefreshPR.mockResolvedValue({ ...openBody, status: 'finishing', last_status: 'merged' });
    await act(() => vi.advanceTimersByTimeAsync(30_100));
    expect(await screen.findByText('PR merged — finishing the un-adopt…')).toBeInTheDocument();
  });

  const removal = { removalPR: { cluster: 'legacy-1', pr_url: 'https://gitea/demo/r/pulls/7', pr_id: 7, merged: false } };

  it('remove: a later close turns "Removal PR opened" into the closed sentence', async () => {
    renderView(removal);
    expect(await screen.findByText('Removal PR opened for "legacy-1"')).toBeInTheDocument();
    mockRefreshPR.mockResolvedValue({ ...openBody, pr_id: 7, status: 'closed', last_status: 'closed' });
    await act(() => vi.advanceTimersByTimeAsync(30_100));
    expect(
      await screen.findByText('Removal PR for "legacy-1" was closed without merging — the cluster is not removed'),
    ).toBeInTheDocument();
    expect(screen.queryByText('Removal PR opened for "legacy-1"')).not.toBeInTheDocument();
  });

  it('remove: a later merge turns the banner into "removed"', async () => {
    renderView(removal);
    expect(await screen.findByText('Removal PR opened for "legacy-1"')).toBeInTheDocument();
    mockRefreshPR.mockResolvedValue({ ...openBody, pr_id: 7, status: 'merged', last_status: 'merged' });
    await act(() => vi.advanceTimersByTimeAsync(30_100));
    expect(await screen.findByText('Cluster "legacy-1" removed')).toBeInTheDocument();
  });

  async function registerOpen() {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      git: { merged: false, pr_url: 'https://gitea/demo/r/pulls/9', pr_id: 9 },
    });
    renderView();
    await waitFor(() => expect(screen.getByText('Clusters')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await waitFor(() => expect(screen.getByText('Register New Cluster')).toBeInTheDocument());
    fireEvent.change(screen.getByDisplayValue(/Choose where this cluster's credentials come from/i), { target: { value: 'inline-kubeconfig' } });
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'kind-local' } });
    fireEvent.change(screen.getByPlaceholderText(/apiVersion: v1/i), {
      target: { value: 'apiVersion: v1\nkind: Config\nusers:\n- name: u\n  user:\n    token: abc' },
    });
    const submit = screen.getAllByRole('button', { name: /^register/i }).find((b) => !b.hasAttribute('disabled'));
    fireEvent.click(submit!);
    await screen.findByText(/Cluster registration PR opened — merge to activate\./);
  }

  it('register: a later merge replaces the "merge to activate" banner with "Cluster registered."', async () => {
    await registerOpen();
    mockRefreshPR.mockResolvedValue({ ...openBody, status: 'merged', last_status: 'merged' });
    await act(() => vi.advanceTimersByTimeAsync(7_100));
    expect(await screen.findByText(/Cluster registered\./)).toBeInTheDocument();
    expect(screen.queryByText(/merge to activate/)).not.toBeInTheDocument();
  });

  it('register: a later close says the cluster was not registered', async () => {
    await registerOpen();
    mockRefreshPR.mockResolvedValue({ ...openBody, status: 'closed', last_status: 'closed' });
    await act(() => vi.advanceTimersByTimeAsync(7_100));
    expect(
      await screen.findByText(/Cluster registration PR was closed without merging — the cluster was not registered\./),
    ).toBeInTheDocument();
    expect(screen.queryByText(/merge to activate/)).not.toBeInTheDocument();
  });
});
