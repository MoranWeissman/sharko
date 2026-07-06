import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { StatusBadge, statusDisplayName, clusterStatusMap } from '@/components/StatusBadge';

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
    // V2-cleanup-61.2 (D1): the PROBLEM name — enabled in the catalog but
    // ArgoCD has no Application for it. Never called "Not Deployed".
    expect(statusDisplayName('missing_in_argocd')).toBe('Missing from ArgoCD');
    expect(statusDisplayName('untracked_in_argocd')).toBe('Not managed');
    expect(statusDisplayName('unknown_state')).toBe('Unknown');
    expect(statusDisplayName('unknown_health')).toBe('Unknown');
    expect(statusDisplayName('healthy')).toBe('healthy');
  });

  it('renders friendly display name for disabled_in_git', () => {
    render(<StatusBadge status="disabled_in_git" />);
    expect(screen.getByText('Not Enabled')).toBeInTheDocument();
  });

  it('renders missing_in_argocd as "Missing from ArgoCD" with red problem styling', () => {
    const { container } = render(<StatusBadge status="missing_in_argocd" />);
    expect(screen.getByText('Missing from ArgoCD')).toBeInTheDocument();
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-red-50');
    expect(badge.className).toContain('text-red-700');
  });

  it('renders untracked_in_argocd as "Not managed" with amber attention styling (no purple)', () => {
    const { container } = render(<StatusBadge status="untracked_in_argocd" />);
    expect(screen.getByText('Not managed')).toBeInTheDocument();
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-amber-50');
    expect(badge.className).not.toContain('purple');
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

  // V2-cleanup-61.2 (D3): the best state is GREEN-family, not purple —
  // purple used to mean both "best state" and "warning".
  it('renders cluster status "Operational" with emerald (green-family) styling — never purple', () => {
    const { container } = render(<StatusBadge status="operational" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-emerald-50');
    expect(badge.className).toContain('text-emerald-700');
    expect(badge.className).not.toContain('purple');

    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-emerald-600');
    expect(screen.getByText('Operational')).toBeInTheDocument();
  });

  it('renders cluster status "Verified" with teal (green-family) styling', () => {
    const { container } = render(<StatusBadge status="verified" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-teal-50');
    expect(screen.getByText('Verified')).toBeInTheDocument();
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

  // V2-cleanup-61.2 (E2): tooltips say what the test verified in plain
  // words — the internal "Stage 1"/"Stage 2" vocabulary is retired.
  it('cluster-status tooltips carry no internal Stage vocabulary', () => {
    for (const def of Object.values(clusterStatusMap)) {
      expect(def.tooltip).not.toMatch(/stage\s*\d/i);
    }
  });

  it('renders test_failing warning icon when testFailing prop is true', () => {
    const { container } = render(<StatusBadge status="connected" testFailing />);
    // The AlertTriangle icon should be present
    const svg = container.querySelector('svg.lucide-triangle-alert');
    expect(svg).toBeTruthy();
    expect(screen.getByText('Connected')).toBeInTheDocument();
  });

  // --- V2-cleanup-36: new addon states ---

  it('renders "sync_failing" with red styling', () => {
    const { container } = render(<StatusBadge status="sync_failing" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-red-50');
    expect(badge.className).toContain('text-red-700');
    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-red-500');
  });

  it('renders "sync_failing" display name as "Sync failing"', () => {
    expect(statusDisplayName('sync_failing')).toBe('Sync failing');
    render(<StatusBadge status="sync_failing" />);
    expect(screen.getByText('Sync failing')).toBeInTheDocument();
  });

  it('renders "deploying" with blue styling', () => {
    const { container } = render(<StatusBadge status="deploying" />);
    const badge = container.firstChild as HTMLElement;
    expect(badge.className).toContain('bg-blue-50');
    expect(badge.className).toContain('text-blue-700');
    const dot = badge.querySelector('span span') as HTMLElement;
    expect(dot.className).toContain('bg-blue-500');
  });

  it('renders "deploying" display name as "Deploying"', () => {
    expect(statusDisplayName('deploying')).toBe('Deploying');
    render(<StatusBadge status="deploying" />);
    expect(screen.getByText('Deploying')).toBeInTheDocument();
  });
});
