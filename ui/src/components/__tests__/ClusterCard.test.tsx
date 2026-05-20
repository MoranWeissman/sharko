import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ClusterCard } from '@/components/ClusterCard';

function renderCard(connectionStatus: string) {
  return render(
    <MemoryRouter>
      <ClusterCard
        name="prod-eu"
        connectionStatus={connectionStatus}
        addonSummary={[]}
        healthyCount={0}
        totalCount={0}
      />
    </MemoryRouter>,
  );
}

describe('ClusterCard connection pill', () => {
  it('renders green "Connected" for ArgoCD status "Successful"', () => {
    const { container } = renderCard('Successful');
    expect(screen.getByText('Connected')).toBeInTheDocument();

    const dot = container.querySelector('div.h-2.w-2.rounded-full');
    expect(dot).not.toBeNull();
    expect(dot!.className).toContain('bg-green-500');
  });

  it('renders green "Connected" for ArgoCD status "Connected"', () => {
    renderCard('Connected');
    expect(screen.getByText('Connected')).toBeInTheDocument();
  });

  it('renders red "Disconnected" for ArgoCD status "Failed"', () => {
    const { container } = renderCard('Failed');
    expect(screen.getByText('Disconnected')).toBeInTheDocument();

    const dot = container.querySelector('div.h-2.w-2.rounded-full');
    expect(dot!.className).toContain('bg-red-500');
  });

  // BUG-033 regression: the post-registration window where ArgoCD has
  // received the cluster Secret but has not yet probed it must render
  // as a neutral "Connecting…" pill — NOT a red "Disconnected" failure.
  // The old binary predicate (`status === 'Successful' || === 'Connected'`)
  // flashed red for the entire ~10-60s probe window, making registration
  // look broken even though it had completed successfully.
  it('renders neutral "Connecting…" — NOT red Disconnected — for empty status (BUG-033)', () => {
    const { container } = renderCard('');
    expect(screen.getByText('Connecting…')).toBeInTheDocument();
    expect(screen.queryByText('Disconnected')).not.toBeInTheDocument();

    const dot = container.querySelector('div.h-2.w-2.rounded-full');
    expect(dot!.className).not.toContain('bg-red-500');
    // Neutral blue-tinted dot per the Sharko palette.
    expect(dot!.className).toContain('bg-[#3a6a8a]');
  });

  it('renders neutral "Connecting…" for status "Unknown" (BUG-033)', () => {
    renderCard('Unknown');
    expect(screen.getByText('Connecting…')).toBeInTheDocument();
    expect(screen.queryByText('Disconnected')).not.toBeInTheDocument();
  });

  it('renders neutral "Connecting…" for status "missing" — cluster registered, ArgoCD not yet aware (BUG-033)', () => {
    renderCard('missing');
    expect(screen.getByText('Connecting…')).toBeInTheDocument();
    expect(screen.queryByText('Disconnected')).not.toBeInTheDocument();
  });
});
