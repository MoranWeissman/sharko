import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { HelperText } from '../HelperText';

describe('HelperText', () => {
  it('renders with default readable styles', () => {
    render(<HelperText>Test helper text</HelperText>);
    const element = screen.getByText('Test helper text');
    expect(element).toBeInTheDocument();
    expect(element.tagName).toBe('P');
    expect(element).toHaveClass('text-sm');
    expect(element).toHaveClass('text-muted-foreground');
  });

  it('supports custom className', () => {
    render(<HelperText className="mt-2">Custom class text</HelperText>);
    const element = screen.getByText('Custom class text');
    expect(element).toHaveClass('text-sm');
    expect(element).toHaveClass('text-muted-foreground');
    expect(element).toHaveClass('mt-2');
  });

  it('passes through other HTML attributes', () => {
    render(<HelperText data-testid="custom-helper">Attributed text</HelperText>);
    const element = screen.getByTestId('custom-helper');
    expect(element).toBeInTheDocument();
  });
});
