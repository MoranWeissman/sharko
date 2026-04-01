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
    expect(card.className).toContain('ring-cyan-500');
  });
});
