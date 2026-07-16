import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// V3 G2 — read-only live drift diff for managed clusters. Pins:
//  1. When label_drift is absent or empty, shows "Labels in sync" green banner.
//  2. When label_drift has content (OutOfSync from G1), shows an amber drift diff with keys.
//  3. Renders added/removed/changed label keys with +/-/~ markers (reuses DryRunPreview diff colors).
//  4. This is a READ-ONLY live-drift view, not a PR preview.

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

const mockGetClusterComparison = vi.fn();
const mockFetchTrackedPRs = vi.fn();
const mockGetAddonCatalog = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
      getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      getAIStatus: vi.fn().mockResolvedValue({ enabled: false }),
      getClusterHistory: vi.fn().mockResolvedValue({ history: [] }),
      getClusterChanges: vi.fn().mockResolvedValue({ changes: [] }),
    },
    fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
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

function renderView() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={['/clusters/prod-eu']}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('ClusterDetail — label drift diff (V3 G2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
  });

  it('renders "Labels in sync" green banner when label_drift is absent', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText(/Labels in sync/)).toBeInTheDocument();
    expect(screen.queryByText(/Label Drift Detected/)).not.toBeInTheDocument();
  });

  it('renders "Labels in sync" when label_drift is present but all arrays are empty', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        label_drift: {
          added: [],
          removed: [],
          changed: [],
        },
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText(/Labels in sync/)).toBeInTheDocument();
    expect(screen.queryByText(/Label Drift Detected/)).not.toBeInTheDocument();
  });

  it('renders drift diff with added keys when label_drift.added is populated', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        label_drift: {
          added: ['sharko.addon.metrics-server', 'sharko.addon.cert-manager'],
        },
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText(/Label Drift Detected/)).toBeInTheDocument();
    expect(screen.getByText(/Added in Git \(missing on cluster\)/)).toBeInTheDocument();
    expect(screen.getByText(/\+ sharko\.addon\.metrics-server/)).toBeInTheDocument();
    expect(screen.getByText(/\+ sharko\.addon\.cert-manager/)).toBeInTheDocument();
  });

  it('renders drift diff with removed keys when label_drift.removed is populated', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        label_drift: {
          removed: ['sharko.addon.old-addon'],
        },
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText(/Label Drift Detected/)).toBeInTheDocument();
    expect(screen.getByText(/Removed in Git \(present on cluster\)/)).toBeInTheDocument();
    expect(screen.getByText(/- sharko\.addon\.old-addon/)).toBeInTheDocument();
  });

  it('renders drift diff with changed keys when label_drift.changed is populated', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        label_drift: {
          changed: ['sharko.addon.nginx-version'],
        },
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText(/Label Drift Detected/)).toBeInTheDocument();
    expect(screen.getByText(/Changed \(values differ\)/)).toBeInTheDocument();
    expect(screen.getByText(/~ sharko\.addon\.nginx-version/)).toBeInTheDocument();
  });

  it('renders drift diff with all three categories together', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        label_drift: {
          added: ['sharko.addon.new-one'],
          removed: ['sharko.addon.gone'],
          changed: ['sharko.addon.version-bump'],
        },
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText(/Label Drift Detected/)).toBeInTheDocument();
    expect(screen.getByText(/Added in Git \(missing on cluster\)/)).toBeInTheDocument();
    expect(screen.getByText(/Removed in Git \(present on cluster\)/)).toBeInTheDocument();
    expect(screen.getByText(/Changed \(values differ\)/)).toBeInTheDocument();
    expect(screen.getByText(/\+ sharko\.addon\.new-one/)).toBeInTheDocument();
    expect(screen.getByText(/- sharko\.addon\.gone/)).toBeInTheDocument();
    expect(screen.getByText(/~ sharko\.addon\.version-bump/)).toBeInTheDocument();
  });

  it('renders an explanatory hint that this shows what drifted', async () => {
    mockGetClusterComparison.mockResolvedValue(
      baseComparisonResponse({
        label_drift: {
          added: ['sharko.addon.test'],
        },
      }),
    );
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    expect(screen.getByText(/This cluster's addon labels drifted from Git/)).toBeInTheDocument();
  });
});
