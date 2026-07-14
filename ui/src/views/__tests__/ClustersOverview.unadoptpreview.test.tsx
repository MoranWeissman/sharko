import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
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

function renderView() {
  sessionStorage.setItem('sharko-auth-token', 'test-token');
  sessionStorage.setItem('sharko-auth-user', 'tester');
  sessionStorage.setItem('sharko-auth-role', 'admin');
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
});
