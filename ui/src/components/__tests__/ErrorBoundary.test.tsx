import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import React, { useState } from 'react';
import { ErrorBoundary } from '@/components/ErrorBoundary';

function Boom(): React.ReactElement {
  throw new Error('kapow');
}

function ConditionalBoom({ shouldThrow }: { shouldThrow: boolean }): React.ReactElement {
  if (shouldThrow) {
    throw new Error('toggleable');
  }
  return <div>recovered</div>;
}

function HarnessedBoundary(): React.ReactElement {
  const [shouldThrow, setShouldThrow] = useState(true);
  return (
    <>
      <button type="button" onClick={() => setShouldThrow(false)}>
        disarm
      </button>
      <ErrorBoundary>
        <ConditionalBoom shouldThrow={shouldThrow} />
      </ErrorBoundary>
    </>
  );
}

describe('ErrorBoundary', () => {
  // Suppress the noisy React error log for these tests — we know the throw is
  // intentional and the boundary is the unit under test.
  let consoleSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    consoleSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
  });

  afterEach(() => {
    consoleSpy.mockRestore();
  });

  it('renders children when no error is thrown', () => {
    render(
      <ErrorBoundary>
        <div>safe</div>
      </ErrorBoundary>,
    );
    expect(screen.getByText('safe')).toBeInTheDocument();
  });

  it('renders the default ErrorFallback when a child throws', () => {
    render(
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
    expect(screen.getByText('kapow')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /try again/i })).toBeInTheDocument();
  });

  it('invokes onError callback when a child throws', () => {
    const onError = vi.fn();
    render(
      <ErrorBoundary onError={onError}>
        <Boom />
      </ErrorBoundary>,
    );
    expect(onError).toHaveBeenCalled();
    expect(onError.mock.calls[0][0]).toBeInstanceOf(Error);
    expect((onError.mock.calls[0][0] as Error).message).toBe('kapow');
  });

  it('reset clears the error and re-renders children', () => {
    render(<HarnessedBoundary />);
    // First render: child throws, fallback shows the error message.
    expect(screen.getByText('toggleable')).toBeInTheDocument();
    // Disarm the child so the next render does not throw, then click Try
    // Again to reset the boundary's error state.
    fireEvent.click(screen.getByRole('button', { name: 'disarm' }));
    fireEvent.click(screen.getByRole('button', { name: /try again/i }));
    expect(screen.getByText('recovered')).toBeInTheDocument();
  });

  it('renders a custom fallback when one is supplied', () => {
    const fallback = (err: Error, reset: () => void): React.ReactElement => (
      <div>
        <span>custom: {err.message}</span>
        <button type="button" onClick={reset}>reset</button>
      </div>
    );
    render(
      <ErrorBoundary fallback={fallback}>
        <Boom />
      </ErrorBoundary>,
    );
    expect(screen.getByText('custom: kapow')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'reset' })).toBeInTheDocument();
  });
});
