import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClusterCard } from '@/components/ClusterCard';

interface RenderCardOpts {
  connectionStatus: string
  healthyCount?: number
  totalCount?: number
  addonSummary?: Array<{ name: string; health: string }>
}

function renderCard(opts: RenderCardOpts) {
  const { connectionStatus, healthyCount = 0, totalCount = 0, addonSummary = [] } = opts
  return render(
    <MemoryRouter>
      <ClusterCard
        name="prod-eu"
        connectionStatus={connectionStatus}
        addonSummary={addonSummary}
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

// LW-4: honest addon count label — "N of M addons healthy"
describe('ClusterCard addon count label (LW-4)', () => {
  it('renders "N of M addons healthy" — NOT "N/M healthy"', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 2, totalCount: 5 });
    expect(screen.getByText('2 of 5 addons healthy')).toBeInTheDocument();
    expect(screen.queryByText('2/5 healthy')).not.toBeInTheDocument();
  });

  it('renders "No addons" when totalCount is 0', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 0, totalCount: 0 });
    expect(screen.getByText('No addons')).toBeInTheDocument();
  });
});

// LW-3: inline reason for why this cluster needs attention
describe('ClusterCard attention reason (LW-3)', () => {
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

  it('shows "All addons unhealthy" reason for Connected with 0 healthy out of N', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 0, totalCount: 3 });
    expect(screen.getByText('All addons unhealthy')).toBeInTheDocument();
  });

  it('does NOT show a reason for Connected with at least one healthy addon', () => {
    renderCard({ connectionStatus: 'Successful', healthyCount: 1, totalCount: 3 });
    expect(screen.queryByText('Disconnected')).not.toBeInTheDocument();
    expect(screen.queryByText('Not reporting')).not.toBeInTheDocument();
    expect(screen.queryByText('All addons unhealthy')).not.toBeInTheDocument();
  });
});
