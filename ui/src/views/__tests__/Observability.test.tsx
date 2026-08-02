import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { Observability } from '@/views/Observability';
// WQ-3 (attention-move-badges): Observability now renders the attention
// detail rows via useAddonStates() — has to be mounted inside the provider
// or the hook throws.
import { AddonStatesProvider } from '@/hooks/useAddonStates';

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
    PieChart: C,
    Pie: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    Legend: () => null,
    LineChart: C,
    Line: () => null,
  };
});

vi.mock('@/services/api', () => ({
  api: {
    getDashboardStats: vi.fn().mockResolvedValue({
      total_clusters: 0,
      connected_clusters: 0,
      bootstrap_app_health: 'Healthy',
      bootstrap_app_sync: 'Synced',
      // WQ-3 — clusterProblemCount reads this real wire shape (same one
      // Dashboard.test.tsx's baseStats uses), not a re-derivation from the
      // named /clusters rows.
      clusters: { total: 0, connected: 0, pending: 0, untested: 0, missing: 0, failed: 0 },
    }),
    getConnections: vi.fn().mockResolvedValue({ connections: [], active_connection: '' }),
    // WQ-3 — attention-detail reads, moved here from the Dashboard.
    getClusters: vi.fn().mockResolvedValue({ clusters: [] }),
    getVersionMatrix: vi.fn().mockResolvedValue({ addons: [] }),
    getAttentionItems: vi.fn().mockResolvedValue([]),
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
  },
}));

function renderObservability() {
  return render(
    <MemoryRouter>
      <AddonStatesProvider>
        <Observability />
      </AddonStatesProvider>
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

  it('renders the control plane panel with version chips and engine health after data loads', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Observability')).toBeInTheDocument();
    });

    expect(screen.getByText('ArgoCD Control Plane')).toBeInTheDocument();
    expect(screen.queryByText('Control Plane')).not.toBeInTheDocument();
    expect(screen.getByText('ArgoCD v3.2.2')).toBeInTheDocument();
    expect(screen.getByText('Helm v3.14.0')).toBeInTheDocument();
    expect(screen.getByText('kubectl v1.29.0')).toBeInTheDocument();
    // Engine health/sync folded into the same panel (v4 slim lock). Scoped
    // to the control-plane section itself — the shared DistributionPie
    // legend below now also renders a real (unmocked) "Healthy" row for
    // the health-distribution pie, so a page-wide getByText is ambiguous.
    expect(screen.getByText('Sharko Engine')).toBeInTheDocument();
    const controlPlaneSection = screen.getByText('ArgoCD Control Plane').closest('section')!;
    expect(within(controlPlaneSection).getByText('Healthy')).toBeInTheDocument();
    expect(within(controlPlaneSection).getByText('Synced')).toBeInTheDocument();
  });

  // Maintainer's slim lock: "go with the slim version" — the four v3 tiles
  // mislabeled addon data as control-plane data (Applications/ApplicationSets
  // were addon apps/groups, not real ArgoCD resources) or duplicated the
  // fleet 5-state model (Clusters configured / In ArgoCD). All four die.
  it('does not render the old v3 grab-bag tiles', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('ArgoCD Control Plane')).toBeInTheDocument();
    });

    expect(screen.queryByText('Applications')).not.toBeInTheDocument();
    expect(screen.queryByText('ApplicationSets')).not.toBeInTheDocument();
    expect(screen.queryByText('Clusters configured')).not.toBeInTheDocument();
    expect(screen.queryByText('In ArgoCD')).not.toBeInTheDocument();
    expect(screen.queryByText('Known to ArgoCD')).not.toBeInTheDocument();
    // The panel's own health-summary bar is gone too — the donut chart
    // below (Application Health Distribution) is the only health-summary
    // surface left, and that section is unaffected.
    expect(screen.queryByText('Health Summary')).not.toBeInTheDocument();
  });

  it('renders sync activity section', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Sync Activity')).toBeInTheDocument();
    });

    expect(screen.getAllByText('istio').length).toBeGreaterThan(0);
    expect(screen.getAllByText('prometheus').length).toBeGreaterThan(0);
  });

  it('renders the Health section with addon groups', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Health')).toBeInTheDocument();
    });

    // The addon group card for 'istio' should be shown with app count
    expect(screen.getByText('10 Applications')).toBeInTheDocument();
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

  it('renders health distribution donut chart', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Application Health Distribution')).toBeInTheDocument();
    });

    // Chart should show total applications
    expect(screen.getByText(/Total: 120 applications/)).toBeInTheDocument();
  });

  // #685 (maintainer's middle-size lock) — this pie now goes through the
  // shared DistributionPie, same component FleetStatusStrip uses. The
  // legend is real, always-visible markup (name + count per state), and
  // the pie region itself carries the full breakdown as one aria sentence.
  it('shows a name+count legend row per health state and an aria breakdown on the shared pie', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Application Health Distribution')).toBeInTheDocument();
    });

    const legend = screen.getByTestId('health-distribution-legend');
    expect(within(legend).getByText('Healthy')).toBeInTheDocument();
    expect(within(legend).getByText('100')).toBeInTheDocument();
    expect(within(legend).getByText('Degraded')).toBeInTheDocument();
    expect(within(legend).getByText('10')).toBeInTheDocument();

    const pie = within(screen.getByTestId('health-distribution-pie')).getByRole('img');
    const ariaLabel = pie.getAttribute('aria-label');
    expect(ariaLabel).toContain('100 Healthy');
    expect(ariaLabel).toContain('10 Degraded');
  });

  it('renders sync distribution donut chart', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Application Sync Distribution')).toBeInTheDocument();
    });

    // Chart should show total from addon_groups child_apps (1 in mock data)
    expect(screen.getByText(/Total: 1 application/)).toBeInTheDocument();
  });

  it('shows a name+count legend row per sync state on the shared pie (#685)', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Application Sync Distribution')).toBeInTheDocument();
    });

    const legend = screen.getByTestId('sync-distribution-legend');
    expect(within(legend).getByText('Synced')).toBeInTheDocument();
    expect(within(legend).getByText('1')).toBeInTheDocument();

    const pie = within(screen.getByTestId('sync-distribution-pie')).getByRole('img');
    expect(pie.getAttribute('aria-label')).toContain('1 Synced');
  });

  it('renders deployment frequency chart', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Deployment frequency')).toBeInTheDocument();
    });

    // Chart should be rendered (not the empty state)
    expect(screen.queryByText('No sync activity yet')).not.toBeInTheDocument();
  });

  it('renders sync duration chart', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Sync duration')).toBeInTheDocument();
    });

    // Chart should be rendered (not the empty state)
    expect(screen.queryByText('No sync activity yet')).not.toBeInTheDocument();
  });

  it('renders empty state for deployment frequency when no syncs', async () => {
    const { api } = await import('@/services/api');
    vi.mocked(api.getObservability).mockResolvedValueOnce({
      control_plane: {
        argocd_version: 'v3.2.2',
        helm_version: 'v3.14.0',
        kubectl_version: 'v1.29.0',
        total_apps: 5,
        total_clusters: 2,
        connected_clusters: 2,
        configured_clusters: 2,
        configured_clusters_available: true,
        total_appsets: 1,
        health_summary: { Healthy: 5 },
      },
      recent_syncs: [],
      addon_health: [],
      addon_groups: [],
      resource_alerts: [],
    });

    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Deployment frequency')).toBeInTheDocument();
    });

    expect(screen.getAllByText('No sync activity yet').length).toBeGreaterThan(0);
  });
});

// Walk finding #2: the Dashboard's Applications card deep-links to
// /observability#addon-health so "which apps are unhealthy" lands right on
// the Addon Health section instead of the top of the page.
describe('Observability — #addon-health deep-link (walk finding #2)', () => {
  const originalHash = window.location.hash;

  beforeEach(() => {
    vi.clearAllMocks();
    window.HTMLElement.prototype.scrollIntoView = vi.fn();
  });

  afterEach(() => {
    window.location.hash = originalHash;
  });

  it('renders the Health section with the #addon-health anchor', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Health')).toBeInTheDocument();
    });

    const section = screen.getByText('Health').closest('section');
    expect(section?.id).toBe('addon-health');
  });

  it('scrolls the section into view when the page loads with the #addon-health hash', async () => {
    window.location.hash = '#addon-health';
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Health')).toBeInTheDocument();
    });
    await waitFor(() => {
      expect(window.HTMLElement.prototype.scrollIntoView).toHaveBeenCalled();
    });
  });
});

// Walk-day lock — the maintainer's three findings on the old "Fleet
// Health" section: (1) "fleet" is a banned display word — the section is
// now just "Health"; (2) the chip-click-to-reveal issues panel is gone —
// "Open issues (N)" is a plain one-line count, nothing to click; (3) the
// double-listing is gone — a problem addon shows up ONLY inside its own
// health group (reason inline), never also as a separate flat row.
// Cluster connection problems and version drift still render as compact
// rows above the list — they have no addon group to live inside.
describe('Observability — Health section open issues (walk-day fold)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders a confirmed cluster problem as a compact row with its reason and a deep link, and counts it in "Open issues (N)"', async () => {
    const { api } = await import('@/services/api');
    vi.mocked(api.getClusters).mockResolvedValue({
      clusters: [{ name: 'spoke-us', connection_status: 'Failed' }],
    } as never);
    // clusterProblemCount reads stats.clusters (the server's single
    // classification), not a re-derivation from the /clusters list above —
    // keep the two in sync the way the real backend would.
    vi.mocked(api.getDashboardStats).mockResolvedValue({
      total_clusters: 1,
      connected_clusters: 0,
      bootstrap_app_health: 'Healthy',
      bootstrap_app_sync: 'Synced',
      clusters: { total: 1, connected: 0, pending: 0, untested: 0, missing: 0, failed: 1 },
    } as never);

    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Open issues (1)')).toBeInTheDocument();
    });

    const row = screen.getByText('spoke-us');
    expect(row).toBeInTheDocument();
    expect(row.closest('a')).toHaveAttribute('href', '/clusters/spoke-us');
    expect(screen.getByText(/argocd tried to reach this cluster and failed/i)).toBeInTheDocument();
  });

  it('renders a version-drift row as a compact row above the list', async () => {
    const { api } = await import('@/services/api');
    // Isolate from the previous test's override — mockResolvedValue
    // persists across vi.clearAllMocks() (it only clears call history).
    vi.mocked(api.getClusters).mockResolvedValue({ clusters: [] });
    vi.mocked(api.getDashboardStats).mockResolvedValue({
      total_clusters: 0,
      connected_clusters: 0,
      bootstrap_app_health: 'Healthy',
      bootstrap_app_sync: 'Synced',
      clusters: { total: 0, connected: 0, pending: 0, untested: 0, missing: 0, failed: 0 },
    } as never);
    vi.mocked(api.getVersionMatrix).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          cells: {
            'prod-eu': { version: '1.12.0', drift_from_catalog: true },
            'prod-us': { version: '1.14.0', drift_from_catalog: true },
          },
        },
      ],
    } as never);

    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Open issues (1)')).toBeInTheDocument();
    });

    expect(screen.getByText('cert-manager')).toBeInTheDocument();
    expect(screen.getByText(/different versions deployed across 2 clusters/i)).toBeInTheDocument();
  });

  it('shows the muted "No open issues." line (not nothing) when there is nothing open', async () => {
    const { api } = await import('@/services/api');
    // Isolate from earlier tests' overrides — mockResolvedValue persists
    // across vi.clearAllMocks() (it only clears call history).
    vi.mocked(api.getClusters).mockResolvedValue({ clusters: [] });
    vi.mocked(api.getVersionMatrix).mockResolvedValue({ addons: [] } as never);
    vi.mocked(api.getDashboardStats).mockResolvedValue({
      total_clusters: 0,
      connected_clusters: 0,
      bootstrap_app_health: 'Healthy',
      bootstrap_app_sync: 'Synced',
      clusters: { total: 0, connected: 0, pending: 0, untested: 0, missing: 0, failed: 0 },
    } as never);
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Health')).toBeInTheDocument();
    });
    expect(screen.getByText('No open issues.')).toBeInTheDocument();
    expect(screen.queryByText(/^Open issues/)).not.toBeInTheDocument();
  });

  it('the Health section (id="addon-health") hosts both the open-issues count and the per-addon groups — one surface, not two', async () => {
    const { api } = await import('@/services/api');
    vi.mocked(api.getClusters).mockResolvedValue({
      clusters: [{ name: 'spoke-us', connection_status: 'Failed' }],
    } as never);
    // clusterProblemCount reads stats.clusters (the server's single
    // classification), not a re-derivation from the /clusters list above —
    // keep the two in sync the way the real backend would.
    vi.mocked(api.getDashboardStats).mockResolvedValue({
      total_clusters: 1,
      connected_clusters: 0,
      bootstrap_app_health: 'Healthy',
      bootstrap_app_sync: 'Synced',
      clusters: { total: 1, connected: 0, pending: 0, untested: 0, missing: 0, failed: 1 },
    } as never);

    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Open issues (1)')).toBeInTheDocument();
    });
    const section = screen.getByText('Health').closest('section');
    expect(section?.id).toBe('addon-health');
    expect(section).toContainElement(screen.getByText('Open issues (1)'));
    expect(section).toContainElement(screen.getByText('10 Applications'));
  });

  it('sorts a problem addon group before a healthy one and shows its reason inline on the group header, not as a separate row', async () => {
    const { api } = await import('@/services/api');
    // Isolate from earlier tests' overrides — mockResolvedValue persists
    // across vi.clearAllMocks() (it only clears call history).
    vi.mocked(api.getClusters).mockResolvedValue({ clusters: [] });
    vi.mocked(api.getDashboardStats).mockResolvedValue({
      total_clusters: 0,
      connected_clusters: 0,
      bootstrap_app_health: 'Healthy',
      bootstrap_app_sync: 'Synced',
      clusters: { total: 0, connected: 0, pending: 0, untested: 0, missing: 0, failed: 0 },
    } as never);
    vi.mocked(api.getVersionMatrix).mockResolvedValue({ addons: [] } as never);
    // A freshly-observed problem — the addon-state provider always sees a
    // newly-fetched bad app as "just went bad", so this lands in the grace
    // tier ("new — confirming"), not confirmed. That's enough to prove
    // problem-tier-first sorting: 'zzz-quiet' is alphabetically LAST and
    // its own addon_groups snapshot says Healthy, but the live attention
    // feed says it's degraded, so it must still sort ahead of 'aaa-clean'.
    vi.mocked(api.getAttentionItems).mockResolvedValue([
      {
        app_name: 'zzz-quiet-c1',
        addon_name: 'zzz-quiet',
        cluster: 'c1',
        health: 'Degraded',
        sync: 'OutOfSync',
        error: 'pod not ready',
      },
    ] as never);
    vi.mocked(api.getObservability).mockResolvedValue({
      control_plane: {
        argocd_version: 'v3.2.2',
        helm_version: 'v3.14.0',
        kubectl_version: 'v1.29.0',
        total_apps: 2,
        total_clusters: 1,
        connected_clusters: 1,
        health_summary: { Healthy: 2 },
      },
      recent_syncs: [],
      addon_health: [],
      addon_groups: [
        {
          addon_name: 'aaa-clean',
          total_apps: 1,
          health_counts: { Healthy: 1 },
          child_apps: [
            {
              app_name: 'aaa-clean-c1',
              cluster_name: 'c1',
              health: 'Healthy',
              sync_status: 'Synced',
              resource_summary: { total_pods: 1, running_pods: 1, total_containers: 1 },
            },
          ],
        },
        {
          addon_name: 'zzz-quiet',
          total_apps: 1,
          health_counts: { Healthy: 1 },
          child_apps: [
            {
              app_name: 'zzz-quiet-c1',
              cluster_name: 'c1',
              health: 'Healthy',
              sync_status: 'Synced',
              resource_summary: { total_pods: 1, running_pods: 1, total_containers: 1 },
            },
          ],
        },
      ],
      resource_alerts: [],
    } as never);

    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Health')).toBeInTheDocument();
    });
    // The reason line is composed inline on the group header — plain
    // words, the cluster name, the detail, and the grace-tier vocabulary.
    await waitFor(() => {
      expect(screen.getByText(/degraded on c1 — pod not ready/i)).toBeInTheDocument();
    });
    expect(screen.getByText(/new — confirming/)).toBeInTheDocument();

    // Problem-tier-first: zzz-quiet's card comes before aaa-clean's card
    // even though it's alphabetically last and its own group snapshot says
    // Healthy — the live addon-state map is what decides the order.
    const names = screen.getAllByText(/^(aaa-clean|zzz-quiet)$/).map((el) => el.textContent);
    expect(names.indexOf('zzz-quiet')).toBeLessThan(names.indexOf('aaa-clean'));

    // No double-listing — the reason appears exactly once (on the group),
    // there is no separate flat "zzz-quiet" attention row anywhere else.
    expect(screen.getAllByText(/degraded on c1 — pod not ready/i)).toHaveLength(1);
  });

  it('never renders "settling", "fleet", or "needs attention" anywhere on the page', async () => {
    const { api } = await import('@/services/api');
    vi.mocked(api.getClusters).mockResolvedValue({
      clusters: [{ name: 'spoke-us', connection_status: 'Failed' }],
    } as never);
    vi.mocked(api.getAttentionItems).mockResolvedValue([
      {
        app_name: 'istio-prod-cluster1',
        addon_name: 'istio',
        cluster: 'prod-cluster1',
        health: 'Degraded',
        sync: 'OutOfSync',
        error: 'pod not ready',
      },
    ] as never);
    vi.mocked(api.getVersionMatrix).mockResolvedValue({
      addons: [
        {
          addon_name: 'cert-manager',
          cells: {
            'prod-eu': { version: '1.12.0', drift_from_catalog: true },
            'prod-us': { version: '1.14.0', drift_from_catalog: true },
          },
        },
      ],
    } as never);

    const { container } = renderObservability();

    await waitFor(() => {
      expect(screen.getByText(/^Open issues/)).toBeInTheDocument();
    });

    const text = container.textContent ?? '';
    expect(text).not.toMatch(/settling/i);
    expect(text).not.toMatch(/fleet/i);
    expect(text).not.toMatch(/needs attention/i);
  });
});

// Bootstrap-app surfaces renamed to Sharko Engine (v4 truth) — the tracked
// ArgoCD app IS the engine app; wire field names (bootstrap_app_health
// etc.) are unchanged, only the user-facing copy moved.
//
// v4 slim (maintainer's lock): the old standalone "Sharko Engine" section
// is gone — its health/sync content now lives INSIDE the control-plane
// panel, so the page has one machinery surface, not two. Engine health
// must still show up, but exactly once.
describe('Observability — Sharko Engine surface rename', () => {
  it('renders "Sharko Engine" exactly once, folded into the control-plane panel (not a separate section)', async () => {
    renderObservability();

    await waitFor(() => {
      expect(screen.getByText('Sharko Engine')).toBeInTheDocument();
    });
    expect(screen.queryByText('Bootstrap Application')).not.toBeInTheDocument();

    // One machinery surface: "Sharko Engine" appears once, and it lives
    // inside the same <section> as "ArgoCD Control Plane", not its own.
    expect(screen.getAllByText('Sharko Engine')).toHaveLength(1);
    const engineLabel = screen.getByText('Sharko Engine');
    const controlPlaneHeading = screen.getByText('ArgoCD Control Plane');
    expect(engineLabel.closest('section')).toBe(controlPlaneHeading.closest('section'));
  });
});
