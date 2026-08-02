import { describe, it, expect, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { DistributionPie, breakdownSentence, DISTRIBUTION_PIE_SIZE, type DistributionSlice } from '@/components/DistributionPie';

// The shared middle-size distribution pie (maintainer's lock, #685) — one
// component used by both FleetStatusStrip (~92px before, now the shared
// 160px) and Observability's HealthDistributionSection / SyncDistributionSection
// (~240px before, now the shared 160px). jsdom can't lay out a
// ResponsiveContainer, so recharts internals are mocked the same way
// FleetStatusStrip.test.tsx and Observability.test.tsx already do — the
// legend itself is plain DOM outside recharts, so it renders and is
// assertable regardless of the mock.
vi.mock('recharts', () => {
  const C = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>;
  return {
    ResponsiveContainer: C,
    PieChart: C,
    Pie: ({ children }: { children?: React.ReactNode }) => <div>{children}</div>,
    Cell: () => null,
  };
});

const colorClassSlices: DistributionSlice[] = [
  { key: 'a', label: 'connected', value: 8, colorClass: 'text-[#16a34a] dark:text-[#16a34a]' },
  { key: 'b', label: 'not connected', value: 1, colorClass: 'text-[#f59e0b] dark:text-[#d97706]' },
  { key: 'c', label: 'disconnected', value: 0, colorClass: 'text-[#b91c1c] dark:text-[#b91c1c]' },
];

const hexColorSlices: DistributionSlice[] = [
  { key: 'Healthy', label: 'Healthy', value: 100, color: '#22c55e' },
  { key: 'Degraded', label: 'Degraded', value: 10, color: '#ef4444' },
];

describe('DistributionPie', () => {
  it('uses the shared 160px middle size by default', () => {
    expect(DISTRIBUTION_PIE_SIZE).toBe(160);
  });

  it('renders one legend row per non-zero slice, each with its name and count', () => {
    render(<DistributionPie slices={colorClassSlices} ariaPrefix="Clusters" legendTestId="legend" />);

    const legend = screen.getByTestId('legend');
    expect(within(legend).getByText('connected')).toBeInTheDocument();
    expect(within(legend).getByText('8')).toBeInTheDocument();
    expect(within(legend).getByText('not connected')).toBeInTheDocument();
    expect(within(legend).getByText('1')).toBeInTheDocument();
    // Zero-value slices get no legend row.
    expect(within(legend).queryByText('disconnected')).not.toBeInTheDocument();
  });

  it('carries the full breakdown on the pie region aria-label — identity is never color-alone', () => {
    render(<DistributionPie slices={colorClassSlices} ariaPrefix="Clusters" testId="pie" />);

    const pie = screen.getByTestId('pie');
    const region = within(pie).getByRole('img');
    expect(region.getAttribute('aria-label')).toBe('Clusters: 9 total — 8 connected, 1 not connected');
  });

  it('supports the currentColor + Tailwind class technique (FleetStatusStrip palette)', () => {
    render(<DistributionPie slices={colorClassSlices} ariaPrefix="Clusters" legendTestId="legend" />);

    const legend = screen.getByTestId('legend');
    // The dot for "connected" carries the exact light+dark class pair, not a new color.
    const dot = within(legend).getByText('connected').previousSibling as HTMLElement;
    expect(dot.className).toContain('text-[#16a34a]');
    expect(dot.className).toContain('dark:text-[#16a34a]');
    expect(dot.className).toContain('bg-current');
  });

  it('supports a raw hex color for call sites without a Tailwind class pair (Observability palette)', () => {
    render(<DistributionPie slices={hexColorSlices} ariaPrefix="Application health" legendTestId="legend" />);

    const legend = screen.getByTestId('legend');
    expect(within(legend).getByText('Healthy')).toBeInTheDocument();
    expect(within(legend).getByText('100')).toBeInTheDocument();
    const dot = within(legend).getByText('Healthy').previousSibling as HTMLElement;
    expect(dot.style.backgroundColor).toBe('rgb(34, 197, 94)'); // #22c55e
  });

  it('renders an empty ring and an empty legend when every slice is zero', () => {
    render(
      <DistributionPie
        slices={[{ key: 'a', label: 'connected', value: 0 }]}
        ariaPrefix="Clusters"
        testId="pie"
        legendTestId="legend"
      />,
    );

    expect(screen.getByTestId('legend').children).toHaveLength(0);
    expect(screen.getByTestId('pie').textContent).toBe('');
  });
});

describe('breakdownSentence', () => {
  it('names every non-zero slice with its count', () => {
    expect(breakdownSentence('Clusters', 9, colorClassSlices)).toBe(
      'Clusters: 9 total — 8 connected, 1 not connected',
    );
  });

  it('falls back to a bare total when nothing is non-zero', () => {
    expect(breakdownSentence('Clusters', 0, [{ key: 'a', label: 'connected', value: 0 }])).toBe(
      'Clusters: 0 total',
    );
  });
});
