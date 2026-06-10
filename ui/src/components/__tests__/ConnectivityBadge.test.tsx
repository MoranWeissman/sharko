import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ConnectivityBadge } from '@/components/ConnectivityBadge';

describe('ConnectivityBadge', () => {
  it('renders nothing when connectivityStatus is absent', () => {
    const { container } = render(<ConnectivityBadge />);
    expect(container.firstChild).toBeNull();
  });

  it('renders nothing when connectivityStatus is empty string', () => {
    const { container } = render(<ConnectivityBadge connectivityStatus="" />);
    expect(container.firstChild).toBeNull();
  });

  it('verified_argocd: shows green "Connectivity verified ✓" badge', () => {
    render(<ConnectivityBadge connectivityStatus="verified_argocd" />);
    expect(screen.getByText(/Connectivity verified/)).toBeInTheDocument();
    const badge = screen.getByText(/Connectivity verified/).closest('span');
    expect(badge?.className).toContain('green');
  });

  it('verified_check: shows green "Connectivity verified ✓" badge', () => {
    render(<ConnectivityBadge connectivityStatus="verified_check" />);
    expect(screen.getByText(/Connectivity verified/)).toBeInTheDocument();
    const badge = screen.getByText(/Connectivity verified/).closest('span');
    expect(badge?.className).toContain('green');
  });

  it('check_failed: shows amber "Connectivity check failed" badge', () => {
    render(<ConnectivityBadge connectivityStatus="check_failed" />);
    expect(screen.getByText(/Connectivity check failed/)).toBeInTheDocument();
    const badge = screen.getByText(/Connectivity check failed/).closest('span');
    expect(badge?.className).toContain('amber');
  });

  it('check_failed: passes connectivityDetail as data-detail attribute on the trigger', () => {
    // TooltipContent renders into a Radix portal which only opens on hover —
    // not accessible via textContent in unit tests. We verify the detail is
    // carried on the trigger span's data-detail attribute instead.
    render(
      <ConnectivityBadge connectivityStatus="check_failed" connectivityDetail="namespace not found" />,
    );
    const trigger = screen.getByText(/Connectivity check failed/).closest('span');
    expect(trigger?.getAttribute('data-detail')).toBe('namespace not found');
  });

  it('sharko status: shows secondary line when sharkoStatus present', () => {
    render(
      <ConnectivityBadge
        sharkoStatus="Connected"
        lastTestAt={new Date(Date.now() - 60000).toISOString()}
      />,
    );
    // Should show a "Sharko can reach it" line with relative time.
    expect(screen.getByText(/Sharko can reach it/)).toBeInTheDocument();
  });

  it('sharko test_failing: shows warning line', () => {
    render(
      <ConnectivityBadge
        sharkoStatus="Unreachable"
        testFailing={true}
        testErrorCode="ERR_TIMEOUT"
        lastTestAt={new Date(Date.now() - 120000).toISOString()}
      />,
    );
    expect(screen.getByText(/Sharko test failed/)).toBeInTheDocument();
    expect(screen.getByText(/ERR_TIMEOUT/)).toBeInTheDocument();
  });

  it('renders both primary badge and secondary line together', () => {
    render(
      <ConnectivityBadge
        connectivityStatus="verified_check"
        sharkoStatus="Connected"
        lastTestAt={new Date(Date.now() - 60000).toISOString()}
      />,
    );
    expect(screen.getByText(/Connectivity verified/)).toBeInTheDocument();
    expect(screen.getByText(/Sharko can reach it/)).toBeInTheDocument();
  });
});
