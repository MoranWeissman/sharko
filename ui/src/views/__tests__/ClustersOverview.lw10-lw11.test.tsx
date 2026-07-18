import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// V3 LW-10, LW-11 — pending registrations must NOT be counted as
// "disconnected" / "not_in_git", and "Not managed" is reframed as
// "Available to manage".
//
// LW-10 is fixed at the SOURCE (internal/api/clusters.go now excludes
// pending/orphan registrations from health_stats.not_in_git). The FE
// consumes health_stats.not_in_git directly — no client-side subtraction.
// These tests therefore assert the FE faithfully renders the ALREADY-CORRECT
// backend value: a pending cluster lives only in Pending Registrations and
// the not_in_git count the backend sends already excludes it.

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
  registerCluster: vi.fn(),
  testClusterConnection: vi.fn(),
  unadoptCluster: vi.fn(),
}));

function renderView() {
  sessionStorage.setItem('sharko-auth-token', 'test-token');
  sessionStorage.setItem('sharko-auth-user', 'tester');
  sessionStorage.setItem('sharko-auth-role', 'admin');
  return render(
    <MemoryRouter>
      <AuthProvider>
        <ClustersOverview />
      </AuthProvider>
    </MemoryRouter>,
  );
}

describe('ClustersOverview — LW-10: pending registrations excluded from disconnected count', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('renders the backend not_in_git count directly (pending already excluded at source) (LW-10)', async () => {
    // Backend now sends the CORRECT count: not_in_git = 2 counts only the
    // two genuinely-discovered clusters; the pending registration
    // (kind-local) is NOT in this count and NOT in the clusters array (the
    // handler prunes it into pending_registrations). Need >= 5 total
    // clusters to show stat cards (collapse threshold).
    mockGetClusters.mockResolvedValue({
      clusters: [
        // 3 managed clusters
        { name: 'prod-us', labels: {}, managed: true, connection_status: 'Successful' },
        { name: 'prod-eu', labels: {}, managed: true, connection_status: 'Failed' },
        { name: 'staging', labels: {}, managed: true, connection_status: 'Successful' },
        // 2 discovered clusters (truly unmanaged) — kind-local is NOT here.
        { name: 'real-discovered-1', labels: {}, managed: false, connection_status: 'not_in_git' },
        { name: 'real-discovered-2', labels: {}, managed: false, connection_status: 'not_in_git' },
      ],
      health_stats: {
        total_in_git: 3,
        connected: 2,
        failed: 1,
        missing_from_argocd: 0,
        // Correct backend value — excludes the pending kind-local.
        not_in_git: 2,
      },
      pending_registrations: [
        {
          cluster_name: 'kind-local',
          pr_url: 'https://github.com/org/repo/pull/42',
          branch: 'sharko/register-cluster-kind-local',
          opened_at: '2026-07-18T10:00:00Z',
        },
      ],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });

    // The "Available to manage" stat card renders with the backend value.
    await waitFor(() => {
      expect(screen.getAllByText('Available to manage').length).toBeGreaterThanOrEqual(1);
    });

    // The pending cluster appears ONLY in the Pending Registrations table,
    // never in a discovered/not_in_git surface.
    expect(screen.getByText(/Pending Registrations/i)).toBeInTheDocument();
    const kindLocal = screen.getAllByText('kind-local');
    expect(kindLocal.length).toBe(1);
    expect(kindLocal[0].closest('table')).toBeTruthy();
  });

  it('derives "All Clusters" total from the backend not_in_git (no FE subtraction) (LW-10)', async () => {
    // totalClusters = total_in_git + not_in_git = 3 + 2 = 5. The FE reads
    // not_in_git straight from the backend (which already excludes the
    // pending registration) — there is no client-side subtraction to drift.
    mockGetClusters.mockResolvedValue({
      clusters: [
        { name: 'prod-us', labels: {}, managed: true, connection_status: 'Successful' },
        { name: 'prod-eu', labels: {}, managed: true, connection_status: 'Successful' },
        { name: 'staging', labels: {}, managed: true, connection_status: 'Successful' },
        { name: 'real-discovered-1', labels: {}, managed: false, connection_status: 'not_in_git' },
        { name: 'real-discovered-2', labels: {}, managed: false, connection_status: 'not_in_git' },
      ],
      health_stats: {
        total_in_git: 3,
        connected: 3,
        failed: 0,
        missing_from_argocd: 0,
        not_in_git: 2, // correct backend value
      },
      pending_registrations: [
        {
          cluster_name: 'kind-local',
          pr_url: 'https://github.com/org/repo/pull/42',
          branch: 'sharko/register',
          opened_at: '2026-07-18T10:00:00Z',
        },
      ],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });

    // Stat cards render only when totalClusters >= 5, which confirms the
    // total = total_in_git + not_in_git = 5 formula resolved correctly.
    await waitFor(() => {
      expect(screen.getByText('All Clusters')).toBeInTheDocument();
      expect(screen.getAllByText('Available to manage').length).toBeGreaterThanOrEqual(1);
    });
  });
});

describe('ClustersOverview — LW-11: "Not managed" reframed as "Available to manage"', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetClusters.mockResolvedValue({
      clusters: [
        { name: 'prod-us', labels: {}, managed: true, connection_status: 'Successful' },
        { name: 'discovered-1', labels: {}, managed: false, connection_status: 'not_in_git' },
      ],
      health_stats: {
        total_in_git: 1,
        connected: 1,
        failed: 0,
        missing_from_argocd: 0,
        not_in_git: 1,
      },
      pending_registrations: [],
    });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('stat card shows "Available to manage" instead of "Not managed" (LW-11)', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });

    // New label appears
    expect(screen.getByText('Available to manage')).toBeInTheDocument();
    // Old label is gone
    expect(screen.queryByText('Not managed')).not.toBeInTheDocument();
  });

  it('legend shows "Available to manage" with the correct meaning (LW-11)', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });

    // The legend entry for the 'unmanaged' connection kind now reads
    // "Available to manage" with the same meaning as before.
    const legendItem = screen.getByText('Available to manage');
    expect(legendItem).toBeInTheDocument();
    // The meaning tooltip should still convey "ArgoCD knows this cluster,
    // but Sharko doesn't manage addons on it yet — adopt it."
    const tooltipContainer = legendItem.closest('[title]');
    expect(tooltipContainer).toHaveAttribute(
      'title',
      expect.stringContaining("In ArgoCD but not in Sharko's Git catalog"),
    );
  });
});
