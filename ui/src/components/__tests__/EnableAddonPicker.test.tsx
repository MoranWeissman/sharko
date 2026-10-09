/**
 * EnableAddonPicker — unit tests.
 *
 * Covers: open/close, filtering, staging (onEnable callback),
 * empty-catalog and empty-search states, loading + error states
 * added in V2-cleanup-32.
 */
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { EnableAddonPicker } from '@/components/EnableAddonPicker';

const ALL_ADDONS = ['cert-manager', 'ingress-nginx', 'prometheus', 'velero'];

function renderPicker({
  open = true,
  allAddonNames = ALL_ADDONS,
  enabledNames = new Set<string>(),
  loading = false,
  error = null as string | null,
  onEnable = vi.fn(),
  onClose = vi.fn(),
  onRetry = undefined as (() => void) | undefined,
} = {}) {
  return render(
    <EnableAddonPicker
      open={open}
      allAddonNames={allAddonNames}
      enabledNames={enabledNames}
      loading={loading}
      error={error}
      onEnable={onEnable}
      onClose={onClose}
      onRetry={onRetry}
    />,
  );
}

describe('EnableAddonPicker', () => {
  it('renders all non-enabled addons when open', () => {
    renderPicker({ enabledNames: new Set(['ingress-nginx']) });
    expect(screen.getByTestId('addon-picker-item-cert-manager')).toBeInTheDocument();
    expect(screen.getByTestId('addon-picker-item-prometheus')).toBeInTheDocument();
    expect(screen.getByTestId('addon-picker-item-velero')).toBeInTheDocument();
    // ingress-nginx is already enabled — must not appear in the picker
    expect(screen.queryByTestId('addon-picker-item-ingress-nginx')).not.toBeInTheDocument();
  });

  it('does not render content when closed', () => {
    renderPicker({ open: false });
    expect(screen.queryByTestId('addon-picker-search')).not.toBeInTheDocument();
  });

  it('filters list by search query (case-insensitive)', () => {
    renderPicker();
    const search = screen.getByTestId('addon-picker-search');
    fireEvent.change(search, { target: { value: 'CERT' } });
    expect(screen.getByTestId('addon-picker-item-cert-manager')).toBeInTheDocument();
    expect(screen.queryByTestId('addon-picker-item-prometheus')).not.toBeInTheDocument();
    expect(screen.queryByTestId('addon-picker-item-velero')).not.toBeInTheDocument();
  });

  it('calls onEnable with the clicked addon name', () => {
    const onEnable = vi.fn();
    renderPicker({ onEnable });
    fireEvent.click(screen.getByTestId('addon-picker-item-prometheus'));
    expect(onEnable).toHaveBeenCalledOnce();
    expect(onEnable).toHaveBeenCalledWith('prometheus');
  });

  it('calls onClose when Done button is clicked', () => {
    const onClose = vi.fn();
    renderPicker({ onClose });
    fireEvent.click(screen.getByTestId('addon-picker-done'));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('shows "All catalog addons already enabled" when all addons are in enabledNames', () => {
    renderPicker({ enabledNames: new Set(ALL_ADDONS) });
    expect(
      screen.getByText(/all catalog addons are already enabled/i),
    ).toBeInTheDocument();
  });

  it('shows "No addons match your search" when search has no hits', () => {
    renderPicker();
    fireEvent.change(screen.getByTestId('addon-picker-search'), {
      target: { value: 'zzznomatch' },
    });
    expect(screen.getByText(/no addons match your search/i)).toBeInTheDocument();
  });

  it('renders an empty catalog message when allAddonNames is empty', () => {
    renderPicker({ allAddonNames: [] });
    expect(
      screen.getByText(/all catalog addons are already enabled/i),
    ).toBeInTheDocument();
  });

  // V2-cleanup-32: loading and error states.

  it('shows a loading spinner and hides the search field when loading=true', () => {
    renderPicker({ loading: true });
    expect(screen.queryByTestId('addon-picker-search')).not.toBeInTheDocument();
    expect(screen.queryByTestId('addon-picker-list')).not.toBeInTheDocument();
    expect(screen.getByText(/loading catalog/i)).toBeInTheDocument();
  });

  it('shows an error message and Retry button when error is set', () => {
    const onRetry = vi.fn();
    renderPicker({ error: 'Network error', onRetry });
    expect(screen.queryByTestId('addon-picker-search')).not.toBeInTheDocument();
    expect(screen.getByText('Network error')).toBeInTheDocument();
    const retryBtn = screen.getByTestId('addon-picker-retry');
    fireEvent.click(retryBtn);
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('does not show a Retry button when onRetry is not provided', () => {
    renderPicker({ error: 'Network error' });
    expect(screen.queryByTestId('addon-picker-retry')).not.toBeInTheDocument();
  });
});
