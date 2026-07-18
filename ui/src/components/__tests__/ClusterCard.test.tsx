import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClusterCard } from '@/components/ClusterCard';

interface RenderCardOpts {
  connectionStatus: string
  healthyCount?: number
  totalCount?: number
}

function renderCard(opts: RenderCardOpts) {
  const { connectionStatus, healthyCount = 0, totalCount = 0 } = opts
  return render(
    <MemoryRouter>
      <ClusterCard
        name="prod-eu"
        connectionStatus={connectionStatus}
        healthyCount={healthyCount}
        totalCount={totalCount}
      />
    </MemoryRouter>,
  );
}

describe('ClusterCard connection pill (LW-5 collapsed)', () => {
  it('renders green "Connected to ArgoCD" for ArgoCD status "Successful"', () => {
    const { container } = renderCard({ connectionStatus: 'Successful' });
    expect(screen.getByText('Connected to ArgoCD')).toBeInTheDocument();

    const dot = container.querySelector('div.h-2.w-2.rounded-full');
    expect(dot).not.toBeNull();
    expect(dot!.className).toContain('bg-green-500');
  });

  it('renders green "Connected to ArgoCD" for ArgoCD status "Connected"', () => {
    renderCard({ connectionStatus: 'Connected' });
    expect(screen.getByText('Connected to ArgoCD')).toBeInTheDocument();
  });

  it('renders red "Disconnected to ArgoCD" for ArgoCD status "Failed"', () => {
    const { container } = renderCard({ connectionStatus: 'Failed' });
    expect(screen.getByText('Disconnected to ArgoCD')).toBeInTheDocument();

    const dot = container.querySelector('div.h-2.w-2.rounded-full');
    expect(dot!.className).toContain('bg-red-500');
  });

  // BUG-033 regression: the post-registration window where ArgoCD has
  // received the cluster Secret but has not yet probed it must render
  // as a neutral "Connecting…" pill — NOT a red "Disconnected" failure.
  // The old binary predicate (`status === 'Successful' || === 'Connected'`)
  // flashed red for the entire ~10-60s probe window, making registration
  // look broken even though it had completed successfully.
  it('renders neutral "Connecting… to ArgoCD" — NOT red Disconnected — for empty status (BUG-033)', () => {
    const { container } = renderCard({ connectionStatus: '' });
    expect(screen.getByText('Connecting… to ArgoCD')).toBeInTheDocument();
    expect(screen.queryByText('Disconnected to ArgoCD')).not.toBeInTheDocument();

    const dot = container.querySelector('div.h-2.w-2.rounded-full');
    expect(dot!.className).not.toContain('bg-red-500');
    // Neutral blue-tinted dot per the Sharko palette.
    expect(dot!.className).toContain('bg-[#3a6a8a]');
  });

  it('renders neutral "Connecting… to ArgoCD" for status "Unknown" (BUG-033)', () => {
    renderCard({ connectionStatus: 'Unknown' });
    expect(screen.getByText('Connecting… to ArgoCD')).toBeInTheDocument();
    expect(screen.queryByText('Disconnected to ArgoCD')).not.toBeInTheDocument();
  });

  // V2-cleanup-75.1: "missing" (ArgoCD has NO connection for this cluster
  // at all) is its own amber "Not connected" state — distinct from the
  // neutral "Connecting…" pending window above. Showing it as "Connecting…"
  // left a cluster ArgoCD can't reach (e.g. a local/kind cluster an EKS hub
  // can't see) looking like it was about to work, forever.
  it('renders amber "Not connected to ArgoCD" — NOT "Connecting…" — for status "missing"', () => {
    const { container } = renderCard({ connectionStatus: 'missing' });
    expect(screen.getByText('Not connected to ArgoCD')).toBeInTheDocument();
    expect(screen.queryByText('Connecting… to ArgoCD')).not.toBeInTheDocument();
    expect(screen.queryByText('Disconnected to ArgoCD')).not.toBeInTheDocument();

    const dot = container.querySelector('div.h-2.w-2.rounded-full');
    expect(dot!.className).toContain('bg-amber-500');
  });

  it('renders amber "Not connected to ArgoCD" for status "missing_from_argocd" too', () => {
    renderCard({ connectionStatus: 'missing_from_argocd' });
    expect(screen.getByText('Not connected to ArgoCD')).toBeInTheDocument();
  });
});

// LW-17: single colored addon-health ratio — "X of Y addons healthy"
// (replaces LW-4's plain text + LW-2's dots)
describe('ClusterCard addon health ratio (LW-17)', () => {
  it('renders "N of M addons healthy" with red color when 0 healthy', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 0, totalCount: 5 });
    const text = screen.getByText('0 of 5 addons healthy');
    expect(text).toBeInTheDocument();
    expect(text.className).toContain('text-red-700');
    expect(text.className).toContain('dark:text-red-400');
  });

  it('renders "N of M addons healthy" with orange color when partially healthy', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 2, totalCount: 5 });
    const text = screen.getByText('2 of 5 addons healthy');
    expect(text).toBeInTheDocument();
    expect(text.className).toContain('text-amber-700');
    expect(text.className).toContain('dark:text-amber-400');
  });

  it('renders "N of M addons healthy" with green color when all healthy', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 5, totalCount: 5 });
    const text = screen.getByText('5 of 5 addons healthy');
    expect(text).toBeInTheDocument();
    expect(text.className).toContain('text-green-700');
    expect(text.className).toContain('dark:text-green-400');
  });

  it('renders "No addons" with neutral color when totalCount is 0', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 0, totalCount: 0 });
    const text = screen.getByText('No addons');
    expect(text).toBeInTheDocument();
    expect(text.className).toContain('text-[#2a5a7a]');
  });
});

// LW-3 + LW-17: inline reason for CONNECTION problems only
// (addon health is now shown via the colored ratio line, not a separate reason text)
describe('ClusterCard attention reason (LW-3, refined by LW-17)', () => {
  it('shows "Disconnected" reason for Failed status', () => {
    renderCard({ connectionStatus: 'Failed', healthyCount: 0, totalCount: 0 });
    expect(screen.getByText('Disconnected')).toBeInTheDocument();
  });

  it('shows "Not connected" reason for missing status', () => {
    renderCard({ connectionStatus: 'missing', healthyCount: 0, totalCount: 0 });
    expect(screen.getByText('Not connected')).toBeInTheDocument();
  });

  it('shows "Not reporting" reason for Unknown with addons', () => {
    renderCard({ connectionStatus: 'Unknown', healthyCount: 0, totalCount: 3 });
    expect(screen.getByText('Not reporting')).toBeInTheDocument();
  });

  it('does NOT show "All addons unhealthy" — addon health is via colored ratio (LW-17)', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 0, totalCount: 3 });
    expect(screen.queryByText('All addons unhealthy')).not.toBeInTheDocument();
    // Instead, the colored ratio shows the health
    expect(screen.getByText('0 of 3 addons healthy')).toBeInTheDocument();
  });

  it('does NOT show a connection reason for Connected with healthy addon', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 1, totalCount: 3 });
    expect(screen.queryByText('Disconnected')).not.toBeInTheDocument();
    expect(screen.queryByText('Not reporting')).not.toBeInTheDocument();
    expect(screen.queryByText('All addons unhealthy')).not.toBeInTheDocument();
  });
});
