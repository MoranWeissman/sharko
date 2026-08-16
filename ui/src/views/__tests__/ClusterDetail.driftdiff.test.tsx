import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// V3 G2 — read-only live drift diff for managed clusters, folded into the
// Managed cluster secret panel's "Diff" toggle (walk day 4 / S1 rebuild).
// Pins:
//  1. Clicking Diff with no label_drift (or all-empty arrays) shows a plain
//     "no differences" message — this is the click-to-show replacement for
//     the old always-visible "Labels in sync" banner.
//  2. Clicking Diff with label_drift content shows plain-English sentences
//     naming what's missing / extra / changed, with the raw label keys as
//     secondary (dimmer) detail — never the old raw-status vocabulary
//     ("Label Drift Detected", "Git vs Live Label Diff", +/-/~ markers).
//  3. This is a READ-ONLY live-drift view, not a PR preview.

function openDiff() {
  fireEvent.click(screen.getByRole('button', { name: /^Diff$/ }));
}

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

  it('shows "no differences" when Diff is opened and label_drift is absent', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    openDiff();
    expect(screen.getByText(/No differences — this secret's addon labels match git/)).toBeInTheDocument();
    expect(screen.queryByText(/Label Drift Detected/)).not.toBeInTheDocument();
  });

  it('shows "no differences" when Diff is opened and label_drift is present but all arrays are empty', async () => {
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

    openDiff();
    expect(screen.getByText(/No differences — this secret's addon labels match git/)).toBeInTheDocument();
    expect(screen.queryByText(/Label Drift Detected/)).not.toBeInTheDocument();
  });

  it('shows a plain-words sentence + the raw keys when label_drift.added is populated', async () => {
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

    openDiff();
    expect(screen.getByText(/This secret is missing 2 addon labels that git expects/)).toBeInTheDocument();
    expect(screen.getByText(/sharko\.addon\.metrics-server, sharko\.addon\.cert-manager/)).toBeInTheDocument();
    // The old raw-status vocabulary must never appear as visible text.
    expect(screen.queryByText(/Added in Git \(missing on cluster\)/)).not.toBeInTheDocument();
  });

  it('shows a plain-words sentence + the raw keys when label_drift.removed is populated', async () => {
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

    openDiff();
    expect(screen.getByText(/This secret has 1 addon label that git doesn't expect/)).toBeInTheDocument();
    expect(screen.getByText(/sharko\.addon\.old-addon/)).toBeInTheDocument();
    expect(screen.queryByText(/Removed in Git \(present on cluster\)/)).not.toBeInTheDocument();
  });

  it('shows a plain-words sentence + the raw keys when label_drift.changed is populated', async () => {
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

    openDiff();
    expect(screen.getByText(/1 addon label has a different value than git/)).toBeInTheDocument();
    expect(screen.getByText(/sharko\.addon\.nginx-version/)).toBeInTheDocument();
    expect(screen.queryByText(/Changed \(values differ\)/)).not.toBeInTheDocument();
  });

  it('shows all three categories together, each with its own plain-words sentence', async () => {
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

    openDiff();
    expect(screen.getByText(/This secret is missing 1 addon label that git expects/)).toBeInTheDocument();
    expect(screen.getByText(/This secret has 1 addon label that git doesn't expect/)).toBeInTheDocument();
    expect(screen.getByText(/1 addon label has a different value than git/)).toBeInTheDocument();
    expect(screen.getByText(/sharko\.addon\.new-one/)).toBeInTheDocument();
    expect(screen.getByText(/sharko\.addon\.gone/)).toBeInTheDocument();
    expect(screen.getByText(/sharko\.addon\.version-bump/)).toBeInTheDocument();
  });

  it('names the Sync addon labels action as the way to apply the difference, for admin/operator (HL-1)', async () => {
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

    openDiff();
    // HL-1, renamed again by the Round 3 ruling (2026-08-16): names the
    // renamed button and only promises the labels — the old "apply git's
    // version" sentence read wider than the action.
    expect(screen.getByText(/Click Sync addon labels above to put git's addon labels back on this secret/)).toBeInTheDocument();
  });
});
