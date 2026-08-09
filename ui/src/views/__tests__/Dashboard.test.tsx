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
  api: {
    getObservability: vi.fn().mockResolvedValue(null),
    getVersionMatrix: vi.fn().mockResolvedValue(null),
    getAttentionItems: vi.fn().mockResolvedValue([]),
    getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
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

const BOOTSTRAP_BANNER_TEXT = 'Sharko Engine Issue';

describe('Dashboard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders loading state initially', () => {
    renderDashboard();
    expect(screen.getByText('Loading dashboard...')).toBeInTheDocument();
  });

  // S2 (walk day 7): the manual refresh control is a real labeled button
  // now — icon + visible "Refresh" text, same header position, spinner
  // swap while a background refresh is in flight — not a bare icon.
  it('shows a labeled Refresh button that spins while a manual refresh is in flight', async () => {
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });

    const refreshButton = screen.getByTestId('dashboard-refresh');
    expect(within(refreshButton).getByText('Refresh')).toBeInTheDocument();
    expect(refreshButton).not.toBeDisabled();

    fireEvent.click(refreshButton);
    expect(refreshButton).toBeDisabled();

    await waitFor(() => {
      expect(refreshButton).not.toBeDisabled();
    });
  });

  it('renders fleet status strip segments after data loads (dashboard-purpose decision, WQ-2: donuts + total legend row, S4)', async () => {
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });

    // Fleet Status Strip v2 — the Clusters/Applications segments show a
    // donut whose legend ends with a "total" row (S4 — same style as every
    // other row, no detached bold number); the per-state breakdown moved
    // to the segment's aria-label (and the donut's hover tooltip), not the
    // visible text (WQ-2 — the v1 text list overflowed and named an addon
    // nobody cared about).
    const clustersLegend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
    const clustersTotalRow = within(clustersLegend).getByText('total').closest('li');
    expect(clustersTotalRow).not.toBeNull();
    expect(within(clustersTotalRow as HTMLElement).getByText('10')).toBeInTheDocument();
    const clustersLabel = screen.getByTestId('fleet-strip-clusters').getAttribute('aria-label');
    expect(clustersLabel).toContain('8 connected');
    expect(clustersLabel).toContain('1 not connected');
    expect(clustersLabel).toContain('1 disconnected');

    const appsLegend = within(screen.getByTestId('fleet-strip-applications')).getByTestId('fleet-strip-legend');
    const appsTotalRow = within(appsLegend).getByText('total').closest('li');
    expect(appsTotalRow).not.toBeNull();
    expect(within(appsTotalRow as HTMLElement).getByText('50')).toBeInTheDocument();
    const appsLabel = screen.getByTestId('fleet-strip-applications').getAttribute('aria-label');
    expect(appsLabel).toContain('45 healthy');
    expect(appsLabel).toContain('5 not healthy');

    // Behind-catalog segment — no version-matrix data in this test's
    // default mocks, so it reads the calm zero-state, not a bare "0".
    expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('all on version');
  });

});

// Walk day 3 lock (formerly WQ-3 attention-move-badges), split into plain
// buckets by the scale-walk (S1): the detailed "Open issues" rows live
// inside Observability's Fleet Health section. Named cluster/addon rows +
// their deep links are tested there now (Observability.test.tsx) — the
// Dashboard keeps two small cards, one per bucket, tested below.
describe('Dashboard thin issues line (walk day 3, split into buckets S1)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the cluster-problem count and navigates to /clusters?status=disconnected on click (no addon problems in this fixture)', async () => {
    // baseStats default mock already has 1 failed + 1 missing = 2 cluster
    // problems; no addon problems in this fixture, so only the clusters
    // card renders.
    renderDashboard();

    const line = await screen.findByText(/2 clusters unreachable/i);
    expect(screen.queryByText(/app.*unhealthy/i)).not.toBeInTheDocument();
    fireEvent.click(line);
    expect(mockNavigate).toHaveBeenCalledWith('/clusters?status=disconnected');
  });

  // A freshly-observed degraded addon starts inside the 10-minute settling
  // window (see useAddonStates.tsx SETTLING_WINDOW_MS) — it must NOT bump
  // the apps-unhealthy count while settling. The confirmed-vs-settling
  // split itself (badSince timing) is unit-tested directly against
  // splitAddonStates in AttentionSection.test.tsx, where a fabricated
  // badSince can be placed outside the window without fighting real timers.
  it('a freshly-degraded addon (still settling) does NOT bump the count', async () => {
    (api.getAttentionItems as ReturnType<typeof vi.fn>).mockResolvedValue([
      { app_name: 'cert-manager-prod', addon_name: 'cert-manager', cluster: 'prod', health: 'Degraded', sync: 'Synced' },
    ]);
    renderDashboard();

    // Still just the 2 cluster problems from baseStats — the addon is
    // settling, not confirmed, so it isn't counted yet, and no apps card
    // renders at all.
    expect(await screen.findByText(/2 clusters unreachable/i)).toBeInTheDocument();
    expect(screen.queryByText(/app.*unhealthy/i)).not.toBeInTheDocument();
  });

  it('shows the singular form for a count of one', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected: 4, pending: 0, untested: 0, missing: 0, failed: 1 },
    });
    renderDashboard();

    expect(await screen.findByText(/1 cluster unreachable\b/i)).toBeInTheDocument();
    expect(screen.queryByText(/1 clusters unreachable/i)).not.toBeInTheDocument();
  });

  // The confirmed-vs-settling badSince clock lives client-side in
  // useAddonStates (started the moment a bad app is first observed — see
  // that file's honesty note), so getting a genuinely CONFIRMED (not
  // settling) app problem through a full render means moving the clock
  // forward past the 10-minute window, then forcing a re-render so
  // splitAddonStates re-evaluates against the later "now". The manual
  // refresh button does that (it re-polls addon state too — the item is
  // still degraded, so its badSince anchor is untouched, only its age is).
  it('shows both cards, each with its own link, once an addon problem clears the settling window', async () => {
    const startTime = Date.now();
    const dateSpy = vi.spyOn(Date, 'now').mockReturnValue(startTime);
    // Isolate from the previous test's mockResolvedValue override, which
    // persists past vi.clearAllMocks() (that only clears call history) —
    // this test needs baseStats's 2-cluster-problem fixture back.
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getAttentionItems as ReturnType<typeof vi.fn>).mockResolvedValue([
      { app_name: 'cert-manager-prod', addon_name: 'cert-manager', cluster: 'prod', health: 'Degraded', sync: 'Synced' },
    ]);
    renderDashboard();

    // Still settling at mount — only the clusters card shows.
    await screen.findByText(/2 clusters unreachable/i);
    expect(screen.queryByText(/app.*unhealthy/i)).not.toBeInTheDocument();

    dateSpy.mockReturnValue(startTime + 11 * 60 * 1000);
    fireEvent.click(screen.getByTestId('dashboard-refresh'));

    const appsLine = await screen.findByText(/1 app unhealthy/i);
    const clustersLine = await screen.findByText(/2 clusters unreachable/i);

    fireEvent.click(appsLine);
    expect(mockNavigate).toHaveBeenCalledWith('/observability#addon-health');
    fireEvent.click(clustersLine);
    expect(mockNavigate).toHaveBeenCalledWith('/clusters?status=disconnected');

    dateSpy.mockRestore();
  });

  it('shows the quiet "No open issues" line inside the Issues box when there are no confirmed problems', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-clusters')).toBeInTheDocument();
    });
    expect(screen.getByText('No open issues')).toBeInTheDocument();
    expect(screen.queryByText(/\d+ apps? unhealthy/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/\d+ clusters? unreachable/i)).not.toBeInTheDocument();
    expect(screen.queryByText('All systems operational')).not.toBeInTheDocument();
  });

  it('the old "Needs Attention" chip panel is gone', async () => {
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-clusters')).toBeInTheDocument();
    });
    expect(screen.queryByText('Needs Attention')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /disconnected cluster/i })).not.toBeInTheDocument();
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });

    expect(screen.queryByText('Nothing connected yet')).not.toBeInTheDocument();
    expect(screen.getByTestId('fleet-strip-clusters')).toBeInTheDocument();
  });

  // WQ-3: the green "All systems operational" banner is gone entirely —
  // a quiet work-queue page renders nothing at all when there's real,
  // healthy data (see 'Dashboard thin attention line (WQ-3)' below for the
  // renders-nothing-at-zero coverage).
  it('renders no banner text at all (quiet page) when there is real, healthy data', async () => {
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
      expect(screen.getByTestId('fleet-strip-clusters')).toBeInTheDocument();
    });
    expect(screen.queryByText('All systems operational')).not.toBeInTheDocument();
    expect(screen.queryByText('Nothing connected yet')).not.toBeInTheDocument();
    // Quiet, healthy data still shows the Issues box, just with its
    // no-problems line, not the old glued "N open issues" total.
    expect(screen.getByText('No open issues')).toBeInTheDocument();
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
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
// FleetStatusStrip-only cluster wording checks. Named cluster attention
// rows (with reasons + deep links) moved to Observability (WQ-3) — see
// Observability.test.tsx for "Failed cluster shows a row with its reason"
// and "missing cluster shows a row" coverage.
describe('Dashboard cluster wording (FleetStatusStrip only)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });

    expect(screen.queryByRole('button', { name: /disconnected cluster/i })).not.toBeInTheDocument();
    // Neutral note instead — "connecting", not red. Now lives in the
    // Clusters segment's donut breakdown (aria-label + hover tooltip),
    // not a floating sentence (dashboard-purpose decision, WQ-1/WQ-2).
    expect(screen.getByTestId('fleet-strip-clusters').getAttribute('aria-label')).toContain('1 connecting');
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });

    expect(screen.queryByRole('button', { name: /disconnected cluster/i })).not.toBeInTheDocument();
    expect(screen.getByTestId('fleet-strip-clusters').getAttribute('aria-label')).toContain(
      '1 waiting for first addon',
    );
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

  it('clicking the behind-catalog segment navigates to /version-matrix?view=matrix&filter=behind-catalog (walk day 5: bare number opens the filtered matrix)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-upgrades');
    fireEvent.click(segment);
    expect(mockNavigate).toHaveBeenCalledWith('/version-matrix?view=matrix&filter=behind-catalog');
  });
});

// Fleet Status Strip exception emphasis (dashboard-purpose decision, WQ-1
// → WQ-2, pies real-labeled per walk day 3): the strip states facts, never
// color alone. A "disconnected" count shows up as a legend row (label +
// count, queryable by text — not tooltip-only, walk day 3) plus the
// plain-English breakdown in the aria-label, not as red-toned inline text.
// Only the Upgrades segment (no pie) still uses a text tone.
describe('Fleet Status Strip exception styling (WQ-1 / WQ-2 / walk day 3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('clusters segment: a healthy fleet (no missing/failed) draws no failed/not-connected legend row', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
    });
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-clusters');
    await waitFor(() => expect(segment.textContent).toContain('5'));
    const legend = within(segment).getByTestId('fleet-strip-legend');
    expect(within(legend).getByText('connected')).toBeInTheDocument();
    expect(within(legend).queryByText('not connected')).not.toBeInTheDocument();
    expect(within(legend).queryByText('disconnected')).not.toBeInTheDocument();
  });

  it('clusters segment: missing/failed clusters each get their own legend row, named in the aria-label', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
    });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-clusters');
    const legend = await waitFor(() => within(segment).getByTestId('fleet-strip-legend'));
    expect(within(legend).getByText('not connected')).toBeInTheDocument();
    expect(within(legend).getByText('disconnected')).toBeInTheDocument();
    expect(segment.getAttribute('aria-label')).toContain('1 not connected');
    expect(segment.getAttribute('aria-label')).toContain('1 disconnected');
  });

  it('applications segment: not-healthy > 0 gets its own legend row, named in the aria-label', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-applications');
    const legend = await waitFor(() => within(segment).getByTestId('fleet-strip-legend'));
    expect(within(legend).getByText('not healthy')).toBeInTheDocument();
    expect(segment.getAttribute('aria-label')).toContain('5 not healthy');
  });

  it('applications segment: zero not-healthy draws no not-healthy legend row', async () => {
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
    const legend = await waitFor(() => within(segment).getByTestId('fleet-strip-legend'));
    expect(within(legend).getByText('healthy')).toBeInTheDocument();
    expect(within(legend).queryByText('not healthy')).not.toBeInTheDocument();
  });

  it('behind-catalog segment: count > 0 gets amber-toned text', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          newest_available: '1.14.0',
          cells: { 'prod-eu': { version: '1.12.0', health: 'Healthy', drift_from_catalog: true } },
        },
      ],
    });
    renderDashboard();

    const segment = await screen.findByTestId('fleet-strip-upgrades');
    await waitFor(() => expect(segment.textContent).toContain('1 behind'));
    const behindText = within(segment).getByText('1 behind');
    expect(behindText.className).toMatch(/text-amber-700/);
  });
});

// Page order (S1, walk day 7): the pies read FIRST, then Pull Requests,
// then one bottom row pairing Recent Activity with Issues. Supersedes the
// old WQ-1 order (PRs before the strip) — the maintainer's walk moved the
// strip to the top.
describe('Dashboard page order — pies, then PRs, then the bottom row (S1, walk day 7)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('FleetStatusStrip appears before PullRequestsPanel, which appears before Recent Activity and Issues, in DOM order', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    const { container } = renderDashboard();

    await screen.findByTestId('fleet-strip-clusters');
    const strip = screen.getByTestId('fleet-strip-clusters');
    const prPanel = screen.getByText('Pull Requests');
    const recentActivity = screen.getByText('Recent Activity');
    const issuesBox = screen.getByText('Issues');

    const allNodes = Array.from(container.querySelectorAll('*'));
    const stripIndex = allNodes.indexOf(strip);
    const prIndex = allNodes.indexOf(prPanel);
    const recentActivityIndex = allNodes.indexOf(recentActivity);
    const issuesIndex = allNodes.indexOf(issuesBox);

    expect(stripIndex).toBeGreaterThanOrEqual(0);
    expect(prIndex).toBeGreaterThanOrEqual(0);
    expect(recentActivityIndex).toBeGreaterThanOrEqual(0);
    expect(issuesIndex).toBeGreaterThanOrEqual(0);
    expect(stripIndex).toBeLessThan(prIndex);
    expect(prIndex).toBeLessThan(recentActivityIndex);
    expect(prIndex).toBeLessThan(issuesIndex);
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
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

// Behind-catalog stat (walk day 5 finding — replaces the old "N outdated"
// stat from Package 2 #1 / WQ-2). The maintainer's verdict on the old
// stat: it counted deployments behind the newest version seen upstream,
// which is trivia — the admin picked their version on purpose and already
// knows something newer exists. What matters is whether a running
// application has fallen behind THIS install's own catalog version
// (drift_from_catalog, already computed server-side per cell) — that's a
// drift the admin can actually act on. newest_available/isNewerVersion
// still power the version-matrix page's own "outdated" filter — they just
// no longer feed this dashboard number.
describe('Behind-catalog stat (walk day 5 finding, replaces Package 2 #1 / WQ-2 "N outdated")', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('counts cells whose drift_from_catalog is true — number only, no addon name', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    // Isolate from any rejected/overridden mock left behind by earlier
    // tests in this file (mockResolvedValue/mockRejectedValue persist
    // across vi.clearAllMocks() — it only clears call history).
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          // newest_available says something's newer upstream — irrelevant
          // to this stat now. Only drift_from_catalog counts.
          newest_available: '1.14.0',
          cells: {
            'prod-eu': { version: '1.12.0', health: 'Healthy', drift_from_catalog: true },
            'staging-us': { version: '1.14.0', health: 'Healthy', drift_from_catalog: false },
          },
        },
        {
          addon_name: 'external-dns',
          cells: { 'prod-eu': { version: '6.20.0', health: 'Healthy', drift_from_catalog: false } },
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-upgrades')).toBeInTheDocument();
    });
    // Only cert-manager/prod-eu carries drift_from_catalog: true.
    expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('1 behind');
    // No addon name anywhere in the segment.
    expect(screen.queryByText('cert-manager')).not.toBeInTheDocument();
  });

  it('does not name any addons even with several behind (bare count only)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    const rowBehindCatalog = (name: string) => ({
      addon_name: name,
      cells: { 'prod-eu': { version: '1.0.0', health: 'Healthy', drift_from_catalog: true } },
    });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        rowBehindCatalog('metrics-server'),
        rowBehindCatalog('cert-manager'),
        rowBehindCatalog('external-dns'),
        rowBehindCatalog('ingress-nginx'),
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('4 behind');
    });
    expect(screen.queryByText('metrics-server')).not.toBeInTheDocument();
    expect(screen.queryByText(/and 3 more/)).not.toBeInTheDocument();
  });

  it('shows a calm "all on version" message, not a bare 0, when nothing has drifted', async () => {
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
          cells: { 'prod-eu': { version: '1.14.0', health: 'Healthy', drift_from_catalog: false } },
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('all on version');
    });
  });

  it('reads calmly at zero even when an addon has a newer version upstream (the old trivia case)', async () => {
    // The exact case the maintainer called out: an addon has a newer
    // version available upstream, but nothing running is behind THIS
    // install's catalog version — the stat must not resurrect the old
    // "N outdated" reading here.
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    (api.getVersionMatrix as ReturnType<typeof vi.fn>).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          newest_available: '1.99.0',
          cells: { 'prod-eu': { version: '1.12.0', health: 'Healthy', drift_from_catalog: false } },
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('all on version');
    });
    expect(screen.queryByTestId('fleet-strip-upgrades')?.textContent).not.toContain('behind');
  });

  // Navigation itself is covered by 'Fleet Status Strip navigation (WQ-1)'
  // above (walk day 5: matrix, not the plain catalog).
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
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });
    expect(screen.queryByText('Quick Actions')).not.toBeInTheDocument();
    expect(screen.queryByText('Available Addons')).not.toBeInTheDocument();
    expect(screen.queryByText('Check Upgrade Impact')).not.toBeInTheDocument();
  });

  it('Recent Activity row reads "deployed <addon> on <cluster> · rev <sha> · <time>" when the server sends no action (S3 fallback, pinned)', async () => {
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
          // action intentionally absent — an older cached response, or a
          // server build that predates the field (S3 compatibility fallback).
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
    expect(screen.getByText('deployed')).toBeInTheDocument();
  });

  it('Recent Activity row reads "installed" for an app\'s earliest history entry (S3)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
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
          action: 'installed',
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Recent Activity')).toBeInTheDocument();
    });
    expect(screen.getByText('installed')).toBeInTheDocument();
    expect(screen.queryByText('deployed')).not.toBeInTheDocument();
  });

  it('Recent Activity row reads "updated" for a later history entry (S3)', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
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
          action: 'updated',
        },
      ],
    });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Recent Activity')).toBeInTheDocument();
    });
    expect(screen.getByText('updated')).toBeInTheDocument();
    expect(screen.queryByText('deployed')).not.toBeInTheDocument();
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

// WQ-3: the home-cluster identity card moved to SystemView entirely — see
// SystemView.test.tsx ("home-cluster identity card") for its rendering and
// degrade-per-field coverage. Confirm it's gone from the Dashboard.
describe('Home-cluster identity card left the Dashboard (WQ-3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('does not render the home-cluster card on the Dashboard', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    renderDashboard();

    await waitFor(() => {
      expect(screen.getByTestId('fleet-strip-clusters')).toBeInTheDocument();
    });
    expect(screen.queryByText("Sharko's home cluster")).not.toBeInTheDocument();
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
      const legend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
      const totalRow = within(legend).getByText('total').closest('li');
      expect(within(totalRow as HTMLElement).getByText('10')).toBeInTheDocument();
    });
    const appsLegend = within(screen.getByTestId('fleet-strip-applications')).getByTestId('fleet-strip-legend');
    const appsTotalRow = within(appsLegend).getByText('total').closest('li');
    expect(within(appsTotalRow as HTMLElement).getByText('50')).toBeInTheDocument();
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
      argoCDUnreachable: false,
      behindCatalog: { behindCount: 0 },
    });

    // The background refetch resolves with fresh (different) data — the
    // default mock's 10-cluster baseStats.
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });

    renderDashboard();

    // Instant paint from cache: no spinner, cached numbers visible without
    // waiting on any fetch.
    expect(screen.queryByText('Loading dashboard...')).not.toBeInTheDocument();
    let legend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
    let totalRow = within(legend).getByText('total').closest('li');
    expect(within(totalRow as HTMLElement).getByText('3')).toBeInTheDocument();

    // Background refresh lands and quietly replaces the stale number.
    await waitFor(() => {
      legend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
      totalRow = within(legend).getByText('total').closest('li');
      expect(within(totalRow as HTMLElement).getByText('10')).toBeInTheDocument();
    });
  });
});

// Dashboard header and width polish (SSF-7): the hero drops the banner
// image and the "Sharko" h1 in favor of a plain "Managed clusters"
// heading, and the page container widens from max-w-screen-xl to the
// exact 1536px max-w-screen-2xl class — same width in both the normal
// and the "nothing connected yet" empty state.
//
// Kept as the LAST describe block in this file on purpose: a couple of
// its cases call the persistent `.mockResolvedValue` (not `Once`) on
// api.getDashboardStats, which — per this file's own established
// pattern (see the "empty install" and "stale-while-refresh" blocks
// above) — survives `vi.clearAllMocks()` and would otherwise leak into
// any test after it that relies on the module's default baseStats mock.
describe('Dashboard header and width polish (SSF-7)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders "Managed clusters" as the h1 heading, not "Sharko"', async () => {
    renderDashboard();

    await waitFor(() => {
      expect(
        screen.getByRole('heading', { level: 1, name: 'Managed clusters' }),
      ).toBeInTheDocument();
    });
    expect(screen.queryByText('Sharko')).not.toBeInTheDocument();
  });

  it('no longer renders the hero banner image', async () => {
    const { container } = renderDashboard();

    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });
    // A literal <img> tag, not the status-strip donut charts' unrelated
    // role="img" spans (their tooltip-trigger wrapper) — those are expected
    // to stay and would false-fail a plain queryByRole('img') check.
    expect(container.querySelector('img')).not.toBeInTheDocument();
  });

  it('carries the max-w-screen-2xl page width on the normal state', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue(baseStats);
    (api.getClusters as ReturnType<typeof vi.fn>).mockResolvedValue({ clusters: [] });
    const { container } = renderDashboard();

    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: 'Managed clusters' })).toBeInTheDocument();
    });
    expect(container.querySelector('.max-w-screen-2xl')).toBeInTheDocument();
    expect(container.querySelector('.max-w-screen-xl')).not.toBeInTheDocument();
  });

  it('carries the max-w-screen-2xl page width on the "nothing connected yet" empty state', async () => {
    (api.getDashboardStats as ReturnType<typeof vi.fn>).mockResolvedValue({
      ...baseStats,
      clusters: zeroClusterStats,
    });
    const { container } = renderDashboard();

    await waitFor(() => {
      expect(screen.getByText('Nothing connected yet')).toBeInTheDocument();
    });
    expect(container.querySelector('.max-w-screen-2xl')).toBeInTheDocument();
    expect(container.querySelector('.max-w-screen-xl')).not.toBeInTheDocument();
  });
});
