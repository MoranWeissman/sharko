import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// V3-AM1: Tests for the redesigned cluster Addons page interaction:
// - Top strip shows ONLY pending changes (not all enabled addons)
// - Button renamed "Manage addons"
// - Remove control on enabled comparison-table rows

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

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const mockGetClusterComparison = vi.fn();
const mockFetchTrackedPRs = vi.fn();
const mockUpdateClusterAddons = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockShowToast = vi.fn();

vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual<typeof import('@/components/ToastNotification')>(
    '@/components/ToastNotification',
  );
  return { ...actual, showToast: (...args: unknown[]) => mockShowToast(...args) };
});

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
    },
    fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
    updateClusterAddons: (...args: unknown[]) => mockUpdateClusterAddons(...args),
  };
});

// Cluster with 2 enabled addons (ingress-nginx + cert-manager)
const comparisonResponse = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
  },
  git_total_addons: 2,
  git_enabled_addons: 2,
  git_disabled_addons: 0,
  argocd_total_applications: 2,
  argocd_healthy_applications: 2,
  argocd_synced_applications: 2,
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
  total_healthy: 2,
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

describe('ClusterDetail - V3-AM1 (one list + discoverable remove + "Manage addons")', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockUpdateClusterAddons.mockResolvedValue({});
    mockGetAddonCatalog.mockResolvedValue({
      addons: [
        { addon_name: 'ingress-nginx', version: '4.7.0' },
        { addon_name: 'cert-manager', version: '1.12.0' },
        { addon_name: 'prometheus', version: '2.45.0' },
      ],
    });
  });

  it('button reads "Manage addons" (not "+ Enable addon")', async () => {
    renderView();

    await waitFor(() => {
      const button = screen.getByTestId('manage-addons-enable-btn');
      expect(button).toBeInTheDocument();
      expect(button).toHaveTextContent('Manage addons');
      expect(button).not.toHaveTextContent('Enable addon');
    });
  });

  it('enabled+synced addon with no pending edit appears ONCE (comparison table), NOT in a top static list', async () => {
    renderView();

    await waitFor(() => {
      // Should see the comparison table
      expect(screen.getByText('Status')).toBeInTheDocument(); // table header
      expect(screen.getByText('Addon Name')).toBeInTheDocument();

      // Find the comparison table row for ingress-nginx
      const tableRows = screen.getAllByRole('row');
      const nginxRow = tableRows.find((row) =>
        within(row).queryByText('ingress-nginx'),
      );
      expect(nginxRow).toBeTruthy();

      // V3-AM1: The top manage strip should NOT show enabled-but-unchanged
      // addons. It should only show pending changes. Since we haven't toggled
      // anything, there should be NO rows in the top strip with these addon names.
      // The "No addons enabled on this cluster yet." message should NOT appear
      // (we have enabled addons), but neither should a static list of them.

      // Strategy: check that the addon names do NOT appear in a static list
      // outside the comparison table. We can't assert "not in top strip" directly
      // because the strip is conditional (renders only when pending). Instead,
      // assert that the only occurrences of the addon names are in the table.
      const allNginxMatches = screen.getAllByText('ingress-nginx');
      // Should be 1 match: in the comparison table link
      expect(allNginxMatches.length).toBe(1);
    });
  });

  it('with nothing pending, the top manage strip is absent (no static enabled-addon list)', async () => {
    renderView();

    await waitFor(() => {
      // The comparison table should be visible
      expect(screen.getByText('Status')).toBeInTheDocument();

      // The "No addons enabled on this cluster yet." message should NOT appear
      // (we have enabled addons)
      expect(
        screen.queryByText('No addons enabled on this cluster yet.'),
      ).not.toBeInTheDocument();

      // The top strip with pending badges/Undo/strikethrough should NOT render
      // when there are no pending changes. We can check this by ensuring the
      // "pending" / "removing" badges are absent.
      expect(screen.queryByText(/pending/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/removing/i)).not.toBeInTheDocument();
    });
  });

  it('each enabled comparison-table row has a "Remove" control (in the row-actions kebab)', async () => {
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // W10 (V3 RW1.4): Remove moved into a per-row RowActionsMenu kebab. Each
    // enabled addon row has its own kebab trigger, labelled "Actions for <name>".
    const kebabs = screen.getAllByRole('button', { name: /actions for/i });
    expect(kebabs.length).toBe(2); // ingress-nginx + cert-manager

    // Opening a kebab reveals a destructive "Remove" menuitem.
    await user.click(screen.getByRole('button', { name: /actions for ingress-nginx/i }));
    expect(await screen.findByRole('menuitem', { name: /remove/i })).toBeInTheDocument();
  });

  it('clicking Remove on an enabled row stages a pending-remove (top strip shows it + Undo) + Apply footer appears', async () => {
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // W10 (V3 RW1.4): open the ingress-nginx row's kebab and click Remove.
    await user.click(screen.getByRole('button', { name: /actions for ingress-nginx/i }));
    await user.click(await screen.findByRole('menuitem', { name: /remove/i }));

    // Now the top manage strip should appear with the pending-remove row
    await waitFor(() => {
      // Should see "removing" badge + strikethrough on the addon name
      expect(screen.getByText(/removing/i)).toBeInTheDocument();

      // Should see the Undo button
      const undoButtons = screen.getAllByText('Undo');
      expect(undoButtons.length).toBeGreaterThan(0);

      // Apply/Discard footer should appear — W9 (RW1.7): primary button reads
      // "Review & open PR" before preview, "Open PR" after.
      expect(screen.getByText('Review & open PR')).toBeInTheDocument();
      expect(screen.getByText('Discard')).toBeInTheDocument();
    });
  });

  it('clicking Apply after staging a remove calls updateClusterAddons with the batch (one PR path)', async () => {
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // W10 (V3 RW1.4): stage a remove via the row-actions kebab.
    await user.click(screen.getByRole('button', { name: /actions for ingress-nginx/i }));
    await user.click(await screen.findByRole('menuitem', { name: /remove/i }));

    await waitFor(() => {
      expect(screen.getByText(/removing/i)).toBeInTheDocument();
    });

    // W9 (RW1.7): preview-then-confirm flow — click "Review & open PR" to get
    // the preview, then "Open PR" to confirm (skip waiting for preview render
    // since the mock returns synchronously in this test).
    const reviewButton = screen.getByText('Review & open PR');
    fireEvent.click(reviewButton);

    await waitFor(() => {
      expect(screen.getByText('Open PR')).toBeInTheDocument();
    });

    const openPRButton = screen.getByText('Open PR');
    fireEvent.click(openPRButton);

    await waitFor(() => {
      // Should have called updateClusterAddons with the toggle map showing
      // ingress-nginx=false (staged for removal) and cert-manager=true (unchanged enabled)
      expect(mockUpdateClusterAddons).toHaveBeenCalledWith(
        'prod-eu',
        expect.objectContaining({
          'ingress-nginx': false,
          'cert-manager': true,
        }),
      );
    });
  });

  it('clicking Undo clears the staged pending-remove (top strip disappears)', async () => {
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // W10 (V3 RW1.4): stage a remove via the row-actions kebab.
    await user.click(screen.getByRole('button', { name: /actions for ingress-nginx/i }));
    await user.click(await screen.findByRole('menuitem', { name: /remove/i }));

    await waitFor(() => {
      expect(screen.getByText(/removing/i)).toBeInTheDocument();
    });

    // Click Undo
    const undoButton = screen.getAllByText('Undo')[0];
    fireEvent.click(undoButton);

    await waitFor(() => {
      // The "removing" badge should disappear
      expect(screen.queryByText(/removing/i)).not.toBeInTheDocument();

      // Apply/Discard footer should disappear — W9 (RW1.7): button was "Review & open PR"
      expect(screen.queryByText('Review & open PR')).not.toBeInTheDocument();
      expect(screen.queryByText('Discard')).not.toBeInTheDocument();
    });
  });

  it('clicking Discard clears all staged changes (same as Undo, but for multiple changes)', async () => {
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // W10 (V3 RW1.4): stage a remove via the row-actions kebab.
    await user.click(screen.getByRole('button', { name: /actions for ingress-nginx/i }));
    await user.click(await screen.findByRole('menuitem', { name: /remove/i }));

    await waitFor(() => {
      expect(screen.getByText(/removing/i)).toBeInTheDocument();
    });

    // Click Discard
    const discardButton = screen.getByText('Discard');
    fireEvent.click(discardButton);

    await waitFor(() => {
      // The "removing" badge should disappear
      expect(screen.queryByText(/removing/i)).not.toBeInTheDocument();

      // Apply/Discard footer should disappear — W9 (RW1.7): button was "Review & open PR"
      expect(screen.queryByText('Review & open PR')).not.toBeInTheDocument();
      expect(screen.queryByText('Discard')).not.toBeInTheDocument();
    });
  });

  it('regression: AP1 preview gate (preview always carries Apply/Discard) still works', async () => {
    const user = userEvent.setup();
    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // W10 (V3 RW1.4): stage a remove via the row-actions kebab.
    await user.click(screen.getByRole('button', { name: /actions for ingress-nginx/i }));
    await user.click(await screen.findByRole('menuitem', { name: /remove/i }));

    await waitFor(() => {
      expect(screen.getByText(/removing/i)).toBeInTheDocument();
    });

    // W9 (RW1.7): Click "Review & open PR" to trigger the preview
    const reviewButton = screen.getByText('Review & open PR');
    fireEvent.click(reviewButton);

    // V3-AP1 fix: preview always carries Apply/Discard, even if the toggle
    // edit is later cleared by background poll. W9 (RW1.7): once the preview
    // is shown, the primary button becomes "Open PR".
    await waitFor(() => {
      expect(screen.getByText('Open PR')).toBeInTheDocument();
      expect(screen.getByText('Discard')).toBeInTheDocument();
    });
  });

  it('regression: V3-BUG-2 eager catalog fetch — button visible even on 0-addon cluster with non-empty catalog', async () => {
    // Override: cluster has 0 enabled addons
    mockGetClusterComparison.mockResolvedValue({
      cluster: {
        name: 'prod-eu',
        labels: { env: 'prod' },
        server_version: '1.28',
        connection_status: 'connected',
      },
      git_total_addons: 0,
      git_enabled_addons: 0,
      git_disabled_addons: 0,
      argocd_total_applications: 0,
      argocd_healthy_applications: 0,
      argocd_synced_applications: 0,
      argocd_degraded_applications: 0,
      argocd_out_of_sync_applications: 0,
      addon_comparisons: [],
      total_healthy: 0,
      total_with_issues: 0,
      total_missing_in_argocd: 0,
      total_untracked_in_argocd: 0,
      total_disabled_in_git: 0,
    });
    // Catalog is non-empty
    mockGetAddonCatalog.mockResolvedValue({
      addons: [{ addon_name: 'prometheus', version: '2.45.0' }],
    });

    renderView();

    await waitFor(() => {
      // Button should be visible
      const button = screen.getByTestId('manage-addons-enable-btn');
      expect(button).toBeInTheDocument();
      expect(button).toHaveTextContent('Manage addons');
    });
  });

  it('regression: V2-cleanup-32 seeding rule — untracked/system addons NOT in toggle map, NOT removable', async () => {
    // Override: add an untracked addon
    mockGetClusterComparison.mockResolvedValue({
      ...comparisonResponse,
      addon_comparisons: [
        ...comparisonResponse.addon_comparisons,
        {
          addon_name: 'orphan-app',
          git_configured: false, // not in git
          git_enabled: false,
          argocd_deployed: true,
          argocd_deployed_version: '1.0.0',
          argocd_namespace: 'default',
          argocd_health_status: 'Healthy',
          status: 'untracked_in_argocd',
          issues: [],
        },
      ],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('ingress-nginx')).toBeInTheDocument();
    });

    // The orphan-app row should be in the comparison table
    expect(screen.getByText('orphan-app')).toBeInTheDocument();

    // W10 (V3 RW1.4): Remove now lives in a per-row kebab. Only git_configured
    // enabled addons get a kebab — the untracked orphan-app has none.
    const kebabs = screen.getAllByRole('button', { name: /actions for/i });
    expect(kebabs.length).toBe(2); // ingress-nginx + cert-manager, not orphan-app
    expect(
      screen.queryByRole('button', { name: /actions for orphan-app/i }),
    ).not.toBeInTheDocument();
  });
});
