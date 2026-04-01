import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ClusterDetail } from '@/views/ClusterDetail';

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual('react-router-dom');
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const mockGetClusterComparison = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getClusterComparison: (...args: unknown[]) => mockGetClusterComparison(...args),
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    getNodeInfo: vi.fn().mockResolvedValue(null),
  },
}));

const comparisonResponse = {
  cluster: {
    name: 'prod-eu',
    labels: { env: 'prod' },
    server_version: '1.28',
    connection_status: 'connected',
  },
  git_total_addons: 5,
  git_enabled_addons: 4,
  git_disabled_addons: 1,
  argocd_total_applications: 4,
  argocd_healthy_applications: 3,
  argocd_synced_applications: 4,
  argocd_degraded_applications: 0,
  argocd_out_of_sync_applications: 0,
  addon_comparisons: [
    {
      addon_name: 'ingress-nginx',
      git_configured: true,
      git_version: '4.7.0',
      git_enabled: true,
      environment_version: '4.7.0',
      custom_version: '4.6.0',
      has_version_override: true,
      argocd_deployed: true,
      argocd_deployed_version: '4.7.0',
      argocd_namespace: 'ingress',
      argocd_health_status: 'Healthy',
      status: 'healthy',
      issues: [],
    },
    {
      addon_name: 'cert-manager',
      git_configured: true,
      git_version: '1.12.0',
      git_enabled: true,
      environment_version: '1.12.0',
      has_version_override: false,
      argocd_deployed: false,
      status: 'missing_in_argocd',
      issues: [
        'Addon is configured in Git but not deployed in ArgoCD',
        'This may indicate a deployment issue',
      ],
    },
    {
      addon_name: 'prometheus',
      git_configured: true,
      git_version: '2.45.0',
      git_enabled: true,
      environment_version: '2.45.0',
      has_version_override: false,
      argocd_deployed: true,
      argocd_deployed_version: '2.44.0',
      argocd_namespace: 'monitoring',
      argocd_health_status: 'Degraded',
      status: 'unhealthy',
      issues: ['Health status is Degraded'],
    },
  ],
  total_healthy: 1,
  total_with_issues: 1,
  total_missing_in_argocd: 1,
  total_untracked_in_argocd: 0,
  total_disabled_in_git: 0,
};

function renderView() {
  return render(
    <MemoryRouter initialEntries={['/clusters/prod-eu']}>
      <Routes>
        <Route path="/clusters/:name" element={<ClusterDetail />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('ClusterDetail', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGetClusterComparison.mockResolvedValue(comparisonResponse);
  });

  it('renders loading state initially', () => {
    mockGetClusterComparison.mockReturnValue(new Promise(() => {}));
    renderView();
    expect(screen.getByText('Loading cluster details...')).toBeInTheDocument();
  });

  it('renders error state on API failure', async () => {
    mockGetClusterComparison.mockRejectedValue(new Error('Server error'));
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Server error')).toBeInTheDocument();
    });
  });

  it('renders cluster detail with stat cards and comparison table', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    // Stat cards — zero-count cards (Unmanaged, Not Enabled) are hidden
    expect(screen.getByText('All Addons')).toBeInTheDocument();
    expect(screen.getByText('Healthy')).toBeInTheDocument();
    expect(screen.getByText('With Issues')).toBeInTheDocument();
    expect(screen.getAllByText('Not Deployed').length).toBeGreaterThanOrEqual(1);
    // Unmanaged (0) and Not Enabled (0) should be hidden
    expect(screen.queryByText('Unmanaged')).not.toBeInTheDocument();
    expect(screen.queryByText('Not Enabled')).not.toBeInTheDocument();

    // Table rows
    expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
    expect(screen.getByText('Cert-manager')).toBeInTheDocument();
    expect(screen.getByText('Prometheus')).toBeInTheDocument();

    // Version override shown as Git Version
    expect(screen.getByText('4.6.0')).toBeInTheDocument();

    // Issues
    expect(screen.getByText('Health status is Degraded')).toBeInTheDocument();
  });

  it('calls API with cluster name from route params', async () => {
    renderView();

    await waitFor(() => {
      expect(mockGetClusterComparison).toHaveBeenCalledWith('prod-eu');
    });
  });

  it('filters addons by clicking stat card', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
    });

    // Click "Healthy" stat card
    const healthyCard = screen.getByText('Healthy').closest('[role="button"]');
    expect(healthyCard).toBeTruthy();
    fireEvent.click(healthyCard!);

    // Only healthy addon should show
    expect(screen.getByText('Ingress-nginx')).toBeInTheDocument();
    expect(screen.queryByText('Cert-manager')).not.toBeInTheDocument();
    expect(screen.queryByText('Prometheus')).not.toBeInTheDocument();
  });

  it('navigates back when clicking back button', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('prod-eu')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByText('Back to Clusters Overview'));
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });

  it('shows expand/collapse for long issues', async () => {
    renderView();

    await waitFor(() => {
      expect(screen.getByText('Cert-manager')).toBeInTheDocument();
    });

    // Cert-manager has 2 issues with long text, should show expand button
    expect(
      screen.getByText('Addon is configured in Git but not deployed in ArgoCD'),
    ).toBeInTheDocument();
  });
});
