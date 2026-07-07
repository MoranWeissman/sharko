import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ConnectionStatus } from '@/components/ConnectionStatus';

// V2-cleanup-61.2 (finding D2): ConnectionStatus renders the canonical
// "ArgoCD → cluster" vocabulary from lib/clusterStatus.ts — the same
// names/colors as ClusterCard, the stat cards, and the legend.
describe('ConnectionStatus', () => {
  it('"connected" shows "Connected" text with green color', () => {
    const { container } = render(<ConnectionStatus status="connected" />);
    expect(screen.getByText('Connected')).toBeInTheDocument();

    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.className).toContain('text-green-700');
  });

  it('"failed" shows "Disconnected" text with red color', () => {
    const { container } = render(<ConnectionStatus status="failed" />);
    expect(screen.getByText('Disconnected')).toBeInTheDocument();

    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.className).toContain('text-red-700');
  });

  it('"" (empty) shows the neutral "Connecting…" state — the normal post-registration window', () => {
    const { container } = render(<ConnectionStatus status="" />);
    expect(screen.getByText('Connecting…')).toBeInTheDocument();

    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.className).toContain('text-[#1a4a6a]');
  });

  it('"missing" shows the amber "Not connected" state — NOT the pending "Connecting…" state (V2-cleanup-75.1)', () => {
    const { container } = render(<ConnectionStatus status="missing" />);
    expect(screen.getByText('Not connected')).toBeInTheDocument();
    expect(screen.queryByText('Connecting…')).not.toBeInTheDocument();

    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.className).toContain('text-amber-700');
  });

  it('"missing_from_argocd" also shows the amber "Not connected" state', () => {
    const { container } = render(<ConnectionStatus status="missing_from_argocd" />);
    expect(screen.getByText('Not connected')).toBeInTheDocument();

    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.className).toContain('text-amber-700');
  });

  it('"not_in_git" shows "Not managed" with amber attention color', () => {
    const { container } = render(<ConnectionStatus status="not_in_git" />);
    expect(screen.getByText('Not managed')).toBeInTheDocument();

    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.className).toContain('text-amber-700');
  });

  it('an unrecognized status falls through to "Disconnected" (red), never a silent green', () => {
    render(<ConnectionStatus status="something_else" />);
    expect(screen.getByText('Disconnected')).toBeInTheDocument();
  });

  it('exposes the plain-English meaning as a tooltip', () => {
    const { container } = render(<ConnectionStatus status="connected" />);
    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper).toHaveAttribute('title', 'ArgoCD is connected to this cluster.');
  });
});
