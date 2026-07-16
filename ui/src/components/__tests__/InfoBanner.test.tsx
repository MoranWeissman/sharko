import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { InfoBanner } from '@/components/InfoBanner';

describe('InfoBanner', () => {
  it('renders info variant with title', () => {
    render(<InfoBanner variant="info" title="System maintenance scheduled" />);
    expect(screen.getByText('System maintenance scheduled')).toBeInTheDocument();
  });

  it('renders warning variant with title', () => {
    render(<InfoBanner variant="warning" title="Connection error detected" />);
    expect(screen.getByText('Connection error detected')).toBeInTheDocument();
  });

  it('renders children content when provided', () => {
    render(
      <InfoBanner variant="info" title="Notice">
        <p>Additional details here.</p>
      </InfoBanner>
    );
    expect(screen.getByText('Additional details here.')).toBeInTheDocument();
  });

  it('renders action node when provided', () => {
    render(
      <InfoBanner
        variant="warning"
        title="Action required"
        action={<button>Fix Now</button>}
      />
    );
    expect(screen.getByRole('button', { name: 'Fix Now' })).toBeInTheDocument();
  });

  it('renders dismiss button when onDismiss provided', () => {
    const onDismiss = vi.fn();
    render(<InfoBanner variant="info" title="Notice" onDismiss={onDismiss} />);

    const dismissButton = screen.getByLabelText('Dismiss');
    expect(dismissButton).toBeInTheDocument();
  });

  it('calls onDismiss and hides banner when dismiss clicked', () => {
    const onDismiss = vi.fn();
    render(<InfoBanner variant="info" title="Notice" onDismiss={onDismiss} />);

    const dismissButton = screen.getByLabelText('Dismiss');
    fireEvent.click(dismissButton);

    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Notice')).not.toBeInTheDocument();
  });

  it('applies info styling for info variant', () => {
    const { container } = render(<InfoBanner variant="info" title="Info message" />);
    const banner = container.firstChild as HTMLElement;
    expect(banner.className).toContain('border-[#6aade0]');
    expect(banner.className).toContain('bg-[#e8f4ff]');
  });

  it('applies warning styling for warning variant', () => {
    const { container } = render(<InfoBanner variant="warning" title="Warning message" />);
    const banner = container.firstChild as HTMLElement;
    expect(banner.className).toContain('border-amber-300');
    expect(banner.className).toContain('bg-amber-50');
  });
});
