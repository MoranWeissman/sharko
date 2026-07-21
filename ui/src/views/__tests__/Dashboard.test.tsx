import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Dashboard, isBootstrapBlocking, BOOTSTRAP_BLOCKING_HEALTH } from '@/views/Dashboard';
import { api } from '@/services/api';
// v1.21 Bundle 3 — Dashboard now consumes addon state via the unified
// provider. Tests have to mount it inside one or the hook throws.
import { AddonStatesProvider } from '@/hooks/useAddonStates';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>(
    'react-router-dom',
  );
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

vi.mock('recharts', () => {
  const C = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>;
  return {
    ResponsiveContainer: C,
    PieChart: C,
    Pie: () => <div data-testid="pie" />,
    Cell: () => null,
    Legend: () => null,
    Tooltip: () => null,
  };
});

vi.mock('@/services/api', () => ({
  fetchTrackedPRs: vi.fn().mockResolvedValue({ prs: [] }),
  fetchMergedPRs: vi.fn().mockResolvedValue({ prs: [], limit: 20 }),
  refreshPR: vi.fn().mockResolvedValue({ status: 'ok' }),
  fetchAuditLog: vi.fn().mockResolvedValue({ entries: [] }),
  api: {
    getObservability: vi.fn().mockResolvedValue(null),
    getVersionMatrix: vi.fn().mockResolvedValue(null),
    getAttentionItems: vi.fn().mockResolvedValue([]),
    getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
    getHomeCluster: vi.fn().mockResolvedValue({ available: false, message: 'only available when running in-cluster' }),
    getDashboardStats: vi.fn().mockResolvedValue({
      connections: { total: 1, active: 'dev' },
      clusters: { total: 10, connected_to_argocd: 8, disconnected_from_argocd: 2 },
      applications: {
        total: 50,
        by_sync_status: { synced: 40, out_of_sync: 8, unknown: 2 },
        by_health_status: { healthy: 45, progressing: 2, degraded: 2, unknown: 1 },
      },
      addons: { total_available: 15, total_deployments: 100, enabled_deployments: 85 },
    }),
  },
}));

function renderDashboard() {
  return render(
    <MemoryRouter>
      <AddonStatesProvider>
        <Dashboard />
      </AddonStatesProvider>
    </MemoryRouter>,
  );
}

// Base stats used by the bootstrap-banner gating tests. We override
// bootstrap_app_health per case via the mocked api.getDashboardStats.
const baseStats = {
  connections: { total: 1, active: 'dev' },
  clusters: { total: 10, connected_to_argocd: 8, disconnected_from_argocd: 2 },
  applications: {
    total: 50,
    by_sync_status: { synced: 40, out_of_sync: 8, unknown: 2 },
    by_health_status: { healthy: 45, progressing: 2, degraded: 2, unknown: 1 },
  },
  addons: { total_available: 15, total_deployments: 100, enabled_deployments: 85 },
};

const BOOTSTRAP_BANNER_TEXT = 'ArgoCD Bootstrap Application Issue';

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders loading state initially', () => {
    renderDashboard();
    expect(screen.getByText('Loading dashboard...')).toBeInTheDocument();
  });

  it('renders stats after data loads', async () => {
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // Stat cards
    expect(screen.getByText('10')).toBeInTheDocument();
    expect(screen.getByText('45/50 healthy')).toBeInTheDocument();
    expect(screen.getByText('15')).toBeInTheDocument();
    expect(screen.getByText('85/100')).toBeInTheDocument();

    // Health bars
    expect(screen.getByText('Application Health')).toBeInTheDocument();
    // LW-7: Cluster Connectivity bar removed — connectivity is now shown in Total Clusters stat
  });

  // BUG-040: clicking the "N disconnected cluster(s)" button under
  // "Needs Attention" must deep-link to /clusters?status=disconnected so
  // the Clusters page's filter resolves to the SAME set of clusters the
  // headline count refers to (any managed cluster ArgoCD does not report
  // as "Successful" / "Connected"). The previous implementation landed on
  // a "failed-only" filter and showed 0 rows when the cluster was actually
  // "missing" or "unknown" — a count vs list mismatch that read as a bug.
  it('disconnected-clusters button deep-links to ?status=disconnected (BUG-040)', async () => {
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // The mocked /dashboard/stats returns disconnected_from_argocd: 2 so
    // the button is rendered with the plural label.
    const btn = await screen.findByRole('button', {
      name: /2 disconnected clusters/i,
    });
    fireEvent.click(btn);

    expect(mockNavigate).toHaveBeenCalledWith('/clusters?status=disconnected');
  });
});

// V2-cleanup-61.3 (B1): a fresh install with 0 clusters must NOT show the
// green "All systems operational" success banner — that's a false-positive
// reading of "everything's fine" when nothing has been connected yet. This
// is also where the first-run wizard's every exit path lands (Go to
// Dashboard / Skip / the X-button escape all navigate to /dashboard), so
// this neutral state is the "what do I do next" guidance for that moment.
describe('Dashboard — empty install (B1, no false-green)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders a neutral "nothing connected yet" state instead of "All systems operational" when there are 0 clusters', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 0, connected_to_argocd: 0, disconnected_from_argocd: 0 },
      applications: {
        total: 0,
        by_sync_status: { synced: 0, out_of_sync: 0, unknown: 0 },
        by_health_status: { healthy: 0, progressing: 0, degraded: 0, unknown: 0 },
      },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    expect(screen.getByText('Nothing connected yet')).toBeInTheDocument();
    expect(screen.queryByText('All systems operational')).not.toBeInTheDocument();
    // "0/0 healthy" must not appear styled as a success stat either — the
    // whole stats grid is skipped for the empty state.
    expect(screen.queryByText('0/0 healthy')).not.toBeInTheDocument();
  });

  it('gives next-step guidance: register a cluster or browse the Marketplace', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 0, connected_to_argocd: 0, disconnected_from_argocd: 0 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Nothing connected yet')).toBeInTheDocument();
    });

    const registerBtn = screen.getByRole('button', { name: /register a cluster/i });
    fireEvent.click(registerBtn);
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');

    const marketplaceBtn = screen.getByRole('button', { name: /browse the marketplace/i });
    fireEvent.click(marketplaceBtn);
    expect(mockNavigate).toHaveBeenCalledWith('/addons?tab=marketplace');
  });

  it('still shows the normal dashboard (stat cards, no empty state) when at least one cluster exists', async () => {
    // Earlier tests in this describe override getDashboardStats with
    // .mockResolvedValue (not Once), which persists past vi.clearAllMocks()
    // (that only clears call history, not the implementation) — restore the
    // normal 10-cluster stats explicitly rather than relying on the module
    // mock's original default.
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    expect(screen.queryByText('Nothing connected yet')).not.toBeInTheDocument();
    expect(screen.getByText('Total Clusters')).toBeInTheDocument();
  });

  it('shows "All systems operational" (green) only when there is real, healthy data', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected_to_argocd: 5, disconnected_from_argocd: 0 },
      applications: {
        total: 20,
        by_sync_status: { synced: 20, out_of_sync: 0, unknown: 0 },
        by_health_status: { healthy: 20, progressing: 0, degraded: 0, unknown: 0 },
      },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('All systems operational')).toBeInTheDocument();
    });
    expect(screen.queryByText('Nothing connected yet')).not.toBeInTheDocument();
  });
});

// connhealth-2: the inline bootstrap banner is now gated to genuinely
// BLOCKING bootstrap states only. Softer / transient states (e.g. Unknown)
// are surfaced through the notification bell instead, so they must NOT show
// the inline banner.
describe('isBootstrapBlocking (banner gate)', () => {
  it('blocking set is exactly Error/Missing/Degraded', () => {
    expect([...BOOTSTRAP_BLOCKING_HEALTH]).toEqual(['Error', 'Missing', 'Degraded']);
  });

  it('returns true for blocking states', () => {
    expect(isBootstrapBlocking('Error')).toBe(true);
    expect(isBootstrapBlocking('Missing')).toBe(true);
    expect(isBootstrapBlocking('Degraded')).toBe(true);
  });

  it('returns false for softer / non-blocking states', () => {
    expect(isBootstrapBlocking('Unknown')).toBe(false);
    expect(isBootstrapBlocking('Progressing')).toBe(false);
    expect(isBootstrapBlocking('Healthy')).toBe(false);
    expect(isBootstrapBlocking(undefined)).toBe(false);
    expect(isBootstrapBlocking(null)).toBe(false);
    expect(isBootstrapBlocking('')).toBe(false);
  });
});

describe('Dashboard bootstrap banner gating (connhealth-2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the inline banner for a blocking state (Error)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      bootstrap_app_health: 'Error',
      bootstrap_app_sync: 'OutOfSync',
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });
    expect(screen.getByText(BOOTSTRAP_BANNER_TEXT)).toBeInTheDocument();
  });

  it('does NOT show the inline banner for a softer state (Unknown) — bell-only', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      bootstrap_app_health: 'Unknown',
      bootstrap_app_sync: 'Unknown',
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });
    expect(screen.queryByText(BOOTSTRAP_BANNER_TEXT)).not.toBeInTheDocument();
  });

  it('does NOT show the inline banner when Healthy', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      bootstrap_app_health: 'Healthy',
      bootstrap_app_sync: 'Synced',
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });
    expect(screen.queryByText(BOOTSTRAP_BANNER_TEXT)).not.toBeInTheDocument();
  });
});

// LW-1: Dashboard needs-attention filter — correct rule (addon health ≠ cluster health)
describe('Dashboard needs-attention filter (LW-1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // The locked rule:
  // 1. connection_status == Failed → needs attention
  // 2. connection_status == Unknown AND total > 0 → needs attention
  // 3. connection_status == Connected but healthy == 0 AND total > 0 → needs attention
  // 4. Otherwise NOT needs attention (addon health ≠ cluster health)

  it('Connected with 29/30 healthy → NOT in needs-attention', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Successful', server_url: 'https://prod' }],
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: Array.from({ length: 30 }, (_, i) => ({
        addon_name: `addon-${i}`,
        cells: { prod: { health: i === 0 ? 'Degraded' : 'Healthy' } },
      })),
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // The card for "prod" should NOT appear under "Clusters Needing Attention"
    // because connection is Successful and 29/30 healthy ≠ needs attention.
    // The "Clusters Needing Attention" section won't render at all when the list is empty.
    // Verify the cluster card does NOT appear in the attention section by checking
    // that the "Clusters Needing Attention" heading is not present (empty list).
    expect(screen.queryByText('Clusters Needing Attention')).not.toBeInTheDocument();
  });

  it('Failed → IN needs-attention', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Failed', server_url: 'https://prod' }],
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [{ addon_name: 'addon-1', cells: { prod: { health: 'Healthy' } } }],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // The cluster card should appear under "Clusters Needing Attention" because
    // connection is Failed (regardless of addon health).
    expect(screen.getByText('Clusters Needing Attention')).toBeInTheDocument();
  });

  it('Unknown with addons → IN needs-attention', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Unknown', server_url: 'https://prod' }],
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [{ addon_name: 'addon-1', cells: { prod: { health: 'Healthy' } } }],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    expect(screen.getByText('Clusters Needing Attention')).toBeInTheDocument();
  });

  it('Unknown with zero addons → NOT in needs-attention', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Unknown', server_url: 'https://prod' }],
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // Unknown with total == 0 → NOT needs attention (nothing to infer from)
    expect(screen.queryByText('Clusters Needing Attention')).not.toBeInTheDocument();
  });

  it('Connected with 0 healthy out of N → IN needs-attention', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Successful', server_url: 'https://prod' }],
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        { addon_name: 'addon-1', cells: { prod: { health: 'Degraded' } } },
        { addon_name: 'addon-2', cells: { prod: { health: 'Degraded' } } },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // Connected but 0/2 healthy → IN needs attention (all addons unhealthy)
    expect(screen.getByText('Clusters Needing Attention')).toBeInTheDocument();
  });
});

// LW-7: Dashboard connectivity widget — fold disconnected count into Total Clusters stat,
// drop redundant "Cluster Connectivity" bar
describe('Dashboard connectivity widget (LW-7)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('when disconnected == 0, Total Clusters stat shows no subtitle and Cluster Connectivity bar does not render', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected_to_argocd: 5, disconnected_from_argocd: 0 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // Total Clusters stat should be visible with value 5
    expect(screen.getByText('Total Clusters')).toBeInTheDocument();
    expect(screen.getByText('5')).toBeInTheDocument();

    // No subtitle about disconnected clusters
    expect(screen.queryByText(/disconnected/i)).not.toBeInTheDocument();

    // The full-width "Cluster Connectivity" HealthBar should not render
    expect(screen.queryByText('Cluster Connectivity')).not.toBeInTheDocument();
  });

  it('when disconnected > 0, Total Clusters stat shows disconnected count as subtitle', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 10, connected_to_argocd: 8, disconnected_from_argocd: 2 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // Total Clusters stat should show the count
    expect(screen.getByText('Total Clusters')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();

    // The subtitle "2 disconnected" should be visible
    expect(screen.getByText('2 disconnected')).toBeInTheDocument();

    // Still no full-width "Cluster Connectivity" bar
    expect(screen.queryByText('Cluster Connectivity')).not.toBeInTheDocument();
  });

  it('clicking Total Clusters stat when disconnected > 0 deep-links to /clusters?status=disconnected', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 10, connected_to_argocd: 8, disconnected_from_argocd: 2 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('2 disconnected')).toBeInTheDocument();
    });

    // The StatCard with "Total Clusters" should be clickable
    const statCard = screen.getByText('Total Clusters').closest('[role="button"]');
    expect(statCard).toBeInTheDocument();
    fireEvent.click(statCard!);

    expect(mockNavigate).toHaveBeenCalledWith('/clusters?status=disconnected');
  });

  it('clicking Total Clusters stat when disconnected == 0 navigates to /clusters (no filter)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected_to_argocd: 5, disconnected_from_argocd: 0 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Total Clusters')).toBeInTheDocument();
    });

    const statCard = screen.getByText('Total Clusters').closest('[role="button"]');
    fireEvent.click(statCard!);

    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });
});
