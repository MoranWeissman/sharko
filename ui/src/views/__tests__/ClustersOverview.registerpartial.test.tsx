import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// LO-8 R6 — a real registration that stopped part-way (HTTP 207, status
// "partial"). Before this fix the dialog read only a `partial` boolean the
// server never sends, so a registration that could not read Git (no pull
// request at all) said "Cluster registration submitted as a pull request
// in your GitOps repo", which was not true.

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  ApiError: class ApiError extends Error {},
  testClusterConnection: vi.fn(() => Promise.resolve({ reachable: true })),
  unadoptCluster: vi.fn(),
}));

// The old guess for a "success" answer with no PR link. Banned (LO-9 R9).
const SUBMITTED_SENTENCE =
  'Cluster registration submitted as a pull request in your GitOps repo. Check the open PR list for status.';

const PR_NO_LINK_SENTENCE =
  "There is an open pull request that registers this cluster, but Sharko's answer had no link to it. Find it in your GitOps repo and merge it to finish the registration.";

function renderView() {
  localStorage.setItem('sharko-auth-token', 'test-token');
  localStorage.setItem('sharko-auth-user', 'tester');
  localStorage.setItem('sharko-auth-role', 'admin');
  return render(
    <MemoryRouter>
      <AuthProvider>
        <ClustersOverview />
      </AuthProvider>
    </MemoryRouter>,
  );
}

async function registerOnce() {
  await waitFor(() => {
    expect(screen.getByText('Clusters')).toBeInTheDocument();
  });
  fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
  await waitFor(() => {
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
  });
  fireEvent.change(screen.getByDisplayValue(/Choose where this cluster's credentials come from/i), {
    target: { value: 'eks-token' },
  });
  fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), { target: { value: 'kind-test' } });
  const submit = screen.getAllByRole('button', { name: /^register/i }).find((b) => !b.hasAttribute('disabled'));
  expect(submit).toBeTruthy();
  fireEvent.click(submit!);
  await waitFor(() => {
    expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
  });
}

describe('ClustersOverview — a registration that stopped part-way (LO-8 R6)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    localStorage.clear();
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
    });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('shows the server sentence in the dialog when Sharko could not read Git, and no pull request was opened', async () => {
    const msg =
      'Sharko could not read the cluster registry from Git, so it stopped before opening a pull request — writing one built from an empty file would have dropped every other cluster out of the registry. Nothing was committed. Sharko created 1 addon secret on the cluster. Try again once Git is reachable.';
    mockRegisterCluster.mockResolvedValue({
      status: 'partial',
      failed_step: 'read_cluster_registry',
      completed_steps: ['validate'],
      error: 'the git host did not answer',
      message: msg,
    });

    renderView();
    await registerOnce();

    expect(await screen.findByText(msg)).toBeInTheDocument();
    // The dialog stays open with what the user typed, so they can try again.
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    expect(screen.getByDisplayValue('kind-test')).toBeInTheDocument();
    expect(screen.queryByText(SUBMITTED_SENTENCE)).not.toBeInTheDocument();
    expect(screen.queryByText(/merge to activate/i)).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /View PR/i })).not.toBeInTheDocument();
  });

  it('says Sharko did not finish when the server sent no sentence and no pull request', async () => {
    mockRegisterCluster.mockResolvedValue({ status: 'partial', failed_step: 'git_commit' });

    renderView();
    await registerOnce();

    expect(await screen.findByText('Sharko did not finish registering this cluster.')).toBeInTheDocument();
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    expect(screen.queryByText(SUBMITTED_SENTENCE)).not.toBeInTheDocument();
  });

  it('shows the server sentence and the PR link when the pull request was opened but auto-merge failed', async () => {
    // The server no longer puts the address in the message; it is in
    // git.pr_url only (LO-10 R16).
    const msg =
      'Sharko opened the pull request that registers this cluster, but could not merge it. Merge it by hand to finish the registration.';
    const prUrl = 'https://github.com/org/repo/pull/9';
    mockRegisterCluster.mockResolvedValue({
      status: 'partial',
      failed_step: 'pr_merge',
      message: msg,
      git: { merged: false, pr_url: prUrl },
    });

    renderView();
    await registerOnce();

    expect(await screen.findByText(msg)).toBeInTheDocument();
    const link = screen.getByRole('link', { name: /View PR/i }) as HTMLAnchorElement;
    expect(link.href).toBe(prUrl);
    // LO-10 R16: the address shows once — as the "View PR" link — and is
    // not also printed as text. Before, the dialog showed it twice.
    const linksToPR = screen.getAllByRole('link').filter((a) => (a as HTMLAnchorElement).href === prUrl);
    expect(linksToPR).toHaveLength(1);
    expect(document.body.textContent ?? '').not.toContain(prUrl);
    expect(document.body.textContent ?? '').not.toContain('Pull request: ');
    // Not the normal blue "PR opened" outcome: a step did not finish.
    expect(screen.queryByText(/merge to activate/i)).not.toBeInTheDocument();
    expect(screen.queryByText(SUBMITTED_SENTENCE)).not.toBeInTheDocument();
  });

  it('does not say "Cluster registered." when the PR merged but some addon secrets failed', async () => {
    const msg = 'The cluster is registered, but 1 addon secret could not be created on it.';
    mockRegisterCluster.mockResolvedValue({
      status: 'partial',
      failed_step: 'create_secrets',
      message: msg,
      git: { merged: true, pr_url: 'https://github.com/org/repo/pull/10' },
    });

    renderView();
    await registerOnce();

    expect(await screen.findByText(msg)).toBeInTheDocument();
    expect((screen.getByRole('link', { name: /View PR/i }) as HTMLAnchorElement).href).toBe(
      'https://github.com/org/repo/pull/10',
    );
    expect(screen.queryByText(/^Cluster registered\./)).not.toBeInTheDocument();
  });

  it('says there is an open pull request with no link when a success answer has no PR link (LO-9 R9)', async () => {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      completed_steps: ['validate', 'git_commit'],
      git: { merged: false },
    });

    renderView();
    await registerOnce();

    expect(await screen.findByText(PR_NO_LINK_SENTENCE)).toBeInTheDocument();
    expect(screen.queryByText(SUBMITTED_SENTENCE)).not.toBeInTheDocument();
    expect(screen.queryByText(/submitted as a pull request/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Cluster registered/)).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: /View PR/i })).not.toBeInTheDocument();
  });
});
