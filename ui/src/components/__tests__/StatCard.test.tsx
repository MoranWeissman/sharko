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

  it('large variant renders with the hero type scale (text-4xl, tabular-nums)', () => {
    render(<StatCard title="Total Clusters" value={127} size="large" />);
    const valueElement = screen.getByText('127');
    expect(valueElement.className).toContain('text-4xl');
    expect(valueElement.className).toContain('font-bold');
    expect(valueElement.className).toContain('tabular-nums');
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
