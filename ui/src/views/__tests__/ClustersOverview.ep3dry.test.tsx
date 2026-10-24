import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// Episode 3 dry run, story D: what the Clusters page must show for
// findings 3, 6, 14, 19, 23, 25 and 7. Each test fails without its fix.

const adminAuth = {
  token: 't', username: 'admin', role: 'admin', login: vi.fn(), logout: vi.fn(),
  isAuthenticated: true, isAdmin: true, loading: false, error: null,
};

const mockGetClusters = vi.fn();
const mockGetAvailable = vi.fn();
const mockTest = vi.fn();
const mockAdopt = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    testClusterConnection: (...a: unknown[]) => mockTest(...a),
    adoptClusters: (...a: unknown[]) => mockAdopt(...a),
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
const known = {
  name: 'argo-known', labels: {}, managed: false, connection_status: 'not_in_git',
  server_url: 'https://argo-known.example.com:6443',
};

function response(extra: Partial<Record<string, unknown>> = {}) {
  return {
    clusters: [managed, known],
    health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 1 },
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

describe('ClustersOverview - episode 3 dry run (story D)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetAvailable.mockResolvedValue([]);
  });

  it('finding 3: a reload after the first load does not swap the page for the loading screen', async () => {
    mockGetClusters.mockResolvedValueOnce(response());
    renderView();
    await screen.findByText('sharko-managed');
    let release: (v: unknown) => void = () => {};
    mockGetClusters.mockReturnValue(new Promise((r) => { release = r; }));
    fireEvent.click(screen.getByTitle('Refresh'));
    expect(screen.queryByText('Loading clusters...')).not.toBeInTheDocument();
    expect(screen.getByText('sharko-managed')).toBeInTheDocument();
    release(response());
  });

  it('finding 3: the Adopt success screen stays put while the list reloads behind it', async () => {
    mockGetClusters.mockResolvedValueOnce(response());
    mockTest.mockResolvedValue({ success: true, stage: 'connectivity', duration_ms: 5, reachable: true, server_version: '1.29.0' });
    mockAdopt.mockResolvedValue({
      results: [{ name: 'argo-known', status: 'success', git: { pr_url: 'https://github.com/o/r/pull/99' } }],
    });
    renderView();
    await screen.findByText('sharko-managed');
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await screen.findByText('Register New Cluster');
    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'user' } });
    fireEvent.click(await screen.findByRole('button', { name: 'Adopt argo-known' }));
    const confirm = await screen.findByRole('button', { name: /Confirm Adoption/i });
    let release: (v: unknown) => void = () => {};
    mockGetClusters.mockReturnValue(new Promise((r) => { release = r; }));
    fireEvent.click(confirm);
    const link = await screen.findByRole('link', { name: /view pull request/i });
    expect(link).toBeInTheDocument();
    expect(screen.queryByText('Loading clusters...')).not.toBeInTheDocument();
    release(response());
  });

  it('finding 6: a cluster the server says is adopted gets an Un-adopt button; a plain one does not', async () => {
    mockGetClusters.mockResolvedValue(
      response({ clusters: [{ ...managed, adopted: true }, { ...managed, name: 'plain-one' }] }),
    );
    renderView();
    await screen.findByText('sharko-managed');
    expect(screen.getAllByRole('button', { name: /Un-adopt/ })).toHaveLength(1);
  });

  it('finding 14: a line under the title says how many clusters ArgoCD already knows', async () => {
    mockGetClusters.mockResolvedValue(response());
    renderView();
    const hint = await screen.findByTestId('argocd-known-hint');
    expect(hint).toHaveTextContent("1 cluster ArgoCD already knows that Sharko doesn't manage yet.");
  });

  it('finding 14: no such line when ArgoCD knows nothing extra', async () => {
    mockGetClusters.mockResolvedValue(response({ clusters: [managed] }));
    renderView();
    await screen.findByText('sharko-managed');
    expect(screen.queryByTestId('argocd-known-hint')).not.toBeInTheDocument();
  });

  it('finding 19: a take-ownership pull request is listed as a change, with its number', async () => {
    mockGetClusters.mockResolvedValue(response({
      pending_registrations: [
        { cluster_name: 'sharko-managed', branch: 'sharko/takeover', pr_url: 'https://x/pull/7', operation: 'takeover', pr_number: 7 },
      ],
    }));
    renderView();
    expect(await screen.findByText('Pending Changes')).toBeInTheDocument();
    expect(screen.queryByText('Pending Registrations')).not.toBeInTheDocument();
    expect(screen.getByText('Take ownership')).toBeInTheDocument();
    expect(screen.getByText('View PR #7')).toBeInTheDocument();
    // The cluster stays in the list: only a register PR hides it.
    expect(screen.getAllByText('sharko-managed').length).toBeGreaterThan(1);
  });

  it('finding 19: register-only pull requests keep the old heading', async () => {
    mockGetClusters.mockResolvedValue(response({
      pending_registrations: [
        { cluster_name: 'new-one', branch: 'b', pr_url: 'https://x/pull/8', operation: 'register', pr_number: 8 },
      ],
    }));
    renderView();
    expect(await screen.findByText('Pending Registrations')).toBeInTheDocument();
    expect(screen.queryByText('Pending Changes')).not.toBeInTheDocument();
  });

  it('findings 25 and 23: the only cluster ArgoCD knows is picked already, and user mode hides the identity strip', async () => {
    mockGetClusters.mockResolvedValue(response());
    renderView();
    await screen.findByText('sharko-managed');
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await screen.findByText('Register New Cluster');
    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'user' } });
    expect(await screen.findByRole('button', { name: 'Adopt argo-known' })).not.toBeDisabled();
    expect(screen.queryByText(/You can add the connection details later/)).not.toBeInTheDocument();
    expect(screen.queryByText('Connection source')).not.toBeInTheDocument();
    expect(screen.queryByTestId('identity-strip')).not.toBeInTheDocument();
  });

  it('finding 7: Take ownership in the picker opens the take-ownership dialog for the picked cluster', async () => {
    mockGetClusters.mockResolvedValue(response());
    renderView();
    await screen.findByText('sharko-managed');
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await screen.findByText('Register New Cluster');
    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'user' } });
    const btn = await screen.findByTestId('picker-take-ownership');
    await waitFor(() => expect(btn).not.toBeDisabled());
    expect(btn).toHaveTextContent('Take ownership of argo-known');
    fireEvent.click(btn);
    expect(await screen.findByText('Take ownership of this cluster')).toBeInTheDocument();
  });

  it('finding 11: the card view shows one v in front of the version, never two', async () => {
    mockGetClusters.mockResolvedValue(
      response({
        clusters: [1, 2, 3, 4, 5].map((n) => ({ ...managed, name: `c-${n}`, server_version: 'v1.29.0' })),
        health_stats: { total_in_git: 5, connected: 5, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      }),
    );
    renderView();
    await screen.findByText('c-1');
    fireEvent.click(await screen.findByLabelText('Grid view'));
    expect((await screen.findAllByText('v1.29.0')).length).toBeGreaterThan(0);
    expect(screen.queryByText(/vv\d/)).not.toBeInTheDocument();
  });
});
