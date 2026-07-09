import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-88.5 — the two-layer registration dialog. These tests pin:
//
//   1. Layer 1 (Identity) calls GET /system/capabilities when the dialog
//      opens and renders the "detected" copy with the identity ARN + method
//      when Sharko has an AWS identity.
//   2. Layer 1 renders the "not detected" copy + a setup-guide link when it
//      doesn't (and the docs-not-fetched-yet fallback behaves the same way).
//   3. Layer 2 lets registration proceed with connection-only info — no
//      kubeconfig / secret path required, for the default Sharko-managed
//      connection ownership (V2-cleanup-88.3 lazy credentials, surfaced in
//      the UI by this story).

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
const mockGetSystemCapabilities = vi.fn();

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
    getSystemCapabilities: (...args: unknown[]) => mockGetSystemCapabilities(...args),
  };
});

const emptyClusters = {
  clusters: [],
  health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
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

async function openAddDialog() {
  await waitFor(() => {
    expect(screen.getByText('Clusters')).toBeInTheDocument();
  });
  fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
  await waitFor(() => {
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
  });
}

describe('ClustersOverview — two-layer registration dialog (V2-cleanup-88.5)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusters.mockResolvedValue(emptyClusters);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockHealth.mockResolvedValue({ status: 'healthy', version: 'test', cluster_test_available: true });
  });

  it('Layer 1 shows the detected-identity copy with the ARN and method', async () => {
    mockGetSystemCapabilities.mockResolvedValue({
      aws: { detected: true, method: 'irsa', identity_arn: 'arn:aws:iam::123456789012:role/sharko-hub' },
      hub_platform: 'eks',
    });

    renderView();
    await openAddDialog();

    await waitFor(() => {
      expect(screen.getByTestId('identity-detected')).toBeInTheDocument();
    });
    expect(screen.getByText(/Sharko is running with an AWS identity/)).toBeInTheDocument();
    expect(screen.getByText('arn:aws:iam::123456789012:role/sharko-hub')).toBeInTheDocument();
    expect(screen.getByText(/\(irsa\)/)).toBeInTheDocument();
    expect(
      screen.getByText(/EKS clusters that trust this identity need no stored credentials/),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('identity-not-detected')).not.toBeInTheDocument();
  });

  it('Layer 1 shows the not-detected copy with a setup-guide link when Sharko has no AWS identity', async () => {
    mockGetSystemCapabilities.mockResolvedValue({
      aws: { detected: false, method: 'none' },
      hub_platform: 'unknown',
    });

    renderView();
    await openAddDialog();

    await waitFor(() => {
      expect(screen.getByTestId('identity-not-detected')).toBeInTheDocument();
    });
    expect(screen.getByText(/No AWS identity detected/)).toBeInTheDocument();
    const guideLink = screen.getByRole('link', { name: /see the setup guide/i });
    expect(guideLink).toHaveAttribute(
      'href',
      'https://sharko.readthedocs.io/en/latest/operator/eks-hub-and-spoke-identity/',
    );
    expect(screen.queryByTestId('identity-detected')).not.toBeInTheDocument();
  });

  it('Layer 1 falls back to the not-detected copy when the capabilities fetch fails, without blocking the form', async () => {
    mockGetSystemCapabilities.mockRejectedValue(new Error('network error'));

    renderView();
    await openAddDialog();

    await waitFor(() => {
      expect(screen.getByTestId('identity-not-detected')).toBeInTheDocument();
    });
    // The dialog itself is still usable — the identity fetch failing does
    // not block Layer 2.
    expect(
      screen.getByDisplayValue(/Choose where this cluster's credentials come from/i),
    ).toBeInTheDocument();
  });

  it('the "How identity-based access works" panel expands with the plain-English explanation + docs link', async () => {
    mockGetSystemCapabilities.mockResolvedValue({ aws: { detected: false, method: 'none' }, hub_platform: 'unknown' });

    renderView();
    await openAddDialog();

    expect(screen.queryByTestId('identity-how-it-works')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /how identity-based access works/i }));

    await waitFor(() => {
      expect(screen.getByTestId('identity-how-it-works')).toBeInTheDocument();
    });
    expect(screen.getByText(/one IAM role on the hub cluster/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /read the full guide/i })).toHaveAttribute(
      'href',
      'https://sharko.readthedocs.io/en/latest/operator/eks-hub-and-spoke-identity/',
    );
  });

  it('Layer 2 allows registering with connection-only info (no kubeconfig, no secret path)', async () => {
    mockGetSystemCapabilities.mockResolvedValue({ aws: { detected: false, method: 'none' }, hub_platform: 'unknown' });
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      git: { merged: true, pr_url: 'https://github.com/org/repo/pull/1' },
    });

    renderView();
    await openAddDialog();

    // Point at a secret store (the recommended path) but leave the actual
    // secret path empty — connection credentials are optional.
    fireEvent.change(
      screen.getByDisplayValue(/Choose where this cluster's credentials come from/i),
      { target: { value: 'secret-kubeconfig' } },
    );
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'connection-only-cluster' },
    });

    const registerBtn = screen.getByRole('button', { name: /^register$/i });
    expect(registerBtn).not.toBeDisabled();
    fireEvent.click(registerBtn);

    await waitFor(() => {
      expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
    });
    const payload = mockRegisterCluster.mock.calls[0][0];
    expect(payload.name).toBe('connection-only-cluster');
    expect(payload.creds_source).toBe('secret-kubeconfig');
    expect(payload.secret_path).toBeUndefined();

    // The GitOps story surfaces on success.
    await waitFor(() => {
      expect(
        screen.getByText(/This registration opens a pull request in your GitOps repo/),
      ).toBeInTheDocument();
    });
  });

  it('the optional-credentials note names "connection credentials" and "addon secrets" plainly', async () => {
    mockGetSystemCapabilities.mockResolvedValue({ aws: { detected: false, method: 'none' }, hub_platform: 'unknown' });

    renderView();
    await openAddDialog();

    expect(
      screen.getByText(/Connection credentials below are\s*optional/),
    ).toBeInTheDocument();
    expect(screen.getByText(/an addon that carries addon secrets/)).toBeInTheDocument();
  });
});
