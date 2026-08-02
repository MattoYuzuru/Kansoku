import { Component, type ErrorInfo, type ReactNode } from "react";

export interface ErrorFallbackProps {
  retry: () => void;
}

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback: (props: ErrorFallbackProps) => ReactNode;
  resetKey?: string;
}

interface ErrorBoundaryState {
  failed: boolean;
}

/**
 * Contains render and lazy-chunk failures without retaining or presenting the
 * thrown value. Query failures are handled by QueryErrorState at page level.
 */
export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): ErrorBoundaryState {
    return { failed: true };
  }

  componentDidCatch(_error: unknown, _info: ErrorInfo) {
    // Deliberately do not log thrown values: render errors may contain local
    // identifiers, and Kansoku's default contract forbids persisting them.
  }

  componentDidUpdate(previous: ErrorBoundaryProps) {
    if (this.state.failed && previous.resetKey !== this.props.resetKey) {
      this.setState({ failed: false });
    }
  }

  private retry = () => this.setState({ failed: false });

  render() {
    return this.state.failed
      ? this.props.fallback({ retry: this.retry })
      : this.props.children;
  }
}
