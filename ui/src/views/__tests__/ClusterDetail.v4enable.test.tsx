/**
 * ClusterDetail — v4 enable routing (v4 Wave 1 Story 4.3 UI half).
 *
 * Verifies the picker routes through V4EnableAddonDialog instead of the
 * v3 bulk-toggle-and-apply flow when the connected repo is v4-format
 * (repo/status.initialized === true — the engine-pin-based v4 marker),
 * and that it keeps using the existing v3 staging flow when the repo is
 * not v4 (the default, so every OTHER existing ClusterDetail test that
 * doesn't mock getRepoStatus keeps its current behavior unchanged).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

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
const mockGetRepoStatus = vi.fn();
const mockEnableAddonV4 = vi.fn();

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
      restartAddonSync: vi.fn().mockResolvedValue({}),
      getRepoStatus: (...args: unknown[]) => mockGetRepoStatus(...args),
    },
    fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
    enableAddonV4: (...args: unknown[]) => mockEnableAddonV4(...args),
  };
});

const comparisonResponse = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
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
      addon_name: 'cert-manager',
      git_configured: true,
      git_version: '1.12.0',
      git_enabled: true,
      environment_version: '1.12.0',
      has_version_override: false,
      argocd_deployed: true,
      argocd_deployed_version: '1.12.0',
      argocd_namespace: 'cert-manager',
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

beforeEach(() => {
  vi.clearAllMocks();
  mockGetClusterComparison.mockResolvedValue(comparisonResponse);
  mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
  mockGetAddonCatalog.mockResolvedValue({
    addons: [
      { addon_name: 'cert-manager', version: '1.12.0' },
      { addon_name: 'metrics-server', version: '0.6.0' },
    ],
  });
});

describe('ClusterDetail — v4 enable routing', () => {
  it('v4 repo: clicking an addon in the picker opens V4EnableAddonDialog, not the v3 pending-stage strip', async () => {
    mockGetRepoStatus.mockResolvedValue({ initialized: true, bootstrap_synced: true });
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByTestId('manage-addons-enable-btn')).toBeInTheDocument();
    });
    await user.click(screen.getByTestId('manage-addons-enable-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('addon-picker-item-metrics-server')).toBeInTheDocument();
    });
    await user.click(screen.getByTestId('addon-picker-item-metrics-server'));

    // The v4 dialog opens naming the addon and cluster — not the v3
    // "pending" strip (which would show a "pending" badge instead).
    await waitFor(() => {
      expect(screen.getByText('Enable metrics-server on prod-eu')).toBeInTheDocument();
    });
    expect(screen.queryByText(/pending/i)).not.toBeInTheDocument();
  });

  it('v3 repo (default / getRepoStatus unset or false): clicking an addon stages it in the v3 pending strip, no v4 dialog', async () => {
    mockGetRepoStatus.mockResolvedValue({ initialized: false, reason: 'not_bootstrapped' });
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByTestId('manage-addons-enable-btn')).toBeInTheDocument();
    });
    await user.click(screen.getByTestId('manage-addons-enable-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('addon-picker-item-metrics-server')).toBeInTheDocument();
    });
    await user.click(screen.getByTestId('addon-picker-item-metrics-server'));

    // v3 behavior unchanged: staged as pending, no v4 dialog title.
    await waitFor(() => {
      expect(screen.getByText(/pending/i)).toBeInTheDocument();
    });
    expect(screen.queryByText('Enable metrics-server on prod-eu')).not.toBeInTheDocument();
    expect(mockEnableAddonV4).not.toHaveBeenCalled();
  });

  // Walk day 5 finding: a fresh enable-PR's addon has no comparison row
  // until it merges, so ClusterDetail needs its tracked-PR fetch to run
  // again the instant the dialog applies — not wait for the next
  // background poll — or the operator sees nothing pending after "Done".
  it('a successful v4 enable refetches tracked PRs immediately (onApplied → fetchData)', async () => {
    mockGetRepoStatus.mockResolvedValue({ initialized: true, bootstrap_synced: true });
    mockEnableAddonV4
      .mockResolvedValueOnce({
        dry_run: {
          effective_addons: ['metrics-server'],
          files_to_write: [{ path: 'cluster-addons/prod-eu.yaml', action: 'update' }],
          pr_title: 'sharko: enable addon metrics-server on cluster prod-eu',
          secrets_to_create: [],
        },
      })
      .mockResolvedValueOnce({
        pr_url: 'https://github.com/example/repo/pull/6001',
        pr_id: 6001,
        merged: false,
      });

    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByTestId('manage-addons-enable-btn')).toBeInTheDocument();
    });
    await user.click(screen.getByTestId('manage-addons-enable-btn'));

    await waitFor(() => {
      expect(screen.getByTestId('addon-picker-item-metrics-server')).toBeInTheDocument();
    });
    const callsBeforeEnable = mockFetchTrackedPRs.mock.calls.length;
    await user.click(screen.getByTestId('addon-picker-item-metrics-server'));

    await waitFor(() => {
      expect(screen.getByText('Enable metrics-server on prod-eu')).toBeInTheDocument();
    });
    await user.click(screen.getByRole('button', { name: /preview/i }));
    await waitFor(() => expect(screen.getByTestId('v4-confirm')).toBeInTheDocument());
    await user.click(screen.getByTestId('v4-confirm'));

    await waitFor(() => {
      expect(screen.getByText(/View PR #6001/)).toBeInTheDocument();
    });

    // The apply's onApplied callback refetches right away, not on the
    // next 10-30s background poll.
    await waitFor(() => {
      expect(mockFetchTrackedPRs.mock.calls.length).toBeGreaterThan(callsBeforeEnable);
    });
    expect(mockFetchTrackedPRs).toHaveBeenLastCalledWith(
      expect.objectContaining({ cluster: 'prod-eu', status: 'open' }),
    );
  });
});
