import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ClusterStatusLegend } from '@/components/ClusterStatusLegend';
import {
  CLUSTER_CONNECTION_KINDS,
  CLUSTER_CONNECTION_STATES,
} from '@/lib/clusterStatus';

// V2-cleanup-61.2 (finding D2): the legend lists EXACTLY the canonical
// "ArgoCD → cluster" connection states the Clusters view can display —
// no more, no fewer. It used to describe a five-state test ladder the
// table almost never showed.
describe('ClusterStatusLegend', () => {
  it('renders the "Cluster Status:" label', () => {
    render(<ClusterStatusLegend />);
    expect(screen.getByText('Cluster Status:')).toBeInTheDocument();
  });

  it('renders the five canonical connection states', () => {
    render(<ClusterStatusLegend />);
    expect(screen.getByText('Connected')).toBeInTheDocument();
    expect(screen.getByText('Connecting…')).toBeInTheDocument();
    // V2-cleanup-75.1: "Not connected" (amber) is distinct from
    // "Connecting…" (neutral) — ArgoCD has no connection at all vs. the
    // normal post-registration wait.
    expect(screen.getByText('Not connected')).toBeInTheDocument();
    expect(screen.getByText('Not managed')).toBeInTheDocument();
    expect(screen.getByText('Disconnected')).toBeInTheDocument();
  });

  it('legend contents match exactly the displayable states (one entry per canonical state)', () => {
    render(<ClusterStatusLegend />);
    // Every canonical state appears…
    for (const kind of CLUSTER_CONNECTION_KINDS) {
      const def = CLUSTER_CONNECTION_STATES[kind];
      const item = screen.getByText(def.label);
      expect(item).toBeInTheDocument();
      // …with its plain-English meaning available as a tooltip.
      expect(item.closest('[title]')).toHaveAttribute('title', def.meaning);
    }
    // …and nothing else: no leftover 5-state test-ladder vocabulary.
    for (const stale of ['Unknown', 'Verified', 'Operational', 'Unreachable']) {
      expect(screen.queryByText(stale)).not.toBeInTheDocument();
    }
  });

  it('renders one severity-colored dot per state — no purple anywhere', () => {
    const { container } = render(<ClusterStatusLegend />);
    const dots = container.querySelectorAll('span.h-2\\.5');
    expect(dots.length).toBe(CLUSTER_CONNECTION_KINDS.length);

    const dotClasses = Array.from(dots).map((d) => d.className);
    expect(dotClasses.some((c) => c.includes('bg-green-500'))).toBe(true); // Connected
    expect(dotClasses.some((c) => c.includes('bg-[#3a6a8a]'))).toBe(true); // Connecting…
    expect(dotClasses.some((c) => c.includes('bg-amber-500'))).toBe(true); // Not managed
    expect(dotClasses.some((c) => c.includes('bg-red-500'))).toBe(true); // Disconnected
    // Purple is retired (V2-cleanup-61.2, D3).
    expect(dotClasses.some((c) => c.includes('purple'))).toBe(false);
  });
});
