import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Dashboard, isBootstrapBlocking, BOOTSTRAP_BLOCKING_HEALTH, DASHBOARD_CACHE_KEY } from '@/views/Dashboard';
import { api } from '@/services/api';
// v1.21 Bundle 3 — Dashboard now consumes addon state via the unified
// provider. Tests have to mount it inside one or the hook throws.
import { AddonStatesProvider } from '@/hooks/useAddonStates';
import { setCached } from '@/lib/viewCache';

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
  // Dashboard.tsx defensively re-normalizes argocd.version through this
  // helper (belt-and-suspenders on top of api.getConfig()'s own
  // normalization — see services/api.ts). Since this whole module is
  // mocked, the mock has to provide the real implementation too, not a
  // stub, or Dashboard's defensive coercion silently no-ops in tests.
  extractArgocdVersionString: (raw: unknown): string | undefined => {
    if (typeof raw === 'string') return raw;
    if (raw && typeof raw === 'object') {
      const version = (raw as { Version?: unknown }).Version;
      if (typeof version === 'string') return version;
    }
    return undefined;
  },
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

  it('renders fleet status strip segments after data loads (dashboard-purpose decision, WQ-1)', async () => {
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Sharko')).toBeInTheDocument();
    });

    // Fleet Status Strip — one slim row of clickable segments, replacing
    // the three stat cards (dashboard-purpose decision, WQ-1). Each
    // segment states its facts as plain text, not a labeled-chip layout.
    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('10 clusters');
    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('8 connected');
    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('1 not connected');
    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('1 disconnected');
    expect(screen.getByTestId('fleet-strip-applications').textContent).toContain('50 apps');
    expect(screen.getByTestId('fleet-strip-applications').textContent).toContain('45 healthy');
    expect(screen.getByTestId('fleet-strip-applications').textContent).toContain('5 not healthy');
    // Upgrades segment — no version-matrix data in this test's default
    // mocks, so it degrades to the "no data yet" state rather than a fake 0.
    expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('No version data yet');
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

  it('still shows the normal dashboard (fleet strip, no empty state) when at least one cluster exists', async () => {
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
    expect(screen.getByTestId('fleet-strip-clusters')).toBeInTheDocument();
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
    // Clusters segment of the Fleet Status Strip (dashboard-purpose
    // decision, WQ-1), not a floating sentence.
    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('1 connecting');
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
    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('1 waiting');
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

// The old stat cards are gone (dashboard-purpose decision, WQ-1) — the
// Fleet Status Strip replaces Total Clusters / Applications / Upgrades
// with one slim clickable row.
describe('Dashboard — old stat cards are gone (WQ-1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders no "Total Clusters" card and none of the old stat-card testids', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-clusters')).toBeInTheDocument();
    });

    expect(screen.queryByText('Total Clusters')).not.toBeInTheDocument();
    expect(screen.queryByTestId('stat-clusters-total')).not.toBeInTheDocument();
    expect(screen.queryByTestId('stat-applications-total')).not.toBeInTheDocument();
    expect(screen.queryByTestId('stat-upgrades-outdated')).not.toBeInTheDocument();
  });
});

// Fleet Status Strip — clicking a segment navigates; numbers are doors
// (dashboard-purpose decision, WQ-1).
describe('Fleet Status Strip navigation (WQ-1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('clicking the Clusters segment navigates to /clusters, regardless of disconnected count', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
    });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-clusters');
    fireEvent.click(segment);
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });

  it('clicking the Applications segment navigates to /observability#addon-health', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-applications');
    fireEvent.click(segment);
    expect(mockNavigate).toHaveBeenCalledWith('/observability#addon-health');
  });

  it('clicking the Upgrades segment navigates to /version-matrix?view=matrix', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-upgrades');
    fireEvent.click(segment);
    expect(mockNavigate).toHaveBeenCalledWith('/version-matrix?view=matrix');
  });
});

// Fleet Status Strip exception text emphasis (dashboard-purpose decision,
// WQ-1): the strip states facts in a quiet muted color; only a broken or
// outdated count gets amber/red text. This is a much smaller vocabulary
// than the Needs Attention severity model on purpose — no settling window,
// no 5-state color table, that logic stays in the banner/attention layer.
describe('Fleet Status Strip exception styling (WQ-1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('clusters segment: a healthy fleet (no missing/failed) has no red-toned text', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-clusters');
    await waitFor(() => expect(segment.textContent).toContain('5 clusters'));
    expect(segment.innerHTML).not.toMatch(/text-red-700/);
  });

  it('clusters segment: missing/failed clusters get red-toned text on that count', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
    });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-clusters');
    await waitFor(() => expect(segment.textContent).toContain('1 disconnected'));
    const disconnectedText = within(segment).getByText('1 disconnected');
    expect(disconnectedText.className).toMatch(/text-red-700/);
  });

  it('applications segment: not-healthy > 0 gets red-toned text', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-applications');
    await waitFor(() => expect(segment.textContent).toContain('5 not healthy'));
    const notHealthyText = within(segment).getByText('5 not healthy');
    expect(notHealthyText.className).toMatch(/text-red-700/);
  });

  it('applications segment: zero not-healthy has no red-toned text', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      applications: {
        total: 20,
        by_sync_status: { synced: 20, out_of_sync: 0, unknown: 0 },
        by_health_status: { healthy: 20, progressing: 0, degraded: 0, unknown: 0 },
      },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-applications');
    await waitFor(() => expect(segment.textContent).toContain('0 not healthy'));
    expect(segment.innerHTML).not.toMatch(/text-red-700/);
  });

  it('upgrades segment: outdated > 0 gets amber-toned text', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          newest_available: '1.14.0',
          cells: { 'prod-eu': { version: '1.12.0', health: 'Healthy' } },
        },
      ],
    });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-upgrades');
    await waitFor(() => expect(segment.textContent).toContain('1 outdated'));
    const outdatedText = within(segment).getByText('1 outdated');
    expect(outdatedText.className).toMatch(/text-amber-700/);
  });
});

// Page order (dashboard-purpose decision, WQ-1): PullRequestsPanel is
// promoted to the first content surface after Needs Attention — it must
// appear BEFORE the Fleet Status Strip in DOM order.
describe('Dashboard page order — PRs before the fleet strip (WQ-1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('PullRequestsPanel appears before FleetStatusStrip in DOM order', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    const { container } = renderDashboard();

    await screen.findByTestId('fleet-strip-clusters');
    const prPanel = screen.getByText('Pull Requests');
    const strip = screen.getByTestId('fleet-strip-clusters');

    const allNodes = Array.from(container.querySelectorAll('*'));
    const prIndex = allNodes.indexOf(prPanel);
    const stripIndex = allNodes.indexOf(strip);

    expect(prIndex).toBeGreaterThanOrEqual(0);
    expect(stripIndex).toBeGreaterThanOrEqual(0);
    expect(prIndex).toBeLessThan(stripIndex);
  });
});

// Walk finding #2: "which apps are unhealthy" is answered by
// Observability's Addon Health Groups section (sorted by issue count
// first, lists per-app-per-cluster health) — not the plain catalog, which
// only speaks per-addon. Deep-links to the section's anchor.
// Applications segment destination (walk finding #2) is covered by
// 'Fleet Status Strip navigation (WQ-1)' above.

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

// Upgrades stat (Package 2 #1, walk finding #1) — "Outdated N", derived
// from the version matrix's per-row newest_available vs. each cell's
// deployed version, names the outdated addons in the subtitle instead of
// a plain "of Y have a newer version" sentence, so the card informs before
// any click. No explicit per-cell flag exists on the wire, so the
// Dashboard compares itself (see summarizeUpgrades/isNewerVersion).
describe('Upgrades stat card (Package 2 #1, walk finding #1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('counts deployments whose version is behind the row\'s newest_available, and names the addon at small N', async () => {
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
      expect(screen.getByTestId('fleet-strip-upgrades')).toBeInTheDocument();
    });
    // 1 of 2 checked deployments (cert-manager on prod-eu) is behind.
    expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('1 outdated');
    // Names the addon instead of a bare "of 2 have a newer version" line.
    expect(screen.getByText('cert-manager')).toBeInTheDocument();
  });

  it('names up to 3 outdated addons, and switches to "first and N more" past that', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    const rowWithUpgrade = (name: string) => ({
      addon_name: name,
      newest_available: '2.0.0',
      cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy' } },
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        rowWithUpgrade('metrics-server'),
        rowWithUpgrade('cert-manager'),
        rowWithUpgrade('external-dns'),
        rowWithUpgrade('ingress-nginx'),
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('metrics-server and 3 more')).toBeInTheDocument();
    });
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

  // Navigation itself is covered by 'Fleet Status Strip navigation (WQ-1)'
  // above (walk finding #1: matrix, not the plain catalog).
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

  // Regression: live dashboard white-screened with React error #31 (object
  // as child). Root cause was api.getConfig() advertising argocd.version as
  // a `string` when the server (internal/api/system.go handleGetConfig)
  // actually sends ArgoCD's full /api/version object verbatim. api.ts now
  // normalizes that in getConfig() itself, but this test mocks api.getConfig
  // directly (like the rest of this file), so it exercises Dashboard's own
  // defensive extractArgocdVersionString() call instead — proving the render
  // survives even if a caller of getConfig ever regresses to the raw shape.
  it('never crashes when argocd.version arrives as the real ArgoCD version object, and shows "—" when it lacks a Version key', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getHomeCluster as ReturnType<typeof vi.fn>).mockResolvedValue({
      available: true,
      kubernetes_version: 'v1.29.0',
      node_count: 3,
      nodes_ready: 3,
    });
    (api.health as ReturnType<typeof vi.fn>).mockResolvedValue({ status: 'healthy', version: '4.2.0' });
    (api.getConfig as ReturnType<typeof vi.fn>).mockResolvedValue({
      argocd: {
        connected: true,
        // The real wire shape: no Version key present (e.g. an older ArgoCD
        // build that doesn't report one). Must degrade to "—", not throw.
        version: { BuildDate: '2026-06-01T00:00:00Z', GitCommit: 'abc123' },
      },
    });
    (api.getFleetStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ server_version: '4.2.0', uptime: '3h12m' });

    expect(() => renderDashboard()).not.toThrow();

    await waitFor(() => {
      expect(screen.getByText("Sharko's home cluster")).toBeInTheDocument();
    });
    // ArgoCD version cell falls back to "—" since the mocked object has no
    // Version key — and critically, the object itself never reached JSX.
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });

  it('renders the extracted Version string when argocd.version arrives as the real ArgoCD version object', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getHomeCluster as ReturnType<typeof vi.fn>).mockResolvedValue({
      available: true,
      kubernetes_version: 'v1.29.0',
      node_count: 3,
      nodes_ready: 3,
    });
    (api.health as ReturnType<typeof vi.fn>).mockResolvedValue({ status: 'healthy', version: '4.2.0' });
    (api.getConfig as ReturnType<typeof vi.fn>).mockResolvedValue({
      argocd: {
        connected: true,
        version: {
          BuildDate: '2026-06-01T00:00:00Z',
          GitCommit: 'abc123',
          Version: 'v2.11.0',
        },
      },
    });
    (api.getFleetStatus as ReturnType<typeof vi.fn>).mockResolvedValue({ server_version: '4.2.0', uptime: '3h12m' });

    expect(() => renderDashboard()).not.toThrow();

    expect(await screen.findByText('v2.11.0')).toBeInTheDocument();
  });
});

// perf S3 — the page used to block the whole frame behind Promise.all on
// all 8 fetches, so one slow/never-resolving call (e.g. an observability
// spike) held up even the stat cards. Now /dashboard/stats alone clears
// the spinner; everything else fills in on its own.
describe('Dashboard progressive paint (perf S3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('paints the frame and stat cards before a slow secondary fetch resolves, then catches up when it lands', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    // getObservability deliberately does not resolve until we say so —
    // the stat cards must not wait on it.
    let resolveObs: (v: unknown) => void = () => {};
    (api.getObservability as ReturnType<typeof vi.fn>).mockReturnValue(
      new Promise((resolve) => {
        resolveObs = resolve;
      }),
    );

    renderDashboard();

    // The fleet strip lands from /dashboard/stats alone, with observability
    // still in flight.
    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('10 clusters');
    });
    expect(screen.getByTestId('fleet-strip-applications').textContent).toContain('50 apps');
    // Recent Activity is still on its own empty/loading state — proof the
    // page rendered without it, not proof it never arrives.
    expect(screen.getByText('No recent sync activity')).toBeInTheDocument();

    // Now let observability land — the Recent Activity panel fills in on
    // its own, without a second full-page load.
    resolveObs({
      recent_syncs: [
        {
          timestamp: new Date().toISOString(),
          addon_name: 'cert-manager',
          cluster_name: 'prod-eu',
          revision: 'abcdef1234567890',
        },
      ],
    });
    await waitFor(() => {
      expect(screen.getByText('cert-manager')).toBeInTheDocument();
    });
  });
});

// perf S2 — a same-session revisit paints from the last successful load
// instantly (no spinner), then quietly refreshes in the background.
describe('Dashboard stale-while-refresh (perf S2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders cached data immediately on mount, then replaces it once the background refetch resolves', async () => {
    setCached(DASHBOARD_CACHE_KEY, {
      stats: {
        ...baseStats,
        clusters: { total: 3, connected: 3, pending: 0, untested: 0, missing: 0, failed: 0 },
      },
      recentSyncs: [],
      versionDrifts: [],
      clusters: [],
      argoCDUnreachable: false,
      homeCluster: null,
      sharkoVersion: undefined,
      argocdVersion: undefined,
      argocdConnected: false,
      uptime: undefined,
      upgrades: { withUpgrade: 0, checked: 0, namesWithUpgrade: [] },
    });

    // The background refetch resolves with fresh (different) data — the
    // default mock's 10-cluster baseStats.
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });

    renderDashboard();

    // Instant paint from cache: no spinner, cached numbers visible without
    // waiting on any fetch.
    expect(screen.queryByText('Loading dashboard...')).not.toBeInTheDocument();
    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('3 clusters');

    // Background refresh lands and quietly replaces the stale number.
    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('10 clusters');
    });
  });
});
