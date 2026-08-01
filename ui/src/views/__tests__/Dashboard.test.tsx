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

// Five-state cluster breakdown (dashboard UX review 2026-08-01, blocker
// B1) replaces the old binary connected_to_argocd/disconnected_from_argocd
// pair. total_deployments (the old fake "N/N" ratio) is gone too.
const zeroClusterStats = { total: 0, connected: 0, pending: 0, untested: 0, missing: 0, failed: 0 };

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
    // Home-cluster identity card (dashboard facelift, Package 3) — three
    // more thin reads the Dashboard fetches alongside everything else.
    health: vi.fn().mockResolvedValue({ status: 'healthy', version: '4.2.0' }),
    getConfig: vi.fn().mockResolvedValue({ argocd: { connected: true, version: '2.11.0' } }),
    getFleetStatus: vi.fn().mockResolvedValue({ server_version: '4.2.0', uptime: '3h12m' }),
    getDashboardStats: vi.fn().mockResolvedValue({
      connections: { total: 1, active: 'dev' },
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      applications: {
        total: 50,
        by_sync_status: { synced: 40, out_of_sync: 8, unknown: 2 },
        by_health_status: { healthy: 45, progressing: 2, degraded: 2, unknown: 1 },
      },
      addons: { total_available: 15, enabled_deployments: 85 },
    }),
    // migration-ui: Dashboard renders <MigrationBanner/>, which probes
    // migration status on mount. "empty" keeps it a no-op for existing
    // Dashboard tests (no active connection to migrate yet).
    getMigrationStatus: vi.fn().mockResolvedValue({ format: 'empty', migration_available: false, message: '' }),
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
// 1 failed + 1 missing == 2 "disconnected" (matches the old default mock's
// disconnected_from_argocd: 2, so existing counts/wording keep meaning).
const baseStats = {
  connections: { total: 1, active: 'dev' },
  clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
  applications: {
    total: 50,
    by_sync_status: { synced: 40, out_of_sync: 8, unknown: 2 },
    by_health_status: { healthy: 45, progressing: 2, degraded: 2, unknown: 1 },
  },
  addons: { total_available: 15, enabled_deployments: 85 },
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
    // Applications card (folded segmented health visualization, Package 2 #4)
    expect(screen.getByText('Applications')).toBeInTheDocument();
    // Upgrades card (Package 2 #1) replaces the old "Active Deployments"
    // plain count — no version-matrix data in this test's default mocks,
    // so it degrades to the "no data yet" state rather than a fake 0.
    expect(screen.getByText('Upgrades')).toBeInTheDocument();
    expect(screen.getByText('No version data yet')).toBeInTheDocument();
  });

  // BUG-040 (rebuilt for the dashboard UX review 2026-08-01 contract):
  // clicking the "N disconnected cluster(s)" pill EXPANDS the one
  // attention panel (cluster rows now live there, named + reasoned +
  // linked, instead of the old standalone "Clusters Needing Attention"
  // section). The panel's "View in Clusters" link still deep-links to
  // /clusters?status=disconnected — same target, reached one click deeper.
  it('expanding "disconnected clusters" shows named rows; "View in Clusters" deep-links to ?status=disconnected', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [
        { name: 'spoke-us', connection_status: 'Failed' },
        { name: 'spoke-eu', connection_status: 'missing' },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    const btn = await screen.findByRole('button', {
      name: /2 disconnected clusters/i,
    });
    fireEvent.click(btn);

    expect(await screen.findByText('spoke-us')).toBeInTheDocument();
    expect(screen.getByText('spoke-eu')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /view in clusters/i }));
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
      clusters: zeroClusterStats,
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
      clusters: zeroClusterStats,
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
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
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
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
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
    // The all-fine line carries a last-checked timestamp (Package 2 #7).
    expect(screen.getByText(/last checked/i)).toBeInTheDocument();
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

// Dashboard needs-attention cluster rows (rebuilt for the dashboard UX
// review 2026-08-01 contract). Cluster rows in the attention list are now
// ONLY for failed/missing connectivity (isClusterNeedsAttention) — the old
// rules 2/3 that inferred "needs attention" by crossing a cluster's
// connection status against its addons' health in the version matrix are
// gone. That was pure duplication of the real addon-health signal filed
// under a cluster heading (findings H2/H3): an "all addons down" cluster
// now shows up as ADDON rows (from the addon attention feed), never as a
// cluster row, and an Unknown/pending cluster is neutral, never red.
describe('Dashboard cluster attention rows (rebuilt LW-1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('Failed cluster → shown as a cluster attention row with its reason', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 1, connected: 0, pending: 0, untested: 0, missing: 0, failed: 1 },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Failed' }],
    });
    renderDashboard();

    const btn = await screen.findByRole('button', { name: /1 disconnected cluster/i });
    fireEvent.click(btn);

    expect(await screen.findByText('prod')).toBeInTheDocument();
    expect(screen.getByText(/argocd tried to reach this cluster and failed/i)).toBeInTheDocument();
  });

  it('Connected cluster, regardless of addon health → NOT a cluster attention row', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Successful' }],
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [{ addon_name: 'addon-1', cells: { prod: { health: 'Degraded' } } }],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    expect(screen.queryByRole('button', { name: /disconnected cluster/i })).not.toBeInTheDocument();
    expect(screen.queryByText('prod')).not.toBeInTheDocument();
  });

  it('Unknown (pending) cluster with addons deployed → NOT a cluster attention row (neutral, not red)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 1, connected: 0, pending: 1, untested: 0, missing: 0, failed: 0 },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Unknown' }],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    expect(screen.queryByRole('button', { name: /disconnected cluster/i })).not.toBeInTheDocument();
    // Neutral note instead — "connecting", not red. Now lives in the
    // Total Clusters stat card subtitle (Package 2 #2 — the 5-state
    // story), not a standalone floating line.
    expect(screen.getByText(/1 connecting/i)).toBeInTheDocument();
  });

  it('Unknown cluster with zero addons → neutral "waiting for its first addon" note', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 1, connected: 0, pending: 0, untested: 1, missing: 0, failed: 0 },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Unknown' }],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    expect(screen.queryByRole('button', { name: /disconnected cluster/i })).not.toBeInTheDocument();
    expect(screen.getByText(/1 waiting for its first addon/i)).toBeInTheDocument();
  });

  it('missing cluster → shown as a cluster attention row', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 1, connected: 0, pending: 0, untested: 0, missing: 1, failed: 0 },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'missing' }],
    });
    renderDashboard();

    const btn = await screen.findByRole('button', { name: /1 disconnected cluster/i });
    fireEvent.click(btn);
    expect(await screen.findByText('prod')).toBeInTheDocument();
  });
});

// Stat cards are permanently neutral (Package 2 #3) — no red/amber border
// color regardless of how bad the underlying numbers are. Informational
// text may still say "N disconnected"; only the wiring that would color
// the card is gone.
describe('Dashboard stat cards are permanently neutral (Package 2 #3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('Total Clusters card has no error-color border even with disconnected clusters', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText(/1 disconnected/)).toBeInTheDocument();
    });

    const card = screen.getByText('Total Clusters').closest('[role="button"]') as HTMLElement;
    expect(card.className).not.toMatch(/border-l-red/);
  });

  it('clicking Total Clusters when disconnected > 0 deep-links to /clusters?status=disconnected', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText(/1 disconnected/)).toBeInTheDocument();
    });

    const statCard = screen.getByText('Total Clusters').closest('[role="button"]');
    fireEvent.click(statCard!);
    expect(mockNavigate).toHaveBeenCalledWith('/clusters?status=disconnected');
  });

  it('clicking Total Clusters when disconnected == 0 navigates to /clusters (no filter)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Total Clusters')).toBeInTheDocument();
    });

    const statCard = screen.getByText('Total Clusters').closest('[role="button"]');
    fireEvent.click(statCard!);
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });

  it('when disconnected == 0, no subtitle text appears on Total Clusters', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Total Clusters')).toBeInTheDocument();
    });
    expect(screen.queryByText(/disconnected/i)).not.toBeInTheDocument();
  });
});

// Segmented per-app health blocks at small N (Package 2 #4) — folded into
// the Applications stat card rather than a separate full-width bar.
describe('Applications card — segmented blocks at small N (Package 2 #4)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders one block per app when there are <= 10 apps (from the version matrix)', async () => {
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        { addon_name: 'cert-manager', cells: { prod: { health: 'Healthy' } } },
        { addon_name: 'external-dns', cells: { prod: { health: 'Degraded' } } },
      ],
    });
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      applications: {
        total: 2,
        by_sync_status: { synced: 1, out_of_sync: 1, unknown: 0 },
        by_health_status: { healthy: 1, progressing: 0, degraded: 1, unknown: 0 },
      },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('1/2 healthy')).toBeInTheDocument();
    });

    expect(screen.getByTitle('cert-manager on prod: Healthy')).toBeInTheDocument();
    expect(screen.getByTitle('external-dns on prod: Degraded')).toBeInTheDocument();
  });

  // Walk finding (dashboard UX review 2026-08-01, H1 follow-through): with
  // exactly 1 app, `flex-1` stretched the single block to a full-width red
  // bar — the same panic slab this card was rebuilt to kill. Blocks are now
  // a fixed width so one broken app reads as one small block.
  it('renders a fixed-width block, not a stretched flex-1 bar, for a single app', async () => {
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [{ addon_name: 'metrics-server', cells: { prod: { health: 'Degraded' } } }],
    });
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      applications: {
        total: 1,
        by_sync_status: { synced: 0, out_of_sync: 1, unknown: 0 },
        by_health_status: { healthy: 0, progressing: 0, degraded: 1, unknown: 0 },
      },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('0/1 healthy')).toBeInTheDocument();
    });

    const block = screen.getByTitle('metrics-server on prod: Degraded');
    expect(block.className).not.toContain('flex-1');
    expect(block.className).toContain('w-8');
  });
});

// Unreachable-banner honesty fix (Package 1): the ONLY signal is the
// getClusters() fetch itself failing, not a heuristic over connection
// status strings.
describe('ArgoCD unreachable banner (Package 1 rewire)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('does NOT show when clusters legitimately all read Failed (that PROVES ArgoCD answered)', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({
      clusters: [{ name: 'prod', connection_status: 'Failed' }],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });
    expect(screen.queryByText(/ArgoCD temporarily unreachable/i)).not.toBeInTheDocument();
  });

  it('shows when the clusters fetch itself fails', async () => {
    (api.getClusters as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('network error'));
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText(/ArgoCD temporarily unreachable/i)).toBeInTheDocument();
    });
  });
});

// Upgrades stat (Package 2 #1) — "X of Y have a newer version", derived
// from the version matrix's per-row newest_available vs. each cell's
// deployed version. No explicit per-cell flag exists on the wire, so the
// Dashboard compares itself (see summarizeUpgrades/isNewerVersion).
describe('Upgrades stat card (Package 2 #1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('counts deployments whose version is behind the row\'s newest_available', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          newest_available: '1.14.0',
          cells: {
            'prod-eu': { version: '1.12.0', health: 'Healthy' },
            'staging-us': { version: '1.14.0', health: 'Healthy' },
          },
        },
        {
          // No freshness data yet for this addon — excluded from both the
          // numerator and the denominator (we don't know, so we don't
          // guess "up to date").
          addon_name: 'external-dns',
          cells: { 'prod-eu': { version: '6.20.0', health: 'Healthy' } },
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Upgrades')).toBeInTheDocument();
    });
    // 1 of 2 checked deployments (cert-manager on prod-eu) is behind.
    expect(screen.getByText('1')).toBeInTheDocument();
    expect(screen.getByText('of 2 have a newer version')).toBeInTheDocument();
  });

  it('shows a muted "everything up to date" message, not a celebratory one, at zero', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          newest_available: '1.14.0',
          cells: { 'prod-eu': { version: '1.14.0', health: 'Healthy' } },
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Everything on the newest known version')).toBeInTheDocument();
    });
  });

  it('clicking the Upgrades card navigates to the version matrix', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue(null);
    renderDashboard();

    const card = await screen.findByText('Upgrades');
    fireEvent.click(card.closest('[role="button"]')!);
    expect(mockNavigate).toHaveBeenCalledWith('/version-matrix');
  });
});

// Page reorganization (Package 2): Quick Actions and Available Addons are
// gone entirely (maintainer's call); Recent Activity moved up and was
// rebuilt (panel-lens finding).
describe('Dashboard page order (Package 2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('no longer renders Quick Actions or Available Addons', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });
    expect(screen.queryByText('Quick Actions')).not.toBeInTheDocument();
    expect(screen.queryByText('Available Addons')).not.toBeInTheDocument();
    expect(screen.queryByText('Check Upgrade Impact')).not.toBeInTheDocument();
  });

  it('Recent Activity row reads "deployed <addon> on <cluster> · rev <sha> · <time>", no status dot', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getObservability as ReturnType<typeof vi.fn>).mockResolvedValue({
      recent_syncs: [
        {
          timestamp: new Date().toISOString(),
          duration: '2s',
          duration_secs: 2,
          app_name: 'cert-manager-prod-eu',
          addon_name: 'cert-manager',
          cluster_name: 'prod-eu',
          revision: 'abcdef1234567890',
          status: 'Succeeded',
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Recent Activity')).toBeInTheDocument();
    });
    expect(screen.getByText('cert-manager')).toBeInTheDocument();
    expect(screen.getByText(/on prod-eu/)).toBeInTheDocument();
    // Revision is short (7 chars), not the full SHA.
    expect(screen.getByText(/rev abcdef1/)).toBeInTheDocument();
    expect(screen.queryByText(/abcdef1234567890/)).not.toBeInTheDocument();
  });

  it('Recent Activity empty state uses the compact recipe (no mascot image)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history). The
    // previous test in this file sets a non-empty recent_syncs list.
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getObservability as ReturnType<typeof vi.fn>).mockResolvedValue(null);
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('No recent sync activity')).toBeInTheDocument();
    });
    expect(screen.queryByAltText('')).not.toBeInTheDocument();
  });
});

// Home-cluster identity card (Package 3) — data wiring from the three new
// reads (health, config, fleet status) plus the existing home-cluster
// probe. Full rendering detail is covered by HomeClusterCard's own tests;
// this just checks the Dashboard wires the fetched values through.
describe('Home-cluster identity card wiring (Package 3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders Sharko/ArgoCD versions and uptime from the new reads', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getHomeCluster as ReturnType<typeof vi.fn>).mockResolvedValue({
      available: true,
      kubernetes_version: 'v1.29.0',
      node_count: 3,
      nodes_ready: 3,
    });
    (api.health as ReturnType<typeof vi.fn>).mockResolvedValue({ status: 'healthy', version: '4.2.0' });
    (api.getConfig as ReturnType<typeof vi.fn>).mockResolvedValue({ argocd: { connected: true, version: '2.11.0' } });
    (api.getFleetStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ server_version: '4.2.0', uptime: '3h12m' });

    renderDashboard();

    expect(await screen.findByText('4.2.0')).toBeInTheDocument();
    expect(screen.getByText('2.11.0')).toBeInTheDocument();
    expect(screen.getByText('v1.29.0')).toBeInTheDocument();
    expect(screen.getByText('all nodes ready')).toBeInTheDocument();
    expect(screen.getByText(/up 3h12m/)).toBeInTheDocument();
  });

  it('degrades gracefully — missing fields render "—", the card never errors', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getHomeCluster as ReturnType<typeof vi.fn>).mockResolvedValue({
      available: false,
      message: 'only available when running in-cluster',
    });
    (api.health as ReturnType<typeof vi.fn>).mockResolvedValue(null);
    (api.getConfig as ReturnType<typeof vi.fn>).mockResolvedValue(null);
    (api.getFleetStatus as ReturnType<typeof vi.fn>).mockResolvedValue(null);

    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText("Sharko's home cluster")).toBeInTheDocument();
    });
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
    expect(screen.getByText('only available when running in-cluster')).toBeInTheDocument();
  });
});
