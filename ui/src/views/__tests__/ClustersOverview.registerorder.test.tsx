import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-61.4 (C1) — the Register New Cluster dialog used to ask
// ownership + creds-source BEFORE the Direct/Discovery mode toggle, even
// though the mode can make both of those questions irrelevant (Discovery
// mode doesn't use either). The mode toggle now renders FIRST, so a user
// never answers a question the very next click could make moot.

const adminAuth = {
  token: 'test-token',
  username: 'admin',
  role: 'admin',
  login: vi.fn(),
  logout: vi.fn(),
  isAuthenticated: true,
  isAdmin: true,
  loading: false,
  error: null,
};

const mockGetClusters = vi.fn();
const mockHealth = vi.fn();
const mockGetAddonCatalog = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusters: (...args: unknown[]) => mockGetClusters(...args),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      health: (...args: unknown[]) => mockHealth(...args),
    },
  };
});

function renderView() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter>
        <ClustersOverview />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('ClustersOverview — Register dialog field order (V2-cleanup-61.4, C1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusters.mockResolvedValue({
      clusters: [{ name: 'prod-eu', labels: {}, server_version: '1.28', connection_status: 'connected' }],
      health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockHealth.mockResolvedValue({ status: 'healthy', version: 'test', cluster_test_available: true });
  });

  it('renders the Registration Mode toggle before the ownership and creds-source questions', async () => {
    renderView();

    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await waitFor(() => expect(screen.getByText('Register New Cluster')).toBeInTheDocument());

    const dialog = screen.getByText('Register New Cluster').closest('[role="dialog"]') as HTMLElement;
    expect(dialog).toBeTruthy();

    const text = dialog.textContent ?? '';
    const modeIdx = text.indexOf('Registration Mode');
    const ownershipIdx = text.indexOf('Who manages the ArgoCD connection?');
    const credsIdx = text.indexOf("How should Sharko get this cluster's credentials?");

    expect(modeIdx).toBeGreaterThan(-1);
    expect(ownershipIdx).toBeGreaterThan(-1);
    expect(credsIdx).toBeGreaterThan(-1);
    expect(modeIdx).toBeLessThan(ownershipIdx);
    expect(modeIdx).toBeLessThan(credsIdx);
  });

  it('still shows the Direct/Discovery toggle and no ownership/creds questions in Discovery mode', async () => {
    renderView();

    await waitFor(() => expect(screen.getByText('prod-eu')).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await waitFor(() => expect(screen.getByText('Register New Cluster')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /Discovery/i }));

    expect(screen.queryByText('Who manages the ArgoCD connection?')).not.toBeInTheDocument();
    expect(screen.queryByText("How should Sharko get this cluster's credentials?")).not.toBeInTheDocument();
    expect(screen.getByText(/^Role ARNs/)).toBeInTheDocument();
  });
});
