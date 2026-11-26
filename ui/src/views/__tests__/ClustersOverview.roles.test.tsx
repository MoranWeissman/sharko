import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClustersOverview } from '@/views/ClustersOverview';
import { AuthContext } from '@/hooks/useAuth';
import { ApiError } from '@/services/api';

// Walk 5, serious 2 and 7. The server lets an operator register a cluster
// and run a connection test; a viewer may do neither. The screen must offer
// the buttons to exactly those people, and a refusal must never be drawn as a
// cluster that "fails".

function authFor(role: 'admin' | 'operator' | 'viewer') {
  return {
    token: 't',
    username: role,
    role,
    login: vi.fn(),
    logout: vi.fn(),
    isAuthenticated: true,
    isAdmin: role === 'admin',
    loading: false,
    error: null,
  };
}

const mockGetClusters = vi.fn();
const mockTest = vi.fn();

vi.mock('@/services/api', async () => {
  const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api');
  return {
    ...actual,
    api: {
      getClusters: (...a: unknown[]) => mockGetClusters(...a),
      getAddonCatalog: () => Promise.resolve({ addons: [] }),
      health: () => Promise.resolve({ status: 'healthy', version: 'test', cluster_test_available: true }),
      getAllowInlineCredentials: () => Promise.resolve({ allow_inline_credentials: false }),
    },
    testClusterConnection: (...a: unknown[]) => mockTest(...a),
  };
});

const oneCluster = {
  clusters: [{ name: 'spoke-1', labels: {}, server_version: '1.30', connection_status: 'Successful' }],
  health_stats: { total_in_git: 1, connected: 1, failed: 0, missing_from_argocd: 0, not_in_git: 0 },
};

function renderAs(role: 'admin' | 'operator' | 'viewer') {
  return render(
    <AuthContext.Provider value={authFor(role)}>
      <MemoryRouter initialEntries={['/clusters']}>
        <ClustersOverview />
      </MemoryRouter>
    </AuthContext.Provider>,
  );
}

describe('Clusters page by role', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusters.mockResolvedValue(oneCluster);
  });

  it.each(['admin', 'operator'] as const)('%s sees Add Cluster', async (role) => {
    renderAs(role);
    await screen.findByText('spoke-1');
    expect(screen.getByRole('button', { name: /add cluster/i })).toBeInTheDocument();
  });

  it('a viewer sees no Add Cluster and no Test connection button', async () => {
    renderAs('viewer');
    await screen.findByText('spoke-1');
    expect(screen.queryByRole('button', { name: /add cluster/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /test connection/i })).not.toBeInTheDocument();
  });

  it('an operator still sees Test connection', async () => {
    renderAs('operator');
    await screen.findByText('spoke-1');
    expect(screen.getAllByRole('button', { name: /test connection/i }).length).toBeGreaterThan(0);
  });

  it('a refused test (403) says "not allowed" and never "Test failing" or the raw role text', async () => {
    mockTest.mockRejectedValue(
      new ApiError(403, { error: "action 'cluster.test' requires role 'operator', you have 'viewer'" }, 'Forbidden'),
    );
    renderAs('operator');
    await screen.findByText('spoke-1');
    fireEvent.click(screen.getAllByRole('button', { name: /test connection/i })[0]);
    await waitFor(() => expect(screen.getAllByTestId('test-not-run').length).toBeGreaterThan(0));
    expect(screen.getAllByTestId('test-not-run')[0].textContent).toBe(
      'Your role is not allowed to do this. Ask an operator or an admin.',
    );
    expect(screen.queryByText(/Test failing/)).not.toBeInTheDocument();
    expect(screen.queryByText(/requires role/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Unreachable|Error:/)).not.toBeInTheDocument();
  });

  it("a request limit (429) on the test says so and shows no failing cluster", async () => {
    mockTest.mockRejectedValue(new ApiError(429, { error: 'rate limit exceeded', code: 'sharko_rate_limit' }, 'Too Many Requests'));
    renderAs('admin');
    await screen.findByText('spoke-1');
    fireEvent.click(screen.getAllByRole('button', { name: /test connection/i })[0]);
    await waitFor(() => expect(screen.getAllByTestId('test-not-run').length).toBeGreaterThan(0));
    expect(screen.getAllByTestId('test-not-run')[0].textContent).toBe(
      'Sharko is getting too many requests from your address right now. Try again in a moment.',
    );
    expect(screen.queryByText(/Test failing/)).not.toBeInTheDocument();
  });

  it('a real failure of the test is still drawn as a failure', async () => {
    mockTest.mockRejectedValue(new Error('connection refused'));
    renderAs('admin');
    await screen.findByText('spoke-1');
    fireEvent.click(screen.getAllByRole('button', { name: /test connection/i })[0]);
    await waitFor(() => expect(screen.getAllByText(/Error: connection refused/).length).toBeGreaterThan(0));
  });
});
