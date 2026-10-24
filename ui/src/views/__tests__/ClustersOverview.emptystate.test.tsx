import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';

// Episode 2 redo findings F4 and F5. These tests pin the exact sentences a
// new user reads on the Clusters page and in the Register New Cluster dialog.

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

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusters: (...args: unknown[]) => mockGetClusters(...args),
      getAddonCatalog: (...args: unknown[]) => mockGetAddonCatalog(...args),
      health: (...args: unknown[]) => mockHealth(...args),
      // Inline kubeconfig is off by default, so the list has two options.
      getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: false }),
    },
  };
});

const emptyResponse = {
  clusters: [],
  health_stats: { total_in_git: 0, connected: 0, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
};

const oneFailedResponse = {
  clusters: [{ name: 'staging-us', labels: {}, server_version: '1.27', connection_status: 'failed' }],
  health_stats: { total_in_git: 1, connected: 0, failed: 1, missing_from_argocd: 0, not_in_git: 0 },
};

const EMPTY_SENTENCE = 'No clusters are registered yet. Click Add Cluster to register your first one.';
const FILTER_SENTENCE = 'No managed clusters match the current filters.';

function renderView(url = '/clusters') {
  return render(
    <AuthContext.Provider value={adminAuth}>
      <MemoryRouter initialEntries={[url]}>
        <ClustersOverview />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('ClustersOverview empty page (F5)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockHealth.mockResolvedValue({ status: 'healthy', version: 'test', cluster_test_available: true });
  });

  it('says nothing is registered yet when there are no clusters and no filter', async () => {
    mockGetClusters.mockResolvedValue(emptyResponse);
    renderView();
    await waitFor(() => {
      expect(screen.getByText(EMPTY_SENTENCE)).toBeInTheDocument();
    });
    expect(screen.queryByText(FILTER_SENTENCE)).not.toBeInTheDocument();
  });

  it('keeps the filter sentence when a filter hides the rows', async () => {
    mockGetClusters.mockResolvedValue(oneFailedResponse);
    renderView('/clusters?status=connected');
    await waitFor(() => {
      expect(screen.getByText(FILTER_SENTENCE)).toBeInTheDocument();
    });
    expect(screen.queryByText(EMPTY_SENTENCE)).not.toBeInTheDocument();
  });
});

describe('Register New Cluster dialog text (F4)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusters.mockResolvedValue(emptyResponse);
    mockGetAddonCatalog.mockResolvedValue({ addons: [] });
    mockHealth.mockResolvedValue({ status: 'healthy', version: 'test', cluster_test_available: true });
  });

  it('is true about what is required and what is optional', async () => {
    renderView();
    await waitFor(() => {
      expect(screen.getByText(EMPTY_SENTENCE)).toBeInTheDocument();
    });
    fireEvent.click(screen.getByRole('button', { name: /add cluster/i }));
    await waitFor(() => {
      expect(screen.getByText('Register New Cluster')).toBeInTheDocument();
    });
    // With no source chosen yet, the hint says a choice is required.
    expect(
      screen.getByText('Required — choose one of the options in the list before registering.'),
    ).toBeInTheDocument();
    // Round S (S37): the "add the connection details later" line said the
    // opposite of "Required" right under it. It is gone.
    expect(screen.queryByText(/You can add the connection details later/)).not.toBeInTheDocument();
    expect(screen.queryByText(/The connection details are optional/)).not.toBeInTheDocument();
    // The old wording is gone.
    expect(screen.queryByText(/three options/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Connection credentials below are optional/)).not.toBeInTheDocument();
    // The select really has two options plus the placeholder.
    const select = screen.getByDisplayValue(/Choose where this cluster's credentials come from/i);
    expect(select.querySelectorAll('option:not([disabled])')).toHaveLength(2);

    // After a choice, the sentence is said once, not twice, and nothing
    // mentions a section that is not on screen.
    fireEvent.change(select, { target: { value: 'secret-kubeconfig' } });
    expect(screen.queryByText(/These details are optional/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Advanced settings/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Connection credentials are optional/)).not.toBeInTheDocument();
  });
});
