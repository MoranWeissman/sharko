import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';
import { CONN_OWNER_USER_LABEL } from '@/components/ConnectionOwnerBadge';

// V2-cleanup-57.2 — "connection managed by: me" (self-managed ArgoCD
// connections). These tests pin the UI half of the contract:
//
//   1. The Register New Cluster dialog asks "Who manages the ArgoCD
//      connection?" with the two plain-English options, defaulting to
//      Sharko.
//   2. Choosing "I do" sends connection_managed_by: 'user' in the register
//      payload; the default sends NO connection_managed_by key at all
//      (byte-compat with pre-57.2 servers).
//   3. Choosing "I do" makes the credential inputs optional — the Register
//      button is enabled with an empty kubeconfig.
//   4. A cluster whose API record carries connection_managed_by: 'user'
//      renders the read-only "connection: managed by you" caption with the
//      explanatory tooltip; Sharko-managed clusters render no caption.

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

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusters: (...args: unknown[]) => mockGetClusters(...args),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      health: (...args: unknown[]) => mockHealth(...args),
    },
    registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  };
});

const clustersResponse = {
  clusters: [
    {
      name: 'byo-conn',
      labels: { monitoring: 'enabled' },
      server_version: '1.29',
      connection_status: 'connected',
      // Self-managed connection — the field under test.
      connection_managed_by: 'user',
    },
    {
      name: 'sharko-owned',
      labels: { monitoring: 'enabled' },
      server_version: '1.28',
      connection_status: 'connected',
      // No connection_managed_by — Sharko-managed default.
    },
  ],
  health_stats: {
    total_in_git: 2,
    connected: 2,
    failed: 0,
    missing_from_argocd: 0,
    not_in_git: 0,
  },
};

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
    expect(screen.getByText('byo-conn')).toBeInTheDocument();
  });
}

async function openAddDialog() {
  await waitForClusters();
  fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
  await waitFor(() => {
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
  });
}

function ownershipSelect(): HTMLSelectElement {
  return screen.getByDisplayValue('Sharko (default)') as HTMLSelectElement;
}

describe('ClustersOverview — self-managed connections (V2-cleanup-57.2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusters.mockResolvedValue(clustersResponse);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockHealth.mockResolvedValue({
      status: 'healthy',
      version: 'test',
      cluster_test_available: true,
    });
    mockRegisterCluster.mockResolvedValue({ status: 'success', git: { merged: true } });
  });

  // V2-cleanup-61.2 (D4): the ownership note moved from an always-visible
  // row caption into the composite status pill's accessible popover — one
  // pill per row, details on demand (keyboard + touch friendly).
  it('shows the "connection: managed by you" note ONLY in the self-managed cluster popover', async () => {
    renderView();
    await waitForClusters();

    const pills = screen.getAllByTestId('cluster-status-pill');
    expect(pills).toHaveLength(2);

    // byo-conn renders first (self-managed) — its popover carries the note.
    fireEvent.click(pills[0]);
    await waitFor(() => {
      expect(screen.getByText(new RegExp(CONN_OWNER_USER_LABEL))).toBeInTheDocument();
    });
    expect(screen.getByText(new RegExp('never writes, rotates, or deletes'))).toBeInTheDocument();

    // Close and open the Sharko-managed cluster's popover — no note.
    fireEvent.click(pills[0]);
    fireEvent.click(pills[1]);
    await waitFor(() => {
      expect(screen.getAllByText('ArgoCD → cluster').length).toBeGreaterThanOrEqual(1);
    });
    expect(screen.queryByText(new RegExp(CONN_OWNER_USER_LABEL))).not.toBeInTheDocument();
  });

  it('asks "Who manages the ArgoCD connection?" with Sharko as the default', async () => {
    renderView();
    await openAddDialog();

    expect(screen.getByText('Who manages the ArgoCD connection?')).toBeInTheDocument();
    const select = ownershipSelect();
    expect(select.value).toBe('sharko');
    // Both options are present, in plain English.
    expect(screen.getByText('Sharko (default)')).toBeInTheDocument();
    expect(screen.getByText('I do — Sharko only manages addon labels')).toBeInTheDocument();
    // Default hint explains Sharko ownership.
    expect(
      screen.getByText(/Sharko creates the ArgoCD cluster secret and keeps its credentials up to date/),
    ).toBeInTheDocument();
  });

  it('explains the self-managed choice when "I do" is selected', async () => {
    renderView();
    await openAddDialog();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });

    expect(
      screen.getByText(/Sharko never touches its credentials — it only keeps the addon labels on it in sync/),
    ).toBeInTheDocument();
  });

  it('sends connection_managed_by: user when "I do" is chosen — with no kubeconfig required', async () => {
    renderView();
    await openAddDialog();

    fireEvent.change(ownershipSelect(), { target: { value: 'user' } });
    // Switch to the inline-kubeconfig source and leave it EMPTY — allowed
    // for self-managed connections (credentials optional).
    fireEvent.change(
      screen.getByDisplayValue(/Choose where this cluster's credentials come from/i),
      { target: { value: 'inline-kubeconfig' } },
    );
    fireEvent.change(screen.getByPlaceholderText('e.g. prod-us-east-1'), {
      target: { value: 'my-byo-cluster' },
    });

    const registerBtn = screen.getByRole('button', { name: 'Register' });
    expect(registerBtn).not.toBeDisabled();
    fireEvent.click(registerBtn);

    await waitFor(() => {
      expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
    });
    const payload = mockRegisterCluster.mock.calls[0][0];
    expect(payload.name).toBe('my-byo-cluster');
    expect(payload.connection_managed_by).toBe('user');
  });

  it('omits connection_managed_by entirely for the Sharko default', async () => {
    renderView();
    await openAddDialog();

    // Default ownership (sharko) + an EXPLICIT EKS source (V2-cleanup-60.4:
    // there is no silent creds-source default anymore): register.
    fireEvent.change(
      screen.getByDisplayValue(/Choose where this cluster's credentials come from/i),
      { target: { value: 'eks-token' } },
    );
    fireEvent.change(screen.getByPlaceholderText('e.g. prod-us-east-1'), {
      target: { value: 'normal-cluster' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Register' }));

    await waitFor(() => {
      expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
    });
    const payload = mockRegisterCluster.mock.calls[0][0];
    expect('connection_managed_by' in payload).toBe(false);
  });

  // V2-cleanup-88.3/88.5 superseded this: connection credentials are now
  // optional for EVERY connection-ownership mode, not just self-managed
  // ("I do"). The backend accepts an empty kubeconfig for every creds_source
  // (skip_credentials step) — the two-layer dialog's Layer 2 must allow
  // registering with connection-only info, Sharko-managed included.
  it('allows Sharko-managed inline registration with an empty kubeconfig (connection credentials are optional)', async () => {
    renderView();
    await openAddDialog();

    fireEvent.change(
      screen.getByDisplayValue(/Choose where this cluster's credentials come from/i),
      { target: { value: 'inline-kubeconfig' } },
    );
    fireEvent.change(screen.getByPlaceholderText('e.g. prod-us-east-1'), {
      target: { value: 'needs-kubeconfig' },
    });

    // Ownership is sharko (default) and the kubeconfig is empty — no
    // longer blocked (V2-cleanup-88.5, contract 3: connection-only info is
    // enough to register).
    const registerBtn = screen.getByRole('button', { name: 'Register' });
    expect(registerBtn).not.toBeDisabled();
  });
});
