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
    expect(screen.getByText('Cluster Connectivity')).toBeInTheDocument();
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
