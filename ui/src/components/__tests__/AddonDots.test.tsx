import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { AddonDots } from '@/components/AddonDots';

describe('AddonDots (LW-2: summarized, fixed-height)', () => {
  it('renders individual dots (with tooltips) for <= 6 addons', () => {
    const addons = [
      { name: 'addon-1', health: 'Healthy' },
      { name: 'addon-2', health: 'Degraded' },
      { name: 'addon-3', health: 'Progressing' },
    ];
    const { container } = render(<AddonDots addons={addons} />);

    // Should render 3 dots
    const dots = container.querySelectorAll('span.inline-block.h-2\\.5.w-2\\.5.rounded-full');
    expect(dots.length).toBe(3);
  });

  it('renders a compact count line for > 6 addons (NOT 30 dots)', () => {
    const addons = Array.from({ length: 30 }, (_, i) => ({
      name: `addon-${i}`,
      health: i < 2 ? 'Degraded' : i < 3 ? 'Progressing' : 'Healthy',
    }));
    const { container } = render(<AddonDots addons={addons} />);

    // Should NOT render 30 dots
    const dots = container.querySelectorAll('span.inline-block.h-2\\.5.w-2\\.5.rounded-full');
    expect(dots.length).toBe(0);

    // Should render a compact count line instead
    expect(screen.getByText(/2 degraded/)).toBeInTheDocument();
    expect(screen.getByText(/1 progressing/)).toBeInTheDocument();
    expect(screen.getByText(/27 healthy/)).toBeInTheDocument();
  });

  it('leads with unhealthy states in the count line', () => {
    const addons = [
      ...Array.from({ length: 20 }, (_, i) => ({ name: `healthy-${i}`, health: 'Healthy' })),
      { name: 'degraded-1', health: 'Degraded' },
      { name: 'progressing-1', health: 'Progressing' },
    ];
    const { container } = render(<AddonDots addons={addons} />);

    const text = container.textContent;
    // "degraded" should appear before "healthy" in the string
    const degradedIdx = text?.indexOf('degraded') ?? -1;
    const healthyIdx = text?.indexOf('healthy') ?? -1;
    expect(degradedIdx).toBeGreaterThan(-1);
    expect(healthyIdx).toBeGreaterThan(-1);
    expect(degradedIdx).toBeLessThan(healthyIdx);
  });

  it('sorts dots unhealthy-first when rendering <= 6', () => {
    const addons = [
      { name: 'healthy-1', health: 'Healthy' },
      { name: 'degraded-1', health: 'Degraded' },
      { name: 'progressing-1', health: 'Progressing' },
    ];
    const { container } = render(<AddonDots addons={addons} />);

    const dots = container.querySelectorAll('span.inline-block.h-2\\.5.w-2\\.5.rounded-full');
    expect(dots.length).toBe(3);

    // First dot should be red (Degraded), second amber (Progressing), third green (Healthy)
    expect(dots[0].className).toContain('bg-red-500');
    expect(dots[1].className).toContain('bg-amber-400');
    expect(dots[2].className).toContain('bg-green-500');
  });

  it('renders nothing when addons list is empty', () => {
    const { container } = render(<AddonDots addons={[]} />);
    expect(container.firstChild).toBeNull();
  });

  it('shows only non-zero categories in the count line', () => {
    const addons = Array.from({ length: 10 }, (_, i) => ({
      name: `addon-${i}`,
      health: i < 2 ? 'Degraded' : 'Healthy',
    }));
    const { container } = render(<AddonDots addons={addons} />);

    expect(screen.getByText(/2 degraded/)).toBeInTheDocument();
    expect(screen.getByText(/8 healthy/)).toBeInTheDocument();
    // Should NOT mention progressing/missing/unknown (count = 0)
    expect(container.textContent).not.toContain('progressing');
    expect(container.textContent).not.toContain('missing');
  });
});
