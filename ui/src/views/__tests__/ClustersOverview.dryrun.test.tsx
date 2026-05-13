import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthProvider } from '@/hooks/useAuth';

// V125-1.4 (BUG-049) — preview-panel null-safety + tooltip presence on
// the cluster registration dialog.
//
// Two concerns covered here:
//
//   1. The DryRunResult preview MUST render without crashing when the
//      backend returns a partial shape (effective_addons / files / files_to_write
//      / secrets_to_create all null). Pre-V125-1.4 this raised
//      `Cannot read properties of null (reading 'length')` and was caught
//      by V124-2.3's ErrorBoundary.
//   2. The Preview, Register, and Auto-merge controls carry a `title=`
//      attribute explaining what they do — surfaced on hover/focus so
//      the operator doesn't have to click to find out what each does.

const mockGetClusters = vi.fn();
const mockGetAddonCatalog = vi.fn();
const mockRegisterCluster = vi.fn();

vi.mock('@/services/api', () => ({
  api: {
    getClusters: (...args: unknown[]) => mockGetClusters(...args),
    getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
  },
  registerCluster: (...args: unknown[]) => mockRegisterCluster(...args),
  discoverEKSClusters: vi.fn(),
  testClusterConnection: vi.fn(),
  unadoptCluster: vi.fn(),
}));

const baseClusters = {
  clusters: [],
  health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
};

function renderView() {
  // Add Cluster button is admin-gated via RoleGuard.
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
  await waitFor(() => {
    expect(screen.getByText('Clusters')).toBeInTheDocument();
  });
  const addButton = screen.getByRole('button', { name: /add cluster/i });
  fireEvent.click(addButton);
  await waitFor(() => {
    expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
  });
}

describe('ClustersOverview — V125-1.4 dry-run null safety + tooltips', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    sessionStorage.clear();
    mockGetClusters.mockResolvedValue(baseClusters);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve({ ok: true } as Response)));
  });

  it('renders the dry-run preview panel without crashing when arrays are null (BUG-049 regression guard)', async () => {
    // The pre-V125-1.4 BE could return null for effective_addons /
    // secrets_to_create on the kubeconfig path; the FE always saw `files`
    // as undefined because the BE field is `files_to_write`. This test
    // pins the view's null-safety: the panel must render the PR title
    // with no crash, no matter what is missing.
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      dry_run: {
        pr_title: 'sharko: register cluster kind-test (kubeconfig provider)',
        effective_addons: null,
        files: null,
        files_to_write: null,
        secrets_to_create: null,
      },
    });

    renderView();
    await openAddDialog();

    // Switch to kubeconfig + fill the minimum fields needed to enable the
    // Preview button (cluster name + kubeconfig text).
    const select = screen.getByDisplayValue('Amazon EKS') as HTMLSelectElement;
    fireEvent.change(select, { target: { value: 'kubeconfig' } });
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'kind-test' },
    });
    fireEvent.change(screen.getByPlaceholderText(/apiVersion: v1/i), {
      target: { value: 'apiVersion: v1\nkind: Config' },
    });

    // Click Preview.
    const previewBtn = screen.getByRole('button', { name: /preview/i });
    fireEvent.click(previewBtn);

    // Panel renders, shows the PR title, and does NOT crash. The three
    // optional sections (Effective Addons / Files / Secrets) are absent
    // because their arrays are null/empty — that's the correct shape.
    await waitFor(() => {
      expect(screen.getByText('Dry Run Preview')).toBeInTheDocument();
    });
    expect(screen.getByText(/kubeconfig provider/i)).toBeInTheDocument();
    expect(screen.queryByText(/Effective Addons:/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/^Files:$/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Secrets to Create:/i)).not.toBeInTheDocument();
  });

  it('renders the dry-run preview panel with files when the backend uses files_to_write (canonical key)', async () => {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      dry_run: {
        pr_title: 'sharko: register cluster kind-test',
        effective_addons: ['monitoring'],
        files_to_write: [
          { path: 'configuration/addons-clusters-values/kind-test.yaml', action: 'create' },
          { path: 'configuration/managed-clusters.yaml', action: 'update' },
        ],
        secrets_to_create: [],
      },
    });

    renderView();
    await openAddDialog();
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'kind-test' },
    });

    const previewBtn = screen.getByRole('button', { name: /preview/i });
    fireEvent.click(previewBtn);

    await waitFor(() => {
      expect(screen.getByText('Dry Run Preview')).toBeInTheDocument();
    });
    // Files section now renders from the canonical files_to_write key.
    expect(screen.getByText('Files:')).toBeInTheDocument();
    expect(screen.getByText(/kind-test\.yaml/)).toBeInTheDocument();
    expect(screen.getByText(/managed-clusters\.yaml/)).toBeInTheDocument();
    // Secrets section omitted — empty array, not null.
    expect(screen.queryByText(/Secrets to Create:/i)).not.toBeInTheDocument();
  });

  it('Preview button carries a tooltip explaining the dry-run action', async () => {
    renderView();
    await openAddDialog();

    const previewBtn = screen.getByRole('button', { name: /preview/i });
    expect(previewBtn).toHaveAttribute(
      'title',
      expect.stringContaining('Dry-run'),
    );
    // Sanity-check the substance of the tooltip — operator should know it
    // does not apply changes.
    expect(previewBtn.getAttribute('title')!.toLowerCase()).toContain('without actually applying');
  });

  it('Register submit button carries a tooltip explaining what registration does', async () => {
    renderView();
    await openAddDialog();

    // There are two "register" matches in the dialog — the dialog header
    // ("Register New Cluster") is text, not a button. The submit is the
    // unique button matching /^register/i in the footer.
    const registerBtn = screen.getByRole('button', { name: /^register$/i });
    const title = registerBtn.getAttribute('title');
    expect(title).toBeTruthy();
    expect(title!.toLowerCase()).toContain('argocd cluster secret');
    expect(title!.toLowerCase()).toContain('managed-clusters');
  });

  it('Auto-merge checkbox carries a tooltip explaining when the PR auto-merges vs waits for review', async () => {
    renderView();
    await openAddDialog();

    const checkbox = screen.getByRole('checkbox', { name: /merge pr automatically/i });
    const title = checkbox.getAttribute('title');
    expect(title).toBeTruthy();
    expect(title!.toLowerCase()).toMatch(/auto-merge|auto merges/);
  });

  it('Scan button (discovery mode) carries a tooltip explaining what it does', async () => {
    renderView();
    await openAddDialog();

    // Switch to Discovery mode.
    const discoveryToggle = screen.getByRole('button', { name: /discovery/i });
    fireEvent.click(discoveryToggle);

    const scanBtn = screen.getByRole('button', { name: /^scan$/i });
    const title = scanBtn.getAttribute('title');
    expect(title).toBeTruthy();
    expect(title!.toLowerCase()).toContain('aws');
    expect(title!.toLowerCase()).toContain('eks');
  });

  // Sanity: the dialog as a whole doesn't blow up after the dry-run renders;
  // the preview panel coexists with the action buttons and stays interactive.
  it('after rendering a partial-shape dry-run, the form remains interactive', async () => {
    mockRegisterCluster.mockResolvedValue({
      status: 'success',
      dry_run: {
        pr_title: 'sharko: register cluster kind-test',
        effective_addons: null,
        files_to_write: null,
        secrets_to_create: null,
      },
    });
    renderView();
    await openAddDialog();
    fireEvent.change(screen.getByPlaceholderText(/prod-us-east-1/i), {
      target: { value: 'kind-test' },
    });
    fireEvent.click(screen.getByRole('button', { name: /preview/i }));

    await waitFor(() => {
      expect(screen.getByText('Dry Run Preview')).toBeInTheDocument();
    });

    // Register button is still there and enabled (we have a valid name).
    const registerBtn = screen.getByRole('button', { name: /^register$/i });
    expect(registerBtn).not.toBeDisabled();

    // Look up the dialog content area to scope the tooltip-presence
    // assertion to the registration form (vs random page chrome).
    const dialog = screen.getByRole('dialog');
    const previewInDialog = within(dialog).getByRole('button', { name: /preview/i });
    expect(previewInDialog).toHaveAttribute('title');
  });
});
