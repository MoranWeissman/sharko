import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// Episode 4 dry run, finding 2: when ArgoCD refuses Sharko's login or cannot be
// reached, the clusters in Git must still show, with a word that says ArgoCD's
// status is not known. The page must not say there are no clusters.

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
    api: {
      getClusters: (...a: unknown[]) => mockGetClusters(...a),
      getAddonCatalog: () => Promise.resolve({ addons: [] }),
      health: () => Promise.resolve({ status: 'healthy', version: 't', cluster_test_available: true }),
      getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
      getAvailableClusters: (...a: unknown[]) => mockGetAvailable(...a),
    },
  };
});

const inGit = { name: 'prod-eu', labels: {}, managed: true, connection_status: 'unknown' };
const argoOnly = { name: 'argo-known', labels: {}, managed: false, connection_status: 'not_in_git' };

function response(extra: Record<string, unknown> = {}) {
  return {
    clusters: [inGit],
    health_stats: { total_in_git: 1, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
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

const EMPTY = 'No clusters are registered yet. Click Add Cluster to register your first one.';

describe('ClustersOverview - ArgoCD did not answer (episode 4, finding 2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetAvailable.mockResolvedValue([]);
  });

  it('refused login: shows the Git cluster with "ArgoCD status unknown" and says why', async () => {
    mockGetClusters.mockResolvedValue(response({ argocd_unavailable_reason: 'auth_refused' }));
    renderView();
    await screen.findByText('prod-eu');
    expect(screen.getAllByTestId('argocd-status-unknown')[0].textContent).toBe('ArgoCD status unknown');
    expect(screen.getByTestId('argocd-status-unknown-note').textContent).toBe(
      "ArgoCD refused Sharko's login, so ArgoCD's status for these clusters is not known right now.",
    );
    expect(screen.queryByText(EMPTY)).not.toBeInTheDocument();
  });

  it('unreachable: says Sharko could not reach ArgoCD', async () => {
    mockGetClusters.mockResolvedValue(response({ argocd_unavailable_reason: 'unreachable' }));
    renderView();
    await screen.findByText('prod-eu');
    expect(screen.getByTestId('argocd-status-unknown-note').textContent).toBe(
      "Sharko could not reach ArgoCD, so ArgoCD's status for these clusters is not known right now.",
    );
  });

  it('hides the "N clusters ArgoCD already knows" line while ArgoCD fails', async () => {
    mockGetClusters.mockResolvedValue(
      response({ clusters: [inGit, argoOnly], argocd_unavailable_reason: 'auth_refused' }),
    );
    renderView();
    await screen.findByText('prod-eu');
    expect(screen.queryByTestId('argocd-known-hint')).not.toBeInTheDocument();
  });

  it('with no clusters in Git, says so, and says separately that ArgoCD is not answering', async () => {
    mockGetClusters.mockResolvedValue(response({ clusters: [], argocd_unavailable_reason: 'unreachable' }));
    renderView();
    expect(
      await screen.findByText(
        'There are no clusters in Git yet. Separately, ArgoCD is not answering Sharko, so Sharko cannot tell what ArgoCD knows. Fix the ArgoCD connection, then refresh.',
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText(EMPTY)).not.toBeInTheDocument();
    // The list comes from Git, so ArgoCD being down is not why it is empty.
    expect(screen.queryByText(/No clusters are listed/)).not.toBeInTheDocument();
    expect(screen.queryByText(/because ArgoCD is not answering/)).not.toBeInTheDocument();
  });

  it('ArgoCD answered: no unknown word, no note, the "already knows" line is back', async () => {
    mockGetClusters.mockResolvedValue(response({ clusters: [inGit, argoOnly] }));
    renderView();
    await screen.findByText('prod-eu');
    expect(screen.queryByTestId('argocd-status-unknown')).not.toBeInTheDocument();
    expect(screen.queryByTestId('argocd-status-unknown-note')).not.toBeInTheDocument();
    expect(screen.getByTestId('argocd-known-hint')).toBeInTheDocument();
  });
});
