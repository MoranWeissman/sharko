import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview, unadoptDescription, UNADOPT_ADMIN_ONLY_HINT } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

/*
 * V3-TX-A3 — Preview on every PR-opening operation. Surface 3: Un-adopt cluster.
 *
 * An adopted cluster's row carries an "Un-adopt" action that opens a
 * type-to-confirm modal. That modal now offers a "Preview changes" button
 * that calls unadoptCluster(name, true) and renders the DryRunResult via the
 * shared DryRunPreview — the destructive confirm stays a separate action.
 */

const mockGetClusters = vi.fn();
const mockUnadoptCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
  registerCluster: vi.fn(),
  testClusterConnection: vi.fn(),
  unadoptCluster: (...args: unknown[]) => mockUnadoptCluster(...args),
  deleteOrphanCluster: vi.fn(),
  isTestClusterUnavailable: vi.fn(() => false),
  getSystemCapabilities: () => Promise.resolve({}),
}));

const adoptedCluster = {
  clusters: [
    {
      name: 'prod-eu',
      labels: { env: 'prod' },
      server_version: '1.28',
      connection_status: 'connected',
      managed: true,
      adopted: true,
    },
  ],
  health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
};

function renderView(role = 'admin') {
  // Auth session lives in localStorage — see ui/src/lib/authStorage.ts.
  localStorage.setItem('sharko-auth-token', 'test-token');
  localStorage.setItem('sharko-auth-user', 'tester');
  localStorage.setItem('sharko-auth-role', role);
  return render(
    <MemoryRouter>
      <AuthProvider>
        <ClustersOverview />
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('ClustersOverview — Un-adopt preview (V3-TX-A3, Surface 3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    localStorage.clear();
    mockGetClusters.mockResolvedValue(adoptedCluster);
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('Preview changes calls unadoptCluster(dry-run) and renders the diff without un-adopting', async () => {
    mockUnadoptCluster.mockImplementation((_name: string, dryRun?: boolean) => {
      if (dryRun) {
        return Promise.resolve({
          pr_title: 'Un-adopt cluster prod-eu',
          files_to_write: [
            { path: 'configuration/managed-clusters.yaml', action: 'update' },
          ],
        });
      }
      return Promise.resolve({ pr_url: 'https://github.com/example/repo/pull/5', pr_id: 5 });
    });

    renderView();
    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());

    // Open the Un-adopt confirm modal.
    fireEvent.click(screen.getByRole('button', { name: /un-adopt/i }));
    await waitFor(() =>
      expect(screen.getByText('Un-adopt Cluster')).toBeInTheDocument(),
    );

    // Click Preview changes inside the modal.
    fireEvent.click(screen.getByRole('button', { name: /preview changes/i }));

    await waitFor(() => expect(mockUnadoptCluster).toHaveBeenCalledWith('prod-eu', true));
    await waitFor(() =>
      expect(screen.getByText('Un-adopt cluster prod-eu')).toBeInTheDocument(),
    );
    expect(screen.getByText('configuration/managed-clusters.yaml')).toBeInTheDocument();
    // Preview must NOT have fired the real un-adopt (no single-arg call).
    expect(mockUnadoptCluster).not.toHaveBeenCalledWith('prod-eu');
  });

  // Round S: the window says what the button does, in plain words, and no
  // longer promises what Sharko will do later.
  it('the Un-adopt window says what the button does, and not the old promise', async () => {
    renderView();
    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /un-adopt/i }));
    await waitFor(() => expect(screen.getByText('Un-adopt Cluster')).toBeInTheDocument());
    expect(unadoptDescription('prod-eu')).toBe(
      'Un-adopt opens a pull request that takes "prod-eu" off the list of clusters Sharko manages. It does not delete the ArgoCD connection. Click Preview changes to see what the pull request changes.',
    );
    expect(screen.getByText(unadoptDescription('prod-eu'))).toBeInTheDocument();
    expect(document.body.textContent).not.toMatch(/Sharko will no longer manage addons/);
    expect(document.body.textContent).not.toMatch(/from Sharko management/);
  });

  // Round S (episode 3 open question 12): a non-admin gets a line saying why
  // there is no Un-adopt button, like Take ownership already has.
  it('a non-admin sees a plain line instead of the Un-adopt button', async () => {
    renderView('viewer');
    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    expect(screen.queryByRole('button', { name: /un-adopt/i })).toBeNull();
    const hints = screen.getAllByTestId('unadopt-admin-only');
    expect(hints.length).toBeGreaterThan(0);
    for (const h of hints) expect(h).toHaveTextContent(/^Only an admin can un-adopt this cluster\.$/);
    expect(UNADOPT_ADMIN_ONLY_HINT).toBe('Only an admin can un-adopt this cluster.');
  });

  it('an admin sees the Un-adopt button and not the line', async () => {
    renderView();
    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    expect(screen.getAllByRole('button', { name: /un-adopt/i }).length).toBeGreaterThan(0);
    expect(screen.queryByTestId('unadopt-admin-only')).toBeNull();
  });

  // v4.0.4 review fix: the server answers HTTP 200 with status "failed"
  // when the Git step did not happen. That used to show
  // "Cluster un-adopted successfully."
  async function confirmUnadopt() {
    renderView();
    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /un-adopt/i }));
    await waitFor(() => expect(screen.getByText('Un-adopt Cluster')).toBeInTheDocument());
    fireEvent.change(screen.getByPlaceholderText('prod-eu'), { target: { value: 'prod-eu' } });
    fireEvent.click(screen.getByRole('button', { name: /^un-adopt$/i }));
    await waitFor(() => expect(mockUnadoptCluster).toHaveBeenCalledWith('prod-eu'));
  }

  it('a "failed" reply is shown as the failure, never as success', async () => {
    mockUnadoptCluster.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'failed',
      error: 'push rejected',
      message: 'Git commit failed during unadoption — ArgoCD secret labels have already been removed',
    });
    await confirmUnadopt();
    expect(
      await screen.findByText('Git commit failed during unadoption — ArgoCD secret labels have already been removed'),
    ).toBeInTheDocument();
    expect(screen.queryByText(/un-adopted successfully/i)).not.toBeInTheDocument();
  });

  it('a "failed" reply with no sentence gets the plain fallback', async () => {
    mockUnadoptCluster.mockResolvedValue({ cluster: 'prod-eu', status: 'failed' });
    await confirmUnadopt();
    expect(
      await screen.findByText('Un-adopt did not finish: Sharko could not write the change to Git.'),
    ).toBeInTheDocument();
    expect(screen.queryByText(/un-adopted successfully/i)).not.toBeInTheDocument();
  });

  it('a "partial" reply with a PR still shows the PR banner', async () => {
    mockUnadoptCluster.mockResolvedValue({
      cluster: 'prod-eu',
      status: 'partial',
      message: 'PR created but merge failed: https://github.com/example/repo/pull/9',
      git: { pr_url: 'https://github.com/example/repo/pull/9', pr_id: 9, merged: false },
    });
    await confirmUnadopt();
    expect(await screen.findByText(/PR open for review/i)).toBeInTheDocument();
    expect(screen.queryByText(/un-adopted successfully/i)).not.toBeInTheDocument();
  });
});
