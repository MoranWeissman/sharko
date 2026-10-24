import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import {
  ClustersOverview,
  AVAILABLE_CHECK_FAILED,
  PICKER_ENTER_BY_HAND,
  latestTestOf,
} from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// Round R (R17): the Clusters page and the clusters ArgoCD already knows.
// Each test fails without its fix.

const adminAuth = {
  token: 't', username: 'admin', role: 'admin', login: vi.fn(), logout: vi.fn(),
  isAuthenticated: true, isAdmin: true, loading: false, error: null,
};

const mockGetClusters = vi.fn();
const mockGetAvailable = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    testClusterConnection: vi.fn(),
    adoptClusters: vi.fn(),
    api: {
      getClusters: (...a: unknown[]) => mockGetClusters(...a),
      getAddonCatalog: () => Promise.resolve({ addons: [] }),
      health: () => Promise.resolve({ status: 'healthy', version: 't', cluster_test_available: true }),
      getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
      getAvailableClusters: (...a: unknown[]) => mockGetAvailable(...a),
    },
  };
});

const managed = {
  name: 'sharko-managed', labels: {}, managed: true,
  connection_status: 'connected', server_version: '1.29',
};
const known = {
  name: 'argo-known', labels: {}, managed: false, connection_status: 'not_in_git',
  server_url: 'https://argo-known.example.com:6443',
};

function response(extra: Partial<Record<string, unknown>> = {}) {
  return {
    clusters: [managed],
    health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
    pending_registrations: [],
    orphan_registrations: [],
    ...extra,
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

const hint = () => screen.queryByTestId('argocd-known-hint');

// The filter bar shows only with five or more clusters.
const fiveManaged = [1, 2, 3, 4, 5].map((n) => ({ ...managed, name: `sharko-${n}` }));
const fiveStats = { total_in_git: 5, connected: 5, failed: 0, missing_from_argocd: 0, not_in_git: 0 };

describe('ClustersOverview - round R', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetAvailable.mockResolvedValue([]);
  });

  it('the one cluster ArgoCD knows can be let go of, and the fields for a typed cluster show', async () => {
    mockGetClusters.mockResolvedValue(response({ clusters: [managed, known] }));
    renderView();
    await screen.findByText('sharko-managed');
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await screen.findByText('Register New Cluster');
    fireEvent.change(screen.getAllByRole('combobox')[0], { target: { value: 'user' } });
    // Picked for the user, so the typed-cluster fields are hidden.
    expect(await screen.findByRole('button', { name: 'Adopt argo-known' })).not.toBeDisabled();
    expect(screen.queryByPlaceholderText('e.g. prod-us-east-1')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Enter a different cluster by hand' }));
    expect(PICKER_ENTER_BY_HAND).toBe('Enter a different cluster by hand');
    // Let go, and it stays let go.
    expect(await screen.findByPlaceholderText('e.g. prod-us-east-1')).toBeInTheDocument();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30));
    });
    expect(screen.getByRole('button', { name: 'Adopt cluster' })).toBeDisabled();
    expect(screen.getByPlaceholderText('e.g. prod-us-east-1')).toBeInTheDocument();
  });

  it('an older, slower answer from /clusters/available does not replace a newer one', async () => {
    mockGetClusters.mockResolvedValue(response());
    let releaseOld: (v: unknown) => void = () => {};
    mockGetAvailable
      .mockReturnValueOnce(new Promise((r) => { releaseOld = r; }))
      .mockResolvedValueOnce([{ name: 'new-one' }]);
    renderView();
    await screen.findByText('sharko-managed');
    fireEvent.click(screen.getByTitle('Refresh'));
    await waitFor(() => expect(hint()).toHaveTextContent("1 cluster ArgoCD already knows that Sharko doesn't manage yet."));
    await act(async () => {
      releaseOld([{ name: 'old-a' }, { name: 'old-b' }]);
      await Promise.resolve();
    });
    expect(hint()).toHaveTextContent("1 cluster ArgoCD already knows that Sharko doesn't manage yet.");
  });

  it('a failed /clusters/available keeps the last list and says it could not check', async () => {
    mockGetClusters.mockResolvedValue(response());
    mockGetAvailable
      .mockResolvedValueOnce([{ name: 'extra-one' }])
      .mockRejectedValueOnce(new Error('boom'));
    renderView();
    await waitFor(() => expect(hint()).toHaveTextContent("1 cluster ArgoCD already knows that Sharko doesn't manage yet."));
    expect(screen.queryByTestId('argocd-known-failed')).not.toBeInTheDocument();
    fireEvent.click(screen.getByTitle('Refresh'));
    expect(await screen.findByText("Sharko could not check which other clusters ArgoCD knows. Refresh to try again.")).toBeInTheDocument();
    expect(AVAILABLE_CHECK_FAILED).toBe("Sharko could not check which other clusters ArgoCD knows. Refresh to try again.");
    expect(hint()).toHaveTextContent("1 cluster ArgoCD already knows that Sharko doesn't manage yet.");
  });

  it('the search box hides clusters that only /clusters/available lists', async () => {
    mockGetClusters.mockResolvedValue(response({ clusters: fiveManaged, health_stats: fiveStats }));
    mockGetAvailable.mockResolvedValue([{ name: 'extra-one' }]);
    renderView();
    await waitFor(() => expect(hint()).not.toBeNull());
    fireEvent.change(screen.getByPlaceholderText('Search clusters by name...'), { target: { value: 'sharko' } });
    await waitFor(() => expect(hint()).toBeNull());
  });

  it('a cluster the search hides does not come back from /clusters/available', async () => {
    mockGetClusters.mockResolvedValue(response({
      clusters: [...fiveManaged, known],
      health_stats: { ...fiveStats, not_in_git: 1 },
    }));
    mockGetAvailable.mockResolvedValue([{ name: 'argo-known', server_url: known.server_url }]);
    renderView();
    await waitFor(() => expect(hint()).toHaveTextContent('1 cluster'));
    fireEvent.change(screen.getByPlaceholderText('Search clusters by name...'), { target: { value: 'sharko' } });
    await waitFor(() => expect(hint()).toBeNull());
  });

  it('a cluster /clusters lists is never added again from /clusters/available', async () => {
    // /clusters lists "odd" as failed. The status filter hides it. The copy
    // from /clusters/available must not slip back in under the filter.
    mockGetClusters.mockResolvedValue(response({
      clusters: [...fiveManaged, { name: 'odd', labels: {}, managed: false, connection_status: 'failed' }],
      health_stats: { ...fiveStats, not_in_git: 1 },
    }));
    mockGetAvailable.mockResolvedValue([{ name: 'odd' }]);
    renderView();
    await waitFor(() => expect(hint()).toHaveTextContent('1 cluster'));
    fireEvent.change(document.getElementById('managed-status-filter')!, { target: { value: 'not_in_git' } });
    await waitFor(() => expect(hint()).toBeNull());
  });

  it('a cluster with any open pull request is not offered as one ArgoCD knows', async () => {
    mockGetClusters.mockResolvedValue(response({
      pending_registrations: [
        { cluster_name: 'adopting-one', branch: 'b', pr_url: 'https://x/pull/3', operation: 'adopt', pr_number: 3 },
      ],
    }));
    mockGetAvailable.mockResolvedValue([{ name: 'adopting-one' }]);
    renderView();
    await screen.findByText('Pending Changes');
    expect(hint()).toBeNull();
  });

  it('the pending table never says "#0", and an unknown operation reads "Change"', async () => {
    mockGetClusters.mockResolvedValue(response({
      pending_registrations: [
        { cluster_name: 'odd-one', branch: 'b', pr_url: 'https://x/pull/new', operation: 'rename', pr_number: 0 },
      ],
    }));
    renderView();
    expect(await screen.findByText('View PR')).toBeInTheDocument();
    expect(screen.queryByText(/#0/)).not.toBeInTheDocument();
    expect(screen.getByRole('cell', { name: 'Change' })).toBeInTheDocument();
  });
});

describe('latestTestOf - what the list says about a test run on this page', () => {
  it('a failed sign-in lookup is "not verified", not a failure', () => {
    expect(latestTestOf({ success: false, stage: 'credentials', error_message: 'x' })).toEqual({
      success: false,
      notVerified: true,
    });
  });
  it('a pass only when the answer says so', () => {
    expect(latestTestOf({ success: true })?.success).toBe(true);
    expect(latestTestOf({ reachable: true })?.success).toBe(true);
    expect(latestTestOf({ reachable: true, success: false })?.success).toBe(false);
    expect(latestTestOf({ error: 'Failed' })?.success).toBe(false);
    expect(latestTestOf({ reachable: false, error: 'Failed' })?.success).toBe(false);
  });
  it('carries when the test finished', () => {
    expect(latestTestOf({ success: true }, '2026-10-23T10:00:00Z')?.at).toBe('2026-10-23T10:00:00Z');
  });
});
