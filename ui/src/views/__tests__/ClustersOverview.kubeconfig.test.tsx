import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// V125-1.1 — UI behavior for the new "Generic K8s (kubeconfig)" provider
// option in the Register New Cluster dialog. These tests exercise:
//
//   1. Dropdown shape: kubeconfig is enabled; GKE / AKS still "coming soon".
//   2. Form re-shaping: selecting kubeconfig HIDES the AWS-shaped fields
//      (region / role ARN / secret path) and SHOWS a kubeconfig textarea
//      with the documented helper text.
//   3. Submit payload: the form sends provider="kubeconfig" + kubeconfig
//      and DOES NOT include AWS-only fields (the server returns 400 for
//      a kubeconfig request that smuggles region/secret_path).

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
}));

const baseClusters = {
  clusters: [],
  health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
};

function renderView() {
  // The Add Cluster button is admin-gated via RoleGuard. Seed sessionStorage
  // with an admin token+role before mounting AuthProvider so the button
  // renders and the dialog can be opened.
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

async function openAddDialog() {
  // Wait for the page to render, then click "Add Cluster" in the header.
  await waitFor(() => {
    expect(screen.getByText('Clusters')).toBeInTheDocument();
  });
  const addButton = screen.getByRole('button', { name: /add cluster/i });
  fireEvent.click(addButton);
  await waitFor(() => {
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
  });
}

describe('ClustersOverview — V125-1.1 kubeconfig provider', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetClusters.mockResolvedValue(baseClusters);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    // The AuthProvider verifies its token by hitting /api/v1/health on mount.
    // Stub fetch globally so the verification is a no-op success.
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('renders the Generic K8s (kubeconfig) option as enabled and keeps GKE/AKS disabled', async () => {
    renderView();
    await openAddDialog();

    // The provider <select> is the only one in the dialog.
    const select = screen.getByDisplayValue('Amazon EKS') as HTMLSelectElement;
    const options = Array.from(select.options);

    const kubeconfigOption = options.find(o => o.value === 'kubeconfig');
    expect(kubeconfigOption).toBeTruthy();
    expect(kubeconfigOption!.disabled).toBe(false);
    expect(kubeconfigOption!.textContent).toMatch(/kubeconfig/i);

    const gkeOption = options.find(o => o.value === 'gke');
    expect(gkeOption!.disabled).toBe(true);
    expect(gkeOption!.textContent).toMatch(/coming soon/i);

    const aksOption = options.find(o => o.value === 'aks');
    expect(aksOption!.disabled).toBe(true);
    expect(aksOption!.textContent).toMatch(/coming soon/i);
  });

  it('selecting kubeconfig hides AWS fields and shows the kubeconfig textarea + helper text', async () => {
    renderView();
    await openAddDialog();

    // Start: EKS form shows the AWS-shaped fields. role ARN is unique to
    // the direct-mode EKS form (discovery mode uses a different placeholder).
    expect(screen.getByPlaceholderText(/arn:aws:iam/i)).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/Override AWS SM secret name/i)).toBeInTheDocument();

    // Switch to kubeconfig.
    const select = screen.getByDisplayValue('Amazon EKS') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'kubeconfig' } });

    // AWS fields gone.
    expect(screen.queryByPlaceholderText(/arn:aws:iam/i)).not.toBeInTheDocument();
    expect(screen.queryByPlaceholderText(/Override AWS SM secret name/i)).not.toBeInTheDocument();

    // Kubeconfig textarea + bearer-token-only helper text present.
    const textarea = screen.getByPlaceholderText(/apiVersion: v1/i) as HTMLTextAreaElement;
    expect(textarea).toBeInTheDocument();
    expect(textarea.tagName).toBe('TEXTAREA');
    expect(screen.getByText(/bearer-token authentication is supported/i)).toBeInTheDocument();
    expect(screen.getByText(/kubectl create token/i)).toBeInTheDocument();
  });

  it('submits a kubeconfig-shaped payload (no region/secret_path/role_arn)', async () => {
    mockRegisterCluster.mockResolvedValue({ status: 'success', git: { merged: true } });
    renderView();
    await openAddDialog();

    // Select kubeconfig provider.
    const select = screen.getByDisplayValue('Amazon EKS') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'kubeconfig' } });

    // Fill cluster name + kubeconfig.
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'kind-test' },
    });
    fireEvent.change(screen.getByPlaceholderText(/apiVersion: v1/i), {
      target: { value: 'apiVersion: v1\nkind: Config\nusers:\n- name: u\n  user:\n    token: abc' },
    });

    // The Register button should now be enabled.
    const submitButtons = screen.getAllByRole('button', { name: /^register/i });
    const submit = submitButtons.find(b => !b.hasAttribute('disabled'));
    expect(submit).toBeTruthy();
    fireEvent.click(submit!);

    await waitFor(() => {
      expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
    });

    const payload = mockRegisterCluster.mock.calls[0][0];
    expect(payload.provider).toBe('kubeconfig');
    expect(payload.name).toBe('kind-test');
    expect(payload.kubeconfig).toContain('apiVersion: v1');
    // Cross-field exclusion — the wizard MUST NOT smuggle AWS-shaped
    // fields into a kubeconfig request (the server would 400 if it did).
    expect(payload.region).toBeUndefined();
    expect(payload.secret_path).toBeUndefined();
    expect(payload.role_arn).toBeUndefined();
  });
});
