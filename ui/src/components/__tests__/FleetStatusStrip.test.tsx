import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import {
  FleetStatusStrip,
  summarizeUpgrades,
  type UpgradesSummary,
} from '@/components/FleetStatusStrip';
import type { DashboardStats, VersionMatrixResponse } from '@/services/models';

// Walk day 3 lock — the hand-made SVG donuts became real recharts labeled
// pies (same library/style as Observability's HealthDistributionSection /
// SyncDistributionSection), at roughly half the height, one row. Each pie
// now carries a VISIBLE legend beside it (state name + count), not just a
// tooltip — recharts internals (Pie/Cell) are mocked below the same way
// Observability.test.tsx and Dashboard.test.tsx already do, since jsdom
// can't lay out a ResponsiveContainer; the legend list itself is plain DOM
// outside recharts, so it renders and is assertable regardless of the mock.
vi.mock('recharts', () => {
  const C = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>;
  return {
    ResponsiveContainer: C,
    PieChart: C,
    Pie: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    Cell: () => null,
  };
});

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => mockNavigate };
});

function renderStrip(props: {
  clusters: DashboardStats['clusters'];
  appsTotal: number;
  appsHealthy: number;
  upgrades: UpgradesSummary;
}) {
  return render(
    <MemoryRouter>
      <FleetStatusStrip {...props} />
    </MemoryRouter>,
  );
}

const noUpgradeData: UpgradesSummary = { withUpgrade: 0, checked: 0, namesWithUpgrade: [] };

describe('FleetStatusStrip — labeled pie segments (walk day 3)', () => {
  it('renders the pie + a visible legend row (label + count) per non-zero cluster state', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    // Pie + legend wrapper is present.
    expect(within(segment).getByTestId('fleet-strip-pie')).toBeInTheDocument();

    // Legend text is visible (not tooltip-only) — label + count per state.
    // Scoped to the legend list itself (not the whole segment) because the
    // NumberWord total can share the same digits as a legend count.
    const legend = within(segment).getByTestId('fleet-strip-legend');
    expect(within(legend).getByText('connected')).toBeInTheDocument();
    expect(within(legend).getByText('8')).toBeInTheDocument();
    expect(within(legend).getByText('not connected')).toBeInTheDocument();
    expect(within(legend).getByText('disconnected')).toBeInTheDocument();
    // Zero-count states get no legend row.
    expect(within(legend).queryByText('connecting')).not.toBeInTheDocument();
    expect(within(legend).queryByText('waiting for first addon')).not.toBeInTheDocument();
  });

  it('shows only the one legend row when every cluster is in a single state', () => {
    renderStrip({
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    const legend = within(segment).getByTestId('fleet-strip-legend');
    expect(within(legend).getByText('connected')).toBeInTheDocument();
    expect(within(legend).getByText('5')).toBeInTheDocument();
    expect(within(legend).queryByText('not connected')).not.toBeInTheDocument();
    expect(within(legend).queryByText('disconnected')).not.toBeInTheDocument();
  });

  it('renders the total as a plain number plus one short word next to the pie', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 50,
      appsHealthy: 45,
      upgrades: noUpgradeData,
    });

    const clustersSegment = screen.getByTestId('fleet-strip-clusters');
    expect(clustersSegment.textContent).toContain('10');
    expect(clustersSegment.textContent).toContain('clusters');

    const appsSegment = screen.getByTestId('fleet-strip-applications');
    expect(appsSegment.textContent).toContain('50');
    expect(appsSegment.textContent).toContain('apps');
  });

  it('pluralizes the word correctly: "1 cluster"/"1 app" singular, "2 clusters"/"2 apps" plural (same rule as v1)', () => {
    renderStrip({
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 1,
      appsHealthy: 1,
      upgrades: noUpgradeData,
    });

    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('1 cluster');
    expect(screen.getByTestId('fleet-strip-clusters').textContent).not.toContain('1 clusters');
    expect(screen.getByTestId('fleet-strip-applications').textContent).toContain('1 app');
    expect(screen.getByTestId('fleet-strip-applications').textContent).not.toContain('1 apps');
  });

  it('pluralizes for counts other than 1, including zero', () => {
    renderStrip({
      clusters: { total: 2, connected: 2, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: noUpgradeData,
    });

    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('2 clusters');
    expect(screen.getByTestId('fleet-strip-applications').textContent).toContain('0 apps');
  });

  it('carries the full breakdown (label + count per non-zero state) on the aria-label — identity is never color-alone', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 1, untested: 0, missing: 0, failed: 1 },
      appsTotal: 50,
      appsHealthy: 45,
      upgrades: noUpgradeData,
    });

    const clustersLabel = screen.getByTestId('fleet-strip-clusters').getAttribute('aria-label');
    expect(clustersLabel).toContain('8 connected');
    expect(clustersLabel).toContain('1 connecting');
    expect(clustersLabel).toContain('1 disconnected');
    // Zero-count states are not named.
    expect(clustersLabel).not.toContain('not connected');
    expect(clustersLabel).not.toMatch(/waiting for first addon/);

    const appsLabel = screen.getByTestId('fleet-strip-applications').getAttribute('aria-label');
    expect(appsLabel).toContain('45 healthy');
    expect(appsLabel).toContain('5 not healthy');
  });

  it('applications pie: legend shows healthy/not-healthy rows from total vs. healthy', () => {
    renderStrip({
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 20,
      appsHealthy: 20,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-applications');
    const legend = within(segment).getByTestId('fleet-strip-legend');
    expect(within(legend).getByText('healthy')).toBeInTheDocument();
    expect(within(legend).getByText('20')).toBeInTheDocument();
    expect(within(legend).queryByText('not healthy')).not.toBeInTheDocument();
  });

  it('carries the exact "waiting" palette class into the shared DistributionPie legend (#685 — no new colors introduced)', () => {
    renderStrip({
      clusters: { total: 3, connected: 1, pending: 0, untested: 2, missing: 0, failed: 0 },
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    const legend = within(segment).getByTestId('fleet-strip-legend');
    const waitingLabel = within(legend).getByText('waiting for first addon');
    expect(waitingLabel).toBeInTheDocument();

    // The color dot next to the label still carries the exact muted
    // blue-violet light+dark class pair that passed the color-blindness
    // validator — the shared component must not invent a new color.
    const dot = waitingLabel.previousSibling as HTMLElement;
    expect(dot.className).toContain('text-[#7a3f9e]');
    expect(dot.className).toContain('dark:text-[#7e4fb0]');

    // Legend text is at least text-sm (visibility guardrail).
    expect(waitingLabel.closest('li')?.className).toContain('text-sm');
  });

  it('applications pie: shows both healthy and not-healthy legend rows with their own counts when mixed', () => {
    renderStrip({
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 20,
      appsHealthy: 15,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-applications');
    const legend = within(segment).getByTestId('fleet-strip-legend');
    expect(within(legend).getByText('healthy')).toBeInTheDocument();
    expect(within(legend).getByText('15')).toBeInTheDocument();
    expect(within(legend).getByText('not healthy')).toBeInTheDocument();
    expect(within(legend).getByText('5')).toBeInTheDocument();
  });
});

describe('FleetStatusStrip — Upgrades segment is a number, not a chart (unchanged)', () => {
  const baseClusters: DashboardStats['clusters'] = {
    total: 1,
    connected: 1,
    pending: 0,
    untested: 0,
    missing: 0,
    failed: 0,
  };

  it('shows "N outdated" and never renders an addon name, even when the summary carries names', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: { withUpgrade: 3, checked: 5, namesWithUpgrade: ['cert-manager', 'metrics-server', 'external-dns'] },
    });

    const segment = screen.getByTestId('fleet-strip-upgrades');
    expect(segment.textContent).toContain('3 outdated');
    expect(segment.textContent).not.toContain('cert-manager');
    expect(segment.textContent).not.toContain('metrics-server');
    expect(segment.textContent).not.toContain('and');
    // No pie in this segment.
    expect(within(segment).queryByTestId('fleet-strip-pie')).not.toBeInTheDocument();
  });

  it('shows "up to date" when checked but nothing is outdated', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: { withUpgrade: 0, checked: 4, namesWithUpgrade: [] },
    });

    expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('up to date');
  });

  it('shows "no version data" when nothing has been checked', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: noUpgradeData,
    });

    expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('no version data');
  });

  it('clicking the Upgrades segment navigates to the matrix pre-filtered to outdated rows', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: { withUpgrade: 2, checked: 2, namesWithUpgrade: ['cert-manager'] },
    });

    fireEvent.click(screen.getByTestId('fleet-strip-upgrades'));
    expect(mockNavigate).toHaveBeenCalledWith('/version-matrix?view=matrix&filter=outdated');
  });
});

describe('FleetStatusStrip — navigation doors are preserved (do not regress)', () => {
  const clusters: DashboardStats['clusters'] = {
    total: 3,
    connected: 3,
    pending: 0,
    untested: 0,
    missing: 0,
    failed: 0,
  };

  it('clicking Clusters navigates to /clusters', () => {
    renderStrip({ clusters, appsTotal: 5, appsHealthy: 5, upgrades: noUpgradeData });
    fireEvent.click(screen.getByTestId('fleet-strip-clusters'));
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });

  it('clicking Applications navigates to /observability#addon-health', () => {
    renderStrip({ clusters, appsTotal: 5, appsHealthy: 5, upgrades: noUpgradeData });
    fireEvent.click(screen.getByTestId('fleet-strip-applications'));
    expect(mockNavigate).toHaveBeenCalledWith('/observability#addon-health');
  });
});

describe('summarizeUpgrades (unchanged)', () => {
  it('counts deployments behind the row\'s newest_available', () => {
    const matrix: VersionMatrixResponse = {
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'cert-manager',
          catalog_version: '1.12.0',
          chart: 'cert-manager',
          newest_available: '1.14.0',
          cells: { 'prod-eu': { version: '1.12.0', health: 'Healthy', drift_from_catalog: false } },
        },
      ],
    };
    const result = summarizeUpgrades(matrix);
    expect(result.withUpgrade).toBe(1);
    expect(result.checked).toBe(1);
    expect(result.namesWithUpgrade).toEqual(['cert-manager']);
  });

  it('returns zeros for null matrix', () => {
    expect(summarizeUpgrades(null)).toEqual({ withUpgrade: 0, checked: 0, namesWithUpgrade: [] });
  });
});
