import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusBadge, statusDisplayName } from '@/components/StatusBadge';

describe('StatusBadge', () => {
  it('renders the status text', () => {
    render(<StatusBadge status="Healthy" />);
    expect(screen.getByText('Healthy')).toBeInTheDocument();
  });

  it('renders with "Healthy" status and shows green styling', () => {
    const { container } = render(<StatusBadge status="Healthy" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-green-50');
    expect(badge.className).toContain('text-green-700');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-green-500');
  });

  it('renders with "Degraded" status and shows red styling', () => {
    const { container } = render(<StatusBadge status="Degraded" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-red-50');
    expect(badge.className).toContain('text-red-700');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-red-500');
  });

  it('renders with "Unknown" status and shows gray styling', () => {
    const { container } = render(<StatusBadge status="Unknown" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-gray-100');
    expect(badge.className).toContain('text-gray-600');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-gray-400');
  });

  it('maps internal statuses to friendly display names', () => {
    expect(statusDisplayName('disabled_in_git')).toBe('Not Enabled');
    expect(statusDisplayName('missing_in_argocd')).toBe('Not Deployed');
    expect(statusDisplayName('untracked_in_argocd')).toBe('Unmanaged');
    expect(statusDisplayName('unknown_state')).toBe('Unknown');
    expect(statusDisplayName('unknown_health')).toBe('Unknown');
    expect(statusDisplayName('healthy')).toBe('healthy');
  });

  it('renders friendly display name for disabled_in_git', () => {
    render(<StatusBadge status="disabled_in_git" />);
    expect(screen.getByText('Not Enabled')).toBeInTheDocument();
  });

  it('renders friendly display name for missing_in_argocd', () => {
    render(<StatusBadge status="missing_in_argocd" />);
    expect(screen.getByText('Not Deployed')).toBeInTheDocument();
  });

  it('renders friendly display name for untracked_in_argocd', () => {
    render(<StatusBadge status="untracked_in_argocd" />);
    expect(screen.getByText('Unmanaged')).toBeInTheDocument();
  });
});
