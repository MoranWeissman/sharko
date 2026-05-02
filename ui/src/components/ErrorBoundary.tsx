import { Component, type ErrorInfo, type ReactNode } from 'react';
import { ErrorFallback } from './ErrorFallback';

interface ErrorBoundaryProps {
  children: ReactNode;
  /**
   * Optional render override. Defaults to <ErrorFallback> which shows the
   * Sharko-themed "Something went wrong" panel with a Try Again button.
   */
  fallback?: (error: Error, reset: () => void) => ReactNode;
  /**
   * Called whenever the boundary catches an error. Useful for logging or
   * surfacing telemetry in tests.
   */
  onError?: (error: Error, info: ErrorInfo) => void;
}

interface ErrorBoundaryState {
  error: Error | null;
}

/**
 * Catches render-phase errors thrown by descendant components and renders a
 * recoverable fallback instead of the stock React white screen.
 *
 * Added in V124-2.3 so a backend transient (e.g. /clusters returning 500)
 * that surfaces as an unhandled render error never leaves the page blank —
 * the user always sees the friendly fallback with a retry button.
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null };

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    if (this.props.onError) {
      this.props.onError(error, info);
    } else if (typeof console !== 'undefined') {
      // eslint-disable-next-line no-console
      console.error('ErrorBoundary caught:', error, info);
    }
  }

  reset = (): void => {
    this.setState({ error: null });
  };

  render(): ReactNode {
    if (this.state.error) {
      if (this.props.fallback) {
        return this.props.fallback(this.state.error, this.reset);
      }
      return <ErrorFallback error={this.state.error} resetErrorBoundary={this.reset} />;
    }
    return this.props.children;
  }
}
