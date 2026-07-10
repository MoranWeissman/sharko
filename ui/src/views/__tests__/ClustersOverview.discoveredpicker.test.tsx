import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-89.3 — "I do" registration picks from ArgoCD's existing
// clusters; the standing Discovered section collapses to a one-line hint.
//
// Maintainer walkthrough finding: Sharko already discovers ArgoCD cluster
// secrets it doesn't manage (GET /clusters returns managed: false for
// them), but the Register dialog's "I do" ownership choice never
// mentioned them — a user picking "I do" was forced to type coordinates
// for a cluster ArgoCD already knew about. Pinned behaviours:
//
//   1. Register dialog + "I do" + at least one discovered cluster: a
//      "Discovered clusters" block lists them by name + server URL
//      (renamed from "Pick from what ArgoCD already has" — V2-cleanup-89.9).
//   2. The block is absent with the Sharko-managed default, and absent
//      when there are no discovered clusters at all (today's behavior).
//   3. Picking one and confirming reuses the EXISTING adopt flow
//      (AdoptClustersDialog -> adoptClusters()), not registerCluster —
//      same verify-then-confirm dialog, same Git-PR banner on success.
//   4. The "I do" hint line explains what self-management means,
//      including that Git stays the source of truth for addon placement.
//   5. The standing Discovered section is a single hint line with a
//      count, not the old card/table bulk-select list; clicking it opens
//      Register New Cluster pre-set to "I do".

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

const mockGetClusters = vi.fn();
const mockHealth = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();
const mockTestClusterConnection = vi.fn();
const mockAdoptClusters = vi.fn();

// V2-cleanup-90.4 (L6) — capture toast calls so the "picked cluster
// disappeared before Adopt was confirmed" assertion can check the error
// toast names the cluster, without needing ToastContainer mounted.
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
      getClusters: (...args: unknown[]) => mockGetClusters(...args),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      health: (...args: unknown[]) => mockHealth(...args),
      // V2-cleanup-89.6 kill switch — not under test here, keep the default.
      getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
    },
    registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
    testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
    adoptClusters: (...args: unknown[]) => mockAdoptClusters(...args),
  };
});

const discoveredCluster = {
  name: 'argo-known-cluster',
  labels: {},
  managed: false,
  connection_status: 'not_in_git',
  server_url: 'https://argo-known.example.com:6443',
  server_version: 'v1.29.0',
};

const managedCluster = {
  name: 'sharko-managed',
  labels: {},
  managed: true,
  connection_status: 'connected',
  server_version: '1.29',
};

function clustersResponse(discovered: (typeof discoveredCluster)[]) {
  return {
    clusters: [managedCluster, ...discovered],
    health_stats: {
      total_in_git: 1,
      connected: 1,
      failed: 0,
      missing_from_argocd: 0,
      not_in_git: discovered.length,
    },
    pending_registrations: [],
    orphan_registrations: [],
  };
}

function renderView() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter>
        <ClustersOverview />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

async function waitForClusters() {
  await waitFor(() => {
    expect(screen.getByText('sharko-managed')).toBeInTheDocument();
  });
}

async function openAddDialog() {
  await waitForClusters();
  fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
  await waitFor(() => {
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
  });
}

// The ownership <select> is always the first combobox in the dialog —
// re-querying by role (rather than by current display text) keeps this
// helper valid across re-renders and dialog re-opens.
function ownershipSelect(): HTMLSelectElement {
  return screen.getAllByRole('combobox')[0] as HTMLSelectElement;
}

describe('ClustersOverview — "I do" picks from ArgoCD (V2-cleanup-89.3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockHealth.mockResolvedValue({ status: 'healthy', version: 'test', cluster_test_available: true });
    mockRegisterCluster.mockResolvedValue({ status: 'success', git: { merged: true } });
  });

  it('shows the picker with the discovered cluster\'s name and server URL once "I do" is chosen', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([discoveredCluster]));
    renderView();
    await openAddDialog();

    // Not shown yet — default ownership is "sharko".
    expect(screen.queryByTestId('discovered-picker')).not.toBeInTheDocument();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    await waitFor(() => {
      expect(screen.getByTestId('discovered-picker')).toBeInTheDocument();
    });
    expect(screen.getByText('argo-known-cluster')).toBeInTheDocument();
    expect(screen.getByText('https://argo-known.example.com:6443')).toBeInTheDocument();
  });

  it('is absent when there are no discovered clusters, even with "I do" chosen — the manual path still works', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([]));
    renderView();
    await openAddDialog();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    expect(screen.queryByTestId('discovered-picker')).not.toBeInTheDocument();
    // The manual coordinates path is unaffected.
    expect(screen.getByText('Connection source')).toBeInTheDocument();
  });

  // V2-cleanup-89.8 — maintainer walkthrough finding: with "I do" chosen and
  // no discovered clusters, the picker was hidden entirely with nothing in
  // its place, indistinguishable from the feature not existing. An explicit
  // calm line now says so out loud.
  it('shows an explicit "nothing to adopt" line when "I do" is chosen and ArgoCD has no discovered clusters', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([]));
    renderView();
    await openAddDialog();

    // Not shown before "I do" is picked — default ownership is "sharko".
    expect(screen.queryByTestId('discovered-empty')).not.toBeInTheDocument();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    await waitFor(() => {
      expect(screen.getByTestId('discovered-empty')).toBeInTheDocument();
    });
    expect(
      screen.getByText('Sharko checked ArgoCD — no other clusters there to adopt.'),
    ).toBeInTheDocument();
    // The picker itself stays absent — this is the "nothing to pick" state.
    expect(screen.queryByTestId('discovered-picker')).not.toBeInTheDocument();
  });

  // V2-cleanup-90.4 (M2) — when ArgoCD itself can't be reached, "Sharko
  // checked ArgoCD — no other clusters there to adopt" is a lie: Sharko
  // didn't check, it couldn't. The ArgoCDStatusBanner already tells the
  // user ArgoCD is unreachable; this line must stay silent instead of
  // adding a second, misleading message.
  it('does not show the "nothing to adopt" line when ArgoCD itself is unreachable', async () => {
    mockGetClusters.mockResolvedValue({
      // Every cluster failed/unknown + non-empty list is the view's
      // hasArgoError detection (ClustersOverview.tsx fetchData).
      clusters: [{ ...managedCluster, connection_status: 'Failed' }],
      health_stats: { total_in_git: 1, connected: 0, failed: 1, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [],
    });
    renderView();
    await openAddDialog();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    // Give the "nothing to adopt" line a chance to render before asserting
    // its absence.
    await waitFor(() => {
      expect(ownershipSelect().value).toBe('user');
    });
    expect(screen.queryByTestId('discovered-empty')).not.toBeInTheDocument();
    expect(screen.queryByTestId('discovered-picker')).not.toBeInTheDocument();
  });

  it('does not show the "nothing to adopt" line when the picker has items to show instead', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([discoveredCluster]));
    renderView();
    await openAddDialog();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    await waitFor(() => {
      expect(screen.getByTestId('discovered-picker')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('discovered-empty')).not.toBeInTheDocument();
  });

  it('picking a cluster and confirming fires the existing adopt flow, not registerCluster', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([discoveredCluster]));
    mockTestClusterConnection.mockResolvedValue({
      success: true,
      stage: 'connectivity',
      duration_ms: 5,
      reachable: true,
      server_version: '1.29.0',
    });
    mockAdoptClusters.mockResolvedValue({
      results: [{ cluster: 'argo-known-cluster', success: true, pr_url: 'https://github.com/org/repo/pull/99' }],
    });

    renderView();
    await openAddDialog();
    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    await waitFor(() => {
      expect(screen.getByTestId('discovered-picker')).toBeInTheDocument();
    });

    // Pick the cluster.
    fireEvent.click(screen.getByRole('radio', { name: /argo-known-cluster/i }));

    const adoptBtn = screen.getByRole('button', { name: /Adopt argo-known-cluster/i });
    expect(adoptBtn).not.toBeDisabled();
    fireEvent.click(adoptBtn);

    // Register dialog closes; the Adopt Clusters dialog takes over —
    // the SAME dialog the standing section used to open.
    await waitFor(() => {
      expect(screen.queryByText('Register New Cluster')).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(screen.getByText('Adopt Clusters')).toBeInTheDocument();
    });

    // Verification runs against the picked cluster, then review lets us confirm.
    await waitFor(() => {
      expect(mockTestClusterConnection).toHaveBeenCalledWith('argo-known-cluster');
    });
    await waitFor(() => {
      expect(screen.getByRole('button', { name: /Confirm Adoption/i })).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole('button', { name: /Confirm Adoption/i }));

    await waitFor(() => {
      expect(mockAdoptClusters).toHaveBeenCalledWith({ clusters: ['argo-known-cluster'] });
    });

    // Same Git-PR banner adopt shows today.
    await waitFor(() => {
      const link = screen.getByRole('link', { name: /PR/i }) as HTMLAnchorElement;
      expect(link.href).toBe('https://github.com/org/repo/pull/99');
    });

    expect(mockRegisterCluster).not.toHaveBeenCalled();
  });

  // V2-cleanup-90.4 (L6) — the picker's 30s auto-refresh can drop the
  // picked cluster out of the discovered list between the pick and the
  // Adopt click, while the picker itself stays visible (other discovered
  // clusters remain). Confirming used to be a silent no-op — nothing told
  // the user their click did nothing.
  it('names the cluster in an error toast and resets the pick when the picked cluster disappears before Adopt is confirmed', async () => {
    const otherDiscoveredCluster = {
      name: 'other-discovered-cluster',
      labels: {},
      managed: false,
      connection_status: 'not_in_git',
      server_url: 'https://other-discovered.example.com:6443',
      server_version: 'v1.29.0',
    };

    // Fake timers must be active BEFORE the component mounts — the 30s
    // auto-refresh `setInterval` is registered on mount, and a timer
    // registered under real timers is not advanced by a later switch to
    // fake ones. `shouldAdvanceTime: true` keeps waitFor/fireEvent working
    // normally alongside it (same pattern as the ClusterDetail adaptive-
    // polling tests).
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      mockGetClusters.mockResolvedValue(clustersResponse([discoveredCluster, otherDiscoveredCluster]));
      renderView();
      await openAddDialog();
      fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

      await waitFor(() => {
        expect(screen.getByTestId('discovered-picker')).toBeInTheDocument();
      });

      // Pick the cluster that's about to disappear.
      fireEvent.click(screen.getByRole('radio', { name: /argo-known-cluster/i }));
      expect(screen.getByRole('button', { name: /Adopt argo-known-cluster/i })).not.toBeDisabled();

      // The 30s auto-refresh drops the picked cluster from ArgoCD's
      // discovered list; the OTHER discovered cluster is still there, so
      // the picker itself stays visible and the pick goes silently stale.
      mockGetClusters.mockResolvedValue(clustersResponse([otherDiscoveredCluster]));
      await act(async () => {
        vi.advanceTimersByTime(30_000);
      });

      await waitFor(() => {
        expect(screen.getByText('other-discovered-cluster')).toBeInTheDocument();
      });
      expect(screen.queryByText('argo-known-cluster')).not.toBeInTheDocument();

      // Confirm the (now-stale) pick — the button still reads the old name
      // because `pickedDiscovered` hasn't been reset yet.
      fireEvent.click(screen.getByRole('button', { name: /Adopt argo-known-cluster/i }));

      expect(mockShowToast).toHaveBeenCalledWith(
        '"argo-known-cluster" is no longer discoverable — refresh the list.',
        'error',
      );
      // No adopt flow fired for a cluster that no longer exists in the list.
      expect(mockTestClusterConnection).not.toHaveBeenCalled();
      expect(mockAdoptClusters).not.toHaveBeenCalled();
      // The Register dialog stays open — closing it would also look like
      // nothing happened.
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();

      // The selection resets — the button reverts to its unpicked label.
      await waitFor(() => {
        expect(screen.getByRole('button', { name: 'Adopt selected cluster' })).toBeInTheDocument();
      });
    } finally {
      vi.useRealTimers();
    }
  });

  it('the "I do" line explains self-management, including that Git stays the source of truth', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([]));
    renderView();
    await openAddDialog();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    expect(
      screen.getByText(
        /Sharko never touches its credentials — it only keeps the addon labels on it in sync\. Git stays the source of truth for which addons go where\./,
      ),
    ).toBeInTheDocument();
  });

  it('collapses the standing Discovered section to a one-line hint with a count', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([discoveredCluster]));
    renderView();
    await waitForClusters();

    const hint = screen.getByTestId('discovered-hint');
    expect(hint.textContent).toMatch(/ArgoCD knows 1 more cluster Sharko doesn't manage/);
    // No individual cluster name and no bulk-select checkbox machinery —
    // the big card/table list is gone.
    expect(screen.queryByText('argo-known-cluster')).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/Select all discovered clusters/i)).not.toBeInTheDocument();
  });

  it('does not render the collapsed hint when there are no discovered clusters', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([]));
    renderView();
    await waitForClusters();

    expect(screen.queryByTestId('discovered-hint')).not.toBeInTheDocument();
  });

  it('clicking the collapsed hint opens Register New Cluster pre-set to "I do"', async () => {
    mockGetClusters.mockResolvedValue(clustersResponse([discoveredCluster]));
    renderView();
    await waitForClusters();

    fireEvent.click(screen.getByRole('button', { name: /adopt them from Register New Cluster/i }));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });
    expect(ownershipSelect().value).toBe('user');
    // Preset "I do" surfaces the picker immediately, no extra click needed.
    expect(screen.getByTestId('discovered-picker')).toBeInTheDocument();
  });
});
