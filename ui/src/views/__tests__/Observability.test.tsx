import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Observability } from '@/views/Observability';

vi.mock('recharts', () => {
  const C = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>;
  return {
    ResponsiveContainer: C,
    BarChart: C,
    Bar: () => null,
    XAxis: () => null,
    YAxis: () => null,
    Tooltip: () => null,
    CartesianGrid: () => null,
    Cell: () => null,
  };
});

vi.mock('@/services/api', () => ({
  api: {
    getObservability: vi.fn().mockResolvedValue({
      control_plane: {
        argocd_version: 'v3.2.2',
        helm_version: 'v3.14.0',
        kubectl_version: 'v1.29.0',
        total_apps: 120,
        total_clusters: 15,
        connected_clusters: 13,
        health_summary: { Healthy: 100, Degraded: 10, Progressing: 5, Unknown: 5 },
      },
      recent_syncs: [
        {
          timestamp: new Date(Date.now() - 3600000).toISOString(),
          duration: '1.2s',
          duration_secs: 1.2,
          app_name: 'istio-prod-cluster1',
          addon_name: 'istio',
          cluster_name: 'prod-cluster1',
          revision: 'abc123',
          status: 'Succeeded',
        },
        {
          timestamp: new Date(Date.now() - 7200000).toISOString(),
          duration: '3.5s',
          duration_secs: 3.5,
          app_name: 'prometheus-staging',
          addon_name: 'prometheus',
          cluster_name: 'staging',
          status: 'Failed',
        },
      ],
      addon_health: [
        {
          addon_name: 'istio',
          total_clusters: 10,
          healthy_clusters: 8,
          degraded_clusters: 2,
          last_deploy_time: new Date(Date.now() - 7200000).toISOString(),
          avg_sync_duration: '1.5s',
          avg_sync_secs: 1.5,
          clusters: [
            {
              cluster_name: 'prod-cluster1',
              health: 'Healthy',
              health_since: new Date(Date.now() - 86400000).toISOString(),
              reconciled_at: new Date(Date.now() - 600000).toISOString(),
              resource_count: 20,
              healthy_resources: 20,
            },
          ],
        },
      ],
      addon_groups: [
        {
          addon_name: 'istio',
          total_apps: 10,
          health_counts: { Healthy: 8, Degraded: 2 },
          child_apps: [
            {
              app_name: 'istio-prod-cluster1',
              cluster_name: 'prod-cluster1',
              health: 'Healthy',
              sync_status: 'Synced',
              reconciled_at: new Date(Date.now() - 600000).toISOString(),
              resource_summary: {
                total_pods: 5,
                running_pods: 5,
                total_containers: 3,
                has_missing_limits: false,
              },
            },
          ],
        },
      ],
      resource_alerts: [
        {
          app_name: '',
          cluster_name: '',
          addon_name: 'prometheus',
          alert_type: 'missing_limits',
          details: 'No resource requests/limits configured in global values',
        },
      ],
    }),
    getDatadogStatus: vi.fn().mockResolvedValue({ enabled: false, site: "" }),
    getClusterMetrics: vi.fn().mockResolvedValue({ cluster_name: "", addons: [] }),
  },
}));

function renderObservability() {
  return render(
    <MemoryRouter>
      <Observability />
    </MemoryRouter>,
  );
}

describe('Observability', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders loading state initially', () => {
    renderObservability();
    expect(screen.getByText('Loading observability data...')).toBeInTheDocument();
  });

  it('renders control plane section after data loads', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Observability')).toBeInTheDocument();
    });

    expect(screen.getByText('Control Plane')).toBeInTheDocument();
    expect(screen.getByText('ArgoCD v3.2.2')).toBeInTheDocument();
    expect(screen.getByText('Helm v3.14.0')).toBeInTheDocument();
    expect(screen.getByText('120')).toBeInTheDocument();
  });

  it('renders sync activity section', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Sync Activity')).toBeInTheDocument();
    });

    expect(screen.getAllByText('istio').length).toBeGreaterThan(0);
    expect(screen.getAllByText('prometheus').length).toBeGreaterThan(0);
  });

  it('renders addon health groups section', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Addon Health')).toBeInTheDocument();
    });

    // The addon group card for 'istio' should be shown with app count
    expect(screen.getByText('10 apps')).toBeInTheDocument();
  });

  it('renders resource alerts section', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Resource Configuration Alerts')).toBeInTheDocument();
    });

    expect(screen.getByText('No resource requests/limits configured in global values')).toBeInTheDocument();
  });

  it('renders error state when API fails', async () => {
    const { api } = await import('@/services/api');
    vi.mocked(api.getObservability).mockRejectedValueOnce(
      new Error('Network error'),
    );

    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Network error')).toBeInTheDocument();
    });
  });
});
