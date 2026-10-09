import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { StatCard } from '@/components/StatCard';

describe('StatCard', () => {
  it('renders title and value', () => {
    render(<StatCard title="Total Apps" value={42} />);
    expect(screen.getByText('Total Apps')).toBeInTheDocument();
    expect(screen.getByText('42')).toBeInTheDocument();
  });

  it('onClick is called when clicked', () => {
    const handleClick = vi.fn();
    render(<StatCard title="Apps" value={10} onClick={handleClick} />);

    const button = screen.getByRole('button');
    fireEvent.click(button);
    expect(handleClick).toHaveBeenCalledTimes(1);
  });

  it('selected state applies ring styling', () => {
    const { container } = render(
      <StatCard title="Apps" value={5} selected onClick={() => {}} />,
    );
    const card = container.firstChild as HTMLElement;
    expect(card.className).toContain('ring-2');
    expect(card.className).toContain('ring-teal-500');
  });

  // Dashboard UX review 2026-08-01 (finding H2 + Package 2 #5): the large
  // variant is now "a labeled row of small stats", title first — not a
  // poster number. Falls back to a single stat built from title/value when
  // no `stats` array is passed.
  it('large variant renders the title first (readable, text-sm font-semibold), value as a labeled stat', () => {
    render(<StatCard title="Total Clusters" value={127} size="large" />);
    // No `stats` passed, so the fallback single stat is labeled with the
    // same text as the title — two "Total Clusters" nodes by design (the
    // heading, and the fallback stat's label). The heading is the first;
    // its text-sm/font-semibold classes live on the wrapping row, not the
    // text node itself (the icon shares that row).
    const [titleElement] = screen.getAllByText('Total Clusters');
    const titleRow = titleElement.parentElement as HTMLElement;
    expect(titleRow.className).toContain('text-sm');
    expect(titleRow.className).toContain('font-semibold');
    const valueElement = screen.getByText('127');
    expect(valueElement.className).toContain('text-lg');
    expect(valueElement.className).toContain('font-semibold');
    expect(valueElement.className).toContain('tabular-nums');
  });

  it('large variant renders multiple labeled stats when `stats` is passed, not one giant numeral', () => {
    render(
      <StatCard
        title="Total Clusters"
        value={10}
        size="large"
        stats={[
          { label: 'Total', value: 10 },
          { label: 'Connected', value: 8 },
          { label: 'Disconnected', value: 1 },
        ]}
      />,
    );
    expect(screen.getByText('Total')).toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('Disconnected')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
    expect(screen.getByText('8')).toBeInTheDocument();
    expect(screen.getByText('1')).toBeInTheDocument();
  });

  it('large variant has no ring — Tier 1 hero cards use bg-card + shadow only', () => {
    const { container } = render(<StatCard title="Total Clusters" value={127} size="large" />);
    const card = container.firstChild as HTMLElement;
    expect(card.className).not.toMatch(/\bring-2\b/);
    expect(card.className).toContain('bg-card');
    expect(card.className).toContain('shadow-sm');
  });

  it('default size unchanged when size not specified', () => {
    render(<StatCard title="Apps" value={42} />);
    const valueElement = screen.getByText('42');
    expect(valueElement.className).toContain('text-2xl');
  });
});
