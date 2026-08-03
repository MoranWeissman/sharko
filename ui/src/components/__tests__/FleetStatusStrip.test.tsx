import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import {
  FleetStatusStrip,
  summarizeBehindCatalog,
  type BehindCatalogSummary,
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
  behindCatalog: BehindCatalogSummary;
}) {
  return render(
    <MemoryRouter>
      <FleetStatusStrip {...props} />
    </MemoryRouter>,
  );
}

const noBehindCatalogData: BehindCatalogSummary = { behindCount: 0 };

describe('FleetStatusStrip — labeled pie segments (walk day 3)', () => {
  it('renders the pie + a visible legend row (label + count) per non-zero cluster state', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: noBehindCatalogData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    // Pie + legend wrapper is present.
    expect(within(segment).getByTestId('fleet-strip-pie')).toBeInTheDocument();

    // Legend text is visible (not tooltip-only) — label + count per state.
    // Scoped to the legend list itself (not the whole segment) because the
    // total row (S4) can share the same digits as a per-state legend count.
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
      behindCatalog: noBehindCatalogData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    const legend = within(segment).getByTestId('fleet-strip-legend');
    const connectedRow = within(legend).getByText('connected').closest('li');
    expect(connectedRow).not.toBeNull();
    // Scoped to the connected row itself — the total row (S4) also reads
    // "5" here since every cluster is connected, so an unscoped getByText
    // would match both.
    expect(within(connectedRow as HTMLElement).getByText('5')).toBeInTheDocument();
    expect(within(legend).queryByText('not connected')).not.toBeInTheDocument();
    expect(within(legend).queryByText('disconnected')).not.toBeInTheDocument();
  });

  it('renders the total as another legend row — "total" + the count, not a detached bold number (S4)', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 50,
      appsHealthy: 45,
      behindCatalog: noBehindCatalogData,
    });

    const clustersLegend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
    expect(within(clustersLegend).getByText('total')).toBeInTheDocument();
    // '10' also appears as the clusters total legend row; scope to the row
    // itself so it can't accidentally match a per-state count instead.
    const clustersTotalRow = within(clustersLegend).getByText('total').closest('li');
    expect(clustersTotalRow).not.toBeNull();
    expect(within(clustersTotalRow as HTMLElement).getByText('10')).toBeInTheDocument();

    const appsLegend = within(screen.getByTestId('fleet-strip-applications')).getByTestId('fleet-strip-legend');
    const appsTotalRow = within(appsLegend).getByText('total').closest('li');
    expect(appsTotalRow).not.toBeNull();
    expect(within(appsTotalRow as HTMLElement).getByText('50')).toBeInTheDocument();
  });

  it('the total row stays lowercase and carries no plural noun (S4 — "total", never "clusters"/"apps")', () => {
    renderStrip({
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 1,
      appsHealthy: 1,
      behindCatalog: noBehindCatalogData,
    });

    const clustersLegend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
    expect(within(clustersLegend).getByText('total')).toBeInTheDocument();
    expect(within(clustersLegend).queryByText(/cluster/)).not.toBeInTheDocument();

    const appsLegend = within(screen.getByTestId('fleet-strip-applications')).getByTestId('fleet-strip-legend');
    expect(within(appsLegend).getByText('total')).toBeInTheDocument();
    expect(within(appsLegend).queryByText(/^\d+ app/)).not.toBeInTheDocument();
  });

  it('the total row\'s dot is a neutral gray token, not one of the status colors (S4)', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: noBehindCatalogData,
    });

    const legend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
    const totalRow = within(legend).getByText('total').closest('li');
    const dot = totalRow?.querySelector('span[aria-hidden="true"]');
    expect(dot).not.toBeNull();
    expect(dot?.className).toContain('bg-muted-foreground');
    // Never one of the status hex colors the other rows use.
    expect(dot?.className).not.toMatch(/text-\[#/);
  });

  it('carries the full breakdown (label + count per non-zero state) on the aria-label — identity is never color-alone', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 1, untested: 0, missing: 0, failed: 1 },
      appsTotal: 50,
      appsHealthy: 45,
      behindCatalog: noBehindCatalogData,
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
      behindCatalog: noBehindCatalogData,
    });

    const segment = screen.getByTestId('fleet-strip-applications');
    const legend = within(segment).getByTestId('fleet-strip-legend');
    const healthyRow = within(legend).getByText('healthy').closest('li');
    expect(healthyRow).not.toBeNull();
    // Scoped to the healthy row itself — the total row (S4) also reads
    // "20" here since every app is healthy, so an unscoped getByText would
    // match both.
    expect(within(healthyRow as HTMLElement).getByText('20')).toBeInTheDocument();
    expect(within(legend).queryByText('not healthy')).not.toBeInTheDocument();
  });

  it('carries the exact "waiting" palette class into the shared DistributionPie legend (#685 — no new colors introduced)', () => {
    renderStrip({
      clusters: { total: 3, connected: 1, pending: 0, untested: 2, missing: 0, failed: 0 },
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: noBehindCatalogData,
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
      behindCatalog: noBehindCatalogData,
    });

    const segment = screen.getByTestId('fleet-strip-applications');
    const legend = within(segment).getByTestId('fleet-strip-legend');
    expect(within(legend).getByText('healthy')).toBeInTheDocument();
    expect(within(legend).getByText('15')).toBeInTheDocument();
    expect(within(legend).getByText('not healthy')).toBeInTheDocument();
    expect(within(legend).getByText('5')).toBeInTheDocument();
  });
});

describe('FleetStatusStrip — behind-catalog segment is a number, not a chart (walk day 5)', () => {
  const baseClusters: DashboardStats['clusters'] = {
    total: 1,
    connected: 1,
    pending: 0,
    untested: 0,
    missing: 0,
    failed: 0,
  };

  it('shows "N behind" and never renders an addon name', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: { behindCount: 3 },
    });

    const segment = screen.getByTestId('fleet-strip-upgrades');
    expect(segment.textContent).toContain('3 behind');
    expect(segment.textContent).not.toContain('cert-manager');
    // No pie in this segment.
    expect(within(segment).queryByTestId('fleet-strip-pie')).not.toBeInTheDocument();
  });

  it('shows a calm "all on version" message at zero, not a bare 0', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: noBehindCatalogData,
    });

    expect(screen.getByTestId('fleet-strip-upgrades').textContent).toContain('all on version');
  });

  it('carries the fuller sentence on the hover title, not just the short strip text', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: { behindCount: 2 },
    });

    const segment = screen.getByTestId('fleet-strip-upgrades');
    expect(within(segment).getByText('2 behind')).toHaveAttribute(
      'title',
      "2 applications behind their addon's version.",
    );
  });

  it('never says "drift" or "outdated" — plain "behind" wording only', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: { behindCount: 1 },
    });

    const segment = screen.getByTestId('fleet-strip-upgrades');
    expect(segment.textContent?.toLowerCase()).not.toContain('drift');
    expect(segment.textContent?.toLowerCase()).not.toContain('outdated');
  });

  it('clicking the segment navigates to the matrix pre-filtered to behind-catalog rows', () => {
    renderStrip({
      clusters: baseClusters,
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: { behindCount: 2 },
    });

    fireEvent.click(screen.getByTestId('fleet-strip-upgrades'));
    expect(mockNavigate).toHaveBeenCalledWith('/version-matrix?view=matrix&filter=behind-catalog');
  });
});

describe('FleetStatusStrip — honest segment titles (walk day 5 finding)', () => {
  const clusters: DashboardStats['clusters'] = {
    total: 3,
    connected: 3,
    pending: 0,
    untested: 0,
    missing: 0,
    failed: 0,
  };

  it('names the clusters segment "Managed clusters"', () => {
    renderStrip({ clusters, appsTotal: 5, appsHealthy: 5, behindCatalog: noBehindCatalogData });
    expect(screen.getByTestId('fleet-strip-clusters-title').textContent).toBe('Managed clusters');
  });

  it('names the applications segment as an addon concern, not a bare "Applications"', () => {
    renderStrip({ clusters, appsTotal: 5, appsHealthy: 5, behindCatalog: noBehindCatalogData });
    expect(screen.getByTestId('fleet-strip-applications-title').textContent).toContain('Addon');
  });

  it('names the behind-catalog segment as an addon concern too', () => {
    renderStrip({ clusters, appsTotal: 5, appsHealthy: 5, behindCatalog: noBehindCatalogData });
    expect(screen.getByTestId('fleet-strip-upgrades-title').textContent).toContain('Addon');
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
    renderStrip({ clusters, appsTotal: 5, appsHealthy: 5, behindCatalog: noBehindCatalogData });
    fireEvent.click(screen.getByTestId('fleet-strip-clusters'));
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });

  it('clicking Applications navigates to /observability#addon-health', () => {
    renderStrip({ clusters, appsTotal: 5, appsHealthy: 5, behindCatalog: noBehindCatalogData });
    fireEvent.click(screen.getByTestId('fleet-strip-applications'));
    expect(mockNavigate).toHaveBeenCalledWith('/observability#addon-health');
  });
});

describe('FleetStatusStrip — legend rows click through to filtered views (S5, scale-walk)', () => {
  it('clusters pie: connected/not-connected/disconnected/total rows link to the right ?status= views', () => {
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: noBehindCatalogData,
    });

    const legend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
    expect(within(legend).getByRole('link', { name: /connected\s*8/ })).toHaveAttribute(
      'href',
      '/clusters?status=connected',
    );
    expect(within(legend).getByRole('link', { name: /not connected\s*1/ })).toHaveAttribute(
      'href',
      '/clusters?status=disconnected',
    );
    expect(within(legend).getByRole('link', { name: /^disconnected\s*1/ })).toHaveAttribute(
      'href',
      '/clusters?status=disconnected',
    );
    expect(within(legend).getByRole('link', { name: /total\s*10/ })).toHaveAttribute('href', '/clusters');
  });

  it('applications pie: "not healthy" links with ?health=issues, "healthy" and total link plain', () => {
    renderStrip({
      clusters: { total: 1, connected: 1, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 20,
      appsHealthy: 15,
      behindCatalog: noBehindCatalogData,
    });

    const legend = within(screen.getByTestId('fleet-strip-applications')).getByTestId('fleet-strip-legend');
    expect(within(legend).getByRole('link', { name: /healthy\s*15/ })).toHaveAttribute(
      'href',
      '/observability#addon-health',
    );
    expect(within(legend).getByRole('link', { name: /not healthy\s*5/ })).toHaveAttribute(
      'href',
      '/observability?health=issues#addon-health',
    );
    expect(within(legend).getByRole('link', { name: /total\s*20/ })).toHaveAttribute(
      'href',
      '/observability#addon-health',
    );
  });

  it('clicking a legend row link does not also fire the whole-segment click (no double navigation)', () => {
    // This file has no global beforeEach mock reset — other tests' navigate
    // calls accumulate on the same module-level mock. Clear it so this
    // test's "not called" assertion reflects only its own click.
    mockNavigate.mockClear();
    renderStrip({
      clusters: { total: 10, connected: 8, pending: 0, untested: 0, missing: 1, failed: 1 },
      appsTotal: 0,
      appsHealthy: 0,
      behindCatalog: noBehindCatalogData,
    });

    const legend = within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend');
    fireEvent.click(within(legend).getByRole('link', { name: /connected\s*8/ }));

    // The segment's own onClick (navigate('/clusters')) must not ALSO have
    // fired — only one navigation, and it's the row's own <Link>, not a
    // duplicate imperative call from the wrapping segment.
    expect(mockNavigate).not.toHaveBeenCalled();
  });

  it('the segment itself stays keyboard-activatable (Enter) even though it is no longer a native <button>', () => {
    renderStrip({
      clusters: { total: 3, connected: 3, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 5,
      appsHealthy: 5,
      behindCatalog: noBehindCatalogData,
    });

    const segment = screen.getByTestId('fleet-strip-clusters');
    expect(segment).toHaveAttribute('role', 'button');
    expect(segment).toHaveAttribute('tabIndex', '0');
    fireEvent.keyDown(segment, { key: 'Enter' });
    expect(mockNavigate).toHaveBeenCalledWith('/clusters');
  });

  it('legend rows are reachable to screen readers (legendAriaHidden no longer set on the interactive pies)', () => {
    renderStrip({
      clusters: { total: 3, connected: 3, pending: 0, untested: 0, missing: 0, failed: 0 },
      appsTotal: 5,
      appsHealthy: 5,
      behindCatalog: noBehindCatalogData,
    });

    expect(within(screen.getByTestId('fleet-strip-clusters')).getByTestId('fleet-strip-legend')).not.toHaveAttribute(
      'aria-hidden',
    );
    expect(
      within(screen.getByTestId('fleet-strip-applications')).getByTestId('fleet-strip-legend'),
    ).not.toHaveAttribute('aria-hidden');
  });
});

describe('summarizeBehindCatalog (walk day 5)', () => {
  it('counts cells whose drift_from_catalog is true, ignoring newest_available entirely', () => {
    const matrix: VersionMatrixResponse = {
      clusters: ['prod-eu', 'staging-us'],
      addons: [
        {
          addon_name: 'cert-manager',
          catalog_version: '1.12.0',
          chart: 'cert-manager',
          // newest_available says there's something newer upstream, but
          // that's no longer what this stat counts — drift_from_catalog
          // is false here, so this cell must NOT be counted.
          newest_available: '1.14.0',
          cells: { 'prod-eu': { version: '1.12.0', health: 'Healthy', drift_from_catalog: false } },
        },
        {
          addon_name: 'metrics-server',
          catalog_version: '0.7.0',
          chart: 'metrics-server',
          cells: {
            'prod-eu': { version: '0.6.0', health: 'Healthy', drift_from_catalog: true },
            'staging-us': { version: '0.7.0', health: 'Healthy', drift_from_catalog: false },
          },
        },
      ],
    };
    expect(summarizeBehindCatalog(matrix)).toEqual({ behindCount: 1 });
  });

  it('returns zero for a null matrix', () => {
    expect(summarizeBehindCatalog(null)).toEqual({ behindCount: 0 });
  });

  it('returns zero when no cell has drifted', () => {
    const matrix: VersionMatrixResponse = {
      clusters: ['prod-eu'],
      addons: [
        {
          addon_name: 'cert-manager',
          catalog_version: '1.12.0',
          chart: 'cert-manager',
          cells: { 'prod-eu': { version: '1.12.0', health: 'Healthy', drift_from_catalog: false } },
        },
      ],
    };
    expect(summarizeBehindCatalog(matrix)).toEqual({ behindCount: 0 });
  });
});
