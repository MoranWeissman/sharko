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

  it('renders with "Unknown" status and shows blue-tinted styling', () => {
    const { container } = render(<StatusBadge status="Unknown" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-[#d6eeff]');
    expect(badge.className).toContain('text-[#1a4a6a]');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-[#3a6a8a]');
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

  // --- Cluster status tests ---

  it('renders cluster status "Unknown" with gray styling', () => {
    const { container } = render(<StatusBadge status="unknown" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-[#d6eeff]');
    expect(badge.className).toContain('text-[#1a4a6a]');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-[#3a6a8a]');
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });

  it('renders cluster status "Connected" with green styling', () => {
    const { container } = render(<StatusBadge status="connected" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-green-50');
    expect(badge.className).toContain('text-green-700');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-green-500');
    expect(screen.getByText('Connected')).toBeInTheDocument();
  });

  it('renders cluster status "Operational" with purple styling', () => {
    const { container } = render(<StatusBadge status="operational" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-purple-50');
    expect(badge.className).toContain('text-purple-700');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-purple-500');
    expect(screen.getByText('Operational')).toBeInTheDocument();
  });

  it('renders cluster status "Unreachable" with red styling', () => {
    const { container } = render(<StatusBadge status="unreachable" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-red-50');
    expect(badge.className).toContain('text-red-700');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-red-500');
    expect(screen.getByText('Unreachable')).toBeInTheDocument();
  });

  it('renders test_failing warning icon when testFailing prop is true', () => {
    const { container } = render(<StatusBadge status="connected" testFailing />);
    // The AlertTriangle icon should be present
    const svg = container.querySelector('svg.lucide-triangle-alert');
    expect(svg).toBeTruthy();
    expect(screen.getByText('Connected')).toBeInTheDocument();
  });
});
