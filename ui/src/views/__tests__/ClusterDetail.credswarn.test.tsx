import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-88.5, L4 — addon_secrets_ready pre-warn. When a cluster has no
// resolvable connection credentials AND the addon the operator just staged
// carries addon secrets, the UI fires a background dry-run of the exact
// pre-flight gate EnableAddon applies (POST .../addons/{addon} with
// dry_run:true) and shows the rejection inline BEFORE "Apply Changes" —
// instead of letting the operator hit the 422 blind. If they proceed
// anyway, the real 422 renders verbatim (already true of the existing
// toggleError path; pinned here too for the full flow).

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
const mockPreviewEnableAddon = vi.fn();
const mockUpdateClusterAddons = vi.fn();

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
    previewEnableAddon: (...args: unknown[]) => mockPreviewEnableAddon(...args),
    updateClusterAddons: (...args: unknown[]) => mockUpdateClusterAddons(...args),
  };
});

const MISSING_CREDS_MESSAGE =
  'addon "datadog" needs 2 secrets pushed to the cluster, but Sharko has no credentials for cluster "prod-eu" — add connection credentials (secret path or EKS role) to the cluster, or choose an addon without secrets';

function baseComparison(addonSecretsReady: boolean) {
  return {
    cluster: {
      name: 'prod-eu',
      labels: { env: 'prod' },
      server_version: '1.28',
      connection_status: 'connected',
      addon_secrets_ready: addonSecretsReady,
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
        has_version_override: false,
        argocd_deployed: true,
        argocd_deployed_version: '4.7.0',
        argocd_health_status: 'Healthy',
        status: 'healthy',
        issues: [],
      },
      {
        // Disabled-in-git so it's a candidate the "Enable addon" picker
        // offers, not already staged.
        addon_name: 'datadog',
        git_configured: true,
        git_version: '3.0.0',
        git_enabled: false,
        has_version_override: false,
        argocd_deployed: false,
        status: 'disabled_in_git',
        issues: [],
      },
    ],
    total_healthy: 1,
    total_with_issues: 0,
    total_missing_in_argocd: 0,
    total_untracked_in_argocd: 0,
    total_disabled_in_git: 1,
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

async function stageDatadog() {
  await waitFor(() => {
    expect(screen.getByText('prod-eu')).toBeInTheDocument();
  });
  fireEvent.click(screen.getByTestId('manage-addons-enable-btn'));
  await waitFor(() => {
    expect(screen.getByTestId('addon-picker-item-datadog')).toBeInTheDocument();
  });
  fireEvent.click(screen.getByTestId('addon-picker-item-datadog'));
}

describe('ClusterDetail — addon_secrets_ready pre-warn (V2-cleanup-88.5)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockGetAddonCatalog.mockResolvedValue({ addons: [{ addon_name: 'datadog' }, { addon_name: 'ingress-nginx' }] });
  });

  it('warns inline when the cluster has no resolvable credentials and the staged addon needs secrets', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparison(false));
    mockPreviewEnableAddon.mockRejectedValue(new Error(MISSING_CREDS_MESSAGE));

    renderView();
    await stageDatadog();

    await waitFor(() => {
      expect(mockPreviewEnableAddon).toHaveBeenCalledWith('prod-eu', 'datadog');
    });
    await waitFor(() => {
      expect(screen.getByTestId('manage-addon-secret-warning-datadog')).toBeInTheDocument();
    });
    // Verbatim backend message — written for humans, rendered as-is.
    expect(screen.getByText(MISSING_CREDS_MESSAGE)).toBeInTheDocument();
  });

  it('does not warn (and does not even call the preview) when the cluster already has resolvable credentials', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparison(true));

    renderView();
    await stageDatadog();

    // Give any stray async work a tick to settle, then assert no call and
    // no warning — addon_secrets_ready:true means dry-run would always
    // succeed regardless of the addon, so the UI skips the round-trip.
    await waitFor(() => {
      expect(screen.getByTestId('manage-addon-row-datadog')).toBeInTheDocument();
    });
    expect(mockPreviewEnableAddon).not.toHaveBeenCalled();
    expect(screen.queryByTestId('manage-addon-secret-warning-datadog')).not.toBeInTheDocument();
  });

  it('does not warn when the dry-run succeeds (addon has no secrets, or credentials resolve)', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparison(false));
    mockPreviewEnableAddon.mockResolvedValue({ status: 'success' });

    renderView();
    await stageDatadog();

    await waitFor(() => {
      expect(mockPreviewEnableAddon).toHaveBeenCalledWith('prod-eu', 'datadog');
    });
    expect(screen.queryByTestId('manage-addon-secret-warning-datadog')).not.toBeInTheDocument();
  });

  it('renders the real 422 verbatim on the real apply if the operator proceeds past the pre-warn', async () => {
    mockGetClusterComparison.mockResolvedValue(baseComparison(false));
    mockPreviewEnableAddon.mockRejectedValue(new Error(MISSING_CREDS_MESSAGE));
    // W9 (V3 RW1.7): the apply now runs through a preview-then-confirm flow.
    // The dry-run preview (dryRun === true) succeeds so the confirm button
    // appears; the REAL apply (dryRun falsy) is where the backend 422 lands —
    // exactly the "operator proceeded past the pre-warn" case this pins.
    mockUpdateClusterAddons.mockImplementation(
      (_name: string, _payload: unknown, dryRun?: boolean) => {
        if (dryRun) {
          return Promise.resolve({
            pr_title: 'Enable datadog on prod-eu',
            files_to_write: [{ path: 'configuration/managed-clusters.yaml', action: 'update' }],
          });
        }
        return Promise.reject(new Error(MISSING_CREDS_MESSAGE));
      },
    );

    renderView();
    await stageDatadog();

    await waitFor(() => {
      expect(screen.getByTestId('manage-addon-secret-warning-datadog')).toBeInTheDocument();
    });

    // Close the picker (it stays open for multi-select) so the rest of the
    // page is reachable again.
    fireEvent.click(screen.getByTestId('addon-picker-done'));
    await waitFor(() => {
      expect(screen.queryByTestId('addon-picker-done')).not.toBeInTheDocument();
    });

    // Proceed: "Review & open PR" fires the dry-run preview, then "Open PR"
    // fires the real apply that the backend rejects with the 422.
    fireEvent.click(screen.getByRole('button', { name: /review & open pr/i }));
    const openPRBtn = await screen.findByRole('button', { name: /^open pr$/i });
    fireEvent.click(openPRBtn);

    await waitFor(() => {
      expect(mockUpdateClusterAddons).toHaveBeenCalledWith('prod-eu', expect.anything());
    });
    // The same verbatim message renders again via the existing
    // toggleError path — pre-warn and the real gate agree.
    await waitFor(() => {
      expect(screen.getAllByText(MISSING_CREDS_MESSAGE).length).toBeGreaterThanOrEqual(1);
    });
  });
});
