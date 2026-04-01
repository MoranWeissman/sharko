import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ConnectionStatus } from '@/components/ConnectionStatus';

describe('ConnectionStatus', () => {
  it('"connected" shows "Connected" text with green color', () => {
    const { container } = render(<ConnectionStatus status="connected" />);
    expect(screen.getByText('Connected')).toBeInTheDocument();

    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.className).toContain('text-green-600');
  });

  it('"failed" shows "Failed" text', () => {
    render(<ConnectionStatus status="failed" />);
    expect(screen.getByText('Failed')).toBeInTheDocument();
  });

  it('"missing" shows "Missing from ArgoCD" text', () => {
    render(<ConnectionStatus status="missing" />);
    expect(screen.getByText('Missing from ArgoCD')).toBeInTheDocument();
  });

  it('unknown status shows "Unknown"', () => {
    render(<ConnectionStatus status="something_else" />);
    expect(screen.getByText('Unknown')).toBeInTheDocument();
  });
});
