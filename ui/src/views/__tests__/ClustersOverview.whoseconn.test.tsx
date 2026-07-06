import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';
import {
  ARGOCD_CONN_LABEL,
  ARGOCD_CONN_TOOLTIP,
  SHARKO_CONN_LABEL,
  SHARKO_CONN_TOOLTIP,
} from '@/components/WhoseConnectionLabel';

// V2-cleanup-55.3 — whose-connection attribution. A cluster's
// `connection_status` is ArgoCD's own connection to the cluster; the Test
// button is Sharko's own connection (creds fetched from the secret backend).
// The two can disagree, so the UI must say whose connection each one is.
// These tests pin:
//
//   1. Every managed-cluster status cell carries an "ArgoCD → cluster"
//      caption with the explanatory tooltip.
//   2. The connection-status filter button is attributed to ArgoCD.
//   3. The Test button's tooltip explains it is Sharko → cluster.
//   4. A completed test result is captioned "Sharko → cluster".
//   5. The creds-source dropdown shows a plain-English hint per option.

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
const mockTestClusterConnection = vi.fn();

vi.mock('@/services/api', async () => {
  // Keep `isTestClusterUnavailable` (and other pure helpers) real; only stub
  // the network calls this suite exercises.
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusters: (...args: unknown[]) => mockGetClusters(...args),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      health: (...args: unknown[]) => mockHealth(...args),
    },
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
  };
});

const clustersResponse = {
  clusters: [
    {
      name: 'prod-eu',
      labels: { env: 'prod' },
      server_version: '1.28',
      // ArgoCD-reported status — the exact field the story is about.
      connection_status: 'connected',
    },
    {
      name: 'staging-us',
      labels: { env: 'staging' },
      server_version: '1.27',
      connection_status: 'failed',
    },
  ],
  health_stats: {
    total_in_git: 2,
    connected: 1,
    failed: 1,
    missing_from_argocd: 0,
    not_in_git: 0,
  },
};

function renderView() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter>
        <ClustersOverview />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

async function waitForClusters() {
  await waitFor(() => {
    expect(screen.getByText('prod-eu')).toBeInTheDocument();
  });
}

describe('ClustersOverview — whose-connection labels (V2-cleanup-55.3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusters.mockResolvedValue(clustersResponse);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockHealth.mockResolvedValue({
      status: 'healthy',
      version: 'test',
      cluster_test_available: true,
    });
  });

  // V2-cleanup-61.2 (D4): each row now renders ONE composite status pill;
  // the "ArgoCD → cluster" attribution lives in the pill's accessible
  // popover breakdown instead of an always-visible per-row caption.
  it('attributes the connection state to "ArgoCD → cluster" inside every row pill popover', async () => {
    renderView();
    await waitForClusters();

    // One composite pill per managed cluster row.
    const pills = screen.getAllByTestId('cluster-status-pill');
    expect(pills).toHaveLength(2);

    // Opening a pill's popover reveals the attribution + its tooltip.
    fireEvent.click(pills[0]);
    await waitFor(() => {
      expect(screen.getByText(ARGOCD_CONN_LABEL)).toBeInTheDocument();
    });
    expect(screen.getByText(ARGOCD_CONN_LABEL)).toHaveAttribute('title', ARGOCD_CONN_TOOLTIP);
  });

  it('attributes the connection-status filter to ArgoCD', async () => {
    // The filter bar (and its "ArgoCD Connection" button) is hidden below
    // the 5-cluster collapse threshold (V2-cleanup-61.3, B3) — use a
    // fixture at the threshold so the filter bar renders for this check.
    mockGetClusters.mockResolvedValue({
      clusters: [
        ...clustersResponse.clusters,
        { name: 'c3', labels: {}, server_version: '1.28', connection_status: 'connected' },
        { name: 'c4', labels: {}, server_version: '1.28', connection_status: 'connected' },
        { name: 'c5', labels: {}, server_version: '1.28', connection_status: 'connected' },
      ],
      health_stats: {
        total_in_git: 5,
        connected: 4,
        failed: 1,
        missing_from_argocd: 0,
        not_in_git: 0,
      },
    });
    renderView();
    await waitForClusters();

    const filterBtn = screen.getByRole('button', { name: /ArgoCD Connection/ });
    expect(filterBtn).toHaveAttribute('title', ARGOCD_CONN_TOOLTIP);
    // The old un-attributed label is gone from the filter bar.
    expect(screen.queryByRole('button', { name: /^Connection Status/ })).not.toBeInTheDocument();
  });

  it('gives the enabled Test connection button a Sharko → cluster tooltip', async () => {
    renderView();
    await waitForClusters();

    const testButtons = screen.getAllByRole('button', { name: 'Test connection' });
    expect(testButtons.length).toBeGreaterThan(0);
    for (const btn of testButtons) {
      expect(btn).toHaveAttribute('title', SHARKO_CONN_TOOLTIP);
    }
  });

  it('captions a completed test result as "Sharko → cluster"', async () => {
    mockTestClusterConnection.mockResolvedValue({
      reachable: true,
      server_version: 'v1.29.3',
      steps: [
        { name: 'Fetch credentials from secret backend', status: 'pass' },
        { name: 'Connect to cluster API', status: 'pass' },
      ],
    });
    renderView();
    await waitForClusters();

    // No Sharko caption before a test has run.
    expect(screen.queryByText(SHARKO_CONN_LABEL)).not.toBeInTheDocument();

    fireEvent.click(screen.getAllByRole('button', { name: 'Test connection' })[0]);

    await waitFor(() => {
      expect(screen.getByText('Connected (2/2 checks passed)')).toBeInTheDocument();
    });
    const caption = screen.getByText(SHARKO_CONN_LABEL);
    expect(caption).toHaveAttribute('title', SHARKO_CONN_TOOLTIP);
  });

  describe('creds-source dropdown hints', () => {
    async function openAddDialog() {
      await waitForClusters();
      fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
      await waitFor(() => {
        expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
      });
    }

    // The dialog opens with NO creds-source choice (V2-cleanup-60.4 un-trap)
    // — the select shows the non-selectable placeholder.
    function credsSourceSelect(): HTMLSelectElement {
      return screen.getByDisplayValue(/Choose where this cluster's credentials come from/i) as HTMLSelectElement;
    }

    it('shows the required-choice hint by default and the EKS hint only after an explicit pick', async () => {
      renderView();
      await openAddDialog();

      // No silent eks-token default anymore.
      expect(credsSourceSelect().value).toBe('');
      expect(
        screen.getByText('Required — pick one of the three options before registering.'),
      ).toBeInTheDocument();
      expect(screen.queryByText(/short-lived AWS tokens/)).not.toBeInTheDocument();

      fireEvent.change(credsSourceSelect(), { target: { value: 'eks-token' } });

      expect(
        screen.getByText(
          'No stored kubeconfig — Sharko generates short-lived AWS tokens using its own AWS identity.',
        ),
      ).toBeInTheDocument();
    });

    it('switches the hint when "Paste a kubeconfig" is chosen', async () => {
      renderView();
      await openAddDialog();

      fireEvent.change(credsSourceSelect(), { target: { value: 'inline-kubeconfig' } });

      expect(
        screen.getByText('Paste the file contents here once — Sharko stores it for this cluster.'),
      ).toBeInTheDocument();
      expect(
        screen.queryByText(/short-lived AWS tokens/),
      ).not.toBeInTheDocument();
    });

    it('switches the hint when "Use a stored kubeconfig" is chosen', async () => {
      renderView();
      await openAddDialog();

      fireEvent.change(credsSourceSelect(), { target: { value: 'secret-kubeconfig' } });

      expect(
        screen.getByText(
          'Sharko fetches the kubeconfig from your configured secrets backend (the secret name/path below).',
        ),
      ).toBeInTheDocument();
    });
  });
});
