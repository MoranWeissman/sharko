/**
 * V2-cleanup-39: "Ask AI" button on sync_failing addon rows.
 *
 * These tests live in a sibling file so the main ClusterDetail.test.tsx
 * stays focused on its existing concerns.  They share the same mock shape
 * but set `api.getAIStatus` to the value each case needs.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';
import { AuthContext } from '@/hooks/useAuth';

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

// Shared mock factories — each test overrides what it needs via
// mockGetAIStatus / mockGetClusterComparison.
const mockGetClusterComparison = vi.fn();
const mockGetAIStatus = vi.fn();
const mockFetchTrackedPRs = vi.fn();
const mockRestartAddonSync = vi.fn();

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return { ...actual, useNavigate: () => vi.fn() };
});

vi.mock('@/components/ToastNotification', async () => {
  const actual = await vi.importActual<typeof import('@/components/ToastNotification')>(
    '@/components/ToastNotification',
  );
  return { ...actual, showToast: vi.fn() };
});

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
      getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
      enableAddonOnCluster: vi.fn().mockResolvedValue({}),
      getAddonCatalog: vi.fn().mockResolvedValue({ addons: [] }),
      restartAddonSync: (...args: unknown[]) => mockRestartAddonSync(...args),
      getAIStatus: (...args: unknown[]) => mockGetAIStatus(...args),
    },
    testClusterConnection: vi.fn().mockResolvedValue({ reachable: true }),
    deregisterCluster: vi.fn().mockResolvedValue({}),
    updateClusterAddons: vi.fn().mockResolvedValue({}),
    updateClusterSettings: vi.fn().mockResolvedValue({}),
    fetchTrackedPRs: (...args: unknown[]) => mockFetchTrackedPRs(...args),
  };
});

// Comparison response with a keda addon that is sync_failing and carries a
// real ArgoCD operation message.
const syncFailingResponse = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.29',
    connection_status: 'connected',
  },
  git_total_addons: 2,
  git_enabled_addons: 2,
  git_disabled_addons: 0,
  argocd_total_applications: 2,
  argocd_healthy_applications: 1,
  argocd_synced_applications: 1,
  argocd_degraded_applications: 1,
  argocd_out_of_sync_applications: 1,
  addon_comparisons: [
    {
      addon_name: 'keda',
      git_configured: true,
      git_version: '2.13.0',
      git_enabled: true,
      has_version_override: false,
      argocd_deployed: true,
      argocd_health_status: 'Healthy',
      status: 'sync_failing',
      argocd_operation_message: 'one or more tasks failed: CRD name too long — keda.sh/....',
      issues: ['one or more synchronization tasks completed unsuccessfully'],
    },
    {
      addon_name: 'cert-manager',
      git_configured: true,
      git_version: '1.12.0',
      git_enabled: true,
      has_version_override: false,
      argocd_deployed: true,
      argocd_health_status: 'Healthy',
      status: 'healthy',
      issues: [],
    },
  ],
  total_healthy: 1,
  total_with_issues: 1,
  total_missing_in_argocd: 0,
  total_untracked_in_argocd: 0,
  total_disabled_in_git: 0,
};

function renderView(initialEntry = '/clusters/prod-eu?section=addons') {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/clusters/:name" element={<ClusterDetail />} />
        </Routes>
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('V2-cleanup-39: Ask AI button on sync_failing rows', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(syncFailingResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
    mockRestartAddonSync.mockResolvedValue({ terminated: true, synced: true });
  });

  // LW-20: The Ask AI (and Restart sync) buttons for a sync_failing row moved
  // INTO the Issues popover. They are no longer rendered inline — the operator
  // opens the row's count+severity chip (e.g. "1 error") to reveal them. These
  // tests open the popover first, then assert on the button, keeping the
  // original intent (Ask AI shows for sync_failing + AI enabled; not otherwise).
  it('renders Ask AI button on sync_failing row when AI is enabled (inside issues popover)', async () => {
    const user = userEvent.setup();
    mockGetAIStatus.mockResolvedValue({ enabled: true });

    renderView();
    await screen.findByText('prod-eu', {}, { timeout: 5000 });

    // keda (sync_failing) has 1 issue → "1 error" chip opens the popover.
    const chip = await screen.findByText('1 error');
    await user.click(chip);

    await waitFor(() => {
      expect(screen.getByTestId('ask-ai-btn')).toBeInTheDocument();
    });
  });

  it('does NOT offer Ask AI on a non-failing (healthy) row', async () => {
    mockGetAIStatus.mockResolvedValue({ enabled: true });

    renderView();
    await screen.findByText('prod-eu', {}, { timeout: 5000 });

    // A healthy row (cert-manager) has NO issues, so it shows "—" with no
    // issues chip — there is no popover to open and thus no Ask AI path.
    // The only issues chip on the page is keda's "1 error"; cert-manager has none.
    await waitFor(() => {
      expect(screen.getByText('1 error')).toBeInTheDocument();
    });
    const table = screen.getByRole('table');
    const certRow = within(table)
      .getAllByRole('row')
      .find((row) => within(row).queryByText('cert-manager'));
    expect(certRow).toBeTruthy();
    // No issues chip in the healthy row, so no way to reach Ask AI.
    expect(within(certRow as HTMLElement).queryByText(/error/i)).not.toBeInTheDocument();
    expect(within(certRow as HTMLElement).getByText('—')).toBeInTheDocument();

    // Only keda's popover carries Ask AI — exactly one issues chip exists.
    expect(screen.getAllByText(/^\d+ errors?$/)).toHaveLength(1);
  });

  it('does NOT render Ask AI button when AI is disabled (even after opening the issues popover)', async () => {
    const user = userEvent.setup();
    mockGetAIStatus.mockResolvedValue({ enabled: false });

    renderView();
    await screen.findByText('prod-eu', {}, { timeout: 5000 });

    // Open keda's issues popover — with AI disabled the Ask AI button must
    // still be absent (Restart sync may still be there, but no Ask AI).
    const chip = await screen.findByText('1 error');
    await user.click(chip);

    await waitFor(() => {
      expect(screen.getByTestId('restart-sync-btn')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('ask-ai-btn')).not.toBeInTheDocument();
  });

  it('dispatches open-assistant event with addon name and error text when clicked (from popover)', async () => {
    const user = userEvent.setup();
    mockGetAIStatus.mockResolvedValue({ enabled: true });

    const dispatchSpy = vi.spyOn(window, 'dispatchEvent');

    renderView();
    await screen.findByText('prod-eu', {}, { timeout: 5000 });

    // Open keda's issues popover, then click Ask AI inside it.
    const chip = await screen.findByText('1 error');
    await user.click(chip);

    const btn = await screen.findByTestId('ask-ai-btn');
    fireEvent.click(btn);

    // The event should be a CustomEvent named 'open-assistant'.
    expect(dispatchSpy).toHaveBeenCalled();
    const calls = dispatchSpy.mock.calls;
    const openAssistantCall = calls.find(
      ([evt]) => evt instanceof CustomEvent && (evt as CustomEvent).type === 'open-assistant',
    );
    expect(openAssistantCall).toBeDefined();

    // V2-cleanup-42: detail is now { message: string, nonce: string }
    const detail = (openAssistantCall![0] as CustomEvent).detail as { message: string; nonce: string };
    expect(typeof detail).toBe('object');
    expect(typeof detail.message).toBe('string');
    expect(typeof detail.nonce).toBe('string');
    expect(detail.message).toContain('keda');
    expect(detail.message).toContain('prod-eu');
    expect(detail.message).toContain('CRD name too long');
  });
});

// V2-cleanup-55.4: the AI assistant is OPT-IN. The "Ask AI" buttons on the
// connection banners (overview section) must also be hidden unless an AI
// provider is configured.
describe('V2-cleanup-55.4: Ask AI on connection banners is opt-in', () => {
  const argoFailedResponse = {
    ...syncFailingResponse,
    argocd_connection_status: 'Failed',
    argocd_connection_message: 'unable to reach apiserver',
    cluster_connection_state: '',
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(argoFailedResponse);
    mockFetchTrackedPRs.mockResolvedValue({ prs: [] });
  });

  it('renders Ask AI on the ArgoCD Connection Failed banner when AI is enabled', async () => {
    mockGetAIStatus.mockResolvedValue({ enabled: true });

    renderView('/clusters/prod-eu');
    await screen.findByText('prod-eu', {}, { timeout: 5000 });

    await waitFor(() => {
      expect(screen.getByText('ArgoCD Connection Failed')).toBeInTheDocument();
    });
    // V2-cleanup-78.1: the banner now renders above the default (addons)
    // section, where the sync_failing keda row ALSO has its own "Ask AI"
    // button — scope the assertion to the banner itself instead of a
    // page-wide text match so the two don't collide.
    const banner = screen.getByText('ArgoCD Connection Failed').closest('.rounded-xl');
    expect(banner).toBeTruthy();
    await waitFor(() => {
      expect(within(banner as HTMLElement).getByText('Ask AI')).toBeInTheDocument();
    });
  });

  it('does NOT render Ask AI on the banner when AI is disabled (default deployment)', async () => {
    mockGetAIStatus.mockResolvedValue({ enabled: false });

    renderView('/clusters/prod-eu');
    await screen.findByText('prod-eu', {}, { timeout: 5000 });

    // The banner itself still renders — only the AI affordance is gone.
    await waitFor(() => {
      expect(screen.getByText('ArgoCD Connection Failed')).toBeInTheDocument();
    });
    expect(screen.queryByText('Ask AI')).not.toBeInTheDocument();
  });
});
