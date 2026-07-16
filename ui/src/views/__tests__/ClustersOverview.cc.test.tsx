import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// V3-CC1+CC2 — connection clarity after register + clearer method names.
//
// CC1: after a successful auto-merge register (merged=true), the connection
// test fires automatically and surfaces the result (reachable / failed+reason)
// inline — no Test button hunt. On PR-pending (merged=false), no false test.
//
// CC2: connection-source option labels + hints state what-you-provide + expiry
// + AWS/IAM requirement. eks-token conveys "short-lived AWS tokens / nothing stored".

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();
const mockTestClusterConnection = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
    getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: true }),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  testClusterConnection: (...args: unknown[]) => mockTestClusterConnection(...args),
  unadoptCluster: vi.fn(),
  deleteOrphanCluster: vi.fn(),
  isTestClusterUnavailable: vi.fn(() => false),
  getSystemCapabilities: vi.fn(() => Promise.resolve({ aws: { detected: false, method: 'none' }, hub_platform: 'unknown' })),
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

describe('ClustersOverview — V3-CC1: auto-test after register', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [],
    });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('auto-tests the cluster after a successful merged registration (reachable=true)', async () => {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      git: { merged: true, pr_url: 'https://github.com/org/repo/pull/100' },
    });

    // Auto-test returns reachable
    mockTestClusterConnection.mockResolvedValue({
      reachable: true,
      success: true,
      server_version: 'v1.29.3',
      platform: 'eks',
    });

    // After register completes, return the cluster as managed
    mockGetClusters.mockResolvedValue({
      clusters: [
        {
          name: 'prod-eks',
          labels: {},
          managed: true,
          connection_status: 'Connected',
          server_version: 'v1.29.3',
        },
      ],
      health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [],
    });

    renderView();

    // Open register dialog
    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    // Fill in required fields
    const nameInput = screen.getByPlaceholderText('e.g. prod-us-east-1');
    fireEvent.change(nameInput, { target: { value: 'prod-eks' } });

    // Pick eks-token
    const credsSelect = screen.getByDisplayValue(/Choose where/i);
    fireEvent.change(credsSelect, { target: { value: 'eks-token' } });

    // Submit
    const registerButton = screen.getByRole('button', { name: /^Register$/i });
    fireEvent.click(registerButton);

    // Wait for success banner
    await waitFor(() => {
      expect(screen.getByText(/Cluster registered/i)).toBeInTheDocument();
    });

    // Wait for auto-test to fire and render result
    await waitFor(() => {
      expect(mockTestClusterConnection).toHaveBeenCalledWith('prod-eks');
    });

    await waitFor(() => {
      // Reachable status should be visible (rendered by renderTestResult) —
      // look for the version + platform string that the test mock returned
      expect(screen.getByText(/v1\.29\.3.*eks/i)).toBeInTheDocument();
    });
  });

  it('auto-tests the cluster after a successful merged registration (reachable=false + error)', async () => {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      git: { merged: true, pr_url: 'https://github.com/org/repo/pull/101' },
    });

    // Auto-test returns failure with error
    mockTestClusterConnection.mockResolvedValue({
      reachable: false,
      success: false,
      error: 'Failed to retrieve secret for cluster prod-eks from AWS Secrets Manager: SecretNotFound',
      suggestions: ['secret-1', 'secret-2'],
    });

    mockGetClusters.mockResolvedValue({
      clusters: [
        {
          name: 'prod-eks',
          labels: {},
          managed: true,
          connection_status: 'Failed',
          server_version: '',
        },
      ],
      health_stats: { total_in_git: 1, connected: 0, failed: 1, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    const nameInput = screen.getByPlaceholderText('e.g. prod-us-east-1');
    fireEvent.change(nameInput, { target: { value: 'prod-eks' } });

    const credsSelect = screen.getByDisplayValue(/Choose where/i);
    fireEvent.change(credsSelect, { target: { value: 'eks-token' } });

    const registerButton = screen.getByRole('button', { name: /^Register$/i });
    fireEvent.click(registerButton);

    await waitFor(() => {
      expect(screen.getByText(/Cluster registered/i)).toBeInTheDocument();
    });

    await waitFor(() => {
      expect(mockTestClusterConnection).toHaveBeenCalledWith('prod-eks');
    });

    // Wait for failure reason to show
    await waitFor(() => {
      expect(screen.getByText(/SecretNotFound/i)).toBeInTheDocument();
    });
  });

  it('does NOT auto-test on PR-pending registration (merged=false)', async () => {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      git: { merged: false, pr_url: 'https://github.com/org/repo/pull/102' },
    });

    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [
        { cluster_name: 'pending-cluster', pr_url: 'https://github.com/org/repo/pull/102', branch: 'sharko/register-pending-cluster' },
      ],
      orphan_registrations: [],
    });

    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    const nameInput = screen.getByPlaceholderText('e.g. prod-us-east-1');
    fireEvent.change(nameInput, { target: { value: 'pending-cluster' } });

    const credsSelect = screen.getByDisplayValue(/Choose where/i);
    fireEvent.change(credsSelect, { target: { value: 'eks-token' } });

    const registerButton = screen.getByRole('button', { name: /^Register$/i });
    fireEvent.click(registerButton);

    // Wait for pending banner
    await waitFor(() => {
      expect(screen.getByText(/PR opened — merge to activate/i)).toBeInTheDocument();
    });

    // Ensure auto-test was NOT called
    await new Promise((resolve) => setTimeout(resolve, 500));
    expect(mockTestClusterConnection).not.toHaveBeenCalled();
  });
});

describe('ClustersOverview — V3-CC2: clearer connection-method names', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetClusters.mockResolvedValue({
      clusters: [],
      health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
      pending_registrations: [],
      orphan_registrations: [],
    });
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('renders the eks-token option label as "Amazon EKS — Use a short-lived token from AWS"', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    // Check the eks-token option text (V3-RW1.2)
    const eksOption = screen.getByRole('option', { name: /Amazon EKS — Use a short-lived token from AWS/i });
    expect(eksOption).toBeInTheDocument();
  });

  it('renders the eks-token hint stating AWS/IAM requirement + no rotation', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    const credsSelect = screen.getByDisplayValue(/Choose where/i);
    fireEvent.change(credsSelect, { target: { value: 'eks-token' } });

    // Check the hint text (V3-RW1.2)
    await waitFor(() => {
      expect(
        screen.getByText(/Sharko generates a short-lived AWS token.*nothing to store or rotate.*EKS only.*Sharko needs AWS access/i)
      ).toBeInTheDocument();
    });
  });

  it('renders the inline-kubeconfig option label as "Paste kubeconfig (stored)"', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    const inlineOption = screen.getByRole('option', { name: /Paste kubeconfig \(stored\)/i });
    expect(inlineOption).toBeInTheDocument();
  });

  it('renders the inline-kubeconfig hint stating token expiry', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    const credsSelect = screen.getByDisplayValue(/Choose where/i);
    fireEvent.change(credsSelect, { target: { value: 'inline-kubeconfig' } });

    await waitFor(() => {
      expect(
        screen.getByText(/Paste the file once.*Sharko stores it.*token inside can expire.*re-paste when it does/i)
      ).toBeInTheDocument();
    });
  });

  it('renders the secret-kubeconfig option label as "Kubeconfig from a secrets backend"', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    const secretOption = screen.getByRole('option', { name: /Kubeconfig from a secrets backend/i });
    expect(secretOption).toBeInTheDocument();
  });

  it('renders the secret-kubeconfig hint stating token lifetime', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Add Cluster')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText('Add Cluster'));

    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });

    const credsSelect = screen.getByDisplayValue(/Choose where/i);
    fireEvent.change(credsSelect, { target: { value: 'secret-kubeconfig' } });

    await waitFor(() => {
      expect(
        screen.getByText(/Sharko fetches the kubeconfig from your secrets backend.*token lifetime is whatever you stored/i)
      ).toBeInTheDocument();
    });
  });
});
