import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';
import { ApiError } from '@/services/api';

// Walk 2, serious 2: the cluster page said "Synced" and "No differences" with
// full confidence while the labels were different right now, because it showed
// the last background check without saying so. Now the pill says it is the
// last check's answer, the Diff starts a fresh check, and the words say when.

function authFor(role: 'admin' | 'operator' | 'viewer') {
  return {
    token: 't', username: role, role, login: vi.fn(), logout: vi.fn(),
    isAuthenticated: true, isAdmin: role === 'admin', loading: false, error: null,
  };
}

const mockGetClusterComparison = vi.fn();
const mockReconcile = vi.fn();
const mockTestConnection = vi.fn();
const mockTestProvider = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusterComparison: (...a: unknown[]) => mockGetClusterComparison(...a),
      getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
      getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
      getAIStatus: vi.fn().mockResolvedValue({ enabled: false }),
      getClusterHistory: vi.fn().mockResolvedValue({ history: [] }),
      getClusterChanges: vi.fn().mockResolvedValue({ changes: [] }),
      testConnection: (...a: unknown[]) => mockTestConnection(...a),
      testProvider: (...a: unknown[]) => mockTestProvider(...a),
    },
    fetchTrackedPRs: vi.fn().mockResolvedValue({ prs: [] }),
    reconcileCluster: (...a: unknown[]) => mockReconcile(...a),
  };
});

function baseComparisonResponse(overrides?: {
  label_drift?: {
    added?: string[];
    removed?: string[];
    changed?: string[];
  };
}) {
  const lastReconcile: {
    time: string;
    outcome: 'succeeded' | 'failed' | 'skipped';
    message?: string;
    label_drift?: {
      added?: string[];
      removed?: string[];
      changed?: string[];
    };
  } = {
    time: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
    outcome: 'succeeded',
  };

  if (overrides?.label_drift) {
    lastReconcile.label_drift = overrides.label_drift;
  }

  return {
    cluster: {
      name: 'prod-eu',
      labels: { env: 'prod' },
      server_version: '1.28',
      connection_status: 'connected',
      addon_secrets_ready: true,
      last_reconcile: lastReconcile,
    },
    git_total_addons: 1,
    git_enabled_addons: 1,
    git_disabled_addons: 0,
    argocd_total_applications: 1,
    argocd_healthy_applications: 1,
    argocd_synced_applications: 1,
    argocd_degraded_applications: 0,
    argocd_out_of_sync_applications: 0,
    addon_comparisons: [
      {
        addon_name: 'ingress-nginx',
        git_configured: true,
        git_version: '4.7.0',
        git_enabled: true,
        environment_version: '4.7.0',
        has_version_override: false,
        argocd_deployed: true,
        argocd_deployed_version: '4.7.0',
        argocd_namespace: 'ingress',
        argocd_health_status: 'Healthy',
        status: 'healthy',
        issues: [],
      },
    ],
    total_healthy: 1,
    total_with_issues: 0,
    total_missing_in_argocd: 0,
    total_untracked_in_argocd: 0,
    total_disabled_in_git: 0,
  };
}


function renderAs(role: 'admin' | 'operator' | 'viewer', url = '/clusters/prod-eu') {
  return render(
    <AuthContext.Provider value={authFor(role)}>
      <MemoryRouter initialEntries={[url]}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('ClusterDetail — what the Synced pill and the Diff really say', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockReconcile.mockResolvedValue({ status: 'accepted', message: 'ok' });
  });
  afterEach(() => vi.useRealTimers());

  it('says the pill is the last check, with when', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderAs('viewer');
    await screen.findByText('prod-eu');
    expect(screen.getByTestId('sync-as-of-note').textContent).toBe('as of the last check, 5m ago');
    expect(screen.getByText('Last checked')).toBeInTheDocument();
    expect(screen.queryByText('Last sync')).not.toBeInTheDocument();
  });

  it('the no-differences sentence names the last check; the old sure-sounding sentence is gone', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderAs('viewer');
    await screen.findByText('prod-eu');
    fireEvent.click(screen.getByRole('button', { name: /^Diff$/ }));
    expect(
      screen.getByText("No differences found at the last check — this secret's addon labels matched Git."),
    ).toBeInTheDocument();
    expect(screen.getByTestId('diff-checked-at').textContent).toBe(
      'Checked 5m ago. Labels changed since then show up at the next check.',
    );
    expect(screen.queryByText("No differences — this secret's addon labels match Git.")).not.toBeInTheDocument();
  });

  it('opening the Diff as an operator starts a fresh check', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderAs('operator');
    await screen.findByText('prod-eu');
    fireEvent.click(screen.getByRole('button', { name: /^Diff$/ }));
    await waitFor(() => expect(mockReconcile).toHaveBeenCalledWith('prod-eu'));
    expect(screen.getByTestId('diff-checked-at').textContent).toBe('Checking now…');
  });

  it('a viewer opening the Diff does not start a check (the server would refuse it)', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderAs('viewer');
    await screen.findByText('prod-eu');
    fireEvent.click(screen.getByRole('button', { name: /^Diff$/ }));
    expect(mockReconcile).not.toHaveBeenCalled();
  });
});

describe('ClusterDetail — Diagnostics when the checks are refused or turned away', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
  });

  it("Sharko's own request limit is not shown as a failing connection and does not point at Settings", async () => {
    const limited = new ApiError(429, { error: 'rate limit exceeded', code: 'sharko_rate_limit' }, 'Too Many Requests');
    mockTestConnection.mockRejectedValue(limited);
    mockTestProvider.mockRejectedValue(limited);
    renderAs('admin', '/clusters/prod-eu?section=diagnostics');
    const note = await screen.findByTestId('diag-unchecked');
    expect(note.textContent).toContain('Sharko could not check its connections just now');
    expect(note.textContent).toContain(
      'Sharko is getting too many requests from your address right now. Try again in a moment.',
    );
    expect(screen.queryByText('A connection Sharko depends on is currently failing')).not.toBeInTheDocument();
    expect(screen.queryByText(/Fix it in Settings/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Secrets Provider/)).not.toBeInTheDocument();
    expect(screen.queryByText(/rate limit exceeded/)).not.toBeInTheDocument();
  });

  it('a viewer refused (403) sees "not allowed", not a failing connection', async () => {
    const refused = new ApiError(403, { error: "action 'connection.test' requires role 'operator', you have 'viewer'" }, 'Forbidden');
    mockTestConnection.mockRejectedValue(refused);
    mockTestProvider.mockRejectedValue(refused);
    renderAs('viewer', '/clusters/prod-eu?section=diagnostics');
    const note = await screen.findByTestId('diag-unchecked');
    expect(note.textContent).toContain('Your role is not allowed to do this. Ask an operator or an admin.');
    expect(screen.queryByText('A connection Sharko depends on is currently failing')).not.toBeInTheDocument();
    expect(screen.queryByText(/requires role/)).not.toBeInTheDocument();
  });

  it('a really failing connection is still shown as failing', async () => {
    mockTestConnection.mockResolvedValue({
      git: { status: 'error', message: "Sharko can't reach your Git host — the credentials were rejected." },
      argocd: { status: 'ok' },
    });
    mockTestProvider.mockResolvedValue({ status: 'connected' });
    renderAs('admin', '/clusters/prod-eu?section=diagnostics');
    expect(await screen.findByText('A connection Sharko depends on is currently failing')).toBeInTheDocument();
    expect(screen.queryByTestId('diag-unchecked')).not.toBeInTheDocument();
  });
});

// Walk 2, L7 / walk 10 #2: the menu said "Addons 1" (it counted Sharko's own
// connectivity check) while the page said "0 All Addons".
describe('ClusterDetail — the Addons count in the menu', () => {
  it('counts what the page counts, not Sharko\'s own connectivity check', async () => {
    const base = baseComparisonResponse();
    base.addon_comparisons = [
      { ...base.addon_comparisons[0], addon_name: 'connectivity-check', status: 'sharko_system' as never },
    ];
    base.total_healthy = 0;
    mockGetClusterComparison.mockResolvedValue(base);
    renderAs('viewer');
    await screen.findByText('prod-eu');
    const nav = screen.getByRole('button', { name: /^Addons/ });
    expect(nav.textContent).not.toMatch(/Addons\s*1\b/);
  });
});
