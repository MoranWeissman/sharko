import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// V125-1.5 / BUG-050..055 — pending-PR registration UX hole.
//
// Three behaviours pinned here:
//
//   1. The "Pending Registrations" surface renders a row per open
//      registration PR with a working "View PR" link. (BUG-053)
//
//   2. Clusters whose names appear in pending_registrations are
//      filtered OUT of the Managed and Discovered sections so the same
//      cluster never appears in two places. (BUG-051, BUG-052)
//
//   3. Manual-mode register (auto_merge=false) shows a "PR opened —
//      merge to activate" banner with a clickable PR link, NOT the
//      misleading "Cluster registered" wording. (BUG-050)

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  // Other named exports used by the view module — stubbed to no-ops so
  // the test only exercises the surfaces under test.
  discoverEKSClusters: vi.fn(),
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

describe('ClustersOverview — V125-1.5 pending-PR registration surface', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('renders a Pending Registrations section per open PR with a working View PR link (BUG-053)', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [
        {
          cluster_name: 'kind-local',
          pr_url: 'https://github.com/org/repo/pull/42',
          branch: 'sharko/register-cluster-kind-local-abcd1234',
          opened_at: '2026-05-10T08:00:00Z',
        },
      ],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText(/Pending Registrations/i)).toBeInTheDocument();
    });
    expect(screen.getByText('kind-local')).toBeInTheDocument();
    expect(screen.getByText('sharko/register-cluster-kind-local-abcd1234')).toBeInTheDocument();
    expect(screen.getByText('2026-05-10T08:00:00Z')).toBeInTheDocument();

    const viewPRLink = screen.getByRole('link', { name: /View PR/i }) as HTMLAnchorElement;
    expect(viewPRLink.href).toBe('https://github.com/org/repo/pull/42');
    expect(viewPRLink.target).toBe('_blank');
    expect(viewPRLink.rel).toContain('noopener');
  });

  it('does not render the Pending Registrations section when the array is empty or undefined', async () => {
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      // pending_registrations omitted entirely — older server response shape.
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });
    expect(screen.queryByText(/Pending Registrations/i)).not.toBeInTheDocument();
  });

  it('filters pending-PR cluster names out of the Discovered section (BUG-052)', async () => {
    // `kind-local` appears BOTH as a not_in_git cluster AND in
    // pending_registrations. The FE must keep it OUT of the Discovered
    // table — the cluster only legitimately belongs in the Pending
    // Registrations row. (Even if the BE forgets to strip it, this FE
    // filter is the second line of defence.)
    mockGetClusters.mockResolvedValue({
      clusters: [
        {
          name: 'kind-local',
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
      pending_registrations: [
        {
          cluster_name: 'kind-local',
          pr_url: 'https://github.com/org/repo/pull/42',
          branch: 'sharko/register-cluster-kind-local-abcd1234',
          opened_at: '2026-05-10T08:00:00Z',
        },
      ],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText(/Discovered Clusters/i)).toBeInTheDocument();
    });

    // The unrelated discovered cluster renders.
    expect(screen.getByText('real-discovered')).toBeInTheDocument();

    // The Discovered Clusters count badge should read "1", not "2",
    // because kind-local was filtered out. The badge sits next to the
    // section header.
    const discoveredHeader = screen.getByText(/Discovered Clusters/i).closest('h3');
    expect(discoveredHeader).toBeTruthy();
    expect(discoveredHeader!.textContent).toMatch(/Discovered Clusters\s*1/);

    // kind-local DOES still render — but only in the Pending
    // Registrations table, never in Discovered. The Pending header sits
    // ABOVE Discovered in DOM order, so the first occurrence is the
    // pending row.
    const allKindLocal = screen.getAllByText('kind-local');
    expect(allKindLocal.length).toBe(1);
    // It should not be inside the Discovered table — assert by walking
    // the parent <table> chain.
    const tableForKindLocal = allKindLocal[0].closest('table');
    expect(tableForKindLocal).toBeTruthy();
    // The Pending table header includes "Cluster Name" + "Branch" +
    // "Opened" — pin one of those to confirm the row landed there.
    const headers = Array.from(tableForKindLocal!.querySelectorAll('th')).map(th => th.textContent ?? '');
    expect(headers.some(h => h.match(/Branch/i))).toBe(true);
    expect(headers.some(h => h.match(/Opened/i))).toBe(true);
  });
});

describe('ClustersOverview — V125-1.5 manual-mode register toast (BUG-050)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
    });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('shows "PR opened — merge to activate" wording when auto_merge is false', async () => {
    // Server returns merged: false + a PR URL — this is the manual-mode
    // path that the user reported as "the toast lies".
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      git: {
        merged: false,
        pr_url: 'https://github.com/org/repo/pull/77',
      },
    });

    renderView();

    // Open the dialog.
    await waitFor(() => {
      expect(screen.getByText('Clusters')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    // Switch to kubeconfig (the simplest "no AWS fields needed" path so
    // we don't need to mock additional discovery calls).
    fireEvent.change(screen.getByDisplayValue('Amazon EKS'), { target: { value: 'kubeconfig' } });
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'kind-local' } });
    fireEvent.change(screen.getByPlaceholderText(/apiVersion: v1/i), {
      target: { value: 'apiVersion: v1\nkind: Config\nusers:\n- name: u\n  user:\n    token: abc' },
    });

    const submitButtons = screen.getAllByRole('button', { name: /^register/i });
    const submit = submitButtons.find(b => !b.hasAttribute('disabled'));
    expect(submit).toBeTruthy();
    fireEvent.click(submit!);

    await waitFor(() => {
      expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
    });

    // Banner appears with the new pending-mode wording. Critical: the
    // pre-V125-1.5 wording was "Cluster registered" which lied.
    await waitFor(() => {
      expect(screen.getByText(/PR opened — merge to activate/i)).toBeInTheDocument();
    });

    const prLink = screen.getByRole('link', { name: 'https://github.com/org/repo/pull/77' }) as HTMLAnchorElement;
    expect(prLink.href).toBe('https://github.com/org/repo/pull/77');
    expect(prLink.target).toBe('_blank');
  });
});
