import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail, NOT_MANAGED_TITLE, notManagedDescription, CLUSTER_NOT_FOUND_DESCRIPTION } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

// Round R (R15): the page for a cluster ArgoCD knows and Sharko does not
// manage. These tests do NOT mock the api module. They stub `fetch` and
// answer the real URL the page calls, with the real body the server sends.
// Two earlier tests passed only because they mocked a field onto a call
// that never carries it; answering by URL stops that from happening again.

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

const NAME = 'legacy-1';
const COMPARISON_URL = `/api/v1/clusters/${NAME}/comparison`;
const NOT_MANAGED_BODY = {
  error: `Sharko does not manage ${NAME} yet. ArgoCD already connects to it, so you can adopt it or take it over.`,
  code: 'not_managed',
  argocd_known: true,
};

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const managedComparison = (extra: Record<string, unknown> = {}) => ({
  cluster: { name: NAME, labels: {}, connection_status: 'connected', ...extra },
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

// What the comparison URL answers. Tests change it to move the server on.
let comparison: () => Response;
// How long /clusters/available takes to answer, in ms.
let availableDelay = 0;
const calls: string[] = [];

function installFetch() {
  calls.length = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      const path = url.split('?')[0];
      calls.push(path);
      if (path === COMPARISON_URL) return comparison();
      if (path === '/api/v1/clusters/available') {
        if (availableDelay > 0) await new Promise((r) => setTimeout(r, availableDelay));
        return jsonResponse(200, { clusters: [{ name: NAME, server_url: 'https://legacy.example.com' }] });
      }
      if (path === '/api/v1/prs') return jsonResponse(200, { prs: [] });
      if (path === '/api/v1/connections/') return jsonResponse(200, { connections: [], active_connection: '' });
      if (path === `/api/v1/clusters/${NAME}/test`) {
        return jsonResponse(200, { success: true, reachable: true, server_version: 'v1.31.0' });
      }
      return jsonResponse(200, {});
    }),
  );
}

function renderPage() {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={[`/clusters/${NAME}`]}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

const comparisonCalls = () => calls.filter((c) => c === COMPARISON_URL).length;

describe('ClusterDetail — not managed, against the real comparison answer', () => {
  beforeEach(() => {
    localStorage.setItem('sharko-auth-token', 'test-token');
    comparison = () => jsonResponse(404, NOT_MANAGED_BODY);
    availableDelay = 0;
    installFetch();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it('shows the not-managed page with its exact title and sentence', async () => {
    renderPage();
    expect(await screen.findByText("Sharko doesn't manage this cluster yet")).toBeInTheDocument();
    expect(
      screen.getByText(
        'ArgoCD already knows "legacy-1". Adopt it to bring it under Sharko, or take ownership of its connection.',
      ),
    ).toBeInTheDocument();
    expect(NOT_MANAGED_TITLE).toBe("Sharko doesn't manage this cluster yet");
    expect(notManagedDescription('x')).not.toMatch(/catalog|not found|managed-clusters/i);
    expect(screen.queryByText(/Cluster "legacy-1" not found/i)).not.toBeInTheDocument();
    expect(screen.getByTestId('not-managed-adopt')).toBeInTheDocument();
    expect(screen.getByTestId('not-managed-takeover')).toHaveTextContent('Take ownership');
  });

  it('a background reload that fails again keeps the page on screen', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    renderPage();
    expect(await screen.findByText(NOT_MANAGED_TITLE)).toBeInTheDocument();
    const before = comparisonCalls();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(31_000);
    });
    await waitFor(() => expect(comparisonCalls()).toBeGreaterThan(before));
    expect(screen.getByText(NOT_MANAGED_TITLE)).toBeInTheDocument();
    expect(screen.getByTestId('not-managed-adopt')).toBeInTheDocument();
  });

  it('no background reload while the Adopt dialog is open, and the dialog keeps its place', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderPage();
    await user.click(await screen.findByTestId('not-managed-adopt'));
    expect(await screen.findByText('Adopt Clusters')).toBeInTheDocument();
    const before = comparisonCalls();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(65_000);
    });
    expect(comparisonCalls()).toBe(before);
    expect(screen.getByText('Adopt Clusters')).toBeInTheDocument();
  });

  it('the Adopt dialog shows the address ArgoCD has for the cluster', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByTestId('not-managed-adopt'));
    expect(await screen.findByText('https://legacy.example.com')).toBeInTheDocument();
  });

  it('the Adopt dialog does not start over when the page renders again', async () => {
    // The address answers late, after the dialog is open. That makes the
    // page render again with a new list of clusters for the dialog.
    availableDelay = 300;
    const testCalls = () => calls.filter((c) => c === `/api/v1/clusters/${NAME}/test`).length;
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByTestId('not-managed-adopt'));
    await waitFor(() => expect(testCalls()).toBe(1));
    expect(screen.queryByText('https://legacy.example.com')).not.toBeInTheDocument();
    // The address lands. The dialog shows it, and does not run its check again.
    expect(await screen.findByText('https://legacy.example.com', {}, { timeout: 2000 })).toBeInTheDocument();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(testCalls()).toBe(1);
  });

  it('after an adopt finishes, closing the dialog reloads the page', async () => {
    const user = userEvent.setup();
    renderPage();
    await user.click(await screen.findByTestId('not-managed-adopt'));
    await screen.findByText('https://legacy.example.com');
    vi.mocked(fetch).mockImplementation(async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      const path = url.split('?')[0];
      calls.push(path);
      if (path === '/api/v1/clusters/adopt') {
        return jsonResponse(200, { results: [{ name: NAME, status: 'success', merged: true }] });
      }
      if (path === COMPARISON_URL) return comparison();
      if (path === '/api/v1/prs') return jsonResponse(200, { prs: [] });
      return jsonResponse(200, {});
    });
    await user.click(await screen.findByRole('button', { name: /confirm adoption/i }));
    expect(await screen.findByText('Adoption complete.')).toBeInTheDocument();
    // Still on screen: no reload yet.
    const before = comparisonCalls();
    // The server now knows the cluster.
    comparison = () => jsonResponse(200, managedComparison({ adopted: true }));
    const closeButtons = screen.getAllByRole('button', { name: /^close$/i });
    await user.click(closeButtons[closeButtons.length - 1]);
    await waitFor(() => expect(comparisonCalls()).toBeGreaterThan(before));
    await waitFor(() => expect(screen.queryByText(NOT_MANAGED_TITLE)).not.toBeInTheDocument());
  });

  it('a plain 404 with no code is still "not found"', async () => {
    comparison = () => jsonResponse(404, { error: 'cluster not found' });
    renderPage();
    expect(await screen.findByText(/not found/i)).toBeInTheDocument();
    expect(screen.queryByText(NOT_MANAGED_TITLE)).not.toBeInTheDocument();
  });

  // S38: the table of open pull requests is "Pending Registrations" only when
  // every row is a register; otherwise it is "Pending Changes". Name both.
  it('the not-found page names the pending table by both of its real names', async () => {
    comparison = () => jsonResponse(404, { error: 'cluster not found' });
    renderPage();
    const want =
      'This cluster is not in your list of managed clusters. ' +
      'It may have been registered via a pending PR that has not yet merged. ' +
      'Open the Clusters page and look for an open PR under "Pending Registrations" or "Pending Changes".';
    expect(await screen.findByText(want)).toBeInTheDocument();
    expect(CLUSTER_NOT_FOUND_DESCRIPTION).toBe(want);
    expect(document.body.textContent).not.toContain('look under "Pending Registrations" for an open PR');
  });
});

describe('ClusterDetail — "Remove the old labels" from the real comparison answer', () => {
  beforeEach(() => {
    localStorage.setItem('sharko-auth-token', 'test-token');
    installFetch();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    localStorage.clear();
  });

  it('shows for a cluster the comparison marks adopted', async () => {
    comparison = () => jsonResponse(200, managedComparison({ adopted: true }));
    renderPage();
    expect(await screen.findByTestId('open-drop-labels')).toBeInTheDocument();
  });

  it('shows for a cluster the comparison marks taken over', async () => {
    comparison = () => jsonResponse(200, managedComparison({ taken_over: true }));
    renderPage();
    expect(await screen.findByTestId('open-drop-labels')).toBeInTheDocument();
  });

  it('stays hidden when the comparison sends neither', async () => {
    comparison = () => jsonResponse(200, managedComparison());
    renderPage();
    await screen.findAllByText(NAME);
    expect(screen.queryByTestId('open-drop-labels')).not.toBeInTheDocument();
  });
});
