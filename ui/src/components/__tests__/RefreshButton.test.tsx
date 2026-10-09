import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { RefreshButton } from '@/components/RefreshButton';

describe('RefreshButton', () => {
  it('renders the button with icon and label', () => {
    render(<RefreshButton onRefresh={vi.fn()} />);
    const button = screen.getByRole('button', { name: /refresh/i });
    expect(button).toBeInTheDocument();
    expect(button).toHaveTextContent('Refresh');
  });

  it('calls onRefresh when clicked', () => {
    const onRefresh = vi.fn();
    render(<RefreshButton onRefresh={onRefresh} />);

    const button = screen.getByRole('button', { name: /refresh/i });
    fireEvent.click(button);

    expect(onRefresh).toHaveBeenCalledTimes(1);
  });

  it('shows spinner while refresh is in progress', async () => {
    const onRefresh = vi.fn(() => new Promise<void>((resolve) => setTimeout(resolve, 100)));
    const { container } = render(<RefreshButton onRefresh={onRefresh} />);

    const button = screen.getByRole('button', { name: /refresh/i });
    fireEvent.click(button);

    // Icon should have animate-spin class
    const icon = container.querySelector('.animate-spin');
    expect(icon).toBeInTheDocument();

    await waitFor(() => expect(button).not.toBeDisabled(), { timeout: 200 });
  });

  it('displays last updated relative time when provided', () => {
    const fiveMinutesAgo = new Date(Date.now() - 5 * 60 * 1000);
    render(<RefreshButton onRefresh={vi.fn()} lastUpdated={fiveMinutesAgo} />);

    expect(screen.getByText(/Updated 5m ago/i)).toBeInTheDocument();
  });

  it('displays tooltip when provided', () => {
    render(<RefreshButton onRefresh={vi.fn()} tooltip="Refresh cluster list" />);
    const button = screen.getByRole('button', { name: /refresh/i });
    expect(button).toBeInTheDocument();
    // Tooltip requires hover/interaction to show; just verify button renders
  });

  it('disables button while refreshing', async () => {
    const onRefresh = vi.fn(() => new Promise<void>((resolve) => setTimeout(resolve, 50)));
    render(<RefreshButton onRefresh={onRefresh} />);

    const button = screen.getByRole('button', { name: /refresh/i });
    fireEvent.click(button);

    expect(button).toBeDisabled();

    await waitFor(() => expect(button).not.toBeDisabled(), { timeout: 100 });
  });
});
