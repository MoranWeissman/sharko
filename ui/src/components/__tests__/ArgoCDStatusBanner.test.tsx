import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ArgoCDStatusBanner } from '@/components/ArgoCDStatusBanner';

describe('ArgoCDStatusBanner', () => {
  it('renders yellow banner with warning text when visible', () => {
    render(<ArgoCDStatusBanner visible={true} />);
    expect(
      screen.getByText('ArgoCD temporarily unreachable — showing last known state'),
    ).toBeInTheDocument();
  });

  it('dismiss button hides the banner', () => {
    render(<ArgoCDStatusBanner visible={true} />);
    expect(
      screen.getByText('ArgoCD temporarily unreachable — showing last known state'),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('Dismiss'));

    expect(
      screen.queryByText('ArgoCD temporarily unreachable — showing last known state'),
    ).not.toBeInTheDocument();
  });

  it('is not rendered when visible is false', () => {
    render(<ArgoCDStatusBanner visible={false} />);
    expect(
      screen.queryByText('ArgoCD temporarily unreachable — showing last known state'),
    ).not.toBeInTheDocument();
  });
});
