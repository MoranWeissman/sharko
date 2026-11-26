import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import {
  ClusterDetail,
  DIFF_LAST_CHECK_FAILED,
  DIFF_LAST_CHECK_SKIPPED,
  DIFF_NO_DIFFERENCES,
} from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// Review must-fix: a failed or skipped check carries no list of differences,
// and the Diff panel read "no list" as "no differences". It showed the green
// "No differences found at the last check" sentence right under the reason
// the check had failed.

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



describe('ClusterDetail Diff — after a check that compared nothing', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockReconcile.mockResolvedValue({ status: 'accepted', message: 'ok' });
  });

  it('pins the sentences', () => {
    expect(DIFF_LAST_CHECK_FAILED).toBe(
      "The last check failed, so Sharko does not know whether this secret's addon labels match Git.",
    );
    expect(DIFF_LAST_CHECK_SKIPPED).toBe(
      "The last check did not compare this secret's addon labels with Git.",
    );
  });

  const cases = [
    { outcome: 'failed' as const, want: DIFF_LAST_CHECK_FAILED, reason: 'Sharko could not reach ArgoCD' },
    { outcome: 'skipped' as const, want: DIFF_LAST_CHECK_SKIPPED, reason: 'The ArgoCD cluster Secret is not there yet' },
  ];
  for (const c of cases) {
    it(`says nothing was compared when the last check ${c.outcome}`, async () => {
      const resp = baseComparisonResponse();
      resp.cluster.last_reconcile.outcome = c.outcome;
      resp.cluster.last_reconcile.message = c.reason;
      mockGetClusterComparison.mockResolvedValue(resp);
      renderAs('viewer');
      await screen.findByText('prod-eu');
      fireEvent.click(screen.getByTestId('secret-sync-diff'));
      const panel = await screen.findByTestId('secret-sync-diff-panel');
      expect(panel.textContent).toContain(c.want);
      expect(panel.textContent).toContain(c.reason);
      expect(panel.textContent).not.toContain(DIFF_NO_DIFFERENCES);
      expect(panel.textContent).not.toContain('No differences');
      expect(panel.textContent).not.toContain('matched Git');
    });
  }

  it('still says "No differences" after a check that worked and found none', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparisonResponse());
    renderAs('viewer');
    await screen.findByText('prod-eu');
    fireEvent.click(screen.getByTestId('secret-sync-diff'));
    const panel = await screen.findByTestId('secret-sync-diff-panel');
    expect(panel.textContent).toContain(DIFF_NO_DIFFERENCES);
    expect(screen.queryByTestId('diff-not-compared')).toBeNull();
  });

  it('still lists the differences when a failed check did carry them', async () => {
    const resp = baseComparisonResponse({ label_drift: { added: ['ingress-nginx'] } });
    resp.cluster.last_reconcile.outcome = 'failed';
    resp.cluster.last_reconcile.message = 'the write was refused';
    mockGetClusterComparison.mockResolvedValue(resp);
    renderAs('viewer');
    await screen.findByText('prod-eu');
    fireEvent.click(screen.getByTestId('secret-sync-diff'));
    const panel = await screen.findByTestId('secret-sync-diff-panel');
    expect(panel.textContent).toContain('This secret is missing 1 addon label that Git expects.');
    expect(screen.queryByTestId('diff-not-compared')).toBeNull();
  });
});
