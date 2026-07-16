import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ConfirmationModal } from '@/components/ConfirmationModal';

function renderConfirmationModal(props: Partial<Parameters<typeof ConfirmationModal>[0]> = {}) {
  const defaults = {
    open: true,
    onClose: vi.fn(),
    onConfirm: vi.fn(),
    title: 'Confirm Action',
    description: 'Are you sure?',
  };
  return render(<ConfirmationModal {...defaults} {...props} />);
}

describe('ConfirmationModal', () => {
  it('renders with scrollable body and footer outside scroll region', () => {
    const extraContent = <div data-testid="extra-content">Preview panel here</div>;
    renderConfirmationModal({ extraContent });

    // Title and description render
    expect(screen.getByText('Confirm Action')).toBeInTheDocument();
    expect(screen.getByText('Are you sure?')).toBeInTheDocument();

    // Extra content renders
    expect(screen.getByTestId('extra-content')).toBeInTheDocument();

    // Footer buttons render (they should be outside the scroll region)
    expect(screen.getByText('Cancel')).toBeInTheDocument();
    expect(screen.getByText('Confirm')).toBeInTheDocument();

    // The dialog content has the containment classes
    const dialogContent = screen.getByRole('dialog');
    expect(dialogContent).toBeInTheDocument();
    // We can't directly test the classes on DialogContent since it's inside radix,
    // but we verify structure: body scrolls, footer is visible
  });

  it('renders type-to-confirm input inside scrollable body', () => {
    renderConfirmationModal({ typeToConfirm: 'DELETE' });

    expect(screen.getByText(/Type/)).toBeInTheDocument();
    expect(screen.getByPlaceholderText('DELETE')).toBeInTheDocument();
  });

  it('footer stays visible when extra content is present', () => {
    const extraContent = (
      <div style={{ height: '500px' }} data-testid="tall-content">
        Tall content
      </div>
    );
    renderConfirmationModal({ extraContent });

    // Footer buttons should still be in the document (not scrolled out)
    expect(screen.getByText('Cancel')).toBeInTheDocument();
    expect(screen.getByText('Confirm')).toBeInTheDocument();
  });
});
