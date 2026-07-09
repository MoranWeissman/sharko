import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// V2-cleanup-88.5 — the two-layer registration dialog. V2-cleanup-89.2
// shrank Layer 1 to a one-line strip (the full explainer moved to the
// System page — see SystemView.test.tsx). These tests pin:
//
//   1. Layer 1 (Identity strip) calls GET /system/capabilities when the
//      dialog opens and renders the one-line "detected" copy + a link to
//      the System page when Sharko has an AWS identity.
//   2. Layer 1 renders the one-line "not detected" copy + a setup-guide
//      link when it doesn't (and the docs-not-fetched-yet fallback behaves
//      the same way).
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

  it('Layer 1 shows the one-line detected-identity strip with a link to the System page', async () => {
    mockGetSystemCapabilities.mockResolvedValue({
      aws: { detected: true, method: 'irsa', identity_arn: 'arn:aws:iam::123456789012:role/sharko-hub' },
      hub_platform: 'eks',
    });

    renderView();
    await openAddDialog();

    await waitFor(() => {
      expect(screen.getByTestId('identity-strip-detected')).toBeInTheDocument();
    });
    expect(screen.getByText(/Sharko has an AWS identity/)).toBeInTheDocument();
    expect(
      screen.getByText(/EKS clusters that trust it need no stored credentials/),
    ).toBeInTheDocument();
    const systemLink = screen.getByRole('link', { name: 'System' });
    expect(systemLink).toHaveAttribute('href', '/system');
    expect(screen.queryByTestId('identity-strip-not-detected')).not.toBeInTheDocument();

    // The full explainer (ARN code block, method badge, expandable "how it
    // works" panel) no longer lives in the dialog — it moved to the System
    // page (V2-cleanup-89.2). See SystemView.test.tsx for that coverage.
    expect(screen.queryByText('arn:aws:iam::123456789012:role/sharko-hub')).not.toBeInTheDocument();
    expect(screen.queryByText(/\(irsa\)/)).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /how identity-based access works/i })).not.toBeInTheDocument();
  });

  it('Layer 1 shows the one-line not-detected strip with a setup-guide link when Sharko has no AWS identity', async () => {
    mockGetSystemCapabilities.mockResolvedValue({
      aws: { detected: false, method: 'none' },
      hub_platform: 'unknown',
    });

    renderView();
    await openAddDialog();

    await waitFor(() => {
      expect(screen.getByTestId('identity-strip-not-detected')).toBeInTheDocument();
    });
    expect(screen.getByText(/No AWS identity detected/)).toBeInTheDocument();
    const guideLink = screen.getByRole('link', { name: /see the setup guide/i });
    expect(guideLink).toHaveAttribute(
      'href',
      'https://sharko.readthedocs.io/en/latest/operator/eks-hub-and-spoke-identity/',
    );
    expect(screen.queryByTestId('identity-strip-detected')).not.toBeInTheDocument();
  });

  it('does not render the old "Layer 1" section header — the strip needs no header (V2-cleanup-89.8)', async () => {
    mockGetSystemCapabilities.mockResolvedValue({
      aws: { detected: true, method: 'irsa', identity_arn: 'arn:aws:iam::123456789012:role/sharko-hub' },
      hub_platform: 'eks',
    });

    renderView();
    await openAddDialog();

    await waitFor(() => {
      expect(screen.getByTestId('identity-strip-detected')).toBeInTheDocument();
    });
    expect(screen.queryByText('Layer 1 — Identity')).not.toBeInTheDocument();
    expect(screen.queryByText(/Layer 1/)).not.toBeInTheDocument();
    // The "Coordinates" section below also dropped its "Layer 2" header in
    // favor of a plain question.
    expect(screen.getByText('Where is this cluster?')).toBeInTheDocument();
    expect(screen.queryByText(/Layer 2/)).not.toBeInTheDocument();
  });

  it('Layer 1 falls back to the not-detected strip when the capabilities fetch fails, without blocking the form', async () => {
    mockGetSystemCapabilities.mockRejectedValue(new Error('network error'));

    renderView();
    await openAddDialog();

    await waitFor(() => {
      expect(screen.getByTestId('identity-strip-not-detected')).toBeInTheDocument();
    });
    // The dialog itself is still usable — the identity fetch failing does
    // not block Layer 2.
    expect(
      screen.getByDisplayValue(/Choose where this cluster's credentials come from/i),
    ).toBeInTheDocument();
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
