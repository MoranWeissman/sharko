import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// creds-reframe-2 — the Register New Cluster dialog now asks "How should
// Sharko get this cluster's credentials?" first (creds_source), and that
// choice drives which inputs appear. These tests exercise:
//
//   1. The creds-source <select> offers the 3 plain-English options.
//   2. Form re-shaping: choosing "Paste a kubeconfig" SHOWS the kubeconfig
//      textarea with the bearer-token helper text and HIDES the AWS fields.
//   3. Form re-shaping: choosing "Use a stored kubeconfig" SHOWS a
//      first-class "Secret name / path" input.
//   4. Submit payload (inline): sends creds_source='inline-kubeconfig' +
//      provider='kubeconfig' + kubeconfig, and NO AWS-only fields.
//   5. Submit payload (stored): sends creds_source='secret-kubeconfig' +
//      secret_path, and NO kubeconfig blob.

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
    // BUG-041: ClustersOverview reads cluster_test_available on mount.
    health: () => Promise.resolve({ status: 'healthy', cluster_test_available: true }),
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

// The creds-source <select> is identified by its current display value. The
// dialog opens defaulting to the EKS-token option.
function credsSourceSelect(): HTMLSelectElement {
  return screen.getByDisplayValue(/Amazon EKS — generate a token/i) as HTMLSelectElement;
}

describe('ClustersOverview — creds-reframe-2 credential source', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetClusters.mockResolvedValue(baseClusters);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    // The AuthProvider verifies its token by hitting /api/v1/health on mount.
    // Stub fetch globally so the verification is a no-op success.
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('offers the three plain-English credential-source options', async () => {
    renderView();
    await openAddDialog();

    const select = credsSourceSelect();
    const values = Array.from(select.options).map((o) => o.value);
    expect(values).toEqual([
      'inline-kubeconfig',
      'secret-kubeconfig',
      'eks-token',
    ]);
    // Default is the EKS token path.
    expect(select.value).toBe('eks-token');
  });

  it('choosing "Paste a kubeconfig" shows the textarea + helper text and hides AWS fields', async () => {
    renderView();
    await openAddDialog();

    // Start: EKS token form shows the AWS-shaped fields.
    expect(screen.getByPlaceholderText(/arn:aws:iam/i)).toBeInTheDocument();
    expect(screen.getByPlaceholderText(/Override AWS SM secret name/i)).toBeInTheDocument();

    fireEvent.change(credsSourceSelect(), { target: { value: 'inline-kubeconfig' } });

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

  it('choosing "Use a stored kubeconfig" shows a first-class Secret name / path input', async () => {
    renderView();
    await openAddDialog();

    fireEvent.change(credsSourceSelect(), { target: { value: 'secret-kubeconfig' } });

    expect(screen.getByText(/Secret name \/ path/i)).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText(/k8s-my-cluster or secret\/data\/clusters/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/holds this cluster's kubeconfig/i)).toBeInTheDocument();
    // No inline kubeconfig textarea on this path.
    expect(screen.queryByPlaceholderText(/apiVersion: v1/i)).not.toBeInTheDocument();
  });

  it('submits an inline-kubeconfig payload (no region/secret_path/role_arn)', async () => {
    mockRegisterCluster.mockResolvedValue({ status: 'success', git: { merged: true } });
    renderView();
    await openAddDialog();

    fireEvent.change(credsSourceSelect(), { target: { value: 'inline-kubeconfig' } });

    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'kind-test' },
    });
    fireEvent.change(screen.getByPlaceholderText(/apiVersion: v1/i), {
      target: { value: 'apiVersion: v1\nkind: Config\nusers:\n- name: u\n  user:\n    token: abc' },
    });

    const submitButtons = screen.getAllByRole('button', { name: /^register/i });
    const submit = submitButtons.find((b) => !b.hasAttribute('disabled'));
    expect(submit).toBeTruthy();
    fireEvent.click(submit!);

    await waitFor(() => {
      expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
    });

    const payload = mockRegisterCluster.mock.calls[0][0];
    expect(payload.creds_source).toBe('inline-kubeconfig');
    expect(payload.provider).toBe('kubeconfig');
    expect(payload.name).toBe('kind-test');
    expect(payload.kubeconfig).toContain('apiVersion: v1');
    // Cross-field exclusion — must NOT smuggle AWS-shaped fields into an
    // inline-kubeconfig request (the server would 400 if it did).
    expect(payload.region).toBeUndefined();
    expect(payload.secret_path).toBeUndefined();
    expect(payload.role_arn).toBeUndefined();
  });

  it('submits a secret-kubeconfig payload (secret_path, no kubeconfig blob)', async () => {
    mockRegisterCluster.mockResolvedValue({ status: 'success', git: { merged: true } });
    renderView();
    await openAddDialog();

    fireEvent.change(credsSourceSelect(), { target: { value: 'secret-kubeconfig' } });

    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'stored-cluster' },
    });
    fireEvent.change(screen.getByPlaceholderText(/k8s-my-cluster or secret\/data\/clusters/i), {
      target: { value: 'secret/data/clusters/stored-cluster' },
    });

    const submitButtons = screen.getAllByRole('button', { name: /^register/i });
    const submit = submitButtons.find((b) => !b.hasAttribute('disabled'));
    expect(submit).toBeTruthy();
    fireEvent.click(submit!);

    await waitFor(() => {
      expect(mockRegisterCluster).toHaveBeenCalledTimes(1);
    });

    const payload = mockRegisterCluster.mock.calls[0][0];
    expect(payload.creds_source).toBe('secret-kubeconfig');
    expect(payload.name).toBe('stored-cluster');
    expect(payload.secret_path).toBe('secret/data/clusters/stored-cluster');
    // The stored path carries no inline kubeconfig blob.
    expect(payload.kubeconfig).toBeUndefined();
  });

  it('blocks Register until the stored-kubeconfig secret path is filled', async () => {
    renderView();
    await openAddDialog();

    fireEvent.change(credsSourceSelect(), { target: { value: 'secret-kubeconfig' } });
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'stored-cluster' },
    });

    // With name but no secret path, every Register button stays disabled.
    const submitButtons = screen.getAllByRole('button', { name: /^register/i });
    expect(submitButtons.every((b) => b.hasAttribute('disabled'))).toBe(true);
  });
});
