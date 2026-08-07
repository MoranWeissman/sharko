import { describe, it, expect, vi } from 'vitest';
import { render, screen, within, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
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

  it('does not render a total row by default (S4 — opt-in only)', () => {
    render(<DistributionPie slices={colorClassSlices} ariaPrefix="Clusters" legendTestId="legend" />);

    expect(screen.queryByText('total')).not.toBeInTheDocument();
  });

  it('showTotalRow appends one more legend row: a neutral gray dot, "total", and the raw total (S4)', () => {
    render(
      <DistributionPie
        slices={colorClassSlices}
        ariaPrefix="Clusters"
        legendTestId="legend"
        showTotalRow
      />,
    );

    const legend = screen.getByTestId('legend');
    const totalRow = within(legend).getByText('total').closest('li');
    expect(totalRow).not.toBeNull();
    // Raw total (8 + 1 + 0), not just the non-zero slices' sum re-derived —
    // same number either way here, but the row must read the actual total.
    expect(within(totalRow as HTMLElement).getByText('9')).toBeInTheDocument();

    const dot = totalRow?.querySelector('span[aria-hidden="true"]');
    expect(dot?.className).toContain('bg-muted-foreground');
    // Never one of the per-slice status colors.
    expect(dot?.className).not.toMatch(/text-\[#/);
  });

  // S5 (scale-walk) — opt-in per-row link capability.
  describe('per-row links (S5)', () => {
    const linkableSlices: DistributionSlice[] = [
      { key: 'connected', label: 'connected', value: 8, colorClass: 'text-[#16a34a]', href: '/clusters?status=connected' },
      { key: 'connecting', label: 'connecting', value: 2, colorClass: 'text-[#3b82f6]' },
    ];

    it('renders a real, keyboard-reachable link for a slice with an href, and a plain row for one without', () => {
      render(
        <MemoryRouter>
          <DistributionPie slices={linkableSlices} ariaPrefix="Clusters" legendTestId="legend" />
        </MemoryRouter>,
      );

      const legend = screen.getByTestId('legend');
      const connectedLink = within(legend).getByRole('link', { name: /connected\s*8/ });
      expect(connectedLink).toHaveAttribute('href', '/clusters?status=connected');

      // "connecting" has no href — no link role for it, plain text only.
      expect(within(legend).getByText('connecting')).toBeInTheDocument();
      expect(within(legend).queryByRole('link', { name: /connecting/ })).not.toBeInTheDocument();
    });

    it('a row link does not double-fire a wrapping click handler (stopPropagation)', () => {
      const outerClick = vi.fn();
      render(
        <MemoryRouter>
          <div onClick={outerClick}>
            <DistributionPie slices={linkableSlices} ariaPrefix="Clusters" legendTestId="legend" />
          </div>
        </MemoryRouter>,
      );

      const legend = screen.getByTestId('legend');
      const connectedLink = within(legend).getByRole('link', { name: /connected\s*8/ });
      fireEvent.click(connectedLink);

      // The row's own click stopped propagation — the wrapping handler
      // (standing in for FleetStatusStrip's whole-segment click) never fires.
      expect(outerClick).not.toHaveBeenCalled();
    });

    it('showTotalRow + totalHref renders the total row as a link too', () => {
      render(
        <MemoryRouter>
          <DistributionPie slices={linkableSlices} ariaPrefix="Clusters" legendTestId="legend" showTotalRow totalHref="/clusters" />
        </MemoryRouter>,
      );

      const legend = screen.getByTestId('legend');
      const totalLink = within(legend).getByRole('link', { name: /total\s*10/ });
      expect(totalLink).toHaveAttribute('href', '/clusters');
    });

    it('the legend is reachable to screen readers when rows are interactive (legendAriaHidden left unset)', () => {
      render(
        <MemoryRouter>
          <DistributionPie slices={linkableSlices} ariaPrefix="Clusters" legendTestId="legend" />
        </MemoryRouter>,
      );

      expect(screen.getByTestId('legend')).not.toHaveAttribute('aria-hidden');
    });
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
