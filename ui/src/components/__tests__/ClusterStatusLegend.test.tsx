import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ClusterStatusLegend } from '@/components/ClusterStatusLegend';
import {
  CLUSTER_CONNECTION_KINDS,
  CLUSTER_CONNECTION_STATES,
  NO_ADDONS_YET_STATE,
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
    expect(screen.getByText('Available to manage')).toBeInTheDocument();
    expect(screen.getByText('Disconnected')).toBeInTheDocument();
  });

  it('writes every meaning on screen, not in hover text', () => {
    const { container } = render(<ClusterStatusLegend />);
    for (const kind of CLUSTER_CONNECTION_KINDS) {
      const def = CLUSTER_CONNECTION_STATES[kind];
      expect(screen.getByText(def.label)).toBeInTheDocument();
      expect(screen.getByText(def.meaning)).toBeVisible();
    }
    // Nothing is left to a hover tooltip.
    expect(container.querySelector('[title]')).toBeNull();
    // …and nothing else: no leftover 5-state test-ladder vocabulary.
    for (const stale of ['Unknown', 'Verified', 'Operational', 'Unreachable']) {
      expect(screen.queryByText(stale)).not.toBeInTheDocument();
    }
  });

  it('lists "Not checked yet" with its meaning', () => {
    render(<ClusterStatusLegend />);
    expect(screen.getByText(/^Not checked yet$/)).toBeInTheDocument();
    expect(screen.getByText(NO_ADDONS_YET_STATE.meaning)).toBeVisible();
  });

  // Round R (R20): the meaning uses the user's words and names both ways in.
  it('"Available to manage" says what it means, with no "Git catalog"', () => {
    const meaning = CLUSTER_CONNECTION_STATES.unmanaged.meaning;
    expect(meaning).toBe(
      'ArgoCD connects to it, but Sharko does not manage it yet. Adopt it or take ownership of it to let Sharko manage its addons.',
    );
    render(<ClusterStatusLegend />);
    expect(screen.getByText(meaning)).toBeVisible();
    expect(document.body.textContent).not.toMatch(/Git catalog/i);
    expect(document.body.textContent).not.toContain("In ArgoCD but not in Sharko's");
  });

  it('"Not connected" and "Available to manage" do not share a colour', () => {
    const missing = CLUSTER_CONNECTION_STATES.missing;
    const unmanaged = CLUSTER_CONNECTION_STATES.unmanaged;
    expect(missing.dot).not.toBe(unmanaged.dot);
    expect(missing.text).not.toBe(unmanaged.text);
    const { container } = render(<ClusterStatusLegend />);
    const dots = Array.from(container.querySelectorAll('span.h-2\\.5')).map((d) => d.className);
    expect(dots.filter((c) => c.includes('bg-amber-500'))).toHaveLength(1);
    expect(dots.filter((c) => c.includes('bg-indigo-500'))).toHaveLength(1);
  });

  it('renders one severity-colored dot per state — no purple anywhere', () => {
    const { container } = render(<ClusterStatusLegend />);
    const dots = container.querySelectorAll('span.h-2\\.5');
    expect(dots.length).toBe(CLUSTER_CONNECTION_KINDS.length + 1); // + "Not checked yet"

    const dotClasses = Array.from(dots).map((d) => d.className);
    expect(dotClasses.some((c) => c.includes('bg-green-500'))).toBe(true); // Connected
    expect(dotClasses.some((c) => c.includes('bg-[#3a6a8a]'))).toBe(true); // Connecting…
    expect(dotClasses.some((c) => c.includes('bg-amber-500'))).toBe(true); // Not connected
    expect(dotClasses.some((c) => c.includes('bg-indigo-500'))).toBe(true); // Available to manage
    expect(dotClasses.some((c) => c.includes('bg-red-500'))).toBe(true); // Disconnected
    // Purple is retired (V2-cleanup-61.2, D3).
    expect(dotClasses.some((c) => c.includes('purple'))).toBe(false);
  });
});
