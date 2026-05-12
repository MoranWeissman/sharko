import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// V125-1-7 / BUG-058 — orphan cluster Secret surface + cleanup.
// V125-1-7.1 — button copy rename: "Delete cluster Secret" → "Discard cancelled registration".
//
// Pinned behaviours:
//
//   1. The "Cancelled / Orphan Registrations" section renders one row
//      per orphan with name, server URL, last seen, and a destructive
//      "Discard cancelled registration" button (updated V125-1-7.1).
//   2. Section is absent when orphan_registrations is empty/undefined.
//   3. Click Discard → ConfirmationModal opens with title "Discard this
//      cancelled registration?" → confirm with "Discard" button →
//      deleteOrphanCluster called with cluster name → on success refetch fires.
//   4. Orphan cluster names are filtered OUT of the Managed and
//      Discovered sections — defence-in-depth alongside the BE filter.
//   5. Success banner reads "Cancelled registration for ... discarded." (V125-1-7.1).

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockDeleteOrphanCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
  },
  registerCluster: vi.fn(),
  discoverEKSClusters: vi.fn(),
  testClusterConnection: vi.fn(),
  unadoptCluster: vi.fn(),
  deleteOrphanCluster: (...args: unknown[]) => mockDeleteOrphanCluster(...args),
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

describe('ClustersOverview — V125-1-7 orphan cluster surface', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('renders the Cancelled / Orphan Registrations section per orphan with delete button (BUG-058)', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [
        {
          cluster_name: 'kind-orphan',
          server_url: 'https://kind-orphan.local:6443',
          last_seen_at: '2026-05-10T12:00:00Z',
        },
      ],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText(/Cancelled \/ Orphan Registrations/i)).toBeInTheDocument();
    });
    expect(screen.getByText('kind-orphan')).toBeInTheDocument();
    expect(screen.getByText('https://kind-orphan.local:6443')).toBeInTheDocument();
    expect(screen.getByText('2026-05-10T12:00:00Z')).toBeInTheDocument();

    // V125-1-7.1: button label renamed to user mental model.
    const deleteBtn = screen.getByRole('button', { name: /Discard cancelled registration for kind-orphan/i });
    expect(deleteBtn).toBeInTheDocument();
    expect(deleteBtn).toHaveTextContent('Discard cancelled registration');
  });

  it('does not render the Orphan section when the array is empty or undefined', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      // orphan_registrations omitted entirely — older server response shape.
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });
    expect(screen.queryByText(/Cancelled \/ Orphan Registrations/i)).not.toBeInTheDocument();
  });

  it('filters orphan cluster names out of the Discovered section (defence-in-depth)', async () => {
    // `kind-orphan` appears as both a not_in_git cluster AND in
    // orphan_registrations. The FE filter must keep it OUT of the
    // Discovered section — orphans only legitimately belong in the
    // Cancelled / Orphan Registrations row above. Even if the BE forgets
    // to strip it, this FE filter is the second line of defence.
    mockGetClusters.mockResolvedValue({
      clusters: [
        {
          name: 'kind-orphan',
          labels: {},
          managed: false,
          connection_status: 'not_in_git',
          server_version: 'v1.30.0',
        },
        {
          // Unrelated discovered cluster that MUST still render.
          name: 'real-discovered',
          labels: {},
          managed: false,
          connection_status: 'not_in_git',
          server_version: 'v1.29.0',
        },
      ],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 2 },
      pending_registrations: [],
      orphan_registrations: [
        {
          cluster_name: 'kind-orphan',
          server_url: 'https://kind-orphan.local:6443',
          last_seen_at: '2026-05-10T12:00:00Z',
        },
      ],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText(/Discovered Clusters/i)).toBeInTheDocument();
    });

    expect(screen.getByText('real-discovered')).toBeInTheDocument();

    // Discovered count should read "1", not "2", because kind-orphan
    // was filtered out into the orphan section.
    const discoveredHeader = screen.getByText(/Discovered Clusters/i).closest('h3');
    expect(discoveredHeader).toBeTruthy();
    expect(discoveredHeader!.textContent).toMatch(/Discovered Clusters\s*1/);

    // kind-orphan still renders ONCE — in the orphan section table,
    // never in Discovered.
    const allKindOrphan = screen.getAllByText('kind-orphan');
    expect(allKindOrphan.length).toBe(1);
    const tableForOrphan = allKindOrphan[0].closest('table');
    expect(tableForOrphan).toBeTruthy();
    const headers = Array.from(tableForOrphan!.querySelectorAll('th')).map(th => th.textContent ?? '');
    expect(headers.some(h => h.match(/Server URL/i))).toBe(true);
    expect(headers.some(h => h.match(/Last Seen/i))).toBe(true);
  });

  it('Delete button click → confirm flow → API call fires with cluster name + refetches', async () => {
    let getClustersCallCount = 0;
    mockGetClusters.mockImplementation(() => {
      getClustersCallCount += 1;
      // First call returns the orphan; subsequent calls (after delete)
      // return the post-delete state — empty.
      if (getClustersCallCount === 1) {
        return Promise.resolve({
          clusters: [],
          health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
          pending_registrations: [],
          orphan_registrations: [
            {
              cluster_name: 'kind-orphan',
              server_url: 'https://kind-orphan.local:6443',
              last_seen_at: '2026-05-10T12:00:00Z',
            },
          ],
        });
      }
      return Promise.resolve({
        clusters: [],
        health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
        pending_registrations: [],
        orphan_registrations: [],
      });
    });
    mockDeleteOrphanCluster.mockResolvedValue(undefined);

    renderView();

    await waitFor(() => {
      expect(screen.getByText('kind-orphan')).toBeInTheDocument();
    });

    // V125-1-7.1: Click the renamed "Discard cancelled registration" button.
    fireEvent.click(screen.getByRole('button', { name: /Discard cancelled registration for kind-orphan/i }));

    // Wait for the dialog. V125-1-7.1: new title "Discard this cancelled registration?".
    await waitFor(() => {
      expect(screen.getByText(/Discard this cancelled registration\?/i)).toBeInTheDocument();
    });

    // V125-1-7.1: modal body explains user mental model (no "Secret" terminology).
    expect(screen.getByText(/The Secret was created when you started registering this cluster/i)).toBeInTheDocument();

    // V125-1-7.1: Confirm button is now labelled "Discard" (not "Delete cluster Secret").
    const confirmBtns = screen.getAllByRole('button', { name: /^Discard$/i });
    // The dialog's confirm button is the one without aria-label override.
    const confirmBtn = confirmBtns.find(b => !b.getAttribute('aria-label'));
    expect(confirmBtn).toBeTruthy();
    fireEvent.click(confirmBtn!);

    await waitFor(() => {
      expect(mockDeleteOrphanCluster).toHaveBeenCalledTimes(1);
    });
    expect(mockDeleteOrphanCluster).toHaveBeenCalledWith('kind-orphan');

    // Refetch fires after success → getClusters call count > 1.
    await waitFor(() => {
      expect(getClustersCallCount).toBeGreaterThan(1);
    });

    // V125-1-7.1: success banner uses updated copy (no "cluster Secret" terminology).
    await waitFor(() => {
      expect(screen.getByText(/Cancelled registration for "kind-orphan" discarded/i)).toBeInTheDocument();
    });
  });

  it('modal title is "Discard this cancelled registration?" (V125-1-7.1)', async () => {
    // Pinned regression test for the V125-1-7.1 modal title rename.
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [
        { cluster_name: 'kind-orphan', server_url: 'https://kind-orphan.local:6443', last_seen_at: '2026-05-10T12:00:00Z' },
      ],
    });
    mockDeleteOrphanCluster.mockResolvedValue(undefined);

    renderView();
    await waitFor(() => expect(screen.getByText('kind-orphan')).toBeInTheDocument());

    fireEvent.click(screen.getByRole('button', { name: /Discard cancelled registration for kind-orphan/i }));

    await waitFor(() => {
      expect(screen.getByText(/Discard this cancelled registration\?/i)).toBeInTheDocument();
    });
    // Action button must say "Discard", not "Delete" or "Delete cluster Secret".
    const discardBtn = screen.getAllByRole('button').find(b => b.textContent?.trim() === 'Discard');
    expect(discardBtn).toBeTruthy();
  });

  it('aria-label on delete button uses new copy (V125-1-7.1)', async () => {
    // aria-label must match new copy for screen-reader accessibility.
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [
        { cluster_name: 'kind-orphan', server_url: 'https://kind-orphan.local:6443', last_seen_at: '2026-05-10T12:00:00Z' },
      ],
    });

    renderView();
    await waitFor(() => expect(screen.getByText('kind-orphan')).toBeInTheDocument());

    const btn = screen.getByRole('button', { name: /Discard cancelled registration for kind-orphan/i });
    expect(btn.getAttribute('aria-label')).toBe('Discard cancelled registration for kind-orphan');

    // Old aria-label must NOT exist.
    const oldBtn = screen.queryByRole('button', { name: /Delete cluster Secret for kind-orphan/i });
    expect(oldBtn).toBeNull();
  });
});
