import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import {
  FleetStatusStrip,
  summarizeUpgrades,
  type UpgradesSummary,
} from '@/components/FleetStatusStrip';
import type { DashboardStats, VersionMatrixResponse } from '@/services/models';

// WQ-2 — the maintainer rejected the v1 text-list strip: with all 5
// cluster states non-zero the segment text overflowed, longer labels
// didn't fit, and naming one outdated addon out of many told nobody
// anything useful. v2 replaces per-state text with a compact donut (the
// breakdown moves to the segment's aria-label + the donut's hover
// tooltip) and turns Upgrades into a bare clickable number.

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

describe('FleetStatusStrip — donut segments (WQ-2)', () => {
  it('renders one donut slice per non-zero cluster state', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    expect(within(segment).getByTestId('donut-slice-connected')).toBeInTheDocument();
    expect(within(segment).getByTestId('donut-slice-not-connected')).toBeInTheDocument();
    expect(within(segment).getByTestId('donut-slice-disconnected')).toBeInTheDocument();
    // Zero-count states get no slice.
    expect(within(segment).queryByTestId('donut-slice-connecting')).not.toBeInTheDocument();
    expect(within(segment).queryByTestId('donut-slice-waiting')).not.toBeInTheDocument();
  });

  it('renders a single full-ring slice when every cluster is in one state', () => {
    renderStrip({
      clusters: { total: 5, connected: 5, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 0,
      appsHealthy: 0,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    const slice = within(segment).getByTestId('donut-slice-connected');
    // No gap subtraction for a single slice — its dash length equals the
    // full circumference (dasharray "<circumference> 0").
    const [dash, gap] = (slice.getAttribute('stroke-dasharray') || '').split(' ').map(Number);
    expect(gap).toBeCloseTo(0, 5);
    expect(dash).toBeGreaterThan(0);
  });

  it('renders the total as a plain number plus one short word, not per-state text', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 50,
      appsHealthy: 45,
      upgrades: noUpgradeData,
    });

    const clustersSegment = screen.getByTestId('fleet-strip-clusters');
    expect(clustersSegment.textContent).toContain('10');
    expect(clustersSegment.textContent).toContain('clusters');
    // No per-state phrases in the visible text anymore.
    expect(clustersSegment.textContent).not.toMatch(/connected|disconnected|not connected/);

    const appsSegment = screen.getByTestId('fleet-strip-applications');
    expect(appsSegment.textContent).toContain('50');
    expect(appsSegment.textContent).toContain('apps');
    expect(appsSegment.textContent).not.toMatch(/healthy/);
  });

  it('pluralizes the word correctly: "1 cluster"/"1 app" singular, "2 clusters"/"2 apps" plural (same rule as v1)', () => {
    renderStrip({
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 1,
      appsHealthy: 1,
      upgrades: noUpgradeData,
    });

    expect(screen.getByTestId('fleet-strip-clusters').textContent).toContain('1 cluster');
    // Not the plural form.
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

  it('carries the full breakdown (label + count per non-zero state) on the aria-label', () => {
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

  it('applications donut: renders healthy/not-healthy slices from total vs. healthy', () => {
    renderStrip({
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 20,
      appsHealthy: 20,
      upgrades: noUpgradeData,
    });

    const segment = screen.getByTestId('fleet-strip-applications');
    expect(within(segment).getByTestId('donut-slice-healthy')).toBeInTheDocument();
    expect(within(segment).queryByTestId('donut-slice-not-healthy')).not.toBeInTheDocument();
  });
});

describe('FleetStatusStrip — Upgrades segment is a number, not a chart (WQ-2)', () => {
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
    // No donut in this segment.
    expect(within(segment).queryByTestId('fleet-strip-donut')).not.toBeInTheDocument();
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
