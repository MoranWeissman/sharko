import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// Fix 13 and 14 of the episode 3 blockers: the Try Again button on a load
// error must show a spinner, the register dialog must not close while it is
// writing, and a preview must not outlive the values it was made from.

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
    // BUG-041: ClustersOverview reads cluster_test_available on mount.
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
    // V2-cleanup-89.6 kill switch — not under test here, keep the default.
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
    // Story G2: the auto-merge note reads the GitOps switch. Off here, the
    // way the first-run wizard leaves it.
    getConfig: () => Promise.resolve({ gitops: { pr_auto_merge: false } }),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  // ErrorDetail checks `instanceof ApiError`; a plain class is enough here.
  ApiError: class ApiError extends Error {},
  testClusterConnection: vi.fn(),
  unadoptCluster: vi.fn(),
}));

const baseClusters = {
  clusters: [],
  health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
};

function renderView() {
  // Add Cluster button is admin-gated via RoleGuard. Auth session lives in
  // localStorage — see ui/src/lib/authStorage.ts.
  localStorage.setItem('sharko-auth-token', 'test-token');
  localStorage.setItem('sharko-auth-user', 'tester');
  localStorage.setItem('sharko-auth-role', 'admin');
  return render(
    <MemoryRouter>
      <AuthProvider>
        <ClustersOverview />
      </AuthProvider>
    </MemoryRouter>,
  );
}

async function openAddDialog() {
  await waitFor(() => {
    expect(screen.getByText('Clusters')).toBeInTheDocument();
  });
  const addButton = screen.getByRole('button', { name: /add cluster/i });
  fireEvent.click(addButton);
  await waitFor(() => {
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
  });
}


async function fillMinimum() {
  const select = screen.getByDisplayValue(/Choose where this cluster's credentials come from/i) as HTMLSelectElement;
  fireEvent.change(select, { target: { value: 'inline-kubeconfig' } });
  fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'kind-test' } });
  fireEvent.change(screen.getByPlaceholderText(/apiVersion: v1/i), {
    target: { value: 'apiVersion: v1\nkind: Config' },
  });
}

describe('ClustersOverview — retry, close guard, stale preview', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    localStorage.clear();
    mockGetClusters.mockResolvedValue(baseClusters);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('Try Again after a load error shows the loading spinner, not a silent refresh', async () => {
    mockGetClusters.mockRejectedValueOnce(new Error('boom'));
    renderView();
    const retry = await screen.findByRole('button', { name: /try again/i });
    let release: (v: unknown) => void = () => {};
    mockGetClusters.mockReturnValueOnce(new Promise((res) => { release = res; }));
    fireEvent.click(retry);
    expect(await screen.findByText('Loading clusters...')).toBeInTheDocument();
    release(baseClusters);
    await waitFor(() => expect(screen.getByText('Clusters')).toBeInTheDocument());
  });

  it('Escape does not close the dialog while Register is still writing', async () => {
    let finish: (v: unknown) => void = () => {};
    mockRegisterCluster.mockReturnValue(new Promise((res) => { finish = res; }));
    renderView();
    await openAddDialog();
    await fillMinimum();
    fireEvent.click(screen.getByRole('button', { name: /^register$/i }));
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    finish({ status: 'success', git: { pr_url: 'https://example.com/pr/1', merged: false } });
    await waitFor(() => expect(screen.queryByText('Register New Cluster')).not.toBeInTheDocument());
  });

  it('Escape still closes the dialog when nothing is being written', async () => {
    renderView();
    await openAddDialog();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByText('Register New Cluster')).not.toBeInTheDocument());
  });

  it('removes the preview as soon as a field changes', async () => {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      dry_run: { pr_title: 'sharko: register cluster kind-test', files_to_write: [], effective_addons: [], secrets_to_create: [] },
    });
    renderView();
    await openAddDialog();
    await fillMinimum();
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));
    expect(await screen.findByText(/register cluster kind-test/)).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'kind-other' } });
    await waitFor(() => expect(screen.queryByText(/register cluster kind-test/)).not.toBeInTheDocument());
  });
  it('drops a preview answer that arrives after a field changed (ep3 review G3)', async () => {
    let answer: (v: unknown) => void = () => {};
    mockRegisterCluster.mockReturnValue(new Promise((res) => { answer = res; }));
    renderView();
    await openAddDialog();
    await fillMinimum();
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));
    // The user edits a field while the answer is still on its way.
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'kind-other' } });
    answer({
      status: 'success',
      dry_run: { pr_title: 'sharko: register cluster kind-test', files_to_write: [], effective_addons: [], secrets_to_create: [] },
    });
    await new Promise((r) => setTimeout(r, 20));
    // The old answer was for the old name, so it must not show up.
    expect(screen.queryByText(/register cluster kind-test/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /preview/i })).toBeEnabled();
  });

  it('drops a preview error that arrives after a field changed (ep3 review G3)', async () => {
    let fail: (e: unknown) => void = () => {};
    mockRegisterCluster.mockReturnValue(new Promise((_res, rej) => { fail = rej; }));
    renderView();
    await openAddDialog();
    await fillMinimum();
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'kind-other' } });
    fail(new Error('old preview failed'));
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByText(/old preview failed/)).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: /preview/i })).toBeEnabled();
  });
});
