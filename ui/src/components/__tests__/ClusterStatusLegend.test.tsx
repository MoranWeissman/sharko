import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ClusterStatusLegend } from '@/components/ClusterStatusLegend';

describe('ClusterStatusLegend', () => {
  it('renders the "Cluster Status:" label', () => {
    render(<ClusterStatusLegend />);
    expect(screen.getByText('Cluster Status:')).toBeInTheDocument();
  });

  it('renders all 5 cluster statuses', () => {
    render(<ClusterStatusLegend />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('Verified')).toBeInTheDocument();
    expect(screen.getByText('Operational')).toBeInTheDocument();
    expect(screen.getByText('Unreachable')).toBeInTheDocument();
  });

  it('renders a colored dot indicator for each status', () => {
    const { container } = render(<ClusterStatusLegend />);
    const dots = container.querySelectorAll('span.rounded-full');
    expect(dots.length).toBe(5);

    // Verify specific dot colors
    const dotClasses = Array.from(dots).map((d) => d.className);
    expect(dotClasses.some((c) => c.includes('bg-[#3a6a8a]'))).toBe(true); // Unknown
    expect(dotClasses.some((c) => c.includes('bg-green-500'))).toBe(true); // Connected
    expect(dotClasses.some((c) => c.includes('bg-blue-500'))).toBe(true); // Verified
    expect(dotClasses.some((c) => c.includes('bg-purple-500'))).toBe(true); // Operational
    expect(dotClasses.some((c) => c.includes('bg-red-500'))).toBe(true); // Unreachable
  });
});
